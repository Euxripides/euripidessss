package downloadscheduler

// RPC Recovery Provider 单元测试 + 调度器恢复链路集成测试
// （Token Transfer Multi-Provider Recovery Layer V1.0 §6/§9/§10）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/normalize"
	"github.com/etl/backend/internal/parquetdownload"
)

// ── mocks ──

// mockLogsRPC 模拟 eth_blockNumber + eth_getLogs（按区块范围键分发日志）。
type mockLogsRPC struct {
	latest uint64
	logs   map[string][]rpcLog // key: "{from}-{to}"
	// limitErrSpan 大于该区块跨度的查询返回结果超限错误（触发二分）。
	limitErrSpan uint64
	calls        []string
}

func (m *mockLogsRPC) Call(ctx context.Context, chainKey, method string, params any) (json.RawMessage, string, error) {
	switch method {
	case "eth_blockNumber":
		return json.RawMessage(fmt.Sprintf(`"0x%x"`, m.latest)), "mock", nil
	case "eth_getLogs":
		filter := params.([]any)[0].(map[string]any)
		from, _ := parseMockHex(filter["fromBlock"].(string))
		to, _ := parseMockHex(filter["toBlock"].(string))
		m.calls = append(m.calls, fmt.Sprintf("%d-%d", from, to))
		if m.limitErrSpan > 0 && to-from > m.limitErrSpan {
			return nil, "", fmt.Errorf("query returned more than 10000 results")
		}
		logs := m.logs[fmt.Sprintf("%d-%d", from, to)]
		if logs == nil {
			logs = []rpcLog{}
		}
		payload, _ := json.Marshal(logs)
		return payload, "mock", nil
	}
	return nil, "", fmt.Errorf("unexpected method %s", method)
}

func parseMockHex(value string) (uint64, error) {
	return strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 64)
}

// fakeRecoveryWriter 记录写入/合并调用。
type fakeRecoveryWriter struct {
	rows       []normalize.TokenTransfer
	taskKeys   []string
	mergeCalls int
}

func (f *fakeRecoveryWriter) WriteTokenTransfers(ctx context.Context, taskKey string, network chain.EVM, rows []normalize.TokenTransfer) (*parquetdownload.RecoveryWriteResult, error) {
	f.taskKeys = append(f.taskKeys, taskKey)
	f.rows = append(f.rows, rows...)
	return &parquetdownload.RecoveryWriteResult{
		ParquetPath: "mock-recovery.parquet", Rows: int64(len(rows)), TaskKey: taskKey,
		UniqueKey: "chain_id + transaction_hash + log_index + token_address",
	}, nil
}

func (f *fakeRecoveryWriter) MergeTokenTransfers(ctx context.Context, planID string, network chain.EVM) (*parquetdownload.RecoveryMergeStats, error) {
	f.mergeCalls++
	return &parquetdownload.RecoveryMergeStats{
		ChainKey: network.Key, RecoveryRows: int64(len(f.rows)), WarehouseRows: 5,
		MergedRows: int64(len(f.rows)) + 4, DuplicateRows: 1, MergedParquet: "mock-merge.parquet",
	}, nil
}

// ── 测试数据 ──

const (
	tokenAddr   = "0x55d398326f99059ff775485246999027b3197955"
	fromAddr    = "0x57136ea9b2be6cd4ad74c3ca5b24172f87c9cb8d"
	toAddr      = "0x238a03c0dcb0f0c4c4c5b6b7c8c9d0e1f2a3b4c5"
	txHash1     = "0xabc123"
	txHash2     = "0xdef456"
	topicFrom   = "0x00000000000000000000000057136ea9b2be6cd4ad74c3ca5b24172f87c9cb8d"
	topicTo     = "0x000000000000000000000000238a03c0dcb0f0c4c4c5b6b7c8c9d0e1f2a3b4c5"
	amountData1 = "0x00000000000000000000000000000000000000000000000000000000000003e8" // 1000
	amountData2 = "0x000000000000000000000000000000000000000000000000000000000000000a" // 10
)

