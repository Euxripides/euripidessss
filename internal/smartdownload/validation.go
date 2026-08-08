package smartdownload

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	v3 "github.com/etl/backend/internal/smartdownload/validation"
	"github.com/etl/backend/internal/writer"
)

// Validation 管道（实施方案 §21）：L1 文件 → L2 记录 → L3 覆盖 → L4 Provider 对账 → L5 补洞 → L6 交叉验证。
type CrossCheckReport struct {
	Status   string   `json:"status"` // PASS / SKIPPED / FAILED
	Windows  int      `json:"windows"`
	Compared int      `json:"compared"`
	Mismatch int      `json:"mismatches"`
	Details  []string `json:"details,omitempty"`
}

type ValidationReport struct {
	DatasetJobID      string           `json:"dataset_job_id"`
	Status            string           `json:"status"` // VALIDATED / PARTIAL / FAILED
	Score             float64          `json:"score"`
	Coverage          float64          `json:"coverage"`
	BlockCoverage     float64          `json:"block_coverage"`
	Rows              int64            `json:"rows"`
	UniqueKeyCount    int64            `json:"unique_key_count"`
	DuplicateCount    int64            `json:"duplicate_count"`
	RawRows           int64            `json:"raw_rows"`
	PartsDuplicateSHA int              `json:"parts_duplicate_sha"`
	ExpectedCount     int64            `json:"expected_count"`
	ActualCount       int64            `json:"actual_count"`
	UnknownRanges     []BlockRange     `json:"unknown_ranges,omitempty"`
	MissingRanges     []BlockRange     `json:"missing_ranges,omitempty"`
	LevelFile         bool             `json:"level_file"`
	LevelRecord       bool             `json:"level_record"`
	LevelCoverage     bool             `json:"level_coverage"`
	LevelProviderCnt  bool             `json:"level_provider_count"`
	LevelCrossCheck   bool             `json:"level_cross_check"`
	CrossCheck        CrossCheckReport `json:"cross_check"`
	Gaps              []v3.GapRecord   `json:"gaps,omitempty"`
	Errors            []string         `json:"errors,omitempty"`
	ValidatedAt       time.Time        `json:"validated_at"`
}

