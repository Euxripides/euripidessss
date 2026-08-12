package smartdownload

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/logger"
	v3 "github.com/etl/backend/internal/smartdownload/validation"
	"github.com/xuri/excelize/v2"
)

// DefaultXLSXThreshold 导出阈值：≤30 万行 XLSX，>30 万行 CSV。
const DefaultXLSXThreshold int64 = 300_000

// IndexedResult 已入库结果（下游：地址画像/关系图/智能调查）。
type IndexedResult struct {
	ChunkKey      string              `json:"chunk_key"`
	DatasetJobID  string              `json:"dataset_job_id"`
	ChainKey      string              `json:"chain_key"`
	ChainID       int64               `json:"chain_id"`
	Dataset       string              `json:"dataset"`
	Address       string              `json:"address"`
	FromBlock     uint64              `json:"from_block"`
	ToBlock       uint64              `json:"to_block"`
	RowCount      int64               `json:"row_count"`
	MergedParquet string              `json:"merged_parquet,omitempty"`
	Validation    string              `json:"validation"`
	Certification string              `json:"certification,omitempty"` // CERTIFIED / PARTIAL
	Writer        *IndexedWriteResult `json:"writer,omitempty"`
	WriteError    string              `json:"write_error,omitempty"`
	IndexedAt     time.Time           `json:"indexed_at"`
}

// ResultProcessor 结果处理器：Part → merged Parquet → Dataset Registry → 下游事件。
type ResultProcessor struct {
	svc           *Service
	mu            sync.Mutex
	path          string // registry.json
	xlsxThreshold int64
}

func NewResultProcessor(svc *Service) *ResultProcessor {
	return &ResultProcessor{
		svc:           svc,
		path:          filepath.Join(svc.store.Root(), "smart_download", "registry.json"),
		xlsxThreshold: DefaultXLSXThreshold,
	}
}

// SetXLSXThreshold 设置导出阈值（测试用）。
func (p *ResultProcessor) SetXLSXThreshold(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.xlsxThreshold = n
}

// xlsxLimit 返回当前阈值。
func (p *ResultProcessor) xlsxLimit() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.xlsxThreshold
}

// ExportDataset 导出数据集：≤30 万行 → XLSX；>30 万行 → CSV（UTF-8 BOM）。
// 返回文件绝对路径、格式（xlsx/csv）、行数。
func (p *ResultProcessor) ExportDataset(ctx context.Context, dsID string) (string, string, int64, error) {
	ds := p.svc.store.GetDataset(dsID)
	if ds == nil {
		return "", "", 0, fmt.Errorf("数据集不存在: %s", dsID)
	}
	exportsDir := filepath.Join(p.svc.store.Root(), "smart_download", "exports")
	if err := os.MkdirAll(exportsDir, 0o755); err != nil {
		return "", "", 0, err
	}
	base := fmt.Sprintf("smart_download_%s_%s", ds.Dataset, shortAddress(ds.Address))
	tmpCSV := filepath.Join(exportsDir, base+".tmp.csv")
	_ = os.Remove(tmpCSV)
	var rows int64
	engine := p.svc.duckdb()
	entry := p.find(dsID)
	if entry != nil && entry.MergedParquet != "" && engine != nil && engine.Available() {
		cnt, err := engine.ExecSQLJSON(ctx, fmt.Sprintf(
			`SELECT COUNT(*) AS n FROM read_parquet('%s', hive_partitioning=false)`,
			filepath.ToSlash(entry.MergedParquet)))
		if err == nil && len(cnt) > 0 {
			rows = int64(toFloat(cnt[0]["n"]))
		}
		sql := fmt.Sprintf(
			`COPY (SELECT * FROM read_parquet('%s', hive_partitioning=false)) TO '%s' (HEADER, DELIMITER ',')`,
			filepath.ToSlash(entry.MergedParquet), filepath.ToSlash(tmpCSV))
		if _, err := engine.ExecSQL(ctx, sql); err != nil {
			os.Remove(tmpCSV)
			return "", "", 0, fmt.Errorf("导出 CSV 失败: %w", err)
		}
	} else {
		if err := p.writePartsCSV(ctx, dsID, ds.Dataset, tmpCSV, &rows); err != nil {
			os.Remove(tmpCSV)
			return "", "", 0, err
		}
	}
	if limit := p.xlsxLimit(); limit > 0 && rows <= limit {
		xlsxPath := filepath.Join(exportsDir, fmt.Sprintf("%s_%d.xlsx", base, rows))
		if err := csvToXLSX(tmpCSV, xlsxPath); err != nil {
			os.Remove(tmpCSV)
			return "", "", rows, err
		}
		os.Remove(tmpCSV)
		return xlsxPath, "xlsx", rows, nil
	}
	csvPath := filepath.Join(exportsDir, fmt.Sprintf("%s_%d.csv", base, rows))
	if err := writeFinalCSV(tmpCSV, csvPath); err != nil {
		os.Remove(tmpCSV)
		return "", "", rows, err
	}
	os.Remove(tmpCSV)
	return csvPath, "csv", rows, nil
}

