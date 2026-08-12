package smartdownload

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/cryptodownload"
)

// BlockTimeResolver resolves a block number to its canonical UTC timestamp.
// CSV exports are time-window based while Smart Download ranges are block based,
// so production CSV execution is disabled unless this resolver is available.
type BlockTimeResolver func(context.Context, string, uint64) (time.Time, error)

type csvExportCollector interface {
	CollectAddress(context.Context, cryptodownload.Config, string) (cryptodownload.ExportData, error)
	Close() error
}

// CSVAdapter connects the current OKLink CSV exporter to Smart Download. It is
// deliberately AUTO-only: TURBO/EMERGENCY must remain bounded, low-latency paths.
type CSVAdapter struct {
	configDir        string
	rawRoot          string
	resolveBlockTime BlockTimeResolver
	loadConfig       func(string, string) (cryptodownload.Config, error)
	newCollector     func(cryptodownload.Config) csvExportCollector
	runtimeReady     func(string) error
}

// NewCSVAdapter keeps the zero-config constructor for diagnostics/tests. Such
// an adapter is unavailable until configured with a resolver and local secrets.
func NewCSVAdapter() *CSVAdapter {
	return &CSVAdapter{loadConfig: cryptodownload.LoadCSVAutomationConfig,
		runtimeReady: cryptodownload.ValidateCSVAutomationRuntime,
		newCollector: func(cfg cryptodownload.Config) csvExportCollector {
			return cryptodownload.NewCSVExportClient(cfg)
		}}
}

// NewProductionCSVAdapter creates an executable CSV lane backed by the settings
// saved on the Browser Download page. Passwords never cross the adapter/API boundary.
func NewProductionCSVAdapter(configDir, rawRoot string, resolver BlockTimeResolver) *CSVAdapter {
	p := NewCSVAdapter()
	p.configDir, p.rawRoot, p.resolveBlockTime = configDir, rawRoot, resolver
	return p
}

func (p *CSVAdapter) Name() string { return "csv" }

func (p *CSVAdapter) Available() bool {
	if p == nil || p.resolveBlockTime == nil || p.loadConfig == nil || p.newCollector == nil || p.runtimeReady == nil ||
		strings.TrimSpace(p.configDir) == "" || strings.TrimSpace(p.rawRoot) == "" {
		return false
	}
	cfg, err := p.loadConfig(p.configDir, p.rawRoot)
	if err != nil {
		return false
	}
	return p.runtimeReady(cfg.BaseURL) == nil
}

func (p *CSVAdapter) AvailableForChain(chainKey string) bool {
	if !p.Available() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(chainKey)) {
	case "bsc", "eth", "ethereum":
		return true
	default:
		return false
	}
}

func (p *CSVAdapter) AvailableForMode(chainKey string, mode DownloadMode) bool {
	return mode == DownloadModeAuto && p.AvailableForChain(chainKey)
}

func (p *CSVAdapter) ManualOnly() bool { return false }

func (p *CSVAdapter) Supports(dataset string) bool {
	return dataset == DatasetTransactions || dataset == DatasetTokenTransfers
}

// Probe intentionally does not submit an export request. CSV availability is
// represented by CandidatesFor; without a real count, confidence must remain 0
// so L6 cross-check never treats an unknown estimate as authoritative zero.
func (p *CSVAdapter) Probe(_ context.Context, req ProbeRequest) (ProbeResult, error) {
	if !p.AvailableForChain(req.ChainKey) || !p.Supports(req.Dataset) {
		return ProbeResult{Confidence: 0}, nil
	}
	return ProbeResult{FirstBlock: req.FromBlock, LastBlock: req.ToBlock,
		Confidence: 0, ProbeProvider: p.Name()}, nil
}

