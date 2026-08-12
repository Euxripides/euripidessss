package downloadscheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/cloudruntime"
	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/parquetdownload"
)

// CloudRuntime SQD Cloud 运行时接口（由 internal/cloudruntime.Manager 实现；测试可 mock）。
type CloudRuntime interface {
	SubmitJob(ctx context.Context, job cloudruntime.Job) (string, error)
	JobStatus(id string) (cloudruntime.Job, error)
	CancelJob(ctx context.Context, id string) error // Phase 5.2 §6 Cancel Marker
	Status() cloudruntime.Status
}

// CloudProvider SQD Cloud 应急 Provider（设计 §2/§16/§17）：
// Tier=100，禁止参与常规竞速，只能由 Cloud Admission Gate 批准后进入候选。
type CloudProvider struct {
	runtime       CloudRuntime
	resolveBlocks func(ctx context.Context, req Requirement) (uint64, uint64, error) // 测试可注入
	mu            sync.Mutex
	chunkJobs     map[string][]string // taskID → chunk job ids（Phase 4：1 任务可拆多 Chunk）
}

// NewCloudProvider 创建 Cloud Provider。
func NewCloudProvider(runtime CloudRuntime) *CloudProvider {
	return &CloudProvider{runtime: runtime, chunkJobs: map[string][]string{}}
}

// WithBlockResolver 注入区块范围解析器（测试用；生产默认走 Portal finalized-head）。
func (p *CloudProvider) WithBlockResolver(fn func(ctx context.Context, req Requirement) (uint64, uint64, error)) *CloudProvider {
	p.resolveBlocks = fn
	return p
}

func (p *CloudProvider) Kind() ProviderKind       { return ProviderSQDCloud }
func (p *CloudProvider) Name() string             { return "SQD Cloud Provider" }
func (p *CloudProvider) Tier() ProviderTier       { return TierEmergencyCloud }
func (p *CloudProvider) ManualOnly() bool         { return false }
func (p *CloudProvider) CanHandle(d Dataset) bool { return d == DatasetTokenTransfer } // V1 仅 BSC Token Transfer

func (p *CloudProvider) Available() bool {
	if p.runtime == nil {
		return false
	}
	st := p.runtime.Status()
	if !st.Available {
		return false
	}
	switch st.State {
	case cloudruntime.WorkerReady, cloudruntime.WorkerBusy, cloudruntime.WorkerIdle:
		return true
	default:
		return false
	}
}

func (p *CloudProvider) State() ProviderState {
	if p.runtime == nil {
		return ProviderNotConfigured
	}
	switch p.runtime.Status().State {
	case cloudruntime.WorkerNotConfigured:
		return ProviderNotConfigured
	case cloudruntime.WorkerAbsent, cloudruntime.WorkerFailed, cloudruntime.WorkerRemoving:
		return ProviderUnavailable
	case cloudruntime.WorkerDeploying, cloudruntime.WorkerStarting, cloudruntime.WorkerDegraded:
		return ProviderDegraded
	case cloudruntime.WorkerReady, cloudruntime.WorkerBusy, cloudruntime.WorkerIdle:
		return ProviderHealthy
	default:
		return ProviderUnavailable
	}
}

func (p *CloudProvider) StateReasons() []string {
	if p.runtime == nil {
		return []string{"Cloud 运行时未装配"}
	}
	st := p.runtime.Status()
	if st.Reason != "" {
		return []string{st.Reason}
	}
	return []string{"状态：" + string(st.State)}
}

func (p *CloudProvider) Score(d Dataset) ProviderScore {
	s := ProviderScore{
		Provider:    ProviderSQDCloud,
		Name:        "SQD Cloud Provider",
		Tier:        TierEmergencyCloud,
		Coverage:    95, // Portal 全量历史
		Accuracy:    90,
		Speed:       80,
		Cost:        15, // 付费资源
		Reliability: 80,
		Available:   p.Available(),
		Reasons: []string{
			"最后兜底 Provider（Tier 100）：仅常规数据源全部耗尽后启用",
			"V1 仅支持 BSC USDT Token Transfer；付费成本受 Cloud Budget Guard 限制",
		},
		State: p.State(),
	}
	s.Total = weightedTotal(s)
	return s
}

