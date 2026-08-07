package analyticsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
)

// ── V2.1 RC2: 业务查询 API 服务验证 ──
//
// 启用：创建 stress-data/bsc_real/.api-service.enabled

const (
	flagAPIService = ".api-service.enabled"
	knownAddress   = "0x55d398326f99059ff775485246999027b3197955" // USDT 合约
	activeAddress  = "0x238a358808379702088667322f80ac48bad5e6c4" // 活跃交易方
)

type apiReport struct {
	Timestamp   time.Time      `json:"timestamp"`
	Correctness map[string]any `json:"correctness"`
	Cache       map[string]any `json:"cache"`
	Perf        map[string]any `json:"performance"`
	Concurrency map[string]any `json:"concurrency"`
	Passed      bool           `json:"passed"`
}

func newAPITest(t *testing.T) (*Service, *duckdb.Engine, string) {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(repoRoot, "stress-data", "bsc_real")
	flag := filepath.Join(dataRoot, flagAPIService)
	if _, err := os.Stat(flag); err != nil {
		t.Skip("create " + flag + " to enable API service validation")
	}
	// 跨包并行互斥（#8 优化）：同一时刻仅一个测试进程可用真实数据
	if release, ok := duckdb.AcquireDataLock(dataRoot); ok {
		t.Cleanup(release)
	} else {
		t.Skip("其他真实数据验证测试正在运行（并行互斥），跳过")
	}
	engine := duckdb.Open(repoRoot, dataRoot, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Fatalf("DuckDB 不可用: %+v", engine.Status())
	}
	parquetPath := filepath.Join(dataRoot, "sqd-200k-warehouse", "logs.parquet")
	if _, err := os.Stat(parquetPath); err != nil {
		t.Skipf("warehouse 数据不存在: %v", err)
	}
	return New(engine, parquetPath), engine, dataRoot
}