func (p *CSVAdapter) ExecuteRange(ctx context.Context, req RangeRequest) (*ProviderResult, error) {
	if !p.AvailableForMode(req.ChainKey, req.Mode) {
		return nil, fmt.Errorf("CSV 生产通道不可用: 请检查链、模式、邮箱配置和区块时间解析器")
	}
	if !p.Supports(req.Dataset) {
		return nil, fmt.Errorf("CSV 不支持数据集 %s", req.Dataset)
	}
	if req.ToBlock < req.FromBlock {
		return nil, fmt.Errorf("CSV 区块范围无效: %d..%d", req.FromBlock, req.ToBlock)
	}
	start, err := p.resolveBlockTime(ctx, req.ChainKey, req.FromBlock)
	if err != nil {
		return nil, fmt.Errorf("CSV 解析起始区块时间: %w", err)
	}
	end, err := p.resolveBlockTime(ctx, req.ChainKey, req.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("CSV 解析结束区块时间: %w", err)
	}
	if end.Before(start) {
		return nil, fmt.Errorf("CSV 区块时间倒序: %s > %s", start, end)
	}
	jobID, err := safeCSVJobID(req.DatasetJobID)
	if err != nil {
		return nil, err
	}
	rawDir := filepath.Join(p.rawRoot, jobID)
	cfg, err := p.loadConfig(p.configDir, rawDir)
	if err != nil {
		return nil, err
	}
	cfg.Address = strings.ToLower(strings.TrimSpace(req.Address))
	cfg.Chains = []string{strings.ToLower(strings.TrimSpace(req.ChainKey))}
	cfg.CSVStartTime, cfg.CSVEndTime = start.Unix(), end.Unix()
	switch req.Dataset {
	case DatasetTransactions:
		cfg.Protocols = []string{"transaction"}
	case DatasetTokenTransfers:
		cfg.Protocols = []string{"token"}
	}
	collector := p.newCollector(cfg)
	if collector == nil {
		return nil, fmt.Errorf("CSV 客户端创建失败")
	}
	defer collector.Close()
	data, err := collector.CollectAddress(ctx, cfg, req.ChainKey)
	if err != nil {
		return nil, fmt.Errorf("CSV 下载失败: %w", err)
	}
	if len(data.Errors) > 0 {
		return nil, fmt.Errorf("CSV 下载未达到质量要求: %s", strings.Join(data.Errors, "; "))
	}
	if len(data.CSVDownloadChecks) != 1 {
		return nil, fmt.Errorf("CSV 下载质量报告数量异常: %d", len(data.CSVDownloadChecks))
	}
	check := data.CSVDownloadChecks[0]
	if check.Status == "failed" || check.Status == "incomplete" || check.Status == "pending" {
		return nil, fmt.Errorf("CSV 下载未完成: status=%s downloaded=%d expected=%d note=%s",
			check.Status, check.Downloaded, check.ExpectedTotal, check.Note)
	}
	if req.Dataset == DatasetTokenTransfers {
		if check.ExpectedTotal < 0 {
			return nil, fmt.Errorf("CSV Token 缺少 OKLink 网站计数，无法核对下载完整性")
		}
		if difference := csvCountDifference(check.ExpectedTotal, check.Downloaded); difference > 100 {
			return nil, fmt.Errorf("CSV Token 数量核对失败: OKLink=%d downloaded=%d difference=%d tolerance=100",
				check.ExpectedTotal, check.Downloaded, difference)
		}
	}
	records, err := csvExportRecords(req, data)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		return nil, fmt.Errorf("CSV 结果计量失败: %w", err)
	}
	return &ProviderResult{Records: records, Bytes: uint64(len(encoded)), CompletedTo: req.ToBlock}, nil
}

func safeCSVJobID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 128 {
		return "", fmt.Errorf("CSV dataset job id 无效")
	}
	for _, r := range raw {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("CSV dataset job id 包含非法字符")
	}
	return raw, nil
}

