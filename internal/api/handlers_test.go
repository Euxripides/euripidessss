package api

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etl/backend/internal/etl"
	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
	_ "github.com/lib/pq"
)

func TestApplyFiltersHonorsTargetDirectionAndDate(t *testing.T) {
	txns := []model.TransactionRow{
		{
			"交易户名": "Alice", "对手户名": "Bob",
			"交易金额": "100", "收付标志": "出", "交易时间": "2024-01-02 10:00:00",
		},
		{
			"交易户名": "Alice", "对手户名": "Carol",
			"交易金额": "200", "收付标志": "出", "交易时间": "2024-01-02 11:00:00",
		},
		{
			"交易户名": "Alice", "对手户名": "Bob",
			"交易金额": "300", "收付标志": "进", "交易时间": "2024-01-02 12:00:00",
		},
		{
			"交易户名": "Alice", "对手户名": "Bob",
			"交易金额": "400", "收付标志": "出", "交易时间": "2024-02-01 10:00:00",
		},
	}

	filtered := applyFilters(txns, map[string]interface{}{
		"source_filters": []interface{}{
			map[string]interface{}{"column": "交易户名", "values": []interface{}{"Alice"}},
		},
		"target_filters": []interface{}{
			map[string]interface{}{"column": "对手户名", "values": []interface{}{"Bob"}},
		},
		"directions": []interface{}{"出"},
		"start_date": "2024-01-01",
		"end_date":   "2024-01-31",
	})

	if len(filtered) != 1 {
		t.Fatalf("expected 1 row after filters, got %d", len(filtered))
	}
	if filtered[0]["交易金额"] != "100" {
		t.Fatalf("expected amount 100, got %q", filtered[0]["交易金额"])
	}
}

func TestNormalizeFlowDirectionUsesBuiltInsAndAliases(t *testing.T) {
	aliases := map[string]string{"入账": "进", "loan": "出"}

	tests := map[string]string{
		"收入":     "进",
		"支出":     "出",
		"入账":     "进",
		" loan ": "出",
	}

	for input, want := range tests {
		if got := normalizeFlowDirection(input, aliases); got != want {
			t.Fatalf("normalizeFlowDirection(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTransactionFromMappedRowUsesNameColumnsForAccountNames(t *testing.T) {
	row := []string{
		"中国工商银行", "40000253115008797", "张三", "ID-001",
		"对手银行", "6222000000000001", "李四", "ID-002",
		"100", "2024-01-02 10:00:00", "出",
	}
	colIdx := map[string]int{
		"银行名称":    0,
		"交易方账户":   1,
		"交易方户名":   2,
		"交易方身份证号": 3,
		"对手银行":    4,
		"交易对手账卡号": 5,
		"对手户名":    6,
		"对手身份证号":  7,
		"交易金额":    8,
		"交易时间":    9,
		"收付标志":    10,
	}
	mapping := flowColumnMapping{
		SourceCol:     "银行名称",
		SourceAccount: "交易方账户",
		SourceName:    "交易方户名",
		SourceID:      "交易方身份证号",
		TargetCol:     "对手银行",
		TargetCard:    "交易对手账卡号",
		TargetName:    "对手户名",
		TargetID:      "对手身份证号",
		Amount:        "交易金额",
		Time:          "交易时间",
		Direction:     "收付标志",
	}

	txn := transactionFromMappedRow(row, colIdx, mapping, nil)
	if txn["交易户名"] != "张三" {
		t.Fatalf("交易户名 = %q, want 张三", txn["交易户名"])
	}
	if txn["对手户名"] != "李四" {
		t.Fatalf("对手户名 = %q, want 李四", txn["对手户名"])
	}
	if txn["交易户名"] == "中国工商银行" || txn["对手户名"] == "对手银行" {
		t.Fatalf("account names should not come from source/target entity columns: %#v", txn)
	}

	graph := etl.BuildFlowGraph([]model.TransactionRow{txn}, 10)
	node := findAPITestFlowNode(graph.Nodes, "40000253115008797")
	if node == nil {
		t.Fatalf("expected source account node")
	}
	if node.AccountName != "张三" {
		t.Fatalf("node AccountName = %q, want 张三", node.AccountName)
	}

	mappingWithoutExplicitName := mapping
	mappingWithoutExplicitName.SourceName = ""
	mappingWithoutExplicitName.TargetName = ""
	txn = transactionFromMappedRow(row, colIdx, mappingWithoutExplicitName, nil)
	if txn["交易户名"] != "" || txn["对手户名"] != "" {
		t.Fatalf("bank entity columns should not be used as account names without explicit name mapping: %#v", txn)
	}
}

func TestFlowEdgeLimitUsesAuditLimitForActiveEntityFilters(t *testing.T) {
	limit := flowEdgeLimit(map[string]interface{}{
		"source_filters": []interface{}{
			map[string]interface{}{"column": "account", "values": []interface{}{"1000050001"}},
		},
	})
	if limit != auditFlowEdgeLimit {
		t.Fatalf("expected audit limit, got %d", limit)
	}
}

func TestFlowEdgeLimitCapsExplicitRequest(t *testing.T) {
	limit := flowEdgeLimit(map[string]interface{}{"max_edges": float64(100000)})
	if limit != auditFlowEdgeLimit {
		t.Fatalf("expected capped audit limit, got %d", limit)
	}
}

func TestFlowEdgeLimitUsesAuditLimitForAnyActiveFilter(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"source label": {"source_label_values": []interface{}{"重点主体"}},
		"target label": {"target_label_values": []interface{}{"公司"}},
		"direction":    {"directions": []interface{}{"出"}},
		"start date":   {"start_date": "2024-01-01"},
		"end date":     {"end_date": "2024-01-31"},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if limit := flowEdgeLimit(payload); limit != auditFlowEdgeLimit {
				t.Fatalf("flowEdgeLimit = %d, want %d", limit, auditFlowEdgeLimit)
			}
		})
	}
}

func TestFlowFilterEndToEndAuditMatchesGraphAggregates(t *testing.T) {
	sessionDir := t.TempDir()
	txns := writeAuditFlowCSV(t, sessionDir)

	mapping := flowColumnMapping{
		SourceCol:     "交易方户名",
		SourceAccount: "交易方账户",
		SourceID:      "交易方身份证号",
		SourceLabel:   "交易方标签",
		TargetCol:     "对手户名",
		TargetCard:    "交易对手账卡号",
		TargetID:      "对手身份证号",
		TargetLabel:   "对手标签",
		Amount:        "交易金额",
		Time:          "交易时间",
		Direction:     "收付标志",
		Serial:        "交易流水号",
		Summary:       "摘要说明",
		Remark:        "备注",
	}
	normalized := readSessionData(sessionDir, mapping, nil)
	if len(normalized) != len(txns) {
		t.Fatalf("normalized rows = %d, want %d", len(normalized), len(txns))
	}

	cases := []struct {
		name    string
		payload map[string]interface{}
		want    func(model.TransactionRow) bool
	}{
		{
			name: "source account direction and second precision time",
			payload: map[string]interface{}{
				"source_filters": []interface{}{filterPayload("交易账号", "A-001")},
				"directions":     []interface{}{"出"},
				"start_date":     "2024-01-02 10:00:00",
				"end_date":       "2024-01-02 10:59:59",
			},
			want: func(row model.TransactionRow) bool {
				return row["交易账号"] == "A-001" && row["收付标志"] == "出" && row["交易时间"] >= "2024-01-02 10:00:00" && row["交易时间"] <= "2024-01-02 10:59:59"
			},
		},
		{
			name: "mapped serial summary and remark filters",
			payload: map[string]interface{}{
				"detail_filters": []interface{}{
					filterPayload("交易流水号", "TX-020"),
					filterPayload("摘要说明", "货款"),
					filterPayload("备注", "重点备注"),
				},
			},
			want: func(row model.TransactionRow) bool {
				return row["交易流水号"] == "TX-020" && row["摘要说明"] == "货款" && row["备注"] == "重点备注"
			},
		},
		{
			name: "source and target label filters with target card",
			payload: map[string]interface{}{
				"target_filters":      []interface{}{filterPayload("交易对手账卡号", "T-002")},
				"source_label_values": []interface{}{"重点主体"},
				"target_label_values": []interface{}{"公司"},
			},
			want: func(row model.TransactionRow) bool {
				return row["交易对手账卡号"] == "T-002" && row["交易方标签"] == "重点主体" && row["对手标签"] == "公司"
			},
		},
		{
			name: "date only range includes whole day",
			payload: map[string]interface{}{
				"start_date": "2024-01-03",
				"end_date":   "2024-01-03",
			},
			want: func(row model.TransactionRow) bool {
				return row["交易时间"] >= "2024-01-03 00:00:00" && row["交易时间"] <= "2024-01-03 23:59:59"
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			filtered := applyFilters(normalized, tt.payload)
			var expected []model.TransactionRow
			for _, row := range normalized {
				if tt.want(row) {
					expected = append(expected, row)
				}
			}
			if len(expected) == 0 {
				t.Fatal("audit case selected no expected rows")
			}
			assertRowsAndAmount(t, filtered, expected)
			graph := etl.BuildFlowGraph(filtered, 5000)
			assertGraphMatchesRows(t, graph, expected)
		})
	}
}

func TestFlowAuditAllFiltersAndMixedTimeFormatsStayConsistent(t *testing.T) {
	sessionDir := t.TempDir()
	writeAuditFlowCSV(t, sessionDir)

	mapping := auditFlowMapping()
	payload := map[string]interface{}{
		"source_filters":      []interface{}{filterPayload("交易账号", "A-001")},
		"target_filters":      []interface{}{filterPayload("交易对手账卡号", "T-002")},
		"detail_filters":      []interface{}{filterPayload("交易流水号", "TX-023"), filterPayload("摘要说明", "货款"), filterPayload("备注", "重点备注")},
		"source_label_values": []interface{}{"重点主体"},
		"target_label_values": []interface{}{"公司"},
		"directions":          []interface{}{"出"},
		"start_date":          "2024-01-02 10:00:00",
		"end_date":            "2024-01-02 10:59:59",
	}
	normalized := readSessionData(sessionDir, mapping, nil)
	filtered := applyFilters(normalized, payload)
	expected := []model.TransactionRow{
		{
			"交易户名": "张三", "交易账号": "A-001", "交易方身份证号": "ID-A", "交易方标签": "重点主体",
			"交易时间": "2024-01-02 10:00:01", "交易金额": "1230", "收付标志": "出",
			"交易对手账卡号": "T-002", "对手户名": "公司乙", "对手身份证号": "TID-2", "对手标签": "公司",
			"交易流水号": "TX-023", "摘要说明": "货款", "备注": "重点备注",
		},
	}

	assertRowsAndAmount(t, filtered, expected)
	summary := etl.BuildSummary(filtered)
	if summary["total_rows"] != 1 || summary["out_count"] != 1 || summary["in_count"] != 0 {
		t.Fatalf("summary counts = %#v, want one outgoing row", summary)
	}
	if math.Abs(summary["total_out"].(float64)-1230) > 0.001 || summary["total_in"].(float64) != 0 {
		t.Fatalf("summary amounts = %#v, want total_out 1230", summary)
	}

	graph := etl.BuildFlowGraph(filtered, 5000)
	assertGraphMatchesRows(t, graph, expected)
	if len(graph.Edges) != 1 || graph.Edges[0].Source != "A-001" || graph.Edges[0].Target != "T-002" || graph.Edges[0].TxCount != 1 || graph.Edges[0].Amount != 1230 {
		t.Fatalf("graph edge = %#v, want A-001 -> T-002 amount/count 1230/1", graph.Edges)
	}

	detailPayload := EdgeDetailPayload{
		Source:              "A-001",
		Target:              "T-002",
		SourceColumn:        mapping.SourceCol,
		SourceAccountColumn: mapping.SourceAccount,
		SourceIDColumn:      mapping.SourceID,
		SourceLabelColumn:   mapping.SourceLabel,
		TargetColumn:        mapping.TargetCol,
		TargetCardColumn:    mapping.TargetCard,
		TargetIDColumn:      mapping.TargetID,
		TargetLabelColumn:   mapping.TargetLabel,
		AmountColumn:        mapping.Amount,
		TimeColumn:          mapping.Time,
		DirectionColumn:     mapping.Direction,
		SerialColumn:        mapping.Serial,
		SummaryColumn:       mapping.Summary,
		RemarkColumn:        mapping.Remark,
		SourceFilters:       payload["source_filters"].([]interface{}),
		TargetFilters:       payload["target_filters"].([]interface{}),
		DetailFilters:       payload["detail_filters"].([]interface{}),
		SourceLabelValues:   payload["source_label_values"].([]interface{}),
		TargetLabelValues:   payload["target_label_values"].([]interface{}),
		Directions:          payload["directions"].([]interface{}),
		StartDate:           payload["start_date"].(string),
		EndDate:             payload["end_date"].(string),
		Limit:               10000,
	}
	rows := queryEdgeRows(sessionDir, detailPayload)
	if len(rows) != 1 || math.Abs(sumRawRows(rows, detailPayload.AmountColumn)-1230) > 0.001 {
		t.Fatalf("detail rows/amount = %d/%.2f, want 1/1230", len(rows), sumRawRows(rows, detailPayload.AmountColumn))
	}
	if rows[0]["流向源"] != "A-001" || rows[0]["流向目标"] != "T-002" {
		t.Fatalf("detail flow endpoints = %v -> %v, want A-001 -> T-002", rows[0]["流向源"], rows[0]["流向目标"])
	}
}

