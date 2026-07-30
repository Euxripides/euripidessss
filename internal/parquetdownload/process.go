package parquetdownload

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
)

type downloadResult struct {
	file       *FileTask
	localPath  string
	checkpoint *checkpoint
	err        error
}

func (m *Manager) runJob(ctx context.Context, id string, settings Settings) {
	started := time.Now()
	m.mutate(id, func(job *Job) {
		job.Status = StatusRunning
		job.Stage = "download"
		job.StartedAt = &started
		setStage(job, "download", StatusRunning, 0, "准备下载队列")
	})
	job, err := m.Get(id)
	if err != nil {
		return
	}
	targetPath := filepath.Join(settings.DataRoot, "jobs", id, "target_addresses.csv")
	batchHash := addressBatchHash(job.Addresses.Addresses)
	if err := writeTargetAddresses(targetPath, job.Addresses.Addresses); err != nil {
		m.finishJob(id, StatusFailed, err)
		return
	}
	if err := ensurePipelineDiskCapacity(settings, job.Files); err != nil {
		m.finishJob(id, StatusFailed, err)
		return
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          8,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 45 * time.Second,
		},
	}
	queue := make(chan *FileTask)
	results := make(chan downloadResult)
	workerCount := minInt(settings.DownloadConcurrency, len(job.Files))
	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for file := range queue {
				select {
				case <-ctx.Done():
					results <- downloadResult{file: file, err: ctx.Err()}
					continue
				default:
				}
				if existing := m.loadCheckpoint(settings, file.SourceObject, batchHash); existing != nil &&
					(!job.ExportCSV || existing.CSVPath != "") {
					if info, statErr := os.Stat(existing.OutputPath); statErr == nil && !info.IsDir() {
						results <- downloadResult{file: file, checkpoint: existing}
						continue
					}
				}
				localPath := sourceLocalPath(settings, file.SourceObject)
				m.updateFile(id, file.URI, func(task *FileTask) {
					task.Status = "downloading"
					task.LocalPath = localPath
					task.Error = ""
				})
				err := downloadSource(ctx, client, file.SourceObject, localPath, func(downloaded int64) {
					m.updateDownloadProgress(id, file.URI, downloaded, started)
				})
				results <- downloadResult{file: file, localPath: localPath, err: err}
			}
		}()
	}
	go func() {
		for _, file := range job.Files {
			queue <- file
		}
		close(queue)
		workers.Wait()
		close(results)
	}()

	var failed int
	var processedBytes int64
	for result := range results {
		if result.err != nil {
			if errors.Is(result.err, context.Canceled) {
				m.updateFile(id, result.file.URI, func(task *FileTask) {
					task.Status = StatusCanceled
					task.Error = "已取消，.partial 文件已保留供重试续传"
				})
			} else {
				failed++
				m.updateFile(id, result.file.URI, func(task *FileTask) {
					task.Status = StatusFailed
					task.RetryCount++
					task.Error = result.err.Error()
				})
			}
			continue
		}
		if result.checkpoint != nil {
			processedBytes += result.file.SizeBytes
			m.applyCheckpoint(id, result.file.URI, result.checkpoint, processedBytes)
			continue
		}
		m.mutate(id, func(job *Job) {
			job.Stage = "schema"
			setStage(job, "schema", StatusRunning, stagePercent(job, "schema"), "校验源字段")
		})
		m.updateFile(id, result.file.URI, func(task *FileTask) { task.Status = "processing" })
		outcome, processErr := m.processSource(ctx, settings, targetPath, result.file.SourceObject, result.localPath, batchHash, job.ExportCSV)
		if processErr != nil {
			failed++
			m.updateFile(id, result.file.URI, func(task *FileTask) {
				task.Status = StatusFailed
				task.RetryCount++
				task.Error = processErr.Error()
			})
			continue
		}
		processedBytes += result.file.SizeBytes
		m.updateFile(id, result.file.URI, func(task *FileTask) {
			task.Status = StatusDone
			task.Progress = 100
			task.OutputPath = outcome.OutputPath
			task.CSVPath = outcome.CSVPath
			task.SourceRows = outcome.SourceRows
			task.MatchedRows = outcome.Matched
			task.Error = ""
		})
		m.mutate(id, func(job *Job) {
			job.Stage = "match"
			job.SourceRows += outcome.SourceRows
			job.MatchedRows += outcome.Matched
			job.Outputs = appendUnique(job.Outputs, outcome.OutputPath)
			if outcome.CSVPath != "" {
				job.Outputs = appendUnique(job.Outputs, outcome.CSVPath)
			}
			updateProcessingStages(job, processedBytes)
		})
		_ = m.saveCheckpoint(settings, result.file.SourceObject, batchHash, outcome)
		if !job.KeepSourceFiles {
			_ = os.Remove(result.localPath)
		}
	}

	select {
	case <-ctx.Done():
		m.finishJob(id, StatusCanceled, errors.New("任务已取消；已完成分区和 .partial 文件均已保留"))
		return
	default:
	}
	if failed > 0 {
		m.mutate(id, func(job *Job) { job.FailedFiles = failed })
		m.finishJob(id, StatusFailed, fmt.Errorf("%d 个分区失败，可点击重试从检查点继续", failed))
		return
	}
	m.mutate(id, func(job *Job) {
		job.Stage = "output"
		setStage(job, "download", StatusDone, 100, "所有分片已校验")
		setStage(job, "schema", StatusDone, 100, "必需字段已确认")
		setStage(job, "match", StatusDone, 100, "批量地址匹配完成")
		setStage(job, "output", StatusRunning, 90, "写入任务清单")
	})
	snapshot, _ := m.Get(id)
	manifestPath := filepath.Join(settings.DataRoot, "exports", id+"-manifest.json")
	if err := writeJSONAtomic(manifestPath, snapshot); err != nil {
		m.finishJob(id, StatusFailed, err)
		return
	}
	m.mutate(id, func(job *Job) {
		job.Outputs = appendUnique(job.Outputs, manifestPath)
		setStage(job, "output", StatusDone, 100, "Parquet、清单与可选 CSV 已提交")
	})
	m.finishJob(id, StatusDone, nil)
}

