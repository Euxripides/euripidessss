package smartdownload

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/etl/backend/internal/cloudruntime"
	"github.com/google/uuid"
)

// CloudRuntime SQD Cloud 运行时接口（由 internal/cloudruntime.Manager 实现；测试可 mock）。
type CloudRuntime interface {
	SubmitJob(ctx context.Context, job cloudruntime.Job) (string, error)
	JobStatus(id string) (cloudruntime.Job, error)
	CancelJob(ctx context.Context, id string) error
	Status() cloudruntime.Status
}

// SQDCloudAdapter 复用 cloudruntime Job 队列，V1 支持 BSC token_transfers。
// 本地已物化的 Cloud Parquet 会回读到统一 Part/Validation 流程；远端产物未
// 物化时保持 fail-closed，由 Turbo ownership 转交 RPC 补洞。
type SQDCloudAdapter struct {
	runtime      CloudRuntime
	pollInterval time.Duration
	resultReader func(context.Context, string) ([]Record, error)
}

// SetResultReader wires the canonical Parquet reader used by local/mock Cloud
// jobs. Remote object-store jobs remain fail-closed until their artifacts are
// materialized locally by the runtime.
func (p *SQDCloudAdapter) SetResultReader(reader func(context.Context, string) ([]Record, error)) {
	p.resultReader = reader
}

// NewSQDCloudAdapter 创建 Cloud Adapter。
func NewSQDCloudAdapter(runtime CloudRuntime) *SQDCloudAdapter {
	return &SQDCloudAdapter{runtime: runtime, pollInterval: 2 * time.Second}
}

func (p *SQDCloudAdapter) Name() string { return "sqd_cloud" }
func (p *SQDCloudAdapter) Available() bool {
	return p.runtime != nil && cloudRuntimeCanSubmit(p.runtime.Status())
}
func (p *SQDCloudAdapter) Supports(d string) bool { return d == DatasetTokenTransfers } // V1
func (p *SQDCloudAdapter) MaxAddressGroupSize(dataset string) int {
	if dataset != DatasetTokenTransfers {
		return 0
	}
	return 25
}
func (p *SQDCloudAdapter) SupportedDatasetBundles() [][]string {
	return [][]string{{DatasetTokenTransfers}}
}
func (p *SQDCloudAdapter) GroupRangeOwner() RangeOwner { return RangeOwnerCloud }

func (p *SQDCloudAdapter) Probe(_ context.Context, _ ProbeRequest) (ProbeResult, error) {
	return ProbeResult{Confidence: 0}, nil
}

// ExecuteRange 提交单个 Range 的 Cloud Job 并轮询到终态。
func (p *SQDCloudAdapter) ExecuteRange(ctx context.Context, req RangeRequest) (*ProviderResult, error) {
	remote, err := p.ExecuteRemoteRange(ctx, req)
	if err != nil {
		return nil, err
	}
	return remote.sync(ctx)
}

// ExecuteRemoteRange stops at REMOTE_COMPLETED. Local materialization and
// Parquet parsing are represented by the returned continuation and run in the
// Service LocalSync worker pool.
func (p *SQDCloudAdapter) ExecuteRemoteRange(ctx context.Context, req RangeRequest) (*cloudRemoteResult, error) {
	return p.executeRemoteJob(ctx, cloudruntime.Job{
		ID:        "sd-" + uuid.NewString(),
		ChunkID:   "chunk-1",
		PlanID:    req.DatasetJobID,
		TaskID:    req.DatasetJobID,
		ChainKey:  strings.ToLower(strings.TrimSpace(req.ChainKey)),
		ChainID:   int(req.ChainID),
		Dataset:   req.Dataset,
		Addresses: []string{strings.ToLower(strings.TrimSpace(req.Address))},
		FromBlock: req.FromBlock,
		ToBlock:   req.ToBlock,
		Priority:  maxInt(req.Priority, 90),
		Attempt:   1,
		Tier:      req.CloudTier,
	}, req)
}

func (p *SQDCloudAdapter) ExecuteGroupRange(ctx context.Context, req GroupRangeRequest) (map[string]map[string]*ProviderResult, error) {
	remote, err := p.ExecuteRemoteGroupRange(ctx, req)
	if err != nil {
		return nil, err
	}
	result, err := remote.sync(ctx)
	if err != nil {
		return nil, err
	}
	return splitCloudGroupResult(req, result), nil
}

