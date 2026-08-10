package smartdownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/etl/backend/internal/analysis/duckdb"
)

// ── Provider Adapter（实施方案 §6：Provider 只负责把某个 Range 的原始数据拿回来）──

// RangeRequest 单个 Range 的采集请求（Address × Dataset × Range）。
type RangeRequest struct {
	DatasetJobID string       `json:"dataset_job_id"`
	Mode         DownloadMode `json:"mode,omitempty"`
	Priority     int          `json:"priority,omitempty"`
	Address      string       `json:"address"`
	Dataset      string       `json:"dataset"`
	ChainKey     string       `json:"chain_key"`
	ChainID      int64        `json:"chain_id"`
	FromBlock    uint64       `json:"from_block"`
	ToBlock      uint64       `json:"to_block"`
	CloudTier    string       `json:"cloud_tier,omitempty"`
}

// Record 中间记录（Phase 3 由 Normalizer 转 Canonical Schema；Phase 1 保持通用键字段）。
type Record struct {
	ChainID         int64          `json:"chain_id"`
	BlockNumber     uint64         `json:"block_number"`
	BlockTime       int64          `json:"block_time,omitempty"`
	TransactionHash string         `json:"transaction_hash"`
	LogIndex        uint64         `json:"log_index"`
	Dataset         string         `json:"dataset"`
	Address         string         `json:"address"`
	Payload         map[string]any `json:"payload,omitempty"`
}

// UniqueKey 通用唯一键（链+块+交易+日志索引；Phase 3 按 Dataset 精化）。
func (r Record) UniqueKey() string {
	return fmt.Sprintf("%d|%d|%s|%d", r.ChainID, r.BlockNumber, strings.ToLower(r.TransactionHash), r.LogIndex)
}

// ProviderResult Provider 返回的原始数据。
type ProviderResult struct {
	Records []Record `json:"records"`
	Bytes   uint64   `json:"bytes"`
	// CompletedTo 表示本 Range 内已安全提交到的区块（含）；0 = 整个 Range 完成。
	CompletedTo uint64 `json:"completed_to"`
}

// ProviderAdapter Provider 统一接口（Phase 1 最小集；Phase 2 增加 Probe/Health/Cancel）。
type ProviderAdapter interface {
	Name() string
	Supports(dataset string) bool
	Available() bool
	// Probe 低成本估算（不支持时返回 Confidence=0）。
	Probe(ctx context.Context, req ProbeRequest) (ProbeResult, error)
	ExecuteRange(ctx context.Context, req RangeRequest) (*ProviderResult, error)
}

// ── Part Writer（Phase 1：JSONL + SHA256；Phase 3 切换 Canonical Parquet）──

// PartWriteResult 已提交 Part 的元数据。
type PartWriteResult struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Rows   int64  `json:"rows"`
	Bytes  int64  `json:"bytes"`
}

// PartMeta Part 写入元数据（溯源列来源）。
type PartMeta struct {
	DatasetJobID string
	PartName     string
	Provider     string
	FromBlock    uint64
	ToBlock      uint64
}

// PartWriter 把 Provider 原始记录写为可校验 Part（JSONL 或 Canonical Parquet）。
type PartWriter interface {
	WritePart(ctx context.Context, meta PartMeta, records []Record) (PartWriteResult, error)
	Extension() string
}

// JSONLPartWriter 简单 Part 写入器（原子写 + SHA256）。
type JSONLPartWriter struct {
	partsDir string
}

// NewJSONLPartWriter 创建写入器（root/smart_download/parts/{datasetJobID}/）。
func NewJSONLPartWriter(root string) *JSONLPartWriter {
	return &JSONLPartWriter{partsDir: filepath.Join(root, "smart_download", "parts")}
}

func (w *JSONLPartWriter) PartsDir() string { return w.partsDir }

func (w *JSONLPartWriter) Extension() string { return ".jsonl" }

func (w *JSONLPartWriter) WritePart(_ context.Context, meta PartMeta, records []Record) (PartWriteResult, error) {
	datasetJobID, partName := meta.DatasetJobID, meta.PartName
	dir := filepath.Join(w.partsDir, datasetJobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return PartWriteResult{}, err
	}
	path := filepath.Join(dir, partName)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return PartWriteResult{}, err
	}
	hasher := sha256.New()
	var bytes int64
	for i := range records {
		line, err := json.Marshal(records[i])
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return PartWriteResult{}, err
		}
		line = append(line, '\n')
		if _, err := f.Write(line); err != nil {
			f.Close()
			os.Remove(tmp)
			return PartWriteResult{}, err
		}
		hasher.Write(line)
		bytes += int64(len(line))
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return PartWriteResult{}, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return PartWriteResult{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return PartWriteResult{}, err
	}
	return PartWriteResult{
		Path:   path,
		SHA256: hex.EncodeToString(hasher.Sum(nil)),
		Rows:   int64(len(records)),
		Bytes:  bytes,
	}, nil
}

