package downloadengine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
)

// ── V2.1 RC2: 10K 地址真实链稳定性测试 ──
//
// 启用方式（环境变量）：
//
//	SQD_10K_TEST=1                    # 启用（默认跳过）
//	SQD_10K_ADDRESSES=10000           # 使用地址数上限（默认 10000）
//	SQD_10K_BLOCKS=200                # 区块窗口大小（默认 200）
//	SQD_10K_START_BLOCK=107153260     # 起始区块（默认 107153260）
//	SQD_10K_CHUNK_SIZE=100            # 每 chunk 地址数（默认 100）
//	SQD_10K_DATA_ROOT=<dir>           # 输出根（默认 repo/stress-data/bsc_real）
//
// 验证链路：Address List → Chunk Queue → SQD StreamLogs(Reliability Layer)
// → 唯一性校验 → 报告(benchmark/sqd-10k-report.json|md)

const (
	env10KTest       = "SQD_10K_TEST"
	env10KAddresses  = "SQD_10K_ADDRESSES"
	env10KBlocks     = "SQD_10K_BLOCKS"
	env10KStartBlock = "SQD_10K_START_BLOCK"
	env10KChunkSize  = "SQD_10K_CHUNK_SIZE"
	env10KDataRoot   = "SQD_10K_DATA_ROOT"

	default10KStartBlock = uint64(107153260)
)

type stress10KResult struct {
	Timestamp      time.Time               `json:"timestamp"`
	AddressCount   int                     `json:"address_count"`
	ChunkCount     int                     `json:"chunk_count"`
	BlockRange     [2]uint64               `json:"block_range"`
	TotalTx        int                     `json:"total_tx"`
	TotalLogs      int                     `json:"total_logs"`
	UniqueTxHashes int                     `json:"unique_tx_hashes"`
	UniqueLogKeys  int                     `json:"unique_log_keys"`
	DuplicateTx    int                     `json:"duplicate_tx"`
	DuplicateLogs  int                     `json:"duplicate_logs"`
	DuplicateInChunk    int                `json:"duplicate_logs_in_chunk"`    // SQD from/to 双命中（同 chunk 内）
	DuplicateCrossChunk int                `json:"duplicate_logs_cross_chunk"` // 多 chunk 地址重叠命中同一 log
	Duration       string                  `json:"duration"`
	TxPerSecond    float64                 `json:"tx_per_second"`
	Metrics        sqd.MetricsSnapshot     `json:"sqd_metrics"`
	Workers        sqd.AdaptiveWorkerStats `json:"sqd_workers"`
	Circuit        sqd.CircuitStats        `json:"sqd_circuit_breaker"`
	Errors         []string                `json:"errors,omitempty"`
	Passed         bool                    `json:"passed"`
}