func ensurePipelineDiskCapacity(settings Settings, files []*FileTask) error {
	free, err := diskFreeBytes(settings.DataRoot)
	if err != nil {
		return err
	}
	var largest int64
	for _, file := range files {
		if file.SizeBytes > largest {
			largest = file.SizeBytes
		}
	}
	required := uint64(largest*int64(settings.DownloadConcurrency)) + uint64(settings.MinimumFreeGB)*1024*1024*1024
	if free < required {
		return fmt.Errorf(
			"磁盘空间不足：流水线需要保留空间 %s，当前可用 %s；请缩小日期范围或调整数据盘",
			formatBytes(int64(required)),
			formatBytes(int64(free)),
		)
	}
	return nil
}

func (m *Manager) finishJob(id, status string, finishErr error) {
	finished := time.Now()
	m.mutate(id, func(job *Job) {
		job.Status = status
		job.FinishedAt = &finished
		job.Progress = 100
		if status == StatusDone {
			job.Stage = "done"
			job.Error = ""
		} else {
			job.Stage = status
			if finishErr != nil {
				job.Error = finishErr.Error()
			}
		}
	})
	m.mu.Lock()
	delete(m.cancels, id)
	job := m.jobs[id]
	m.mu.Unlock()
	if job != nil {
		_ = m.persistJob(job, true)
	}
}

func (m *Manager) updateFile(id, uri string, mutate func(*FileTask)) {
	m.mutate(id, func(job *Job) {
		for _, file := range job.Files {
			if file.URI == uri {
				mutate(file)
				return
			}
		}
	})
}

