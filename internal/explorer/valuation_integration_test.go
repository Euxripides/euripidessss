package explorer

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

func TestHistoricalUSDTValuationIntegration(t *testing.T) {
	if os.Getenv("CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_INTEGRATION=1 to run against deployed ClickHouse")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	nonce := uint64(time.Now().UnixNano())
	address := fmt.Sprintf("0x%040x", nonce)
	token := fmt.Sprintf("0x%040x", nonce+1)
	unknown := fmt.Sprintf("0x%040x", nonce+2)
	jobID := fmt.Sprintf("valuation_acceptance_%d", nonce)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		for _, statement := range []string{
			fmt.Sprintf("ALTER TABLE onchain.address_activity DELETE WHERE ingest_job_id='%s' SETTINGS mutations_sync=2", jobID),
			fmt.Sprintf("ALTER TABLE onchain.token_price_1m DELETE WHERE chain_id=56 AND token_address='%s' SETTINGS mutations_sync=2", token),
			fmt.Sprintf("ALTER TABLE onchain.token_prices DELETE WHERE chain_id=56 AND token_address='%s' SETTINGS mutations_sync=2", token),
		} {
			_ = client.Exec(cleanup, statement)
		}
	}()

	if err := client.Exec(ctx, fmt.Sprintf(`INSERT INTO onchain.token_price_1m
(chain_id,token_address,minute,open,high,low,close,vwap,volume_token,volume_usd,trade_count,pool_count,liquidity_usd,price_source,confidence,is_interpolated,is_last_known,price_age_seconds,route)
VALUES (56,'%s',toDateTime64('2026-08-09 12:00:00',3,'UTC'),2,2,2,2,2,100,200,1,1,NULL,'DEX_RECONSTRUCTED',0.94,false,false,0,'TEST/USDT')`, token)); err != nil {
		t.Fatal(err)
	}
	if err := client.Exec(ctx, fmt.Sprintf(`INSERT INTO onchain.token_prices
(chain_id,token_address,timestamp_bucket,price_usd,source,confidence,observed_at)
VALUES (56,'%s',toDateTime64('2026-08-09 12:00:00',3,'UTC'),2,'DEX_RECONSTRUCTED','HIGH',now64(3))`, token)); err != nil {
		t.Fatal(err)
	}
	insert := fmt.Sprintf(`INSERT INTO onchain.address_activity
(chain_id,address,counterparty_address,direction,activity_type,block_number,block_time,tx_hash,event_index,token_address,token_symbol,amount,usd_value,price_usd,price_time,price_source,price_confidence,status,source_provider,ingest_job_id,source_range_id)
VALUES
(56,'%s','0x2222222222222222222222222222222222222222','OUT','TOKEN_TRANSFER',100,toDateTime64('2026-08-09 12:00:26',3,'UTC'),'0x%064x','log:1','0x55d398326f99059ff775485246999027b3197955','USDT',100000,NULL,NULL,NULL,'','','SUCCESS','acceptance','%s','r'),
(56,'%s','0x2222222222222222222222222222222222222222','OUT','TOKEN_TRANSFER',101,toDateTime64('2026-08-09 12:00:26',3,'UTC'),'0x%064x','log:2','%s','TEST',3,NULL,NULL,NULL,'','','SUCCESS','acceptance','%s','r'),
(56,'%s','0x2222222222222222222222222222222222222222','OUT','TOKEN_TRANSFER',102,toDateTime64('2026-08-09 12:05:26',3,'UTC'),'0x%064x','log:3','%s','TEST',3,NULL,NULL,NULL,'','','SUCCESS','acceptance','%s','r'),
(56,'%s','0x2222222222222222222222222222222222222222','OUT','TOKEN_TRANSFER',103,toDateTime64('2026-08-09 12:05:26',3,'UTC'),'0x%064x','log:4','%s','NONE',4,NULL,NULL,NULL,'','','SUCCESS','acceptance','%s','r')`,
		address, nonce+10, jobID, address, nonce+11, token, jobID, address, nonce+12, token, jobID, address, nonce+13, unknown, jobID)
	if err := client.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}

	page, err := NewRepository(client).ListActivity(ctx, ActivityQuery{ChainID: 56, Address: address, Activity: ActivityTokenTransfers, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("items=%d want=4", len(page.Items))
	}
	byEvent := map[string]Activity{}
	for _, item := range page.Items {
		byEvent[item.EventIndex] = item
	}
	equalDecimal := func(got *string, want string) bool {
		if got == nil {
			return false
		}
		gotNumber, gotOK := new(big.Rat).SetString(*got)
		wantNumber, wantOK := new(big.Rat).SetString(want)
		return gotOK && wantOK && gotNumber.Cmp(wantNumber) == 0
	}
	assertValue := func(event, price, value, priceType string, age int64) {
		t.Helper()
		item := byEvent[event]
		if !equalDecimal(item.HistoricalPriceUSDT, price) || !equalDecimal(item.HistoricalValueUSDT, value) || item.PriceType != priceType || item.PriceAgeSeconds != age || item.ValuationStatus != "VALUED" {
			t.Fatalf("event %s valuation mismatch: %+v", event, item)
		}
	}
	assertValue("log:1", "1", "100000", "PEG", 26)
	assertValue("log:2", "2", "6", "TRADED", 26)
	assertValue("log:3", "2", "6", "LAST_KNOWN", 326)
	if item := byEvent["log:4"]; item.HistoricalPriceUSDT != nil || item.HistoricalValueUSDT != nil || item.ValuationStatus != "NO_PRICE" {
		t.Fatalf("NO_PRICE must remain null: %+v", item)
	}
}

