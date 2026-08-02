package intelligence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testAddress = "0x00000000000000000000000000000000000000a1"

func newTestRequest() *InvestigationRequest {
	return &InvestigationRequest{
		Address:        testAddress,
		ChainID:        "bsc",
		Objective:      "寻找资金沉淀地址",
		ExpectedResult: []string{"资金流图", "交易所入口"},
		Mode:           ModeFundTrace,
	}
}

func TestRequestStoreCreateAndGet(t *testing.T) {
	s := NewRequestStore("")
	req, err := s.Create(newTestRequest())
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if req.ID == "" {
		t.Fatal("应生成请求 ID")
	}
	if req.Status != RequestCreated {
		t.Fatalf("状态应为 created, got %s", req.Status)
	}
	got, ok := s.Get(req.ID)
	if !ok {
		t.Fatal("Get 应命中")
	}
	if got.Objective != "寻找资金沉淀地址" || len(got.ExpectedResult) != 2 {
		t.Fatalf("Get 内容不一致: %+v", got)
	}
}

func TestRequestStorePersistReload(t *testing.T) {
	dir := t.TempDir()
	s := NewRequestStore(dir)
	r1, err := s.Create(newTestRequest())
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	// 回填调查 ID 并保存
	if err := s.Link(r1.ID, "inv-1", RequestStarted); err != nil {
		t.Fatalf("Link 失败: %v", err)
	}
	// 新 store 重载同一目录
	s2 := NewRequestStore(dir)
	got, ok := s2.Get(r1.ID)
	if !ok {
		t.Fatal("重载后应能读取请求")
	}
	if got.InvestigationID != "inv-1" || got.Status != RequestStarted {
		t.Fatalf("重载后回填丢失: %+v", got)
	}
	// ID 自增应推进，避免重启后覆盖
	r2, err := s2.Create(newTestRequest())
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if r2.ID == r1.ID {
		t.Fatalf("重启后 ID 不应重复: %s", r2.ID)
	}
	if _, err := filepath.Glob(filepath.Join(dir, "req-*.json.tmp")); err != nil {
		t.Fatalf("glob 失败: %v", err)
	}
}

func TestRequestStoreLinkAndList(t *testing.T) {
	s := NewRequestStore("")
	r1, _ := s.Create(newTestRequest())
	time.Sleep(2 * time.Millisecond) // 保证 CreatedAt 有先后
	r2 := newTestRequest()
	r2.Objective = "识别交易所入口"
	r2.Mode = ModeExchangeEntry
	r2b, _ := s.Create(r2)
	if err := s.Link(r1.ID, "inv-9", RequestStarted); err != nil {
		t.Fatalf("Link 失败: %v", err)
	}
	list := s.List()
	if len(list) != 2 {
		t.Fatalf("List 数量应为 2, got %d", len(list))
	}
	if list[0].ID != r2b.ID {
		t.Fatalf("List 应按创建时间倒序, first=%s want=%s", list[0].ID, r2b.ID)
	}
	got, _ := s.Get(r1.ID)
	if got.InvestigationID != "inv-9" {
		t.Fatalf("Link 回填失败: %+v", got)
	}
}

func TestValidateInvestigationRequest(t *testing.T) {
	cases := []struct {
		name     string
		address  string
		mode     string
		obj      string
		expected []string
		wantErr  error
		wantMode InvestigationMode
	}{
		{"合法", testAddress, "fund_trace", "找资金去向", []string{"资金流图"}, nil, ModeFundTrace},
		{"空 mode 按 auto", testAddress, "", "找资金去向", nil, nil, ModeAuto},
		{"非法地址", "0xzzz", "fund_trace", "找资金去向", nil, ErrInvalidAddress, ""},
		{"目的与期望结果都为空", testAddress, "fund_trace", "", nil, ErrEmptyRequest, ""},
		{"非法模式", testAddress, "hack", "找资金去向", nil, ErrInvalidMode, ""},
		{"仅期望结果", testAddress, "", "", []string{"资金流图"}, nil, ModeAuto},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, _, mode, err := ValidateInvestigationRequest(c.address, "bsc", c.obj, c.expected, c.mode)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if err == nil {
				if mode != c.wantMode {
					t.Fatalf("mode = %s, want %s", mode, c.wantMode)
				}
				if addr != testAddress {
					t.Fatalf("address 应小写规范化, got %s", addr)
				}
			}
		})
	}
}