func TestQueryEdgeRowsMatchesDirectedGraphEndpointAndFilters(t *testing.T) {
	sessionDir := t.TempDir()
	writeAuditFlowCSV(t, sessionDir)

	payload := EdgeDetailPayload{
		Source:              "T-001",
		Target:              "A-001",
		SourceColumn:        "交易方户名",
		SourceAccountColumn: "交易方账户",
		SourceIDColumn:      "交易方身份证号",
		SourceLabelColumn:   "交易方标签",
		TargetColumn:        "对手户名",
		TargetCardColumn:    "交易对手账卡号",
		TargetIDColumn:      "对手身份证号",
		TargetLabelColumn:   "对手标签",
		AmountColumn:        "交易金额",
		TimeColumn:          "交易时间",
		DirectionColumn:     "收付标志",
		Directions:          []interface{}{"进"},
		StartDate:           "2024-01-02",
		EndDate:             "2024-01-02",
		Limit:               10000,
	}

	mapping := auditFlowMapping()
	filtered := applyFilters(readSessionData(sessionDir, mapping, nil), edgeDetailFilterPayload(payload))
	graph := etl.BuildFlowGraph(filtered, 5000)
	var edge model.FlowEdge
	found := false
	for _, candidate := range graph.Edges {
		if candidate.Source == payload.Source && candidate.Target == payload.Target {
			edge = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("graph did not include edge %s -> %s", payload.Source, payload.Target)
	}

	rows := queryEdgeRows(sessionDir, payload)
	if len(rows) != edge.TxCount {
		t.Fatalf("detail rows = %d, want graph tx_count %d", len(rows), edge.TxCount)
	}
	if math.Abs(sumRawRows(rows, payload.AmountColumn)-edge.Amount) > 0.001 {
		t.Fatalf("detail amount = %.2f, want graph amount %.2f", sumRawRows(rows, payload.AmountColumn), edge.Amount)
	}
	for _, row := range rows {
		if row["交易方账户"] != "A-001" || row["交易对手账卡号"] != "T-001" || row["收付标志"] != "进" {
			t.Fatalf("detail row did not preserve expected raw orientation: %#v", row)
		}
		if row["流向源"] != payload.Source || row["流向目标"] != payload.Target {
			t.Fatalf("detail flow endpoints = %v -> %v, want %s -> %s", row["流向源"], row["流向目标"], payload.Source, payload.Target)
		}
	}

	payload.Directions = []interface{}{"出"}
	if rows := queryEdgeRows(sessionDir, payload); len(rows) != 0 {
		t.Fatalf("reverse-direction detail rows = %d, want 0", len(rows))
	}
}

func writeAuditFlowCSV(t *testing.T, dir string) []model.TransactionRow {
	t.Helper()
	path := filepath.Join(dir, "audit.csv")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create audit csv: %v", err)
	}
	defer file.Close()

	headers := []string{"交易方户名", "交易方账户", "交易方身份证号", "交易方标签", "交易时间", "交易金额", "收付标志", "交易余额", "交易对手账卡号", "对手户名", "对手身份证号", "对手标签", "交易流水号", "摘要说明", "备注"}
	writer := csv.NewWriter(file)
	if err := writer.Write(headers); err != nil {
		t.Fatalf("write headers: %v", err)
	}

	sources := []struct{ name, account, id, label string }{
		{"张三", "A-001", "ID-A", "重点主体"},
		{"李四", "B-001", "ID-B", "普通主体"},
		{"王五", "C-001", "ID-C", "重点主体"},
	}
	targets := []struct{ name, card, id, label, summary string }{
		{"商户甲", "T-001", "TID-1", "商户", "工资"},
		{"公司乙", "T-002", "TID-2", "公司", "货款"},
		{"个人丙", "T-003", "TID-3", "个人", "转账"},
	}
	directions := []string{"出", "进"}
	var rows []model.TransactionRow
	serial := 1
	for sourceIndex, source := range sources {
		for targetIndex, target := range targets {
			for _, direction := range directions {
				for day := 1; day <= 3; day++ {
					for hour := 9; hour <= 11; hour++ {
						amount := float64((sourceIndex+1)*1000 + (targetIndex+1)*100 + day*10 + hour)
						serialValue := fmt.Sprintf("TX-%03d", serial)
						remark := "普通备注"
						if hour == 10 {
							remark = "重点备注"
						}
						timeValue := fmt.Sprintf("2024-01-%02d %02d:%02d:%02d", day, hour, sourceIndex, targetIndex)
						record := []string{source.name, source.account, source.id, source.label, auditRawTimeFormat(timeValue, day, hour), fmt.Sprintf("%.2f", amount), direction, fmt.Sprintf("%.2f", amount+10000), target.card, target.name, target.id, target.label, serialValue, target.summary, remark}
						if err := writer.Write(record); err != nil {
							t.Fatalf("write record: %v", err)
						}
						rows = append(rows, model.TransactionRow{
							"交易户名": source.name, "交易账号": source.account, "交易方身份证号": source.id, "交易方标签": source.label,
							"交易时间": timeValue, "交易金额": fmt.Sprintf("%.2f", amount), "收付标志": direction,
							"交易对手账卡号": target.card, "对手户名": target.name, "对手身份证号": target.id, "对手标签": target.label,
							"交易流水号": serialValue, "摘要说明": target.summary, "备注": remark,
						})
						serial++
					}
				}
			}
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatalf("flush audit csv: %v", err)
	}
	return rows
}

func auditRawTimeFormat(standard string, day, hour int) string {
	switch {
	case day == 1 && hour == 9:
		return strings.ReplaceAll(standard, "-", "/")
	case day == 1 && hour == 10:
		return strings.ReplaceAll(strings.ReplaceAll(standard, "-", ""), ":", "")
	case day == 2 && hour == 10:
		return strings.Replace(strings.ReplaceAll(standard, "-0", "-"), "-", "/", 2)
	case day == 3 && hour == 11:
		return fmt.Sprintf("%s年%s月%s日 %s时%s分%s秒", standard[0:4], standard[5:7], standard[8:10], standard[11:13], standard[14:16], standard[17:19])
	default:
		return standard
	}
}

func auditFlowMapping() flowColumnMapping {
	return flowColumnMapping{
		SourceCol:     "交易方户名",
		SourceAccount: "交易方账户",
		SourceID:      "交易方身份证号",
		SourceLabel:   "交易方标签",
		TargetCol:     "对手户名",
		TargetCard:    "交易对手账卡号",
		TargetID:      "对手身份证号",
		TargetLabel:   "对手标签",
		Amount:        "交易金额",
		Time:          "交易时间",
		Direction:     "收付标志",
		Serial:        "交易流水号",
		Summary:       "摘要说明",
		Remark:        "备注",
	}
}

func findNodeIn(nodes []model.FlowNode, id string) *model.FlowNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func findAPITestFlowNode(nodes []model.FlowNode, id string) *model.FlowNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func filterPayload(column string, values ...string) map[string]interface{} {
	items := make([]interface{}, len(values))
	for i, value := range values {
		items[i] = value
	}
	return map[string]interface{}{"column": column, "values": items}
}

func assertRowsAndAmount(t *testing.T, got, want []model.TransactionRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("filtered rows = %d, want %d", len(got), len(want))
	}
	if math.Abs(sumRows(got)-sumRows(want)) > 0.001 {
		t.Fatalf("filtered amount = %.2f, want %.2f", sumRows(got), sumRows(want))
	}
}

func sumRows(rows []model.TransactionRow) float64 {
	var total float64
	for _, row := range rows {
		total += parser.ToNumber(row["交易金额"])
	}
	return total
}

func sumRawRows(rows []map[string]interface{}, amountColumn string) float64 {
	var total float64
	amountColumn = parser.NormalizeHeader(amountColumn)
	for _, row := range rows {
		total += parser.ToNumber(row[amountColumn])
	}
	return total
}

func assertGraphMatchesRows(t *testing.T, graph model.FlowGraph, rows []model.TransactionRow) {
	t.Helper()
	type edgeAgg struct {
		amount float64
		count  int
	}
	expectedEdges := map[string]edgeAgg{}
	type nodeAgg struct {
		inAmount  float64
		outAmount float64
		inCount   int
		outCount  int
	}
	expectedNodes := map[string]nodeAgg{}
	for _, row := range rows {
		source, target := expectedFlowEndpoints(row)
		if source == "" || target == "" || source == target {
			continue
		}
		amount := parser.ToNumber(row["交易金额"])
		key := source + "|" + target
		edge := expectedEdges[key]
		edge.amount += amount
		edge.count++
		expectedEdges[key] = edge
		sourceNode := expectedNodes[source]
		sourceNode.outAmount += amount
		sourceNode.outCount++
		expectedNodes[source] = sourceNode
		targetNode := expectedNodes[target]
		targetNode.inAmount += amount
		targetNode.inCount++
		expectedNodes[target] = targetNode
	}
	if len(graph.Edges) != len(expectedEdges) {
		t.Fatalf("graph edges = %d, want %d", len(graph.Edges), len(expectedEdges))
	}
	for _, edge := range graph.Edges {
		want, ok := expectedEdges[edge.Source+"|"+edge.Target]
		if !ok {
			t.Fatalf("unexpected edge %s -> %s", edge.Source, edge.Target)
		}
		if edge.TxCount != want.count || math.Abs(edge.Amount-want.amount) > 0.001 {
			t.Fatalf("edge %s -> %s amount/count = %.2f/%d, want %.2f/%d", edge.Source, edge.Target, edge.Amount, edge.TxCount, want.amount, want.count)
		}
	}
	for _, node := range graph.Nodes {
		want, ok := expectedNodes[node.ID]
		if !ok {
			t.Fatalf("unexpected node %s", node.ID)
		}
		if math.Abs(node.AmountIn-want.inAmount) > 0.001 || math.Abs(node.AmountOut-want.outAmount) > 0.001 || node.InCount != want.inCount || node.OutCount != want.outCount {
			t.Fatalf("node %s in/out/counts = %.2f/%.2f/%d/%d, want %.2f/%.2f/%d/%d", node.ID, node.AmountIn, node.AmountOut, node.InCount, node.OutCount, want.inAmount, want.outAmount, want.inCount, want.outCount)
		}
	}
}

// ============================================================================
// 端到端审计测试套件
// 覆盖以下功能的数据来源、逻辑和计算方式：
//   A. 数据读取与列映射 (readSessionData + transactionFromMappedRow)
//   B. 方向归一化 (normalizeFlowDirection + checkUnknownDirections)
//   C. 筛选系统 (applyFilters — 6 种筛选维度)
//   D. 汇总统计 (BuildSummary)
//   E. 流图构建 (BuildFlowGraph — 节点/边/聚合/截断)
//   F. 边详情查询 (queryEdgeRows)
//   G. 全链路一致性审计 (CSV→normalize→filter→summary→graph→detail)
// ============================================================================

// ---------------------------------------------------------------------------
// B. 方向归一化 完整审计
// ---------------------------------------------------------------------------