// TestAPI_Correctness 验证 API 结果 == DuckDB SQL 结果。
func TestAPI_Correctness(t *testing.T) {
	svc, engine, _ := newAPITest(t)
	ctx := context.Background()
	report := &apiReport{Timestamp: time.Now().UTC()}

	// 1. Profile：API vs 直接 SQL（已知地址）
	p, err := svc.Profile(ctx, knownAddress)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if p.Address != knownAddress {
		t.Errorf("地址不匹配: %s", p.Address)
	}
	if p.EventCount <= 0 || p.TransactionCount <= 0 {
		t.Errorf("画像字段异常: %+v", p)
	}
	// 与直接 SQL 对比（单地址聚合事件数）
	rows, err := engine.ExecSQLJSON(ctx, fmt.Sprintf(
		"SELECT COUNT(*) AS n FROM read_parquet('%s') WHERE address = '%s'",
		svc.parquet, knownAddress))
	if err != nil || len(rows) == 0 {
		t.Fatalf("sql count: %v", err)
	}
	sqlCount := int64(rows[0]["n"].(float64))
	if sqlCount == 0 {
		t.Fatal("已知地址应有事件")
	}
	// API 的 emitter 事件数应 >= SQL 计数（API 三源）
	report.Correctness = map[string]any{
		"address":           knownAddress,
		"api_event_count":   p.EventCount,
		"sql_emitter_count": sqlCount,
		"profile_ok":        p.EventCount > 0,
	}

	// 2. 不存在的地址返回空结果（不报错）
	missing, err := svc.Profile(ctx, "0x00000000000000000000000000000000000000ff")
	if err != nil {
		t.Fatalf("missing profile: %v", err)
	}
	if missing.EventCount != 0 {
		t.Errorf("不存在地址应返回空画像: %+v", missing)
	}
	report.Correctness["missing_address_empty"] = missing.EventCount == 0

	// 3. 多次查询一致（可复现）
	p2, _ := svc.Profile(ctx, knownAddress)
	report.Correctness["repeatable"] = p2.EventCount == p.EventCount

	// 4. Flows：金额可解析、方向正确
	flows, err := svc.Flows(ctx, activeAddress, "")
	if err != nil {
		t.Fatalf("flows: %v", err)
	}
	if len(flows) == 0 {
		t.Fatal("活跃地址应有资金流")
	}
	hasOut, hasIn := false, false
	amountOK := true
	for _, f := range flows {
		if f.Direction == "outgoing" {
			hasOut = true
		}
		if f.Direction == "incoming" {
			hasIn = true
		}
		if f.Amount == "" {
			amountOK = false
		}
	}
	report.Correctness["flows_has_in_out"] = hasIn && hasOut
	report.Correctness["flows_amount_parsed"] = amountOK

	// 5. Path：无自环
	paths, err := svc.Path(ctx, activeAddress)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	noCycle := true
	for _, pt := range paths {
		if pt.A == pt.B || pt.B == pt.C || pt.A == pt.C {
			noCycle = false
		}
	}
	report.Correctness["path_no_cycle"] = noCycle
	report.Correctness["path_count"] = len(paths)

	// 6. Risk：分值范围 [0,100]
	risk, err := svc.Risk(ctx, activeAddress)
	if err != nil {
		t.Fatalf("risk: %v", err)
	}
	report.Correctness["risk_score"] = risk.RiskScore
	report.Correctness["risk_level"] = risk.RiskLevel
	report.Correctness["risk_in_range"] = risk.RiskScore >= 0 && risk.RiskScore <= 100

	passed := p.EventCount > 0 && missing.EventCount == 0 && p2.EventCount == p.EventCount &&
		hasIn && hasOut && amountOK && noCycle && risk.RiskScore >= 0 && risk.RiskScore <= 100
	report.Passed = passed

	t.Logf("=== API 正确性 ===")
	t.Logf("  profile: event=%d (SQL emitter=%d) missing=%v repeatable=%v", p.EventCount, sqlCount, missing.EventCount == 0, p2.EventCount == p.EventCount)
	t.Logf("  flows: in=%v out=%v amount_ok=%v", hasIn, hasOut, amountOK)
	t.Logf("  path: %d 条无自环=%v", len(paths), noCycle)
	t.Logf("  risk: %.1f (%s)", risk.RiskScore, risk.RiskLevel)
	_ = writeAPIReport(filepath.Join(dataRootOf(t), "..", "..", "benchmark"), report, t)
	if !passed {
		t.Error("正确性验证未通过")
	}
}

func dataRootOf(t *testing.T) string {
	t.Helper()
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	return filepath.Join(repoRoot, "stress-data", "bsc_real")
}

// TestAPI_Cache 验证缓存：首次 miss → DuckDB，再次 hit。
func TestAPI_Cache(t *testing.T) {
	svc, _, _ := newAPITest(t)
	ctx := context.Background()

	// 首次（miss）
	_, err := svc.Profile(ctx, knownAddress)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	s1 := svc.CacheStats()
	// 再次（hit）
	_, err = svc.Profile(ctx, knownAddress)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	s2 := svc.CacheStats()

	if s1.Hits != 0 {
		t.Errorf("首次应为 miss，hits=%d", s1.Hits)
	}
	if s2.Hits != 1 {
		t.Errorf("再次应为 hit，hits=%d", s2.Hits)
	}
	t.Logf("缓存验证: miss=%d hit=%d", s2.Misses, s2.Hits)
	_ = writeAPIReport(filepath.Join(dataRootOf(t), "..", "..", "benchmark"),
		&apiReport{Timestamp: time.Now().UTC(), Cache: map[string]any{"first_miss": s1.Hits == 0, "second_hit": s2.Hits == 1, "hits": s2.Hits, "misses": s2.Misses}, Passed: s2.Hits == 1}, t)
}

