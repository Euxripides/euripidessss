package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource"
	awssrc "github.com/etl/backend/internal/datasource/aws"
	"github.com/etl/backend/internal/downloadengine"
	"github.com/etl/backend/internal/parquetdownload"
	"github.com/etl/backend/internal/writer"
)

// ── Real AWSAdapter (replaces stub bridges.go version) ──
// Bridges existing parquetdownload discoverer for real S3 object listing + download.

type RealAWSAdapter struct {
	httpClient *http.Client
}

func NewRealAWSAdapter() *RealAWSAdapter {
	return &RealAWSAdapter{httpClient: &http.Client{Timeout: 90 * time.Second}}
}

func (a *RealAWSAdapter) Name() string { return "AWS" }
func (a *RealAWSAdapter) Capabilities() downloadengine.ProviderCapabilities {
	return downloadengine.ProviderCapabilities{
		Name:           "AWS",
		SupportsObject: true,
		DatasetTypes:   []string{"transactions"},
		MaxBlockRange:  0,
		SupportsResume: true,
	}
}
func (a *RealAWSAdapter) Health(ctx context.Context) downloadengine.ProviderHealth {
	return downloadengine.ProviderHealth{Name: "AWS", Status: downloadengine.ProviderHealthy, LastCheck: time.Now()}
}
func (a *RealAWSAdapter) Estimate(ctx context.Context, req downloadengine.ObjectEstimateRequest) (*downloadengine.EstimateResult, error) {
	return &downloadengine.EstimateResult{SupportsRequest: true}, nil
}

// ExecuteObject downloads an S3 object to the output directory and verifies Parquet format.
func (a *RealAWSAdapter) ExecuteObject(ctx context.Context, req downloadengine.ObjectRequest) (*downloadengine.ObjectResult, error) {
	_ = os.MkdirAll(req.OutputDir, 0755)
	base := filepath.Base(req.SourceURI)
	outPath := filepath.Join(req.OutputDir, base)

	// If file already exists locally, skip download (resume)
	if info, err := os.Stat(outPath); err == nil && info.Size() > 0 {
		if verr := writer.VerifyParquet(outPath); verr == nil {
			return &downloadengine.ObjectResult{OutputPath: outPath, RowCount: 0, ByteCount: info.Size()}, nil
		}
		// corrupt → re-download
		_ = os.Remove(outPath)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.SourceURI, nil)
	if err != nil {
		return nil, fmt.Errorf("AWS ExecuteObject: %w", err)
	}
	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("AWS download %s: %w", req.SourceURI, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AWS download %s: HTTP %d", req.SourceURI, resp.StatusCode)
	}

	tmp := outPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	written, copyErr := io.Copy(f, io.LimitReader(resp.Body, 1<<40))
	_ = f.Close()

	if copyErr != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("AWS copy: %w", copyErr)
	}
	if err := os.Rename(tmp, outPath); err != nil {
		return nil, err
	}

	if verr := writer.VerifyParquet(outPath); verr != nil {
		return nil, fmt.Errorf("AWS Parquet verify %s: %w", outPath, verr)
	}

	return &downloadengine.ObjectResult{OutputPath: outPath, RowCount: 0, ByteCount: written}, nil
}

// ── Real DuckDB Indexer ──

type RealDuckDBIndexer struct {
	engine *duckdb.Engine
}

func NewRealDuckDBIndexer(engine *duckdb.Engine) *RealDuckDBIndexer {
	return &RealDuckDBIndexer{engine: engine}
}

func (d *RealDuckDBIndexer) IndexParquet(ctx context.Context, tableName, parquetPath string) error {
	absPath, err := filepath.Abs(parquetPath)
	if err != nil {
		return err
	}
	sql := fmt.Sprintf(
		`CREATE OR REPLACE VIEW "%s" AS SELECT * FROM read_parquet('%s', union_by_name=true)`,
		tableName, strings.ReplaceAll(absPath, "\\", "/"),
	)
	_, err = d.engine.ExecSQL(ctx, sql)
	return err
}

func (d *RealDuckDBIndexer) IndexParquetGlob(ctx context.Context, tableName, globPattern string) error {
	absPattern, err := filepath.Abs(globPattern)
	if err != nil {
		return err
	}
	sql := fmt.Sprintf(
		`CREATE OR REPLACE VIEW "%s" AS SELECT * FROM read_parquet('%s', union_by_name=true)`,
		tableName, strings.ReplaceAll(absPattern, "\\", "/"),
	)
	_, err = d.engine.ExecSQL(ctx, sql)
	return err
}

func (d *RealDuckDBIndexer) UpdateFirstSeen(ctx context.Context, chainID, address string, block uint64, timestamp string) error {
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO address_first_seen (chain_id, address, first_seen_block, first_seen_time, coverage_status, query_status, updated_at, created_at)
		 VALUES ('%s', '%s', %d, '%s', 'FULL', 'found', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		chainID, address, block, timestamp,
	)
	_, err := d.engine.ExecSQL(ctx, sql)
	return err
}

func (d *RealDuckDBIndexer) TableRowCount(ctx context.Context, tableName string) (int, error) {
	return d.engine.TableRowCount(ctx, tableName)
}

// ── Real Checkpoint Bridge (SQDCheckpointStore) ──

type RealCheckpointBridge struct {
	store    *parquetdownload.SQDCheckpointStore
	storeDir string
}

func NewRealCheckpointBridge(storeDir string) *RealCheckpointBridge {
	return &RealCheckpointBridge{
		store:    parquetdownload.NewSQDCheckpointStore(storeDir),
		storeDir: storeDir,
	}
}