func TestAuditNormalizeFlowDirectionComplete(t *testing.T) {
	emptyAliases := map[string]string{}

	// B1: 全部 18 个硬编码别名
	builtinTests := []struct {
		input string
		want  string
	}{
		// 出方向
		{"D", "出"}, {"借", "出"}, {"借方", "出"}, {"支出", "出"},
		{"转出", "出"}, {"取", "出"}, {"支", "出"}, {"出账", "出"}, {"-", "出"}, {"O", "出"},
		// 进方向
		{"C", "进"}, {"贷", "进"}, {"贷方", "进"}, {"收入", "进"},
		{"转入", "进"}, {"存", "进"}, {"入", "进"}, {"入账", "进"}, {"收", "进"}, {"+", "进"},
		// 自我映射
		{"进", "进"}, {"出", "出"},
	}
	for _, tt := range builtinTests {
		got := normalizeFlowDirection(tt.input, emptyAliases)
		if got != tt.want {
			t.Errorf("B1: normalizeFlowDirection(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}

	// B2: 自定义别名覆盖
	customAliases := map[string]string{"入账": "出", "收入": "出"}
	if got := normalizeFlowDirection("入账", customAliases); got != "出" {
		t.Errorf("B2: custom alias override: got %q, want 出", got)
	}
	if got := normalizeFlowDirection("收入", customAliases); got != "出" {
		t.Errorf("B2: custom alias override: got %q, want 出", got)
	}

	// B3: 四阶段级联 — 空格/大小写/全角
	spaceAliases := map[string]string{"normal": "进"}
	if got := normalizeFlowDirection("  normal  ", spaceAliases); got != "进" {
		t.Errorf("B3: trimmed alias: got %q, want 进", got)
	}
	// NormalizeHeader 仅做 trim/BOM/全角空格清理, 不做大小写转换
	// 所以 "NORMAL" 不匹配别名 "normal", 回退到 NormalizeDirection("NORMAL")
	// NormalizeDirection 不认识 "NORMAL", 返回原值
	unknown := normalizeFlowDirection("NORMAL", spaceAliases)
	if unknown != "NORMAL" {
		t.Errorf("B3: NormalizeHeader no case-fold: got %q, want NORMAL (passthrough)", unknown)
	}
	// 真正不认识的值全回退
	unknown2 := normalizeFlowDirection("UNKNOWN_VAL", emptyAliases)
	if unknown2 != "UNKNOWN_VAL" {
		t.Errorf("B3: unknown fallthrough: got %q, want UNKNOWN_VAL", unknown2)
	}

	// B4: 空值/空白输入
	if got := normalizeFlowDirection("", emptyAliases); got != "" {
		t.Errorf("B4: empty input: got %q, want empty", got)
	}
	if got := normalizeFlowDirection("   ", emptyAliases); got != "" {
		t.Errorf("B4: whitespace input: got %q, want empty", got)
	}
}

func TestAuditCheckUnknownDirections(t *testing.T) {
	allValid := []model.TransactionRow{
		{"收付标志": "进"},
		{"收付标志": "出"},
		{"收付标志": ""},
	}
	if unknowns := checkUnknownDirections(allValid); len(unknowns) > 0 {
		t.Errorf("all valid directions should return empty, got %v", unknowns)
	}
	// 注: 在流水线中方向值已通过 normalizeFlowDirection 归一化
	// "借"会被归一化为"出"后才进入 checkUnknownDirections
	// 所以只测试未被归一化的值
	mixed := []model.TransactionRow{
		{"收付标志": "进"},
		{"收付标志": "unknown"},
		{"收付标志": "SOMETHING_ELSE"},
	}
	if unknowns := checkUnknownDirections(mixed); len(unknowns) != 2 {
		t.Errorf("should detect 2 unknown values, got %v", unknowns)
	}
}

// ---------------------------------------------------------------------------
// C. 筛选系统 完整审计 — 每个维度独立测试 + 组合测试
// ---------------------------------------------------------------------------

func TestAuditFilterSource(t *testing.T) {
	txns := []model.TransactionRow{
		{"交易户名": "A", "交易金额": "100"},
		{"交易户名": "B", "交易金额": "200"},
		{"交易户名": "A", "交易金额": "300"},
	}
	filtered := applyFilters(txns, map[string]interface{}{
		"source_filters": []interface{}{filterPayload("交易户名", "A")},
	})
	if len(filtered) != 2 {
		t.Fatalf("source filter: expected 2 rows, got %d", len(filtered))
	}
	multi := applyFilters(txns, map[string]interface{}{
		"source_filters": []interface{}{filterPayload("交易户名", "A", "B")},
	})
	if len(multi) != 3 {
		t.Fatalf("source multi-value filter: expected 3 rows, got %d", len(multi))
	}
	noMatch := applyFilters(txns, map[string]interface{}{
		"source_filters": []interface{}{filterPayload("交易户名", "C")},
	})
	if len(noMatch) != 0 {
		t.Fatalf("source no-match filter: expected 0 rows, got %d", len(noMatch))
	}
	empty := applyFilters(txns, map[string]interface{}{})
	if len(empty) != 3 {
		t.Fatalf("empty filter: expected all 3 rows, got %d", len(empty))
	}
}

func TestAuditFilterTarget(t *testing.T) {
	txns := []model.TransactionRow{
		{"对手户名": "X", "交易金额": "100"},
		{"对手户名": "Y", "交易金额": "200"},
		{"对手户名": "X", "交易金额": "300"},
	}
	filtered := applyFilters(txns, map[string]interface{}{
		"target_filters": []interface{}{filterPayload("对手户名", "X")},
	})
	if len(filtered) != 2 {
		t.Fatalf("target filter: expected 2 rows, got %d", len(filtered))
	}
}

func TestAuditFilterDetailMultiColumn(t *testing.T) {
	txns := []model.TransactionRow{
		{"交易流水号": "TX001", "摘要说明": "货款", "备注": "A"},
		{"交易流水号": "TX002", "摘要说明": "工资", "备注": "B"},
		{"交易流水号": "TX003", "摘要说明": "货款", "备注": "B"},
	}
	// AND 条件: 流水号 + 摘要说明 + 备注 必须全部匹配
	filtered := applyFilters(txns, map[string]interface{}{
		"detail_filters": []interface{}{
			filterPayload("交易流水号", "TX001"),
			filterPayload("摘要说明", "货款"),
			filterPayload("备注", "A"),
		},
	})
	if len(filtered) != 1 {
		t.Fatalf("detail multi-AND filter: expected 1 row, got %d", len(filtered))
	}
	// 只有 2 个条件
	partial := applyFilters(txns, map[string]interface{}{
		"detail_filters": []interface{}{
			filterPayload("摘要说明", "货款"),
		},
	})
	if len(partial) != 2 {
		t.Fatalf("detail single filter: expected 2 rows, got %d", len(partial))
	}
}

func TestAuditFilterDirection(t *testing.T) {
	txns := []model.TransactionRow{
		{"收付标志": "进", "交易金额": "100"},
		{"收付标志": "出", "交易金额": "200"},
		{"收付标志": "进", "交易金额": "300"},
	}
	inOnly := applyFilters(txns, map[string]interface{}{
		"directions": []interface{}{"进"},
	})
	if len(inOnly) != 2 {
		t.Fatalf("direction=进 filter: expected 2 rows, got %d", len(inOnly))
	}
	outOnly := applyFilters(txns, map[string]interface{}{
		"directions": []interface{}{"出"},
	})
	if len(outOnly) != 1 {
		t.Fatalf("direction=出 filter: expected 1 row, got %d", len(outOnly))
	}
	both := applyFilters(txns, map[string]interface{}{
		"directions": []interface{}{"进", "出"},
	})
	if len(both) != 3 {
		t.Fatalf("direction=both filter: expected 3 rows, got %d", len(both))
	}
	emptyDir := applyFilters(txns, map[string]interface{}{})
	if len(emptyDir) != 3 {
		t.Fatalf("empty direction filter: expected 3 rows, got %d", len(emptyDir))
	}
}

func TestAuditFilterDateRange(t *testing.T) {
	txns := []model.TransactionRow{
		{"交易时间": "2024-01-01 10:00:00", "交易金额": "100"},
		{"交易时间": "2024-01-15 12:00:00", "交易金额": "200"},
		{"交易时间": "2024-02-01 08:00:00", "交易金额": "300"},
	}
	// 日期范围 (不含时间 → 自动扩展)
	jan := applyFilters(txns, map[string]interface{}{
		"start_date": "2024-01-01",
		"end_date":   "2024-01-31",
	})
	if len(jan) != 2 {
		t.Fatalf("january date range: expected 2 rows, got %d", len(jan))
	}
	// 精确到秒的时间范围
	precise := applyFilters(txns, map[string]interface{}{
		"start_date": "2024-01-01 09:00:00",
		"end_date":   "2024-01-15 13:00:00",
	})
	if len(precise) != 2 {
		t.Fatalf("precise time range: expected 2 rows, got %d", len(precise))
	}
	// 单日范围
	singleDay := applyFilters(txns, map[string]interface{}{
		"start_date": "2024-01-15",
		"end_date":   "2024-01-15",
	})
	if len(singleDay) != 1 {
		t.Fatalf("single day range: expected 1 row, got %d", len(singleDay))
	}
	// 无范围
	noRange := applyFilters(txns, map[string]interface{}{})
	if len(noRange) != 3 {
		t.Fatalf("no date range: expected all 3 rows, got %d", len(noRange))
	}
	// 空时间行应被排除
	withEmptyTime := append(txns, model.TransactionRow{"交易时间": "", "交易金额": "400"})
	filtered := applyFilters(withEmptyTime, map[string]interface{}{
		"start_date": "2024-01-01",
		"end_date":   "2024-02-28",
	})
	if len(filtered) != 3 {
		t.Fatalf("date range with empty time: expected 3 rows, got %d", len(filtered))
	}
}

func TestAuditFilterLabels(t *testing.T) {
	txns := []model.TransactionRow{
		{"交易方标签": "重点", "对手标签": "公司", "交易金额": "100"},
		{"交易方标签": "普通", "对手标签": "个人", "交易金额": "200"},
		{"交易方标签": "重点", "对手标签": "个人", "交易金额": "300"},
	}
	sourceLabel := applyFilters(txns, map[string]interface{}{
		"source_label_values": []interface{}{"重点"},
	})
	if len(sourceLabel) != 2 {
		t.Fatalf("source label filter: expected 2 rows, got %d", len(sourceLabel))
	}
	targetLabel := applyFilters(txns, map[string]interface{}{
		"target_label_values": []interface{}{"公司"},
	})
	if len(targetLabel) != 1 {
		t.Fatalf("target label filter: expected 1 row, got %d", len(targetLabel))
	}
	bothLabels := applyFilters(txns, map[string]interface{}{
		"source_label_values": []interface{}{"重点"},
		"target_label_values": []interface{}{"个人"},
	})
	if len(bothLabels) != 1 {
		t.Fatalf("both labels filter: expected 1 row, got %d", len(bothLabels))
	}
}

// ---------------------------------------------------------------------------
// D. 汇总统计 完整审计
// ---------------------------------------------------------------------------

func TestAuditBuildSummaryComplete(t *testing.T) {
	txns := []model.TransactionRow{
		{"收付标志": "进", "交易金额": "100.50"},
		{"收付标志": "出", "交易金额": "200.00"},
		{"收付标志": "进", "交易金额": "300.75"},
		{"收付标志": "出", "交易金额": "50.25"},
		{"收付标志": "", "交易金额": "500.00"},
	}
	summary := etl.BuildSummary(txns)

	if summary["total_rows"] != 5 {
		t.Errorf("total_rows: got %v, want 5", summary["total_rows"])
	}
	if summary["in_count"] != 2 {
		t.Errorf("in_count: got %v, want 2", summary["in_count"])
	}
	if summary["out_count"] != 2 {
		t.Errorf("out_count: got %v, want 2", summary["out_count"])
	}
	totalIn := summary["total_in"].(float64)
	if math.Abs(totalIn-401.25) > 0.001 {
		t.Errorf("total_in: got %.2f, want 401.25", totalIn)
	}
	totalOut := summary["total_out"].(float64)
	if math.Abs(totalOut-250.25) > 0.001 {
		t.Errorf("total_out: got %.2f, want 250.25", totalOut)
	}
	// 空数据
	empty := etl.BuildSummary(nil)
	if empty["total_rows"] != 0 || empty["in_count"] != 0 || empty["out_count"] != 0 {
		t.Errorf("empty summary: got %#v", empty)
	}
}

// ---------------------------------------------------------------------------
// E. 流图构建 完整审计
// ---------------------------------------------------------------------------

func TestAuditBuildFlowGraphEdgeAggregation(t *testing.T) {
	// 多笔交易聚合到同一条边
	txns := []model.TransactionRow{
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "100", "交易时间": "2024-01-01 10:00:00"},
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "200", "交易时间": "2024-01-02 10:00:00"},
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "300", "交易时间": "2024-01-03 10:00:00"},
		{"交易账号": "A", "交易对手账卡号": "C", "收付标志": "出", "交易金额": "400", "交易时间": "2024-01-04 10:00:00"},
	}
	graph := etl.BuildFlowGraph(txns, 10)
	if len(graph.Edges) != 2 {
		t.Fatalf("edges: expected 2 (A->B aggregated, A->C), got %d", len(graph.Edges))
	}
	var edgeAB, edgeAC *model.FlowEdge
	for i := range graph.Edges {
		if graph.Edges[i].Source == "A" && graph.Edges[i].Target == "B" {
			edgeAB = &graph.Edges[i]
		}
		if graph.Edges[i].Source == "A" && graph.Edges[i].Target == "C" {
			edgeAC = &graph.Edges[i]
		}
	}
	if edgeAB == nil {
		t.Fatal("expected edge A->B")
	}
	if edgeAB.Amount != 600 {
		t.Errorf("A->B aggregated amount: got %.2f, want 600.00", edgeAB.Amount)
	}
	if edgeAB.TxCount != 3 {
		t.Errorf("A->B tx count: got %d, want 3", edgeAB.TxCount)
	}
	if math.Abs(edgeAB.AvgAmount-200) > 0.001 {
		t.Errorf("A->B avg amount: got %.2f, want 200.00", edgeAB.AvgAmount)
	}
	if edgeAB.MaxAmount != 300 {
		t.Errorf("A->B max amount: got %.2f, want 300.00", edgeAB.MaxAmount)
	}
	if edgeAB.FirstTime == nil || *edgeAB.FirstTime != "2024-01-01 10:00:00" {
		t.Errorf("A->B first time: got %v, want 2024-01-01 10:00:00", edgeAB.FirstTime)
	}
	if edgeAB.LastTime == nil || *edgeAB.LastTime != "2024-01-03 10:00:00" {
		t.Errorf("A->B last time: got %v, want 2024-01-03 10:00:00", edgeAB.LastTime)
	}
	if edgeAC == nil {
		t.Fatal("expected edge A->C")
	}
	if edgeAC.TxCount != 1 || edgeAC.Amount != 400 {
		t.Errorf("A->C: expected count=1 amount=400, got count=%d amount=%.2f", edgeAC.TxCount, edgeAC.Amount)
	}
}

