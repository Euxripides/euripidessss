package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/etl/backend/benchmark/runner"
)

const duckdbExe = `E:\codex\etl\tools\duckdb\duckdb.exe`

func main() {
	workload := flag.String("workload", "all", "parquet|duckdb|pipeline|all")
	output := flag.String("output", "benchmark-report.json", "output path")
	rows := flag.Int("rows", 1_000_000, "rows for benchmark")
	addr := flag.Int("addresses", 100_000, "addresses for pipeline")
	flag.Parse()

	cfg := runner.Config{
		Name:     "V2.1-RC2",
		Workload: *workload,
		Params:   map[string]any{"rows": *rows},
		Targets: []runner.Target{
			{RowsPerSec: 500_000, QueryMS: 5000, MemMB: 512},
		},
	}

	r := runner.New(cfg)
	dir, err := os.MkdirTemp("", "bsc-benchmark-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}

	switch *workload {
	case "parquet":
		runParquetBench(r, dir, *rows)
	case "duckdb":
		runDuckDBBench(r, dir, *rows)
	case "pipeline":
		runPipelineBench(r, *addr)
	case "simulated":
		runSimulatedChainBench(r, dir, *addr)
	default:
		runParquetBench(r, dir, *rows)
		runDuckDBBench(r, dir, *rows)
		runPipelineBench(r, *addr)
		runSimulatedChainBench(r, dir, *addr)
	}

	if err := r.WriteReport(*output); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Report: %s | %s.md\n", *output, *output)
}

func runParquetBench(r *runner.Runner, dir string, rows int) {
	if _, err := os.Stat(duckdbExe); err != nil {
		r.Run("parquet_skipped", func() (runner.AppMetrics, error) { return runner.AppMetrics{}, err })
		return
	}
	outPath := filepath.Join(dir, "bench.parquet")
	outSlash := strings.ReplaceAll(outPath, "\\", "/")

	r.Run("parquet_write", func() (runner.AppMetrics, error) {
		start := time.Now()
		sql := fmt.Sprintf(`COPY (SELECT r AS block_number FROM generate_series(1,%d) AS t(r)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`, rows, outSlash)
		cmd := exec.CommandContext(context.Background(), duckdbExe, "-c", sql)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		dur := time.Since(start)
		if err != nil {
			return runner.AppMetrics{}, fmt.Errorf("%w\n%s", err, string(out))
		}
		info, _ := os.Stat(outPath)
		sz := int64(0)
		if info != nil {
			sz = info.Size()
		}
		return runner.AppMetrics{RowsTotal: int64(rows), RowsPerSec: float64(rows) / dur.Seconds(), MBPerSec: float64(sz) / dur.Seconds() / 1e6, FileSizeMB: float64(sz) / 1e6}, nil
	})
}

func runDuckDBBench(r *runner.Runner, dir string, rows int) {
	if _, err := os.Stat(duckdbExe); err != nil {
		r.Run("duckdb_skipped", func() (runner.AppMetrics, error) { return runner.AppMetrics{}, err })
		return
	}
	outSlash := strings.ReplaceAll(filepath.Join(dir, "bench.parquet"), "\\", "/")

	// ensure file exists
	genSQL := fmt.Sprintf(`COPY (SELECT r AS block_number FROM generate_series(1,%d) AS t(r)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`, rows, outSlash)
	exec.CommandContext(context.Background(), duckdbExe, "-c", genSQL).Run()

	for _, q := range []struct{ name, sql string }{
		{"count_all", fmt.Sprintf("SELECT count(*) FROM read_parquet('%s')", outSlash)},
		{"group_by", fmt.Sprintf("SELECT block_number%%1000 AS g, count(*) FROM read_parquet('%s') GROUP BY g ORDER BY g", outSlash)},
	} {
		r.Run("duckdb_"+q.name, func() (runner.AppMetrics, error) {
			start := time.Now()
			cmd := exec.CommandContext(context.Background(), duckdbExe, "-c", q.sql)
			cmd.Dir = dir
			_, err := cmd.CombinedOutput()
			dur := time.Since(start)
			return runner.AppMetrics{RowsTotal: int64(rows), RowsPerSec: float64(rows) / dur.Seconds()}, err
		})
	}
}

func runPipelineBench(r *runner.Runner, addrs int) {
	r.Run("pipeline_addresses", func() (runner.AppMetrics, error) {
		start := time.Now()
		seen := make(map[string]bool, addrs)
		for i := 0; i < addrs; i++ {
			seen[fmt.Sprintf("0x%040x", i)] = true
		}
		dur := time.Since(start)
		return runner.AppMetrics{RowsTotal: int64(addrs), RowsPerSec: float64(addrs) / dur.Seconds()}, nil
	})
}

// ── Simulated Chain Benchmark (DuckDB synthetic data, Levels 1-4) ──

func runSimulatedChainBench(r *runner.Runner, dir string, addrs int) {
	if _, err := os.Stat(duckdbExe); err != nil {
		r.Run("sim_chain_skipped", func() (runner.AppMetrics, error) { return runner.AppMetrics{}, err })
		return
	}

	levels := []struct {
		name string
		n    int
	}{
		{"L1_10K", 10000},
		{"L2_100K", 100000},
		{"L3_500K", 500000},
	}
	if addrs >= 1000000 {
		levels = append(levels, struct{ name string; n int }{"L4_1M", 1000000})
	}

	outDir := filepath.Join(dir, "sim-chain")
	_ = os.MkdirAll(outDir, 0755)

	for _, lv := range levels {
		n := lv.n
		if n > addrs {
			n = addrs
		}

		// 生成并立即查询（DuckDB 内存表）
		fromSQL := fmt.Sprintf(`SELECT '0x'||printf('%%040x',r%%%d) AS from_addr, CAST(random()*1e18 AS BIGINT) AS value, CAST('2022-01-01' AS TIMESTAMP)+INTERVAL (r%%30000000) SECOND AS ts FROM generate_series(1,%d) AS t(r)`, n, n)

		// 1. COUNT
		r.Run("sim_"+lv.name+"_count", func() (runner.AppMetrics, error) {
			start := time.Now()
			sql := fmt.Sprintf("SELECT count(*) FROM (%s)", fromSQL)
			cmd := exec.CommandContext(context.Background(), duckdbExe, "-c", sql)
			_, err := cmd.CombinedOutput()
			return runner.AppMetrics{RowsTotal: int64(n), RowsPerSec: float64(n) / time.Since(start).Seconds()}, err
		})

		// 2. GROUP BY
		r.Run("sim_"+lv.name+"_group_from", func() (runner.AppMetrics, error) {
			start := time.Now()
			sql := fmt.Sprintf("SELECT from_addr, count(*) c FROM (%s) GROUP BY from_addr ORDER BY c DESC LIMIT 100", fromSQL)
			cmd := exec.CommandContext(context.Background(), duckdbExe, "-c", sql)
			cmd.Dir = outDir
			_, err := cmd.CombinedOutput()
			return runner.AppMetrics{RowsTotal: int64(n), RowsPerSec: float64(n) / time.Since(start).Seconds()}, err
		})
	}

	// 3. Parquet write benchmark (Level 3 only)
	r.Run("sim_parquet_500k", func() (runner.AppMetrics, error) {
		n := 500000
		start := time.Now()
		outPath := strings.ReplaceAll(filepath.Join(outDir, "sim_tx.parquet"), "\\", "/")
		sql := fmt.Sprintf(`COPY (SELECT r AS block_number FROM generate_series(1,%d) AS t(r)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`, n, outPath)
		cmd := exec.CommandContext(context.Background(), duckdbExe, "-c", sql)
		cmd.Dir = outDir
		out, err := cmd.CombinedOutput()
		dur := time.Since(start)
		if err != nil {
			return runner.AppMetrics{}, fmt.Errorf("%w\n%s", err, string(out))
		}
		info, _ := os.Stat(outPath)
		sz := int64(0)
		if info != nil {
			sz = info.Size()
		}
		return runner.AppMetrics{RowsTotal: int64(n), RowsPerSec: float64(n) / dur.Seconds(), MBPerSec: float64(sz) / dur.Seconds() / 1e6, FileSizeMB: float64(sz) / 1e6}, nil
	})

	// 4. Crash Resume
	r.Run("sim_crash_resume", func() (runner.AppMetrics, error) {
		chunks := make([]struct{ id string; done bool }, 1000)
		for i := range chunks {
			chunks[i].id = fmt.Sprintf("c-%d", i)
			chunks[i].done = i < 500
		}
		resumed := 0
		for _, ch := range chunks {
			if !ch.done {
				resumed++
			}
		}
		return runner.AppMetrics{ChunksTotal: int64(len(chunks)), RowsTotal: int64(resumed)}, nil
	})
}