var (
	evmPattern  = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	hashPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

// Validator 校验器（L1-L6）。
type Validator struct {
	svc *Service
}

func NewValidator(svc *Service) *Validator { return &Validator{svc: svc} }

// ValidateDataset 执行完整校验并返回报告（不修改任务状态；补洞由 Service 执行）。
func (v *Validator) ValidateDataset(ctx context.Context, dsID string) (*ValidationReport, error) {
	report := &ValidationReport{
		DatasetJobID: dsID,
		Status:       "VALIDATED",
		ValidatedAt:  time.Now().UTC(),
	}
	ds := v.svc.store.GetDataset(dsID)
	if ds == nil {
		return nil, fmt.Errorf("dataset %s 不存在", dsID)
	}
	v.svc.events.Publish(Event{Type: EventValidationStarted, DatasetJobID: dsID, Status: "RUNNING"})
	cp, err := v.svc.cp.Load(dsID)
	if err != nil {
		return nil, err
	}
	ledgerEntries, _ := NewLedger(v.svc.store.Root(), dsID).Replay()
	partsDir := filepath.Join(v.svc.PartsDir(), dsID)

	// ── L1 文件级 ──
	report.LevelFile = true
	var recordsByPart []struct {
		name    string
		from    uint64
		to      uint64
		records []Record
	}
	for _, part := range cp.Parts {
		path := filepath.Join(partsDir, part.Name)
		if _, err := os.Stat(path); err != nil {
			report.LevelFile = false
			report.Errors = append(report.Errors, fmt.Sprintf("L1 文件缺失: %s", part.Name))
			continue
		}
		sha, err := fileSHA256(path)
		if err != nil || !strings.EqualFold(sha, part.SHA256) {
			report.LevelFile = false
			report.Errors = append(report.Errors, fmt.Sprintf("L1 SHA256 不匹配: %s", part.Name))
			continue
		}
		records, err := v.readPartRecords(ctx, path)
		if err != nil {
			report.LevelFile = false
			report.Errors = append(report.Errors, fmt.Sprintf("L1 读取失败 %s: %v", part.Name, err))
			continue
		}
		recordsByPart = append(recordsByPart, struct {
			name    string
			from    uint64
			to      uint64
			records []Record
		}{name: part.Name, from: part.RangeFrom, to: part.RangeTo, records: records})
	}
	if len(recordsByPart) == 0 && len(cp.CompletedRanges) > 0 {
		report.LevelFile = false
		report.Errors = append(report.Errors, "L1 无有效 Part（完成区间存在但没有可读数据）")
	}

	// ── L2 记录级 ──
	report.LevelRecord = true
	seen := map[string]bool{}
	var all []Record
	perPartUnique := map[string]int64{}
	for _, item := range recordsByPart {
		var uniq int64
		for _, r := range item.records {
			if r.BlockNumber < item.from || r.BlockNumber > item.to {
				report.LevelRecord = false
				report.Errors = append(report.Errors, fmt.Sprintf("L2 越界记录 %s: block %d 不在 [%d,%d]",
					item.name, r.BlockNumber, item.from, item.to))
			}
			if !validRecordFields(r) {
				report.LevelRecord = false
				report.Errors = append(report.Errors, fmt.Sprintf("L2 非法字段 %s: %s", item.name, r.TransactionHash))
			}
			key := CanonicalKey(r)
			if !seen[key] {
				seen[key] = true
				uniq++
			}
			all = append(all, r)
		}
		perPartUnique[item.name] = uniq
	}
	report.Rows = int64(len(all))
	report.UniqueKeyCount = int64(len(seen))
	report.DuplicateCount = report.Rows - report.UniqueKeyCount
	if report.DuplicateCount > 0 {
		report.LevelRecord = false
		report.Errors = append(report.Errors, fmt.Sprintf("L2 重复唯一键 %d 条", report.DuplicateCount))
	}

	// ── L3 覆盖：requested == completed + confirmed_empty，不允许未知洞 ──
	requested := rangesFromJobs(v.svc.store.ListRangesByDataset(dsID))
	if len(requested) == 0 {
		requested = SplitBlockRange(cp.RequestedFrom, cp.RequestedTo, v.svc.opts.RangeChunkSize)
	}
	done := RangeKeySet{}
	var completedIntervals, emptyIntervals []v3.BlockInterval
	for _, r := range cp.CompletedRanges {
		done.Add(r)
		completedIntervals = append(completedIntervals, v3.BlockInterval{From: r.From, To: r.To})
	}
	for _, r := range cp.ConfirmedEmptyRanges {
		done.Add(r)
		emptyIntervals = append(emptyIntervals, v3.BlockInterval{From: r.From, To: r.To})
	}
	for _, r := range requested {
		if !done.Has(r) {
			report.UnknownRanges = append(report.UnknownRanges, r)
		}
	}
	report.LevelCoverage = len(report.UnknownRanges) == 0
	report.BlockCoverage = v3.FromIntervals(append(append([]v3.BlockInterval(nil), completedIntervals...), emptyIntervals...)).
		CoverageRatio(cp.RequestedFrom, cp.RequestedTo)
	if len(requested) > 0 {
		report.Coverage = 1 - float64(len(report.UnknownRanges))/float64(len(requested))
	} else {
		report.Coverage = 1
	}
	if !report.LevelCoverage {
		report.MissingRanges = append([]BlockRange(nil), report.UnknownRanges...)
		report.Errors = append(report.Errors, fmt.Sprintf("L3 覆盖缺口 %d 个区间", len(report.UnknownRanges)))
	}

	// ── Gap Detection（Validation V3：RANGE_GAP / SUSPICIOUS_EMPTY / COUNT_GAP）──
	shaCount := map[string]int{}
	for _, p := range cp.Parts {
		shaCount[p.SHA256]++
	}
	for _, n := range shaCount {
		if n > 1 {
			report.PartsDuplicateSHA++
		}
	}
	rowsByRange := map[string]int64{}
	for _, e := range ledgerEntries {
		if e.Event == LedgerPartCommitted || e.Event == LedgerRangeEmpty {
			rowsByRange[fmt.Sprintf("%d_%d", e.FromBlock, e.ToBlock)] = e.Rows
		}
	}
	rangeGaps := v3.RangeGaps(v3.BlockInterval{From: cp.RequestedFrom, To: cp.RequestedTo},
		completedIntervals, emptyIntervals)
	var allRanges []v3.BlockInterval
	for _, r := range v.svc.store.ListRangesByDataset(dsID) {
		allRanges = append(allRanges, v3.BlockInterval{From: r.FromBlock, To: r.ToBlock})
	}
	suspicious := v3.SuspiciousEmpty(emptyIntervals, allRanges, rowsByRange, 50)
	for _, g := range rangeGaps {
		report.Gaps = append(report.Gaps, g)
	}
	for _, g := range suspicious {
		report.Gaps = append(report.Gaps, g)
	}
	if report.ExpectedCount != report.ActualCount {
		report.Gaps = append(report.Gaps, v3.GapRecord{
			GapID: "count_gap", Type: v3.GapCountGap,
			FromBlock: cp.RequestedFrom, ToBlock: cp.RequestedTo,
			Status: v3.GapDetected, Reason: fmt.Sprintf("Provider %d vs final %d",
				report.ExpectedCount, report.ActualCount),
			CreatedAt: time.Now().UTC(),
		})
	}
	// 缺口区间并入 MissingRanges（去重）
	missingSet := map[string]bool{}
	for _, r := range report.MissingRanges {
		missingSet[r.Key()] = true
	}
	for _, g := range report.Gaps {
		r := BlockRange{From: g.FromBlock, To: g.ToBlock}
		if !missingSet[r.Key()] {
			missingSet[r.Key()] = true
			report.MissingRanges = append(report.MissingRanges, r)
			report.UnknownRanges = append(report.UnknownRanges, r)
			report.LevelCoverage = false
		}
	}

	// ── L4 Provider Count 对账：provider rows（Ledger）== 每 Part 唯一键数 ──
	report.LevelProviderCnt = true
	providerRows := map[string]int64{}
	for _, e := range ledgerEntries {
		if e.Event == LedgerPartCommitted && e.Part != "" {
			providerRows[e.Part] += e.Rows
		}
	}
	var expectedTotal int64
	for _, item := range recordsByPart {
		expectedTotal += providerRows[item.name]
		if providerRows[item.name] != perPartUnique[item.name] {
			report.LevelProviderCnt = false
			report.Errors = append(report.Errors, fmt.Sprintf(
				"L4 Provider 对账失败 %s: provider=%d unique=%d", item.name, providerRows[item.name], perPartUnique[item.name]))
		}
	}
	report.ExpectedCount = expectedTotal
	report.ActualCount = report.UniqueKeyCount
	report.RawRows = expectedTotal
	if expectedTotal != report.ActualCount {
		report.LevelProviderCnt = false
		report.Errors = append(report.Errors, fmt.Sprintf(
			"L4 总数不一致: provider=%d actual_unique=%d", expectedTotal, report.ActualCount))
	}

	// ── L6 抽样交叉验证（大任务：≥3 Range 或估算 ≥500 行，随机取 ≤2 个窗口）──
	report.CrossCheck = v.crossCheck(ctx, ds, cp, recordsByPart, ledgerEntries)
	report.LevelCrossCheck = report.CrossCheck.Status == "PASS" || report.CrossCheck.Status == "SKIPPED"
	if report.CrossCheck.Status == "FAILED" {
		report.Errors = append(report.Errors, "L6 交叉验证失败: "+strings.Join(report.CrossCheck.Details, "; "))
	}

	// ── 汇总评分 ──
	score := 0.0
	if report.LevelFile {
		score += 15
	}
	if report.LevelRecord {
		score += 25
	}
	if report.LevelCoverage {
		score += 30
	} else {
		score += 30 * report.Coverage
	}
	if report.LevelProviderCnt {
		score += 20
	}
	if report.LevelCrossCheck {
		score += 10
	}
	report.Score = math.Round(score)
	switch {
	case !report.LevelFile || !report.LevelRecord:
		report.Status = "FAILED"
	case report.LevelCoverage && report.LevelProviderCnt && report.LevelCrossCheck:
		report.Status = "VALIDATED"
	default:
		report.Status = "PARTIAL"
	}
	v.svc.finishValidationPipeline(dsID, report)
	return report, nil
}

// rangesFromJobs 数据集全部 RangeJob 的区间（本地复用/差量下载时与 checkpoint 精确一致）。
func rangesFromJobs(jobs []*RangeJob) []BlockRange {
	seen := map[string]bool{}
	var out []BlockRange
	for _, j := range jobs {
		if j == nil {
			continue
		}
		r := BlockRange{From: j.FromBlock, To: j.ToBlock}
		key := r.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	SortRanges(out)
	return out
}

func validRecordFields(r Record) bool {
	if r.Dataset == DatasetBalances {
		return evmPattern.MatchString(r.Address)
	}
	if r.TransactionHash != "" && !hashPattern.MatchString(r.TransactionHash) {
		return false
	}
	for _, key := range []string{"from_address", "to_address", "token_address", "contract_address"} {
		if v, ok := r.Payload[key].(string); ok && v != "" && !evmPattern.MatchString(v) {
			return false
		}
	}
	return true
}

// readPartRecords 读取 Part（Parquet 用 DuckDB 转 Record；JSONL 直接解析）。
func (v *Validator) readPartRecords(ctx context.Context, path string) ([]Record, error) {
	if strings.HasSuffix(strings.ToLower(path), ".parquet") {
		return v.readParquetRecords(ctx, path)
	}
	return ReadPartRecords(path)
}

func (v *Validator) readParquetRecords(ctx context.Context, path string) ([]Record, error) {
	if err := writer.VerifyParquet(path); err != nil {
		return nil, err
	}
	engine := v.svc.duckdb()
	if engine == nil {
		return nil, fmt.Errorf("DuckDB 不可用，无法读取 Parquet")
	}
	rows, err := engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT * FROM read_parquet('%s')`, filepath.ToSlash(path)))
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(rows))
	for _, row := range rows {
		out = append(out, Record{
			ChainID:         int64(toFloat(row["chain_id"])),
			BlockNumber:     uint64(toFloat(row["block_number"])),
			BlockTime:       int64(toFloat(row["block_time"])),
			TransactionHash: str(row["transaction_hash"]),
			LogIndex:        uint64(toFloat(row["log_index"])),
			Dataset:         datasetFromColumns(row),
			Address:         firstNonEmpty(row, "token_address", "contract_address", "address"),
			Payload:         row,
		})
	}
	return out, nil
}

func datasetFromColumns(row map[string]any) string {
	if _, ok := row["token_standard"]; ok {
		return DatasetTokenTransfers
	}
	if _, ok := row["topics"]; ok {
		return DatasetLogs
	}
	if _, ok := row["trace_address"]; ok {
		return DatasetInternalTransactions
	}
	if _, ok := row["balance"]; ok {
		return DatasetBalances
	}
	return DatasetTransactions
}

func firstNonEmpty(row map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := str(row[k]); v != "" {
			return v
		}
	}
	return ""
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// crossCheck L6：用第二 Provider 对已完成区间抽样计数。
func (v *Validator) crossCheck(ctx context.Context, ds *DatasetJob, cp *CheckpointV3,
	recordsByPart []struct {
		name    string
		from    uint64
		to      uint64
		records []Record
	}, ledger []LedgerEntry) CrossCheckReport {
	report := CrossCheckReport{Status: "SKIPPED"}
	if len(cp.CompletedRanges) < 3 && ds.EstimatedRows < 500 {
		return report
	}
	used := map[string]bool{}
	for _, r := range ledger {
		if r.Provider != "" {
			used[r.Provider] = true
		}
	}
	var second ProviderAdapter
	for _, c := range v.svc.scheduler.Candidates(ds.Dataset) {
		if c.ManualOnly || used[c.Name] || !c.Available {
			continue
		}
		a, ok := v.svc.AdapterByName(c.Name)
		if ok {
			second = a
			break
		}
	}
	if second == nil {
		report.Status = "SKIPPED"
		report.Details = []string{"无第二 Provider 可用"}
		return report
	}
	windows := pickWindows(cp.CompletedRanges, 2, 100)
	report.Status = "PASS"
	report.Windows = len(windows)
	for _, w := range windows {
		probe, err := ProbeWith(ctx, second, ProbeRequest{
			Address: ds.Address, Dataset: ds.Dataset, ChainKey: ds.ChainKey,
			FromBlock: w.From, ToBlock: w.To,
		})
		if err != nil {
			continue
		}
		expected := probe.EstimatedRows
		actual := countRecordsInWindow(recordsByPart, w)
		report.Compared++
		tolerance := uint64(1)
		if actual > 10 {
			tolerance = actual / 10
		}
		diff := uint64(0)
		if expected > actual {
			diff = expected - actual
		} else {
			diff = actual - expected
		}
		if diff > tolerance && !(expected == 0 && actual == 0) {
			report.Mismatch++
			report.Status = "FAILED"
			report.Details = append(report.Details, fmt.Sprintf(
				"窗口 %d-%d: provider=%d local=%d", w.From, w.To, expected, actual))
		}
	}
	return report
}

func pickWindows(ranges []BlockRange, max, size uint64) []BlockRange {
	sorted := append([]BlockRange(nil), ranges...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].From < sorted[j].From })
	if len(sorted) > 2 {
		sorted = []BlockRange{sorted[0], sorted[len(sorted)-1]}
	}
	var out []BlockRange
	for _, r := range sorted {
		to := r.From + size - 1
		if to > r.To {
			to = r.To
		}
		out = append(out, BlockRange{From: r.From, To: to})
	}
	return out
}

func countRecordsInWindow(parts []struct {
	name    string
	from    uint64
	to      uint64
	records []Record
}, w BlockRange) uint64 {
	var n uint64
	for _, p := range parts {
		for _, r := range p.records {
			if r.BlockNumber >= w.From && r.BlockNumber <= w.To {
				n++
			}
		}
	}
	return n
}