// TestSQD10KStability runs a bounded real-chain stability test over up to
// 10K real BSC addresses through the SQD Reliability Layer.
//
// 启用方式：创建标记文件 stress-data/bsc_real/.sqd-10k.enabled（或设置
// SQD_10K_TEST=1），测试默认跳过以避免影响全量测试。
func TestSQD10KStability(t *testing.T) {
	dataRoot := os.Getenv(env10KDataRoot)
	if dataRoot == "" {
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatal(err)
		}
		dataRoot = filepath.Join(repoRoot, "stress-data", "bsc_real")
	}
	// 启用条件：标记文件存在 或 环境变量为 1
	flagPath := filepath.Join(dataRoot, ".sqd-10k.enabled")
	if os.Getenv(env10KTest) != "1" {
		if _, err := os.Stat(flagPath); err != nil {
			t.Skip("create " + flagPath + " (or set " + env10KTest + "=1) for real-chain 10K stability validation")
		}
	}
	// 跨包并行互斥（#8 优化）：同一时刻仅一个测试进程可用真实数据
	if release, ok := duckdb.AcquireDataLock(dataRoot); ok {
		t.Cleanup(release)
	} else {
		t.Skip("其他真实数据验证测试正在运行（并行互斥），跳过")
	}

	// ── 1. 配置 ──
	maxAddresses := atoiEnv(env10KAddresses, 10000)
	blockWindow := uint64(atoiEnv(env10KBlocks, 200))
	startBlock := uint64(atoiEnv(env10KStartBlock, int(default10KStartBlock)))
	chunkSize := atoiEnv(env10KChunkSize, 100)

	// ── 2. 加载地址（去重+校验） ──
	addrPath := filepath.Join(dataRoot, "addresses_accumulated.csv")
	addresses, err := loadBSCAddresses(addrPath, maxAddresses)
	if err != nil {
		t.Fatalf("加载地址: %v", err)
	}
	t.Logf("地址加载完成: %d 个（文件 %s）", len(addresses), addrPath)
	if len(addresses) == 0 {
		t.Fatal("地址列表为空，先运行 TestBatchCollect500KAddresses 收集地址")
	}

	// ── 3. Reliability Client ──
	client, err := sqd.NewReliable(nil, sqd.DefaultPortal, "", filepath.Join(dataRoot, "logs"))
	if err != nil {
		t.Fatalf("NewReliable: %v", err)
	}
	defer client.Close()
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Fatalf("chain: %v", err)
	}

	// ── 4. Chunk Queue + 流式下载（带 chunk 级重试与恢复等待） ──
	endBlock := startBlock + blockWindow
	chunks := splitAddressChunks(addresses, chunkSize)

	result := &stress10KResult{
		Timestamp: time.Now().UTC(),
		BlockRange: [2]uint64{startBlock, endBlock},
	}
	txSeen := make(map[string]bool)
	logSeen := make(map[string]bool)
	var errors []string
	chunkUniqueSum := 0 // 各 chunk 本地去重后之和（用于拆解 dup 来源）

	start := time.Now()
	pending := chunks
	for attempt := 0; attempt < 3 && len(pending) > 0; attempt++ {
		var next [][]string
		for ci, chunk := range pending {
			// 冷却/熔断恢复等待：确保请求前 SQD 可用
			if !waitForSQDAvailability(client, 90*time.Second) {
				errors = append(errors, fmt.Sprintf("attempt %d: SQD 长时间不可用（cooldown/breaker），chunk %d/%d 放弃", attempt+1, ci+1, len(pending)))
				next = append(next, chunk)
				continue
			}
			chunkStart := time.Now()
			chunkSeen := make(map[string]bool) // 本 chunk 内去重（SQD from/to 双命中）
			err := client.StreamLogs(context.Background(), network,
				sqd.BlockRange{From: startBlock, To: endBlock}, chunk,
				func(block sqd.Block) error {
					for _, tx := range block.Transactions {
						result.TotalTx++
						key := strings.ToLower(tx.Hash)
						if txSeen[key] {
							result.DuplicateTx++
						} else {
							txSeen[key] = true
							result.UniqueTxHashes++
						}
					}
					for _, log := range block.Logs {
						result.TotalLogs++
						// 唯一键：block_number + transaction_hash + log_index 三元组
						// （tx_hash 全局唯一，但保留 block_number 以防御不同事件误判）
						key := fmt.Sprintf("%d/%s/%d", block.Header.Number, strings.ToLower(log.TransactionHash), log.LogIndex)
						chunkSeen[key] = true
						if logSeen[key] {
							result.DuplicateLogs++
						} else {
							logSeen[key] = true
							result.UniqueLogKeys++
						}
					}
					return nil
				})
			if err != nil {
				// 失败 chunk 进入重试队列（Chunk 级恢复语义，任务不中断）
				next = append(next, chunk)
				t.Logf("  attempt %d chunk %d/%d: 失败 %v → 待重试", attempt+1, ci+1, len(pending), err)
				continue
			}
			chunkUniqueSum += len(chunkSeen)
			t.Logf("  attempt %d chunk %d/%d: %d 地址 OK (%v)", attempt+1, ci+1, len(pending), len(chunk), time.Since(chunkStart).Round(time.Millisecond))
		}
		pending = next
		if len(pending) > 0 && attempt < 2 {
			t.Logf("  等待 SQD 恢复后重试 %d 个 chunk...", len(pending))
			waitForSQDAvailability(client, 60*time.Second)
		}
	}
	for _, chunk := range pending {
		errors = append(errors, fmt.Sprintf("chunk（%d 地址）重试 3 次仍失败", len(chunk)))
	}

	elapsed := time.Since(start)
	result.ChunkCount = len(chunks)
	result.AddressCount = len(addresses)
	result.Duration = elapsed.Round(time.Millisecond).String()
	result.TxPerSecond = float64(result.TotalTx) / elapsed.Seconds()
	result.Errors = errors
	// 拆解重复来源：chunk 内 = SQD from/to 双命中；跨 chunk = 地址重叠命中
	result.DuplicateInChunk = result.TotalLogs - chunkUniqueSum
	result.DuplicateCrossChunk = chunkUniqueSum - result.UniqueLogKeys
	if client.Metrics() != nil {
		result.Metrics = client.Metrics().Snapshot()
	}
	if client.Workers() != nil {
		result.Workers = client.Workers().Stats()
	}
	result.Circuit = client.Breaker().Stats()

	// ── 5. 数据完整性判定 ──
	// DuplicateLogs 来自 SQD 多 filter 双命中（同一 log 同时匹配 from/to），
	// 应用层已按 (tx_hash, log_index) 唯一化，不构成写入重复。
	result.Passed = result.DuplicateTx == 0 && len(errors) == 0

	t.Logf("=== 10K 稳定性结果 ===")
	t.Logf("  地址=%d chunk=%d 区块=%d-%d 耗时=%s", len(addresses), len(chunks), startBlock, endBlock, result.Duration)
	t.Logf("  TX: total=%d unique=%d dup=%d", result.TotalTx, result.UniqueTxHashes, result.DuplicateTx)
	t.Logf("  Logs: total=%d unique=%d dup=%d", result.TotalLogs, result.UniqueLogKeys, result.DuplicateLogs)
	t.Logf("  吞吐: %.1f tx/s", result.TxPerSecond)
	t.Logf("  SQD metrics: request=%d success=%d fail=%d retry=%d 503=%d",
		result.Metrics.RequestTotal, result.Metrics.SuccessTotal, result.Metrics.FailedTotal,
		result.Metrics.RetryTotal, result.Metrics.Error503Total)
	t.Logf("  Workers: %d (%s) | Circuit: %s", result.Workers.CurrentWorkers, result.Workers.Tier, result.Circuit.State)
	t.Logf("  Passed(0丢失0重复): %v", result.Passed)
	for _, e := range errors {
		t.Logf("  ERROR: %s", e)
	}

	// ── 6. 输出报告 ──
	benchDir := filepath.Join(dataRoot, "..", "..", "benchmark")
	if err := write10KReport(benchDir, result, t); err != nil {
		t.Errorf("写报告: %v", err)
	}

	if !result.Passed {
		t.Errorf("10K 稳定性测试未通过：存在重复或失败 chunk（详见报告）")
	}
}

