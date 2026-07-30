package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"

	"github.com/rs/zerolog/log"
)

type duckDBGraphQuery struct {
	tableName        string
	maxEdges         int
	availableColumns map[string]string // normalized -> raw
	rawColumns       []string

	sourceCol     string
	sourceAccount string
	sourceName    string
	sourceID      string
	sourceLabel   string
	targetCol     string
	targetCard    string
	targetName    string
	targetID      string
	targetLabel   string
	amountCol     string
	timeCol       string
	directionCol  string
	serialCol     string
	summaryCol    string
	remarkCol     string

	startDate        string
	endDate          string
	directions       []string
	filterPredicates []string
}

func newDuckDBGraphQuery(tableName string, columns []string, mapping flowColumnMapping, payload map[string]interface{}) *duckDBGraphQuery {
	q := &duckDBGraphQuery{
		tableName:        tableName,
		maxEdges:         flowEdgeLimit(payload),
		rawColumns:       columns,
		availableColumns: make(map[string]string),
	}
	for _, col := range columns {
		q.addAvailableColumn(col)
	}
	q.addAvailableColumn(mapping.SourceCol)
	q.addAvailableColumn(mapping.SourceAccount)
	q.addAvailableColumn(mapping.SourceName)
	q.addAvailableColumn(mapping.SourceID)
	q.addAvailableColumn(mapping.SourceLabel)
	q.addAvailableColumn(mapping.TargetCol)
	q.addAvailableColumn(mapping.TargetCard)
	q.addAvailableColumn(mapping.TargetName)
	q.addAvailableColumn(mapping.TargetID)
	q.addAvailableColumn(mapping.TargetLabel)
	q.addAvailableColumn(mapping.Amount)
	q.addAvailableColumn(mapping.Time)
	q.addAvailableColumn(mapping.Direction)
	q.addAvailableColumn(mapping.Serial)
	q.addAvailableColumn(mapping.Summary)
	q.addAvailableColumn(mapping.Remark)

	q.sourceCol = mapping.SourceCol
	q.sourceAccount = mapping.SourceAccount
	q.sourceName = mapping.SourceName
	q.sourceID = mapping.SourceID
	q.sourceLabel = mapping.SourceLabel
	q.targetCol = mapping.TargetCol
	q.targetCard = mapping.TargetCard
	q.targetName = mapping.TargetName
	q.targetID = mapping.TargetID
	q.targetLabel = mapping.TargetLabel
	q.amountCol = mapping.Amount
	q.timeCol = mapping.Time
	q.directionCol = mapping.Direction
	q.serialCol = mapping.Serial
	q.summaryCol = mapping.Summary
	q.remarkCol = mapping.Remark

	if start, ok := payload["start_date"].(string); ok {
		q.startDate = strings.TrimSpace(start)
	}
	if end, ok := payload["end_date"].(string); ok {
		q.endDate = strings.TrimSpace(end)
	}
	if dirs, ok := payload["directions"].([]interface{}); ok {
		for _, d := range dirs {
			if s, ok := d.(string); ok {
				q.directions = append(q.directions, s)
			}
		}
	}
	q.filterPredicates = q.buildFilterPredicates(payload)
	return q
}

func (q *duckDBGraphQuery) addAvailableColumn(column string) {
	column = strings.TrimSpace(column)
	if column == "" {
		return
	}
	normalized := parser.NormalizeHeader(column)
	if normalized == "" {
		return
	}
	if _, ok := q.availableColumns[normalized]; !ok {
		q.availableColumns[normalized] = column
	}
}

func (q *duckDBGraphQuery) quote(s string) string {
	return fmt.Sprintf(`"%s"`, s)
}

func (q *duckDBGraphQuery) resolveColumn(column string) string {
	column = strings.TrimSpace(column)
	if column == "" {
		return ""
	}
	normalized := parser.NormalizeHeader(column)
	if normalized == "" {
		return ""
	}
	if raw, ok := q.availableColumns[normalized]; ok {
		return raw
	}
	return column
}

func (q *duckDBGraphQuery) sourceExpr() string {
	cols := []string{q.sourceAccount, q.sourceID, q.sourceName, q.sourceCol}
	return q.coalesceExpr(cols)
}

