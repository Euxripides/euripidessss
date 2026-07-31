package downloadengine

import (
	"context"
	"fmt"
	"sort"
)

// ── Discovery Engine ──
// 封装地址发现逻辑，返回每个地址的首次出现信息。

type DiscoveryEngine struct {
	resolver FirstSeenResolver
}

// FirstSeenResolver 定义首次时间解析能力。具体实现由 parquetdownload 或外部注入。
type FirstSeenResolver interface {
	ResolveFirstSeen(ctx context.Context, chainID, address string) (*AddressDiscovery, error)
}

func NewDiscoveryEngine(resolver FirstSeenResolver) *DiscoveryEngine {
	return &DiscoveryEngine{resolver: resolver}
}

// Discover 批量解析地址首次出现时间。
// 不因少量地址失败而整体失败；每个地址独立记录状态。
func (e *DiscoveryEngine) Discover(ctx context.Context, chainID string, addresses []string) *DiscoveryResult {
	result := &DiscoveryResult{Total: len(addresses)}
	for _, addr := range addresses {
		disc, err := e.resolver.ResolveFirstSeen(ctx, chainID, addr)
		if err != nil {
			disc = &AddressDiscovery{
				Address: addr,
				Status:  FSFailed,
			}
		}
		result.Items = append(result.Items, *disc)
		switch disc.Status {
		case FSFound:
			result.Found++
		case FSPartial:
			result.Partial++
		case FSNotFound:
			result.NotFound++
		case FSTemporarilyUnavailable:
			result.TemporarilyUnavailable++
		case FSFailed:
			result.Failed++
		}
	}
	return result
}

// ── Range Planner ──
// 根据 Discovery 结果和范围模式，计算最终下载范围。

type RangePlanner struct {
	discovery *DiscoveryEngine
}

func NewRangePlanner(discovery *DiscoveryEngine) *RangePlanner {
	return &RangePlanner{discovery: discovery}
}

// Plan 根据请求参数计算 EffectiveRange。
func (p *RangePlanner) Plan(ctx context.Context, req RangePlanRequest) (*EffectiveRange, error) {
	switch req.Mode {
	case RangeAutoFirstSeen:
		return p.planAutoFirstSeen(ctx, req)
	case RangeBlock:
		return p.planBlock(req)
	case RangeTime:
		return p.planTime(req)
	case RangeFullHistory:
		return p.planFullHistory(req)
	case RangeResume:
		return p.planResume(req)
	case RangeIncremental:
		return p.planIncremental(req)
	default:
		return nil, fmt.Errorf("不支持的范围模式: %s", req.Mode)
	}
}

type RangePlanRequest struct {
	Mode          RangeMode
	ChainID       string
	Addresses     []string
	StartBlock    *uint64
	EndBlock      *uint64
	StartTime     string
	EndTime       string
	LastSyncBlock *uint64 // for Resume/Incremental
}

func (p *RangePlanner) planAutoFirstSeen(ctx context.Context, req RangePlanRequest) (*EffectiveRange, error) {
	if len(req.Addresses) == 0 {
		return nil, fmt.Errorf("%s: 至少需要一个地址", ErrFirstSeenNotFound)
	}

	result := p.discovery.Discover(ctx, req.ChainID, req.Addresses)
	if result.Found == 0 && result.Partial == 0 {
		return nil, fmt.Errorf("%s: 所有地址均无法解析首次时间 (found=%d partial=%d not_found=%d unavailable=%d failed=%d)",
			ErrFirstSeenNotFound, result.Found, result.Partial, result.NotFound, result.TemporarilyUnavailable, result.Failed)
	}

	// 取所有成功解析地址的最小区块
	var minBlock uint64
	var minTime string
	coverage := CoverageV2Full
	for _, item := range result.Items {
		if item.Status == FSPartial {
			coverage = CoverageV2Partial
		}
		if item.FirstSeenBlock != nil && (minBlock == 0 || *item.FirstSeenBlock < minBlock) {
			minBlock = *item.FirstSeenBlock
			if item.FirstSeenTime != nil {
				minTime = *item.FirstSeenTime
			}
		}
	}

	return &EffectiveRange{
		StartBlock:     minBlock,
		EndBlock:       req.EndBlockValue(),
		StartTime:      minTime,
		EndTime:        req.EndTime,
		RangeSource:    "FIRST_SEEN",
		CoverageStatus: string(coverage),
	}, nil
}

func (p *RangePlanner) planBlock(req RangePlanRequest) (*EffectiveRange, error) {
	if req.StartBlock == nil || req.EndBlock == nil {
		return nil, fmt.Errorf("区块范围模式需要 start_block 和 end_block")
	}
	if *req.StartBlock >= *req.EndBlock {
		return nil, fmt.Errorf("%s: start_block(%d) >= end_block(%d)", ErrDateRangeInvalid, *req.StartBlock, *req.EndBlock)
	}
	return &EffectiveRange{
		StartBlock:  *req.StartBlock,
		EndBlock:    *req.EndBlock,
		RangeSource: "USER_SELECTED",
	}, nil
}

func (p *RangePlanner) planTime(req RangePlanRequest) (*EffectiveRange, error) {
	if req.StartTime == "" || req.EndTime == "" {
		return nil, fmt.Errorf("%s: 时间范围必须提供 start_time 和 end_time", ErrStartTimeRequired)
	}
	return &EffectiveRange{
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		RangeSource: "USER_SELECTED",
	}, nil
}