// writePartsCSV 无 DuckDB/merged 时直接从 Part 记录流式写 CSV。
func (p *ResultProcessor) writePartsCSV(ctx context.Context, dsID, dataset, path string, rows *int64) error {
	cp, err := p.svc.cp.Load(dsID)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	cols := canonicalColumns(dataset)
	if err := w.Write(cols); err != nil {
		return err
	}
	for _, part := range cp.Parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		recs, err := ReadPartRecords(filepath.Join(p.svc.PartsDir(), dsID, part.Name))
		if err != nil {
			continue
		}
		rangeID := fmt.Sprintf("%d-%d", part.RangeFrom, part.RangeTo)
		for _, r := range recs {
			if err := w.Write(canonicalRow(dataset, normalizeRecord(r, "", rangeID))); err != nil {
				return err
			}
			*rows++
		}
	}
	w.Flush()
	return w.Error()
}

func shortAddress(addr string) string {
	if len(addr) >= 10 {
		return strings.ToLower(addr)[2:10]
	}
	return strings.ToLower(addr)
}

// csvToXLSX 流式读取 CSV 写入 XLSX（默认样式，无颜色/格式）。
func csvToXLSX(csvPath, xlsxPath string) error {
	f := excelize.NewFile()
	defer f.Close()
	// StreamWriter serializes the worksheet dimension when Flush is called, so
	// determine the exact used range before creating it. This second sequential
	// CSV pass keeps memory bounded and makes strict streaming readers honor all
	// rows instead of the template's default A1 dimension.
	probe, err := os.Open(csvPath)
	if err != nil {
		return err
	}
	pr := csv.NewReader(probe)
	pr.FieldsPerRecord = -1
	rows, maxCols := 0, 0
	var headers []string
	for {
		rec, readErr := pr.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = probe.Close()
			return readErr
		}
		rows++
		if rows == 1 {
			headers = append([]string(nil), rec...)
		}
		if len(rec) > maxCols {
			maxCols = len(rec)
		}
	}
	if err := probe.Close(); err != nil {
		return err
	}
	if rows > 0 && maxCols > 0 {
		last, err := excelize.CoordinatesToCellName(maxCols, rows)
		if err != nil {
			return err
		}
		if err := f.SetSheetDimension("Sheet1", "A1:"+last); err != nil {
			return err
		}
	}
	for i, header := range headers {
		col, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			return err
		}
		if err := f.SetColWidth("Sheet1", col, col, exportColumnWidth(header)); err != nil {
			return err
		}
	}
	sw, err := f.NewStreamWriter("Sheet1")
	if err != nil {
		return err
	}
	in, err := os.Open(csvPath)
	if err != nil {
		return err
	}
	defer in.Close()
	cr := csv.NewReader(in)
	cr.FieldsPerRecord = -1
	rowIdx := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		cell, err := excelize.CoordinatesToCellName(1, rowIdx)
		if err != nil {
			return err
		}
		vals := make([]interface{}, len(rec))
		for i, v := range rec {
			vals[i] = v
		}
		if err := sw.SetRow(cell, vals); err != nil {
			return err
		}
		rowIdx++
	}
	if err := sw.Flush(); err != nil {
		return err
	}
	return f.SaveAs(xlsxPath)
}

func exportColumnWidth(header string) float64 {
	name := strings.ToLower(strings.TrimSpace(header))
	switch {
	case strings.Contains(name, "transaction_hash") || strings.HasSuffix(name, "_hash"):
		return 68
	case strings.Contains(name, "address"):
		return 44
	case strings.Contains(name, "time") || strings.Contains(name, "date"):
		return 22
	case strings.Contains(name, "value") || strings.Contains(name, "amount") || strings.Contains(name, "range_id"):
		return 24
	default:
		return 18
	}
}