func (q *duckDBGraphQuery) targetExpr() string {
	cols := []string{q.targetCard, q.targetID, q.targetName, q.targetCol}
	return q.coalesceExpr(cols)
}

func (q *duckDBGraphQuery) coalesceExpr(cols []string) string {
	var parts []string
	for _, c := range cols {
		if c != "" {
			resolved := q.resolveColumn(c)
			if resolved != "" {
				parts = append(parts, fmt.Sprintf("NULLIF(TRIM(CAST(%s AS VARCHAR)), '')", q.quote(resolved)))
			}
		}
	}
	if len(parts) == 0 {
		return "'未知主体'"
	}
	return "COALESCE(" + strings.Join(parts, ", ") + ", '未知主体')"
}

func (q *duckDBGraphQuery) directionExpr() string {
	raw := q.trimExpr(q.resolveColumn(q.directionCol))
	lower := fmt.Sprintf("lower(%s)", raw)
	return fmt.Sprintf(`CASE
		WHEN %s IN ('进', 'in', 'c', 'credit') OR contains(%s, '收入') OR contains(%s, '存入') OR contains(%s, '转入') OR contains(%s, '贷') THEN '进'
		WHEN %s IN ('出', 'out', 'd', 'debit') OR contains(%s, '支出') OR contains(%s, '取出') OR contains(%s, '转出') OR contains(%s, '转账') OR contains(%s, '消费') OR contains(%s, '借') OR %s = 'o' OR %s = 'O' THEN '出'
		ELSE %s
	END`, lower, lower, lower, lower, lower, lower, lower, lower, lower, lower, lower, lower, lower, lower, raw)
}

func (q *duckDBGraphQuery) caseByDirection(outExpr, inExpr string) string {
	dir := q.directionExpr()
	return fmt.Sprintf("CASE WHEN %s = '出' THEN %s ELSE %s END", dir, outExpr, inExpr)
}

func (q *duckDBGraphQuery) trimExpr(column string) string {
	return fmt.Sprintf("TRIM(CAST(%s AS VARCHAR))", q.quote(column))
}

func (q *duckDBGraphQuery) whereClause() string {
	var parts []string
	if len(q.directions) > 0 {
		var qdirs []string
		for _, d := range q.directions {
			qdirs = append(qdirs, quoteSQLString(strings.TrimSpace(d)))
		}
		parts = append(parts, fmt.Sprintf("%s IN (%s)", q.directionExpr(), strings.Join(qdirs, ",")))
	}
	if q.startDate != "" {
		parts = append(parts, fmt.Sprintf("%s >= %s",
			q.quote(q.resolveColumn(q.timeCol)), quoteSQLString(flowStartBound(q.startDate))))
	}
	if q.endDate != "" {
		parts = append(parts, fmt.Sprintf("%s <= %s",
			q.quote(q.resolveColumn(q.timeCol)), quoteSQLString(flowEndBound(q.endDate))))
	}
	parts = append(parts, q.filterPredicates...)
	if len(parts) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(parts, " AND ")
}

func (q *duckDBGraphQuery) buildFilterPredicates(payload map[string]interface{}) []string {
	var predicates []string
	predicates = append(predicates, q.sideFilterPredicates(payload, "source_filters", true)...)
	predicates = append(predicates, q.sideFilterPredicates(payload, "target_filters", false)...)
	predicates = append(predicates, q.detailFilterPredicates(payload)...)
	return predicates
}

func (q *duckDBGraphQuery) sideFilterPredicates(payload map[string]interface{}, key string, sourceSide bool) []string {
	items, _ := payload[key].([]interface{})
	var predicates []string
	for _, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		values := filterEntryValues(entry)
		if len(values) == 0 {
			continue
		}
		column, _ := entry["column"].(string)
		exprs := q.filterColumnExprs(column, sourceSide)
		if pred := q.valuesPredicate(exprs, values); pred != "" {
			predicates = append(predicates, pred)
		}
	}
	return predicates
}