func (p *SQDCloudAdapter) ExecuteRemoteGroupRange(ctx context.Context, req GroupRangeRequest) (*cloudRemoteResult, error) {
	if p.runtime == nil || !cloudRuntimeCanSubmit(p.runtime.Status()) {
		return nil, fmt.Errorf("SQD Cloud 运行时不可用")
	}
	if req.ChainKey == "" || req.FromBlock > req.ToBlock {
		return nil, fmt.Errorf("SQD Cloud Group Range 请求非法")
	}
	addresses := normalizeAndUniqueLower(req.Addresses)
	if len(addresses) == 0 {
		return nil, fmt.Errorf("SQD Cloud Group Range 缺少地址")
	}
	datasets := normalizeAndUniqueLower(req.Datasets)
	if len(datasets) != 1 || datasets[0] != DatasetTokenTransfers {
		return nil, fmt.Errorf("SQD Cloud Group Range 仅支持 token_transfers 单数据集")
	}
	if len(addresses) > p.MaxAddressGroupSize(datasets[0]) {
		return nil, fmt.Errorf("SQD Cloud Group Range 地址数 %d 超过上限 %d", len(addresses), p.MaxAddressGroupSize(datasets[0]))
	}
	job := cloudruntime.Job{
		ID:        "sd-" + uuid.NewString(),
		ChunkID:   "chunk-1",
		PlanID:    req.SharedWorkID,
		TaskID:    req.SharedWorkID,
		ChainKey:  strings.ToLower(strings.TrimSpace(req.ChainKey)),
		ChainID:   int(req.ChainID),
		Dataset:   datasets[0],
		Addresses: addresses,
		FromBlock: req.FromBlock,
		ToBlock:   req.ToBlock,
		Priority:  maxInt(req.Priority, 90),
		Attempt:   1,
		Tier:      req.CloudTier,
	}
	remote, err := p.executeRemoteJob(ctx, job, RangeRequest{
		DatasetJobID: req.SharedWorkID,
		Mode:         req.Mode,
		ChainKey:     req.ChainKey,
		ChainID:      req.ChainID,
		Dataset:      DatasetTokenTransfers,
		FromBlock:    req.FromBlock,
		ToBlock:      req.ToBlock,
	})
	if err != nil {
		return nil, err
	}
	return remote, nil
}

func splitCloudGroupResult(req GroupRangeRequest, result *ProviderResult) map[string]map[string]*ProviderResult {
	addresses := normalizeAndUniqueLower(req.Addresses)
	out := make(map[string]map[string]*ProviderResult, len(addresses))
	for _, address := range addresses {
		out[address] = map[string]*ProviderResult{
			DatasetTokenTransfers: &ProviderResult{CompletedTo: req.ToBlock},
		}
	}
	for _, rec := range result.Records {
		for _, address := range addresses {
			if cloudRecordTouchesAddress(rec, address) {
				out[address][DatasetTokenTransfers].Records = append(out[address][DatasetTokenTransfers].Records, rec)
			}
		}
	}
	return out
}

func (p *SQDCloudAdapter) executeRemoteJob(ctx context.Context, job cloudruntime.Job, req RangeRequest) (*cloudRemoteResult, error) {
	if p.runtime == nil || !cloudRuntimeCanSubmit(p.runtime.Status()) {
		return nil, fmt.Errorf("SQD Cloud 运行时不可用")
	}
	if req.Dataset != "" && req.Dataset != DatasetTokenTransfers {
		return nil, fmt.Errorf("V1 SQD Cloud 仅支持 token_transfers")
	}
	jobID, err := p.runtime.SubmitJob(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("Cloud Job 提交失败: %w", err)
	}
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = p.runtime.CancelJob(context.Background(), jobID)
			return nil, ctx.Err()
		case <-ticker.C:
			st, err := p.runtime.JobStatus(jobID)
			if err != nil {
				continue
			}
			switch st.State {
			case "done":
				completed := st
				return &cloudRemoteResult{jobID: jobID, rows: uint64(maxInt64(completed.Rows, 0)), sync: func(syncCtx context.Context) (*ProviderResult, error) {
					return p.materializeRemoteResult(syncCtx, jobID, completed, req)
				}}, nil
			case "failed":
				return nil, fmt.Errorf("Cloud Job %s 失败: %s", jobID, sanitizeText(st.Error))
			case "cancelled":
				return nil, fmt.Errorf("Cloud Job %s 已取消", jobID)
			}
		}
	}
}

