package clickhouseinvestigation_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/clickhouseinvestigation"
	"github.com/etl/backend/internal/config"
)

// This opt-in test validates ClickHouse SQL syntax against the local deployed
// schema without requiring any fixture rows.
func TestLocalClickHouseQueries(t *testing.T) {
	if os.Getenv("CLICKHOUSE_INVESTIGATION_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_INVESTIGATION_INTEGRATION=1 for local ClickHouse validation")
	}
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := clickhouseinvestigation.New(client, 56)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	address := "0x000000000000000000000000000000000000dead"
	if _, err := repo.Profile(ctx, address); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddressStats(ctx, address, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DiscoverRelations(ctx, []string{address}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddressEvidence(ctx, address, 10); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := repo.ExportCSV(ctx, &output, clickhouseinvestigation.ExportRequest{Dataset: "activity", Address: address, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if output.Len() == 0 {
		t.Fatal("expected CSV header")
	}
}