func (q *duckDBGraphQuery) detailFilterPredicates(payload map[string]interface{}) []string {
	items, _ := payload["detail_filters"].([]interface{})
	var predicates []string
	for _, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		values := filterEntryValues(entry)
		if len(values) == 0 {
			continue
		}
		column, _ := entry["column"].(string)
		resolved := q.resolveColumn(column)
		if resolved == "" {
			continue
		}
		mode := "exact"
		if m, _ := entry["match_mode"].(string); strings.ToLower(strings.TrimSpace(m)) == "fuzzy" {
			normalized := parser.NormalizeHeader(column)
			if normalized == "摘要说明" || normalized == "备注" || normalized == "交易摘要" || normalized == "交易备注" {
				mode = "fuzzy"
			}
		}
		if mode == "fuzzy" {
			if pred := q.containsPredicate([]string{q.trimExpr(resolved)}, values); pred != "" {
				predicates = append(predicates, pred)
			}
		} else {
			if pred := q.valuesPredicate([]string{q.trimExpr(resolved)}, values); pred != "" {
				predicates = append(predicates, pred)
			}
		}
	}
	return predicates
}

func (q *duckDBGraphQuery) filterColumnExprs(column string, sourceSide bool) []string {
	if column != "" {
		resolved := q.resolveColumn(column)
		if resolved != "" {
			return []string{q.trimExpr(resolved)}
		}
	}
	if sourceSide {
		return []string{
			q.caseByDirection(q.sourceExpr(), q.targetExpr()),
			q.caseByDirection(q.columnExpr(q.sourceAccount), q.columnExpr(q.targetCard)),
			q.caseByDirection(q.columnExpr(q.sourceName), q.columnExpr(q.targetName)),
		}
	}
	return []string{
		q.caseByDirection(q.targetExpr(), q.sourceExpr()),
		q.caseByDirection(q.columnExpr(q.targetCard), q.columnExpr(q.sourceAccount)),
		q.caseByDirection(q.columnExpr(q.targetName), q.columnExpr(q.sourceName)),
	}
}

func (q *duckDBGraphQuery) columnExpr(col string) string {
	if col == "" {
		return "''"
	}
	resolved := q.resolveColumn(col)
	if resolved == "" {
		return "''"
	}
	return q.trimExpr(resolved)
}