func (p *SQDCloudAdapter) materializeRemoteResult(ctx context.Context, jobID string, st cloudruntime.Job, req RangeRequest) (*ProviderResult, error) {
	var err error
	if strings.TrimSpace(st.OutputDir) == "" {
		if materializer, ok := p.runtime.(interface {
			MaterializeJobResult(context.Context, string) (string, error)
		}); ok {
			st.OutputDir, err = materializer.MaterializeJobResult(ctx, jobID)
			if err != nil {
				return nil, fmt.Errorf("Cloud Job %s 远端产物物化失败: %w", jobID, err)
			}
		}
	}
	if strings.TrimSpace(st.OutputDir) == "" || p.resultReader == nil {
		if st.Rows == 0 {
			return &ProviderResult{CompletedTo: req.ToBlock}, nil
		}
		return nil, fmt.Errorf("Cloud Job %s 已完成（rows=%d），但远端产物尚未物化到本地", jobID, st.Rows)
	}
	if observer, ok := p.runtime.(interface {
		BeginValidation(string)
		FinishValidation(string)
	}); ok {
		observer.BeginValidation(jobID)
		defer observer.FinishValidation(jobID)
	}
	var records []Record
	var materializedBytes uint64
	err = filepath.WalkDir(st.OutputDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".parquet") {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr == nil && info.Size() > 0 {
			materializedBytes += uint64(info.Size())
		}
		recs, readErr := p.resultReader(ctx, path)
		if readErr != nil {
			return readErr
		}
		for i := range recs {
			recs[i].Dataset = req.Dataset
		}
		records = append(records, recs...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Cloud Job %s 产物读取失败: %w", jobID, err)
	}
	if len(records) == 0 && st.Rows > 0 {
		return nil, fmt.Errorf("Cloud Job %s 声明 rows=%d 但未读到 Parquet 记录", jobID, st.Rows)
	}
	if int64(len(records)) != st.Rows {
		return nil, fmt.Errorf("Cloud Job %s 行数对账失败: manifest=%d parquet=%d", jobID, st.Rows, len(records))
	}
	return &ProviderResult{Records: records, Bytes: materializedBytes, CompletedTo: req.ToBlock}, nil
}

func maxInt64(value, floor int64) int64 {
	if value < floor {
		return floor
	}
	return value
}

// cloudRuntimeCanSubmit distinguishes an executable worker from a configured
// cloud runtime that can deploy on the first SubmitJob. Provider health still
// reports ABSENT as unavailable; this predicate is only for the submit path.
func cloudRuntimeCanSubmit(status cloudruntime.Status) bool {
	if status.Available {
		return true
	}
	return status.Mode == cloudruntime.ModeCloud &&
		status.State == cloudruntime.WorkerAbsent &&
		status.DeploymentKeyConfigured && status.R2Configured
}

func normalizeAndUniqueLower(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloudRecordTouchesAddress(record Record, requested string) bool {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		return false
	}
	if strings.EqualFold(record.Address, requested) {
		return true
	}
	return strings.EqualFold(firstNonEmpty(record.Payload, "from_address"), requested) ||
		strings.EqualFold(firstNonEmpty(record.Payload, "to_address"), requested)
}

// MockCloudProvider 确定性 SQD Cloud Provider（仅测试/验收 Case C）。
type MockCloudProvider struct {
	MockProvider
}

// NewMockCloudProvider 创建名为 sqd_cloud 的确定性 Provider。
func NewMockCloudProvider() *MockCloudProvider {
	return &MockCloudProvider{MockProvider: MockProvider{name: "sqd_cloud"}}
}

func (p *MockCloudProvider) Supports(d string) bool { return d == DatasetTokenTransfers }
