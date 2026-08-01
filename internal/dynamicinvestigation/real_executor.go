package dynamicinvestigation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/parquetdownload"
)

// ── 真实采集执行器 ──
//
// 普通钱包 → SQD 增量（logs/traces 走 parquetdownload 落盘通道，复用下载引擎可靠性）
// 大型实体 → CSV 直链（parquetdownload.Manager.Start transactions）
// 低价值地址 → 仅保存关系（无操作）

// RealExecutor 包装现有下载引擎执行采集任务。
type RealExecutor struct {
	manager *parquetdownload.Manager
	network chain.EVM
}

// NewRealExecutor 创建真实执行器。
// manager 可为 nil（此时任何真实采集都不可用）。
func NewRealExecutor(manager *parquetdownload.Manager, network chain.EVM) *RealExecutor {
	return &RealExecutor{manager: manager, network: network}
}

// Execute 按任务采集方式执行。
func (e *RealExecutor) Execute(ctx context.Context, task *AcquisitionTask) error {
	switch task.Mode {
	case AcquisitionRelationsOnly:
		task.SetStatus("done")
		return nil
	case AcquisitionCSVDirect:
		return e.executeViaManager(ctx, task, "transactions")
	case AcquisitionSQDLogs:
		return e.executeViaManager(ctx, task, "logs")
	case AcquisitionSQDTransactions:
		return e.executeViaManager(ctx, task, "transactions")
	case AcquisitionSQDTrace:
		return e.executeViaManager(ctx, task, "traces")
	default:
		return fmt.Errorf("未知采集方式: %s", task.Mode)
	}
}

// executeViaManager 调用 parquetdownload.Manager.Start 执行采集。
// dataset 为 transactions（CSV 直链/大实体）或 logs/traces（SQD 增量）。
func (e *RealExecutor) executeViaManager(ctx context.Context, task *AcquisitionTask, dataset string) error {
	if e.manager == nil {
		return fmt.Errorf("%s 采集不可用：Parquet 下载管理器未初始化", strings.ToUpper(dataset))
	}
	if len(task.Addresses) == 0 {
		task.Addresses = []string{task.Address}
	}
	startDate := task.StartDate
	if startDate == "" {
		startDate = "2020-01-01"
	}
	endDate := task.EndDate
	if endDate == "" {
		endDate = time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	}
	job, err := e.manager.Start(ctx, parquetdownload.StartRequest{
		ChainKey:        task.ChainID,
		Addresses:       strings.Join(task.Addresses, ","),
		StartDate:       startDate,
		EndDate:         endDate,
		UseFirstSeen:    true,
		ExportCSV:       boolPtr(true),
		SelectedSource:  []string{dataset},
		IncludeReceipts: false,
	})
	if err != nil {
		return fmt.Errorf("%s 下载启动失败: %w", strings.ToUpper(dataset), err)
	}
	task.SetJobID(job.ID)
	task.SetStatus("running") // Manager.Start 异步执行，任务状态由下载引擎管理
	return nil
}

func boolPtr(b bool) *bool { return &b }