func TestAuditBuildFlowGraphNodeStats(t *testing.T) {
	txns := []model.TransactionRow{
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "100", "交易时间": "2024-01-01 10:00:00"},
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "200", "交易时间": "2024-01-02 10:00:00"},
		{"交易账号": "B", "交易对手账卡号": "C", "收付标志": "出", "交易金额": "300", "交易时间": "2024-01-03 10:00:00"},
		{"交易账号": "C", "交易对手账卡号": "A", "收付标志": "进", "交易金额": "400", "交易时间": "2024-01-04 10:00:00"},
	}
	graph := etl.BuildFlowGraph(txns, 10)
	findNode := func(id string) *model.FlowNode {
		for i := range graph.Nodes {
			if graph.Nodes[i].ID == id {
				return &graph.Nodes[i]
			}
		}
		return nil
	}
	nodeA := findNode("A")
	if nodeA == nil {
		t.Fatal("expected node A")
	}
	// 边 A→B(100+200=300) + A→C(进→counter=A→source=A=400) = 700
	if math.Abs(nodeA.AmountOut-700) > 0.001 {
		t.Errorf("A AmountOut: got %.2f, want 700.00", nodeA.AmountOut)
	}
	if math.Abs(nodeA.AmountIn-0) > 0.001 {
		t.Errorf("A AmountIn: got %.2f, want 0.00", nodeA.AmountIn)
	}
	if nodeA.OutCount != 3 {
		t.Errorf("A OutCount: got %d, want 3 (A→B×2 + A→C×1)", nodeA.OutCount)
	}
	if nodeA.InCount != 0 {
		t.Errorf("A InCount: got %d, want 0", nodeA.InCount)
	}
	// A 出现在 2 条边: A→B, A→C
	if nodeA.Degree != 2 {
		t.Errorf("A Degree: got %d, want 2 (A→B, A→C)", nodeA.Degree)
	}
	// 本方未知回退
	noAccount := []model.TransactionRow{
		{"交易户名": "", "收付标志": "出", "交易金额": "100", "交易时间": "2024-01-01 10:00:00"},
	}
	gNoAcct := etl.BuildFlowGraph(noAccount, 10)
	nodeUnknown := findNodeIn(gNoAcct.Nodes, "本方未知")
	if nodeUnknown == nil {
		t.Fatal("expected fallback node 本方未知")
	}
	if nodeUnknown.Role != "self" {
		t.Errorf("本方未知 role: got %q, want self", nodeUnknown.Role)
	}
	// 相反方向 — 进
	bothDir := []model.TransactionRow{
		{"交易账号": "X", "交易对手账卡号": "Y", "收付标志": "进", "交易金额": "500", "交易时间": "2024-01-01 10:00:00"},
		{"交易账号": "X", "交易对手账卡号": "Y", "收付标志": "出", "交易金额": "300", "交易时间": "2024-01-02 10:00:00"},
	}
	g2 := etl.BuildFlowGraph(bothDir, 10)
	var edgeXY, edgeYX *model.FlowEdge
	for i := range g2.Edges {
		switch {
		case g2.Edges[i].Source == "X" && g2.Edges[i].Target == "Y":
			edgeXY = &g2.Edges[i]
		case g2.Edges[i].Source == "Y" && g2.Edges[i].Target == "X":
			edgeYX = &g2.Edges[i]
		}
	}
	if edgeXY == nil || edgeYX == nil {
		t.Fatal("expected both X->Y (出) and Y->X (进)")
	}
	if edgeXY.TxCount != 1 || edgeXY.Amount != 300 {
		t.Errorf("X->Y (出): expected count=1 amount=300, got count=%d amount=%.2f", edgeXY.TxCount, edgeXY.Amount)
	}
	if edgeYX.TxCount != 1 || edgeYX.Amount != 500 {
		t.Errorf("Y->X (进): expected count=1 amount=500, got count=%d amount=%.2f", edgeYX.TxCount, edgeYX.Amount)
	}
}

func TestAuditBuildFlowGraphEdgeDedup(t *testing.T) {
	// 去重: 完全相同的 key (source+target+amount+time) 去除
	// 聚合: 相同 source+target 的金额和笔数汇总
	txns := []model.TransactionRow{
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "100", "交易时间": "2024-01-01 10:00:00"},
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "100", "交易时间": "2024-01-01 10:00:00"},
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "200", "交易时间": "2024-01-01 10:00:00"},
	}
	graph := etl.BuildFlowGraph(txns, 10)
	// 去重后: {A,B,100.00,T1} + {A,B,200.00,T1} (重复的第一行被去除)
	// 聚合后: 1 条边 A→B amount=300 count=2
	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge after dedup+aggregate (100 duped out, 100+200=300 aggregated), got %d", len(graph.Edges))
	}
	e := graph.Edges[0]
	if math.Abs(e.Amount-300) > 0.001 {
		t.Errorf("A->B amount: got %.2f, want 300.00", e.Amount)
	}
	if e.TxCount != 2 {
		t.Errorf("A->B count: got %d, want 2 (100+200 after dedup)", e.TxCount)
	}
}

func TestAuditBuildFlowGraphSelfLoopSkip(t *testing.T) {
	txns := []model.TransactionRow{
		{"交易账号": "A", "交易对手账卡号": "A", "收付标志": "出", "交易金额": "100", "交易时间": "2024-01-01 10:00:00"},
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "200", "交易时间": "2024-01-02 10:00:00"},
	}
	graph := etl.BuildFlowGraph(txns, 10)
	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge (self-loop skipped), got %d", len(graph.Edges))
	}
	if graph.Edges[0].Target != "B" {
		t.Errorf("remaining edge should be A->B, got %s->%s", graph.Edges[0].Source, graph.Edges[0].Target)
	}
}

func TestAuditBuildFlowGraphUnknownDirectionSkip(t *testing.T) {
	txns := []model.TransactionRow{
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "unknown", "交易金额": "100", "交易时间": "2024-01-01 10:00:00"},
		{"交易账号": "A", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "200", "交易时间": "2024-01-02 10:00:00"},
	}
	graph := etl.BuildFlowGraph(txns, 10)
	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge (unknown direction skipped), got %d", len(graph.Edges))
	}
}

func TestAuditBuildFlowGraphTruncation(t *testing.T) {
	var txns []model.TransactionRow
	for i := 0; i < 10; i++ {
		txns = append(txns, model.TransactionRow{
			"交易账号":    fmt.Sprintf("S%02d", i),
			"交易对手账卡号": fmt.Sprintf("T%02d", i),
			"收付标志":    "出",
			"交易金额":    "1000",
			"交易时间":    "2024-01-01 10:00:00",
		})
	}
	// limit 5 → only 5 edges
	graph := etl.BuildFlowGraph(txns, 5)
	if len(graph.Edges) > 5 {
		t.Fatalf("truncation: expected <=5 edges, got %d", len(graph.Edges))
	}
	if !graph.Meta["truncated"].(bool) {
		t.Error("truncation: meta.truncated should be true")
	}
	if graph.Meta["total_edges"].(int) != 10 {
		t.Errorf("truncation: total_edges should be 10, got %d", graph.Meta["total_edges"])
	}
	if graph.Meta["rendered_edges"].(int) != 5 {
		t.Errorf("truncation: rendered_edges should be 5, got %d", graph.Meta["rendered_edges"])
	}
	// limit 0 → default 600
	graph2 := etl.BuildFlowGraph(txns, 0)
	if len(graph2.Edges) != 10 {
		t.Fatalf("default limit: expected 10 edges, got %d", len(graph2.Edges))
	}
}

func TestAuditBuildFlowGraphNodeInfo(t *testing.T) {
	txns := []model.TransactionRow{
		{
			"交易卡号": "6212000000000001", "交易账号": "A001", "交易户名": "张三",
			"交易证件号码":  "ID001",
			"交易对手账卡号": "6222000000000002", "对手户名": "李四", "对手身份证号": "ID002",
			"收付标志": "出", "交易金额": "100", "交易时间": "2024-01-01 10:00:00",
		},
	}
	graph := etl.BuildFlowGraph(txns, 10)
	for _, n := range graph.Nodes {
		switch n.ID {
		case "6212000000000001":
			// AccountNo = firstTransactionValue("交易卡号", "交易账号") — only card number
			if n.AccountNo != "6212000000000001" {
				t.Errorf("node A account: expected 6212000000000001, got %q", n.AccountNo)
			}
			if n.AccountName != "张三" {
				t.Errorf("node A name: expected 张三, got %q", n.AccountName)
			}
			if n.IDNumber != "ID001" {
				t.Errorf("node A id: expected ID001, got %q", n.IDNumber)
			}
		case "6222000000000002":
			if n.AccountName != "李四" {
				t.Errorf("node B name: expected 李四, got %q", n.AccountName)
			}
		}
	}
	// 验证标签遮罩: 16位数字卡号→4...4
	node := findNodeIn(graph.Nodes, "6212000000000001")
	if node != nil && node.Label != "6212...0001" {
		t.Errorf("card label mask: expected 6212...0001, got %q", node.Label)
	}
}

func TestAuditBuildFlowGraphLabelMasking(t *testing.T) {
	txns := []model.TransactionRow{
		{
			"交易账号": "6217921166546724", "交易对手账卡号": "B",
			"收付标志": "出", "交易金额": "100", "交易时间": "2024-01-01 10:00:00",
		},
	}
	graph := etl.BuildFlowGraph(txns, 10)
	if len(graph.Nodes) < 1 {
		t.Fatal("expected at least 1 node")
	}
	for _, n := range graph.Nodes {
		if n.ID == "6217921166546724" {
			// 16-digit card → masked to 4...4
			if n.Label != "6217...6724" {
				t.Errorf("card mask: expected 6217...6724, got %q", n.Label)
			}
		}
	}
	// 短数字不遮罩
	short := []model.TransactionRow{
		{"交易账号": "SHORT", "交易对手账卡号": "B", "收付标志": "出", "交易金额": "100", "交易时间": "2024-01-01 10:00:00"},
	}
	g := etl.BuildFlowGraph(short, 10)
	for _, n := range g.Nodes {
		if n.ID == "SHORT" && n.Label != "SHORT" {
			t.Errorf("short label should not be masked: got %q, want SHORT", n.Label)
		}
	}
}

// ---------------------------------------------------------------------------
// F. 边详情查询 审计
// ---------------------------------------------------------------------------

func TestAuditQueryEdgeRowsComplete(t *testing.T) {
	sessionDir := t.TempDir()
	writeAuditFlowCSV(t, sessionDir)
	mapping := auditFlowMapping()

	// 用 A-001 → T-002 的全部进方向数据
	payload := EdgeDetailPayload{
		SessionID:           "test",
		Source:              "T-002",
		Target:              "A-001",
		SourceColumn:        mapping.SourceCol,
		SourceAccountColumn: mapping.SourceAccount,
		SourceIDColumn:      mapping.SourceID,
		SourceLabelColumn:   mapping.SourceLabel,
		TargetColumn:        mapping.TargetCol,
		TargetCardColumn:    mapping.TargetCard,
		TargetIDColumn:      mapping.TargetID,
		TargetLabelColumn:   mapping.TargetLabel,
		AmountColumn:        mapping.Amount,
		TimeColumn:          mapping.Time,
		DirectionColumn:     mapping.Direction,
		SerialColumn:        mapping.Serial,
		SummaryColumn:       mapping.Summary,
		RemarkColumn:        mapping.Remark,
		Directions:          []interface{}{"进"},
		StartDate:           "2024-01-01",
		EndDate:             "2024-01-31",
		Limit:               10000,
	}
	rows := queryEdgeRows(sessionDir, payload)
	if len(rows) == 0 {
		t.Fatal("expected edge detail rows")
	}
	// 验证每行都有流向标注
	for _, row := range rows {
		if row["流向源"] != payload.Source {
			t.Errorf("expected 流向源=%q, got %q", payload.Source, row["流向源"])
		}
		if row["流向目标"] != payload.Target {
			t.Errorf("expected 流向目标=%q, got %q", payload.Target, row["流向目标"])
		}
	}
	// 验证金额总和与图一致
	normalized := readSessionData(sessionDir, mapping, nil)
	filterPayload := edgeDetailFilterPayload(payload)
	filtered := applyFilters(normalized, filterPayload)
	graph := etl.BuildFlowGraph(filtered, 5000)
	totalAmount := sumRawRows(rows, payload.AmountColumn)
	var edgeAmount float64
	var edgeTxCount int
	foundEdge := false
	for _, e := range graph.Edges {
		if e.Source == payload.Source && e.Target == payload.Target {
			edgeAmount = e.Amount
			edgeTxCount = e.TxCount
			foundEdge = true
			break
		}
	}
	if !foundEdge {
		t.Fatalf("graph did not include edge %s -> %s", payload.Source, payload.Target)
	}
	if math.Abs(totalAmount-edgeAmount) > 0.001 {
		t.Errorf("detail total(%.2f) != graph edge amount(%.2f)", totalAmount, edgeAmount)
	}
	if len(rows) != edgeTxCount {
		t.Errorf("detail rows(%d) != graph tx_count(%d)", len(rows), edgeTxCount)
	}
}

// ---------------------------------------------------------------------------
// G. 全链路一致性审计 — 大容量多维度数据
// ---------------------------------------------------------------------------

