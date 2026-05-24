package model

import (
	"encoding/json"
	"testing"
)

func TestTransactionRow(t *testing.T) {
	row := TransactionRow{
		"交易时间": "2024-01-01 12:00:00",
		"交易金额": "100.00",
		"收付标志": "出",
	}
	if row["交易时间"] != "2024-01-01 12:00:00" {
		t.Errorf("expected 2024-01-01 12:00:00, got %s", row["交易时间"])
	}
}

func TestFlowGraphJSON(t *testing.T) {
	graph := FlowGraph{
		Nodes: []FlowNode{{ID: "node1", Label: "Node 1"}},
		Edges: []FlowEdge{{ID: "edge1", Source: "node1", Target: "node2", Amount: 100}},
		Meta:  map[string]interface{}{"total": 1},
	}
	data, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	var decoded FlowGraph
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Nodes) != 1 || decoded.Nodes[0].ID != "node1" {
		t.Errorf("node id mismatch")
	}
}

func TestQualityReport(t *testing.T) {
	report := QualityReport{
		RowsIn:  100,
		RowsOut: 80,
	}
	if report.RowsIn != 100 || report.RowsOut != 80 {
		t.Errorf("quality report fields mismatch")
	}
}