func (q *duckDBGraphQuery) valuesPredicate(exprs []string, values []string) string {
	values = cleanFilterValues(values)
	if len(exprs) == 0 || len(values) == 0 {
		return ""
	}
	quotedValues := make([]string, 0, len(values))
	for _, value := range values {
		quotedValues = append(quotedValues, quoteSQLString(value))
	}
	var parts []string
	for _, expr := range exprs {
		parts = append(parts, fmt.Sprintf("%s IN (%s)", expr, strings.Join(quotedValues, ",")))
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func (q *duckDBGraphQuery) containsPredicate(exprs []string, values []string) string {
	values = cleanFilterValues(values)
	if len(exprs) == 0 || len(values) == 0 {
		return ""
	}
	var parts []string
	for _, expr := range exprs {
		for _, value := range values {
			parts = append(parts, fmt.Sprintf("contains(%s, %s)", expr, quoteSQLString(value)))
		}
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

// ========== Graph Building via DuckDB ==========

func buildFlowFromDuckDB(sessionID string, mapping flowColumnMapping, payload map[string]interface{}) (*model.FlowGraph, map[string]interface{}, error) {
	tableName := getDuckDBAnalysisTable(sessionID)
	if tableName == "" || analysisEngine == nil || !analysisEngine.Available() {
		return nil, nil, fmt.Errorf("duckdb not available for session %s", sessionID)
	}

	// Get column names from DuckDB table
	columns, err := getDuckDBTableColumns(tableName)
	if err != nil {
		return nil, nil, err
	}

	q := newDuckDBGraphQuery(tableName, columns, mapping, payload)
	if q.amountCol == "" || q.timeCol == "" || q.directionCol == "" {
		return nil, nil, fmt.Errorf("missing required column mappings for duckdb query")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	start := time.Now()
	edges, summary, err := q.executeEdgesWithSummary(ctx)
	if err != nil {
		log.Warn().Err(err).Str("session_id", sessionID).Msg("duckdb_graph_build_failed")
		return nil, nil, err
	}

	nodes := q.buildNodes(ctx, edges, summary)
	log.Info().
		Str("session_id", sessionID).
		Str("table", tableName).
		Int("rows", summary["total_rows"].(int)).
		Int("nodes", len(nodes)).
		Int("edges", len(edges)).
		Dur("duration", time.Since(start)).
		Msg("duckdb_graph_built")

	graph := &model.FlowGraph{
		Nodes: nodes,
		Edges: edges,
		Meta: map[string]interface{}{
			"total_nodes":    len(nodes),
			"total_edges":    len(edges),
			"rendered_nodes": len(nodes),
			"rendered_edges": len(edges),
			"edge_limit":     q.maxEdges,
			"truncated":      q.maxEdges > 0 && len(edges) >= q.maxEdges,
			"duckdb":         true,
		},
	}
	return graph, summary, nil
}

func (q *duckDBGraphQuery) executeEdgesWithSummary(ctx context.Context) ([]model.FlowEdge, map[string]interface{}, error) {
	dirCol := q.directionExpr()
	amountExpr := fmt.Sprintf("TRY_CAST(%s AS DOUBLE)", q.quote(q.resolveColumn(q.amountCol)))
	timeCol := q.quote(q.resolveColumn(q.timeCol))
	where := q.whereClause()

	sql := fmt.Sprintf(`
		WITH filtered AS (
			SELECT
				CASE WHEN %s = '出' THEN %s ELSE %s END AS src_id,
				CASE WHEN %s = '出' THEN %s ELSE %s END AS tgt_id,
				%s AS amt,
				%s AS tx_time,
				%s AS dir
			FROM %s
			%s
		),
		summary AS (
			SELECT
				COUNT(*) AS total_rows,
				COALESCE(SUM(amt), 0) AS total_amount,
				COALESCE(SUM(CASE WHEN dir = '进' THEN amt ELSE 0 END), 0) AS in_amount,
				COALESCE(SUM(CASE WHEN dir = '进' THEN 1 ELSE 0 END), 0) AS in_count,
				COALESCE(SUM(CASE WHEN dir != '进' THEN amt ELSE 0 END), 0) AS out_amount,
				COALESCE(SUM(CASE WHEN dir != '进' THEN 1 ELSE 0 END), 0) AS out_count,
				MIN(tx_time) AS start_time,
				MAX(tx_time) AS end_time
			FROM filtered
		),
		edges AS (
			SELECT src_id AS source, tgt_id AS target,
				SUM(amt) AS amount, COUNT(*) AS tx_count,
				AVG(amt) AS avg_amount, MAX(amt) AS max_amount,
				MIN(tx_time) AS first_time, MAX(tx_time) AS last_time
			FROM filtered
			WHERE src_id != '' AND tgt_id != '' AND src_id != tgt_id
			GROUP BY src_id, tgt_id
			ORDER BY amount DESC
			LIMIT %d
		)
		SELECT e.source, e.target, e.amount, e.tx_count, e.avg_amount, e.max_amount, e.first_time, e.last_time,
			s.total_rows, s.total_amount, s.in_amount, s.in_count, s.out_amount, s.out_count, s.start_time, s.end_time
		FROM summary s
		LEFT JOIN edges e ON TRUE
	`, dirCol, q.sourceExpr(), q.targetExpr(),
		dirCol, q.targetExpr(), q.sourceExpr(),
		amountExpr, timeCol, dirCol, q.quote(q.tableName), where, q.maxEdges)

	rows, err := analysisEngine.ExecSQLJSON(ctx, sql)
	if err != nil {
		return nil, nil, err
	}

	edges := make([]model.FlowEdge, 0, len(rows))
	summary := make(map[string]interface{})
	for _, row := range rows {
		if summary["total_rows"] == nil {
			summary = map[string]interface{}{
				"total_rows":   int(getFloat(row, "total_rows")),
				"total_amount": getFloat(row, "total_amount"),
				"in_count":     int(getFloat(row, "in_count")),
				"in_amount":    getFloat(row, "in_amount"),
				"out_count":    int(getFloat(row, "out_count")),
				"out_amount":   getFloat(row, "out_amount"),
				"start_time":   getStr(row, "start_time"),
				"end_time":     getStr(row, "end_time"),
			}
		}
		source := getStr(row, "source")
		target := getStr(row, "target")
		if source == "" || target == "" {
			continue
		}
		e := model.FlowEdge{
			ID:      fmt.Sprintf("edge-%s-%s", source, target),
			Source:  source,
			Target:  target,
			Amount:  getFloat(row, "amount"),
			TxCount: int(getFloat(row, "tx_count")),
		}
		e.AvgAmount = getFloat(row, "avg_amount")
		e.MaxAmount = getFloat(row, "max_amount")
		firstTime := getStr(row, "first_time")
		if firstTime != "" {
			e.FirstTime = &firstTime
		}
		lastTime := getStr(row, "last_time")
		if lastTime != "" {
			e.LastTime = &lastTime
		}
		e.Label = fmt.Sprintf("%.2f (%d笔)", e.Amount, e.TxCount)
		edges = append(edges, e)
	}
	return edges, summary, nil
}

func (q *duckDBGraphQuery) buildNodes(ctx context.Context, edges []model.FlowEdge, summary map[string]interface{}) []model.FlowNode {
	nodeMap := make(map[string]*model.FlowNode)
	for _, e := range edges {
		for _, id := range []string{e.Source, e.Target} {
			if _, ok := nodeMap[id]; ok {
				continue
			}
			nodeMap[id] = &model.FlowNode{
				ID:    id,
				Label: id,
				Role:  "entity",
				Tags:  []string{},
			}
		}
	}
	q.enrichNodesFromDuckDB(ctx, nodeMap)
	for _, e := range edges {
		s := nodeMap[e.Source]
		t := nodeMap[e.Target]
		if s != nil {
			s.AmountOut += e.Amount
			s.TxCount += e.TxCount
			s.OutCount += e.TxCount
			if e.FirstTime != nil && *e.FirstTime != "" {
				if s.FirstTime == nil || *e.FirstTime < *s.FirstTime {
					s.FirstTime = e.FirstTime
				}
			}
			if e.LastTime != nil && *e.LastTime != "" {
				if s.LastTime == nil || *e.LastTime > *s.LastTime {
					s.LastTime = e.LastTime
				}
			}
		}
		if t != nil {
			t.AmountIn += e.Amount
			t.TxCount += e.TxCount
			t.InCount += e.TxCount
			if e.FirstTime != nil && *e.FirstTime != "" {
				if t.FirstTime == nil || *e.FirstTime < *t.FirstTime {
					t.FirstTime = e.FirstTime
				}
			}
			if e.LastTime != nil && *e.LastTime != "" {
				if t.LastTime == nil || *e.LastTime > *t.LastTime {
					t.LastTime = e.LastTime
				}
			}
		}
	}
	for _, n := range nodeMap {
		n.Degree = n.InCount + n.OutCount
	}
	nodes := make([]model.FlowNode, 0, len(nodeMap))
	for _, n := range nodeMap {
		nodes = append(nodes, *n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes
}

func (q *duckDBGraphQuery) enrichNodesFromDuckDB(ctx context.Context, nodeMap map[string]*model.FlowNode) {
	if len(nodeMap) == 0 {
		return
	}
	var nodeIDs []string
	for id := range nodeMap {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	quotedIDs := make([]string, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		quotedIDs = append(quotedIDs, quoteSQLString(id))
	}
	where := q.whereClause()
	sql := fmt.Sprintf(`
		WITH filtered AS (
			SELECT
				%s AS src_node,
				%s AS tgt_node,
				%s AS src_account,
				%s AS src_name,
				%s AS src_id_no,
				%s AS tgt_account,
				%s AS tgt_name,
				%s AS tgt_id_no
			FROM %s
			%s
		),
		identities AS (
			SELECT src_node AS node_id, src_account AS account_no, src_name AS account_name, src_id_no AS id_number FROM filtered
			UNION ALL
			SELECT tgt_node AS node_id, tgt_account AS account_no, tgt_name AS account_name, tgt_id_no AS id_number FROM filtered
		)
		SELECT
			node_id,
			MAX(NULLIF(account_no, '')) AS account_no,
			MAX(NULLIF(account_name, '')) AS account_name,
			MAX(NULLIF(id_number, '')) AS id_number
		FROM identities
		WHERE node_id IN (%s) AND node_id != ''
		GROUP BY node_id
	`, q.caseByDirection(q.sourceExpr(), q.targetExpr()),
		q.caseByDirection(q.targetExpr(), q.sourceExpr()),
		q.caseByDirection(q.columnExpr(q.sourceAccount), q.columnExpr(q.targetCard)),
		q.caseByDirection(q.columnExpr(q.sourceName), q.columnExpr(q.targetName)),
		q.caseByDirection(q.columnExpr(q.sourceID), q.columnExpr(q.targetID)),
		q.caseByDirection(q.columnExpr(q.targetCard), q.columnExpr(q.sourceAccount)),
		q.caseByDirection(q.columnExpr(q.targetName), q.columnExpr(q.sourceName)),
		q.caseByDirection(q.columnExpr(q.targetID), q.columnExpr(q.sourceID)),
		q.quote(q.tableName), where, strings.Join(quotedIDs, ","))

	rows, err := analysisEngine.ExecSQLJSON(ctx, sql)
	if err != nil {
		return
	}
	for _, row := range rows {
		id := getStr(row, "node_id")
		node := nodeMap[id]
		if node == nil {
			continue
		}
		if accountNo := getStr(row, "account_no"); accountNo != "" {
			node.AccountNo = accountNo
		}
		if accountName := getStr(row, "account_name"); accountName != "" {
			node.AccountName = accountName
			if node.Label == "" || node.Label == node.ID {
				node.Label = accountName
			}
		}
		if idNumber := getStr(row, "id_number"); idNumber != "" {
			node.IDNumber = idNumber
		}
	}
}

// ========== Edge Detail via DuckDB ==========

func queryEdgeDetailFromDuckDB(sessionID string, mapping flowColumnMapping, payload EdgeDetailPayload) ([]map[string]interface{}, int, float64, []string, error) {
	tableName := getDuckDBAnalysisTable(sessionID)
	if tableName == "" || analysisEngine == nil || !analysisEngine.Available() {
		return nil, 0, 0, nil, fmt.Errorf("duckdb not available")
	}

	columns, err := getDuckDBTableColumns(tableName)
	if err != nil {
		return nil, 0, 0, nil, err
	}

	q := newDuckDBGraphQuery(tableName, columns, mapping, map[string]interface{}{
		"source_filters":   payload.SourceFilters,
		"target_filters":   payload.TargetFilters,
		"detail_filters":   payload.DetailFilters,
		"directions":       payload.Directions,
		"start_date":       payload.StartDate,
		"end_date":         payload.EndDate,
		"source_column":    payload.SourceColumn,
		"target_column":    payload.TargetColumn,
		"amount_column":    payload.AmountColumn,
		"time_column":      payload.TimeColumn,
		"direction_column": payload.DirectionColumn,
	})

	// Add source/target edge filter
	sourceExpr := q.caseByDirection(q.sourceExpr(), q.targetExpr())
	targetExpr := q.caseByDirection(q.targetExpr(), q.sourceExpr())
	if payload.Source != "" {
		q.filterPredicates = append(q.filterPredicates,
			fmt.Sprintf("TRIM(%s) = %s", sourceExpr, quoteSQLString(payload.Source)))
	}
	if payload.Target != "" {
		q.filterPredicates = append(q.filterPredicates,
			fmt.Sprintf("TRIM(%s) = %s", targetExpr, quoteSQLString(payload.Target)))
	}
	if payload.Source == "" && payload.Target == "" {
		// If neither provided, the edge detail query needs source and target
		return nil, 0, 0, nil, nil
	}

	where := q.whereClause()

	amountExpr := fmt.Sprintf("TRY_CAST(%s AS DOUBLE)", q.quote(q.resolveColumn(q.amountCol)))

	limit := payload.Limit
	if limit <= 0 {
		limit = 10000
	}

	sql := fmt.Sprintf(`
		SELECT
			COUNT(*) AS total_rows,
			COALESCE(SUM(%s), 0) AS total_amount
		FROM %s
		%s
	`, amountExpr, q.quote(q.tableName), where)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	summaryRows, err := analysisEngine.ExecSQLJSON(ctx, sql)
	if err != nil {
		return nil, 0, 0, nil, err
	}
	totalRows := 0
	totalAmount := 0.0
	if len(summaryRows) > 0 {
		totalRows = int(getFloat(summaryRows[0], "total_rows"))
		totalAmount = getFloat(summaryRows[0], "total_amount")
	}

	detailSQL := fmt.Sprintf(`SELECT * FROM %s %s LIMIT %d`, q.quote(q.tableName), where, limit)
	detailRows, err := analysisEngine.ExecSQLJSON(ctx, detailSQL)
	if err != nil {
		return nil, totalRows, totalAmount, columns, err
	}

	return detailRows, totalRows, totalAmount, columns, nil
}

// ========== Column Values via DuckDB ==========

func queryColumnValuesFromDuckDB(sessionID, column string, search string, limit int) ([]map[string]interface{}, error) {
	tableName := getDuckDBAnalysisTable(sessionID)
	if tableName == "" || analysisEngine == nil || !analysisEngine.Available() {
		return nil, fmt.Errorf("duckdb not available")
	}

	resolved := parser.NormalizeHeader(column)
	if resolved == "" {
		return nil, fmt.Errorf("invalid column name")
	}

	columns, err := getDuckDBTableColumns(tableName)
	if err != nil {
		return nil, err
	}

	var actualColumn string
	for _, col := range columns {
		if parser.NormalizeHeader(col) == resolved {
			actualColumn = col
			break
		}
	}
	if actualColumn == "" {
		return nil, fmt.Errorf("column not found in duckdb table")
	}

	if limit <= 0 {
		limit = 300
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	quotedCol := fmt.Sprintf(`"%s"`, actualColumn)
	quotedTable := fmt.Sprintf(`"%s"`, tableName)

	var sql string
	search = strings.TrimSpace(search)
	if search != "" {
		sql = fmt.Sprintf(
			`SELECT DISTINCT %s AS value, COUNT(*) AS cnt FROM %s WHERE contains(TRIM(CAST(%s AS VARCHAR)), %s) GROUP BY %s ORDER BY cnt DESC LIMIT %d`,
			quotedCol, quotedTable, quotedCol, quoteSQLString(search), quotedCol, limit,
		)
	} else {
		sql = fmt.Sprintf(
			`SELECT %s AS value, COUNT(*) AS cnt FROM %s GROUP BY %s ORDER BY cnt DESC LIMIT %d`,
			quotedCol, quotedTable, quotedCol, limit,
		)
	}

	rows, err := analysisEngine.ExecSQLJSON(ctx, sql)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		val := getStr(row, "value")
		if val == "" {
			continue
		}
		result = append(result, map[string]interface{}{
			"value": val,
			"count": int(getFloat(row, "cnt")),
		})
	}
	return result, nil
}

// ========== Helpers ==========

func getDuckDBTableColumns(tableName string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sql := fmt.Sprintf("SELECT column_name FROM information_schema.columns WHERE table_name = '%s' ORDER BY ordinal_position", tableName)
	rows, err := analysisEngine.ExecSQLJSON(ctx, sql)
	if err != nil {
		return nil, err
	}

	columns := make([]string, 0, len(rows))
	for _, row := range rows {
		col := getStr(row, "column_name")
		if col != "" {
			columns = append(columns, col)
		}
	}
	return columns, nil
}

func getStr(row map[string]interface{}, key string) string {
	if v, ok := row[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(row map[string]interface{}, key string) float64 {
	switch v := row[key].(type) {
	case float64:
		return v
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	}
	return 0
}

func getInt(row map[string]interface{}, key string) int {
	switch v := row[key].(type) {
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return 0
}

func quoteSQLString(s string) string {
	return `'` + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "/"), `'`, `''`) + `'`
}

func cleanFilterValues(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		v := parser.CleanText(value)
		if s, ok := v.(string); ok {
			value = s
		}
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func filterEntryValues(entry map[string]interface{}) []string {
	vals, _ := entry["values"].([]interface{})
	var values []string
	for _, v := range vals {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			values = append(values, s)
		}
	}
	return values
}

func flowStartBound(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= len("2006-01-02") {
		return value + " 00:00:00"
	}
	return value
}

func flowEndBound(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= len("2006-01-02") {
		return value + " 23:59:59"
	}
	return value
}