func (m *Manager) updateDownloadProgress(id, uri string, downloaded int64, started time.Time) {
	m.mutate(id, func(job *Job) {
		var total int64
		for _, file := range job.Files {
			if file.URI == uri {
				file.DownloadedBytes = downloaded
				if file.SizeBytes > 0 {
					file.Progress = float64(downloaded) / float64(file.SizeBytes) * 100
				}
			}
			total += file.DownloadedBytes
		}
		job.DownloadedBytes = min64(total, job.TotalBytes)
		elapsed := time.Since(started).Seconds()
		if elapsed > 0 {
			job.DownloadSpeedBPS = float64(job.DownloadedBytes) / elapsed
			remaining := job.TotalBytes - job.DownloadedBytes
			if job.DownloadSpeedBPS > 0 && remaining > 0 {
				job.ETASeconds = int64(float64(remaining) / job.DownloadSpeedBPS)
			}
		}
		ratio := safeRatio(job.DownloadedBytes, job.TotalBytes)
		job.Progress = 5 + ratio*55
		setStage(job, "download", StatusRunning, ratio*100, fmt.Sprintf("%s / %s", formatBytes(job.DownloadedBytes), formatBytes(job.TotalBytes)))
	})
}

func (m *Manager) applyCheckpoint(id, uri string, existing *checkpoint, processedBytes int64) {
	m.updateFile(id, uri, func(task *FileTask) {
		task.Status = StatusDone
		task.Progress = 100
		task.DownloadedBytes = task.SizeBytes
		task.OutputPath = existing.OutputPath
		task.CSVPath = existing.CSVPath
		task.SourceRows = existing.SourceRows
		task.MatchedRows = existing.Matched
	})
	m.mutate(id, func(job *Job) {
		job.DownloadedBytes += existing.SizeBytes
		job.SourceRows += existing.SourceRows
		job.MatchedRows += existing.Matched
		job.Outputs = appendUnique(job.Outputs, existing.OutputPath)
		if existing.CSVPath != "" {
			job.Outputs = appendUnique(job.Outputs, existing.CSVPath)
		}
		updateProcessingStages(job, processedBytes)
	})
}

func updateProcessingStages(job *Job, processedBytes int64) {
	ratio := safeRatio(processedBytes, job.TotalBytes)
	setStage(job, "schema", StatusDone, 100, "已按源文件动态探测")
	setStage(job, "match", StatusRunning, ratio*100, fmt.Sprintf("已匹配 %d 行", job.MatchedRows))
	setStage(job, "output", StatusRunning, ratio*100, "ZSTD / Row Group 250,000")
	job.Progress = maxFloat(job.Progress, 60+ratio*35)
}

func setStage(job *Job, key, status string, progress float64, detail string) {
	for index := range job.Stages {
		if job.Stages[index].Key == key {
			job.Stages[index].Status = status
			job.Stages[index].Progress = progress
			job.Stages[index].Detail = detail
			return
		}
	}
}

func stagePercent(job *Job, key string) float64 {
	for _, stage := range job.Stages {
		if stage.Key == key {
			return stage.Progress
		}
	}
	return 0
}

func sourceLocalPath(settings Settings, source SourceObject) string {
	etag := sanitizeETag(source.ETag)
	return filepath.Join(
		settings.DataRoot,
		"staging",
		source.DataType,
		"date="+source.SourceDate,
		source.DataType+"-"+etag+".parquet",
	)
}

type processOutcome struct {
	OutputPath string
	CSVPath    string
	SourceRows int64
	Matched    int64
}

