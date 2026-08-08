package smartdownload

import (
	"context"
	"fmt"
	"strings"

	"github.com/etl/backend/internal/downloadscheduler"
)

// LegacyPlanBridge 把现有 downloadscheduler.Plan 桥接为新的四层任务模型
// （实施方案 Phase 1：Legacy Plan Bridge）。旧计划仍按原逻辑执行，桥接只负责
// 把需求复制到新任务层，供统一 API 展示与后续接管。
type LegacyPlanBridge struct {
	svc *Service
}

// NewLegacyPlanBridge 创建桥接器。
func NewLegacyPlanBridge(svc *Service) *LegacyPlanBridge {
	return &LegacyPlanBridge{svc: svc}
}

// BridgePlan 将旧 Plan 转为新 BatchJob。
func (b *LegacyPlanBridge) BridgePlan(ctx context.Context, plan *downloadscheduler.Plan) (*CreateBatchResponse, error) {
	if plan == nil {
		return nil, fmt.Errorf("plan 为空")
	}
	seenAddr := map[string]bool{}
	seenDS := map[string]bool{}
	var addresses []string
	var datasets []string
	var defaultRange *RangeSpec
	first := true
	for _, t := range plan.Tasks {
		if t == nil {
			continue
		}
		ds, ok := legacyDatasetMap(t.Requirement.Dataset)
		if !ok {
			continue
		}
		if !seenDS[ds] {
			seenDS[ds] = true
			datasets = append(datasets, ds)
		}
		for _, a := range t.Requirement.Addresses {
			a = strings.ToLower(strings.TrimSpace(a))
			if !seenAddr[a] {
				seenAddr[a] = true
				addresses = append(addresses, a)
			}
		}
		if first && (t.Requirement.FromBlock > 0 || t.Requirement.ToBlock > 0) {
			defaultRange = &RangeSpec{
				Mode:      RangeModeBlock,
				FromBlock: t.Requirement.FromBlock,
				ToBlock:   t.Requirement.ToBlock,
			}
			first = false
		}
	}
	if len(addresses) == 0 || len(datasets) == 0 {
		return nil, fmt.Errorf("plan 内没有可桥接的地址/数据集（labels 等手动数据集跳过）")
	}
	chainKey := "bsc"
	if len(plan.Tasks) > 0 && plan.Tasks[0] != nil {
		chainKey = plan.Tasks[0].Requirement.ChainKey
	}
	return b.svc.CreateBatch(ctx, CreateBatchRequest{
		ChainKey:     chainKey,
		Addresses:    addresses,
		Datasets:     datasets,
		DefaultRange: defaultRange,
	})
}

// legacyDatasetMap 旧调度器 Dataset → 新任务模型 Dataset。
func legacyDatasetMap(ds downloadscheduler.Dataset) (string, bool) {
	switch ds {
	case downloadscheduler.DatasetTransactions:
		return DatasetTransactions, true
	case downloadscheduler.DatasetTokenTransfer:
		return DatasetTokenTransfers, true
	case downloadscheduler.DatasetBalance:
		return DatasetBalances, true
	default:
		return "", false
	}
}
