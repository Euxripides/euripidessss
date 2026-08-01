package intelligence

import (
	"context"
	"sync"
)

// ── 测试用 fake ──

// FakeFlowSource 是内存 FlowSource。
type FakeFlowSource struct {
	mu    sync.Mutex
	flows map[string][]FundEdge
}

// NewFakeFlowSource 创建空 fake。
func NewFakeFlowSource() *FakeFlowSource {
	return &FakeFlowSource{flows: make(map[string][]FundEdge)}
}

// SetFlows 设置地址资金流。
func (f *FakeFlowSource) SetFlows(address string, edges []FundEdge) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flows[address] = edges
}

// Flows 实现 FlowSource。
func (f *FakeFlowSource) Flows(_ context.Context, address string) ([]FundEdge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FundEdge(nil), f.flows[address]...), nil
}

// edge 快速构造测试边。
func edge(from, to, token, amount string, ts int64) FundEdge {
	return FundEdge{
		From:      from,
		To:        to,
		Token:     token,
		Amount:    amount,
		TxHash:    "0x" + from[2:8] + to[2:8] + "abc",
		Timestamp: ts,
	}
}