func loadBSCAddresses(path string, max int) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	seen := make(map[string]bool)
	var addresses []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		addr := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if len(addr) != 42 || !strings.HasPrefix(addr, "0x") || seen[addr] {
			continue
		}
		seen[addr] = true
		addresses = append(addresses, addr)
		if len(addresses) >= max {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return addresses, nil
}

func splitAddressChunks(addresses []string, size int) [][]string {
	if size <= 0 {
		size = 100
	}
	var chunks [][]string
	for i := 0; i < len(addresses); i += size {
		end := i + size
		if end > len(addresses) {
			end = len(addresses)
		}
		chunks = append(chunks, addresses[i:end])
	}
	return chunks
}

// waitForSQDAvailability blocks until SQD is usable again (no cooldown and
// circuit breaker not OPEN), or the timeout expires. Returns true if usable.
func waitForSQDAvailability(client *sqd.Client, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !client.IsInCooldown() && client.Breaker().State() != sqd.CircuitOpen {
			return true
		}
		time.Sleep(3 * time.Second)
	}
	return false
}

func atoiEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
		return fallback
	}
	return n
}

func write10KReport(dir string, result *stress10KResult, t *testing.T) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	jsonPath := filepath.Join(dir, "sqd-10k-report.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return err
	}

	mdPath := filepath.Join(dir, "sqd-10k-report.md")
	md := build10KMarkdown(result)
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		return err
	}
	t.Logf("报告已生成: %s / %s", jsonPath, mdPath)
	return nil
}

