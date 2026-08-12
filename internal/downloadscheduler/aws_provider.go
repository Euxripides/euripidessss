package downloadscheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/parquetdownload"
)

// ── AWS Provider（公共区块链 Parquet 数据集，仅 BSC 原生交易）──
//
// V3 Smart Provider Router（设计 §9）：
//   历史交易优先 AWS > SQD > RPC；Logs/Transfers 优先 SQD > AWS > RPC；实时余额 RPC。
// AWS 数据源（aws-public-blockchain S3，v1.1/bnb/transactions）仅支持 BSC 原生交易，
// 其余场景由 SQD/RPC 承担。

// AWSProvider 公共 Parquet 数据集下载 Provider。
type AWSProvider struct {
	engine SQDEngine // 复用 parquetdownload.Manager（SelectedSource=transactions 走 AWS）
}

func NewAWSProvider(engine SQDEngine) *AWSProvider { return &AWSProvider{engine: engine} }

func (p *AWSProvider) Kind() ProviderKind { return ProviderAWS }
func (p *AWSProvider) Name() string       { return "AWS Provider" }
func (p *AWSProvider) Tier() ProviderTier { return TierNormal }
func (p *AWSProvider) ManualOnly() bool   { return false }

// Available is deliberately false. parquetdownload.Manager now routes the
// transactions selection through SQD streaming, so presenting this wrapper as
// AWS would be a false capability claim until a real AWS discover/execute path
// is wired back in.
func (p *AWSProvider) Available() bool { return false }

// CanHandle 仅 BSC 原生交易（AWS S3 公开数据集的唯一可用范围）。
func (p *AWSProvider) CanHandle(d Dataset) bool { return d == DatasetTransactions }

func (p *AWSProvider) State() ProviderState {
	if !p.Available() {
		return ProviderUnavailable
	}
	return ProviderHealthy
}

func (p *AWSProvider) StateReasons() []string {
	return []string{"AWS legacy 路径已禁用：transactions 当前由 SQD 流式执行"}
}

func (p *AWSProvider) Score(d Dataset) ProviderScore {
	s := ProviderScore{
		Provider:    ProviderAWS,
		Name:        "AWS Provider",
		Tier:        TierNormal,
		State:       p.State(),
		Coverage:    95, // S3 公开全量日分区
		Accuracy:    95, // 原始链上交易，无解析损耗
		Speed:       60, // 大文件流式下载
		Cost:        85, // 公开匿名访问
		Reliability: 95, // S3 高可用，非 SQD Portal 单点
		Available:   p.Available(),
		Reasons:     []string{"BSC 原生交易首选：S3 公开全量 Parquet，高可用免配额", "仅支持 BSC 原生交易；Token 事件/非 BSC 由 SQD 承担"},
	}
	s.Total = weightedTotal(s)
	return s
}

func (p *AWSProvider) Execute(ctx context.Context, req Requirement) (*TaskResult, error) {
	if !p.Available() {
		return nil, errors.New("AWS Provider 已禁用：当前 transactions 由 SQD 流式执行")
	}
	if p.engine == nil {
		return nil, errors.New("AWS Provider 未装配（Parquet 下载管理器不可用）")
	}
	chainKey := strings.ToLower(strings.TrimSpace(req.ChainKey))
	if chainKey != "bsc" {
		return nil, fmt.Errorf("AWS 数据源仅支持 BSC 原生交易（当前 %s）", chainKey)
	}
	if len(req.Addresses) == 0 {
		return nil, errors.New("历史交易需求缺少地址")
	}
	startDate := req.StartDate
	if startDate == "" {
		startDate = "2020-01-01"
	}
	endDate := req.EndDate
	if endDate == "" {
		endDate = time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	}
	job, err := p.engine.Start(ctx, parquetdownload.StartRequest{
		ChainKey:       chainKey,
		Addresses:      strings.Join(req.Addresses, ","),
		StartDate:      startDate,
		EndDate:        endDate,
		UseFirstSeen:   true,
		ExportCSV:      boolPtr(true),
		SelectedSource: []string{"transactions"}, // AWS 路径
	})
	if err != nil {
		return nil, fmt.Errorf("AWS 下载启动失败: %w", err)
	}
	logger.Log.Info().Str("job_id", job.ID).Str("chain", chainKey).
		Int("addresses", job.Addresses.Valid).Msg("scheduler_aws_job_started")
	return &TaskResult{
		JobID:   job.ID,
		Output:  strings.Join(job.Outputs, "; "),
		Summary: fmt.Sprintf("AWS Parquet 任务已启动（job=%s），落盘 BSC 原生交易数据资产", job.ID),
		Rows:    0,
		NewData: true,
	}, nil
}

// JobProgress 查询下游 Parquet 任务进度（调度器统一轮询）。
func (p *AWSProvider) JobProgress(ctx context.Context, jobID string) (float64, string, error) {
	return pollEngineJob(ctx, p.engine, jobID)
}
