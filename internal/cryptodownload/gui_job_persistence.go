package cryptodownload

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func guiPersistedRequest(req GUIStartRequest) GUIJobPersistedRequest {
	return GUIJobPersistedRequest{
		Source: req.Source, RPCURL: req.RPCURL, Chains: req.Chains, NativeSymbol: req.NativeSymbol,
		CSVEmail: req.CSVEmail, CSVIMAPHost: req.CSVIMAPHost, CSVIMAPPort: req.CSVIMAPPort,
		CSVIMAPUser: req.CSVIMAPUser, CSVDeliveryMode: req.CSVDeliveryMode, CSVStartTime: req.CSVStartTime, CSVEndTime: req.CSVEndTime,
		StartBlock: req.StartBlock, EndBlock: req.EndBlock, CutoffBlock: req.CutoffBlock,
		TraceMode: req.TraceMode, BlockBatch: req.BlockBatch, LogBatch: req.LogBatch,
		Workers: req.Workers, RPS: req.RPS, TimeoutSeconds: req.TimeoutSeconds,
		Retries: req.Retries, PageSize: req.PageSize, RawDir: req.RawDir,
		OutputDir: req.OutputDir, OutputPrefix: req.OutputPrefix, AMLLabels: req.AMLLabels,
		AMLRPS: req.AMLRPS, FilterExchange: req.FilterExchange, Details: req.Details, ScanNative: req.ScanNative,
		Incremental: req.Incremental, RiskCooldownSecs: req.RiskCooldownSecs,
	}
}

func (j *GUIJob) persistedRecord() GUIJobRecord {
	j.mu.Lock()
	defer j.mu.Unlock()
	summaries := j.checkpointSummariesLocked()
	return GUIJobRecord{
		Version: guiJobStoreVersion, ID: j.ID, Status: j.Status, Message: j.Message,
		Progress: j.Progress, Done: j.Done, Total: j.Total, Running: j.Running,
		NeedsCredentials: j.NeedsCredentials, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
		TaskDir: j.TaskDir, Request: guiPersistedRequest(j.request), Entries: append([]GUIAddressChain(nil), j.entries...),
		Addresses:         cloneGUIAddressProgress(j.Addresses),
		CheckpointSummary: summaries,
		CooldownUntil:     j.CooldownUntil,
	}
}

func (j *GUIJob) save(event string) error {
	record := j.persistedRecord()
	if j.store != nil {
		if err := j.store.Save(record); err != nil {
			return &GUIJobPersistError{JobID: j.ID, Event: event, Err: err}
		}
	}
	j.mu.Lock()
	history, historyID := j.history, j.historyID
	j.mu.Unlock()
	if history == nil {
		return nil
	}
	if historyID != "" {
		record.ID = historyID
	}
	if err := history.Save(record); err != nil {
		return &GUIJobPersistError{JobID: j.ID, Event: event, Err: err}
	}
	return nil
}

func (j *GUIJob) persist() {
	if err := j.save("lifecycle update"); err != nil {
		j.mu.Lock()
		j.Errors = append(j.Errors, "保存任务状态失败: "+err.Error())
		j.mu.Unlock()
	}
}

