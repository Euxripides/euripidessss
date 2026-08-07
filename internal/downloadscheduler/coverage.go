package downloadscheduler

import (
	"context"
	"fmt"
	"strings"
)

// CoverageSource 本地数据覆盖查询源（由 API 层注入 analyticsapi.Service）。
// AddressTxCount 返回该地址在本地数据集内的交易/事件笔数；0 表示无数据。
type CoverageSource interface {
	AddressTxCount(ctx context.Context, address string) (int64, error)
}

// CoverageResolver 覆盖检查器（设计文档 §10）：判断已有数据/缺什么，避免重复下载。
type CoverageResolver struct {
	source CoverageSource // 可为 nil（本地无数据集时所有检查返回"无数据"）
}

// NewCoverageResolver 创建覆盖检查器。
func NewCoverageResolver(source CoverageSource) *CoverageResolver {
	return &CoverageResolver{source: source}
}

// TxCount 查询单个地址在本地数据集内的交易/事件笔数（活跃度评分用）。
// 数据源不可用时返回 0（按普通活跃度处理）。
func (r *CoverageResolver) TxCount(ctx context.Context, address string) int64 {
	if r == nil || r.source == nil {
		return 0
	}
	n, err := r.source.AddressTxCount(ctx, strings.ToLower(strings.TrimSpace(address)))
	if err != nil {
		return 0
	}
	return n
}

// Check 检查一组地址在指定数据集上的本地覆盖情况。
// datasets 为空时检查全部 4 类。地址必须为合法 EVM 地址（防 SQL 注入）。
func (r *CoverageResolver) Check(ctx context.Context, chainKey string, addresses []string, datasets []Dataset) (*CoverageResult, error) {
	if len(datasets) == 0 {
		datasets = []Dataset{DatasetTransactions, DatasetTokenTransfer, DatasetBalance, DatasetLabels}
	}
	clean := make([]string, 0, len(addresses))
	for _, a := range addresses {
		a = strings.ToLower(strings.TrimSpace(a))
		if !evmAddressRE.MatchString(a) {
			return nil, fmt.Errorf("非法 EVM 地址")
		}
		clean = append(clean, a)
	}
	result := &CoverageResult{
		ChainKey:  strings.ToLower(strings.TrimSpace(chainKey)),
		Addresses: clean,
	}
	if r.source == nil {
		for _, d := range datasets {
			result.Items = append(result.Items, Coverage{
				Dataset: d,
				Have:    false,
				Note:    "本地数据集不可用，无法判断覆盖",
			})
		}
		return result, nil
	}
	for _, d := range datasets {
		item := Coverage{Dataset: d}
		if d == DatasetBalance || d == DatasetLabels {
			// 余额/标签是链上/外部实时信息，本地覆盖检查不适用
			item.Note = "实时/外部数据，不检查本地覆盖"
			result.Items = append(result.Items, item)
			continue
		}
		for _, addr := range addresses {
			n, err := r.source.AddressTxCount(ctx, strings.ToLower(strings.TrimSpace(addr)))
			if err != nil {
				item.Note = fmt.Sprintf("覆盖查询失败: %v", err)
				break
			}
			if n > 0 {
				item.Have = true
				item.TxCount += n
			}
		}
		if item.Have {
			item.Note = fmt.Sprintf("数据集内已有 %d 笔关联交易", item.TxCount)
		} else {
			item.Note = "数据集内暂无该地址数据，需要下载"
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}
