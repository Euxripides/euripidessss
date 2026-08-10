package financialpnl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
)

func TestFinancialPnLClickHouseIntegration(t *testing.T) {
	if os.Getenv("FINANCIAL_PNL_INTEGRATION") != "1" {
		t.Skip("set FINANCIAL_PNL_INTEGRATION=1")
	}
	port, _ := strconv.Atoi(os.Getenv("CLICKHOUSE_HTTP_PORT"))
	if port == 0 {
		port = 8123
	}
	client, err := clickhouse.New(clickhouse.Config{Enabled: true, Host: env("CLICKHOUSE_HOST", "127.0.0.1"), HTTPPort: port,
		Database: env("CLICKHOUSE_DATABASE", "onchain"), User: env("CLICKHOUSE_USER", "etl_app"), Password: os.Getenv("CLICKHOUSE_PASSWORD"), RequestTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = client.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	seed := sha256.Sum256([]byte(fmt.Sprintf("pnl-%d", time.Now().UnixNano())))
	hexSeed := hex.EncodeToString(seed[:])
	address, token := "0x"+hexSeed[:40], "0x"+hexSeed[24:64]
	otherToken := "0x" + hexSeed[12:52]
	buyHash, transferHash, sellHash := "0x"+hexSeed, "0x"+hexSeed[1:]+hexSeed[:1], "0x"+hexSeed[2:]+hexSeed[:2]
	jobID := "integration-" + hexSeed[:12]
	now := time.Now().UTC().Truncate(time.Millisecond)
	defer func() {
		for _, query := range []string{
			fmt.Sprintf("ALTER TABLE onchain.financial_position_events DELETE WHERE ingest_job_id='%s' SETTINGS mutations_sync=1", jobID),
			fmt.Sprintf("ALTER TABLE onchain.token_prices DELETE WHERE chain_id=56 AND token_address='%s' SETTINGS mutations_sync=1", token),
			fmt.Sprintf("ALTER TABLE onchain.token_position_lots DELETE WHERE chain_id=56 AND address='%s' AND token_address='%s' SETTINGS mutations_sync=1", address, token),
			fmt.Sprintf("ALTER TABLE onchain.financial_pnl_snapshots DELETE WHERE chain_id=56 AND address='%s' AND token_address='%s' SETTINGS mutations_sync=1", address, token),
		} {
			_ = client.Exec(context.Background(), query)
		}
	}()

	producer := NewProducer(client)
	if err = producer.MaterializeSwaps(ctx, []CanonicalSwap{
		{ChainID: 56, Trader: address, TokenIn: otherToken, AmountIn: "100000", USDIn: "100000", TokenOut: token, AmountOut: "100", USDOut: "100000", Time: now.Add(-3 * time.Minute), BlockNumber: 1, TransactionHash: buyHash, SemanticConfidence: "VERIFIED", PriceVersion: "historical-v1", DataSnapshotVersion: "facts-v1", IngestJobID: jobID},
		{ChainID: 56, Trader: address, TokenIn: token, AmountIn: "90", USDIn: "150000", TokenOut: otherToken, AmountOut: "150000", USDOut: "150000", GasUSD: ptr("1000"), Time: now.Add(-time.Minute), BlockNumber: 3, TransactionHash: sellHash, EventIndex: 1, SemanticConfidence: "VERIFIED", PriceVersion: "historical-v1", DataSnapshotVersion: "facts-v1", IngestJobID: jobID},
	}); err != nil {
		t.Fatal(err)
	}
	if err = producer.MaterializePositionEvents(ctx, Query{ChainID: 56, Address: address, Token: token, AsOf: now}, []PositionEvent{{Time: now.Add(-2 * time.Minute), BlockNumber: 2, TransactionHash: transferHash, Type: EventTransferOut, Amount: "10", SemanticSource: "TOKEN_TRANSFER", SemanticConfidence: "HIGH", DataSnapshotVersion: "facts-v1"}}, jobID); err != nil {
		t.Fatal(err)
	}
	priceColumns := []string{"chain_id", "token_address", "timestamp_bucket", "price_usd", "source", "confidence", "observed_at", "updated_at", "price_time", "time_bucket", "resolution", "source_priority", "is_fallback", "is_verified", "price_version", "source_version", "ingested_at"}
	insertRows(t, ctx, client, "onchain.token_prices", priceColumns, [][]string{{"56", token, formatCH(now), "2000", "LOCAL_VERIFIED", "HIGH", formatCH(now), formatCH(now), formatCH(now), formatCH(now), "1m", "0", "0", "1", "current-v1", "source-v1", formatCH(now)}})

	result, snapshotID, err := NewService(NewRepository(client), 2*time.Minute).Calculate(ctx, Query{ChainID: 56, Address: address, Token: token, AsOf: now.Add(time.Second)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.RealizedPnLUSD != "59000" || result.SoldAmount != "90" || result.PositionAmount != "0" || snapshotID == "" {
		t.Fatalf("live pnl mismatch: %+v snapshot=%s", result, snapshotID)
	}
	rows, err := client.QueryJSON(ctx, fmt.Sprintf("SELECT (SELECT count() FROM onchain.financial_pnl_snapshots FINAL WHERE snapshot_id='%s') snapshots,(SELECT count() FROM onchain.token_position_lots FINAL WHERE snapshot_id='%s') lots", snapshotID, snapshotID))
	if err != nil || len(rows) != 1 || uintValue(rows[0]["snapshots"]) != 1 {
		t.Fatalf("live persistence mismatch rows=%v err=%v", rows, err)
	}
}

func insertRows(t *testing.T, ctx context.Context, client *clickhouse.Client, table string, columns []string, rows [][]string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if writer.Error() != nil {
		t.Fatal(writer.Error())
	}
	if err := client.InsertCSV(ctx, table, columns, &buffer); err != nil {
		t.Fatalf("insert %s: err=%v", table, err)
	}
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func formatCH(value time.Time) string { return value.UTC().Format("2006-01-02 15:04:05.000") }