func (j *GUIJob) checkpointSummariesLocked() []GUIJobCheckpointSummary {
	summaries := make([]GUIJobCheckpointSummary, 0, len(j.entries))
	for index, entry := range j.entries {
		summary := GUIJobCheckpointSummary{Address: entry.Address, Chain: entry.Chain}
		path := filepath.Join(j.request.RawDir, "csv_"+strings.ToLower(strings.TrimSpace(entry.Chain)), strings.ToLower(strings.TrimSpace(entry.Address)), "export_state.json")
		encoded, err := os.ReadFile(path)
		var state CSVCheckpointState
		if err == nil && json.Unmarshal(encoded, &state) == nil {
			summary.Complete = len(state.Kinds) > 0
			for _, checkpoint := range state.Kinds {
				summary.Complete = summary.Complete && checkpoint.Complete
				summary.Segments += len(checkpoint.Segments)
				for _, segment := range checkpoint.Segments {
					summary.Rows += segment.Rows
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) && err != nil {
			summary.Complete = false
		}
		if index < len(j.Addresses) && summary.Rows == 0 {
			summary.Rows = int64(j.Addresses[index].Downloaded)
			summary.Complete = j.Addresses[index].Status == "done"
			for _, part := range j.Addresses[index].Parts {
				if part.Downloaded > 0 {
					summary.Segments++
				}
			}
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func restoreGUIJob(record GUIJobRecord, store *GUIJobStore, history *GUIDownloadHistoryStore, settings GUIPersistedSettings) *GUIJob {
	r := record.Request
	req := GUIStartRequest{
		Source: r.Source, RPCURL: r.RPCURL, AddressChains: append([]GUIAddressChain(nil), record.Entries...), Chains: r.Chains,
		NativeSymbol: r.NativeSymbol, CSVEmail: r.CSVEmail, CSVIMAPHost: r.CSVIMAPHost,
		CSVIMAPPort: r.CSVIMAPPort, CSVIMAPUser: r.CSVIMAPUser, CSVDeliveryMode: r.CSVDeliveryMode, CSVStartTime: r.CSVStartTime,
		CSVEndTime: r.CSVEndTime, StartBlock: r.StartBlock, EndBlock: r.EndBlock,
		CutoffBlock: r.CutoffBlock, TraceMode: r.TraceMode, BlockBatch: r.BlockBatch,
		LogBatch: r.LogBatch, Workers: r.Workers, RPS: r.RPS, TimeoutSeconds: r.TimeoutSeconds,
		Retries: r.Retries, PageSize: r.PageSize, RawDir: r.RawDir, OutputDir: r.OutputDir,
		OutputPrefix: r.OutputPrefix, AMLLabels: r.AMLLabels, AMLRPS: r.AMLRPS,
		FilterExchange: r.FilterExchange, Details: r.Details, ScanNative: r.ScanNative,
		Incremental: r.Incremental, RiskCooldownSecs: r.RiskCooldownSecs,
	}
	interrupted := record.Running || record.Status == "running"
	resumable := interrupted || record.Status == "paused" || record.Status == "pending" || record.Status == "cooling"
	needsCredentials := resumable && record.NeedsCredentials
	if resumable && strings.EqualFold(req.Source, "csv") {
		req.CSVIMAPPassword = settings.CSVIMAPPassword
		needsCredentials = strings.TrimSpace(req.CSVIMAPPassword) == ""
	}
	status, message := record.Status, record.Message
	addresses := cloneGUIAddressProgress(record.Addresses)
	if interrupted {
		status, message = "paused", "程序已重启，任务已安全暂停，可继续下载"
		for index := range addresses {
			if addresses[index].Status == "running" {
				addresses[index].Status = "paused"
				addresses[index].Message = message
			}
		}
	}
	if needsCredentials {
		status, message = "paused", "继续下载前请在设置中补充邮箱密码或重新提交凭据"
	}
	return &GUIJob{
		ID: record.ID, Status: status, Message: message, Progress: record.Progress,
		Done: record.Done, Total: record.Total, Running: false, NeedsCredentials: needsCredentials,
		StartedAt: record.StartedAt, FinishedAt: record.FinishedAt, TaskDir: record.TaskDir,
		Addresses: addresses, CheckpointSummary: append([]GUIJobCheckpointSummary(nil), record.CheckpointSummary...),
		Incremental: req.Incremental, CooldownUntil: record.CooldownUntil,
		request: req, entries: append([]GUIAddressChain(nil), record.Entries...), store: store, history: history, historyID: record.ID,
		addressCancels: map[int]context.CancelFunc{}, cancelledAddresses: map[int]bool{},
	}
}
