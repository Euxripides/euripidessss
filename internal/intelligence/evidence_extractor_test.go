package intelligence

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEvidenceFromPaths(t *testing.T) {
	paths := []RankedPath{{
		Path: FundPath{Nodes: []string{vTarget, vOut}, Edges: []FundEdge{
			{From: vTarget, To: vOut, Token: "USDT", Amount: "500000", TxHash: "0xdeadbeef", Block: 100},
		}},
	}}
	evs := evidenceFromPaths("inv-1", paths)
	if len(evs) != 1 {
		t.Fatalf("应提取 1 条交易证据, got %d", len(evs))
	}
	ev := evs[0]
	if ev.Type != EvTransaction || ev.TxHash != "0xdeadbeef" || ev.BlockNumber != 100 {
		t.Fatalf("交易证据字段错误: %+v", ev)
	}
	if ev.Token != "USDT" || ev.Amount != "500000" {
		t.Fatalf("Token/金额错误: %+v", ev)
	}
	if ev.Confidence != 0.85 {
		t.Fatalf("链上交易置信度应为 0.85, got %v", ev.Confidence)
	}
}

func TestEvidenceFromPatternsAndObservations(t *testing.T) {
	patterns := []RiskPattern{{
		Type: PatternRapidTransfer, Address: vTarget, Severity: "high",
		Detail: "快速转移", DetectedAt: time.Now().UTC(),
		Edges: []FundEdge{{From: vTarget, To: vOut, Token: "USDT", Amount: "100", TxHash: "0xabc", Block: 1}},
	}}
	evs := evidenceFromPatterns("inv-1", patterns)
	if len(evs) != 1 || evs[0].Type != EvRisk || evs[0].Confidence != 0.9 {
		t.Fatalf("风险证据错误: %+v", evs)
	}
	obs := []Observation{
		{Type: ObsNewTransaction, Address: vTarget, Detail: "新交易", Timestamp: 1700000000},
		{Type: ObsNewAddress, Address: vOut, Detail: "新地址", Timestamp: 1700000001},
	}
	evs2 := evidenceFromObservations("inv-1", obs)
	if len(evs2) != 2 {
		t.Fatalf("观察证据应 2 条, got %d", len(evs2))
	}
	if evs2[0].Type != EvTransaction || evs2[1].Type != EvAddress {
		t.Fatalf("观察证据类型错误: %+v", evs2)
	}
}

func TestEvidenceFromProfitAndDedup(t *testing.T) {
	profit := &ProfitReport{Detected: true, Kind: "profit", Tokens: []string{"shib"}, Summary: "检测到买卖结构"}
	evs := evidenceFromProfit("inv-1", profit)
	if len(evs) != 2 { // 摘要 + token
		t.Fatalf("获利证据应 2 条, got %d", len(evs))
	}
	if evs[0].Type != EvProfit || evs[0].Confidence != 0.6 {
		t.Fatalf("获利证据错误: %+v", evs[0])
	}
	// 汇总去重：相同证据不重复
	paths := []RankedPath{{Path: FundPath{Edges: []FundEdge{{From: vTarget, To: vOut, Token: "USDT", Amount: "1", TxHash: "0xabc", Block: 1}}}}}
	all := extractEvidence(nil, "inv-1", paths, nil, nil, profit, 100)
	all2 := extractEvidence(all, "inv-1", paths, nil, nil, profit, 100)
	if len(all2) != 0 {
		t.Fatalf("重复证据应被去重, got %d", len(all2))
	}
}

func TestInvestigationHandlerEvidence(t *testing.T) {
	agent := newTestAgent()
	agent.evidence = NewEvidenceStore("")
	h := NewInvestigationHandler(agent, NewRequestStore(""), NewIntentAnalyzer())
	rr := doJSON(h, http.MethodPost, "/investigation/create", `{
		"address":"0x00000000000000000000000000000000000000a1",
		"objective":"找资金去向"
	}`)
	var out struct {
		Investigation Investigation `json:"investigation"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("create 响应解析失败: %v", err)
	}
	id := out.Investigation.ID
	// 注入证据
	evs := []Evidence{{Type: EvTransaction, TxHash: "0xev", Detail: "测试证据", Confidence: 0.9}}
	if err := agent.evidence.Add(id, evs...); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	rr = doJSON(h, http.MethodGet, "/investigation/"+id+"/evidence", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("evidence 应 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Total    int        `json:"total"`
		Evidence []Evidence `json:"evidence"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("evidence 响应解析失败: %v", err)
	}
	if resp.Total != 1 || len(resp.Evidence) != 1 {
		t.Fatalf("证据数量错误: %+v", resp)
	}
	if !strings.Contains(resp.Evidence[0].Detail, "测试证据") {
		t.Fatalf("证据内容错误: %+v", resp.Evidence[0])
	}
	// 不存在调查 404
	rr = doJSON(h, http.MethodGet, "/investigation/inv-999/evidence", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("不存在调查应 404, got %d", rr.Code)
	}
}

func TestEvidenceProfitBothKind(t *testing.T) {
	// 双结构（profit+holding）获利证据置信度应为 0.7
	profit := &ProfitReport{Detected: true, Kind: "profit+holding", Tokens: []string{"shib", "usdt"}, Summary: "买卖+沉淀"}
	evs := evidenceFromProfit("inv-1", profit)
	if len(evs) == 0 || evs[0].Confidence != 0.7 {
		t.Fatalf("双结构置信度应为 0.7, got %+v", evs)
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	// 中文按 rune 截断，不产生乱码
	s := "这是一个大额获利地址，寻找最终资金沉淀的完整描述文本"
	out := truncate(s, 10)
	if len([]rune(out)) != 10 {
		t.Fatalf("应截断为 10 个 rune, got %d", len([]rune(out)))
	}
	if strings.ContainsRune(out, '\uFFFD') {
		t.Fatal("不应出现乱码替换符")
	}
}