func sampleLog(block, logIndex uint64, txHash, data string) rpcLog {
	return rpcLog{
		Address: tokenAddr, Topics: []string{TransferTopic, topicFrom, topicTo},
		Data: data, BlockNumber: fmt.Sprintf("0x%x", block),
		TransactionHash: txHash, LogIndex: fmt.Sprintf("0x%x", logIndex),
	}
}

// ── 单元测试 ──

func TestParseTransferLog(t *testing.T) {
	network := chain.EVM{Key: "bsc", ID: 56}
	transfer, ok := parseTransferLog(network, sampleLog(1000, 5, txHash1, amountData1))
	if !ok {
		t.Fatal("valid Transfer log should parse")
	}
	if transfer.TxHash != txHash1 || transfer.LogIndex != 5 || transfer.BlockNumber != 1000 {
		t.Fatalf("key fields mismatch: %+v", transfer)
	}
	if transfer.FromAddress != fromAddr || transfer.ToAddress != toAddr {
		t.Fatalf("address parsing mismatch: from=%s to=%s", transfer.FromAddress, transfer.ToAddress)
	}
	if transfer.AmountRaw != "1000" || transfer.TokenAddress != tokenAddr {
		t.Fatalf("amount/token mismatch: %+v", transfer)
	}
	if transfer.Standard != "BEP20" || transfer.ChainID != 56 || transfer.ChainKey != "bsc" {
		t.Fatalf("chain/standard mismatch: %+v", transfer)
	}
	if transfer.BlockTime != (time.Time{}) {
		t.Fatal("RPC recovery must not fabricate block_time")
	}

	eth := chain.EVM{Key: "eth", ID: 1}
	if transfer, ok = parseTransferLog(eth, sampleLog(1, 0, txHash1, amountData1)); !ok || transfer.Standard != "ERC20" {
		t.Fatal("eth chain should be ERC20")
	}

	cases := []struct {
		name string
		item rpcLog
	}{
		{"topics 不足", rpcLog{Topics: []string{TransferTopic}}},
		{"topic0 不匹配", rpcLog{Topics: []string{"0x1111111111111111111111111111111111111111111111111111111111111111", topicFrom, topicTo}}},
		{"data 为空", rpcLog{Address: tokenAddr, Topics: []string{TransferTopic, topicFrom, topicTo}, Data: "0x"}},
		{"topic 非 64 hex", rpcLog{Topics: []string{TransferTopic, "0x1234", topicTo}, Data: amountData1}},
		{"blockNumber 非法", rpcLog{Address: tokenAddr, Topics: []string{TransferTopic, topicFrom, topicTo}, Data: amountData1, BlockNumber: "zzz"}},
		{"logIndex 非法", rpcLog{Address: tokenAddr, Topics: []string{TransferTopic, topicFrom, topicTo}, Data: amountData1, BlockNumber: "0x64", TransactionHash: txHash1, LogIndex: "zzz"}},
	}
	for _, tc := range cases {
		if _, ok := parseTransferLog(network, tc.item); ok {
			t.Fatalf("%s: expected parse failure", tc.name)
		}
	}
}