// longNumberRE 长数字（≥12 位，超出 Excel 安全显示范围）——防止被转成科学计数法。
var longNumberRE = regexp.MustCompile(`^-?\d{12,}$`)

// protectLongNumbers 长数字包成 ="..." 文本公式，Excel 打开 CSV 时按文本显示。
func protectLongNumbers(v string) string {
	if longNumberRE.MatchString(v) {
		return `="` + v + `"`
	}
	return v
}

// writeFinalCSV 生成最终 CSV：UTF-8 BOM + 长数字文本保护（流式，30 万+ 行不进内存）。
func writeFinalCSV(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cr := csv.NewReader(in)
	cr.FieldsPerRecord = -1
	cw := csv.NewWriter(out)
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		for i := range rec {
			rec[i] = protectLongNumbers(rec[i])
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// MergeDataset 合并该数据集全部 Part 为 warehouse Parquet 并登记 Registry。
func (p *ResultProcessor) MergeDataset(ctx context.Context, dsID string) (*IndexedResult, error) {
	ds := p.svc.store.GetDataset(dsID)
	if ds == nil {
		return nil, fmt.Errorf("dataset %s 不存在", dsID)
	}
	cp, err := p.svc.cp.Load(dsID)
	if err != nil {
		return nil, err
	}
	engine := p.svc.duckdb()
	entry := &IndexedResult{
		ChunkKey:      "sd-" + dsID,
		DatasetJobID:  dsID,
		ChainKey:      ds.ChainKey,
		ChainID:       p.chainIDOf(ds),
		Dataset:       ds.Dataset,
		Address:       ds.Address,
		FromBlock:     cp.RequestedFrom,
		ToBlock:       cp.RequestedTo,
		Validation:    indexedValidationStatus(ds.Validation),
		Certification: "PARTIAL",
		IndexedAt:     time.Now().UTC(),
	}
	outRoot := "staging"
	certificationCandidate := (ds.Status == DatasetCompleted || ds.Status == DatasetIndexing || ds.Status == DatasetDBWriteFailed) &&
		validationReadyForCertification(ds.Validation)
	uniqueRows, integrityErr := p.verifyMergeInputs(ctx, ds, cp, certificationCandidate)
	if integrityErr != nil {
		return nil, fmt.Errorf("结果合并输入校验失败: %w", integrityErr)
	}
	if certificationCandidate {
		outRoot = "final"
		entry.Certification = "CERTIFIED"
	}
	var paths []string
	for _, part := range cp.Parts {
		paths = append(paths, filepath.Join(p.svc.PartsDir(), dsID, part.Name))
	}
	if len(paths) > 0 && engine != nil && engine.Available() {
		outDir := filepath.Join(p.svc.store.Root(), "smart_download", outRoot, ds.Dataset, "chain="+ds.ChainKey)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return nil, err
		}
		merged := filepath.Join(outDir, dsID+".parquet")
		tmp := merged + ".tmp"
		_ = os.Remove(tmp)
		list := make([]string, 0, len(paths))
		for _, pt := range paths {
			list = append(list, "'"+strings.ReplaceAll(filepath.ToSlash(pt), "'", "''")+"'")
		}
		sel := make([]string, 0, len(canonicalColumns(ds.Dataset)))
		for _, c := range canonicalColumns(ds.Dataset) {
			sel = append(sel, quoteIdent(c))
		}
		sql := fmt.Sprintf(
			`COPY (SELECT %s FROM read_parquet([%s], union_by_name=true)) TO '%s' (FORMAT PARQUET, COMPRESSION GZIP)`,
			strings.Join(sel, ", "), strings.Join(list, ", "), filepath.ToSlash(tmp))
		if _, err := engine.ExecSQL(ctx, sql); err != nil {
			os.Remove(tmp)
			return nil, fmt.Errorf("结果合并失败: %w", err)
		}
		if err := os.Rename(tmp, merged); err != nil {
			os.Remove(tmp)
			return nil, err
		}
		rows, err := engine.ExecSQLJSON(ctx, fmt.Sprintf(
			`SELECT COUNT(*) AS n FROM read_parquet('%s')`, filepath.ToSlash(merged)))
		if err == nil && len(rows) > 0 {
			entry.RowCount = int64(toFloat(rows[0]["n"]))
		}
		if entry.RowCount != uniqueRows {
			_ = os.Remove(merged)
			return nil, fmt.Errorf("结果合并行数不一致: merged=%d unique=%d", entry.RowCount, uniqueRows)
		}
		entry.MergedParquet = merged
		if entry.Certification == "CERTIFIED" {
			p.writeManifestV3(ctx, dsID, entry, cp)
		}
	} else {
		entry.RowCount = uniqueRows
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entries := p.loadLocked()
	replaced := false
	for i, e := range entries {
		if e.DatasetJobID == dsID {
			entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].IndexedAt.After(entries[j].IndexedAt) })
	payload, _ := json.MarshalIndent(entries, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return nil, err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, p.path); err != nil {
		return nil, err
	}
	logger.Log.Info().Str("dataset_job", dsID).Str("dataset", ds.Dataset).
		Int64("rows", entry.RowCount).Str("merged", entry.MergedParquet).
		Msg("smartdownload_result_indexed")
	return entry, nil
}

func indexedValidationStatus(report *ValidationReport) string {
	if report == nil {
		return "UNVALIDATED"
	}
	switch strings.ToUpper(strings.TrimSpace(report.Status)) {
	case "PASS", "VALIDATED":
		return "VALIDATED"
	case "PARTIAL":
		return "PARTIAL"
	case "FAILED":
		return "FAILED"
	default:
		return "UNVALIDATED"
	}
}

func validationReadyForCertification(report *ValidationReport) bool {
	if indexedValidationStatus(report) != "VALIDATED" {
		return false
	}
	if report.Coverage < 0.999999 {
		return false
	}
	// BlockCoverage was absent from legacy reports. When present it must also be
	// complete; using max(Coverage, BlockCoverage) allowed contradictory reports.
	if report.BlockCoverage > 0 && report.BlockCoverage < 0.999999 {
		return false
	}
	return report.DuplicateCount == 0 && report.PartsDuplicateSHA == 0 && len(report.Errors) == 0 &&
		len(report.MissingRanges) == 0 && len(report.UnknownRanges) == 0
}

// verifyMergeInputs is the final fail-closed boundary before publishing a
// canonical result. It rechecks immutable artifact evidence independently from
// the in-memory ValidationReport and rejects overlapping duplicate events.
func (p *ResultProcessor) verifyMergeInputs(ctx context.Context, ds *DatasetJob, cp *CheckpointV3, requireComplete bool) (int64, error) {
	if ds == nil || cp == nil {
		return 0, fmt.Errorf("dataset/checkpoint 为空")
	}
	if cp.RequestedTo < cp.RequestedFrom {
		return 0, fmt.Errorf("请求区间非法: %d-%d", cp.RequestedFrom, cp.RequestedTo)
	}
	if len(cp.Parts) == 0 {
		if ds.DownloadedRows > 0 || cp.RowsCommitted > 0 {
			return 0, fmt.Errorf("声明存在下载行但没有 Part")
		}
		if requireComplete && !checkpointEmptyCoverageComplete(cp) {
			return 0, fmt.Errorf("无 Part 且缺少完整的已认证空区间证据")
		}
		return 0, nil
	}
	validator := NewValidator(p.svc)
	seen := make(map[string]struct{})
	for _, part := range cp.Parts {
		if part.RangeTo < part.RangeFrom || part.RangeFrom < cp.RequestedFrom || part.RangeTo > cp.RequestedTo {
			return 0, fmt.Errorf("Part %s 区间 [%d,%d] 超出请求 [%d,%d]", part.Name,
				part.RangeFrom, part.RangeTo, cp.RequestedFrom, cp.RequestedTo)
		}
		path := filepath.Join(p.svc.PartsDir(), ds.ID, part.Name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return 0, fmt.Errorf("Part %s 缺失或为空", part.Name)
		}
		if part.Bytes > 0 && info.Size() != part.Bytes {
			return 0, fmt.Errorf("Part %s 文件大小不匹配", part.Name)
		}
		sha, err := fileSHA256(path)
		if err != nil || strings.TrimSpace(part.SHA256) == "" || !strings.EqualFold(sha, part.SHA256) {
			return 0, fmt.Errorf("Part %s SHA256 不匹配", part.Name)
		}
		records, err := validator.readPartRecords(ctx, path)
		if err != nil {
			return 0, fmt.Errorf("Part %s 读取失败: %w", part.Name, err)
		}
		if int64(len(records)) != part.Rows {
			return 0, fmt.Errorf("Part %s 行数不匹配: checkpoint=%d actual=%d", part.Name, part.Rows, len(records))
		}
		for _, record := range records {
			if record.BlockNumber < part.RangeFrom || record.BlockNumber > part.RangeTo ||
				record.BlockNumber < cp.RequestedFrom || record.BlockNumber > cp.RequestedTo {
				return 0, fmt.Errorf("Part %s 含越界 block %d", part.Name, record.BlockNumber)
			}
			if !validRecordFields(record) {
				return 0, fmt.Errorf("Part %s 含非法记录 %s", part.Name, record.TransactionHash)
			}
			key := CanonicalKey(record)
			if _, duplicate := seen[key]; duplicate {
				return 0, fmt.Errorf("Part 间存在重复唯一键: %s", key)
			}
			seen[key] = struct{}{}
		}
	}
	if uint64(len(seen)) != cp.RowsCommitted {
		return 0, fmt.Errorf("checkpoint 行数不一致: committed=%d unique=%d", cp.RowsCommitted, len(seen))
	}
	return int64(len(seen)), nil
}

func checkpointEmptyCoverageComplete(cp *CheckpointV3) bool {
	if cp == nil || len(cp.ConfirmedEmptyRanges) == 0 {
		return false
	}
	_, missing := planReuse(BlockRange{From: cp.RequestedFrom, To: cp.RequestedTo}, cp.ConfirmedEmptyRanges)
	return len(missing) == 0
}

// MarkWriteFailure keeps the merged Parquet retryable while revoking registry certification.
func (p *ResultProcessor) MarkWriteFailure(dsID string, result IndexedWriteResult, message string) error {
	return p.updateWriteState(dsID, &result, "DB_WRITE_FAILED", message)
}

// MarkWriteSuccess persists writer reconciliation in the certified registry entry.
func (p *ResultProcessor) MarkWriteSuccess(dsID string, result IndexedWriteResult) error {
	return p.updateWriteState(dsID, &result, "CERTIFIED", "")
}

func (p *ResultProcessor) updateWriteState(dsID string, result *IndexedWriteResult, certification, message string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	entries := p.loadLocked()
	found := false
	for _, entry := range entries {
		if entry.DatasetJobID == dsID {
			entry.Writer = result
			entry.Certification = certification
			entry.WriteError = message
			entry.IndexedAt = time.Now().UTC()
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("dataset %s registry entry not found", dsID)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].IndexedAt.After(entries[j].IndexedAt) })
	payload, _ := json.MarshalIndent(entries, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

// writeManifestV3 生成 manifest-v3.json（设计 §64）。
func (p *ResultProcessor) writeManifestV3(ctx context.Context, dsID string, entry *IndexedResult, cp *CheckpointV3) {
	ledger, _ := p.svc.LedgerEntries(dsID)
	providers := map[string]bool{}
	switches := 0
	for _, e := range ledger {
		if e.Provider != "" {
			providers[e.Provider] = true
		}
		if e.Event == LedgerProviderSwitched {
			switches++
		}
	}
	var providersList []string
	for pv := range providers {
		providersList = append(providersList, pv)
	}
	cert := map[string]any{}
	if c, err := v3.NewGapStore(p.svc.store.Root(), dsID).LoadCertificate(); err == nil {
		cert = map[string]any{
			"status": c.Status, "coverage": c.Coverage,
			"rows_final": c.RowsFinal, "duplicates_removed": c.DuplicatesRemoved,
			"gaps_detected": c.GapsDetected, "gaps_repaired": c.GapsRepaired,
			"gaps_remaining":            c.GapsRemaining,
			"cross_check_sample_ranges": c.CrossCheckSampleRanges,
			"cross_check_matched":       c.CrossCheckMatched,
			"certified_at":              c.CertifiedAt,
		}
	}
	var parts []map[string]any
	for _, pt := range cp.Parts {
		parts = append(parts, map[string]any{
			"name": pt.Name, "sha256": pt.SHA256, "rows": pt.Rows,
			"range": []uint64{pt.RangeFrom, pt.RangeTo},
		})
	}
	manifest := map[string]any{
		"schema_version":         3,
		"dataset_job_id":         dsID,
		"dataset":                entry.Dataset,
		"address":                entry.Address,
		"chain_id":               entry.ChainID,
		"range":                  []uint64{entry.FromBlock, entry.ToBlock},
		"rows":                   entry.RowCount,
		"parts":                  parts,
		"providers_used":         providersList,
		"provider_switches":      switches,
		"validation_certificate": cert,
		"certified_at":           entry.IndexedAt,
	}
	payload, _ := json.MarshalIndent(manifest, "", "  ")
	dir := filepath.Dir(entry.MergedParquet)
	manifestName := entry.DatasetJobID + "-manifest-v3.json"
	tmp := filepath.Join(dir, manifestName+".tmp")
	if os.WriteFile(tmp, payload, 0o644) == nil {
		_ = os.Rename(tmp, filepath.Join(dir, manifestName))
	}
	_ = ctx
}

func (p *ResultProcessor) chainIDOf(ds *DatasetJob) int64 {
	if ds == nil {
		return 0
	}
	if a := p.svc.store.GetAddress(ds.AddressJobID); a != nil {
		return a.ChainID
	}
	return 0
}

func (p *ResultProcessor) loadLocked() []*IndexedResult {
	payload, err := os.ReadFile(p.path)
	if err != nil {
		return nil
	}
	var out []*IndexedResult
	if json.Unmarshal(payload, &out) != nil {
		return nil
	}
	return out
}

// List 返回已入库结果（新→旧）。
func (p *ResultProcessor) List() []*IndexedResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*IndexedResult(nil), p.loadLocked()...)
}

