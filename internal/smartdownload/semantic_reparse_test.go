package smartdownload

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReparseCertifiedUsesOnlyCertifiedArtifactAndImmutableRange(t *testing.T) {
	base := `E:\database\clickhouse\tmp`
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "semantic-reparse-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	writer := &fakeIndexedWriter{result: IndexedWriteResult{InputRows: 1, InsertedRows: 1}}
	svc.SetIndexedWriter(writer)
	artifact := filepath.Join(root, "certified.parquet")
	if err := os.WriteFile(artifact, []byte("certified-test-artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(root, "smart_download", "registry.json")
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal([]*IndexedResult{{DatasetJobID: "certified_ds", ChainKey: "bsc", ChainID: 56,
		Dataset: DatasetTransactions, Address: "0x1111111111111111111111111111111111111111", FromBlock: 100, ToBlock: 199,
		MergedParquet: artifact, Certification: "CERTIFIED"}})
	if err := os.WriteFile(registryPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	var completed, total, last uint64
	err = svc.ReparseCertified(context.Background(), "bsc", DatasetTransactions, 120, 130, "parser-v3",
		func(c, n, b uint64) error { completed, total, last = c, n, b; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if writer.calls != 1 || len(writer.reqs) != 1 {
		t.Fatalf("writer calls=%d requests=%d", writer.calls, len(writer.reqs))
	}
	req := writer.reqs[0]
	if req.FromBlock != 120 || req.ToBlock != 130 || req.ParserVersion != "parser-v3" || req.SourceProvider != "CERTIFIED_REPARSE" || req.MergedParquet != artifact {
		t.Fatalf("request=%+v", req)
	}
	if completed != 11 || total != 11 || last != 130 {
		t.Fatalf("progress completed=%d total=%d last=%d", completed, total, last)
	}
}
