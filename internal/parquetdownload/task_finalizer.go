package parquetdownload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	datasetwriter "github.com/etl/backend/internal/writer"
)

func (m *Manager) reconcileFinalManifests() {
	for _, job := range m.List() {
		if job.Status != StatusDone && job.Status != StatusFailed && job.Status != StatusCanceled {
			continue
		}
		csvBackfill := needsCSVBackfill(job)
		if !manifestNeedsRepair(job) && !csvBackfill {
			continue
		}
		var finishErr error
		if job.Error != "" {
			finishErr = errors.New(job.Error)
		}
		if job.Status == StatusDone && csvBackfill {
			parquetOutputs := parquetOutputPaths(job.Outputs)
			csvOutputs, exportErr := m.exportDatasetCSVs(context.Background(), job.ID, m.Settings(), parquetOutputs)
			if exportErr != nil {
				finishErr = fmt.Errorf("补生成历史任务 CSV: %w", exportErr)
				job.Status = StatusFailed
			} else {
				m.mutate(job.ID, func(item *Job) {
					for _, output := range csvOutputs {
						item.Outputs = appendUnique(item.Outputs, output)
					}
					addTaskEvent(item, "CSV_BACKFILLED", fmt.Sprintf("从现有 Parquet 补生成 %d 个 CSV", len(csvOutputs)), "output", map[string]any{
						"csv_count": len(csvOutputs),
					})
				})
			}
		}
		m.finalizeJob(job.ID, job.Status, finishErr)
	}
}

func manifestNeedsRepair(job *Job) bool {
	if job == nil || job.SchemaVersion != datasetwriter.ManifestSchemaVersion ||
		job.Manifest.SchemaVersion != datasetwriter.ManifestSchemaVersion ||
		!job.Manifest.Consistent || job.Manifest.Path == "" {
		return true
	}
	content, err := os.ReadFile(job.Manifest.Path)
	if err != nil {
		return true
	}
	var manifest Job
	if json.Unmarshal(content, &manifest) != nil {
		return true
	}
	return manifest.Status != job.Status || manifest.Stage != job.Stage ||
		manifest.Progress != job.Progress || manifest.FinishedAt == nil
}

func needsCSVBackfill(job *Job) bool {
	if job == nil || job.Status != StatusDone || !job.ExportCSV {
		return false
	}
	hasParquet := false
	hasCSV := false
	for _, output := range job.Outputs {
		switch strings.ToLower(filepath.Ext(output)) {
		case ".parquet":
			hasParquet = true
		case ".csv":
			hasCSV = true
		}
	}
	return hasParquet && !hasCSV
}

func parquetOutputPaths(outputs []string) []string {
	result := make([]string, 0, len(outputs))
	for _, output := range outputs {
		if strings.EqualFold(filepath.Ext(output), ".parquet") {
			result = append(result, output)
		}
	}
	return result
}