func (p *ResultProcessor) HasAnyForRange(chainKey, address, dataset string, from, to uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, entry := range p.loadLocked() {
		if strings.EqualFold(entry.ChainKey, chainKey) && strings.EqualFold(entry.Address, address) &&
			entry.Dataset == dataset && entry.ToBlock >= from && entry.FromBlock <= to {
			return true
		}
	}
	return false
}

// AuthoritativeForRange chooses one immutable, reusable result for a requested
// scope. Blockchain history cannot legitimately change from non-empty to empty
// for the same exact range, so usable non-empty evidence outranks empty or
// missing-artifact versions; time only breaks ties of equal quality.
func (p *ResultProcessor) AuthoritativeForRange(chainKey, address, dataset string, from, to uint64) *IndexedResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	var best *IndexedResult
	bestRank := -1
	for _, entry := range p.loadLocked() {
		if !strings.EqualFold(entry.ChainKey, chainKey) || !strings.EqualFold(entry.Address, address) ||
			entry.Dataset != dataset || entry.FromBlock > from || entry.ToBlock < to || !p.isReusableIndexedResult(entry) {
			continue
		}
		rank := reusableEvidenceRank(entry)
		if entry.FromBlock == from && entry.ToBlock == to {
			rank += 10
		}
		if best == nil || rank > bestRank || (rank == bestRank && entry.IndexedAt.After(best.IndexedAt)) {
			best, bestRank = entry, rank
		}
	}
	return best
}