// ParquetPartWriter Canonical Parquet 写入器（Phase 3）：
// 记录 → Canonical CSV → DuckDB read_csv → COPY TO Parquet（GZIP）→ SHA256。
type ParquetPartWriter struct {
	engine   *duckdb.Engine
	partsDir string
}

// NewParquetPartWriter 创建 Parquet 写入器（engine 不可用时由调用方回退 JSONL）。
func NewParquetPartWriter(root string, engine *duckdb.Engine) *ParquetPartWriter {
	return &ParquetPartWriter{engine: engine, partsDir: filepath.Join(root, "smart_download", "parts")}
}

func (w *ParquetPartWriter) PartsDir() string { return w.partsDir }

func (w *ParquetPartWriter) Extension() string { return ".parquet" }

func (w *ParquetPartWriter) WritePart(ctx context.Context, meta PartMeta, records []Record) (PartWriteResult, error) {
	if w.engine == nil || !w.engine.Available() {
		return PartWriteResult{}, fmt.Errorf("DuckDB 不可用，无法写 Parquet Part")
	}
	dir := filepath.Join(w.partsDir, meta.DatasetJobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return PartWriteResult{}, err
	}
	csvPath := filepath.Join(dir, meta.PartName+".csv.tmp")
	parquetPath := filepath.Join(dir, meta.PartName)
	_ = os.Remove(csvPath)
	_ = os.Remove(parquetPath)
	_ = os.Remove(parquetPath + ".tmp")
	if err := writeCanonicalCSV(csvPath, meta, records); err != nil {
		os.Remove(csvPath)
		return PartWriteResult{}, err
	}
	selectSQL := canonicalTypedSQL(records[0].Dataset)
	sql := fmt.Sprintf(
		`COPY (SELECT %s FROM read_csv('%s', header=true, all_varchar=true, strict_mode=false, ignore_errors=true, quote='"', escape='"')) TO '%s' (FORMAT PARQUET, COMPRESSION GZIP)`,
		selectSQL, filepath.ToSlash(csvPath), filepath.ToSlash(parquetPath+".tmp"),
	)
	if _, err := w.engine.ExecSQL(ctx, sql); err != nil {
		os.Remove(csvPath)
		os.Remove(parquetPath + ".tmp")
		return PartWriteResult{}, fmt.Errorf("duckdb parquet 写入失败: %w", err)
	}
	os.Remove(csvPath)
	if err := os.Rename(parquetPath+".tmp", parquetPath); err != nil {
		os.Remove(parquetPath + ".tmp")
		return PartWriteResult{}, err
	}
	rows, err := w.partRows(ctx, parquetPath)
	if err != nil {
		return PartWriteResult{}, err
	}
	info, err := os.Stat(parquetPath)
	if err != nil {
		return PartWriteResult{}, err
	}
	sha, err := fileSHA256(parquetPath)
	if err != nil {
		return PartWriteResult{}, err
	}
	return PartWriteResult{Path: parquetPath, SHA256: sha, Rows: rows, Bytes: info.Size()}, nil
}

func (w *ParquetPartWriter) partRows(ctx context.Context, path string) (int64, error) {
	rows, err := w.engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT COUNT(*) AS n FROM read_parquet('%s')`, filepath.ToSlash(path)))
	if err != nil || len(rows) == 0 {
		return 0, fmt.Errorf("parquet 行数校验失败: %w", err)
	}
	return int64(toFloat(rows[0]["n"])), nil
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}

func writeCanonicalCSV(path string, meta PartMeta, records []Record) error {
	if len(records) == 0 {
		return fmt.Errorf("空记录不写 Part")
	}
	dataset := records[0].Dataset
	cols := canonicalColumns(dataset)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(cols, ",") + "\n"); err != nil {
		return err
	}
	rangeID := fmt.Sprintf("%d-%d", meta.FromBlock, meta.ToBlock)
	buf := make([]byte, 0, 4096)
	for i := range records {
		row := canonicalRow(dataset, normalizeRecord(records[i], meta.Provider, rangeID))
		buf = buf[:0]
		for j, cell := range row {
			if j > 0 {
				buf = append(buf, ',')
			}
			buf = append(buf, '"')
			buf = append(buf, strings.ReplaceAll(strings.ReplaceAll(cell, `"`, `""`), "\n", " ")...)
			buf = append(buf, '"')
		}
		buf = append(buf, '\n')
		if _, err := f.Write(buf); err != nil {
			return err
		}
	}
	return f.Sync()
}

// ReadPartRecords 读取 JSONL Part 全部记录（恢复对账/测试用）。
func ReadPartRecords(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	dec := json.NewDecoder(f)
	for dec.More() {
		var r Record
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