func TestRPCProviderTokenTransferRecovery(t *testing.T) {
	client := &mockLogsRPC{
		latest: 1_000_000,
		logs: map[string][]rpcLog{
			"107200-157200": {sampleLog(110000, 5, txHash1, amountData1), sampleLog(110001, 9, txHash2, amountData2)},
			// 跨分块重复（同唯一键）→ 应被去重
			"157201-207201": {sampleLog(110000, 5, txHash1, amountData1)},
		},
	}
	writer := &fakeRecoveryWriter{}
	provider := NewRPCProvider(client).WithRecovery(writer)
	req := Requirement{
		ID: "plan-1", Dataset: DatasetTokenTransfer, ChainKey: "bsc",
		Addresses: []string{fromAddr, toAddr},
		StartDate: "2026-07-01", EndDate: "2026-07-31",
	}
	result, err := provider.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.Rows != 2 {
		t.Fatalf("expected 2 unique rows after dedup, got %d", result.Rows)
	}
	if !result.NewData || result.Output != "recovery 1 parts" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(writer.taskKeys) != 1 || writer.taskKeys[0] != "plan-1-b1" {
		t.Fatalf("writer task key mismatch: %v", writer.taskKeys)
	}
	if len(writer.rows) != 2 {
		t.Fatalf("writer rows mismatch: %d", len(writer.rows))
	}
	if !strings.Contains(result.Summary, "唯一键") {
		t.Fatalf("summary should mention unique key: %s", result.Summary)
	}
}

func TestRPCProviderTokenTransferNoWriter(t *testing.T) {
	provider := NewRPCProvider(&mockLogsRPC{latest: 100})
	_, err := provider.Execute(context.Background(), Requirement{
		ID: "plan-1", Dataset: DatasetTokenTransfer, ChainKey: "bsc", Addresses: []string{fromAddr},
	})
	if err == nil || !strings.Contains(err.Error(), "恢复写入器未装配") {
		t.Fatalf("expected writer-missing error, got %v", err)
	}
}

func TestRPCProviderTokenTransferBisect(t *testing.T) {
	// 跨度 > 10 块报结果超限 → 应二分收窄到 ≤10 块后成功
	client := &mockLogsRPC{
		latest: 100, limitErrSpan: 10,
		logs: map[string][]rpcLog{
			"0-6":    {sampleLog(3, 1, txHash1, amountData1)},
			"95-100": {sampleLog(97, 3, txHash2, amountData2)},
		},
	}
	writer := &fakeRecoveryWriter{}
	provider := NewRPCProvider(client).WithRecovery(writer)
	req := Requirement{
		ID: "plan-1", Dataset: DatasetTokenTransfer, ChainKey: "bsc",
		Addresses: []string{fromAddr},
		StartDate: "2026-07-31", EndDate: "2026-07-31", // 1 天窗口
	}
	result, err := provider.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("bisect execute failed: %v", err)
	}
	if result.Rows != 2 {
		t.Fatalf("expected 2 rows via bisect, got %d", result.Rows)
	}
	// 验证确实发生过二分（有超限跨度调用）
	sawWide := false
	for _, call := range client.calls {
		var from, to uint64
		fmt.Sscanf(call, "%d-%d", &from, &to)
		if to-from > 10 {
			sawWide = true
		}
	}
	if !sawWide {
		t.Fatal("expected wide range calls that triggered bisect")
	}
}

func TestRecoveryWindow(t *testing.T) {
	// 默认窗口：90 天
	from, to, note := recoveryWindow(Requirement{}, 1_000_000, "bsc")
	if to != 1_000_000 || from != 0 {
		t.Fatalf("default window should clamp to genesis: %d-%d", from, to)
	}
	if !strings.Contains(note, "90") {
		t.Fatalf("default note should mention 90 days: %s", note)
	}
	// 显式窗口 31 天 → 31*86400/3 = 892800 块
	req := Requirement{StartDate: "2026-07-01", EndDate: "2026-07-31"}
	from, to, note = recoveryWindow(req, 1_000_000, "bsc")
	if from != 107_200 || to != 1_000_000 {
		t.Fatalf("31-day bsc window mismatch: %d-%d", from, to)
	}
	if !strings.Contains(note, "31") {
		t.Fatalf("note should mention 31 days: %s", note)
	}
	// 超过 180 天 → clamp 到 180
	longReq := Requirement{StartDate: "2020-01-01", EndDate: "2026-07-31"}
	from, _, _ = recoveryWindow(longReq, 10_000_000, "bsc")
	// 180 天 ≈ 5,184,000 块
	if from != 10_000_000-5_184_000 {
		t.Fatalf("clamped window mismatch: from=%d", from)
	}
	// 未知链 → 默认 12s/块
	from, _, _ = recoveryWindow(req, 1_000_000, "avax")
	if from != 1_000_000-31*86400/12 {
		t.Fatalf("unknown chain window mismatch: from=%d", from)
	}
}

