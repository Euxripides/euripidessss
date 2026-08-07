package analyticsapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/etl/backend/internal/analysis/duckdb"
)

// TestFlowsCloudSourceUnion Phase 5 §32-33：Cloud token_transfers parquet 联合进 Flows，
// Graph 可增量看到 Cloud 数据；缓存失效后再次查询仍返回。
func TestFlowsCloudSourceUnion(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := t.TempDir()
	engine := duckdb.Open(repoRoot, dataRoot, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Skip("duckdb.exe 不可用，跳过联合查询验证")
	}
	ctx := context.Background()

	logsCSV := filepath.Join(dataRoot, "logs.csv")
	logsParquet := filepath.Join(dataRoot, "logs.parquet")
	logs := "address,topic0,topic1,topic2,data,block_number,transaction_hash\n" +
		"0xtoken,0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef,0xbbb,0xaaa,0x0000000000000000000000000000000000000000000000000000000000000001,1,0xtx1\n"
	if err := os.WriteFile(logsCSV, []byte(logs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecSQL(ctx, fmt.Sprintf(
		"COPY (SELECT * FROM read_csv_auto('%s', all_varchar=true)) TO '%s' (FORMAT PARQUET)",
		filepath.ToSlash(logsCSV), filepath.ToSlash(logsParquet),
	)); err != nil {
		t.Fatalf("create logs parquet: %v", err)
	}

	cloudCSV := filepath.Join(dataRoot, "cloud.csv")
	cloudParquet := filepath.Join(dataRoot, "cloud.parquet")
	cloud := "chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n" +
		"56,2,1700000000,0xtx2,0,0xtoken,0xbbb,0xaaa,123\n"
	if err := os.WriteFile(cloudCSV, []byte(cloud), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecSQL(ctx, fmt.Sprintf(
		"COPY (SELECT * FROM read_csv_auto('%s', all_varchar=true)) TO '%s' (FORMAT PARQUET)",
		filepath.ToSlash(cloudCSV), filepath.ToSlash(cloudParquet),
	)); err != nil {
		t.Fatalf("create cloud parquet: %v", err)
	}

	svc := New(engine, logsParquet)
	svc.AddFlowSource(cloudParquet)
	edges, err := svc.Flows(ctx, "0xaaa", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("Flows(0xaaa) = %d edges, want 2（logs 1 + cloud 1）: %+v", len(edges), edges)
	}
	hasCloud := false
	for _, e := range edges {
		if e.TxHash == "0xtx2" && e.Amount == "123" {
			hasCloud = true
		}
	}
	if !hasCloud {
		t.Fatalf("cloud edge missing: %+v", edges)
	}

	svc.InvalidateCache()
	edges2, err := svc.Flows(ctx, "0xaaa", "")
	if err != nil || len(edges2) != 2 {
		t.Fatalf("Flows after invalidate = %d, %v", len(edges2), err)
	}
}