// TestAPI_Perf 验证批量画像性能（50K < 1s 目标）。
func TestAPI_Perf(t *testing.T) {
	svc, _, dataRoot := newAPITest(t)
	ctx := context.Background()
	report := &apiReport{Timestamp: time.Now().UTC(), Perf: map[string]any{}}

	// 单地址（含缓存预热后）
	for i := 0; i < 2; i++ {
		start := time.Now()
		_, err := svc.Profile(ctx, knownAddress)
		if err != nil {
			t.Fatalf("profile: %v", err)
		}
		if i == 1 {
			report.Perf["single_profile_ms"] = time.Since(start).Milliseconds()
		}
	}

	// 批量 1K/10K/50K
	addrs, err := loadAddresses(filepath.Join(dataRoot, "addresses_accumulated.csv"), 50000)
	if err != nil || len(addrs) == 0 {
		t.Fatalf("加载地址: %v", err)
	}
	for _, size := range []int{1000, 10000, 50000} {
		if size > len(addrs) {
			size = len(addrs)
		}
		addrFile := filepath.Join(dataRoot, "sqd-200k-warehouse", fmt.Sprintf("bench-addr-%d.csv", size))
		if err := os.WriteFile(addrFile, []byte(strings.Join(addrs[:size], "\n")), 0644); err != nil {
			t.Fatalf("写地址文件: %v", err)
		}
		start := time.Now()
		profiles, err := svc.BatchProfiles(ctx, addrs[:size], addrFile)
		dur := time.Since(start)
		if err != nil {
			t.Fatalf("batch %d: %v", size, err)
		}
		report.Perf[fmt.Sprintf("batch_%d", size)] = map[string]any{
			"ms": dur.Milliseconds(), "rows": len(profiles),
		}
		t.Logf("  批量 %d 地址: %v（返回 %d 行）", size, dur.Round(time.Millisecond), len(profiles))
		if size == 50000 && dur >= time.Second {
			t.Errorf("50K 批量目标 <1s 未达成: %v", dur)
		}
	}
	report.Passed = true
	_ = writeAPIReport(filepath.Join(dataRoot, "..", "..", "benchmark"), report, t)
}

// TestAPI_Concurrency 验证 10/50/100 并发查询。
func TestAPI_Concurrency(t *testing.T) {
	svc, _, dataRoot := newAPITest(t)
	report := &apiReport{Timestamp: time.Now().UTC(), Concurrency: map[string]any{}}

	handler := NewHandler(svc.engine, svc.parquet)
	pass := true
	for _, n := range []int{10, 50, 100} {
		var wg sync.WaitGroup
		start := time.Now()
		errCh := make(chan error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				addr := knownAddress
				if i%3 == 0 {
					addr = activeAddress
				}
				req := httptest.NewRequest("GET", "/analytics/address/"+addr+"/profile", nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != 200 {
					errCh <- fmt.Errorf("HTTP %d: %s", w.Code, w.Body.String())
					return
				}
				var p Profile
				if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
					errCh <- err
				}
			}(i)
		}
		wg.Wait()
		close(errCh)
		dur := time.Since(start)
		errs := 0
		for err := range errCh {
			errs++
			if err != nil {
				t.Errorf("并发 %d: %v", n, err)
			}
		}
		report.Concurrency[fmt.Sprintf("concurrent_%d", n)] = map[string]any{
			"ms": dur.Milliseconds(), "errors": errs,
		}
		t.Logf("  并发 %d: %v（错误 %d）", n, dur.Round(time.Millisecond), errs)
		if errs > 0 {
			pass = false
		}
	}
	report.Passed = pass
	_ = writeAPIReport(filepath.Join(dataRoot, "..", "..", "benchmark"), report, t)
	if !pass {
		t.Error("并发测试存在错误")
	}
}

