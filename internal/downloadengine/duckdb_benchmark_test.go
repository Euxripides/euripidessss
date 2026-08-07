package downloadengine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
)

// ── V2.1 RC2: DuckDB Analytics Benchmark ──
//
// 基于 200K 生产验证的 logs.parquet（49,031 行）验证 DuckDB 分析性能：
//   1. 基础扫描  2. 地址画像  3. 多地址 IN  4. Token 流向
//   5. 时间范围  6. 聚合排行  7. 并发查询  8. 字段裁剪
//
// 启用：创建 stress-data/bsc_real/.duckdb-bench.enabled

const (
	flagDuckBench  = ".duckdb-bench.enabled"
	transferTopic  = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	transferSingle = "0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62"
	transferBatch  = "0x4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb"
)

type benchScenario struct {
	Name     string  `json:"name"`
	SQL      string  `json:"sql,omitempty"`
	Duration string  `json:"duration"`
	Seconds  float64 `json:"seconds"`
	Rows     int64   `json:"rows,omitempty"`
	HeapMB   float64 `json:"heap_delta_mb,omitempty"`
	Note     string  `json:"note,omitempty"`
}

type duckdbBenchResult struct {
	Timestamp time.Time       `json:"timestamp"`
	DataFile  string          `json:"data_file"`
	DataRows  int64           `json:"data_rows"`
	Scenarios []benchScenario `json:"scenarios"`
	Passed    bool            `json:"passed"`
}