// Execute 提交 Cloud 应急任务（设计 §33-34：request.json + Parquet 输出 + _SUCCESS）。
func (p *CloudProvider) Execute(ctx context.Context, req Requirement) (*TaskResult, error) {
	if p.runtime == nil {
		return nil, errors.New("SQD Cloud 运行时未装配")
	}
	if req.Dataset != DatasetTokenTransfer {
		return nil, errors.New("V1 Cloud 仅支持 token_transfer")
	}
	// ABSENT/DEPLOYING/STARTING 是可部署或可等待状态，但不是可执行健康状态。
	// SubmitJob 会在入队前执行 EnsureWorker + sqd list 对账；其他不可用状态立即返原因。
	st := p.runtime.Status()
	deployableState := (st.State == cloudruntime.WorkerAbsent || st.State == cloudruntime.WorkerDeploying || st.State == cloudruntime.WorkerStarting) &&
		st.Mode == cloudruntime.ModeCloud && st.DeploymentKeyConfigured && st.R2Configured && st.FailureCooldownUntil == nil
	if !deployableState && !p.Available() {
		reason := strings.TrimSpace(st.Reason)
		if reason == "" {
			reason = "状态=" + string(st.State)
		}
		return nil, fmt.Errorf("SQD Cloud 运行时不可执行: %s", reason)
	}
	resolve := p.resolveBlocks
	if resolve == nil {
		resolve = p.resolveBlockRange
	}
	from, to, err := resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	// Phase 4 §20：Chunk 化（地址组 25 / 区块窗口 50,000），Worker 按 Chunk 领取。
	chunks := splitCloudChunks(req, from, to, cloudAddressesPerChunk, cloudBlocksPerChunk)
	var jobIDs []string
	for i, chunk := range chunks {
		jobID, err := p.runtime.SubmitJob(ctx, chunk)
		if err != nil {
			// 多 Chunk 提交不是事务；任一后续 Chunk 失败时必须撤销此前已提交的
			// Job，避免调度器返回失败后仍有付费孤儿任务在远端运行。
			var cancelErrors []string
			for j := len(jobIDs) - 1; j >= 0; j-- {
				if cancelErr := p.runtime.CancelJob(context.Background(), jobIDs[j]); cancelErr != nil {
					cancelErrors = append(cancelErrors, jobIDs[j]+": "+cancelErr.Error())
				}
			}
			if len(cancelErrors) > 0 {
				return nil, fmt.Errorf("应急 Cloud Chunk %d/%d 提交失败: %w；已提交 Chunk 撤销不完整: %s",
					i+1, len(chunks), err, strings.Join(cancelErrors, "; "))
			}
			return nil, fmt.Errorf("应急 Cloud Chunk %d/%d 提交失败: %w；此前 %d 个 Chunk 已请求取消", i+1, len(chunks), err, len(jobIDs))
		}
		jobIDs = append(jobIDs, jobID)
	}
	p.mu.Lock()
	p.chunkJobs[req.ID] = append([]string(nil), jobIDs...)
	// Scheduler 使用 TaskResult.JobID 轮询；首 Job ID 必须映射到完整 Chunk
	// 集合，否则首 Chunk 完成时会把仍在运行的其余范围误判为整体完成。
	p.chunkJobs[jobIDs[0]] = append([]string(nil), jobIDs...)
	p.mu.Unlock()
	logger.Log.Info().Str("task", req.ID).Str("chain", strings.ToLower(strings.TrimSpace(req.ChainKey))).
		Uint64("from_block", from).Uint64("to_block", to).Int("chunks", len(jobIDs)).
		Msg("scheduler_cloud_jobs_submitted")
	return &TaskResult{
		JobID:   jobIDs[0],
		Summary: fmt.Sprintf("应急 Cloud 已提交 %d 个 Chunk（区块 %d-%d，job=%s…）", len(jobIDs), from, to, jobIDs[0]),
		Rows:    0,
		NewData: true,
	}, nil
}

// cloudChunkConfig Phase 4 §20 默认分块（后续按延迟/行数自适应）。
const (
	cloudAddressesPerChunk = 25
	cloudBlocksPerChunk    = 50_000
)