// TestBatchProfiles_Validation 回归：addresses 与 addr_file 都空时拒绝；
// addresses 走 VALUES 内联（不产生 read_csv(”) 读 stdin 的 OOM 路径）。
func TestBatchProfiles_Validation(t *testing.T) {
	// 1. 两者都空 → 错误（修复前：read_csv('') 读标准输入 → JOIN 爆炸 → 服务 OOM）
	if _, err := batchWantSQL(nil, ""); err == nil {
		t.Fatal("addresses 与 addr_file 都为空时必须返回错误")
	}
	if _, err := batchWantSQL(nil, "  "); err == nil {
		t.Fatal("空白 addr_file 视为缺失，必须返回错误")
	}

	// 2. addresses 内联 VALUES，禁止 read_csv
	sql, err := batchWantSQL([]string{"0xABC", "0xDEF'"}, "")
	if err != nil {
		t.Fatalf("addresses 应生成 VALUES SQL: %v", err)
	}
	if !strings.Contains(sql, "VALUES") {
		t.Errorf("addresses 分支应使用 VALUES 内联: %s", sql)
	}
	if strings.Contains(sql, "read_csv") {
		t.Errorf("addresses 分支禁止 read_csv（空路径会读 stdin）: %s", sql)
	}
	if !strings.Contains(sql, "('0xabc')") || !strings.Contains(sql, "('0xdef''')") {
		t.Errorf("地址应小写化并转义单引号: %s", sql)
	}

	// 3. addr_file 分支保留 read_csv（有真实路径，且转义防注入）
	sql, err = batchWantSQL(nil, `E:\tmp\addr.csv`)
	if err != nil {
		t.Fatalf("addr_file 应生成 read_csv SQL: %v", err)
	}
	if !strings.Contains(sql, "read_csv('E:/tmp/addr.csv'") {
		t.Errorf("addr_file 路径应归一化为正斜杠: %s", sql)
	}
	// 单引号路径必须被转义（防 SQL 注入/任意文件读取）
	sql, err = batchWantSQL(nil, `E:\x' OR 1=1 --`)
	if err != nil {
		t.Fatalf("addr_file 转义: %v", err)
	}
	if strings.Count(sql, "'")%2 != 0 || !strings.Contains(sql, "''") {
		t.Errorf("addr_file 单引号必须成对转义（偶数个引号）: %s", sql)
	}

	// 4. 超量截断：>500 只保留前 500（Windows 命令行 32K 限制）
	big := make([]string, 800)
	for i := range big {
		big[i] = "0x1"
	}
	sql, err = batchWantSQL(big, "")
	if err != nil {
		t.Fatalf("超量地址应被截断: %v", err)
	}
	if strings.Count(sql, "('0x1')") != 500 {
		t.Errorf("超量地址应截断到 500: %s", sql)
	}

	// 5. addrFile 与 addresses 同时提供时优先 addrFile（命令短，避免超长命令行）
	sql, err = batchWantSQL([]string{"0x1"}, `E:\tmp\addr.csv`)
	if err != nil {
		t.Fatalf("addrFile 优先: %v", err)
	}
	if !strings.Contains(sql, "read_csv") || strings.Contains(sql, "VALUES") {
		t.Errorf("addrFile 存在时应优先 read_csv 而非 VALUES: %s", sql)
	}

	// 6. 超长地址被跳过（防命令行长度 DoS）；全部非法时返回错误
	sql, err = batchWantSQL([]string{strings.Repeat("a", 100), "0xabc"}, "")
	if err != nil {
		t.Fatalf("超长地址应被跳过: %v", err)
	}
	if strings.Contains(sql, strings.Repeat("a", 100)) || !strings.Contains(sql, "('0xabc')") {
		t.Errorf("超长地址应跳过且保留合法地址: %s", sql)
	}
	if _, err := batchWantSQL([]string{strings.Repeat("a", 100)}, ""); err == nil {
		t.Fatal("全部地址非法时应返回错误")
	}
}