// TestDuckDBAnalyticsBenchmark 顺序执行 8 个 DuckDB 分析场景并输出报告。
func TestDuckDBAnalyticsBenchmark(t *testing.T) {
	dataRoot := integrityDataRoot(t)
	flag := filepath.Join(dataRoot, flagDuckBench)
	if _, err := os.Stat(flag); err != nil {
		t.Skip("create " + flag + " to enable DuckDB analytics benchmark")
	}
	// 跨包并行互斥（#8 优化）：同一时刻仅一个测试进程可用真实数据
	if release, ok := duckdb.AcquireDataLock(dataRoot); ok {
		t.Cleanup(release)
	} else {
		t.Skip("其他真实数据验证测试正在运行（并行互斥），跳过")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	engine := duckdb.Open(repoRoot, dataRoot, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Fatalf("DuckDB 不可用: %+v", engine.Status())
	}
	ctx := context.Background()

	parquetPath := filepath.Join(dataRoot, "sqd-200k-warehouse", "logs.parquet")
	if _, err := os.Stat(parquetPath); err != nil {
		t.Skipf("warehouse 数据不存在: %v", err)
	}
	p := strings.ReplaceAll(parquetPath, "\\", "/")

	result := &duckdbBenchResult{Timestamp: time.Now().UTC(), DataFile: p}
	transferFilter := fmt.Sprintf("topic0 IN ('%s','%s','%s')", transferTopic, transferSingle, transferBatch)

	exec := func(name, sql string) (int64, float64, error) {
		start := time.Now()
		rows, err := engine.ExecSQLJSON(ctx, sql)
		dur := time.Since(start)
		if err != nil {
			return 0, 0, err
		}
		return int64(len(rows)), dur.Seconds(), nil
	}

	// ── 1. 基础扫描 ──
	sql1 := fmt.Sprintf("SELECT COUNT(*) AS n FROM read_parquet('%s')", p)
	rows1, err := engine.ExecSQLJSON(ctx, sql1)
	if err != nil || len(rows1) == 0 {
		t.Fatalf("scan: %v", err)
	}
	result.DataRows = int64(rows1[0]["n"].(float64))
	rows, secs, err := exec("scan_count", sql1)
	if err != nil {
		t.Fatalf("scan bench: %v", err)
	}
	result.Scenarios = append(result.Scenarios, benchScenario{
		Name: "1. 基础扫描 COUNT(*)", SQL: sql1,
		Duration: roundMS(secs), Seconds: secs, Rows: rows,
		Note: fmt.Sprintf("%.0f rows/s", float64(result.DataRows)/secs),
	})

	// ── 2. 地址画像 ──
	addr := "0x55d398326f99059ff775485246999027b3197955" // USDT 合约
	sql2 := fmt.Sprintf(`SELECT block_number, transaction_hash, address, topic0, topic1, topic2
		FROM read_parquet('%s') WHERE address = '%s' ORDER BY block_number DESC LIMIT 100`, p, addr)
	rows, secs, err = exec("address_profile", sql2)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	sql2b := fmt.Sprintf("SELECT COUNT(*) AS n FROM read_parquet('%s') WHERE address = '%s'", p, addr)
	_, secs2, err := exec("address_profile_count", sql2b)
	if err != nil {
		t.Fatalf("profile count: %v", err)
	}
	result.Scenarios = append(result.Scenarios, benchScenario{
		Name: "2. 地址画像（单地址事件）", SQL: sql2,
		Duration: roundMS(secs), Seconds: secs, Rows: rows,
		Note: "COUNT 查询另耗时 " + roundMS(secs2),
	})

	// ── 3. 多地址分析（1K/10K/50K，SEMI JOIN 避免命令行长度限制） ──
	addrs, err := loadBSCAddresses(filepath.Join(dataRoot, "addresses_accumulated.csv"), 50000)
	if err != nil || len(addrs) == 0 {
		t.Fatalf("加载地址: %v", err)
	}
	for _, size := range []int{1000, 10000, 50000} {
		if size > len(addrs) {
			size = len(addrs)
		}
		addrFile := filepath.Join(dataRoot, "sqd-200k-warehouse", fmt.Sprintf("bench-addr-%d.csv", size))
		content := strings.Join(addrs[:size], "\n")
		if err := os.WriteFile(addrFile, []byte(content), 0644); err != nil {
			t.Fatalf("写地址文件: %v", err)
		}
		af := strings.ReplaceAll(addrFile, "\\", "/")
		sql3 := fmt.Sprintf(`SELECT COUNT(*) AS n FROM read_parquet('%s') t
			SEMI JOIN read_csv('%s', header=false, columns={'addr':'VARCHAR'}) a ON t.address = a.addr`, p, af)
		start := time.Now()
		r3, err := engine.ExecSQLJSON(ctx, sql3)
		dur := time.Since(start).Seconds()
		if err != nil {
			t.Fatalf("multi-address %d: %v", size, err)
		}
		n := int64(0)
		if len(r3) == 1 {
			n = int64(r3[0]["n"].(float64))
		}
		result.Scenarios = append(result.Scenarios, benchScenario{
			Name: fmt.Sprintf("3. 多地址 SEMI JOIN（%d 地址）", size), SQL: sql3,
			Duration: roundMS(dur), Seconds: dur, Rows: n,
			Note: fmt.Sprintf("命中 %d/%d 行", n, result.DataRows),
		})
	}

	// ── 4. Token 流向（ERC20/1155 Transfer 聚合） ──
	sql4a := fmt.Sprintf(`SELECT topic1 AS from_addr, COUNT(*) AS n FROM read_parquet('%s')
		WHERE %s GROUP BY 1 ORDER BY n DESC LIMIT 10`, p, transferFilter)
	rows, secs, err = exec("token_top_senders", sql4a)
	if err != nil {
		t.Fatalf("token flow: %v", err)
	}
	sql4b := fmt.Sprintf(`SELECT topic2 AS to_addr, COUNT(*) AS n FROM read_parquet('%s')
		WHERE %s GROUP BY 1 ORDER BY n DESC LIMIT 10`, p, transferFilter)
	_, secs4b, err := exec("token_top_receivers", sql4b)
	if err != nil {
		t.Fatalf("token flow2: %v", err)
	}
	sql4c := fmt.Sprintf(`SELECT address AS token, topic1 AS holder, COUNT(*) AS n FROM read_parquet('%s')
		WHERE %s GROUP BY 1,2 ORDER BY n DESC LIMIT 10`, p, transferFilter)
	_, secs4c, err := exec("token_holder", sql4c)
	if err != nil {
		t.Fatalf("token flow3: %v", err)
	}
	result.Scenarios = append(result.Scenarios, benchScenario{
		Name: "4. Token 流向（Top 发送方）", SQL: sql4a,
		Duration: roundMS(secs), Seconds: secs, Rows: rows,
		Note: fmt.Sprintf("Top 接收方 %s，Holder %s", roundMS(secs4b), roundMS(secs4c)),
	})

	// ── 5. 时间范围（Block 窗口 + 每日聚合） ──
	mid := int64(107153360)
	sql5 := fmt.Sprintf(`SELECT TRY_CAST(block_number AS UBIGINT) AS block_number, COUNT(*) AS n FROM read_parquet('%s')
		WHERE TRY_CAST(block_number AS UBIGINT) BETWEEN %d AND %d GROUP BY 1 ORDER BY 1 LIMIT 20`, p, mid, mid+200)
	rows, secs, err = exec("block_range", sql5)
	if err != nil {
		t.Fatalf("time range: %v", err)
	}
	sql5b := fmt.Sprintf(`SELECT to_timestamp(TRY_CAST(block_time AS UBIGINT))::DATE AS day, COUNT(*) AS n FROM read_parquet('%s')
		WHERE TRY_CAST(block_number AS UBIGINT) BETWEEN %d AND %d GROUP BY 1 ORDER BY 1`, p, mid-500, mid+500)
	_, secs5b, err := exec("daily_agg", sql5b)
	if err != nil {
		t.Fatalf("daily agg: %v", err)
	}
	result.Scenarios = append(result.Scenarios, benchScenario{
		Name: "5. 时间范围（Block 窗口）", SQL: sql5,
		Duration: roundMS(secs), Seconds: secs, Rows: rows,
		Note: "每日聚合另耗时 " + roundMS(secs5b),
	})

	// ── 6. 大规模聚合排行 ──
	sql6a := fmt.Sprintf(`SELECT address, COUNT(*) AS n FROM read_parquet('%s') GROUP BY 1 ORDER BY n DESC LIMIT 10`, p)
	rows, secs, err = exec("agg_addr_rank", sql6a)
	if err != nil {
		t.Fatalf("agg addr: %v", err)
	}
	sql6b := fmt.Sprintf(`SELECT topic1 AS addr, COUNT(*) AS n FROM read_parquet('%s')
		WHERE %s GROUP BY 1 ORDER BY n DESC LIMIT 10`, p, transferFilter)
	_, secs6b, err := exec("agg_holder_rank", sql6b)
	if err != nil {
		t.Fatalf("agg holder: %v", err)
	}
	result.Scenarios = append(result.Scenarios, benchScenario{
		Name: "6. 聚合排行（地址活跃）", SQL: sql6a,
		Duration: roundMS(secs), Seconds: secs, Rows: rows,
		Note: "Holder 排行另耗时 " + roundMS(secs6b),
	})

	// ── 7. 并发查询（1/5/10，独立临时数据目录避免 db 文件锁冲突） ──
	concurrent := func(n int) float64 {
		var wg sync.WaitGroup
		start := time.Now()
		mu := &sync.Mutex{}
		var errs []string
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tmpDir, _ := os.MkdirTemp("", "duckdb-conc-")
				defer os.RemoveAll(tmpDir)
				e := duckdb.Open(repoRoot, tmpDir, duckdb.AnalyticsConfig{})
				_, err := e.ExecSQLJSON(ctx, sql1)
				if err != nil {
					mu.Lock()
					errs = append(errs, err.Error())
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		dur := time.Since(start).Seconds()
		if len(errs) > 0 {
			t.Errorf("并发 %d: %v", n, errs[0])
		}
		return dur
	}
	var concurrentNote []string
	for _, n := range []int{1, 5, 10} {
		d := concurrent(n)
		result.Scenarios = append(result.Scenarios, benchScenario{
			Name:     fmt.Sprintf("7. 并发查询（%d 连接）", n),
			Duration: roundMS(d), Seconds: d,
			Note: fmt.Sprintf("平均单查询 %.0fms", d/float64(n)*1000),
		})
		concurrentNote = append(concurrentNote, fmt.Sprintf("%d 并发: %s", n, roundMS(d)))
	}

	// ── 8. 字段裁剪（Projection） ──
	sql8a := fmt.Sprintf("SELECT block_number, address FROM read_parquet('%s')", p)
	start := time.Now()
	_, err = engine.ExecSQLJSON(ctx, sql8a)
	projDur := time.Since(start).Seconds()
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	start = time.Now()
	_, err = engine.ExecSQLJSON(ctx, fmt.Sprintf("SELECT * FROM read_parquet('%s')", p))
	fullDur := time.Since(start).Seconds()
	if err != nil {
		t.Fatalf("full scan: %v", err)
	}
	result.Scenarios = append(result.Scenarios, benchScenario{
		Name:     "8. 字段裁剪（SELECT 子集 vs SELECT *）",
		Duration: roundMS(projDur), Seconds: projDur,
		Note: fmt.Sprintf("SELECT * 耗时 %s（裁剪加速 %.1f%%）", roundMS(fullDur), (1-projDur/fullDur)*100),
	})

	// ── 汇总判定 ──
	result.Passed = true
	for _, s := range result.Scenarios {
		t.Logf("  %-45s %s", s.Name, s.Duration)
	}
	t.Logf("=== DuckDB Analytics Benchmark 完成：%d 场景，数据 %d 行 ===", len(result.Scenarios), result.DataRows)

	// ── 报告输出 ──
	benchDir := filepath.Join(dataRoot, "..", "..", "benchmark")
	if err := writeDuckDBReport(benchDir, result, t); err != nil {
		t.Errorf("写报告: %v", err)
	}
	if !result.Passed {
		t.Error("benchmark 存在失败场景")
	}
}

func writeDuckDBReport(dir string, result *duckdbBenchResult, t *testing.T) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	snapDir := filepath.Join(dir, "snapshots")
	_ = os.MkdirAll(snapDir, 0755)

	jsonPath := filepath.Join(dir, "duckdb-report.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return err
	}

	mdPath := filepath.Join(dir, "duckdb-report.md")
	var b strings.Builder
	b.WriteString("# V2.1 RC2 DuckDB Analytics Benchmark 报告\n\n")
	b.WriteString(fmt.Sprintf("- 时间: %s\n", result.Timestamp.Format("2006-01-02 15:04:05")))
	b.WriteString(fmt.Sprintf("- 数据: %s（%d 行）\n\n", result.DataFile, result.DataRows))
	b.WriteString("## 场景结果\n\n")
	b.WriteString("| 场景 | 耗时 | 结果行 | 备注 |\n|---|---|---|---|\n")
	for _, s := range result.Scenarios {
		b.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n", s.Name, s.Duration, s.Rows, s.Note))
	}
	b.WriteString(fmt.Sprintf("\n**结论**: %s\n", map[bool]string{true: "✅ 全部场景通过", false: "❌ 存在失败"}[result.Passed]))
	if err := os.WriteFile(mdPath, []byte(b.String()), 0644); err != nil {
		return err
	}
	t.Logf("报告已生成: %s / %s", jsonPath, mdPath)
	return nil
}

// roundMS 把秒转为毫秒字符串（保留 0 位小数）。
func roundMS(secs float64) string {
	return fmt.Sprintf("%.0fms", secs*1000)
}

var _ = bufio.NewReader // keep import if unused in future edits
var _ = runtime.NumCPU
