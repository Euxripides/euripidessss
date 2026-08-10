package clickhouse

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/config"
)

func TestClickHouseQueryPerformanceMatrix(t *testing.T) {
	if os.Getenv("CLICKHOUSE_QUERY_BENCHMARK") != "1" {
		t.Skip("set CLICKHOUSE_QUERY_BENCHMARK=1 to run 1M/10M/50M query matrix")
	}
	sizes := []int64{1_000_000, 10_000_000, 50_000_000}
	if raw := strings.TrimSpace(os.Getenv("CLICKHOUSE_QUERY_BENCHMARK_SIZES")); raw != "" {
		sizes = nil
		for _, part := range strings.Split(raw, ",") {
			n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil || n <= 0 || n > 50_000_000 {
				t.Fatalf("invalid query benchmark size %q", part)
			}
			sizes = append(sizes, n)
		}
	}
	client, err := New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	for _, statement := range []string{
		"DROP TABLE IF EXISTS onchain.address_activity_benchmark SYNC",
		`CREATE TABLE onchain.address_activity_benchmark
(
 chain_id UInt32, address String, counterparty_address String, direction LowCardinality(String),
 activity_type LowCardinality(String), block_number UInt64, block_time DateTime64(3, 'UTC'),
 tx_hash String, event_index String, token_address String, amount Decimal256(38), ingested_at DateTime64(3, 'UTC')
) ENGINE=MergeTree PARTITION BY toYYYYMM(block_time)
ORDER BY (chain_id,address,block_time,block_number,tx_hash,event_index)`,
	} {
		if err := client.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Minute)
		defer stop()
		_ = client.Exec(cleanup, "DROP TABLE IF EXISTS onchain.address_activity_benchmark SYNC")
	}()
	address := "0x7777777777777777777777777777777777777777"
	var populated int64
	for _, size := range sizes {
		if size > populated {
			insert := fmt.Sprintf(`INSERT INTO onchain.address_activity_benchmark
SELECT 56, '%s', concat('0x', leftPad(toString(number %% 1000), 40, '0')),
if(number %% 2=0,'IN','OUT'), if(number %% 3=0,'TOKEN_TRANSFER','NATIVE_TRANSFER'),
number, now64(3)-toIntervalSecond(number), concat('0x', lower(hex(SHA256(toString(number))))),
concat('event:',toString(number)), if(number %% 3=0,'0x6666666666666666666666666666666666666666',''),
toDecimal256(number %% 1000000,38), now64(3)
FROM numbers(%d,%d)`, address, populated, size-populated)
			started := time.Now()
			if err := client.Exec(ctx, insert); err != nil {
				t.Fatal(err)
			}
			t.Logf("scale=%d populate_latency=%s", size, time.Since(started))
			populated = size
		}
		queries := map[string]string{
			"address_tx_first_page": fmt.Sprintf("SELECT * FROM onchain.address_activity_benchmark WHERE chain_id=56 AND address='%s' AND activity_type='NATIVE_TRANSFER' ORDER BY block_time DESC,block_number DESC,tx_hash DESC,event_index DESC LIMIT 50", address),
			"token_first_page":      fmt.Sprintf("SELECT * FROM onchain.address_activity_benchmark WHERE chain_id=56 AND address='%s' AND activity_type='TOKEN_TRANSFER' ORDER BY block_time DESC,block_number DESC,tx_hash DESC,event_index DESC LIMIT 50", address),
			"address_summary":       fmt.Sprintf("SELECT uniqExact(tx_hash),sumIf(amount,direction='IN'),sumIf(amount,direction='OUT'),uniqExact(counterparty_address),min(block_time),max(block_time) FROM onchain.address_activity_benchmark WHERE chain_id=56 AND address='%s'", address),
			"top_counterparty":      fmt.Sprintf("SELECT counterparty_address,count() n FROM onchain.address_activity_benchmark WHERE chain_id=56 AND address='%s' GROUP BY counterparty_address ORDER BY n DESC,counterparty_address LIMIT 20", address),
			"daily_30d":             fmt.Sprintf("SELECT toDate(block_time),sumIf(amount,direction='IN'),sumIf(amount,direction='OUT') FROM onchain.address_activity_benchmark WHERE chain_id=56 AND address='%s' AND block_time>=now()-INTERVAL 30 DAY GROUP BY toDate(block_time) ORDER BY toDate(block_time)", address),
			"all_time_stats":        fmt.Sprintf("SELECT activity_type,direction,count(),sum(amount) FROM onchain.address_activity_benchmark WHERE chain_id=56 AND address='%s' GROUP BY activity_type,direction ORDER BY activity_type,direction", address),
		}
		for name, query := range queries {
			started := time.Now()
			rows, err := client.QueryJSON(ctx, query)
			if err != nil {
				t.Fatalf("scale=%d query=%s: %v", size, name, err)
			}
			t.Logf("scale=%d query=%s latency=%s result_rows=%d", size, name, time.Since(started), len(rows))
		}
	}
}
