package dynamicinvestigation

import (
	"strings"
	"sync"
)

// ── 实体识别 ──
//
// 分类：wallet / exchange / bridge / dex / router / contract / unknown
// 重点识别：交易所热钱包、提现地址、归集地址。
// 信号来源：已知实体标签库（优先）+ 合约判定 + 图结构模式（归集/分散/中转）。

// EntityHints 是实体识别所需的信号。
type EntityHints struct {
	Address      string // 地址
	IsContract   bool   // 是否合约（来自 Profile.ContractCount > 0 或 RPC）
	InCount      int64  // 入边数
	OutCount     int64  // 出边数
	TxCount      int64  // 交易笔数
	IsKnownLabel bool   // 是否命中已知标签库
}

// KnownEntity 是已知实体条目。
type KnownEntity struct {
	Address string     `json:"address"`
	Entity  EntityType `json:"entity"`
	Label   string     `json:"label"`
}

// Recognizer 负责实体识别。
type Recognizer struct {
	mu    sync.RWMutex
	known map[string]KnownEntity // address(lower) → known
}

// NewRecognizer 创建识别器。
func NewRecognizer() *Recognizer {
	return &Recognizer{known: make(map[string]KnownEntity)}
}

// AddKnown 注册已知实体（交易所热钱包/桥/DEX 等）。
func (r *Recognizer) AddKnown(entries ...KnownEntity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range entries {
		r.known[strings.ToLower(strings.TrimSpace(e.Address))] = e
	}
}

// AddKnownMap 批量注册（address → (entity,label)）。
func (r *Recognizer) AddKnownMap(m map[string][2]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for addr, v := range m {
		r.known[strings.ToLower(strings.TrimSpace(addr))] = KnownEntity{
			Address: strings.ToLower(strings.TrimSpace(addr)),
			Entity:  EntityType(v[0]),
			Label:   v[1],
		}
	}
}

// Known 返回已知实体（副本）。
func (r *Recognizer) Known() []KnownEntity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]KnownEntity, 0, len(r.known))
	for _, v := range r.known {
		out = append(out, v)
	}
	return out
}

// Recognize 识别地址实体类型。
// 优先级：已知标签库 > 合约（bridge/dex/router 需标签，纯合约判 contract）
// > 图模式（归集 sink / 分散 spreader / 中转 hub）> wallet / unknown。
func (r *Recognizer) Recognize(hints EntityHints) (EntityType, string) {
	addr := strings.ToLower(strings.TrimSpace(hints.Address))

	// 1. 已知标签库（交易所热钱包/桥/DEX 等）
	r.mu.RLock()
	known, ok := r.known[addr]
	r.mu.RUnlock()
	if ok {
		return known.Entity, known.Label
	}

	// 2. 无任何交易数据 → unknown
	if hints.TxCount == 0 && !hints.IsContract && hints.InCount == 0 && hints.OutCount == 0 {
		return EntityUnknown, ""
	}

	// 3. 合约（无标签的纯合约）
	if hints.IsContract {
		return EntityContract, "合约地址"
	}

	// 4. 图结构模式（基于 in/out 比例）
	// 归集地址：入多出少（sink）
	if hints.InCount >= 10 && hints.InCount > 2*hints.OutCount {
		return EntityExchange, "归集地址（入多出少）"
	}
	// 分散地址：出多入少（spreader）
	if hints.OutCount >= 10 && hints.OutCount > 2*hints.InCount {
		return EntityExchange, "分散地址（出多入少）"
	}
	// 中转枢纽：出入均衡且量大（hub）
	if hints.InCount >= 10 && hints.OutCount >= 10 {
		return EntityRouter, "中转枢纽"
	}

	// 5. 普通钱包
	if hints.TxCount > 0 {
		return EntityWallet, "钱包地址"
	}
	return EntityUnknown, ""
}