func (m *Manager) processSource(
	ctx context.Context,
	settings Settings,
	targetPath string,
	source SourceObject,
	localPath string,
	batchHash string,
	exportCSV bool,
) (processOutcome, error) {
	if m.engine == nil || !m.engine.Available() {
		return processOutcome{}, errors.New("DuckDB CLI 不可用，无法探测和筛选 Parquet")
	}
	if err := inspectRequiredSchema(ctx, m.engine, localPath); err != nil {
		return processOutcome{}, err
	}
	year, month := dateParts(source.SourceDate)
	outputDir := filepath.Join(settings.DataRoot, "warehouse", "transactions", "chain=bsc", "year="+year, "month="+month, "date="+source.SourceDate)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return processOutcome{}, err
	}
	outputPath := filepath.Join(outputDir, "part-"+sanitizeETag(source.ETag)+"-"+batchHash+".parquet")
	csvPath := ""
	if exportCSV {
		csvPath = filepath.Join(settings.DataRoot, "exports", "transactions-"+source.SourceDate+"-"+sanitizeETag(source.ETag)+"-"+batchHash+".csv")
		if err := os.MkdirAll(filepath.Dir(csvPath), 0755); err != nil {
			return processOutcome{}, err
		}
	}
	tempDir := filepath.Join(settings.DataRoot, "tmp")
	sqlText := buildProcessSQL(settings, targetPath, localPath, outputPath, csvPath, source.SourceDate)
	rows, err := m.engine.ExecSQLJSON(ctx, sqlText)
	if err != nil {
		return processOutcome{}, err
	}
	if len(rows) == 0 {
		return processOutcome{}, errors.New("DuckDB 未返回处理统计")
	}
	_ = tempDir
	return processOutcome{
		OutputPath: outputPath,
		CSVPath:    csvPath,
		SourceRows: numberToInt64(rows[0]["source_rows"]),
		Matched:    numberToInt64(rows[0]["matched_rows"]),
	}, nil
}

func inspectRequiredSchema(ctx context.Context, engine *duckdb.Engine, parquetPath string) error {
	rows, err := engine.ExecSQLJSON(ctx, "DESCRIBE SELECT * FROM read_parquet("+sqlString(parquetPath)+")")
	if err != nil {
		return fmt.Errorf("Schema 探测失败: %w", err)
	}
	found := map[string]bool{}
	for _, row := range rows {
		found[strings.ToLower(fmt.Sprint(row["column_name"]))] = true
	}
	required := []string{"hash", "block_number", "from_address", "to_address", "value", "block_timestamp"}
	var missing []string
	for _, name := range required {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("源 Parquet 缺少必需字段：%s；任务已停止，未静默写入", strings.Join(missing, ", "))
	}
	return nil
}

func buildProcessSQL(settings Settings, targetPath, sourcePath, outputPath, csvPath, sourceDate string) string {
	statements := []string{
		"SET memory_limit=" + sqlString(settings.MemoryLimit),
		"SET threads=" + strconv.Itoa(settings.DuckDBThreads),
		"SET temp_directory=" + sqlString(filepath.Join(settings.DataRoot, "tmp")),
		"SET preserve_insertion_order=false",
		"CREATE OR REPLACE TEMP TABLE target_addresses AS SELECT DISTINCT lower(address) AS address FROM read_csv(" + sqlString(targetPath) + ", header=true, all_varchar=true)",
		`CREATE OR REPLACE TEMP TABLE matched_transactions AS
WITH source AS (SELECT * FROM read_parquet(` + sqlString(sourcePath) + `)),
matched AS (
  SELECT s.* FROM source s SEMI JOIN target_addresses a ON lower(s.from_address) = a.address
  UNION
  SELECT s.* FROM source s SEMI JOIN target_addresses a ON lower(s.to_address) = a.address
)
SELECT
  'bsc' AS chain_key,
  56::UINTEGER AS chain_id,
  hash AS tx_hash,
  nonce,
  block_hash,
  block_number,
  transaction_index AS tx_index,
  lower(from_address) AS from_address,
  lower(to_address) AS to_address,
  value AS value_raw,
  CAST(TRY_CAST(value AS DECIMAL(38, 0)) / 1000000000000000000 AS DECIMAL(38, 18)) AS value_native,
  gas,
  gas_price AS gas_price_raw,
  input,
  CASE WHEN input IS NOT NULL AND length(input) >= 10 THEN substr(input, 1, 10) ELSE NULL END AS method_id,
  CASE WHEN to_address IS NULL OR trim(to_address) = '' THEN true ELSE false END AS is_contract_creation_candidate,
  to_timestamp(block_timestamp) AS block_time,
  DATE ` + sqlString(sourceDate) + ` AS source_date,
  ` + sqlString(sourcePath) + ` AS source_file,
  current_timestamp AS ingested_at
FROM matched`,
		"COPY matched_transactions TO " + sqlString(outputPath) + " (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 250000)",
	}
	if csvPath != "" {
		statements = append(statements, "COPY matched_transactions TO "+sqlString(csvPath)+" (FORMAT CSV, HEADER true)")
	}
	statements = append(statements,
		"SELECT COALESCE((SELECT SUM(num_rows) FROM parquet_file_metadata("+sqlString(sourcePath)+")), 0) AS source_rows, (SELECT COUNT(*) FROM matched_transactions) AS matched_rows",
	)
	return strings.Join(statements, "; ")
}