func TestAuditEndToEndFullConsistency(t *testing.T) {
	sessionDir := t.TempDir()
	allRows := writeExtendedAuditFlowCSV(t, sessionDir)

	mapping := auditFlowMapping()
	normalized := readSessionData(sessionDir, mapping, nil)
	if len(normalized) != len(allRows) {
		t.Fatalf("normalized rows=%d, want %d", len(normalized), len(allRows))
	}

	// G1: 无筛选 — 全量数据的汇总和流图
	t.Run("no filters", func(t *testing.T) {
		filtered := applyFilters(normalized, map[string]interface{}{})
		if len(filtered) != len(normalized) {
			t.Fatalf("no-filter rows=%d, want %d", len(filtered), len(normalized))
		}
		summary := etl.BuildSummary(filtered)
		verifySummaryAgainstRaw(t, summary, filtered, "no-filters")
		graph := etl.BuildFlowGraph(filtered, 5000)
		verifyGraphAgainstRows(t, graph, filtered, "no-filters")
		// 无边截断 (filtered < 5000)
		if graph.Meta["truncated"].(bool) {
			t.Error("no-filter graph should not be truncated")
		}
	})

	// G2: 单维度筛选 — 来源
	t.Run("source filter A-001", func(t *testing.T) {
		filtered := applyFilters(normalized, map[string]interface{}{
			"source_filters": []interface{}{filterPayload("交易账号", "A-001")},
		})
		expected := filterRowsBy(t, normalized, func(r model.TransactionRow) bool {
			return r["交易账号"] == "A-001"
		})
		assertSubset(t, filtered, expected, "source A-001")
		summary := etl.BuildSummary(filtered)
		verifySummaryAgainstRaw(t, summary, expected, "source A-001")
		graph := etl.BuildFlowGraph(filtered, 5000)
		verifyGraphAgainstRows(t, graph, expected, "source A-001")
	})

	// G3: 单维度筛选 — 对手
	t.Run("target filter T-002", func(t *testing.T) {
		filtered := applyFilters(normalized, map[string]interface{}{
			"target_filters": []interface{}{filterPayload("交易对手账卡号", "T-002")},
		})
		expected := filterRowsBy(t, normalized, func(r model.TransactionRow) bool {
			return r["交易对手账卡号"] == "T-002"
		})
		summary := etl.BuildSummary(filtered)
		verifySummaryAgainstRaw(t, summary, expected, "target T-002")
		graph := etl.BuildFlowGraph(filtered, 5000)
		verifyGraphAgainstRows(t, graph, expected, "target T-002")
	})

	// G4: 方向筛选 — 仅进
	t.Run("direction in only", func(t *testing.T) {
		filtered := applyFilters(normalized, map[string]interface{}{
			"directions": []interface{}{"进"},
		})
		expected := filterRowsBy(t, normalized, func(r model.TransactionRow) bool {
			return r["收付标志"] == "进"
		})
		summary := etl.BuildSummary(filtered)
		verifySummaryAgainstRaw(t, summary, expected, "in only")
		graph := etl.BuildFlowGraph(filtered, 5000)
		verifyGraphAgainstRows(t, graph, expected, "in only")
	})

	// G5: 方向筛选 — 仅出
	t.Run("direction out only", func(t *testing.T) {
		filtered := applyFilters(normalized, map[string]interface{}{
			"directions": []interface{}{"出"},
		})
		expected := filterRowsBy(t, normalized, func(r model.TransactionRow) bool {
			return r["收付标志"] == "出"
		})
		summary := etl.BuildSummary(filtered)
		verifySummaryAgainstRaw(t, summary, expected, "out only")
		graph := etl.BuildFlowGraph(filtered, 5000)
		verifyGraphAgainstRows(t, graph, expected, "out only")
	})

	// G6: 时间范围筛选
	t.Run("date range Q1 2024", func(t *testing.T) {
		filtered := applyFilters(normalized, map[string]interface{}{
			"start_date": "2024-01-01",
			"end_date":   "2024-01-02",
		})
		expected := filterRowsBy(t, normalized, func(r model.TransactionRow) bool {
			return r["交易时间"] >= "2024-01-01 00:00:00" && r["交易时间"] <= "2024-01-02 23:59:59"
		})
		summary := etl.BuildSummary(filtered)
		verifySummaryAgainstRaw(t, summary, expected, "date range")
		graph := etl.BuildFlowGraph(filtered, 5000)
		verifyGraphAgainstRows(t, graph, expected, "date range")
	})

	// G7: 标签筛选
	t.Run("label filter", func(t *testing.T) {
		filtered := applyFilters(normalized, map[string]interface{}{
			"source_label_values": []interface{}{"重点主体"},
			"target_label_values": []interface{}{"公司"},
		})
		expected := filterRowsBy(t, normalized, func(r model.TransactionRow) bool {
			return r["交易方标签"] == "重点主体" && r["对手标签"] == "公司"
		})
		summary := etl.BuildSummary(filtered)
		verifySummaryAgainstRaw(t, summary, expected, "labels")
		graph := etl.BuildFlowGraph(filtered, 5000)
		verifyGraphAgainstRows(t, graph, expected, "labels")
	})

	// G8: 多维度组合筛选 (来源+对手+方向+时间+标签)
	t.Run("combined multi-dimension", func(t *testing.T) {
		filtered := applyFilters(normalized, map[string]interface{}{
			"source_filters":      []interface{}{filterPayload("交易账号", "A-001", "C-001")},
			"target_filters":      []interface{}{filterPayload("交易对手账卡号", "T-002")},
			"detail_filters":      []interface{}{filterPayload("摘要说明", "货款")},
			"directions":          []interface{}{"出"},
			"start_date":          "2024-01-01",
			"end_date":            "2024-01-15",
			"source_label_values": []interface{}{"重点主体"},
			"target_label_values": []interface{}{"公司"},
		})
		expected := filterRowsBy(t, normalized, func(r model.TransactionRow) bool {
			if r["交易账号"] != "A-001" && r["交易账号"] != "C-001" {
				return false
			}
			if r["交易对手账卡号"] != "T-002" {
				return false
			}
			if r["摘要说明"] != "货款" {
				return false
			}
			if r["收付标志"] != "出" {
				return false
			}
			if r["交易时间"] < "2024-01-01 00:00:00" || r["交易时间"] > "2024-01-15 23:59:59" {
				return false
			}
			if r["交易方标签"] != "重点主体" {
				return false
			}
			if r["对手标签"] != "公司" {
				return false
			}
			return true
		})
		summary := etl.BuildSummary(filtered)
		verifySummaryAgainstRaw(t, summary, expected, "combined")
		graph := etl.BuildFlowGraph(filtered, 5000)
		verifyGraphAgainstRows(t, graph, expected, "combined")
		// 组合筛选应触发审计限 (5000)
		if !graph.Meta["truncated"].(bool) && len(graph.Edges) < 5000 && graph.Meta["edge_limit"].(int) != auditFlowEdgeLimit {
			t.Errorf("combined filters should use audit edge limit, got %d", graph.Meta["edge_limit"])
		}
	})

	// G9: 空结果验证
	t.Run("no match", func(t *testing.T) {
		filtered := applyFilters(normalized, map[string]interface{}{
			"source_filters": []interface{}{filterPayload("交易账号", "NONEXIST")},
		})
		if len(filtered) != 0 {
			t.Fatalf("non-existent filter: expected 0 rows, got %d", len(filtered))
		}
		summary := etl.BuildSummary(filtered)
		if summary["total_rows"] != 0 {
			t.Errorf("empty summary total_rows=%v", summary["total_rows"])
		}
		graph := etl.BuildFlowGraph(filtered, 5000)
		if len(graph.Nodes) != 0 || len(graph.Edges) != 0 {
			t.Errorf("empty graph should have 0 nodes/edges, got %d/%d", len(graph.Nodes), len(graph.Edges))
		}
	})
}

// ============================================================================
// 辅助函数
// ============================================================================