func (m *Manager) finalizeJob(id, requestedStatus string, finishErr error) {
	settings := m.Settings()
	manifestPath := filepath.Join(settings.DataRoot, "exports", id+"-manifest.json")

	snapshot, getErr := m.Get(id)
	if getErr != nil {
		return
	}
	checksumPaths, missing := existingOutputPaths(snapshot.Outputs, manifestPath)
	checksums, checksumErr := datasetwriter.ComputeChecksums(checksumPaths)
	finalStatus := requestedStatus
	finalErr := finishErr
	if requestedStatus == StatusDone {
		if len(missing) > 0 {
			finalStatus = StatusFailed
			finalErr = fmt.Errorf("最终输出检查失败，缺少文件：%v", missing)
		} else if checksumErr != nil {
			finalStatus = StatusFailed
			finalErr = fmt.Errorf("最终输出 SHA256 失败: %w", checksumErr)
		}
	} else if checksumErr != nil && finalErr == nil {
		finalErr = checksumErr
	}

	finished := time.Now()
	m.mutate(id, func(job *Job) {
		job.SchemaVersion = datasetwriter.ManifestSchemaVersion
		job.Status = finalStatus
		job.FinishedAt = &finished
		job.CancellationRequested = finalStatus == StatusCanceled || job.CancellationRequested
		job.Checksums = checksums
		job.Outputs = appendUnique(job.Outputs, manifestPath)
		settleFileTasks(job, finalStatus)
		settleStages(job, finalStatus)
		switch finalStatus {
		case StatusDone:
			job.Stage = "done"
			job.Progress = 100
			job.Error = ""
			setStage(job, "output", StatusDone, 100, "输出检查、SHA256 与最终清单已提交")
			addTaskEvent(job, "JOB_COMPLETED", "任务成功完成", "done", map[string]any{
				"output_count":   len(job.Outputs),
				"checksum_count": len(checksums),
			})
		case StatusCanceled:
			job.Stage = "canceled"
			if finalErr != nil {
				job.Error = finalErr.Error()
			} else {
				job.Error = "任务已取消"
			}
			addTaskEvent(job, "JOB_CANCELED", "Worker 已停止，任务取消完成", "canceled", nil)
		default:
			job.Stage = "failed"
			if finalErr != nil {
				job.Error = finalErr.Error()
			} else {
				job.Error = "任务执行失败"
			}
			addTaskEvent(job, "WORKER_FAILED", job.Error, "failed", nil)
		}
		updateCoverage(job)
		job.Manifest = ManifestInfo{
			Path:          manifestPath,
			Status:        finalStatus,
			SchemaVersion: datasetwriter.ManifestSchemaVersion,
			Consistent:    false,
			FinishedAt:    &finished,
		}
	})

	m.mu.Lock()
	delete(m.cancels, id)
	job := m.jobs[id]
	m.mu.Unlock()
	if job == nil {
		return
	}
	_ = m.persistJob(job, true)
	finalSnapshot, err := m.Get(id)
	if err != nil {
		return
	}
	finalSnapshot.Manifest.Consistent = true
	if err := datasetwriter.WriteJSONAtomic(manifestPath, finalSnapshot); err != nil {
		manifestErr := fmt.Errorf("最终 Manifest 原子提交失败: %w", err)
		m.mutate(id, func(item *Job) {
			item.Status = StatusFailed
			item.Stage = "failed"
			item.Error = manifestErr.Error()
			item.Manifest.Status = StatusFailed
			item.Manifest.Consistent = false
			settleStages(item, StatusFailed)
			addTaskEvent(item, "MANIFEST_FAILED", manifestErr.Error(), "output", nil)
			updateCoverage(item)
		})
		if failedSnapshot, snapshotErr := m.Get(id); snapshotErr == nil {
			_ = datasetwriter.WriteJSONAtomic(manifestPath, failedSnapshot)
		}
		if current, currentErr := m.Get(id); currentErr == nil {
			_ = m.persistJob(current, true)
		}
		return
	}
	m.mutate(id, func(item *Job) {
		item.Manifest.Consistent = true
	})
	if current, currentErr := m.Get(id); currentErr == nil {
		_ = m.persistJob(current, true)
	}
}

func existingOutputPaths(outputs []string, manifestPath string) ([]string, []string) {
	paths := make([]string, 0, len(outputs))
	var missing []string
	for _, output := range outputs {
		if output == "" || filepath.Clean(output) == filepath.Clean(manifestPath) {
			continue
		}
		info, err := os.Stat(output)
		if err != nil || info.IsDir() {
			missing = append(missing, output)
			continue
		}
		paths = append(paths, output)
	}
	return paths, missing
}

func settleFileTasks(job *Job, terminalStatus string) {
	for _, file := range job.Files {
		if file == nil {
			continue
		}
		switch terminalStatus {
		case StatusCanceled:
			if file.Status == StatusRunning || file.Status == StatusQueued ||
				file.Status == "downloading" || file.Status == "processing" {
				file.Status = StatusCanceled
				if file.Error == "" {
					file.Error = "任务已取消，分片检查点已保留"
				}
			}
		case StatusFailed:
			if file.Status == StatusRunning || file.Status == "downloading" || file.Status == "processing" {
				file.Status = StatusFailed
				if file.Error == "" {
					file.Error = "任务执行失败"
				}
			}
		case StatusDone:
			if file.Status != StatusDone {
				file.Status = StatusFailed
				if file.Error == "" {
					file.Error = "任务结束但文件未完成"
				}
			}
		}
	}
}
