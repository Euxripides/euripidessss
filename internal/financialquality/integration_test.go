package financialquality

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
)

func TestRepositoryClickHouseIntegration(t *testing.T) {
	if os.Getenv("CLICKHOUSE_FINANCIAL_QUALITY_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_FINANCIAL_QUALITY_INTEGRATION=1")
	}
	port, _ := strconv.Atoi(os.Getenv("CLICKHOUSE_HTTP_PORT"))
	if port == 0 {
		port = 8123
	}
	client, err := clickhouse.New(clickhouse.Config{
		Enabled: true, Host: envOr("CLICKHOUSE_HOST", "127.0.0.1"), HTTPPort: port,
		Database: envOr("CLICKHOUSE_DATABASE", "onchain"), User: envOr("CLICKHOUSE_USER", "etl_app"),
		Password: os.Getenv("CLICKHOUSE_PASSWORD"), RequestTimeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	report, err := NewRepository(client).Report(ctx, 56, Filter{Window: "ALL"})
	if err != nil {
		t.Fatal(err)
	}
	if report.ChainID != 56 || report.Window.Name != "ALL" {
		t.Fatalf("unexpected report identity: %+v", report)
	}
	if report.CostBasis.Coverage.Percentage != nil || report.CostBasis.Coverage.Available {
		t.Fatalf("cost basis must remain explicitly unavailable: %+v", report.CostBasis)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
