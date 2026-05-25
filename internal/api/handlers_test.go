package api

import (
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
