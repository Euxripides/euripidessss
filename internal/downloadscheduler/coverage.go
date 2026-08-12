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

// RangeCoverageSource 提供请求级精确覆盖。Cloud 准入不得用“地址历史上
// 曾有数据”替代“本次链/数据集/区间已完整覆盖”。
type RangeCoverageSource interface {
	AddressRangeCovered(ctx context.Context, chainKey, address string, dataset Dataset, fromBlock, toBlock uint64) (covered bool, rows int64, err error)
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

// CheckRequirement 对单个调度需求执行范围精确的覆盖检查。若底层尚未实现
// 范围接口，则保守地视为未完整覆盖；Cloud fail-closed 准入不能因地址在
// 其他区间存在历史记录而拒绝本次缺口任务。
func (r *CoverageResolver) CheckRequirement(ctx context.Context, req Requirement) (*CoverageResult, error) {
	clean := make([]string, 0, len(req.Addresses))
	for _, address := range req.Addresses {
		address = strings.ToLower(strings.TrimSpace(address))
		if !evmAddressRE.MatchString(address) {
			return nil, fmt.Errorf("非法 EVM 地址")
		}
		clean = append(clean, address)
	}
	result := &CoverageResult{ChainKey: strings.ToLower(strings.TrimSpace(req.ChainKey)), Addresses: clean}
	item := Coverage{Dataset: req.Dataset, Note: "缺少请求区间级覆盖证据，需要下载"}
	source, ok := r.source.(RangeCoverageSource)
	if !ok || req.ToBlock < req.FromBlock {
		result.Items = append(result.Items, item)
		return result, nil
	}
	allCovered := len(clean) > 0
	for _, address := range clean {
		covered, rows, err := source.AddressRangeCovered(ctx, result.ChainKey, address, req.Dataset, req.FromBlock, req.ToBlock)
		if err != nil {
			item.Note = fmt.Sprintf("范围覆盖查询失败: %v", err)
			allCovered = false
			break
		}
		item.TxCount += rows
		if !covered {
			allCovered = false
		}
	}
	item.Have = allCovered
	if allCovered {
		item.Note = fmt.Sprintf("请求区间已完整覆盖（%d 笔）", item.TxCount)
	}
	result.Items = append(result.Items, item)
	return result, nil
}