func csvExportRecords(req RangeRequest, data cryptodownload.ExportData) ([]Record, error) {
	rows := data.Transactions
	if req.Dataset == DatasetTokenTransfers {
		rows = data.TokenTransfers
	}
	records := make([]Record, 0, len(rows))
	for i, row := range rows {
		block, err := csvUint(row["height"])
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行区块高度无效: %w", i+1, err)
		}
		if block < req.FromBlock || block > req.ToBlock {
			continue
		}
		hash := strings.ToLower(csvText(row["txId"]))
		if hash == "" {
			return nil, fmt.Errorf("CSV 第 %d 行缺少交易哈希", i+1)
		}
		from, to := strings.ToLower(csvText(row["from"])), strings.ToLower(csvText(row["to"]))
		if !strings.EqualFold(from, req.Address) && !strings.EqualFold(to, req.Address) {
			return nil, fmt.Errorf("CSV 第 %d 行不属于请求地址", i+1)
		}
		blockTime := csvUnixSeconds(row["transactionTime"])
		if blockTime <= 0 {
			return nil, fmt.Errorf("CSV 第 %d 行缺少有效区块时间", i+1)
		}
		record := Record{ChainID: req.ChainID, BlockNumber: block, BlockTime: blockTime,
			TransactionHash: hash, Dataset: req.Dataset, Address: strings.ToLower(req.Address)}
		if req.Dataset == DatasetTransactions {
			record.LogIndex, _ = csvUint(row["transactionIndex"])
			record.Payload = map[string]any{
				"block_hash": csvText(row["blockHash"]), "from_address": from, "to_address": to,
				"value_raw": csvText(row["amount"]), "native_symbol": csvText(row["transactionSymbol"]),
				"method_id": csvText(row["methodId"]), "transaction_fee_native": csvText(row["txFee"]),
				"status": csvStatus(row["state"]),
			}
		} else {
			// OKLink's production token CSV currently omits the on-chain log_index.
			// The product completeness contract for this lane is therefore the
			// OKLink window count versus downloaded rows (validated in ExecuteRange),
			// with a tolerance of 100. Preserve every accepted CSV row by assigning
			// its stable export order as an internal surrogate instead of collapsing
			// all events to log_index=0. This value is not claimed as chain evidence.
			record.LogIndex = uint64(i)
			standard := "ERC20"
			if strings.EqualFold(req.ChainKey, "bsc") {
				standard = "BEP20"
			}
			record.Payload = map[string]any{
				"token_address": strings.ToLower(csvText(row["tokenContractAddress"])),
				"from_address":  from, "to_address": to, "value_raw": csvText(row["amount"]), "standard": standard,
			}
		}
		records = append(records, record)
	}
	return uniqueRecords(records), nil
}

func csvCountDifference(expected int64, downloaded int) int64 {
	difference := expected - int64(downloaded)
	if difference < 0 {
		return -difference
	}
	return difference
}

func csvText(value any) string { return strings.TrimSpace(fmt.Sprintf("%v", value)) }

func csvUint(value any) (uint64, error) {
	text := csvText(value)
	if text == "" || text == "<nil>" {
		return 0, fmt.Errorf("空值")
	}
	base := 10
	if strings.HasPrefix(strings.ToLower(text), "0x") {
		base, text = 16, text[2:]
	}
	return strconv.ParseUint(text, base, 64)
}

func csvUnixSeconds(value any) int64 {
	n, err := strconv.ParseInt(csvText(value), 10, 64)
	if err != nil {
		return 0
	}
	if n > 100_000_000_000 {
		return n / 1000
	}
	return n
}

func csvStatus(value any) int {
	switch strings.ToLower(csvText(value)) {
	case "success", "successful", "ok", "1", "true":
		return 1
	default:
		return 0
	}
}

// MockCSVProvider is a deterministic CSV provider used only by tests.
type MockCSVProvider = MockProvider

func NewMockCSVProvider() *MockCSVProvider { return NewMockNamedProvider("csv") }

func NewMockNamedProvider(name string) *MockProvider { return &MockProvider{name: name} }
