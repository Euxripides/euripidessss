package smartdownload

import (
	"context"
	"fmt"
)

// CSVAdapter 浏览器/CSV 采集 Adapter（Phase 2 标记实现）：
// 生产环境需要浏览器登录态，Available=false（ManualOnly），自动调度跳过；
// 由原「浏览器下载」页人工执行。CSV→SQD 的自动切换验收用 MockCSVProvider 驱动。
type CSVAdapter struct{}

func NewCSVAdapter() *CSVAdapter { return &CSVAdapter{} }

func (p *CSVAdapter) Name() string           { return "csv" }
func (p *CSVAdapter) Available() bool        { return false }
func (p *CSVAdapter) ManualOnly() bool       { return true }
func (p *CSVAdapter) Supports(d string) bool { return ValidDataset(d) }

func (p *CSVAdapter) Probe(_ context.Context, _ ProbeRequest) (ProbeResult, error) {
	return ProbeResult{Confidence: 0}, nil
}

func (p *CSVAdapter) ExecuteRange(_ context.Context, _ RangeRequest) (*ProviderResult, error) {
	return nil, fmt.Errorf("CSV/浏览器采集需要登录态：请在「浏览器下载」页人工执行（自动调度已跳过）")
}

// MockCSVProvider 确定性 CSV Provider（仅测试/验收）：可注入失败以演示 CSV→SQD 切换。
type MockCSVProvider = MockProvider

// NewMockCSVProvider 创建名为 csv 的确定性 Provider。
func NewMockCSVProvider() *MockCSVProvider {
	return NewMockNamedProvider("csv")
}

// NewMockNamedProvider 创建指定名称的确定性 Provider（切换验收用）。
func NewMockNamedProvider(name string) *MockProvider {
	return &MockProvider{name: name}
}