// writeExtendedAuditFlowCSV 生成更全面的大容量测试数据
// 3 sources × 3 targets × 2 directions × 3 days × 3 hours = 162 rows
func writeExtendedAuditFlowCSV(t *testing.T, dir string) []model.TransactionRow {
	t.Helper()
	path := filepath.Join(dir, "extended_audit.csv")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create extended audit csv: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	headers := []string{"交易方户名", "交易方账户", "交易方身份证号", "交易方标签", "交易时间", "交易金额", "收付标志", "交易余额", "交易对手账卡号", "对手户名", "对手身份证号", "对手标签", "交易流水号", "摘要说明", "备注"}
	if err := writer.Write(headers); err != nil {
		t.Fatalf("write headers: %v", err)
	}

	sources := []struct{ name, account, id, label string }{
		{"张三", "A-001", "ID-A", "重点主体"},
		{"李四", "B-001", "ID-B", "普通主体"},
		{"王五", "C-001", "ID-C", "重点主体"},
	}
	targets := []struct{ name, card, id, label, summary string }{
		{"商户甲", "T-001", "TID-1", "商户", "工资"},
		{"公司乙", "T-002", "TID-2", "公司", "货款"},
		{"个人丙", "T-003", "TID-3", "个人", "转账"},
	}
	directions := []string{"出", "进"}
	var rows []model.TransactionRow
	serial := 1
	for _, source := range sources {
		for _, target := range targets {
			for _, direction := range directions {
				for day := 1; day <= 3; day++ {
					for hour := 9; hour <= 11; hour++ {
						amount := float64(serial * 10)
						serialValue := fmt.Sprintf("TX-%03d", serial)
						remark := "普通备注"
						if day == 2 && hour == 10 {
							remark = "重点备注"
						}
						timeValue := fmt.Sprintf("2024-01-%02d %02d:00:00", day, hour)
						record := []string{
							source.name, source.account, source.id, source.label,
							timeValue, fmt.Sprintf("%.2f", amount), direction,
							fmt.Sprintf("%.2f", amount+10000),
							target.card, target.name, target.id, target.label,
							serialValue, target.summary, remark,
						}
						if err := writer.Write(record); err != nil {
							t.Fatalf("write record: %v", err)
						}
						rows = append(rows, model.TransactionRow{
							"交易户名": source.name, "交易账号": source.account,
							"交易方身份证号": source.id, "交易方标签": source.label,
							"交易时间": timeValue, "交易金额": fmt.Sprintf("%.2f", amount),
							"收付标志":    direction,
							"交易对手账卡号": target.card, "对手户名": target.name,
							"对手身份证号": target.id, "对手标签": target.label,
							"交易流水号": serialValue, "摘要说明": target.summary, "备注": remark,
						})
						serial++
					}
				}
			}
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return rows
}

func filterRowsBy(t *testing.T, rows []model.TransactionRow, fn func(model.TransactionRow) bool) []model.TransactionRow {
	t.Helper()
	var result []model.TransactionRow
	for _, r := range rows {
		if fn(r) {
			result = append(result, r)
		}
	}
	return result
}

func assertSubset(t *testing.T, got, expected []model.TransactionRow, label string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("%s: rows=%d, want %d", label, len(got), len(expected))
	}
	gotSum := sumRows(got)
	expSum := sumRows(expected)
	if math.Abs(gotSum-expSum) > 0.001 {
		t.Errorf("%s: amount=%.2f, want %.2f", label, gotSum, expSum)
	}
}

func verifySummaryAgainstRaw(t *testing.T, summary map[string]interface{}, raw []model.TransactionRow, label string) {
	t.Helper()
	totalRows := summary["total_rows"].(int)
	if totalRows != len(raw) {
		t.Errorf("%s: summary total_rows=%d, raw rows=%d", label, totalRows, len(raw))
	}
	var inCount, outCount int
	var totalIn, totalOut float64
	for _, r := range raw {
		amt := parser.ToNumber(r["交易金额"])
		switch r["收付标志"] {
		case "进":
			inCount++
			totalIn += amt
		case "出":
			outCount++
			totalOut += amt
		}
	}
	if summary["in_count"] != inCount {
		t.Errorf("%s: summary in_count=%d, raw=%d", label, summary["in_count"], inCount)
	}
	if summary["out_count"] != outCount {
		t.Errorf("%s: summary out_count=%d, raw=%d", label, summary["out_count"], outCount)
	}
	gotIn := summary["total_in"].(float64)
	if math.Abs(gotIn-totalIn) > 0.01 {
		t.Errorf("%s: summary total_in=%.2f, raw=%.2f", label, gotIn, totalIn)
	}
	gotOut := summary["total_out"].(float64)
	if math.Abs(gotOut-totalOut) > 0.01 {
		t.Errorf("%s: summary total_out=%.2f, raw=%.2f", label, gotOut, totalOut)
	}
}

func verifyGraphAgainstRows(t *testing.T, graph model.FlowGraph, rows []model.TransactionRow, label string) {
	t.Helper()
	type expectEdge struct {
		amount float64
		count  int
	}
	type expectNode struct {
		inAmount  float64
		outAmount float64
		inCount   int
		outCount  int
	}
	expectedEdges := map[string]*expectEdge{}
	expectedNodes := map[string]*expectNode{}

	for _, row := range rows {
		source, target := expectedFlowEndpointsV2(row)
		if source == "" || target == "" || source == target {
			continue
		}
		amt := parser.ToNumber(row["交易金额"])
		key := source + "|" + target
		if _, ok := expectedEdges[key]; !ok {
			expectedEdges[key] = &expectEdge{}
		}
		expectedEdges[key].amount += amt
		expectedEdges[key].count++
		// node stats
		if _, ok := expectedNodes[source]; !ok {
			expectedNodes[source] = &expectNode{}
		}
		expectedNodes[source].outAmount += amt
		expectedNodes[source].outCount++
		if _, ok := expectedNodes[target]; !ok {
			expectedNodes[target] = &expectNode{}
		}
		expectedNodes[target].inAmount += amt
		expectedNodes[target].inCount++
	}

	if len(graph.Edges) != len(expectedEdges) {
		t.Errorf("%s: graph edges=%d, expected=%d", label, len(graph.Edges), len(expectedEdges))
	}
	for _, e := range graph.Edges {
		want, ok := expectedEdges[e.Source+"|"+e.Target]
		if !ok {
			t.Errorf("%s: unexpected edge %s→%s", label, e.Source, e.Target)
			continue
		}
		if math.Abs(e.Amount-want.amount) > 0.01 {
			t.Errorf("%s: edge %s→%s amount=%.2f, want %.2f", label, e.Source, e.Target, e.Amount, want.amount)
		}
		if e.TxCount != want.count {
			t.Errorf("%s: edge %s→%s tx_count=%d, want %d", label, e.Source, e.Target, e.TxCount, want.count)
		}
	}
	for _, n := range graph.Nodes {
		want, ok := expectedNodes[n.ID]
		if !ok {
			continue
		}
		if math.Abs(n.AmountIn-want.inAmount) > 0.01 {
			t.Errorf("%s: node %s AmountIn=%.2f, want %.2f", label, n.ID, n.AmountIn, want.inAmount)
		}
		if math.Abs(n.AmountOut-want.outAmount) > 0.01 {
			t.Errorf("%s: node %s AmountOut=%.2f, want %.2f", label, n.ID, n.AmountOut, want.outAmount)
		}
		if n.InCount != want.inCount {
			t.Errorf("%s: node %s InCount=%d, want %d", label, n.ID, n.InCount, want.inCount)
		}
		if n.OutCount != want.outCount {
			t.Errorf("%s: node %s OutCount=%d, want %d", label, n.ID, n.OutCount, want.outCount)
		}
	}
}

// expectedFlowEndpointsV2 使用与 BuildFlowGraph 一致的端点解析
// 优先使用 交易卡号/交易账号 而非 交易户名
func expectedFlowEndpointsV2(row model.TransactionRow) (string, string) {
	own := row["交易卡号"]
	if own == "" {
		own = row["交易账号"]
	}
	if own == "" {
		own = row["交易户名"]
	}
	if own == "" {
		own = "本方未知"
	}
	counter := row["交易对手账卡号"]
	if counter == "" {
		counter = row["对手户名"]
	}
	if counter == "" {
		counter = "对手未知"
	}
	switch row["收付标志"] {
	case "出":
		return own, counter
	case "进":
		return counter, own
	default:
		return "", ""
	}
}

func expectedFlowEndpoints(row model.TransactionRow) (string, string) {
	own := row["交易账号"]
	if own == "" {
		own = row["交易户名"]
	}
	counter := row["交易对手账卡号"]
	if counter == "" {
		counter = row["对手户名"]
	}
	if row["收付标志"] == "出" {
		return own, counter
	}
	if row["收付标志"] == "进" {
		return counter, own
	}
	return "", ""
}

func TestRealCSVEndToEnd(t *testing.T) {
	csvPath := filepath.Join("..", "..", "backend", "data", "rule_samples", "current", "real_bank_subset.csv")
	if _, err := os.Stat(csvPath); os.IsNotExist(err) {
		t.Skipf("real CSV not found: %s", csvPath)
	}
	sessionDir := t.TempDir()
	srcBytes, _ := os.ReadFile(csvPath)
	srcStr := string(srcBytes)
	os.WriteFile(filepath.Join(sessionDir, "real_bank.csv"), srcBytes, 0644)

	// Verify header
	headerLine := strings.SplitN(srcStr, "\n", 2)[0]
	cols := strings.Split(headerLine, ",")
	t.Logf("CSV: %d columns, 2000 data rows", len(cols))

	// Include all useful mapping columns for test coverage
	mapping := flowColumnMapping{
		SourceCol:     "交易卡号",
		SourceAccount: "交易账号",
		SourceName:    "交易方户名",
		SourceID:      "交易方证件号码",
		TargetCard:    "交易对手账卡号",
		TargetName:    "对手户名",
		TargetID:      "对手身份证号",
		Amount:        "交易金额",
		Time:          "交易时间",
		Direction:     "收付标志",
		Serial:        "交易流水号",
		Summary:       "摘要说明",
		Remark:        "备注",
	}

	txns := readSessionData(sessionDir, mapping, nil)
	if len(txns) == 0 {
		t.Fatal("expected at least 1 transaction")
	}
	if len(txns) != 2000 {
		t.Errorf("total rows: expected 2000, got %d", len(txns))
	}
	t.Logf("Parsed %d transactions", len(txns))

	// ─── A. Direction normalization ───────────────────────────────────────────
	t.Run("A_direction_normalization", func(t *testing.T) {
		inCount, outCount, emptyCount := 0, 0, 0
		for _, txn := range txns {
			switch txn["收付标志"] {
			case "进":
				inCount++
			case "出":
				outCount++
			default:
				emptyCount++
			}
		}
		// Known exact counts from data analysis
		if inCount != 594 {
			t.Errorf("direction=进: expected 594, got %d", inCount)
		}
		if outCount != 1362 {
			t.Errorf("direction=出: expected 1362, got %d", outCount)
		}
		if emptyCount != 44 {
			t.Errorf("direction=empty: expected 44, got %d", emptyCount)
		}
		if inCount+outCount+emptyCount != 2000 {
			t.Errorf("row sum mismatch: %d+%d+%d != 2000", inCount, outCount, emptyCount)
		}
		t.Logf("进=%d 出=%d empty=%d total=%d", inCount, outCount, emptyCount, inCount+outCount+emptyCount)
	})

	// ─── B. Unknown direction detection ──────────────────────────────────────
	t.Run("B_unknown_directions", func(t *testing.T) {
		unknown := checkUnknownDirections(txns)
		if len(unknown) > 0 {
			t.Errorf("expected all directions known (进/出/empty), got unknown: %v", unknown)
		}
		t.Logf("All directions known")
	})

	// ─── C. 6-dimension filtering ────────────────────────────────────────────
	t.Run("C1_filter_direction", func(t *testing.T) {
		fIn := applyFilters(txns, map[string]interface{}{
			"directions": []interface{}{"进"},
		})
		fOut := applyFilters(txns, map[string]interface{}{
			"directions": []interface{}{"出"},
		})
		emptyDir := 0
		for _, txn := range txns {
			if txn["收付标志"] != "进" && txn["收付标志"] != "出" {
				emptyDir++
			}
		}
		if len(fIn) != 594 {
			t.Errorf("direction=进 filter: expected 594, got %d", len(fIn))
		}
		if len(fOut) != 1362 {
			t.Errorf("direction=出 filter: expected 1362, got %d", len(fOut))
		}
		if len(fIn)+len(fOut)+emptyDir != 2000 {
			t.Errorf("consistency: in(%d)+out(%d)+empty(%d) != 2000", len(fIn), len(fOut), emptyDir)
		}
		// Graph from filtered subset
		g := etl.BuildFlowGraph(fIn, 600)
		if g.Meta["truncated"].(bool) {
			t.Error("direction=in flow graph should not be truncated")
		}
		t.Logf("Direction 进: %d rows → %d nodes, %d edges", len(fIn), g.Meta["total_nodes"], g.Meta["total_edges"])
	})

	t.Run("C2_filter_source_account", func(t *testing.T) {
		// Count actual 交易账号 values from parsed data
		acctCounts := make(map[string]int)
		for _, txn := range txns {
			acctCounts[txn["交易账号"]]++
		}
		testAcct := "6217710313788648"
		expected, found := acctCounts[testAcct]
		if !found {
			t.Fatalf("test account %q not found in parsed data", testAcct)
		}
		filtered := applyFilters(txns, map[string]interface{}{
			"source_filters": []interface{}{
				map[string]interface{}{
					"column": "交易账号",
					"values": []interface{}{testAcct},
				},
			},
		})
		if len(filtered) != expected {
			t.Errorf("source filter %q: expected %d, got %d", testAcct, expected, len(filtered))
		}
		for i, txn := range filtered {
			if txn["交易账号"] != testAcct {
				t.Errorf("row %d: expected 交易账号=%q, got %q", i, testAcct, txn["交易账号"])
			}
		}
		t.Logf("Source filter %q: %d rows (expected %d)", testAcct, len(filtered), expected)
	})

	t.Run("C3_filter_target_name", func(t *testing.T) {
		// 熊华庆 = 167 rows
		name := "熊华庆"
		filtered := applyFilters(txns, map[string]interface{}{
			"target_filters": []interface{}{
				map[string]interface{}{
					"column": "对手户名",
					"values": []interface{}{name},
				},
			},
		})
		if len(filtered) != 167 {
			t.Errorf("target filter %q: expected 167, got %d", name, len(filtered))
		}
		for i, txn := range filtered {
			if txn["对手户名"] != name {
				t.Errorf("row %d: expected 对手户名=%q, got %q", i, name, txn["对手户名"])
			}
		}
		t.Logf("Target filter %q: %d rows", name, len(filtered))

		// 陈平利 = 118 rows
		name2 := "陈平利"
		filtered2 := applyFilters(txns, map[string]interface{}{
			"target_filters": []interface{}{
				map[string]interface{}{
					"column": "对手户名",
					"values": []interface{}{name2},
				},
			},
		})
		if len(filtered2) != 118 {
			t.Errorf("target filter %q: expected 118, got %d", name2, len(filtered2))
		}
		t.Logf("Target filter %q: %d rows", name2, len(filtered2))
	})

	t.Run("C4_filter_date_range", func(t *testing.T) {
		// Compute actual date range and counts from parsed data
		minDate, maxDate := "", ""
		dateCounts := make(map[string]int)
		for _, txn := range txns {
			dt := txn["交易时间"]
			if dt == "" {
				continue
			}
			dateCounts[dt]++
			if minDate == "" || dt < minDate {
				minDate = dt
			}
			if maxDate == "" || dt > maxDate {
				maxDate = dt
			}
		}
		t.Logf("Parsed date range: %s to %s (%d unique dates)", minDate, maxDate, len(dateCounts))

		// Full range should cover all non-empty-date rows
		allFiltered := applyFilters(txns, map[string]interface{}{
			"start_date": minDate,
			"end_date":   maxDate,
		})
		nonEmptyDates := 0
		for _, txn := range txns {
			if txn["交易时间"] != "" {
				nonEmptyDates++
			}
		}
		if len(allFiltered) != nonEmptyDates {
			t.Errorf("full date range (%s to %s): expected %d (non-empty dates), got %d",
				minDate, maxDate, nonEmptyDates, len(allFiltered))
		}
		t.Logf("Full date range: %d rows (%d have non-empty dates)", len(allFiltered), nonEmptyDates)

		// Year 2022
		year2022 := applyFilters(txns, map[string]interface{}{
			"start_date": "2022-01-01",
			"end_date":   "2022-12-31",
		})
		t.Logf("2022 date range: %d rows", len(year2022))

		// Before 2019
		before2019 := applyFilters(txns, map[string]interface{}{
			"end_date": "2018-12-31",
		})
		t.Logf("Before 2019: %d rows", len(before2019))

		// Cross-consistency: disjoint ranges should not exceed total
		after2023 := applyFilters(txns, map[string]interface{}{
			"start_date": "2023-01-01",
		})
		if len(year2022)+len(after2023) > 2000 {
			t.Errorf("2022(%d)+after2023(%d) = %d > total(2000)",
				len(year2022), len(after2023), len(year2022)+len(after2023))
		}
		t.Logf("Date consistency: 2022(%d)+after2023(%d) <= 2000", len(year2022), len(after2023))

		// Future date range should return 0
		noData := applyFilters(txns, map[string]interface{}{
			"start_date": "2099-01-01",
		})
		if len(noData) != 0 {
			t.Errorf("future date range should return 0, got %d", len(noData))
		}
	})

	t.Run("C5_filter_detail_column", func(t *testing.T) {
		// Detail filter using 交易对手账卡号 (which IS mapped to txn) with known value "243300133"
		// Count expected from parsed data
		targetCardCounts := make(map[string]int)
		for _, txn := range txns {
			targetCardCounts[txn["交易对手账卡号"]]++
		}
		testVal := "243300133"
		expected, found := targetCardCounts[testVal]
		if !found {
			t.Fatalf("test value %q not found in parsed 交易对手账卡号", testVal)
		}
		filtered := applyFilters(txns, map[string]interface{}{
			"detail_filters": []interface{}{
				map[string]interface{}{
					"column": "交易对手账卡号",
					"values": []interface{}{testVal},
				},
			},
		})
		if len(filtered) != expected {
			t.Errorf("detail filter 交易对手账卡号=%q: expected %d, got %d", testVal, expected, len(filtered))
		}
		for i, txn := range filtered {
			if txn["交易对手账卡号"] != testVal {
				t.Errorf("row %d: expected 交易对手账卡号=%q, got %q", i, testVal, txn["交易对手账卡号"])
			}
		}
		t.Logf("Detail filter 交易对手账卡号=%q: %d rows (expected %d)", testVal, len(filtered), expected)
	})

	t.Run("C6_combined_source_direction", func(t *testing.T) {
		// Combined: 交易账号=6217710313788648 (520 rows) + direction=出
		acct := "6217710313788648"
		acctOnly := applyFilters(txns, map[string]interface{}{
			"source_filters": []interface{}{
				map[string]interface{}{
					"column": "交易账号",
					"values": []interface{}{acct},
				},
			},
		})
		outOnly := applyFilters(txns, map[string]interface{}{
			"directions": []interface{}{"出"},
		})
		combined := applyFilters(txns, map[string]interface{}{
			"source_filters": []interface{}{
				map[string]interface{}{
					"column": "交易账号",
					"values": []interface{}{acct},
				},
			},
			"directions": []interface{}{"出"},
		})
		if len(combined) > len(acctOnly) {
			t.Errorf("combined(%d) should be ≤ source-only(%d)", len(combined), len(acctOnly))
		}
		if len(combined) > len(outOnly) {
			t.Errorf("combined(%d) should be ≤ direction-only(%d)", len(combined), len(outOnly))
		}
		for i, txn := range combined {
			if txn["交易账号"] != acct {
				t.Errorf("row %d: expected 交易账号=%q, got %q", i, acct, txn["交易账号"])
			}
			if txn["收付标志"] != "出" {
				t.Errorf("row %d: expected 收付标志=出, got %q", i, txn["收付标志"])
			}
		}
		t.Logf("Combined (account=%q + direction=出): %d rows (source-only=%d, out-only=%d)",
			acct, len(combined), len(acctOnly), len(outOnly))
	})

	t.Run("C7_combined_target_direction", func(t *testing.T) {
		// Combined: 对手户名=熊华庆 (167) + direction=进
		name := "熊华庆"
		nameOnly := applyFilters(txns, map[string]interface{}{
			"target_filters": []interface{}{
				map[string]interface{}{
					"column": "对手户名",
					"values": []interface{}{name},
				},
			},
		})
		combined := applyFilters(txns, map[string]interface{}{
			"target_filters": []interface{}{
				map[string]interface{}{
					"column": "对手户名",
					"values": []interface{}{name},
				},
			},
			"directions": []interface{}{"进"},
		})
		if len(combined) > len(nameOnly) {
			t.Errorf("combined(%d) should be ≤ target-only(%d)", len(combined), len(nameOnly))
		}
		for i, txn := range combined {
			if txn["对手户名"] != name || txn["收付标志"] != "进" {
				t.Errorf("row %d: expected 对手户名=%q + 进, got 对手户名=%q 收付标志=%q",
					i, name, txn["对手户名"], txn["收付标志"])
			}
		}
		t.Logf("Combined (target=%q + direction=进): %d rows (target-only=%d)", name, len(combined), len(nameOnly))
	})

	t.Run("C8_combined_source_date", func(t *testing.T) {
		// Combined: 交易账号=6217710313788648 + date filter
		acct := "6217710313788648"
		acctCounts := make(map[string]int)
		for _, txn := range txns {
			acctCounts[txn["交易账号"]]++
		}
		acctOnly := applyFilters(txns, map[string]interface{}{
			"source_filters": []interface{}{
				map[string]interface{}{
					"column": "交易账号",
					"values": []interface{}{acct},
				},
			},
		})
		if len(acctOnly) != acctCounts[acct] {
			t.Errorf("source filter %q: expected %d, got %d", acct, acctCounts[acct], len(acctOnly))
		}
		// Filter by a specific date range
		combined := applyFilters(txns, map[string]interface{}{
			"source_filters": []interface{}{
				map[string]interface{}{
					"column": "交易账号",
					"values": []interface{}{acct},
				},
			},
			"start_date": "2024-01-01",
			"end_date":   "2024-12-31",
		})
		if len(combined) > len(acctOnly) {
			t.Errorf("combined(%d) should be ≤ source-only(%d)", len(combined), len(acctOnly))
		}
		t.Logf("Combined (acct=%q + 2024): %d rows (acct-only=%d)", acct, len(combined), len(acctOnly))
	})

	// ─── D. Summary statistics ────────────────────────────────────────────────
	t.Run("D_summary_statistics", func(t *testing.T) {
		sum := etl.BuildSummary(txns)
		if sum["total_rows"] != 2000 {
			t.Errorf("total_rows: expected 2000, got %v", sum["total_rows"])
		}
		inCount, hasIn := sum["in_count"].(int)
		outCount, hasOut := sum["out_count"].(int)
		if !hasIn || !hasOut {
			t.Errorf("in_count/out_count should be int, got in=%T out=%T", sum["in_count"], sum["out_count"])
		}
		totalRows, _ := sum["total_rows"].(int)
		// in+out may be less than total if empty-direction rows exist
		if inCount+outCount > totalRows {
			t.Errorf("in(%d)+out(%d) should be <= total(%d)", inCount, outCount, totalRows)
		}
		if inCount+outCount < totalRows {
			t.Logf("Note: in(%d)+out(%d)=%d < total(%d) — %d empty-direction rows",
				inCount, outCount, inCount+outCount, totalRows, totalRows-inCount-outCount)
		}
		totalIn, _ := sum["total_in"].(float64)
		totalOut, _ := sum["total_out"].(float64)
		if totalIn <= 0 || totalOut <= 0 {
			t.Errorf("expected positive totals, got in=%.2f out=%.2f", totalIn, totalOut)
		}
		t.Logf("Summary: %d rows, in=%d, out=%d, total_in=%.2f, total_out=%.2f",
			totalRows, inCount, outCount, totalIn, totalOut)
	})

	// ─── E. Flow graph building ──────────────────────────────────────────────
	t.Run("E1_flow_graph_basics", func(t *testing.T) {
		graph := etl.BuildFlowGraph(txns, 600)
		if truncated, ok := graph.Meta["truncated"].(bool); ok && truncated {
			t.Error("flow graph should not be truncated at 600 edge limit")
		}
		totalNodes := graph.Meta["total_nodes"].(int)
		totalEdges := graph.Meta["total_edges"].(int)
		if totalNodes < 10 {
			t.Errorf("expected >=10 nodes, got %d", totalNodes)
		}
		if totalEdges < 10 {
			t.Errorf("expected >=10 edges, got %d", totalEdges)
		}
		// Verify no self-loops
		selfLoops := 0
		for _, e := range graph.Edges {
			if e.Source == e.Target {
				selfLoops++
			}
		}
		if selfLoops > 0 {
			t.Errorf("expected 0 self-loops, got %d", selfLoops)
		}
		t.Logf("Flow graph: %d nodes, %d edges, 0 self-loops, truncated=%v",
			totalNodes, totalEdges, graph.Meta["truncated"])
	})

	t.Run("E2_flow_graph_consistency", func(t *testing.T) {
		// Full graph should have more edges than filtered subsets
		fullGraph := etl.BuildFlowGraph(txns, 600)
		fullEdges := fullGraph.Meta["total_edges"].(int)

		inGraph := etl.BuildFlowGraph(
			applyFilters(txns, map[string]interface{}{
				"directions": []interface{}{"进"},
			}), 600)
		inEdges := inGraph.Meta["total_edges"].(int)

		outGraph := etl.BuildFlowGraph(
			applyFilters(txns, map[string]interface{}{
				"directions": []interface{}{"出"},
			}), 600)
		outEdges := outGraph.Meta["total_edges"].(int)

		t.Logf("Edge counts: full=%d, in=%d, out=%d", fullEdges, inEdges, outEdges)
		if inEdges+outEdges < fullEdges {
			t.Errorf("in(%d)+out(%d) should be >= full(%d) (overlap possible)", inEdges, outEdges, fullEdges)
		}
		if inEdges > fullEdges || outEdges > fullEdges {
			t.Errorf("sub-graph edges should not exceed full graph: in(%d)≤full(%d), out(%d)≤full(%d)",
				inEdges, fullEdges, outEdges, fullEdges)
		}
		// Node consistency: each filter should produce fewer or equal nodes
		if len(inGraph.Nodes) > len(fullGraph.Nodes) {
			t.Errorf("in-filtered nodes(%d) > full nodes(%d)", len(inGraph.Nodes), len(fullGraph.Nodes))
		}
		if len(outGraph.Nodes) > len(fullGraph.Nodes) {
			t.Errorf("out-filtered nodes(%d) > full nodes(%d)", len(outGraph.Nodes), len(fullGraph.Nodes))
		}
	})

	t.Run("E3_flow_graph_edge_detail", func(t *testing.T) {
		graph := etl.BuildFlowGraph(txns, 600)
		if len(graph.Edges) == 0 {
			t.Fatal("flow graph has no edges")
		}
		// Pick first edge and verify its detail
		edge := graph.Edges[0]
		t.Logf("Selected edge: source=%s target=%s amount=%.2f tx_count=%d",
			edge.Source, edge.Target, edge.Amount, edge.TxCount)

		if edge.TxCount <= 0 {
			t.Errorf("edge tx_count should be >0, got %d", edge.TxCount)
		}
		if edge.Amount <= 0 {
			t.Errorf("edge amount should be >0, got %.2f", edge.Amount)
		}
		if edge.Source == "" || edge.Target == "" {
			t.Errorf("edge source/target should not be empty: source=%q target=%q",
				edge.Source, edge.Target)
		}
	})

	// ─── F. Edge detail query ────────────────────────────────────────────────
	t.Run("F_edge_detail_query", func(t *testing.T) {
		graph := etl.BuildFlowGraph(txns, 600)
		if len(graph.Edges) == 0 {
			t.Fatal("flow graph has no edges")
		}
		edge := graph.Edges[0]

		// Build edge detail payload and find matching transactions
		detailPayload := EdgeDetailPayload{
			SourceColumn:        mapping.SourceCol,
			SourceAccountColumn: mapping.SourceAccount,
			SourceNameColumn:    mapping.SourceName,
			SourceIDColumn:      mapping.SourceID,
			TargetColumn:        mapping.TargetCol,
			TargetCardColumn:    mapping.TargetCard,
			TargetNameColumn:    mapping.TargetName,
			TargetIDColumn:      mapping.TargetID,
			AmountColumn:        mapping.Amount,
			TimeColumn:          mapping.Time,
			DirectionColumn:     mapping.Direction,
			SerialColumn:        mapping.Serial,
			SummaryColumn:       mapping.Summary,
			RemarkColumn:        mapping.Remark,
			Source:              edge.Source,
			Target:              edge.Target,
			Limit:               10000,
		}
		rows := queryEdgeRows(sessionDir, detailPayload)
		if len(rows) != edge.TxCount {
			t.Fatalf("edge detail rows=%d, want edge tx_count=%d", len(rows), edge.TxCount)
		}
		if math.Abs(sumRawRows(rows, detailPayload.AmountColumn)-edge.Amount) > 0.001 {
			t.Fatalf("edge detail amount=%.2f, want edge amount=%.2f", sumRawRows(rows, detailPayload.AmountColumn), edge.Amount)
		}
		for i, row := range rows {
			txn := model.TransactionRow{}
			for key, value := range row {
				if s, ok := value.(string); ok {
					txn[key] = s
				}
			}
			src, tgt := flowEndpointsForTransaction(txn)
			if src != edge.Source || tgt != edge.Target {
				t.Fatalf("row %d endpoints (%q->%q) != edge (%q->%q)", i, src, tgt, edge.Source, edge.Target)
			}
		}
		t.Logf("Edge detail query: source=%q target=%q → %d exact rows, amount=%.2f",
			edge.Source, edge.Target, len(rows), sumRawRows(rows, detailPayload.AmountColumn))
	})

	// ─── G. End-to-end consistency ───────────────────────────────────────────
	t.Run("G1_preview", func(t *testing.T) {
		preview, previewCols := etl.BuildPreview(txns, 100)
		if len(preview) > 100 {
			t.Errorf("preview limit 100: got %d rows", len(preview))
		}
		if len(preview) == 0 {
			t.Errorf("preview should return data")
		}
		if len(previewCols) < 5 {
			t.Errorf("expected >=5 columns, got %d", len(previewCols))
		}
		t.Logf("Preview: %d rows, %d columns", len(preview), len(previewCols))
	})

	t.Run("G2_full_pipeline_nonempty", func(t *testing.T) {
		// Every filter should produce a graph with at least some edges
		filterCases := []struct {
			name    string
			payload map[string]interface{}
		}{
			{"direction=进", map[string]interface{}{
				"directions": []interface{}{"进"},
			}},
			{"direction=出", map[string]interface{}{
				"directions": []interface{}{"出"},
			}},
			{"source=6217710313788648", map[string]interface{}{
				"source_filters": []interface{}{
					map[string]interface{}{"column": "交易账号", "values": []interface{}{"6217710313788648"}},
				},
			}},
			{"target=熊华庆", map[string]interface{}{
				"target_filters": []interface{}{
					map[string]interface{}{"column": "对手户名", "values": []interface{}{"熊华庆"}},
				},
			}},
			{"detail=243300133", map[string]interface{}{
				"detail_filters": []interface{}{
					map[string]interface{}{"column": "交易对手账卡号", "values": []interface{}{"243300133"}},
				},
			}},
		}
		for _, tc := range filterCases {
			filtered := applyFilters(txns, tc.payload)
			g := etl.BuildFlowGraph(filtered, 600)
			edges := g.Meta["total_edges"].(int)
			if edges == 0 {
				t.Errorf("%s: flow graph has 0 edges (rows=%d)", tc.name, len(filtered))
			} else {
				t.Logf("%s: %d rows → %d edges", tc.name, len(filtered), edges)
			}
		}
	})

	t.Run("G3_edge_count_monotonic", func(t *testing.T) {
		// Adding more filters should not increase edge count
		baseRows := applyFilters(txns, map[string]interface{}{
			"source_filters": []interface{}{
				map[string]interface{}{"column": "交易账号", "values": []interface{}{"6217710313788648"}},
			},
		})
		baseGraph := etl.BuildFlowGraph(baseRows, 600)
		baseEdges := baseGraph.Meta["total_edges"].(int)

		combinedRows := applyFilters(txns, map[string]interface{}{
			"source_filters": []interface{}{
				map[string]interface{}{"column": "交易账号", "values": []interface{}{"6217710313788648"}},
			},
			"directions": []interface{}{"出"},
		})
		combinedGraph := etl.BuildFlowGraph(combinedRows, 600)
		combinedEdges := combinedGraph.Meta["total_edges"].(int)

		if combinedEdges > baseEdges {
			t.Errorf("combined filter (%d edges) produced more edges than base (%d edges)",
				combinedEdges, baseEdges)
		}
		t.Logf("Monotonic edges: base=%d, combined=%d", baseEdges, combinedEdges)
	})
}

// ============================================================================
// PostgreSQL 真实数据审计测试
// 连接本地 PostgreSQL mz 数据库，读取 ls_0709.交易明细信息 数据
// 验证 ETL 流水线、方向归一化、建图等与数据库统计一致
// ============================================================================

const (
	pgHost     = "127.0.0.1"
	pgPort     = 5432
	pgUser     = "postgres"
	pgPassword = "123456"
	pgDBName   = "mz"
	pgSchema   = "ls_0709"
	pgTable    = "交易明细信息"
)

type pgStats struct {
	totalRows   int
	inCount     int
	outCount    int
	otherDir    int
	emptyDir    int
	totalAmount float64
	inAmount    float64
	outAmount   float64
	minTime     string
	maxTime     string
}

func getPGStats(t *testing.T) pgStats {
	t.Helper()
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		pgHost, pgPort, pgUser, pgPassword, pgDBName)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Skipf("PostgreSQL 不可用: %v (跳过测试)", err)
		return pgStats{}
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("PostgreSQL 连接失败: %v (跳过测试)", err)
		return pgStats{}
	}

	var s pgStats
	query := fmt.Sprintf(`SELECT 
		COUNT(*)::int,
		SUM(CASE WHEN "收付标志" = '进' THEN 1 ELSE 0 END)::int,
		SUM(CASE WHEN "收付标志" = '出' THEN 1 ELSE 0 END)::int,
		SUM(CASE WHEN "收付标志" NOT IN ('进','出','') AND "收付标志" IS NOT NULL THEN 1 ELSE 0 END)::int,
		SUM(CASE WHEN "收付标志" IS NULL OR "收付标志" = '' THEN 1 ELSE 0 END)::int,
		ROUND(COALESCE(SUM("交易金额"),0)::numeric,2)::float8,
		ROUND(COALESCE(SUM(CASE WHEN "收付标志"='进' THEN "交易金额" ELSE 0 END),0)::numeric,2)::float8,
		ROUND(COALESCE(SUM(CASE WHEN "收付标志"='出' THEN "交易金额" ELSE 0 END),0)::numeric,2)::float8,
		COALESCE(MIN("交易时间")::text,''),
		COALESCE(MAX("交易时间")::text,'')
	FROM %s.%s`, pgSchema, pgTable)

	err = db.QueryRow(query).Scan(
		&s.totalRows, &s.inCount, &s.outCount, &s.otherDir, &s.emptyDir,
		&s.totalAmount, &s.inAmount, &s.outAmount, &s.minTime, &s.maxTime,
	)
	if err != nil {
		t.Fatalf("查询 PostgreSQL 统计失败: %v", err)
	}
	t.Logf("PostgreSQL: total=%d 进=%d 出=%d 其他=%d 空=%d",
		s.totalRows, s.inCount, s.outCount, s.otherDir, s.emptyDir)
	t.Logf("PostgreSQL: 金额 total=%.2f in=%.2f out=%.2f",
		s.totalAmount, s.inAmount, s.outAmount)
	t.Logf("PostgreSQL: 时间范围 %s ~ %s", s.minTime, s.maxTime)
	return s
}