func (p *ResultProcessor) isReusableIndexedResult(entry *IndexedResult) bool {
	if entry == nil || !strings.EqualFold(entry.Validation, "VALIDATED") ||
		!strings.EqualFold(entry.Certification, "CERTIFIED") {
		return false
	}
	if strings.TrimSpace(entry.MergedParquet) == "" {
		if entry.RowCount == 0 {
			return p.hasCertifiedEmptyEvidence(entry.DatasetJobID, entry.FromBlock, entry.ToBlock)
		}
		return p.hasVerifiedPartEvidence(entry)
	}
	info, err := os.Stat(entry.MergedParquet)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func (p *ResultProcessor) hasVerifiedPartEvidence(entry *IndexedResult) bool {
	if p == nil || p.svc == nil || entry == nil || entry.RowCount <= 0 {
		return false
	}
	cp, err := p.svc.cp.Load(entry.DatasetJobID)
	if err != nil || len(cp.Parts) == 0 {
		return false
	}
	var rows int64
	for _, part := range cp.Parts {
		path := filepath.Join(p.svc.PartsDir(), entry.DatasetJobID, part.Name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return false
		}
		if part.Bytes > 0 && info.Size() != part.Bytes {
			return false
		}
		if strings.TrimSpace(part.SHA256) == "" {
			return false
		}
		digest, err := fileSHA256(path)
		if err != nil || !strings.EqualFold(digest, part.SHA256) {
			return false
		}
		rows += part.Rows
	}
	return rows == entry.RowCount
}

func (p *ResultProcessor) hasCertifiedEmptyEvidence(datasetJobID string, from, to uint64) bool {
	if p == nil || p.svc == nil || strings.TrimSpace(datasetJobID) == "" {
		return false
	}
	emptyRanges := make([]BlockRange, 0)
	for _, r := range p.svc.store.ListRangesByDataset(datasetJobID) {
		if r.Certified && r.Status == RangeEmpty {
			emptyRanges = append(emptyRanges, BlockRange{From: r.FromBlock, To: r.ToBlock})
		}
	}
	if len(emptyRanges) == 0 || to < from {
		return false
	}
	_, missing := planReuse(BlockRange{From: from, To: to}, emptyRanges)
	return len(missing) == 0
}

func reusableEvidenceRank(entry *IndexedResult) int {
	if entry == nil {
		return -1
	}
	if entry.RowCount > 0 && strings.TrimSpace(entry.MergedParquet) != "" {
		return 3
	}
	if entry.RowCount == 0 {
		return 2
	}
	return 1
}

// QueryResults 服务端分页查询结果（DuckDB；无引擎时回退 Part 内存扫描）。
type ResultQueryParamError struct {
	Param  string
	Reason string
}

func (e *ResultQueryParamError) Error() string {
	return fmt.Sprintf("结果查询参数 %s 无效: %s", e.Param, e.Reason)
}

func IsResultQueryParamError(err error) bool {
	var target *ResultQueryParamError
	return errors.As(err, &target)
}

func (p *ResultProcessor) QueryResults(ctx context.Context, dsID string, page, pageSize int, sortCol, filter string) ([]map[string]any, int64, error) {
	if page < 1 {
		return nil, 0, &ResultQueryParamError{Param: "page", Reason: "必须大于等于 1"}
	}
	if pageSize < 1 || pageSize > 1000 {
		return nil, 0, &ResultQueryParamError{Param: "page_size", Reason: "必须在 1 到 1000 之间"}
	}
	ds := p.svc.store.GetDataset(dsID)
	if err := validateResultQuery(ds, sortCol, filter); err != nil {
		return nil, 0, err
	}
	entry := p.find(dsID)
	engine := p.svc.duckdb()
	if entry != nil && entry.MergedParquet != "" && engine != nil && engine.Available() {
		return p.queryParquet(ctx, entry.MergedParquet, page, pageSize, sortCol, filter)
	}
	return p.queryParts(ctx, dsID, page, pageSize, sortCol, filter)
}

func (p *ResultProcessor) find(dsID string) *IndexedResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.loadLocked() {
		if e.DatasetJobID == dsID {
			return e
		}
	}
	return nil
}

