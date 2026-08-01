package dynamicinvestigation

import (
	"context"
	"sync"
)

// ── 测试用 fake 实现 ──

// FakeSource 是内存 DiscoverySource。
type FakeSource struct {
	mu       sync.Mutex
	flows    map[string][]FlowSignal // address → 资金流
	profiles map[string]*ProfileSignal
}

// NewFakeSource 创建空 fake。
func NewFakeSource() *FakeSource {
	return &FakeSource{
		flows:    make(map[string][]FlowSignal),
		profiles: make(map[string]*ProfileSignal),
	}
}

// SetFlows 设置地址的资金流。
func (f *FakeSource) SetFlows(address string, flows []FlowSignal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flows[address] = flows
}

// SetProfile 设置地址画像。
func (f *FakeSource) SetProfile(address string, p *ProfileSignal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles[address] = p
}

// Flows 实现 DiscoverySource。
func (f *FakeSource) Flows(_ context.Context, address string) ([]FlowSignal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FlowSignal(nil), f.flows[address]...), nil
}

// Profile 实现 DiscoverySource。
func (f *FakeSource) Profile(_ context.Context, address string) (*ProfileSignal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[address]
	if !ok {
		return &ProfileSignal{}, nil
	}
	copy := *p
	return &copy, nil
}

// FakeExecutor 是内存采集执行器，记录执行的任务。
type FakeExecutor struct {
	mu     sync.Mutex
	tasks  []*AcquisitionTask
	failOn map[string]string // mode → error message（指定模式失败）
}

// NewFakeExecutor 创建空 fake。
func NewFakeExecutor() *FakeExecutor {
	return &FakeExecutor{failOn: make(map[string]string)}
}

// FailOn 指定某采集模式失败。
func (f *FakeExecutor) FailOn(mode AcquisitionMode, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOn[string(mode)] = msg
}

// Execute 实现 AcquisitionExecutor。
func (f *FakeExecutor) Execute(_ context.Context, task *AcquisitionTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, task)
	if msg, ok := f.failOn[string(task.Mode)]; ok {
		return &fakeErr{msg}
	}
	task.SetStatus("done")
	task.SetJobID("fake-job-" + task.TaskID)
	return nil
}

// Tasks 返回执行过的任务只读视图。
func (f *FakeExecutor) Tasks() []TaskView {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]TaskView, len(f.tasks))
	for i, t := range f.tasks {
		out[i] = t.View()
	}
	return out
}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }
