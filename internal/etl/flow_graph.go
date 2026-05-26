package etl

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
)

// BuildFlowGraph aggregates transactions into nodes/edges for visualization
func BuildFlowGraph(txns []model.TransactionRow, maxEdges int) model.FlowGraph {
	if maxEdges <= 0 {
		maxEdges = 600
	}
	if len(txns) == 0 {
		return model.FlowGraph{
			Nodes: []model.FlowNode{},
			Edges: []model.FlowEdge{},
			Meta: map[string]interface{}{
				"total_edges": 0, "total_nodes": 0,
				"rendered_edges": 0, "rendered_nodes": 0,
				"truncated": false, "edge_limit": maxEdges,
			},
		}
	}
	type flowRow struct {
		source, target string
		amount         float64
		tradeTime      string
	}
	work := make([]flowRow, 0, len(txns))
	nodeInfoMap := make(map[string]*flowNodeInfo)
	for _, txn := range txns {
		amt := parser.ToNumber(txn["交易金额"])
		own := firstTransactionValue(txn, "交易卡号", "交易账号", "交易户名")
		if own == "" {
			own = "本方未知"
		}
		counter := firstTransactionValue(txn, "交易对手账卡号", "对手户名")
		if counter == "" {
			counter = "对手未知"
		}
		dir := txn["收付标志"]
		timeVal := txn["交易时间"]
		var source, target string
		if dir == "出" {
			source, target = own, counter
		} else if dir == "进" {
			source, target = counter, own
		} else {
			continue
		}
		if source == target || source == "" || target == "" {
			continue
		}
		addFlowNodeInfo(nodeInfoMap, own, flowNodeInfoFromTransaction(txn, true))
		addFlowNodeInfo(nodeInfoMap, counter, flowNodeInfoFromTransaction(txn, false))
		work = append(work, flowRow{source, target, amt, timeVal})
	}
	// Dedup
	type flowKey struct {
		source, target string
		amount         float64
		timeKey        string
	}
	seen := make(map[flowKey]bool)
	deduped := make([]flowRow, 0, len(work))
	for _, r := range work {
		k := flowKey{r.source, r.target, math.Round(r.amount*100) / 100, r.tradeTime}
		if seen[k] {
			continue
		}
		seen[k] = true
		deduped = append(deduped, r)
	}
	work = deduped
	// Aggregate edges
	type edgeAgg struct {
		source, target, firstTime, lastTime string
		amount, maxAmount                   float64
		txCount                             int
	}
	edgeMap := make(map[string]*edgeAgg)
	for _, r := range work {
		key := r.source + "|" + r.target
		ea, ok := edgeMap[key]
		if !ok {
			ea = &edgeAgg{source: r.source, target: r.target}
			edgeMap[key] = ea
		}
		ea.amount += r.amount
		ea.txCount++
		if r.amount > ea.maxAmount {
			ea.maxAmount = r.amount
		}
		if r.tradeTime != "" {
			if ea.firstTime == "" || r.tradeTime < ea.firstTime {
				ea.firstTime = r.tradeTime
			}
			if ea.lastTime == "" || r.tradeTime > ea.lastTime {
				ea.lastTime = r.tradeTime
			}
		}
	}
	totalNodeSet := make(map[string]bool)
	for _, ea := range edgeMap {
		totalNodeSet[ea.source] = true
		totalNodeSet[ea.target] = true
	}
	sortedEdges := make([]*edgeAgg, 0, len(edgeMap))
	for _, ea := range edgeMap {
		sortedEdges = append(sortedEdges, ea)
	}
	sort.Slice(sortedEdges, func(i, j int) bool {
		if sortedEdges[i].amount != sortedEdges[j].amount {
			return sortedEdges[i].amount > sortedEdges[j].amount
		}
		return sortedEdges[i].txCount > sortedEdges[j].txCount
	})
	truncated := len(sortedEdges) > maxEdges
	if len(sortedEdges) > maxEdges {
		sortedEdges = sortedEdges[:maxEdges]
	}
	// Build node stats
	nodeTimeMap := make(map[string]map[string]string)
	for _, r := range work {
		updateNodeTime(nodeTimeMap, r.source, r.tradeTime)
		updateNodeTime(nodeTimeMap, r.target, r.tradeTime)
	}
	degreeMap := make(map[string]int)
	for _, ea := range sortedEdges {
		degreeMap[ea.source]++
		degreeMap[ea.target]++
	}
	nodeStats := make(map[string]*model.FlowNode)
	for _, ea := range sortedEdges {
		initNode(nodeStats, ea.source)
		initNode(nodeStats, ea.target)
		ns := nodeStats[ea.source]
		ns.AmountOut += ea.amount
		ns.TxCount += ea.txCount
		ns.OutCount += ea.txCount
		nt := nodeStats[ea.target]
		nt.AmountIn += ea.amount
		nt.TxCount += ea.txCount
		nt.InCount += ea.txCount
	}
	for id, stats := range nodeStats {
		if strings.HasPrefix(id, "本方") || id == "本方未知" {
			stats.Role = "self"
		}
		if info, ok := nodeInfoMap[id]; ok {
			stats.AccountNo = info.value("account")
			stats.AccountName = info.value("name")
			stats.IDNumber = info.value("id")
		}
		if tm, ok := nodeTimeMap[id]; ok {
			stats.FirstTime = strPtr(tm["first"])
			stats.LastTime = strPtr(tm["last"])
		}
		stats.Degree = degreeMap[id]
	}
	nodes := make([]model.FlowNode, 0, len(nodeStats))
	for _, n := range nodeStats {
		n.Label = maskLabel(n.ID)
		if n.Label == "" {
			n.Label = n.ID
		}
		nodes = append(nodes, *n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	edges := make([]model.FlowEdge, 0, len(sortedEdges))
	for _, ea := range sortedEdges {
		avgAmt := 0.0
		if ea.txCount > 0 {
			avgAmt = math.Round(ea.amount/float64(ea.txCount)*100) / 100
		}
		edges = append(edges, model.FlowEdge{
			ID:        flowEdgeID(ea.source, ea.target),
			Source:    ea.source,
			Target:    ea.target,
			Amount:    math.Round(ea.amount*100) / 100,
			TxCount:   ea.txCount,
			AvgAmount: avgAmt,
			MaxAmount: math.Round(ea.maxAmount*100) / 100,
			Label:     fmt.Sprintf("%s / %d笔", Float64ToStr(ea.amount), ea.txCount),
			FirstTime: strPtr(ea.firstTime),
			LastTime:  strPtr(ea.lastTime),
		})
	}
	return model.FlowGraph{
		Nodes: nodes, Edges: edges,
		Meta: map[string]interface{}{
			"total_edges": len(edgeMap), "total_nodes": len(totalNodeSet),
			"rendered_edges": len(edges), "rendered_nodes": len(nodes),
			"truncated": truncated, "edge_limit": maxEdges,
		},
	}
}

func updateNodeTime(m map[string]map[string]string, node, timeVal string) {
	if timeVal == "" {
		return
	}
	entry, ok := m[node]
	if !ok {
		m[node] = map[string]string{"first": timeVal, "last": timeVal}
		return
	}
	if entry["first"] == "" || timeVal < entry["first"] {
		entry["first"] = timeVal
	}
	if entry["last"] == "" || timeVal > entry["last"] {
		entry["last"] = timeVal
	}
}

func initNode(m map[string]*model.FlowNode, id string) {
	if _, ok := m[id]; !ok {
		m[id] = &model.FlowNode{ID: id, Label: id, Tags: []string{}}
	}
}

type flowNodeInfo struct {
	accountNos   []string
	accountNames []string
	idNumbers    []string
}

func flowNodeInfoFromTransaction(txn model.TransactionRow, own bool) flowNodeInfo {
	if own {
		return flowNodeInfo{
			accountNos:   singleNonEmptyValue(firstTransactionValue(txn, "交易卡号", "交易账号")),
			accountNames: nonEmptyTransactionValues(txn, "交易户名"),
			idNumbers:    singleNonEmptyValue(firstTransactionValue(txn, "交易证件号码", "交易方身份证号")),
		}
	}
	return flowNodeInfo{
		accountNos:   nonEmptyTransactionValues(txn, "交易对手账卡号"),
		accountNames: nonEmptyTransactionValues(txn, "对手户名"),
		idNumbers:    nonEmptyTransactionValues(txn, "对手身份证号"),
	}
}

func addFlowNodeInfo(m map[string]*flowNodeInfo, node string, info flowNodeInfo) {
	if node == "" {
		return
	}
	current, ok := m[node]
	if !ok {
		current = &flowNodeInfo{}
		m[node] = current
	}
	current.accountNos = appendUniqueValues(current.accountNos, info.accountNos...)
	current.accountNames = appendUniqueValues(current.accountNames, info.accountNames...)
	current.idNumbers = appendUniqueValues(current.idNumbers, info.idNumbers...)
}

func (info flowNodeInfo) value(kind string) string {
	switch kind {
	case "account":
		return strings.Join(info.accountNos, "、")
	case "name":
		return strings.Join(info.accountNames, "、")
	case "id":
		return strings.Join(info.idNumbers, "、")
	default:
		return ""
	}
}

func firstTransactionValue(txn model.TransactionRow, columns ...string) string {
	values := nonEmptyTransactionValues(txn, columns...)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func nonEmptyTransactionValues(txn model.TransactionRow, columns ...string) []string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		value := strings.TrimSpace(txn[column])
		if value == "" {
			continue
		}
		values = appendUniqueValues(values, value)
	}
	return values
}

func singleNonEmptyValue(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func appendUniqueValues(values []string, additions ...string) []string {
	for _, addition := range additions {
		addition = strings.TrimSpace(addition)
		if addition == "" {
			continue
		}
		exists := false
		for _, value := range values {
			if value == addition {
				exists = true
				break
			}
		}
		if !exists {
			values = append(values, addition)
		}
	}
	return values
}

func flowEdgeID(source, target string) string {
	return fmt.Sprintf("%d:%s->%d:%s", len(source), source, len(target), target)
}

func maskLabel(value string) string {
	if len(value) >= 12 {
		allDigit := true
		for _, c := range value {
			if c < '0' || c > '9' {
				allDigit = false
				break
			}
		}
		if allDigit {
			return value[:4] + "..." + value[len(value)-4:]
		}
	}
	if len(value) > 24 {
		return value[:24]
	}
	return value
}

// Float64ToStr formats float64 to 2 decimal places string
func Float64ToStr(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', 2, 64)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