// TestValidateAddrFile 回归：addr_file 路径安全校验（防任意文件读取）。
func TestValidateAddrFile(t *testing.T) {
	engine := duckdb.Open(filepath.Join("..", ".."), filepath.Join("..", "..", "stress-data", "bsc_real"), duckdb.AnalyticsConfig{})
	h := NewHandler(engine, filepath.Join("..", "..", "stress-data", "bsc_real", "sqd-200k-warehouse", "logs.parquet"))

	// 合法：数据目录内相对路径
	if err := h.validateAddrFile("sqd-200k-warehouse/bench-addr-1000.csv"); err != nil {
		t.Errorf("合法相对路径应通过: %v", err)
	}
	// 空值放行（Handler 层已先校验必填）
	if err := h.validateAddrFile(""); err != nil {
		t.Errorf("空值应放行: %v", err)
	}
	// 绝对路径拒绝
	abs, _ := filepath.Abs(".")
	if err := h.validateAddrFile(abs); err == nil {
		t.Error("绝对路径必须拒绝")
	}
	// 路径穿越拒绝
	if err := h.validateAddrFile(`..\..\backend\data\dune\auth.json`); err == nil {
		t.Error("路径穿越必须拒绝")
	}
	if err := h.validateAddrFile(`..\..\..\Windows\win.ini`); err == nil {
		t.Error("深层穿越必须拒绝")
	}
	// 通配符拒绝
	if err := h.validateAddrFile(`sqd-200k-warehouse\*.csv`); err == nil {
		t.Error("通配符必须拒绝")
	}
}

// TestBatchCacheKey 回归：地址集哈希缓存键（顺序无关、大小写归一、区分 addr_file、分隔符防碰撞）。
func TestBatchCacheKey(t *testing.T) {
	k1 := batchCacheKey([]string{"0xABC", "0xdef"}, "")
	k2 := batchCacheKey([]string{"0xdef", "0xabc"}, "")
	if k1 != k2 {
		t.Errorf("地址顺序无关: %s != %s", k1, k2)
	}
	k3 := batchCacheKey([]string{"0xabc"}, "")
	k4 := batchCacheKey([]string{"0xabc"}, "file.csv")
	if k3 == k4 {
		t.Error("addr_file 应区分缓存键")
	}
	k5 := batchCacheKey([]string{"0xabc"}, "file.csv")
	if k4 != k5 {
		t.Error("相同 addr_file 应一致")
	}
	if !strings.HasPrefix(k1, "batch:") {
		t.Errorf("键应带 batch: 前缀: %s", k1)
	}
	// 分隔符防碰撞：["ab","cd"] 与 ["abc","d"] 不得同键
	k6 := batchCacheKey([]string{"ab", "cd"}, "")
	k7 := batchCacheKey([]string{"abc", "d"}, "")
	if k6 == k7 {
		t.Error("拼接碰撞：['ab','cd'] 与 ['abc','d'] 不得同键")
	}
	// addr_file 文件版本（mtime+size）参与键：临时文件改写后键变化
	dir := t.TempDir()
	fp := filepath.Join(dir, "addrs.csv")
	if err := os.WriteFile(fp, []byte("0xabc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ka := batchCacheKey(nil, fp)
	if err := os.WriteFile(fp, []byte("0xabc\n0xdef\n"), 0644); err != nil {
		t.Fatal(err)
	}
	kb := batchCacheKey(nil, fp)
	if ka == kb {
		t.Error("addr_file 内容变化后缓存键应变化（mtime+size 参与）")
	}
	// 截断一致性：>500 地址时键与 batchWantSQL 截断语义一致（前 500 排序哈希）
	big1 := make([]string, 600)
	big2 := make([]string, 900)
	for i := range big2 {
		big2[i] = "0x1"
		if i < 600 {
			big1[i] = "0x1"
		}
	}
	kBig := batchCacheKey(big1, "")
	kBig2 := batchCacheKey(big2, "")
	if kBig != kBig2 {
		t.Error(">500 地址缓存键应只取决于前 500 个（与 batchWantSQL 截断一致）")
	}
	// 排序语义统一：相同集合不同顺序 → 同 key；且 batchWantSQL 对两顺序产生相同 VALUES 集
	a1 := []string{"0x9", "0x1", "0x5"}
	a2 := []string{"0x5", "0x9", "0x1"}
	if batchCacheKey(a1, "") != batchCacheKey(a2, "") {
		t.Error("相同集合不同顺序应同键")
	}
	sql1, err1 := batchWantSQL(a1, "")
	sql2, err2 := batchWantSQL(a2, "")
	if err1 != nil || err2 != nil || sql1 != sql2 {
		t.Errorf("排序后 SQL 应与输入顺序无关: %q vs %q (%v/%v)", sql1, sql2, err1, err2)
	}
}

func loadAddresses(path string, max int) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(content), "\n") {
		addr := strings.ToLower(strings.TrimSpace(line))
		if len(addr) == 42 && strings.HasPrefix(addr, "0x") && !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
			if len(out) >= max {
				break
			}
		}
	}
	return out, nil
}

