package cryptodownload

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func guiStartRequestFromPersisted(record GUIJobRecord, settings GUIPersistedSettings) GUIStartRequest {
	r := record.Request
	req := GUIStartRequest{
		Source: r.Source, RPCURL: r.RPCURL, AddressChains: append([]GUIAddressChain(nil), record.Entries...), Chains: r.Chains,
		NativeSymbol: r.NativeSymbol, CSVEmail: r.CSVEmail, CSVIMAPHost: r.CSVIMAPHost,
		CSVIMAPPort: r.CSVIMAPPort, CSVIMAPUser: r.CSVIMAPUser, CSVStartTime: r.CSVStartTime,
		CSVEndTime: r.CSVEndTime, StartBlock: r.StartBlock, EndBlock: r.EndBlock,
		CutoffBlock: r.CutoffBlock, TraceMode: r.TraceMode, BlockBatch: r.BlockBatch,
		LogBatch: r.LogBatch, Workers: r.Workers, RPS: r.RPS, TimeoutSeconds: r.TimeoutSeconds,
		Retries: r.Retries, PageSize: r.PageSize, RawDir: r.RawDir, OutputDir: r.OutputDir,
		OutputPrefix: r.OutputPrefix, AMLLabels: r.AMLLabels, AMLRPS: r.AMLRPS,
		FilterExchange: r.FilterExchange, Details: r.Details, ScanNative: r.ScanNative,
		Incremental: r.Incremental, RiskCooldownSecs: r.RiskCooldownSecs,
	}
	if strings.EqualFold(req.Source, "csv") {
		req.CSVIMAPPassword = settings.CSVIMAPPassword
	}
	return req
}

func (m *GUIManager) launchGUIJob(req GUIStartRequest, entries []GUIAddressChain, addresses []GUIAddressProgress, startIndex int, historyID string) (*GUIJob, error) {
	if len(entries) == 0 {
		return nil, errors.New("请至少输入一个地址")
	}
	if startIndex < 0 || startIndex >= len(entries) {
		return nil, fmt.Errorf("invalid GUI job start index %d", startIndex)
	}
	id := newJobID()
	ctx, cancel := context.WithCancel(context.Background())
	if len(addresses) == 0 {
		addresses = newGUIAddressProgress(entries)
	} else {
		addresses = cloneGUIAddressProgress(addresses)
	}
	if strings.TrimSpace(historyID) == "" {
		historyID = id
	}
	job := &GUIJob{
		ID: id, Status: "running", Message: "等待开始", Total: len(entries), Running: true,
		StartedAt: time.Now().Format("2006-01-02 15:04:05"), Addresses: addresses,
		request: req, Incremental: req.Incremental, entries: append([]GUIAddressChain(nil), entries...),
		cancel: cancel, addressCancels: map[int]context.CancelFunc{}, cancelledAddresses: map[int]bool{},
		store: m.store, history: m.history, historyID: historyID,
	}
	m.mu.Lock()
	if m.scheduler == nil {
		m.scheduler = NewGUIBatchScheduler(1)
	}
	job.scheduler = m.scheduler
	m.jobs[id] = job
	m.mu.Unlock()
	job.persist()
	go runGUIJob(ctx, job, req, entries, startIndex)
	return job, nil
}

func (m *GUIManager) launchHistoryJob(record GUIJobRecord, resume bool) (*GUIJob, error) {
	if resume && record.Status != "paused" && record.Status != "cooling" {
		return nil, errors.New("只有已暂停或冷却中的历史任务可以继续下载")
	}
	settings, err := loadGUISettingsFromConfigDir(m.configDir)
	if err != nil {
		return nil, fmt.Errorf("load current GUI settings: %w", err)
	}
	entries := append([]GUIAddressChain(nil), record.Entries...)
	addresses := []GUIAddressProgress(nil)
	startIndex := 0
	historyID := ""
	if resume {
		addresses = cloneGUIAddressProgress(record.Addresses)
		startIndex = firstResumableGUIAddressLocked(addresses)
		if startIndex < 0 {
			return nil, errors.New("历史任务没有可继续下载的地址")
		}
		historyID = record.ID
	}
	return m.launchGUIJob(guiStartRequestFromPersisted(record, settings), entries, addresses, startIndex, historyID)
}