func writeTargetAddresses(path string, addresses []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"address"}); err != nil {
		file.Close()
		return err
	}
	for _, address := range addresses {
		if err := writer.Write([]string{address}); err != nil {
			file.Close()
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func (m *Manager) checkpointPath(settings Settings, source SourceObject, addressHash string) string {
	sum := sha256.Sum256([]byte(source.URI + "|" + source.ETag + "|" + addressHash))
	return filepath.Join(settings.DataRoot, "checkpoints", hex.EncodeToString(sum[:8])+".json")
}

func (m *Manager) loadCheckpoint(settings Settings, source SourceObject, addressHash string) *checkpoint {
	content, err := os.ReadFile(m.checkpointPath(settings, source, addressHash))
	if err != nil {
		return nil
	}
	var item checkpoint
	if jsonErr := json.Unmarshal(content, &item); jsonErr != nil {
		return nil
	}
	if item.SourceURI != source.URI || item.ETag != source.ETag || item.AddressHash != addressHash || item.SizeBytes != source.SizeBytes {
		return nil
	}
	return &item
}

func (m *Manager) saveCheckpoint(settings Settings, source SourceObject, addressHash string, outcome processOutcome) error {
	return writeJSONAtomic(m.checkpointPath(settings, source, addressHash), checkpoint{
		SourceURI:   source.URI,
		ETag:        source.ETag,
		AddressHash: addressHash,
		SizeBytes:   source.SizeBytes,
		OutputPath:  outcome.OutputPath,
		CSVPath:     outcome.CSVPath,
		SourceRows:  outcome.SourceRows,
		Matched:     outcome.Matched,
		Completed:   time.Now(),
	})
}

func addressBatchHash(addresses []string) string {
	sum := sha256.Sum256([]byte(strings.Join(addresses, "\n")))
	return hex.EncodeToString(sum[:6])
}

func sanitizeETag(etag string) string {
	etag = strings.Trim(strings.TrimSpace(etag), `"`)
	etag = strings.ReplaceAll(etag, "-", "")
	if len(etag) > 16 {
		etag = etag[:16]
	}
	if etag == "" {
		etag = "unknown"
	}
	return etag
}

func dateParts(date string) (string, string) {
	parts := strings.Split(date, "-")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "unknown", "unknown"
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(filepath.ToSlash(value), "'", "''") + "'"
}

func numberToInt64(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return result
	case string:
		result, _ := strconv.ParseInt(typed, 10, 64)
		return result
	default:
		result, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		return result
	}
}

func appendUnique(items []string, value string) []string {
	if value == "" {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func safeRatio(value, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(value) / float64(total)
}

func formatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, exponent := int64(unit), 0
	for quotient := value / unit; quotient >= unit; quotient /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(divisor), "KMGTPE"[exponent])
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