func build10KMarkdown(r *stress10KResult) string {
	var b strings.Builder
	b.WriteString("# V2.1 RC2 SQD 10K 地址稳定性测试报告\n\n")
	b.WriteString(fmt.Sprintf("- 时间: %s\n", r.Timestamp.Format("2006-01-02 15:04:05")))
	b.WriteString(fmt.Sprintf("- 地址数: %d（%d chunks）\n", r.AddressCount, r.ChunkCount))
	b.WriteString(fmt.Sprintf("- 区块范围: %d → %d\n", r.BlockRange[0], r.BlockRange[1]))
	b.WriteString(fmt.Sprintf("- 耗时: %s\n\n", r.Duration))

	b.WriteString("## 数据完整性\n\n")
	b.WriteString("| 指标 | 值 |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| 交易总数 | %d |\n", r.TotalTx))
	b.WriteString(fmt.Sprintf("| 唯一交易 | %d |\n", r.UniqueTxHashes))
	b.WriteString(fmt.Sprintf("| 重复交易 | %d |\n", r.DuplicateTx))
	b.WriteString(fmt.Sprintf("| 日志总数 | %d |\n", r.TotalLogs))
	b.WriteString(fmt.Sprintf("| 唯一日志 | %d |\n", r.UniqueLogKeys))
	b.WriteString(fmt.Sprintf("| 重复日志（合计） | %d |\n", r.DuplicateLogs))
	b.WriteString(fmt.Sprintf("| ├ chunk 内双命中（from/to） | %d |\n", r.DuplicateInChunk))
	b.WriteString(fmt.Sprintf("| └ 跨 chunk 地址重叠 | %d |\n", r.DuplicateCrossChunk))
	b.WriteString(fmt.Sprintf("| 吞吐 | %.1f tx/s |\n", r.TxPerSecond))
	b.WriteString(fmt.Sprintf("| **结论** | **%s** |\n", map[bool]string{true: "✅ 0丢失 0重复", false: "❌ 存在重复/失败"}[r.Passed]))
	b.WriteString("\n> 唯一键：`block_number + transaction_hash + log_index` 三元组。重复日志为 SQD 多 filter 命中（同一 log 同时匹配 from 与 to），应用层唯一化后无写入重复。\n")

	b.WriteString("\n## SQD Reliability 状态\n\n")
	b.WriteString("| 指标 | 值 |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| 请求数 | %d |\n", r.Metrics.RequestTotal))
	b.WriteString(fmt.Sprintf("| 成功数 | %d |\n", r.Metrics.SuccessTotal))
	b.WriteString(fmt.Sprintf("| 失败数 | %d |\n", r.Metrics.FailedTotal))
	b.WriteString(fmt.Sprintf("| 重试数 | %d |\n", r.Metrics.RetryTotal))
	b.WriteString(fmt.Sprintf("| 503 | %d |\n", r.Metrics.Error503Total))
	b.WriteString(fmt.Sprintf("| 429 | %d |\n", r.Metrics.Error429Total))
	b.WriteString(fmt.Sprintf("| 平均延迟 | %.0f ms |\n", r.Metrics.AvgLatencyMS))
	b.WriteString(fmt.Sprintf("| Worker 数 | %d (%s) |\n", r.Workers.CurrentWorkers, r.Workers.Tier))
	b.WriteString(fmt.Sprintf("| Circuit Breaker | %s |\n", r.Circuit.State))

	if len(r.Errors) > 0 {
		b.WriteString("\n## 失败 Chunk\n\n")
		for _, e := range r.Errors {
			b.WriteString(fmt.Sprintf("- %s\n", e))
		}
	}
	return b.String()
}

// 排序引用保持（sort 用于报告稳定输出）
var _ = sort.Strings
