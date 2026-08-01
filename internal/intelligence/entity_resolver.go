package intelligence

import (
	"context"
	"time"

	"github.com/etl/backend/internal/dynamicinvestigation"
)

// ── Entity Resolver ──
//
// 地址身份识别：wallet / exchange / bridge / dex / router / contract / unknown。
// 复用 dynamicinvestigation.Recognizer 的识别能力（标签库 + 合约判定 + 图模式）。

// EntityResolver 识别地址实体。
type EntityResolver struct {
	recognizer *dynamicinvestigation.Recognizer
	svc        *dynamicinvestigation.DiscoverySource
}

// NewEntityResolver 创建解析器。
// recognizer 可为 nil（自动创建）；svc 可为 nil（无画像信号时仅按图模式识别）。
func NewEntityResolver(recognizer *dynamicinvestigation.Recognizer, svc *dynamicinvestigation.DiscoverySource) *EntityResolver {
	if recognizer == nil {
		recognizer = dynamicinvestigation.NewRecognizer()
	}
	return &EntityResolver{recognizer: recognizer, svc: svc}
}

// Resolve 识别单个地址实体。
func (r *EntityResolver) Resolve(ctx context.Context, address string) (EntityInfo, error) {
	hints := dynamicinvestigation.EntityHints{Address: address}
	info := EntityInfo{Address: address, Entity: string(dynamicinvestigation.EntityUnknown)}

	if r.svc != nil && *r.svc != nil {
		signal, err := (*r.svc).Profile(ctx, address)
		if err == nil && signal != nil {
			hints.IsContract = signal.IsContract
			hints.TxCount = signal.TxCount
			hints.InCount = signal.InCount
			hints.OutCount = signal.OutCount
			info.TxCount = signal.TxCount
			info.Risk = signal.RiskScore
		}
	}

	entity, label := r.recognizer.Recognize(hints)
	info.Entity = string(entity)
	info.Label = label
	return info, nil
}

// ResolveBatch 批量识别地址实体。
func (r *EntityResolver) ResolveBatch(ctx context.Context, addresses []string) []EntityInfo {
	out := make([]EntityInfo, 0, len(addresses))
	for _, addr := range addresses {
		info, err := r.Resolve(ctx, addr)
		if err != nil {
			info = EntityInfo{Address: addr, Entity: string(dynamicinvestigation.EntityUnknown)}
		}
		out = append(out, info)
	}
	return out
}

// ── Expansion Engine ──
//
// 自动地址扩展：发现地址后不立即下载，而是实体识别 → Expansion Score → 决定是否扩展。
// 复用 dynamicinvestigation.Engine 的扩展队列与采集路由。

// ExpansionEngine 包装 dynamicinvestigation 的地址扩展能力。
type ExpansionEngine struct {
	engine *dynamicinvestigation.Engine
}

// NewExpansionEngine 创建扩展引擎适配器。
func NewExpansionEngine(engine *dynamicinvestigation.Engine) *ExpansionEngine {
	return &ExpansionEngine{engine: engine}
}

// Expand 从目标地址启动一轮扩展，返回扩展结果摘要。
// 仅返回本次调用新发现的邻居地址（Depth>0 且 DiscoveredAt 晚于启动时间），
// 避免共享发现队列中其他调查/目标的条目污染本调查候选；本地截断到 maxAddresses，
// 不再写共享引擎配置（消除并发调查间的配置竞争）。
func (e *ExpansionEngine) Expand(ctx context.Context, target string, maxAddresses int) ([]ExpansionResult, error) {
	if e.engine == nil {
		return nil, nil
	}
	before := time.Now().UTC()
	if err := e.engine.Start(ctx, target); err != nil {
		return nil, err
	}
	// 汇总扩展结果（仅本次新发现的邻居）
	var out []ExpansionResult
	for _, item := range e.engine.Queue().List("", "", -1) {
		if item.Depth <= 0 || item.DiscoveredAt.Before(before) {
			continue // 目标自身（Depth 0）或历史条目
		}
		out = append(out, ExpansionResult{
			Address:     item.Address,
			Entity:      string(item.Entity),
			Score:       item.Score,
			Acquisition: string(item.Acquisition),
			Depth:       item.Depth,
			Reason:      item.IgnoredReason,
		})
		if maxAddresses > 0 && len(out) >= maxAddresses {
			break
		}
	}
	return out, nil
}
