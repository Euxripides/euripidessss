package semanticquality

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

func TestLiveSemanticQualitySQL(t *testing.T) {
	if os.Getenv("SEMANTIC_QUALITY_LIVE") != "1" {
		t.Skip("set SEMANTIC_QUALITY_LIVE=1 to query local ClickHouse")
	}
	cfg := config.Load().ClickHouse
	client, err := clickhouse.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(client)
	if _, err := repository.DataQuality(ctx, 56); err != nil {
		stub := &qualityStub{}
		_, _ = NewRepository(stub).DataQuality(ctx, 56)
		_, rawErr := client.QueryJSON(ctx, stub.queries[0])
		t.Fatalf("data quality: %v (live SQL: %v)", err, rawErr)
	}
	if _, err := repository.TokenQuality(ctx, 56); err != nil {
		t.Fatalf("token quality: %v", err)
	}
	if _, err := repository.ContractQuality(ctx, 56); err != nil {
		t.Fatalf("contract quality: %v", err)
	}
	if _, err := repository.DecoderQuality(ctx, 56); err != nil {
		t.Fatalf("decoder quality: %v", err)
	}
	if _, err := repository.PriceQuality(ctx, 56); err != nil {
		t.Fatalf("price quality: %v", err)
	}
	report, err := repository.Report(ctx, 56)
	if err != nil {
		t.Fatal(err)
	}
	if report.ChainID != 56 || len(report.Data.Datasets) != 9 {
		t.Fatalf("unexpected report: %+v", report)
	}
}