func (p *RangePlanner) planFullHistory(req RangePlanRequest) (*EffectiveRange, error) {
	return &EffectiveRange{
		StartBlock:  0, // genesis
		EndBlock:    req.EndBlockValue(),
		RangeSource: "FULL_HISTORY",
	}, nil
}

func (p *RangePlanner) planResume(req RangePlanRequest) (*EffectiveRange, error) {
	if req.LastSyncBlock == nil {
		return nil, fmt.Errorf("恢复模式需要 last_sync_block")
	}
	return &EffectiveRange{
		StartBlock:  *req.LastSyncBlock + 1,
		EndBlock:    req.EndBlockValue(),
		RangeSource: "RESUME",
	}, nil
}

func (p *RangePlanner) planIncremental(req RangePlanRequest) (*EffectiveRange, error) {
	if req.LastSyncBlock == nil {
		return nil, fmt.Errorf("增量模式需要 last_sync_block")
	}
	return &EffectiveRange{
		StartBlock:  *req.LastSyncBlock + 1,
		EndBlock:    req.EndBlockValue(),
		RangeSource: "INCREMENTAL",
	}, nil
}

// ── Address Group Planner ──

type AddressGroup struct {
	ID        string   `json:"group_id"`
	Addresses []string `json:"addresses"`
	MinBlock  uint64   `json:"min_block"`
	MaxBlock  uint64   `json:"max_block"`
}

// PlanGroups 按地址数量和首次区块分组。
// 1-100 合并为单组；101-5000 按区块分层；5000+ 哈希分桶。
func PlanGroups(addresses []string, discoveries []AddressDiscovery, maxPerGroup int) []AddressGroup {
	if maxPerGroup <= 0 {
		maxPerGroup = 100
	}

	total := len(addresses)
	switch {
	case total <= 100:
		return singleGroup(addresses, discoveries)
	case total <= 5000:
		return blockLayerGroups(addresses, discoveries, maxPerGroup)
	default:
		return hashBucketGroups(addresses, discoveries, maxPerGroup)
	}
}

func singleGroup(addresses []string, discoveries []AddressDiscovery) []AddressGroup {
	discMap := make(map[string]*AddressDiscovery, len(discoveries))
	for i := range discoveries {
		discMap[discoveries[i].Address] = &discoveries[i]
	}
	var minBlock, maxBlock uint64
	for _, addr := range addresses {
		if d, ok := discMap[addr]; ok && d.FirstSeenBlock != nil {
			if minBlock == 0 || *d.FirstSeenBlock < minBlock {
				minBlock = *d.FirstSeenBlock
			}
			if *d.FirstSeenBlock > maxBlock {
				maxBlock = *d.FirstSeenBlock
			}
		}
	}
	return []AddressGroup{{ID: "group-0", Addresses: addresses, MinBlock: minBlock, MaxBlock: maxBlock}}
}

func blockLayerGroups(addresses []string, discoveries []AddressDiscovery, maxPerGroup int) []AddressGroup {
	discMap := make(map[string]*AddressDiscovery)
	for i := range discoveries {
		discMap[discoveries[i].Address] = &discoveries[i]
	}
	// 按首次区块排序
	type addrBlock struct {
		addr  string
		block uint64
	}
	sorted := make([]addrBlock, 0, len(addresses))
	for _, addr := range addresses {
		b := uint64(0)
		if d, ok := discMap[addr]; ok && d.FirstSeenBlock != nil {
			b = *d.FirstSeenBlock
		}
		sorted = append(sorted, addrBlock{addr, b})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].block < sorted[j].block })

	var groups []AddressGroup
	for i := 0; i < len(sorted); i += maxPerGroup {
		end := i + maxPerGroup
		if end > len(sorted) {
			end = len(sorted)
		}
		batch := sorted[i:end]
		addrs := make([]string, len(batch))
		for j, ab := range batch {
			addrs[j] = ab.addr
		}
		groups = append(groups, AddressGroup{
			ID:        fmt.Sprintf("group-%d", len(groups)),
			Addresses: addrs,
			MinBlock:  batch[0].block,
			MaxBlock:  batch[len(batch)-1].block,
		})
	}
	return groups
}

func hashBucketGroups(addresses []string, discoveries []AddressDiscovery, maxPerGroup int) []AddressGroup {
	groups := make(map[uint32][]string)
	for _, addr := range addresses {
		h := fnvHash(addr)
		bucket := h % 16
		groups[bucket] = append(groups[bucket], addr)
	}
	var result []AddressGroup
	for bucket, addrs := range groups {
		// 超限再拆
		for i := 0; i < len(addrs); i += maxPerGroup {
			end := i + maxPerGroup
			if end > len(addrs) {
				end = len(addrs)
			}
			result = append(result, AddressGroup{
				ID:        fmt.Sprintf("group-%d-%d", bucket, i/maxPerGroup),
				Addresses: addrs[i:end],
			})
		}
	}
	return result
}

func fnvHash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func (r RangePlanRequest) EndBlockValue() uint64 {
	if r.EndBlock != nil {
		return *r.EndBlock
	}
	return 0 // caller should set current finalized block
}
