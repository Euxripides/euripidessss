package smartdownload

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
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

func (p *SQDCloudAdapter) Probe(_ context.Context, _ ProbeRequest) (ProbeResult, error) {
	return ProbeResult{Confidence: 0}, nil
}

// ExecuteRange 提交单个 Range 的 Cloud Job 并轮询到终态。
func (p *SQDCloudAdapter) ExecuteRange(ctx context.Context, req RangeRequest) (*ProviderResult, error) {
	if p.runtime == nil || !cloudRuntimeCanSubmit(p.runtime.Status()) {
		return nil, fmt.Errorf("SQD Cloud 运行时不可用")
	}
	if req.Dataset != DatasetTokenTransfers {
		return nil, fmt.Errorf("V1 SQD Cloud 仅支持 token_transfers")
	}
	priority := req.Priority
	if priority <= 0 {
		priority = 90
	}
	job := cloudruntime.Job{
		ID:       "sd-" + uuid.NewString(),
		ChunkID:  "chunk-1",
		PlanID:   req.DatasetJobID,
		TaskID:   req.DatasetJobID,
		ChainKey: strings.ToLower(strings.TrimSpace(req.ChainKey)),
		ChainID:  int(req.ChainID),
		Dataset:  req.Dataset,
		// Smart Download 的 Address 是调查钱包，不是 Token 合约。Cloud Worker
		// 通过 Transfer topic1/topic2 过滤钱包参与事件；TokenContract 留空仅作为
		// 旧 Worker 的兼容默认值，不能替代钱包 watch filter。
		Addresses: []string{strings.ToLower(strings.TrimSpace(req.Address))},
		FromBlock: req.FromBlock,
		ToBlock:   req.ToBlock,
		Priority:  priority,
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
						return &ProviderResult{}, nil
					}
					return nil, fmt.Errorf("Cloud Job %s 已完成（rows=%d），但远端产物尚未物化到本地", jobID, st.Rows)
				}
				var records []Record
				err := filepath.WalkDir(st.OutputDir, func(path string, entry fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".parquet") {
						return nil
					}
					recs, readErr := p.resultReader(ctx, path)
					if readErr != nil {
						return readErr
					}
					for i := range recs {
						// The Cloud request is the authoritative dataset boundary.
						// Provider Parquet may omit canonical discriminator columns
						// (for example token_standard), so schema inference alone can
						// otherwise misclassify transfers as transactions.
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
				return &ProviderResult{Records: records, CompletedTo: req.ToBlock}, nil
			case "failed":
				return nil, fmt.Errorf("Cloud Job %s 失败: %s", jobID, sanitizeText(st.Error))
			case "cancelled":
				return nil, fmt.Errorf("Cloud Job %s 已取消", jobID)
			}
		}
	}
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

// MockCloudProvider 确定性 SQD Cloud Provider（仅测试/验收 Case C）。
type MockCloudProvider struct {
	MockProvider
}

// NewMockCloudProvider 创建名为 sqd_cloud 的确定性 Provider。
func NewMockCloudProvider() *MockCloudProvider {
	return &MockCloudProvider{MockProvider: MockProvider{name: "sqd_cloud"}}
}

func (p *MockCloudProvider) Supports(d string) bool { return d == DatasetTokenTransfers }
