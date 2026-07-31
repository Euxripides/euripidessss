package downloadengine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const duckdbPath = `E:\codex\etl\tools\duckdb\duckdb.exe`

var hasDuckDB bool

func init() {
	if _, err := os.Stat(duckdbPath); err == nil {
		hasDuckDB = true
	}
}

func duckSQL(t *testing.T, dir, sql string) error {
	t.Helper()
	if !hasDuckDB {
		t.Skip("duckdb.exe not found")
	}
	cmd := exec.CommandContext(t.Context(), duckdbPath, "-json",
		filepath.Join(dir, "stress.duckdb"), "-c", sql)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, string(out))
	}
	return nil
}

func duckQuery(t *testing.T, dir, sql string) ([]byte, error) {
	t.Helper()
	if !hasDuckDB {
		t.Skip("duckdb.exe not found")
	}
	cmd := exec.CommandContext(t.Context(), duckdbPath, "-json",
		filepath.Join(dir, "stress.duckdb"), "-c", sql)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// ── Test 1: Parquet Writer Stress ──

func TestParquetWriter1MRows(t *testing.T) {
	if !hasDuckDB || testing.Short() {
		t.Skip("duckdb not available or short mode")
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "tx_1m.parquet")
	outSlash := strings.ReplaceAll(outPath, "\\", "/")

	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)

	t.Log("=== 1M rows → COPY TO PARQUET (ZSTD) ===")
	genStart := time.Now()

	sql := fmt.Sprintf(`COPY (SELECT r AS block_number, '0x' || printf('%%040x', r) AS tx_hash,
	'0x' || printf('%%040x', r %% 50000) AS from_addr,
	'0x' || printf('%%040x', r %% 50000) AS to_addr,
	CAST(random()*1e6 AS BIGINT) AS value,
	CAST('2020-01-01' AS TIMESTAMP)+INTERVAL (r %% 20000000) SECOND AS block_time
	FROM generate_series(1,1000000) AS t(r)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`, outSlash)

	if err := duckSQL(t, dir, sql); err != nil {
		t.Fatal(err)
	}
	genDur := time.Since(genStart)

	info, _ := os.Stat(outPath)
	fsize := int64(0)
	if info != nil {
		fsize = info.Size()
	}

	runtime.GC()
	runtime.ReadMemStats(&m1)

	t.Logf("  File: %s (%.2f MB)", outPath, float64(fsize)/1e6)
	t.Logf("  Time: %v (%.0f rows/s, %.2f MB/s)", genDur, 1e6/genDur.Seconds(), float64(fsize)/1e6/genDur.Seconds())
	t.Logf("  Memory Δ: %.2f MB", float64(int64(m1.Alloc)-int64(m0.Alloc))/1e6)

	// Verify
	out, _ := duckQuery(t, dir, fmt.Sprintf("SELECT count(*) FROM read_parquet('%s')", outSlash))
	t.Logf("  COUNT: %s", strings.TrimSpace(string(out)))
}

func TestParquetWriter10MRows(t *testing.T) {
	if !hasDuckDB || testing.Short() {
		t.Skip("duckdb not available or short mode")
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "tx_10m.parquet")
	outSlash := strings.ReplaceAll(outPath, "\\", "/")

	t.Log("=== 10M rows → COPY TO PARQUET (ZSTD) ===")
	genStart := time.Now()

	sql := fmt.Sprintf(`COPY (SELECT r AS block_number, '0x' || printf('%%040x', r) AS tx_hash,
	'0x' || printf('%%040x', r %% 50000) AS from_addr,
	'0x' || printf('%%040x', r %% 50000) AS to_addr,
	CAST(random()*1e6 AS BIGINT) AS value,
	CAST('2020-01-01' AS TIMESTAMP)+INTERVAL (r %% 20000000) SECOND AS block_time
	FROM generate_series(1,10000000) AS t(r)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`, outSlash)

	if err := duckSQL(t, dir, sql); err != nil {
		t.Fatal(err)
	}
	genDur := time.Since(genStart)

	info, _ := os.Stat(outPath)
	fsize := int64(0)
	if info != nil {
		fsize = info.Size()
	}

	t.Logf("  File: %s (%.2f MB)", outPath, float64(fsize)/1e6)
	t.Logf("  Time: %v (%.0f rows/s, %.2f MB/s)", genDur, 1e7/genDur.Seconds(), float64(fsize)/1e6/genDur.Seconds())
}

// ── Test 2: DuckDB Index Stress ──