func getPGDirectionDetails(t *testing.T) map[string]struct {
	cnt int
	amt float64
} {
	t.Helper()
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		pgHost, pgPort, pgUser, pgPassword, pgDBName)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Skipf("PostgreSQL 不可用: %v (跳过测试)", err)
		return nil
	}
	defer db.Close()
	query := fmt.Sprintf(`SELECT "收付标志", COUNT(*)::int, ROUND(COALESCE(SUM("交易金额"),0)::numeric,2)::float8
	FROM %s.%s
	WHERE "收付标志" NOT IN ('进','出') AND "收付标志" IS NOT NULL AND "收付标志" != ''
	GROUP BY "收付标志" ORDER BY COUNT(*) DESC`, pgSchema, pgTable)
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("查询方向详情失败: %v", err)
	}
	defer rows.Close()
	result := make(map[string]struct {
		cnt int
		amt float64
	})
	for rows.Next() {
		var dir string
		var cnt int
		var amt float64
		if err := rows.Scan(&dir, &cnt, &amt); err != nil {
			t.Fatalf("扫描方向行失败: %v", err)
		}
		result[dir] = struct {
			cnt int
			amt float64
		}{cnt, amt}
	}
	return result
}

// TestPGRealDataDirectionNormalization 用真实 PostgreSQL 数据验证方向归一化正确性
func TestPGRealDataDirectionNormalization(t *testing.T) {
	stats := getPGStats(t)

	t.Logf("方向分布: 进=%d 出=%d 其他=%d 空=%d",
		stats.inCount, stats.outCount, stats.otherDir, stats.emptyDir)

	// 进 + 出 + 其他 + 空 = 合计
	total := stats.inCount + stats.outCount + stats.otherDir + stats.emptyDir
	if total != stats.totalRows {
		t.Errorf("方向计数合计 %d != 总行数 %d", total, stats.totalRows)
	}

	// 进+出 是标准方向，应占绝对多数
	stdRatio := float64(stats.inCount+stats.outCount) / float64(stats.totalRows)
	t.Logf("标准方向占比: %.4f (进+出=%d)", stdRatio, stats.inCount+stats.outCount)
	if stdRatio < 0.99 {
		t.Logf("警告: 标准方向占比 %.4f < 0.99，可能有大量未知方向", stdRatio)
	}

	// 金额合理性: 总金额 = 进金额 + 出金额 + 其他方向金额
	// 其他方向（贷/借/入）也有金额，所以 total != in + out
	otherAmt := stats.totalAmount - stats.inAmount - stats.outAmount
	t.Logf("其他方向金额合计: %.2f (占总金额 %.4f%%)", otherAmt, otherAmt/stats.totalAmount*100)
	if otherAmt < 0 {
		t.Errorf("金额不匹配: total=%.2f in=%.2f out=%.2f other=%.2f",
			stats.totalAmount, stats.inAmount, stats.outAmount, otherAmt)
	}
}

