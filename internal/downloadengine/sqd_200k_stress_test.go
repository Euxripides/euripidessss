package downloadengine

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
)

// ── V2.1 RC2: 200K 地址真实链生产验证 ──
//
// 链路：Address List → Chunk Queue(100/chunk) → SQD Reliability Layer
// → StreamLogs → Global Dedup(chain_id+block+tx_hash+log_index)
// → CSV → DuckDB → Parquet → COUNT 验证
//
// Checkpoint：{dataRoot}/checkpoints/sqd-200k.json — 已完成 chunk 跳过，
// 全局 CSV 重建去重 map，恢复不重复写入。
//
// 启用：创建 stress-data/bsc_real/.sqd-200k.enabled

const (
	env200KTest     = "SQD_200K_TEST"
	env200KMaxAddr  = "SQD_200K_MAX_ADDRESSES"
	env200KBlocks   = "SQD_200K_BLOCKS"
	env200KChunk    = "SQD_200K_CHUNK_SIZE"
	env200KDataRoot = "SQD_200K_DATA_ROOT"
	env200KStartBlock = "SQD_200K_START_BLOCK"
	flag200KEnabled = ".sqd-200k.enabled"

	default200KAddresses = 200000
	default200KBlocks    = 200
	default200KChunkSize = 100
)