func TestHistoricalUSDTValuation100RowsPerformance(t *testing.T) {
	if os.Getenv("CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_INTEGRATION=1 to run against deployed ClickHouse")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	nonce := uint64(time.Now().UnixNano())
	address := fmt.Sprintf("0x%040x", nonce)
	jobID := fmt.Sprintf("valuation_perf_%d", nonce)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		_ = client.Exec(cleanup, fmt.Sprintf("ALTER TABLE onchain.address_activity DELETE WHERE ingest_job_id='%s' SETTINGS mutations_sync=2", jobID))
	}()
	insert := fmt.Sprintf(`INSERT INTO onchain.address_activity
(chain_id,address,counterparty_address,direction,activity_type,block_number,block_time,tx_hash,event_index,token_address,token_symbol,amount,usd_value,price_usd,price_time,price_source,price_confidence,status,source_provider,ingest_job_id,source_range_id)
SELECT 56,'%s','0x2222222222222222222222222222222222222222','OUT','TOKEN_TRANSFER',number+1000,toDateTime64('2026-08-09 12:00:00',3,'UTC')+toIntervalSecond(number),lower(concat('0x',leftPad(hex(number+1000),64,'0'))),concat('log:',toString(number)),'0x55d398326f99059ff775485246999027b3197955','USDT',toDecimal128(1000+number,18),NULL,NULL,NULL,'','','SUCCESS','acceptance','%s','r' FROM numbers(100)`, address, jobID)
	if err := client.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(client)
	durations := make([]time.Duration, 0, 20)
	for index := 0; index < 21; index++ {
		started := time.Now()
		page, queryErr := repository.ListActivity(ctx, ActivityQuery{ChainID: 56, Address: address, Activity: ActivityTokenTransfers, PageSize: 100})
		elapsed := time.Since(started)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if len(page.Items) != 100 {
			t.Fatalf("items=%d want=100", len(page.Items))
		}
		if index > 0 {
			durations = append(durations, elapsed)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p50 := durations[len(durations)/2]
	p95 := durations[(len(durations)*95+99)/100-1]
	t.Logf("100-row historical valuation latency p50=%s p95=%s queries=20", p50, p95)
	if p50 >= 100*time.Millisecond || p95 >= 250*time.Millisecond {
		t.Fatalf("performance target missed: p50=%s p95=%s", p50, p95)
	}
}