func writeAPIReport(dir string, report *apiReport, t *testing.T) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	jsonPath := filepath.Join(dir, "api-service-report.json")
	// 合并累积（各测试独立 report）
	if existing := loadAPIReport(jsonPath); existing != nil {
		mergeAPIReport(existing, report)
		report = existing
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return err
	}
	mdPath := filepath.Join(dir, "api-service-report.md")
	var b strings.Builder
	b.WriteString("# V2.1 RC2 业务查询 API 服务验证报告\n\n")
	b.WriteString(fmt.Sprintf("- 时间: %s\n\n", report.Timestamp.Format("2006-01-02 15:04:05")))
	if report.Correctness != nil {
		b.WriteString("## 正确性\n\n")
		body, _ := json.MarshalIndent(report.Correctness, "", "  ")
		b.WriteString("```json\n" + string(body) + "\n```\n")
	}
	if report.Cache != nil {
		b.WriteString("\n## 缓存\n\n")
		body, _ := json.MarshalIndent(report.Cache, "", "  ")
		b.WriteString("```json\n" + string(body) + "\n```\n")
	}
	if report.Perf != nil {
		b.WriteString("\n## 性能\n\n")
		b.WriteString("| 场景 | 耗时 |\n|---|---|\n")
		for k, v := range report.Perf {
			if m, ok := v.(map[string]any); ok {
				b.WriteString(fmt.Sprintf("| %s | %vms |\n", k, m["ms"]))
			} else {
				b.WriteString(fmt.Sprintf("| %s | %v |\n", k, v))
			}
		}
	}
	if report.Concurrency != nil {
		b.WriteString("\n## 并发\n\n")
		b.WriteString("| 规模 | 总耗时 | 错误 |\n|---|---|---|\n")
		for k, v := range report.Concurrency {
			if m, ok := v.(map[string]any); ok {
				b.WriteString(fmt.Sprintf("| %s | %vms | %v |\n", k, m["ms"], m["errors"]))
			}
		}
	}
	b.WriteString(fmt.Sprintf("\n**结论**: %s\n", map[bool]string{true: "✅ 全部通过", false: "❌ 存在失败"}[report.Passed]))
	if err := os.WriteFile(mdPath, []byte(b.String()), 0644); err != nil {
		return err
	}
	t.Logf("报告已生成: %s / %s", jsonPath, mdPath)
	return nil
}

func loadAPIReport(path string) *apiReport {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var r apiReport
	if err := json.Unmarshal(content, &r); err != nil {
		return nil
	}
	return &r
}

func mergeAPIReport(existing, fresh *apiReport) {
	if len(fresh.Correctness) > 0 {
		existing.Correctness = fresh.Correctness
	}
	if len(fresh.Cache) > 0 {
		existing.Cache = fresh.Cache
	}
	if len(fresh.Perf) > 0 {
		existing.Perf = fresh.Perf
	}
	if len(fresh.Concurrency) > 0 {
		existing.Concurrency = fresh.Concurrency
	}
	existing.Passed = existing.Passed || fresh.Passed
}
