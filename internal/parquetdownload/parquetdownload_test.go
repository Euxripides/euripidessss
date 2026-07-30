package parquetdownload

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/etl/backend/internal/analysis/duckdb"
)

func TestNormalizeAddresses(t *testing.T) {
	valid := "0x1111111111111111111111111111111111111111"
	summary := normalizeAddresses(strings.Join([]string{
		valid,
		strings.ToUpper(valid[:2]) + valid[2:],
		"not-an-address",
	}, "\n"))
	if summary.Input != 3 || summary.Valid != 1 || summary.Invalid != 1 || summary.Duplicates != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.Addresses[0] != valid {
		t.Fatalf("address not normalized: %s", summary.Addresses[0])
	}
}

func TestValidateSettingsRejectsSystemDrive(t *testing.T) {
	settings := defaultSettings(`E:\codex\etl`)
	settings.DataRoot = `C:\bsc_analytics`
	if _, err := validateSettings(settings); err == nil {
		t.Fatal("expected C drive rejection")
	}
	settings.DataRoot = `D:\bsc_analytics`
	if _, err := validateSettings(settings); err != nil {
		t.Fatalf("expected D drive to pass: %v", err)
	}
}

func TestDiscovererListsDatePartition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.URL.Query().Get("prefix"); got != "v1.1/bnb/transactions/" {
			t.Fatalf("unexpected prefix: %s", got)
		}
		if got := request.URL.Query().Get("start-after"); got != "v1.1/bnb/transactions/date=2026-07-28/" {
			t.Fatalf("unexpected start-after: %s", got)
		}
		writer.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(writer, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <IsTruncated>false</IsTruncated>
  <Contents>
    <Key>v1.1/bnb/transactions/date=2026-07-28/transactions.parquet</Key>
    <LastModified>2026-07-29T00:00:00.000Z</LastModified>
    <ETag>"abc"</ETag>
    <Size>1234</Size>
  </Contents>
</ListBucketResult>`)
	}))
	defer server.Close()
	discovery := newDiscoverer(server.Client())
	discovery.endpoint = server.URL
	files, err := discovery.discover(context.Background(), "bsc", "2026-07-28", "2026-07-28")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].SizeBytes != 1234 || files[0].ETag != "abc" {
		t.Fatalf("unexpected files: %+v", files)
	}
}

func TestDownloadSourceResumesPartial(t *testing.T) {
	payload := append([]byte("PAR1"), make([]byte, 64*1024)...)
	payload = append(payload, []byte("PAR1")...)
	var sawRange bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start := 0
		if header := request.Header.Get("Range"); header != "" {
			sawRange = true
			fmt.Sscanf(header, "bytes=%d-", &start)
			writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
			writer.WriteHeader(http.StatusPartialContent)
		}
		_, _ = writer.Write(payload[start:])
	}))
	defer server.Close()

	source := SourceObject{Key: "sample.parquet", SizeBytes: int64(len(payload))}
	root := repositoryRoot(t)
	testRoot := filepath.Join(root, "backend", "data", "crypto_parquet", "test-download")
	_ = os.RemoveAll(testRoot)
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	if err := os.MkdirAll(testRoot, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(testRoot, "sample.parquet")
	if err := os.WriteFile(path+".partial", payload[:4096], 0644); err != nil {
		t.Fatal(err)
	}
	err := downloadSourceFromURL(context.Background(), server.Client(), server.URL, source, path, func(int64) {})
	if err != nil {
		t.Fatal(err)
	}
	if !sawRange {
		t.Fatal("expected Range resume request")
	}
	if info, err := os.Stat(path); err != nil || info.Size() != int64(len(payload)) {
		t.Fatalf("unexpected downloaded file: info=%v err=%v", info, err)
	}
}

func TestProcessSourceWithLocalDuckDB(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("bundled DuckDB executable is Windows-only")
	}
	root := repositoryRoot(t)
	exe := filepath.Join(root, "tools", "duckdb", "duckdb.exe")
	if _, err := os.Stat(exe); err != nil {
		t.Skip("bundled DuckDB executable not available")
	}
	testRoot := filepath.Join(root, "backend", "data", "crypto_parquet", "test-process")
	_ = os.RemoveAll(testRoot)
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	if err := os.MkdirAll(testRoot, 0755); err != nil {
		t.Fatal(err)
	}
	engine := duckdb.Open(root, testRoot, duckdb.AnalyticsConfig{DuckDBPath: exe, DuckDBDatabase: filepath.Join(testRoot, "test.duckdb")})
	sourcePath := filepath.Join(testRoot, "source.parquet")
	targetPath := filepath.Join(testRoot, "targets.csv")
	address := "0x1111111111111111111111111111111111111111"
	createSQL := `COPY (
SELECT * FROM (VALUES
 ('0xaaa', 1::BIGINT, '0xblock', 100::BIGINT, 0::INTEGER, '` + address + `', '0x2222222222222222222222222222222222222222', '1000000000000000000', 21000::BIGINT, 1::BIGINT, '0x', 1785196935::BIGINT, NULL::BIGINT, NULL::BIGINT, 0::INTEGER, NULL::BIGINT, NULL::VARCHAR, DATE '2026-07-28'),
 ('0xbbb', 2::BIGINT, '0xblock', 101::BIGINT, 1::INTEGER, '0x3333333333333333333333333333333333333333', '0x4444444444444444444444444444444444444444', '0', 21000::BIGINT, 1::BIGINT, '0x', 1785196936::BIGINT, NULL::BIGINT, NULL::BIGINT, 0::INTEGER, NULL::BIGINT, NULL::VARCHAR, DATE '2026-07-28')
) AS t(hash, nonce, block_hash, block_number, transaction_index, from_address, to_address, value, gas, gas_price, input, block_timestamp, max_fee_per_gas, max_priority_fee_per_gas, transaction_type, max_fee_per_blob_gas, blob_versioned_hashes, date)
) TO ` + sqlString(sourcePath) + ` (FORMAT PARQUET)`
	if output, err := engine.ExecSQL(context.Background(), createSQL); err != nil {
		t.Fatalf("create source: %v %s", err, output)
	}
	if err := writeTargetAddresses(targetPath, []string{address}); err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings(root)
	settings.DataRoot = testRoot
	manager := &Manager{engine: engine}
	outcome, err := manager.processSource(
		context.Background(),
		settings,
		targetPath,
		SourceObject{SourceDate: "2026-07-28", ETag: "test"},
		sourcePath,
		addressBatchHash([]string{address}),
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.SourceRows != 2 || outcome.Matched != 1 {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if err := verifyParquetFile(outcome.OutputPath); err != nil {
		t.Fatalf("invalid output parquet: %v", err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("repository root not found")
		}
		current = parent
	}
}