func (p *ResultProcessor) queryParquet(ctx context.Context, path string, page, pageSize int, sortCol, filter string) ([]map[string]any, int64, error) {
	engine := p.svc.duckdb()
	where := buildWhere(filter)
	order := ""
	if sortCol != "" {
		order = " ORDER BY " + quoteIdent(sortCol)
	}
	cntSQL := fmt.Sprintf(`SELECT COUNT(*) AS n FROM read_parquet('%s', hive_partitioning=false) %s`, filepath.ToSlash(path), where)
	cnt, err := engine.ExecSQLJSON(ctx, cntSQL)
	if err != nil {
		logger.Log.Warn().Err(err).Msg("smartdownload_result_count_failed")
		return nil, 0, fmt.Errorf("结果计数失败")
	}
	if len(cnt) == 0 {
		return []map[string]any{}, 0, nil
	}
	total := int64(toFloat(cnt[0]["n"]))
	if total == 0 {
		return []map[string]any{}, 0, nil
	}
	offset := (page - 1) * pageSize
	sql := fmt.Sprintf(
		`SELECT * FROM read_parquet('%s', hive_partitioning=false) %s%s LIMIT %d OFFSET %d`,
		filepath.ToSlash(path), where, order, pageSize, offset)
	rows, err := engine.ExecSQLJSON(ctx, sql)
	if err != nil {
		logger.Log.Warn().Err(err).Msg("smartdownload_result_query_failed")
		return nil, 0, fmt.Errorf("结果查询失败")
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, total, nil
}

func (p *ResultProcessor) queryParts(ctx context.Context, dsID string, page, pageSize int, sortCol, filter string) ([]map[string]any, int64, error) {
	cp, err := p.svc.cp.Load(dsID)
	if err != nil {
		return nil, 0, err
	}
	var records []Record
	seen := map[string]struct{}{}
	for _, part := range cp.Parts {
		recs, err := ReadPartRecords(filepath.Join(p.svc.PartsDir(), dsID, part.Name))
		if err != nil {
			continue
		}
		for _, record := range recs {
			key := CanonicalKey(record)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			records = append(records, record)
		}
	}
	rows := make([]map[string]any, 0, len(records))
	for _, r := range records {
		m := normalizeRecord(r, "", fmt.Sprintf("%d-%d", r.BlockNumber, r.BlockNumber))
		m["transaction_hash"] = r.TransactionHash
		m["block_number"] = r.BlockNumber
		m["chain_id"] = r.ChainID
		rows = append(rows, m)
	}
	if filter != "" {
		rows = filterRows(rows, filter)
	}
	if sortCol != "" {
		sort.SliceStable(rows, func(i, j int) bool {
			return fmt.Sprintf("%v", rows[i][sortCol]) < fmt.Sprintf("%v", rows[j][sortCol])
		})
	}
	total := int64(len(rows))
	start := (page - 1) * pageSize
	if start > len(rows) {
		start = len(rows)
	}
	end := start + pageSize
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], total, nil
}