type stress200KCheckpoint struct {
	JobID           string    `json:"job_id"`
	AddressCount    int       `json:"address_count"`
	BlockRange      [2]uint64 `json:"block_range"`
	CompletedChunks []int     `json:"completed_chunks"`
	UniqueLogs      int       `json:"unique_logs"`
	TotalLogs       int       `json:"total_logs"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type stress200KResult struct {
	Timestamp           time.Time               `json:"timestamp"`
	AddressCount        int                     `json:"address_count"`
	ChunkCount          int                     `json:"chunk_count"`
	CompletedChunks     int                     `json:"completed_chunks"`
	BlockRange          [2]uint64               `json:"block_range"`
	TotalLogs           int                     `json:"raw_logs"`
	UniqueLogs          int                     `json:"unique_logs"`
	DuplicateLogs       int                     `json:"duplicate_logs"`
	DedupRatio          float64                 `json:"dedup_ratio"`
	DuplicateInChunk    int                     `json:"duplicate_logs_in_chunk"`
	DuplicateCrossChunk int                     `json:"duplicate_logs_cross_chunk"`
	ResumedFromCheckpoint bool                  `json:"resumed_from_checkpoint"`
	Duration            string                  `json:"duration"`
	Metrics             sqd.MetricsSnapshot     `json:"sqd_metrics"`
	Workers             sqd.AdaptiveWorkerStats `json:"sqd_workers"`
	Circuit             sqd.CircuitStats        `json:"sqd_circuit_breaker"`
	ParquetPath         string                  `json:"parquet_path"`
	ParquetRows         int64                   `json:"parquet_rows"`
	DuckDBVerified      bool                    `json:"duckdb_verified"`
	Errors              []string                `json:"errors,omitempty"`
	Passed              bool                    `json:"passed"`
}

// TestSQD200KStability runs a bounded real-chain production validation over
// up to 200K real BSC addresses with checkpoint recovery and Parquet output.
func TestSQD200KStability(t *testing.T) {
	dataRoot := os.Getenv(env200KDataRoot)
	if dataRoot == "" {
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatal(err)
		}
		dataRoot = filepath.Join(repoRoot, "stress-data", "bsc_real")
	}
	flagPath := filepath.Join(dataRoot, flag200KEnabled)
	if os.Getenv(env200KTest) != "1" {
		if _, err := os.Stat(flagPath); err != nil {
			t.Skip("create " + flagPath + " (or set " + env200KTest + "=1) for real-chain 200K validation")
		}
	}
	// 跨包并行互斥（#8 优化）：同一时刻仅一个测试进程可用真实数据
	if release, ok := duckdb.AcquireDataLock(dataRoot); ok {
		t.Cleanup(release)
	} else {
		t.Skip("其他真实数据验证测试正在运行（并行互斥），跳过")
	}

	// ── 1. 配置 ──
	maxAddresses := atoiEnv(env200KMaxAddr, default200KAddresses)
	blockWindow := uint64(atoiEnv(env200KBlocks, default200KBlocks))
	startBlock := uint64(atoiEnv(env200KStartBlock, int(default10KStartBlock)))
	chunkSize := atoiEnv(env200KChunk, default200KChunkSize)
	jobID := "sqd-200k-" + time.Now().Format("20060102-150405")

	// ── 2. 加载地址 ──
	addrPath := filepath.Join(dataRoot, "addresses_accumulated.csv")
	addresses, err := loadBSCAddresses(addrPath, maxAddresses)
	if err != nil {
		t.Fatalf("加载地址: %v", err)
	}
	t.Logf("地址加载: %d 个（上限 %d）", len(addresses), maxAddresses)
	if len(addresses) == 0 {
		t.Fatal("地址列表为空")
	}

	// ── 3. Reliability Client + DuckDB ──
	client, err := sqd.NewReliable(nil, sqd.DefaultPortal, "", filepath.Join(dataRoot, "logs"))
	if err != nil {
		t.Fatalf("NewReliable: %v", err)
	}
	defer client.Close()
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	engine := duckdb.Open(repoRoot, dataRoot, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Fatalf("DuckDB 不可用: %+v", engine.Status())
	}

	// ── 4. Checkpoint 加载 ──
	cpPath := filepath.Join(dataRoot, "checkpoints", "sqd-200k.json")
	cp := load200KCheckpoint(cpPath)
	resumed := cp != nil
	if cp == nil {
		cp = &stress200KCheckpoint{
			JobID: jobID, AddressCount: len(addresses),
			BlockRange: [2]uint64{startBlock, startBlock + blockWindow},
		}
	}
	if cp.AddressCount != len(addresses) || cp.BlockRange[0] != startBlock || cp.BlockRange[1] != startBlock+blockWindow {
		t.Logf("checkpoint 配置不匹配（地址/区块变更），重建 checkpoint")
		cp = &stress200KCheckpoint{
			JobID: jobID, AddressCount: len(addresses),
			BlockRange: [2]uint64{startBlock, startBlock + blockWindow},
		}
		resumed = false
	}
	doneChunks := make(map[int]bool)
	for _, idx := range cp.CompletedChunks {
		doneChunks[idx] = true
	}
	// 全局 CSV 重建去重 map（恢复时避免重复写入）
	warehouseDir := filepath.Join(dataRoot, "sqd-200k-warehouse")
	if err := os.MkdirAll(warehouseDir, 0755); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(warehouseDir, "logs.csv")
	logSeen, csvLogs, err := loadLogKeysFromCSV(csvPath)
	if err != nil {
		t.Fatalf("读取历史 CSV: %v", err)
	}
	_ = csvLogs

	// ── 5. Chunk Queue + 下载 + 全局去重 ──
	endBlock := startBlock + blockWindow
	chunks := splitAddressChunks(addresses, chunkSize)

	result := &stress200KResult{
		Timestamp: time.Now().UTC(), BlockRange: [2]uint64{startBlock, endBlock},
		AddressCount: len(addresses), ChunkCount: len(chunks),
		ResumedFromCheckpoint: resumed,
	}
	// 恢复 checkpoint 计数（已完成 chunk 的日志数）
	if cp != nil {
		result.UniqueLogs = cp.UniqueLogs
		result.TotalLogs = cp.TotalLogs
	}
	// 全局 map 是唯一日志的权威（checkpoint 可能与 CSV 有偏差时以 map 为准）
	if len(logSeen) > result.UniqueLogs {
		result.UniqueLogs = len(logSeen)
	}
	var errors []string

	start := time.Now()
	chunkUniqueSum := 0
	processed := 0
	writer := newAppendCSV(csvPath)
	defer writer.Close()

	pending := make([]pendingChunk, 0, len(chunks))
	for i, chunk := range chunks {
		if doneChunks[i] {
			continue
		}
		pending = append(pending, pendingChunk{idx: i, addrs: chunk})
	}

	for attempt := 0; attempt < 3 && len(pending) > 0; attempt++ {
		var next []pendingChunk
		for _, pc := range pending {
			if !waitForSQDAvailability(client, 90*time.Second) {
				errors = append(errors, fmt.Sprintf("attempt %d: SQD 长时间不可用", attempt+1))
				next = append(next, pc)
				continue
			}
			chunkSeen := make(map[string]bool)
			err := client.StreamLogs(context.Background(), network,
				sqd.BlockRange{From: startBlock, To: endBlock}, pc.addrs,
				func(block sqd.Block) error {
					for _, log := range block.Logs {
						result.TotalLogs++
						key := fmt.Sprintf("%d/%d/%s/%d", network.ID, block.Header.Number,
							strings.ToLower(log.TransactionHash), log.LogIndex)
						chunkSeen[key] = true
						if logSeen[key] {
							result.DuplicateLogs++
						} else {
							logSeen[key] = true
							result.UniqueLogs++
							if err := writer.Write(logCSVRow(network.ID, block, log)); err != nil {
								return err
							}
						}
					}
					return nil
				})
			if err != nil {
				next = append(next, pc)
				t.Logf("  attempt %d: chunk 失败 %v → 待重试", attempt+1, err)
				continue
			}
			chunkUniqueSum += len(chunkSeen)
			processed++
			// 持久化 checkpoint（每 chunk 完成后）；UniqueLogs 以全局 map 为权威（累计值）
			cp.CompletedChunks = append(cp.CompletedChunks, pc.idx)
			cp.UniqueLogs = len(logSeen)
			cp.TotalLogs = result.TotalLogs
			cp.UpdatedAt = time.Now().UTC()
			if err := save200KCheckpoint(cpPath, cp); err != nil {
				errors = append(errors, fmt.Sprintf("checkpoint 保存失败: %v", err))
			}
			t.Logf("  attempt %d: chunk %d/%d OK（已处理 %d/%d）", attempt+1, pc.idx+1, len(chunks), processed, len(chunks)-len(doneChunks))
		}
		pending = next
		if len(pending) > 0 && attempt < 2 {
			t.Logf("  等待 SQD 恢复后重试 %d 个 chunk...", len(pending))
			waitForSQDAvailability(client, 60*time.Second)
		}
	}
	for _, pc := range pending {
		errors = append(errors, fmt.Sprintf("chunk %d（%d 地址）重试 3 次仍失败", pc.idx, len(pc.addrs)))
	}
	writer.Close()

	elapsed := time.Since(start)
	result.CompletedChunks = processed
	result.Duration = elapsed.Round(time.Millisecond).String()
	result.Errors = errors
	if processed > 0 {
		result.DuplicateInChunk = result.TotalLogs - chunkUniqueSum
		result.DuplicateCrossChunk = chunkUniqueSum - result.UniqueLogs
	}
	if result.UniqueLogs > 0 {
		result.DedupRatio = float64(result.UniqueLogs) / float64(result.UniqueLogs+result.DuplicateLogs)
	}
	if client.Metrics() != nil {
		result.Metrics = client.Metrics().Snapshot()
	}
	if client.Workers() != nil {
		result.Workers = client.Workers().Stats()
	}
	result.Circuit = client.Breaker().Stats()

	// ── 6. Parquet 输出 + DuckDB 验证 ──
	if result.UniqueLogs > 0 {
		parquetPath := filepath.Join(warehouseDir, "logs.parquet")
		// quote/escape 显式指定：data 字段可能含逗号，禁止 DuckDB 自动检测（采样可能误判为空引号）
		sqlText := fmt.Sprintf(
			`COPY (SELECT * FROM read_csv('%s', header=true, all_varchar=true, auto_detect=true, quote='"', escape='"', strict_mode=false)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`,
			strings.ReplaceAll(csvPath, "\\", "/"),
			strings.ReplaceAll(parquetPath, "\\", "/"))
		output, err := engine.ExecSQL(context.Background(), sqlText)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Parquet 写入失败: %v %s", err, strings.TrimSpace(string(output))))
		} else {
			result.ParquetPath = parquetPath
			rows, err := engine.ExecSQLJSON(context.Background(),
				fmt.Sprintf("SELECT COUNT(*) AS n FROM read_parquet('%s')", strings.ReplaceAll(parquetPath, "\\", "/")))
			if err == nil && len(rows) == 1 {
				if n, ok := rows[0]["n"].(float64); ok {
					result.ParquetRows = int64(n)
					result.DuckDBVerified = int64(result.UniqueLogs) == result.ParquetRows
				}
			}
		}
	}

	// ── 7. 判定：0 丢失 0 重复 + DuckDB 一致 ──
	result.Passed = len(errors) == 0 && result.DuckDBVerified

	t.Logf("=== 200K 生产验证结果 ===")
	t.Logf("  地址=%d chunk=%d 已完成=%d 恢复=%v 耗时=%s", len(addresses), len(chunks), processed, resumed, result.Duration)
	t.Logf("  日志: raw=%d unique=%d dup=%d (in_chunk=%d cross_chunk=%d) dedup=%.3f",
		result.TotalLogs, result.UniqueLogs, result.DuplicateLogs, result.DuplicateInChunk, result.DuplicateCrossChunk, result.DedupRatio)
	t.Logf("  Parquet: %s rows=%d verified=%v", result.ParquetPath, result.ParquetRows, result.DuckDBVerified)
	t.Logf("  SQD: request=%d success=%d fail=%d retry=%d 503=%d",
		result.Metrics.RequestTotal, result.Metrics.SuccessTotal, result.Metrics.FailedTotal,
		result.Metrics.RetryTotal, result.Metrics.Error503Total)
	t.Logf("  Workers: %d (%s) | Circuit: %s", result.Workers.CurrentWorkers, result.Workers.Tier, result.Circuit.State)
	t.Logf("  Passed: %v", result.Passed)
	for _, e := range errors {
		t.Logf("  ERROR: %s", e)
	}

	// ── 8. 报告 ──
	benchDir := filepath.Join(dataRoot, "..", "..", "benchmark")
	if err := write200KReport(benchDir, result, t); err != nil {
		t.Errorf("写报告: %v", err)
	}

	if !result.Passed {
		t.Errorf("200K 验证未通过：存在失败 chunk 或 DuckDB 不一致（详见报告）")
	}
}

// originalIndex 根据 chunk 内容找回其在完整 chunks 中的索引。
func originalIndex(chunks [][]string, chunk []string) int {
	for i, c := range chunks {
		if len(c) == len(chunk) && len(c) > 0 && c[0] == chunk[0] && c[len(c)-1] == chunk[len(c)-1] {
			return i
		}
	}
	return -1
}

// pendingChunk 携带原始 chunk 索引的待处理项（checkpoint 恢复用）。
type pendingChunk struct {
	idx   int
	addrs []string
}

func logCSVRow(chainID int64, block sqd.Block, log sqd.Log) []string {
	topics := make([]string, 4)
	for i, t := range log.Topics {
		if i < 4 {
			topics[i] = t
		}
	}
	// data 字段防御性清洗：去除换行/制表符（可能出现在非标准 data 中），
	// 避免 CSV 解析歧义
	data := strings.NewReplacer("\n", "", "\r", "", "\t", "").Replace(log.Data)
	return []string{
		fmt.Sprintf("%d", chainID),
		fmt.Sprintf("%d", block.Header.Number),
		fmt.Sprintf("%d", block.Header.Timestamp),
		strings.ToLower(log.TransactionHash),
		fmt.Sprintf("%d", log.LogIndex),
		strings.ToLower(log.Address),
		topics[0], topics[1], topics[2], topics[3],
		data,
	}
}

func load200KCheckpoint(path string) *stress200KCheckpoint {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cp stress200KCheckpoint
	if err := json.Unmarshal(content, &cp); err != nil {
		return nil
	}
	return &cp
}

func save200KCheckpoint(path string, cp *stress200KCheckpoint) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0644)
}

// loadLogKeysFromCSV 读取已有 CSV 重建去重 map（checkpoint 恢复用）。
func loadLogKeysFromCSV(path string) (map[string]bool, int, error) {
	seen := make(map[string]bool)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return seen, 0, nil
		}
		return nil, 0, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	count := 0
	first := true
	for {
		row, err := reader.Read()
		if err != nil {
			break
		}
		if first {
			first = false
			continue // 跳过 header 行
		}
		if len(row) < 5 {
			continue
		}
		key := fmt.Sprintf("%s/%s/%s/%s", row[0], row[1], row[3], row[4])
		seen[key] = true
		count++
	}
	return seen, count, nil
}

// appendCSV 支持追加写 CSV（header 仅首写时输出）。
type appendCSV struct {
	file   *os.File
	writer *csv.Writer
	header bool
}

func newAppendCSV(path string) *appendCSV {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return &appendCSV{}
	}
	info, err := file.Stat()
	header := err != nil || info.Size() == 0
	return &appendCSV{file: file, writer: csv.NewWriter(file), header: header}
}

func (w *appendCSV) Write(row []string) error {
	if w.file == nil {
		return fmt.Errorf("csv writer 未初始化")
	}
	if w.header {
		w.header = false
		if err := w.writer.Write([]string{"chain_id", "block_number", "block_time", "transaction_hash", "log_index", "address", "topic0", "topic1", "topic2", "topic3", "data"}); err != nil {
			return err
		}
	}
	return w.writer.Write(row)
}

func (w *appendCSV) Close() {
	if w.writer != nil {
		w.writer.Flush()
	}
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
}

func write200KReport(dir string, result *stress200KResult, t *testing.T) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	snapDir := filepath.Join(dir, "snapshots")
	_ = os.MkdirAll(snapDir, 0755)

	jsonPath := filepath.Join(dir, "sqd-200k-report.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return err
	}

	mdPath := filepath.Join(dir, "sqd-200k-report.md")
	var b strings.Builder
	b.WriteString("# V2.1 RC2 SQD 200K 地址生产验证报告\n\n")
	b.WriteString(fmt.Sprintf("- 时间: %s\n", result.Timestamp.Format("2006-01-02 15:04:05")))
	b.WriteString(fmt.Sprintf("- 地址数: %d（%d chunks，已完成 %d）\n", result.AddressCount, result.ChunkCount, result.CompletedChunks))
	b.WriteString(fmt.Sprintf("- 区块范围: %d → %d\n", result.BlockRange[0], result.BlockRange[1]))
	b.WriteString(fmt.Sprintf("- 耗时: %s（checkpoint 恢复: %v）\n\n", result.Duration, result.ResumedFromCheckpoint))

	b.WriteString("## 数据完整性\n\n")
	b.WriteString("| 指标 | 值 |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| raw logs | %d |\n", result.TotalLogs))
	b.WriteString(fmt.Sprintf("| unique logs | %d |\n", result.UniqueLogs))
	b.WriteString(fmt.Sprintf("| duplicate logs | %d（in-chunk %d / cross-chunk %d）|\n", result.DuplicateLogs, result.DuplicateInChunk, result.DuplicateCrossChunk))
	b.WriteString(fmt.Sprintf("| dedup ratio | %.3f |\n", result.DedupRatio))
	b.WriteString(fmt.Sprintf("| Parquet rows | %d |\n", result.ParquetRows))
	b.WriteString(fmt.Sprintf("| DuckDB 验证 | %v |\n", result.DuckDBVerified))
	b.WriteString(fmt.Sprintf("| **结论** | **%s** |\n", map[bool]string{true: "✅ 0丢失 0重复", false: "❌ 存在重复/失败"}[result.Passed]))
	b.WriteString("\n> 唯一键：`chain_id + block_number + transaction_hash + log_index`。\n")

	b.WriteString("\n## SQD Reliability 状态\n\n")
	b.WriteString("| 指标 | 值 |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| 请求数 | %d |\n", result.Metrics.RequestTotal))
	b.WriteString(fmt.Sprintf("| 成功数 | %d |\n", result.Metrics.SuccessTotal))
	b.WriteString(fmt.Sprintf("| 失败数 | %d |\n", result.Metrics.FailedTotal))
	b.WriteString(fmt.Sprintf("| 重试数 | %d |\n", result.Metrics.RetryTotal))
	b.WriteString(fmt.Sprintf("| 503 | %d |\n", result.Metrics.Error503Total))
	b.WriteString(fmt.Sprintf("| 429 | %d |\n", result.Metrics.Error429Total))
	b.WriteString(fmt.Sprintf("| 平均延迟 | %.0f ms |\n", result.Metrics.AvgLatencyMS))
	b.WriteString(fmt.Sprintf("| Worker 数 | %d (%s) |\n", result.Workers.CurrentWorkers, result.Workers.Tier))
	b.WriteString(fmt.Sprintf("| Circuit Breaker | %s |\n", result.Circuit.State))

	if len(result.Errors) > 0 {
		b.WriteString("\n## 失败项\n\n")
		for _, e := range result.Errors {
			b.WriteString(fmt.Sprintf("- %s\n", e))
		}
	}
	if err := os.WriteFile(mdPath, []byte(b.String()), 0644); err != nil {
		return err
	}
	t.Logf("报告已生成: %s / %s", jsonPath, mdPath)
	return nil
}
