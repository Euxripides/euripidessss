package graphincrement

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/datasetsync"
)

func testDuckDB(t *testing.T, dataDir string) *duckdb.Engine {
	t.Helper()
	engine := duckdb.Open(`E:\codex\etl`, dataDir, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Skip("duckdb.exe 不可用")
	}
	return engine
}

func writeParquet(t *testing.T, engine *duckdb.Engine, csvPath, parquetPath string) {
	t.Helper()
	sql := fmt.Sprintf(
		"COPY (SELECT * FROM read_csv_auto('%s')) TO '%s' (FORMAT PARQUET, COMPRESSION GZIP)",
		filepath.ToSlash(csvPath), filepath.ToSlash(parquetPath))
	if _, err := engine.ExecSQL(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementerDedupeAndStatus(t *testing.T) {
	root := t.TempDir()
	engine := testDuckDB(t, filepath.Join(root, "data"))
	inc, err := NewIncrementer(engine, filepath.Join(root, "graph_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	dir1 := filepath.Join(root, "c1")
	_ = os.MkdirAll(filepath.Join(dir1, "token_transfers"), 0o755)
	csv1 := filepath.Join(root, "a.csv")
	p1 := filepath.Join(dir1, "token_transfers", "a.parquet")
	_ = os.WriteFile(csv1, []byte("chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n"+
		"56,1,1700000000,0xtx1,0,0xtoken,0xaaa1,0xbbb1,100\n"+
		"56,2,1700000100,0xtx2,0,0xtoken,0xccc1,0xddd1,200\n"), 0o644)
	writeParquet(t, engine, csv1, p1)

	dir2 := filepath.Join(root, "c2")
	_ = os.MkdirAll(filepath.Join(dir2, "token_transfers"), 0o755)
	csv2 := filepath.Join(root, "b.csv")
	p2 := filepath.Join(dir2, "token_transfers", "b.parquet")
	_ = os.WriteFile(csv2, []byte("chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n"+
		"56,1,1700000000,0xtx1,0,0xtoken,0xaaa1,0xbbb1,100\n"+ // 重复边
		"56,3,1700000200,0xtx3,0,0xtoken,0xeee1,0xfff1,300\n"), 0o644)
	writeParquet(t, engine, csv2, p2)

	e1 := &datasetsync.Entry{ChunkKey: "job1/chunk1", LocalDir: dir1, ChainKey: "bsc", Dataset: "token_transfer"}
	e2 := &datasetsync.Entry{ChunkKey: "job2/chunk1", LocalDir: dir2, ChainKey: "bsc", Dataset: "token_transfer"}

	r1, err := inc.Apply(ctx, e1)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Applied || r1.Edges != 2 {
		t.Fatalf("apply1 = %+v, want edges=2", r1)
	}
	r1b, _ := inc.Apply(ctx, e1)
	if r1b.Applied {
		t.Fatal("same chunk must not re-apply")
	}
	r2, err := inc.Apply(ctx, e2)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Edges != 3 {
		t.Fatalf("apply2 edges = %d, want 3 (duplicate edge skipped)", r2.Edges)
	}
	st := inc.Status()
	if st.Status != "GRAPH_READY" || st.NodeCount != 6 || st.EdgeCount != 3 {
		t.Fatalf("status = %+v, want GRAPH_READY nodes=6 edges=3", st)
	}
}
