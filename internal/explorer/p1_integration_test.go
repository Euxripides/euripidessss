package explorer

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

func TestClickHouseP1ExplorerAggregatesIntegration(t *testing.T) {
	if os.Getenv("CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_INTEGRATION=1 to run against the deployed ClickHouse")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	address := fmt.Sprintf("0x%040x", uint64(time.Now().UnixNano()))
	counterparty := "0x2222222222222222222222222222222222222222"
	txHash := fmt.Sprintf("0x%064x", uint64(time.Now().UnixNano()))
	jobID := "p1_explorer_integration_" + fmt.Sprint(time.Now().UnixNano())
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		statements := []string{
			fmt.Sprintf("ALTER TABLE onchain.address_activity DELETE WHERE ingest_job_id='%s' SETTINGS mutations_sync=2", jobID),
			fmt.Sprintf("ALTER TABLE onchain.address_summary DELETE WHERE chain_id=56 AND address='%s' SETTINGS mutations_sync=2", address),
			fmt.Sprintf("ALTER TABLE onchain.address_counterparty_stats DELETE WHERE chain_id=56 AND address='%s' SETTINGS mutations_sync=2", address),
			fmt.Sprintf("ALTER TABLE onchain.address_daily_stats DELETE WHERE chain_id=56 AND address='%s' SETTINGS mutations_sync=2", address),
		}
		for _, statement := range statements {
			_ = client.Exec(cleanup, statement)
		}
	}()

	insert := fmt.Sprintf(`INSERT INTO onchain.address_activity
(chain_id,address,counterparty_address,direction,activity_type,block_number,block_time,tx_hash,event_index,
token_address,token_symbol,amount,usd_value,method_id,method_name,status,counterparty_entity_type,
counterparty_label,source_provider,ingest_job_id,source_range_id)
VALUES (56,'%s','%s','OUT','NATIVE_TRANSFER',999999998,now64(3),'%s','tx:7','','',2,3.5,'','','SUCCESS','','','integration_test','%s','range')`, address, counterparty, txHash, jobID)
	if err := client.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(client)
	for attempt := 0; attempt < 2; attempt++ {
		if err := repo.RefreshAddressAnalytics(ctx, 56, address); err != nil {
			for index, query := range addressRefreshQueries(56, address) {
				if queryErr := client.Exec(ctx, query); queryErr != nil {
					t.Fatalf("refresh %d statement %d: %v", attempt+1, index+1, queryErr)
				}
			}
			t.Fatalf("refresh %d: %v", attempt+1, err)
		}
	}
	summary, err := repo.GetAddressSummary(ctx, 56, address)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TransactionCount != 1 || summary.OutgoingTransactionCount != 1 || summary.NativeSent != "2" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	counterparties, err := repo.GetCounterpartyStats(ctx, 56, address, 10)
	if err != nil || len(counterparties) != 1 || counterparties[0].TransactionCount != 1 {
		t.Fatalf("unexpected counterparties: %+v err=%v", counterparties, err)
	}
	daily, err := repo.GetDailyStats(ctx, DailyStatsQuery{ChainID: 56, Address: address, Limit: 10})
	if err != nil || len(daily) != 1 || daily[0].OutgoingCount != 1 {
		t.Fatalf("unexpected daily stats: %+v err=%v", daily, err)
	}
}
