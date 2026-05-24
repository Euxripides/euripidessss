package etl

import (
	"testing"

	"github.com/etl/backend/internal/model"
)

func TestBuildPreview(t *testing.T) {
	rows := []model.TransactionRow{
		{"交易时间": "2024-01-01", "交易金额": "100", "收付标志": "进"},
		{"交易时间": "2024-01-02", "交易金额": "200", "收付标志": "出"},
	}
	preview, cols := BuildPreview(rows, 10)
	if len(preview) != 2 {
		t.Errorf("expected 2 preview rows, got %d", len(preview))
	}
	if len(cols) < 3 {
		t.Errorf("expected at least 3 columns, got %d", len(cols))
	}
}

func TestBuildPreviewEmpty(t *testing.T) {
	preview, cols := BuildPreview(nil, 10)
	if preview != nil {
		t.Errorf("expected nil preview for empty input")
	}
	if len(cols) == 0 {
		t.Errorf("expected non-empty default columns")
	}
}

func TestDeduplicateTransactions(t *testing.T) {
	rows := []model.TransactionRow{
		{"交易时间": "2024-01-01", "交易金额": "100", "收付标志": "进"},
		{"交易时间": "2024-01-01", "交易金额": "100", "收付标志": "进"},
		{"交易时间": "2024-01-02", "交易金额": "200", "收付标志": "出"},
	}
	deduped := DeduplicateTransactions(rows)
	if len(deduped) != 2 {
		t.Errorf("expected 2 after dedup, got %d", len(deduped))
	}
}

func TestBuildSummary(t *testing.T) {
	rows := []model.TransactionRow{
		{"交易时间": "2024-01-01", "交易金额": "100", "收付标志": "进"},
		{"交易时间": "2024-01-02", "交易金额": "50", "收付标志": "出"},
	}
	summary := BuildSummary(rows)
	totalIn := summary["total_in"].(float64)
	totalOut := summary["total_out"].(float64)
	if totalIn != 100 {
		t.Errorf("expected total_in=100, got %f", totalIn)
	}
	if totalOut != 50 {
		t.Errorf("expected total_out=50, got %f", totalOut)
	}
}

func TestBuildFlowGraph(t *testing.T) {
	rows := []model.TransactionRow{
		{"交易时间": "2024-01-01", "交易金额": "100", "收付标志": "出",
			"交易卡号": "card1", "交易账号": "acct1", "交易对手账卡号": "card2", "对手户名": "user2"},
		{"交易时间": "2024-01-02", "交易金额": "200", "收付标志": "进",
			"交易卡号": "card1", "交易账号": "acct1", "交易对手账卡号": "card3", "对手户名": "user3"},
	}
	graph := BuildFlowGraph(rows, 10)
	if len(graph.Nodes) == 0 {
		t.Errorf("expected non-empty nodes")
	}
	if len(graph.Edges) == 0 {
		t.Errorf("expected non-empty edges")
	}
	if graph.Meta == nil {
		t.Errorf("expected non-nil meta")
	}
}

func TestBuildFlowGraphMetaCountsUntruncatedTotals(t *testing.T) {
	rows := []model.TransactionRow{
		{"交易时间": "2024-01-01", "交易金额": "300", "收付标志": "出", "交易卡号": "card1", "交易对手账卡号": "card2"},
		{"交易时间": "2024-01-02", "交易金额": "200", "收付标志": "出", "交易卡号": "card1", "交易对手账卡号": "card3"},
		{"交易时间": "2024-01-03", "交易金额": "100", "收付标志": "出", "交易卡号": "card1", "交易对手账卡号": "card4"},
	}

	graph := BuildFlowGraph(rows, 1)
	if got := graph.Meta["total_edges"]; got != 3 {
		t.Fatalf("expected total_edges=3, got %v", got)
	}
	if got := graph.Meta["total_nodes"]; got != 4 {
		t.Fatalf("expected total_nodes=4, got %v", got)
	}
	if got := graph.Meta["rendered_edges"]; got != 1 {
		t.Fatalf("expected rendered_edges=1, got %v", got)
	}
	if got := graph.Meta["rendered_nodes"]; got != 2 {
		t.Fatalf("expected rendered_nodes=2, got %v", got)
	}
}

func TestBuildFlowGraphEmpty(t *testing.T) {
	graph := BuildFlowGraph(nil, 10)
	if len(graph.Nodes) != 0 || len(graph.Edges) != 0 {
		t.Errorf("expected empty graph for nil input")
	}
}

func TestGenerateJobID(t *testing.T) {
	id1 := GenerateJobID()
	id2 := GenerateJobID()
	if id1 == "" || id2 == "" {
		t.Errorf("expected non-empty job IDs")
	}
	if id1 == id2 {
		t.Errorf("expected unique job IDs, got same: %s", id1)
	}
}

func TestParseQueryParam(t *testing.T) {
	if n := ParseQueryParam("100", 50); n != 100 {
		t.Errorf("expected 100, got %d", n)
	}
	if n := ParseQueryParam("", 50); n != 50 {
		t.Errorf("expected 50, got %d", n)
	}
	if n := ParseQueryParam("abc", 50); n != 50 {
		t.Errorf("expected 50, got %d", n)
	}
}