func buildWhere(filter string) string {
	if filter == "" {
		return ""
	}
	parts := strings.SplitN(filter, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	col := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])
	if !validFilterColumn(col) {
		return ""
	}
	return fmt.Sprintf(" WHERE %s = '%s'", quoteIdent(col), strings.ReplaceAll(val, "'", "''"))
}

func filterRows(rows []map[string]any, filter string) []map[string]any {
	parts := strings.SplitN(filter, ":", 2)
	if len(parts) != 2 {
		return rows
	}
	col, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if !validFilterColumn(col) {
		return rows
	}
	out := rows[:0]
	for _, r := range rows {
		if fmt.Sprintf("%v", r[col]) == val {
			out = append(out, r)
		}
	}
	return out
}

func validFilterColumn(col string) bool {
	switch col {
	case "transaction_hash", "from_address", "to_address", "token_address", "contract_address", "address", "symbol", "status", "block_number", "chain_id":
		return true
	default:
		return false
	}
}

func validateResultQuery(ds *DatasetJob, sortCol, filter string) error {
	allowed := map[string]bool{}
	if ds != nil {
		for _, col := range canonicalColumns(ds.Dataset) {
			allowed[col] = true
		}
	} else {
		for _, dataset := range []string{DatasetTransactions, DatasetTokenTransfers, DatasetInternalTransactions,
			DatasetLogs, DatasetBalances, DatasetContractCreations} {
			for _, col := range canonicalColumns(dataset) {
				allowed[col] = true
			}
		}
	}
	sortCol = strings.TrimSpace(sortCol)
	if sortCol != "" && !allowed[sortCol] {
		return &ResultQueryParamError{Param: "sort", Reason: "不支持字段 " + sortCol}
	}
	if strings.TrimSpace(filter) == "" {
		return nil
	}
	parts := strings.SplitN(filter, ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return &ResultQueryParamError{Param: "filter", Reason: "格式必须为 field:value"}
	}
	column := strings.TrimSpace(parts[0])
	if !allowed[column] {
		return &ResultQueryParamError{Param: "filter", Reason: "不支持字段 " + column}
	}
	return nil
}

// ResultSummary 数据集结果摘要。
func (p *ResultProcessor) ResultSummary(ctx context.Context, dsID string) (map[string]any, error) {
	ds := p.svc.store.GetDataset(dsID)
	if ds == nil {
		return nil, fmt.Errorf("数据集不存在: %s", dsID)
	}
	out := map[string]any{
		"dataset":       ds.Dataset,
		"address":       ds.Address,
		"status":        ds.Status,
		"downloaded":    ds.DownloadedRows,
		"validation":    ds.Validation,
		"repair_rounds": ds.RepairRounds,
	}
	if entry := p.find(dsID); entry != nil {
		out["indexed_rows"] = entry.RowCount
		out["merged_parquet"] = entry.MergedParquet
		out["certification"] = entry.Certification
		out["from_block"] = entry.FromBlock
		out["to_block"] = entry.ToBlock
		out["indexed_at"] = entry.IndexedAt
	}
	return out, nil
}
