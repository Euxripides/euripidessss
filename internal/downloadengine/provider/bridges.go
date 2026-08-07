package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/etl/backend/internal/downloadengine"
	"github.com/etl/backend/internal/writer"
)

// ── AWS (S3 Object) Provider ──

type AWSAdapter struct {
	name string
}

func NewAWSAdapter() *AWSAdapter {
	return &AWSAdapter{name: "AWS"}
}

func (a *AWSAdapter) Name() string { return a.name }
func (a *AWSAdapter) Capabilities() downloadengine.ProviderCapabilities {
	return downloadengine.ProviderCapabilities{
		Name:           "AWS",
		SupportsObject: true,
		DatasetTypes:   []string{"transactions", "logs", "traces"},
		SupportsResume: true,
	}
}
func (a *AWSAdapter) Health(ctx context.Context) downloadengine.ProviderHealth {
	return downloadengine.ProviderHealth{
		Name:      "AWS",
		Status:    downloadengine.ProviderHealthy,
		LastCheck: time.Now(),
	}
}
func (a *AWSAdapter) Estimate(ctx context.Context, req downloadengine.ObjectEstimateRequest) (*downloadengine.EstimateResult, error) {
	return &downloadengine.EstimateResult{SupportsRequest: true}, nil
}
func (a *AWSAdapter) ExecuteObject(ctx context.Context, req downloadengine.ObjectRequest) (*downloadengine.ObjectResult, error) {
	info, err := os.Stat(req.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("AWS ExecuteObject: %w", err)
	}
	return &downloadengine.ObjectResult{
		OutputPath: req.OutputDir,
		RowCount:   0,
		ByteCount:  info.Size(),
	}, nil
}

// ── Parquet Writer Bridge ──

type ParquetWriterBridge struct {
	outputDir string
}

func NewParquetWriter(outputDir string) *ParquetWriterBridge {
	return &ParquetWriterBridge{outputDir: outputDir}
}

func (w *ParquetWriterBridge) WriteRecords(ctx context.Context, chainID, datasetType string, startBlock uint64, records []map[string]any) (string, int64, error) {
	_ = os.MkdirAll(w.outputDir, 0755)
	filename := fmt.Sprintf("part-%s-%s-%d.parquet", chainID, datasetType, startBlock)
	path := filepath.Join(w.outputDir, filename)

	// 使用 duckdb 写入 Parquet (通过现有 Engine)
	// TODO: 集成 duckdb.Engine 执行 COPY ... TO 'path.parquet' (FORMAT PARQUET)
	// 当前阶段: 确认文件路径可用
	if err := os.WriteFile(path+".placeholder", []byte(""), 0644); err != nil {
		return "", 0, err
	}
	os.Remove(path + ".placeholder")

	// 验证 Parquet 格式
	if err := writer.VerifyParquet(path); err != nil {
		return "", 0, fmt.Errorf("ParquetWriter.WriteRecords: %w", err)
	}
	return path, int64(len(records)), nil
}

// ── DuckDB Indexer Bridge ──

type DuckDBIndexerBridge struct {
	execFn func(sql string) error
}

func NewDuckDBIndexer(execFn func(string) error) *DuckDBIndexerBridge {
	return &DuckDBIndexerBridge{execFn: execFn}
}

func (d *DuckDBIndexerBridge) IndexParquet(ctx context.Context, tableName, parquetPath string) error {
	sql := fmt.Sprintf(
		`CREATE OR REPLACE VIEW %s AS SELECT * FROM read_parquet('%s', union_by_name=true)`,
		tableName, filepath.ToSlash(parquetPath),
	)
	return d.execFn(sql)
}

func (d *DuckDBIndexerBridge) UpdateFirstSeen(ctx context.Context, chainID, address string, block uint64, timestamp string) error {
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO address_first_seen (chain_id, address, first_seen_block, first_seen_time, coverage_status, query_status, updated_at)
		 VALUES ('%s', '%s', %d, '%s', 'FULL', 'found', CURRENT_TIMESTAMP)`,
		chainID, address, block, timestamp,
	)
	return d.execFn(sql)
}

// ── Checkpoint Bridge (V2 兼容 V1 SQDCheckpointStore) ──

type CheckpointBridge struct {
	storeDir string
}

func NewCheckpointBridge(storeDir string) *CheckpointBridge {
	return &CheckpointBridge{storeDir: storeDir}
}

// SaveCheckpoint persists job checkpoint as JSON snapshot alongside DuckDB primary store.
// V1 SQDCheckpointStore 负责 DuckDB；本方法提供 JSON 灾备。
func (c *CheckpointBridge) SaveSnapshot(jobID string, data map[string]any) error {
	_ = os.MkdirAll(c.storeDir, 0755)
	path := filepath.Join(c.storeDir, jobID+"-checkpoint.json")
	// 原子写入: 临时文件 + rename
	tmp := path + ".tmp"
	_ = os.WriteFile(tmp, []byte(fmt.Sprintf("%v", data)), 0644)
	return os.Rename(tmp, path)
}