func TestTokenTransferKey(t *testing.T) {
	key := tokenTransferKey(56, txHash1, 5, tokenAddr)
	if key != fmt.Sprintf("56|%s|5|%s", txHash1, tokenAddr) {
		t.Fatalf("unexpected unique key: %s", key)
	}
	// 大小写不敏感
	if tokenTransferKey(56, strings.ToUpper(txHash1), 5, strings.ToUpper(tokenAddr)) != key {
		t.Fatal("unique key must be case-insensitive")
	}
}

// ── 集成测试：SQD 失败 → 自动切换 RPC 恢复 → MERGING 合并 ──

func TestSchedulerSQDFailureFallsBackToRPCAndMerges(t *testing.T) {
	ctx := context.Background()
	sqdEngine := &mockSQDEngine{startErr: fmt.Errorf("SQD HTTP 503: No available workers")}
	rpcClient := &mockLogsRPC{
		latest: 100,
		logs: map[string][]rpcLog{
			"0-100": {sampleLog(50, 1, txHash1, amountData1), sampleLog(60, 2, txHash2, amountData2)},
		},
	}
	writer := &fakeRecoveryWriter{}
	registry := NewRegistry(
		NewRPCProvider(rpcClient).WithRecovery(writer),
		NewSQDProvider(sqdEngine),
	)
	scheduler := NewScheduler(registry, NewCoverageResolver(nil), "", DefaultBudget())
	scheduler.WithRecoveryWriter(writer)

	plan, err := scheduler.Submit(ctx, []Requirement{{
		Dataset: DatasetTokenTransfer, ChainKey: "bsc", Addresses: []string{fromAddr},
		StartDate: "2026-07-01", EndDate: "2026-07-31",
	}})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	task := plan.Tasks[0]
	if task.Provider != ProviderSQD {
		t.Fatalf("SQD healthy → should be selected first, got %s", task.Provider)
	}
	// 日期范围应传播到任务（RPC 恢复通道按此估算区块窗口）
	if task.Requirement.StartDate != "2026-07-01" || task.Requirement.EndDate != "2026-07-31" {
		t.Fatalf("dates should propagate to task: %+v", task.Requirement)
	}
	if len(task.Candidates) != 2 || task.Candidates[1].Provider != ProviderRPC {
		t.Fatalf("token_transfer candidates should be [sqd, rpc]: %+v", task.Candidates)
	}
	if err := scheduler.Run(ctx, plan.ID); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	// 轮询直到终态
	deadline := time.Now().Add(10 * time.Second)
	for {
		current := scheduler.Plan(plan.ID)
		if current.Status.Terminal() {
			if current.Status != StatusReady {
				t.Fatalf("plan should be READY_FOR_GRAPH, got %s: %s", current.Status, current.StageDetail)
			}
			task = current.Tasks[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("plan did not reach terminal state in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if task.Status != "done" || task.Provider != ProviderRPC {
		t.Fatalf("task should fall back to RPC and finish: %s/%s", task.Status, task.Provider)
	}
	if task.Result == nil || task.Result.Rows != 2 || !task.Result.NewData {
		t.Fatalf("unexpected task result: %+v", task.Result)
	}
	if task.Retries < 1 {
		t.Fatalf("expected at least 1 retry before fallback, got %d", task.Retries)
	}
	current := scheduler.Plan(plan.ID)
	if len(current.Recovery) != 1 || current.Recovery[0].MergedRows == 0 {
		t.Fatalf("MERGING should record recovery stats: %+v", current.Recovery)
	}
	if writer.mergeCalls != 1 {
		t.Fatalf("expected 1 merge call, got %d", writer.mergeCalls)
	}
}
