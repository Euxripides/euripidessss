package investigationstore

import "time"

// ── Memory Storage（V1 设计 §9）──
//
// 跨案件知识记忆：地址历史/实体关系/案件关联。
// 分目录存储，避免单文件无限增长：
//
//	memory/address/{addr}.json    地址 → 标签/案件/资金关系
//	memory/entity/{entity}.json   实体 → 归属地址
//	memory/case/{case}.json       案件 → 涉及地址
//
// 关系同时写入 From 与 To 两个端点的地址记录（双向可达），
// 加载时按关系 ID 去重，保证 Search 与 All 语义与旧实现一致。

// RelationRecord 是一条跨案件知识关系（与业务类型解耦的存储记录）。
type RelationRecord struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"` // ADDRESS_ENTITY / ADDRESS_LINK / CASE_ADDRESS
	From            string    `json:"from"`
	To              string    `json:"to"`
	Detail          string    `json:"detail,omitempty"`
	InvestigationID string    `json:"investigation_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// MemoryAddressRecord 是地址维度的记忆记录。
type MemoryAddressRecord struct {
	Address   string           `json:"address"`
	Labels    []string         `json:"labels,omitempty"`
	Cases     []string         `json:"cases,omitempty"`
	Relations []RelationRecord `json:"relations,omitempty"`
}

// MemoryEntityRecord 是实体维度的记忆记录（实体 → 归属地址）。
type MemoryEntityRecord struct {
	Entity    string   `json:"entity"`
	Addresses []string `json:"addresses,omitempty"`
}

// MemoryCaseRecord 是案件维度的记忆记录（案件 → 涉及地址）。
type MemoryCaseRecord struct {
	CaseID    string   `json:"case_id"`
	Addresses []string `json:"addresses,omitempty"`
}
