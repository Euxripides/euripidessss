package smartdownload

import (
	"context"
	"fmt"
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

// SQDCloudAdapter SQD Cloud 最终兜底 Adapter（Phase 2）：
// 复用现有 cloudruntime Job 队列，V1 仅 BSC token_transfers；Tier 100 语义由调度器保证（最后选择）。
// 边界：Cloud Worker 产出 Parquet 的结果回读/入库属 Phase 3 Result Processor，
// 因此 Phase 2 中 ExecuteRange 提交并轮询成功后会返回“结果回读未接入”错误（诚实失败）。
type SQDCloudAdapter struct {
	runtime      CloudRuntime
	pollInterval time.Duration
}

// NewSQDCloudAdapter 创建 Cloud Adapter。
func NewSQDCloudAdapter(runtime CloudRuntime) *SQDCloudAdapter {
	return &SQDCloudAdapter{runtime: runtime, pollInterval: 2 * time.Second}
}

func (p *SQDCloudAdapter) Name() string { return "sqd_cloud" }
func (p *SQDCloudAdapter) Available() bool {
	return p.runtime != nil && p.runtime.Status().Available
}
func (p *SQDCloudAdapter) Supports(d string) bool { return d == DatasetTokenTransfers } // V1

func (p *SQDCloudAdapter) Probe(_ context.Context, _ ProbeRequest) (ProbeResult, error) {
	return ProbeResult{Confidence: 0}, nil
}

// ExecuteRange 提交单个 Range 的 Cloud Job 并轮询到终态。
func (p *SQDCloudAdapter) ExecuteRange(ctx context.Context, req RangeRequest) (*ProviderResult, error) {
	if p.runtime == nil || !p.runtime.Status().Available {
		return nil, fmt.Errorf("SQD Cloud 运行时不可用")
	}
	if req.Dataset != DatasetTokenTransfers {
		return nil, fmt.Errorf("V1 SQD Cloud 仅支持 token_transfers")
	}
	job := cloudruntime.Job{
		ID:        "sd-" + uuid.NewString(),
		ChunkID:   "chunk-1",
		PlanID:    req.DatasetJobID,
		TaskID:    req.DatasetJobID,
		ChainKey:  strings.ToLower(strings.TrimSpace(req.ChainKey)),
		Addresses: []string{req.Address},
		FromBlock: req.FromBlock,
		ToBlock:   req.ToBlock,
		Priority:  90,
		Attempt:   1,
		Tier:      req.CloudTier,
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
				return nil, fmt.Errorf("Cloud Job %s 已完成（rows=%d），但结果回读/入库需 Phase 3 Result Processor", jobID, st.Rows)
			case "failed":
				return nil, fmt.Errorf("Cloud Job %s 失败: %s", jobID, sanitizeText(st.Error))
			case "cancelled":
				return nil, fmt.Errorf("Cloud Job %s 已取消", jobID)
			}
		}
	}
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