// Save persists checkpoint using SQDCheckpointStore (DuckDB) + JSON snapshot (灾备).
func (c *RealCheckpointBridge) Save(ctx context.Context, jobID string, startBlock, endBlock, currentBlock uint64, completedChunks []parquetdownload.SQDBlockChunk) error {
	chk := &parquetdownload.SQDCheckpoint{
		JobID:        jobID,
		Chain:        "bsc", // caller should set
		Dataset:      "transactions",
		StartBlock:   startBlock,
		EndBlock:     endBlock,
		CurrentBlock: currentBlock,
	}

	for _, cc := range completedChunks {
		chk.CompletedChunks = append(chk.CompletedChunks, cc)
		chk.Manifest.CompletedBlocks += cc.To - cc.From
		chk.Manifest.TotalBlocks += cc.To - cc.From
	}

	if err := c.store.Save(chk); err != nil {
		return fmt.Errorf("RealCheckpointBridge.Save: %w", err)
	}

	// JSON 灾备
	_ = os.MkdirAll(c.storeDir, 0755)
	jsonPath := filepath.Join(c.storeDir, jobID+"-checkpoint-v2.json")
	tmp := jsonPath + ".tmp"
	data := fmt.Sprintf(`{"job_id":"%s","start_block":%d,"end_block":%d,"current_block":%d,"chunks_completed":%d}`,
		jobID, startBlock, endBlock, currentBlock, len(completedChunks))
	_ = os.WriteFile(tmp, []byte(data), 0644)
	_ = os.Rename(tmp, jsonPath)
	return nil
}

// Load 从 SQDCheckpointStore 恢复 checkpoint，并校验对应文件是否存在。
func (c *RealCheckpointBridge) Load(ctx context.Context, jobID string) (*parquetdownload.SQDCheckpoint, error) {
	chk, err := c.store.Load(jobID)
	if err != nil {
		return nil, fmt.Errorf("RealCheckpointBridge.Load: %w", err)
	}
	// 校验已完成的 chunk 文件是否真实存在（PRD §20 要求）
	for _, cc := range chk.CompletedChunks {
		glob := filepath.Join(c.storeDir, fmt.Sprintf("part-*-%d-%d.parquet", cc.From, cc.To))
		matches, _ := filepath.Glob(glob)
		if len(matches) == 0 {
			return nil, fmt.Errorf("checkpoint chunk [%d,%d] file missing, checkpoint corrupted", cc.From, cc.To)
		}
	}
	return chk, nil
}

// ── BSC Single-Address Pipeline Helper ──

// Dispatcher is a convenience type that wires ProviderRouter + ParquetWriter + Indexer + Checkpoint
// into a single-call pipeline for BSC single-address scenarios.
type Dispatcher struct {
	Router    *downloadengine.Router
	Writer    *RealParquetWriter
	Indexer   *RealDuckDBIndexer
	Checkpoint *RealCheckpointBridge
}

func NewDispatcher(router *downloadengine.Router, writer *RealParquetWriter, indexer *RealDuckDBIndexer, cp *RealCheckpointBridge) *Dispatcher {
	return &Dispatcher{Router: router, Writer: writer, Indexer: indexer, Checkpoint: cp}
}

// ── Real Parquet Writer (DuckDB COPY TO PARQUET) ──

type RealParquetWriter struct {
	engine    *duckdb.Engine
	outputDir string
}

func NewRealParquetWriter(engine *duckdb.Engine, outputDir string) *RealParquetWriter {
	return &RealParquetWriter{engine: engine, outputDir: outputDir}
}

// WriteFromSource 从源 parquet/CSV 路径执行 COPY TO PARQUET 到输出目录。
func (w *RealParquetWriter) WriteFromSource(ctx context.Context, tableName, sourcePath string) (string, error) {
	_ = os.MkdirAll(w.outputDir, 0755)
	outPath := filepath.Join(w.outputDir, tableName+".parquet")

	// DuckDB: COPY (SELECT * FROM read_parquet(source)) TO 'output.parquet' (FORMAT PARQUET)
	sql := fmt.Sprintf(
		`COPY (SELECT * FROM read_parquet('%s', union_by_name=true)) TO '%s' (FORMAT PARQUET)`,
		strings.ReplaceAll(sourcePath, "\\", "/"),
		strings.ReplaceAll(outPath, "\\", "/"),
	)
	if _, err := w.engine.ExecSQL(ctx, sql); err != nil {
		return "", fmt.Errorf("RealParquetWriter.WriteFromSource: %w", err)
	}
	return outPath, nil
}

// ── Real S3 Discoverer Bridge ──

type RealS3Discoverer struct {
	awsAdapter *awssrc.Adapter
}

func NewRealS3Discoverer() *RealS3Discoverer {
	return &RealS3Discoverer{awsAdapter: awssrc.New(&http.Client{Timeout: 90 * time.Second})}
}

func (d *RealS3Discoverer) Discover(ctx context.Context, chainKey, startDate, endDate string) ([]datasource.Object, error) {
	network, err := chain.Resolve(chainKey)
	if err != nil {
		return nil, err
	}
	objects, err := d.awsAdapter.DiscoverTransactions(ctx, network, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("RealS3Discoverer.Discover: %w", err)
	}
	for i := range objects {
		objects[i].URI = awssrc.HTTPURL(awssrc.DefaultEndpoint, objects[i])
	}
	return objects, nil
}