func TestRequestStoreCreateIsolation(t *testing.T) {
	s := NewRequestStore("")
	req := newTestRequest()
	req.ExpectedResult = []string{"资金流图", "交易所入口"}
	req.Intent = &InvestigationIntent{Direction: "out", Goals: []string{GoalFundDestination}, Mode: ModeFundTrace, Summary: "s"}
	created, err := s.Create(req)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	// 调用方修改原对象不应影响 store
	req.ExpectedResult[0] = "被改写"
	req.Intent.Goals[0] = "被改写"
	got, ok := s.Get(created.ID)
	if !ok {
		t.Fatal("Get 应命中")
	}
	if got.ExpectedResult[0] != "资金流图" {
		t.Fatalf("store 内请求被调用方改写: %v", got.ExpectedResult)
	}
	if got.Intent == nil || got.Intent.Goals[0] != GoalFundDestination {
		t.Fatalf("store 内意图被调用方改写: %+v", got.Intent)
	}
	// 返回值也应隔离
	created.ExpectedResult[0] = "再改写"
	got2, _ := s.Get(created.ID)
	if got2.ExpectedResult[0] != "资金流图" {
		t.Fatalf("返回值不应共享 store 状态: %v", got2.ExpectedResult)
	}
}

func TestRequestStoreLinkTerminalGuard(t *testing.T) {
	s := NewRequestStore("")
	r1, _ := s.Create(newTestRequest())
	// 终态 finished 不可被回退为 started
	if err := s.Link(r1.ID, "inv-1", RequestFinished); err != nil {
		t.Fatalf("Link 失败: %v", err)
	}
	if err := s.Link(r1.ID, "inv-1", RequestStarted); err != nil {
		t.Fatalf("Link 失败: %v", err)
	}
	got, _ := s.Get(r1.ID)
	if got.Status != RequestFinished {
		t.Fatalf("终态不应被回退为 started, got %s", got.Status)
	}
	// 终态之间可更新（finished→failed 等终态间覆盖无实际触发路径，此处验证 started 无法覆盖终态）
	r2, _ := s.Create(newTestRequest())
	_ = s.Link(r2.ID, "inv-2", RequestFailed)
	_ = s.Link(r2.ID, "inv-2", RequestStarted)
	g2, _ := s.Get(r2.ID)
	if g2.Status != RequestFailed {
		t.Fatalf("failed 终态不应被回退, got %s", g2.Status)
	}
}

func TestRequestStoreLoadAllRejectsBadID(t *testing.T) {
	dir := t.TempDir()
	// 篡改磁盘：越界 ID（路径穿越）与非法 ID 不应被加载
	// 注：V1 存储层文件为 envelope 格式（schema_version + data）
	writeEnvelope := func(name string, v any) {
		env := struct {
			SchemaVersion int `json:"schema_version"`
			Data          any `json:"data"`
		}{SchemaVersion: 1, Data: v}
		data, _ := json.Marshal(env)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			t.Fatalf("写坏文件失败: %v", err)
		}
	}
	writeEnvelope("evil.json", &InvestigationRequest{ID: "../../evil", Address: testAddress, Objective: "x"})
	writeEnvelope("req-7.json", &InvestigationRequest{ID: "req-7", Address: testAddress, Objective: "ok"})
	s := NewRequestStore(dir)
	if _, ok := s.Get("../../evil"); ok {
		t.Fatal("越界 ID 不应被加载")
	}
	got, ok := s.Get("req-7")
	if !ok || got.Objective != "ok" {
		t.Fatalf("合法 ID 应被加载: %+v", got)
	}
	if s.nextID <= 7 {
		t.Fatalf("计数器应推进到 8, got %d", s.nextID)
	}
}

func TestValidateObjectiveTooLong(t *testing.T) {
	long := strings.Repeat("测", 501)
	_, _, _, err := ValidateInvestigationRequest(testAddress, "bsc", long, nil, "auto")
	if !errors.Is(err, ErrObjectiveTooLong) {
		t.Fatalf("超长目的应 ErrObjectiveTooLong, got %v", err)
	}
	ok := strings.Repeat("测", 500)
	if _, _, _, err := ValidateInvestigationRequest(testAddress, "bsc", ok, nil, "auto"); err != nil {
		t.Fatalf("500 字符应合法: %v", err)
	}
}