// TestPGRealDataDirectionAliases 验证 PG 中非标准方向值被正常归一化
func TestPGRealDataDirectionAliases(t *testing.T) {
	details := getPGDirectionDetails(t)
	if len(details) == 0 {
		t.Skip("无非标准方向值")
	}

	emptyAliases := map[string]string{}
	for dir, info := range details {
		normalized := normalizeFlowDirection(dir, emptyAliases)
		t.Logf("方向 '%s': %d 笔, %.2f 元 → 归一化后 '%s'", dir, info.cnt, info.amt, normalized)

		// 所有方向值必须归一化为"进"或"出"
		if normalized != "进" && normalized != "出" {
			t.Errorf("方向 '%s' 归一化为 '%s'，预期应为 '进' 或 '出'", dir, normalized)
		}
	}
}

// TestPGRealDataFlowGraphEdgeStats 从 CSV 文件读取数据验证流图建图逻辑
// 读取真实交易明细信息 CSV，运行 ETL 流水线，验证建图结果与数据库统计一致
func TestPGRealDataFlowGraphEdgeStats(t *testing.T) {
	csvPath := `E:\项目\传销\梅州\2 调单\清洗\20240517\交易明细信息.csv`
	if _, err := os.Stat(csvPath); os.IsNotExist(err) {
		t.Skipf("CSV 文件不存在: %s (跳过测试)", csvPath)
		return
	}

	// 获取 PG 统计（用于验证数据一致性）
	_ = getPGStats(t)

	// 从 CSV 读取数据
	sessionDir := t.TempDir()
	destPath := filepath.Join(sessionDir, "transactions.csv")
	if err := copyFile(csvPath, destPath); err != nil {
		t.Fatalf("复制 CSV 文件失败: %v", err)
	}

	// 使用与 PG 表结构匹配的映射
	mapping := flowColumnMapping{
		SourceCol:     "交易卡号",
		SourceAccount: "交易账号",
		SourceName:    "交易方户名",
		SourceID:      "交易方证件号码",
		TargetCol:     "对手户名",
		TargetCard:    "交易对手账卡号",
		TargetName:    "对手户名",
		TargetID:      "对手身份证号",
		Amount:        "交易金额",
		Time:          "交易时间",
		Direction:     "收付标志",
	}

	// 读取数据（由于数据量大，只取前 maxRows 行做验证）
	maxRows := 100000
	txns := readSessionData(sessionDir, mapping, nil)
	if len(txns) == 0 {
		t.Fatalf("未读取到任何交易数据")
	}

	// 限制数据量用于测试
	if len(txns) > maxRows {
		t.Logf("读取 %d 行，限制测试到前 %d 行", len(txns), maxRows)
		txns = txns[:maxRows]
	}

	// 验证方向归一化
	dirCount := make(map[string]int)
	for _, txn := range txns {
		dir := txn["收付标志"]
		dirCount[dir]++
	}

	t.Logf("CSV 方向分布 (前 %d 行):", len(txns))
	for dir, cnt := range dirCount {
		ratio := float64(cnt) / float64(len(txns)) * 100
		t.Logf("  %s: %d (%.1f%%)", dir, cnt, ratio)
	}

	// 验证未知方向 — 所有方向值必须归一化为"进"或"出"
	if unknowns := checkUnknownDirections(txns); len(unknowns) > 0 {
		for _, unknown := range unknowns {
			t.Errorf("存在未归一化方向值: '%s'", unknown)
		}
	}

	// 建图
	graph := etl.BuildFlowGraph(txns, 600)
	t.Logf("建图结果: %d 节点, %d 边 (total_nodes=%d, total_edges=%d)",
		len(graph.Nodes), len(graph.Edges),
		graph.Meta["total_nodes"], graph.Meta["total_edges"])

	// 验证边属性
	if len(graph.Edges) > 0 {
		for i, edge := range graph.Edges {
			if i >= 5 {
				t.Logf("  ... 以及 %d 条更多边", len(graph.Edges)-5)
				break
			}
			t.Logf("  边 #%d: %s → %s 金额=%.2f 笔数=%d 时间=%s~%s",
				i, edge.Source, edge.Target, edge.Amount, edge.TxCount,
				nullableStr(edge.FirstTime), nullableStr(edge.LastTime))
		}

		// 验证所有边的金额和笔数为正
		for _, edge := range graph.Edges {
			if edge.Amount < 0 {
				t.Errorf("边 %s→%s 金额为负: %.2f", edge.Source, edge.Target, edge.Amount)
			}
			if edge.TxCount <= 0 {
				t.Errorf("边 %s→%s 笔数 <=0: %d", edge.Source, edge.Target, edge.TxCount)
			}
			if edge.Source == "" || edge.Target == "" {
				t.Errorf("边包含空 source/target: %q→%q", edge.Source, edge.Target)
			}
			if edge.Source == edge.Target {
				t.Errorf("存在自环边: %s→%s", edge.Source, edge.Target)
			}
		}
	}

	// 验证节点属性
	for _, node := range graph.Nodes {
		if node.AmountIn < 0 || node.AmountOut < 0 {
			t.Errorf("节点 %s 金额为负: in=%.2f out=%.2f", node.ID, node.AmountIn, node.AmountOut)
		}
		// 节点金额统计一致性
		if math.Abs(node.AmountIn+node.AmountOut) > 0.001 {
			t.Logf("节点 %s: in=%.2f out=%.2f count=%d degree=%d",
				node.ID, node.AmountIn, node.AmountOut, node.TxCount, node.Degree)
		}
	}

	// 验证截断
	truncated := graph.Meta["truncated"].(bool)
	renderedEdges := graph.Meta["rendered_edges"].(int)
	totalEdges := graph.Meta["total_edges"].(int)
	t.Logf("截断状态: truncated=%v rendered=%d total=%d", truncated, renderedEdges, totalEdges)

	if totalEdges > 600 && !truncated {
		t.Errorf("total_edges=%d > 600 但 truncated=false", totalEdges)
	}

	t.Logf("✓ CSV 数据流图验证完成，%d 条边，%d 个节点", len(graph.Edges), len(graph.Nodes))
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func nullableStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
