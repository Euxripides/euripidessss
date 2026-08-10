package pricing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

func applyIntegrationSchema(t *testing.T, client *clickhouse.Client, path string) {
	t.Helper()
	schema, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, statement := range strings.Split(string(schema), ";") {
		lines := make([]string, 0)
		for _, line := range strings.Split(statement, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				lines = append(lines, line)
			}
		}
		if query := strings.TrimSpace(strings.Join(lines, "\n")); query != "" {
			if err = client.Exec(ctx, query); err != nil {
				t.Fatalf("migration failed: %v", err)
			}
		}
	}
}

func TestClickHousePriceResolverIntegration(t *testing.T) {
	if os.Getenv("CLICKHOUSE_PRICING_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_PRICING_INTEGRATION=1")
	}
	cfg := config.Load().ClickHouse
	cfg.Enabled = true
	client, err := clickhouse.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err = client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join("..", "..", "deploy", "clickhouse", "financial_schema.sql")
	applyIntegrationSchema(t, client, schemaPath)
	seed := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	token := "0x" + hex.EncodeToString(seed[:20])
	priceTime := time.Date(2025, 1, 1, 12, 1, 0, 0, time.UTC)
	defer client.Exec(context.Background(), "ALTER TABLE onchain.token_prices DELETE WHERE chain_id = 56 AND token_address = '"+token+"' SETTINGS mutations_sync=1")
	repository := NewRepository(client)
	if err = repository.PutPrices(ctx, []HistoricalPrice{{ChainID: 56, TokenID: token, PriceTime: priceTime, PriceUSD: "2", Source: "LOCAL_VERIFIED", Confidence: "HIGH", Resolution: Resolution1Minute, IsVerified: true, PriceVersion: "integration-v1", SourceVersion: "fixture-v1"}}); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(repository, ResolverOptions{Now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }})
	price, err := resolver.ResolvePrice(ctx, 56, token, priceTime.Add(37*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if price.PriceUSD != "2" || price.Source != "LOCAL_VERIFIED" || price.Distance != 37*time.Second || !price.IsVerified {
		t.Fatalf("unexpected live resolved price: %+v", price)
	}
	// Apply the same migration a second time to prove idempotency.
	applyIntegrationSchema(t, client, schemaPath)
}

func TestClickHouseCanonicalSwapIntegration(t *testing.T) {
	if os.Getenv("CLICKHOUSE_PRICING_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_PRICING_INTEGRATION=1")
	}
	cfg := config.Load().ClickHouse
	cfg.Enabled = true
	client, err := clickhouse.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err = client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	applyIntegrationSchema(t, client, filepath.Join("..", "..", "deploy", "clickhouse", "price_engine_schema.sql"))
	seed := sha256.Sum256([]byte("canonical-swap-" + time.Now().UTC().Format(time.RFC3339Nano)))
	pool := "0x" + hex.EncodeToString(seed[:20])
	token0Seed := sha256.Sum256([]byte("token0-" + pool))
	token1Seed := sha256.Sum256([]byte("token1-" + pool))
	token0 := "0x" + hex.EncodeToString(token0Seed[:20])
	token1 := "0x" + hex.EncodeToString(token1Seed[:20])
	txHash := "0x" + hex.EncodeToString(seed[:])
	defer client.Exec(context.Background(), fmt.Sprintf("ALTER TABLE onchain.dex_swaps DELETE WHERE chain_id=56 AND tx_hash='%s' SETTINGS mutations_sync=2", txHash))
	swap := NormalizedSwap{ChainID: 56, BlockNumber: 1, BlockTime: time.Now().UTC(), TxHash: txHash, LogIndex: 7, DEX: "PANCAKESWAP", Version: "V2", ProtocolID: "pancakeswap_v2", PoolAddress: pool, Token0: token0, Token1: token1, Amount0Raw: big.NewInt(100_000_000), Amount1Raw: big.NewInt(-200_000_000), Amount0: big.NewRat(100, 1), Amount1: big.NewRat(-200, 1), Liquidity: big.NewInt(0), SqrtPriceX96: big.NewInt(0), Source: "CODEX_INTEGRATION", SourceJobID: "canonical-swap-test"}
	if err = NewDEXRepository(client).PutSwaps(ctx, []NormalizedSwap{swap}); err != nil {
		t.Fatal(err)
	}
	rows, err := client.QueryJSON(ctx, fmt.Sprintf(`SELECT protocol_id,token_in_address,toString(amount_in) amount_in,token_out_address,toString(amount_out) amount_out,toString(price_token0_token1) price_token0_token1,dataset FROM onchain.dex_swaps FINAL WHERE chain_id=56 AND tx_hash='%s' AND log_index=7`, txHash))
	if err != nil || len(rows) != 1 {
		t.Fatalf("canonical swap query rows=%d err=%v", len(rows), err)
	}
	row := rows[0]
	if text(row["protocol_id"]) != "pancakeswap_v2" || text(row["token_in_address"]) != token0 || text(row["token_out_address"]) != token1 || text(row["amount_in"]) != "100" || text(row["amount_out"]) != "200" || text(row["price_token0_token1"]) != "2" || text(row["dataset"]) != datasetDEXSwap {
		t.Fatalf("unexpected canonical swap row: %+v", row)
	}
}