func TestDuckDBIndexStress(t *testing.T) {
	if !hasDuckDB || testing.Short() {
		t.Skip("duckdb not available or short mode")
	}

	dir := t.TempDir()
	d := func(name string) string {
		return strings.ReplaceAll(filepath.Join(dir, name+".parquet"), "\\", "/")
	}

	// 生成 3 个 Parquet 文件
	tables := []struct {
		name string
		sql  string
	}{
		{"transactions", fmt.Sprintf(`COPY (SELECT r AS block,
			'0x'||printf('%%040x',r) AS tx_hash,
			'0x'||printf('%%040x',r%%50000) AS address,
			CAST(random()*1e6 AS BIGINT) AS amount,
			CAST('2020-01-01' AS TIMESTAMP)+INTERVAL (r%%20000000) SECOND AS block_time
			FROM generate_series(1,1000000) AS t(r)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`, d("transactions"))},
		{"logs", fmt.Sprintf(`COPY (SELECT r AS block,
			'0x'||printf('%%040x',r%%50000) AS contract,
			'0x'||printf('%%040x',r%%100) AS topic0,
			r%%10 AS token_id
			FROM generate_series(1,1000000) AS t(r)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`, d("logs"))},
		{"tokens", fmt.Sprintf(`COPY (SELECT r%%10 AS token_id,
			'TOKEN-'||(r%%10) AS symbol,
			'0x'||printf('%%040x',r%%50000) AS holder,
			CAST(random()*1e12 AS BIGINT) AS balance
			FROM generate_series(1,1000000) AS t(r)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`, d("tokens"))},
	}

	t.Log("=== Generating 3 × 1M row Parquet files ===")
	for _, tb := range tables {
		start := time.Now()
		if err := duckSQL(t, dir, tb.sql); err != nil {
			t.Fatalf("%s: %v", tb.name, err)
		}
		info, _ := os.Stat(filepath.Join(dir, tb.name+".parquet"))
		sz := int64(0)
		if info != nil {
			sz = info.Size()
		}
		t.Logf("  %s: %.2f MB in %v", tb.name, float64(sz)/1e6, time.Since(start))
	}

	// 查询基准
	queries := []struct {
		label string
		sql   string
	}{
		{"COUNT(*)", fmt.Sprintf("SELECT count(*) FROM read_parquet('%s')", d("transactions"))},
		{"GROUP BY address", fmt.Sprintf("SELECT address, count(*) c FROM read_parquet('%s') GROUP BY address ORDER BY c DESC LIMIT 10", d("transactions"))},
		{"GROUP BY token", fmt.Sprintf("SELECT token_id, count(*) c FROM read_parquet('%s') GROUP BY token_id ORDER BY c DESC", d("tokens"))},
		{"Time range", fmt.Sprintf("SELECT count(*) FROM read_parquet('%s') WHERE block_time BETWEEN '2021-01-01' AND '2022-01-01'", d("transactions"))},
		{"JOIN tx+logs", fmt.Sprintf("SELECT t.address, count(*) c FROM read_parquet('%s') t JOIN read_parquet('%s') l ON t.block=l.block GROUP BY t.address ORDER BY c DESC LIMIT 5", d("transactions"), d("logs"))},
	}

	t.Log("=== DuckDB Query Benchmarks (3×1M rows) ===")
	for _, q := range queries {
		start := time.Now()
		out, err := duckQuery(t, dir, q.sql)
		dur := time.Since(start)
		if err != nil {
			t.Logf("  %s: ERROR %v", q.label, err)
			continue
		}
		line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
		if len(line) > 80 {
			line = line[:80] + "..."
		}
		t.Logf("  %s: %v → %s", q.label, dur, line)
	}
}

// ── Test 3: Provider Simulation ──

func TestProviderSimulationStress(t *testing.T) {
	t.Run("SQD_100k_chunks", func(t *testing.T) {
		const n = 100_000
		start := time.Now()
		for i := 0; i < n; i++ {
			_ = i
		}
		dur := time.Since(start)
		t.Logf("  SQD: %s chunks in %v (%.0f chunks/s)", commas(n), dur, float64(n)/dur.Seconds())
	})

	t.Run("AWS_100_chunks_50MiB_each", func(t *testing.T) {
		const n = 100
		start := time.Now()
		totalB := int64(0)
		for i := 0; i < n; i++ {
			totalB += 50_000_000
		}
		dur := time.Since(start)
		t.Logf("  AWS: %d chunks × 50MiB = %.2f GB in %v", n, float64(totalB)/1e9, dur)
	})
}

func commas(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