// splitCloudChunks 按地址组 × 区块窗口拆分 Chunk（Phase 4 §20/§33）。
func splitCloudChunks(req Requirement, from, to uint64, addrsPerChunk int, blocksPerChunk uint64) []cloudruntime.Job {
	chainKey := strings.ToLower(strings.TrimSpace(req.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	if addrsPerChunk <= 0 {
		addrsPerChunk = 25
	}
	if blocksPerChunk <= 0 {
		blocksPerChunk = cloudBlocksPerChunk
	}
	var out []cloudruntime.Job
	n := 0
	for i := 0; i < len(req.Addresses); i += addrsPerChunk {
		end := i + addrsPerChunk
		if end > len(req.Addresses) {
			end = len(req.Addresses)
		}
		for blockFrom := from; ; {
			blockTo := to
			// blocksPerChunk 表示区块数量；闭区间终点应为 from+size-1。
			// 用剩余长度比较避免 uint64 上溢。
			if blocksPerChunk > 0 && to-blockFrom >= blocksPerChunk {
				blockTo = blockFrom + blocksPerChunk - 1
			}
			n++
			out = append(out, cloudruntime.Job{
				ID:        fmt.Sprintf("%s-c%d", req.ID, n),
				ChunkID:   fmt.Sprintf("chunk-%d", n),
				PlanID:    req.PlanID,
				TaskID:    req.ID,
				ChainKey:  chainKey,
				Addresses: append([]string(nil), req.Addresses[i:end]...),
				FromBlock: blockFrom,
				ToBlock:   blockTo,
				Priority:  90,
				Attempt:   1,
			})
			if blockTo == to {
				break
			}
			blockFrom = blockTo + 1
		}
	}
	if len(out) == 0 {
		out = append(out, cloudruntime.Job{
			ID: req.ID + "-c1", ChunkID: "chunk-1", PlanID: req.PlanID, TaskID: req.ID,
			ChainKey: chainKey, Addresses: req.Addresses, FromBlock: from, ToBlock: to,
			Priority: 90, Attempt: 1,
		})
	}
	return out
}

// JobProgress 轮询 Cloud 任务进度（调度器 waitSQDJob 统一轮询）。
func (p *CloudProvider) JobProgress(ctx context.Context, jobID string) (float64, string, error) {
	p.mu.Lock()
	ids := append([]string(nil), p.chunkJobs[jobID]...)
	p.mu.Unlock()
	if len(ids) == 0 {
		ids = []string{jobID}
	}
	done, failed := 0, 0
	var firstErr error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return 0, parquetdownload.StatusFailed, err
		}
		job, err := p.runtime.JobStatus(id)
		if err != nil {
			return float64(done) / float64(len(ids)), parquetdownload.StatusFailed, fmt.Errorf("读取 Cloud Job %s 状态: %w", id, err)
		}
		switch job.State {
		case "queued":
			// 等待
		case "running":
			// 进行中
		case "done":
			done++
		case "failed":
			failed++
			if firstErr == nil {
				firstErr = errors.New(job.Error)
			}
		case "cancelled":
			return 0, parquetdownload.StatusCanceled, nil // Phase 5.2 §6：Cancel Marker 终态
		}
	}
	if failed > 0 {
		return 0, parquetdownload.StatusFailed, firstErr
	}
	if done == len(ids) {
		return 1, parquetdownload.StatusDone, nil
	}
	return float64(done) / float64(len(ids)), parquetdownload.StatusRunning, nil
}

// ── 区块范围解析（V1：日期 → 区块，锚定 Portal finalized head）──

var cloudPortalURL = map[string]string{
	"bsc":      "https://portal.sqd.dev/datasets/binance-mainnet",
	"eth":      "https://portal.sqd.dev/datasets/ethereum-mainnet",
	"base":     "https://portal.sqd.dev/datasets/base-mainnet",
	"arbitrum": "https://portal.sqd.dev/datasets/arbitrum-mainnet",
}

// cloudDefaultLookbackBlocks 未指定日期时 Cloud 任务默认回溯区块数（V1 应急单 Chunk）。
const cloudDefaultLookbackBlocks = 5000

func (p *CloudProvider) resolveBlockRange(ctx context.Context, req Requirement) (uint64, uint64, error) {
	// 显式 Chunk 区块范围（设计 §33：Job 参数驱动）：优先使用，避免依赖 Portal head 解析。
	if req.FromBlock > 0 && req.ToBlock >= req.FromBlock {
		return req.FromBlock, req.ToBlock, nil
	}
	chainKey := strings.ToLower(strings.TrimSpace(req.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	portal, ok := cloudPortalURL[chainKey]
	if !ok {
		return 0, 0, fmt.Errorf("V1 Cloud 暂不支持链 %s", chainKey)
	}
	head, err := cloudFinalizedHead(ctx, portal)
	if err != nil {
		return 0, 0, err
	}
	to := head
	from := uint64(0)
	if head > cloudDefaultLookbackBlocks {
		from = head - cloudDefaultLookbackBlocks
	}
	if req.StartDate != "" && req.EndDate != "" {
		start, err1 := time.Parse("2006-01-02", req.StartDate)
		end, err2 := time.Parse("2006-01-02", req.EndDate)
		if err1 == nil && err2 == nil && !end.Before(start) {
			days := end.Sub(start).Hours()/24 + 1
			blockTime := chainBlockTimeSec[chainKey]
			if blockTime <= 0 {
				blockTime = 12
			}
			span := uint64(math.Min(days*86400/blockTime, float64(head)))
			if span > 0 {
				from = head - span
			}
		}
	}
	return from, to, nil
}

// cloudFinalizedHead 查询 Portal finalized-head（与 Cloud Worker 同源）。
func cloudFinalizedHead(ctx context.Context, portalURL string) (uint64, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, portalURL+"/finalized-head", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Portal finalized-head 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("Portal finalized-head HTTP %d", resp.StatusCode)
	}
	var head struct {
		Number uint64 `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&head); err != nil {
		return 0, fmt.Errorf("解析 Portal finalized-head: %w", err)
	}
	if head.Hash == "" {
		return 0, errors.New("Portal finalized-head 返回无效数据")
	}
	return head.Number, nil
}
