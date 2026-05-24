package api

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"

	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/dbimport"
	"github.com/etl/backend/internal/etl"
	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/rules"
	"github.com/etl/backend/internal/scanner"
	"github.com/etl/backend/internal/storage"
)

var (
	cfg       *config.Config
	store     *storage.FileStorage
	dbStore   *dbimport.Store
	dbService *dbimport.Service
)

const (
	defaultFlowEdgeLimit = 600
	auditFlowEdgeLimit   = 5000
)

// Setup initializes the API package with config
func Setup(c *config.Config) {
	cfg = c
	store = storage.NewFileStorage(c.UploadDir, c.OutputDir)
	dbStore = dbimport.NewStore(filepath.Join(c.RootDir, "backend", "data", "db_import"))
	dbService = dbimport.NewService(dbStore, c.UploadDir)
}

// RegisterRoutes registers all API routes on the Gin router
func RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api")
	{
		api.POST("/process", HandleProcess)
		api.GET("/download/:job_id", HandleDownload)
		api.GET("/flow/history", HandleFlowHistory)
		api.GET("/flow/history/:job_id", HandleLoadHistoryFlow)
		api.GET("/flow/edge-detail", HandleFlowEdgeDetail)
		api.POST("/flow/edge-detail/imported", HandleImportedFlowEdgeDetail)
		api.POST("/flow/upload", HandleUploadFlowData)
		api.POST("/flow/import", HandleImportFlowData)
		api.POST("/flow/mapping-rules", HandleSaveFlowMapping)
		api.GET("/flow/template", HandleDownloadFlowTemplate)
		api.POST("/flow/build", HandleBuildImportedFlow)
		api.POST("/ai/analyze", HandleAnalyzeFlowWithAI)
		api.POST("/flow/direction-rules", HandleSaveFlowDirectionRules)
		api.POST("/flow/direction-check", HandleCheckFlowDirectionValues)
		api.POST("/flow/values", HandleFlowFieldValues)
		api.GET("/health", HandleHealth)
		api.GET("/files/current", HandleCurrentFiles)
		api.POST("/rules/analyze", HandleAnalyzeRules)
		api.POST("/rules/confirm", HandleConfirmRules)
		registerDBImportRoutes(api)
	}
}

// HandleProcess handles file upload and ETL pipeline
func HandleProcess(c *gin.Context) {
	log.Info().Msg("process_files_start")

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(400, gin.H{"detail": "invalid multipart form: " + err.Error()})
		return
	}

	txFiles := form.File["transaction_files"]
	acctFiles := form.File["account_files"]
	labelFiles := form.File["label_file"]

	batchDir := filepath.Join(cfg.UploadDir, "current")
	os.RemoveAll(batchDir)

	for _, f := range txFiles {
		path := filepath.Join(batchDir, "transactions", safeName(f.Filename))
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := c.SaveUploadedFile(f, path); err != nil {
			c.JSON(500, gin.H{"detail": "save upload: " + err.Error()})
			return
		}
	}
	for _, f := range acctFiles {
		path := filepath.Join(batchDir, "accounts", safeName(f.Filename))
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := c.SaveUploadedFile(f, path); err != nil {
			c.JSON(500, gin.H{"detail": "save upload: " + err.Error()})
			return
		}
	}
	for _, f := range labelFiles {
		path := filepath.Join(batchDir, "labels", safeName(f.Filename))
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := c.SaveUploadedFile(f, path); err != nil {
			continue
		}
		break
	}

	jobID := etl.GenerateJobID()
	result, err := etl.RunPipeline(batchDir, cfg.OutputDir, jobID)
	if err != nil {
		c.JSON(400, gin.H{"detail": err.Error()})
		return
	}

	preview, columns := etl.BuildPreview(result.Transactions, 100)
	summary := etl.BuildSummary(result.Transactions)
	flowGraph := etl.BuildFlowGraph(result.Transactions, 600)

	resp := model.ProcessResponse{
		JobID:       jobID,
		Rows:        len(result.Transactions),
		Columns:     columns,
		Preview:     preview,
		Report:      result.Report,
		Summary:     summary,
		FlowGraph:   flowGraph,
		DownloadURL: fmt.Sprintf("/api/download/%s", jobID),
	}

	c.JSON(200, resp)
}

// HandleDownload handles file download
func HandleDownload(c *gin.Context) {
	jobID := c.Param("job_id")
	pattern := filepath.Join(cfg.OutputDir, "*"+jobID+"*")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		c.JSON(404, gin.H{"detail": "导出文件不存在或已被清理。"})
		return
	}
	path := matches[0]
	c.FileAttachment(path, filepath.Base(path))
}

// HandleFlowHistory returns list of flow sessions
func HandleFlowHistory(c *gin.Context) {
	var items []map[string]interface{}

	sessionsDir := filepath.Join(cfg.UploadDir, "flow_sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(200, gin.H{"items": []map[string]interface{}{}})
			return
		}
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		item, err := summarizeFlowSession(entry.Name())
		if err == nil {
			items = append(items, item)
		}
	}
	if items == nil {
		items = []map[string]interface{}{}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["updated_at"].(int64) > items[j]["updated_at"].(int64)
	})
	c.JSON(200, gin.H{"items": items})
}

// HandleLoadHistoryFlow loads a specific flow session
func HandleLoadHistoryFlow(c *gin.Context) {
	jobID := c.Param("job_id")
	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", jobID)
	if _, err := os.Stat(sessionDir); err != nil {
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"detail": "session not found: " + jobID})
			return
		}
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}

	columns, sample, totalRows := extractFileColumns(sessionDir)
	files := listFlowSessionFiles(sessionDir)

	var signature string
	var mappingRule map[string]interface{}
	if len(columns) > 0 {
		signature = rules.GenerateColumnSignature(columns)
		mappingRule = rules.FlowMappingRule(signature)
	}

	c.JSON(200, gin.H{
		"session_id":   jobID,
		"job_id":       jobID,
		"name":         flowSessionName(jobID, files),
		"rows":         totalRows,
		"columns":      columns,
		"files":        files,
		"sample":       sample,
		"signature":    signature,
		"mapping_rule": mappingRule,
	})
}

func summarizeFlowSession(sessionID string) (map[string]interface{}, error) {
	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID)
	var size int64
	var updatedAt int64
	files := []string{}

	err := filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.ModTime().Unix() > updatedAt {
			updatedAt = info.ModTime().Unix()
		}
		if info.IsDir() {
			return nil
		}
		size += info.Size()
		if parser.SupportedSuffixes[strings.ToLower(filepath.Ext(path))] {
			if rel, err := filepath.Rel(sessionDir, path); err == nil {
				files = append(files, rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if updatedAt == 0 {
		if info, err := os.Stat(sessionDir); err == nil {
			updatedAt = info.ModTime().Unix()
		}
	}

	return map[string]interface{}{
		"id":         sessionID,
		"job_id":     sessionID,
		"name":       flowSessionName(sessionID, files),
		"size":       size,
		"updated_at": updatedAt,
		"status":     "exists",
	}, nil
}

func listFlowSessionFiles(sessionDir string) []string {
	files := []string{}
	filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !parser.SupportedSuffixes[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if rel, err := filepath.Rel(sessionDir, path); err == nil {
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func flowSessionName(sessionID string, files []string) string {
	if len(files) == 0 {
		return sessionID
	}
	return filepath.Base(files[0])
}

// HandleFlowEdgeDetail returns edge detail for a job
func HandleFlowEdgeDetail(c *gin.Context) {
	jobID := c.Query("job_id")
	source := c.Query("source")
	target := c.Query("target")
	if jobID == "" || source == "" || target == "" {
		c.JSON(400, gin.H{"detail": "job_id, source, target required"})
		return
	}
	c.JSON(200, gin.H{"job_id": jobID, "source": source, "target": target, "rows": []map[string]interface{}{}})
}

// HandleImportedFlowEdgeDetail handles edge detail for imported data
func HandleImportedFlowEdgeDetail(c *gin.Context) {
	var payload struct {
		SessionID    string `json:"session_id"`
		SourceColumn string `json:"source_column"`
		TargetColumn string `json:"target_column"`
		AmountColumn string `json:"amount_column"`
		TimeColumn   string `json:"time_column"`
		Source       string `json:"source"`
		Target       string `json:"target"`
		Limit        int    `json:"limit"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	if payload.SessionID == "" {
		c.JSON(400, gin.H{"detail": "session_id required"})
		return
	}
	if payload.Limit <= 0 || payload.Limit > 100000 {
		payload.Limit = 10000
	}

	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", payload.SessionID)
	rows := queryEdgeRows(sessionDir, payload)
	// Calculate columns from data
	var columns []string
	if len(rows) > 0 {
		for k := range rows[0] {
			columns = append(columns, k)
		}
	}
	// Calculate total amount
	var totalAmount float64
	for _, row := range rows {
		if v, ok := row[payload.AmountColumn]; ok {
			if s, ok := v.(string); ok {
				totalAmount += parser.ToNumber(s)
			}
		}
	}
	// Apply limit
	totalRows := len(rows)
	returnedRows := totalRows
	truncated := false
	if payload.Limit > 0 && totalRows > payload.Limit {
		returnedRows = payload.Limit
		truncated = true
	}
	resultRows := rows
	if truncated {
		resultRows = rows[:returnedRows]
	}
	c.JSON(200, gin.H{
		"job_id":        payload.SessionID,
		"source":        payload.Source,
		"target":        payload.Target,
		"total_rows":    totalRows,
		"returned_rows": returnedRows,
		"amount":        totalAmount,
		"columns":       columns,
		"rows":          resultRows,
		"truncated":     truncated,
	})
}

// HandleUploadFlowData handles flow data upload
func HandleUploadFlowData(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(400, gin.H{"detail": err.Error()})
		return
	}
	files := form.File["files"]
	sessionID := uuid.New().String()[:12]
	for _, f := range files {
		path := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID, safeName(f.Filename))
		os.MkdirAll(filepath.Dir(path), 0755)
		c.SaveUploadedFile(f, path)
	}
	c.JSON(200, gin.H{"session_id": sessionID, "files": len(files)})
}

// HandleImportFlowData imports flow data
// HandleImportFlowData accepts file uploads and returns ImportedDataset
func HandleImportFlowData(c *gin.Context) {
	// Accept multipart FormData with files
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(400, gin.H{"detail": err.Error()})
		return
	}
	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(400, gin.H{"detail": "请上传数据文件"})
		return
	}

	// Create session and save files
	sessionID := uuid.New().String()[:12]
	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID)
	var fileNames []string
	for _, f := range files {
		path := filepath.Join(sessionDir, safeName(f.Filename))
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := c.SaveUploadedFile(f, path); err != nil {
			continue
		}
		fileNames = append(fileNames, f.Filename)
	}

	// Extract columns and sample data from uploaded files
	columns, sample, totalRows := extractFileColumns(sessionDir)
	if len(columns) == 0 && len(files) > 0 {
		firstPath := filepath.Join(sessionDir, safeName(files[0].Filename))
		columns, sample, totalRows = readFileColumns(firstPath)
	}

	// Check for existing mapping rules
	var mappingRule map[string]interface{}
	if len(columns) > 0 {
		signature := rules.GenerateColumnSignature(columns)
		mappingRule = rules.FlowMappingRule(signature)
	}

	c.JSON(200, gin.H{
		"session_id":   sessionID,
		"rows":         totalRows,
		"columns":      columns,
		"files":        fileNames,
		"sample":       sample,
		"mapping_rule": mappingRule,
	})
}
func HandleSaveFlowMapping(c *gin.Context) {
	var payload struct {
		Columns []string               `json:"columns"`
		Mapping map[string]interface{} `json:"mapping"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	if len(payload.Columns) == 0 {
		c.JSON(400, gin.H{"detail": "columns required"})
		return
	}
	if payload.Mapping == nil {
		c.JSON(400, gin.H{"detail": "mapping required"})
		return
	}

	signature := rules.GenerateColumnSignature(payload.Columns)
	rule := map[string]interface{}{
		"signature":      signature,
		"source_columns": payload.Columns,
		"mapping":        payload.Mapping,
		"updated_at":     time.Now().Format("2006-01-02 15:04:05"),
	}
	if _, err := rules.SaveFlowMappingRule(rule); err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}

	c.JSON(200, gin.H{"status": "ok", "signature": signature})
}

// HandleDownloadFlowTemplate downloads the flow template
func HandleDownloadFlowTemplate(c *gin.Context) {
	templatePath := cfg.FlowTemplatePath
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(templatePath), 0755)
		f := excelize.NewFile()
		columns := []string{"交易方户名", "交易方账户", "交易方身份证号", "交易方标签", "交易时间", "交易金额", "收付标志",
			"交易余额", "交易对手账卡号", "对手户名", "对手身份证号", "对手标签", "摘要说明", "备注"}
		for i, h := range columns {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			f.SetCellValue("Sheet1", cell, h)
		}
		f.SaveAs(templatePath)
		f.Close()
	}
	c.FileAttachment(templatePath, "flow_template.xlsx")
}

// HandleBuildImportedFlow builds flow graph from imported data
func HandleBuildImportedFlow(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}

	sessionID, _ := payload["session_id"].(string)
	if sessionID == "" {
		c.JSON(400, gin.H{"detail": "session_id required"})
		return
	}

	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID)
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		c.JSON(404, gin.H{"detail": "session not found"})
		return
	}

	// Extract column mapping from payload
	sourceCol, _ := payload["source_column"].(string)
	accountCol, _ := payload["source_account_column"].(string)
	targetCol, _ := payload["target_column"].(string)
	targetCardCol, _ := payload["target_card_column"].(string)
	amountCol, _ := payload["amount_column"].(string)
	timeCol, _ := payload["time_column"].(string)
	directionCol, _ := payload["direction_column"].(string)
	directionValues, _ := payload["direction_values"].([]interface{})

	// Parse direction values mapping
	dirMap := make(map[string]string)
	for _, v := range directionValues {
		if m, ok := v.(map[string]interface{}); ok {
			src, _ := m["source"].(string)
			dst, _ := m["target"].(string)
			if src != "" && dst != "" {
				dirMap[src] = dst
			}
		}
	}

	// Read source files and build transaction rows
	txns := readSessionData(sessionDir, sourceCol, accountCol, targetCol, targetCardCol, amountCol, timeCol, directionCol, dirMap)

	// Check for unknown direction values
	unknownDirs := checkUnknownDirections(txns)
	if len(unknownDirs) > 0 {
		c.JSON(400, gin.H{
			"detail": map[string]interface{}{
				"code":    "unknown_flow_directions",
				"message": "\u53d1\u73b0\u672a\u77e5\u6536\u4ed8\u6807\u5fd7\uff1a" + strings.Join(unknownDirs, "\u3001"),
				"values":  unknownDirs,
			},
		})
		return
	}

	// Build flow graph from unfiltered data
	// Apply source/target filters if provided
	filteredTxns := applyFilters(txns, payload)

	// Build preview and flow graph
	preview, columns := etl.BuildPreview(filteredTxns, 200)
	summary := etl.BuildSummary(filteredTxns)
	flowGraph := etl.BuildFlowGraph(filteredTxns, flowEdgeLimit(payload))

	c.JSON(200, gin.H{
		"nodes":      flowGraph.Nodes,
		"edges":      flowGraph.Edges,
		"meta":       flowGraph.Meta,
		"columns":    columns,
		"preview":    preview,
		"rows":       len(filteredTxns),
		"session_id": sessionID,
		"summary":    summary,
	})
}

// HandleAnalyzeFlowWithAI handles AI-powered flow analysis
func HandleAnalyzeFlowWithAI(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	c.JSON(200, gin.H{
		"report":   "AI analysis not configured. Set DEEPSEEK_API_KEY for AI-powered analysis.",
		"filtered": 0, "session_id": payload["session_id"],
	})
}

// HandleSaveFlowDirectionRules saves direction aliases
func HandleSaveFlowDirectionRules(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	aliases, _ := payload["aliases"].(map[string]interface{})
	strAliases := make(map[string]string)
	for k, v := range aliases {
		strAliases[k] = fmt.Sprint(v)
	}
	_, err := rules.SaveDirectionAliases(strAliases)
	if err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "ok"})
}

// HandleCheckFlowDirectionValues checks direction values
func HandleCheckFlowDirectionValues(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}

	sessionID, _ := payload["session_id"].(string)
	column, _ := payload["column"].(string)
	if sessionID == "" || column == "" {
		c.JSON(400, gin.H{"detail": "session_id and column required"})
		return
	}

	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID)
	rawValues := extractColumnValues(sessionDir, column, 500)
	aliases := make(map[string]string)
	for k, v := range rules.LoadDirectionAliases() {
		aliases[strings.TrimSpace(k)] = v
		aliases[parser.NormalizeHeader(k)] = v
	}
	var values []string
	for _, v := range rawValues {
		normalized := normalizeFlowDirection(v, aliases)
		if normalized != "出" && normalized != "进" {
			values = append(values, v)
		}
	}
	c.JSON(200, gin.H{
		"unknown_values": values,
		"session_id":     sessionID,
	})
}

// HandleFlowFieldValues returns field values for a session
func HandleFlowFieldValues(c *gin.Context) {
	var payload struct {
		SessionID string `json:"session_id"`
		Column    string `json:"column"`
		Search    string `json:"search"`
		Limit     int    `json:"limit"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	if payload.SessionID == "" || payload.Column == "" {
		c.JSON(400, gin.H{"detail": "session_id and column required"})
		return
	}
	if payload.Limit <= 0 || payload.Limit > 1000 {
		payload.Limit = 300
	}

	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", payload.SessionID)
	values := extractColumnValues(sessionDir, payload.Column, payload.Limit)
	c.JSON(200, gin.H{
		"values":     values,
		"session_id": payload.SessionID,
	})
}

// HandleHealth returns health status
func HandleHealth(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

// HandleCurrentFiles lists current uploads and rule samples
func HandleCurrentFiles(c *gin.Context) {
	uploads := listLocalFiles(filepath.Join(cfg.UploadDir, "current"))
	samples := listLocalFiles(filepath.Join(cfg.RuleSamplesDir, "current"))
	c.JSON(200, gin.H{"uploads": uploads, "rule_samples": samples})
}

// HandleAnalyzeRules analyzes rule samples
func HandleAnalyzeRules(c *gin.Context) {
	providerStr := c.PostForm("provider")
	if providerStr != "alipay" && providerStr != "wechat" && providerStr != "bank" {
		c.JSON(400, gin.H{"detail": "provider 必须是 alipay、wechat 或 bank"})
		return
	}
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(400, gin.H{"detail": err.Error()})
		return
	}
	files := form.File["sample_files"]
	batchDir := filepath.Join(cfg.RuleSamplesDir, "current")
	os.RemoveAll(batchDir)
	os.MkdirAll(batchDir, 0755)
	for _, f := range files {
		path := filepath.Join(batchDir, safeName(f.Filename))
		c.SaveUploadedFile(f, path)
	}
	c.JSON(200, gin.H{
		"provider": providerStr, "candidates": []map[string]interface{}{},
		"suggestions": []map[string]interface{}{},
	})
}

// HandleConfirmRules confirms and saves custom rules
func HandleConfirmRules(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	providerStr, _ := payload["provider"].(string)
	rule, _ := payload["rule"].(map[string]interface{})
	if providerStr == "" || rule == nil {
		c.JSON(400, gin.H{"detail": "provider and rule required"})
		return
	}
	data, err := rules.SaveCustomRule(providerStr, rule)
	if err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "ok", "rules": data.Providers[providerStr]})
}

func safeName(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(filepath.Base(name))
}

func listLocalFiles(dir string) []map[string]interface{} {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return []map[string]interface{}{}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []map[string]interface{}{}
	}
	var files []map[string]interface{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		files = append(files, map[string]interface{}{
			"name": e.Name(), "path": e.Name(),
			"size": info.Size(), "updated_at": info.ModTime().Unix(),
		})
	}
	return files
}

// ========== Import/Flow helper functions ==========

// extractFileColumns scans a directory and extracts columns and sample data
func extractFileColumns(sessionDir string) ([]string, []map[string]interface{}, int) {
	scan, err := scanner.ScanDirectory(sessionDir)
	if err != nil || len(scan.Transactions) == 0 {
		return nil, nil, 0
	}

	cand := scan.Transactions[0]
	columns := cand.Columns
	if len(columns) == 0 {
		return nil, nil, 0
	}

	// Read data rows
	var rows [][]string
	if cand.SheetName != "" {
		rows, _ = parser.ReadExcelSheet(cand.Path, cand.SheetName)
	} else {
		rows, _ = parser.ReadCSVFile(cand.Path)
	}
	if len(rows) < 2 {
		return columns, nil, 0
	}

	totalRows := len(rows) - 1
	sample := rowsToSample(rows, columns, 20)
	return columns, sample, totalRows
}

// readFileColumns directly reads a file and extracts columns/sample
func readFileColumns(path string) ([]string, []map[string]interface{}, int) {
	ext := strings.ToLower(filepath.Ext(path))
	if parser.ExcelSuffixes[ext] {
		sheets, err := parser.ReadExcelFile(path)
		if err != nil || len(sheets) == 0 {
			return nil, nil, 0
		}
		for _, rows := range sheets {
			if len(rows) < 2 {
				continue
			}
			columns := make([]string, len(rows[0]))
			for i, c := range rows[0] {
				columns[i] = parser.NormalizeHeader(c)
			}
			totalRows := len(rows) - 1
			sample := rowsToSample(rows, columns, 20)
			return columns, sample, totalRows
		}
	} else {
		rows, err := parser.ReadCSVFile(path)
		if err != nil || len(rows) < 2 {
			return nil, nil, 0
		}
		columns := make([]string, len(rows[0]))
		for i, c := range rows[0] {
			columns[i] = parser.NormalizeHeader(c)
		}
		totalRows := len(rows) - 1
		sample := rowsToSample(rows, columns, 20)
		return columns, sample, totalRows
	}
	return nil, nil, 0
}

// rowsToSample converts raw rows to sample map slice
func rowsToSample(rows [][]string, columns []string, maxRows int) []map[string]interface{} {
	if len(rows) < 2 {
		return nil
	}
	dataRows := rows[1:]
	if len(dataRows) > maxRows {
		dataRows = dataRows[:maxRows]
	}
	sample := make([]map[string]interface{}, len(dataRows))
	for i, row := range dataRows {
		m := make(map[string]interface{})
		for j, col := range columns {
			if j < len(row) {
				m[col] = row[j]
			} else {
				m[col] = ""
			}
		}
		sample[i] = m
	}
	return sample
}

// readSessionData reads session files and builds TransactionRows with column mapping
func readSessionData(sessionDir string, sourceCol, accountCol, targetCol, targetCardCol, amountCol, timeCol, directionCol string, dirMap map[string]string) []model.TransactionRow {
	var txns []model.TransactionRow
	// Normalize all column lookup keys to match normalized headers
	sourceCol = parser.NormalizeHeader(sourceCol)
	accountCol = parser.NormalizeHeader(accountCol)
	targetCol = parser.NormalizeHeader(targetCol)
	targetCardCol = parser.NormalizeHeader(targetCardCol)
	amountCol = parser.NormalizeHeader(amountCol)
	timeCol = parser.NormalizeHeader(timeCol)
	directionCol = parser.NormalizeHeader(directionCol)
	// Also normalize dirMap keys for consistent matching
	normalizedDirMap := make(map[string]string, len(dirMap))
	for k, v := range rules.LoadDirectionAliases() {
		normalizedDirMap[strings.TrimSpace(k)] = v
		normalizedDirMap[parser.NormalizeHeader(k)] = v
	}
	for k, v := range dirMap {
		normalizedDirMap[strings.TrimSpace(k)] = v
		normalizedDirMap[parser.NormalizeHeader(k)] = v
	}

	filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !parser.SupportedSuffixes[ext] {
			return nil
		}

		var rows [][]string
		if parser.ExcelSuffixes[ext] {
			sheets, err := parser.ReadExcelFile(path)
			if err != nil {
				return nil
			}
			for _, sheet := range sheets {
				rows = append(rows, sheet...)
			}
		} else {
			rows, err = parser.ReadCSVFile(path)
			if err != nil {
				return nil
			}
		}

		if len(rows) < 2 {
			return nil
		}

		headers := rows[0]
		colIdx := make(map[string]int)
		for i, h := range headers {
			colIdx[parser.NormalizeHeader(h)] = i
		}

		for _, row := range rows[1:] {
			txn := make(model.TransactionRow)

			// Map source columns
			if sourceCol != "" {
				if idx, ok := colIdx[sourceCol]; ok && idx < len(row) {
					txn["\u4ea4\u6613\u6237\u540d"] = row[idx]
				}
			}
			if accountCol != "" {
				if idx, ok := colIdx[accountCol]; ok && idx < len(row) {
					txn["\u4ea4\u6613\u8d26\u53f7"] = row[idx]
				}
			}
			if targetCol != "" {
				if idx, ok := colIdx[targetCol]; ok && idx < len(row) {
					txn["\u5bf9\u624b\u6237\u540d"] = row[idx]
				}
			}
			if targetCardCol != "" {
				if idx, ok := colIdx[targetCardCol]; ok && idx < len(row) {
					txn["\u4ea4\u6613\u5bf9\u624b\u8d26\u5361\u53f7"] = row[idx]
				}
			}
			if amountCol != "" {
				if idx, ok := colIdx[amountCol]; ok && idx < len(row) {
					txn["\u4ea4\u6613\u91d1\u989d"] = row[idx]
				}
			}
			if timeCol != "" {
				if idx, ok := colIdx[timeCol]; ok && idx < len(row) {
					txn["\u4ea4\u6613\u65f6\u95f4"] = row[idx]
				}
			}
			if directionCol != "" {
				if idx, ok := colIdx[directionCol]; ok && idx < len(row) {
					val := normalizeFlowDirection(row[idx], normalizedDirMap)
					txn["\u6536\u4ed8\u6807\u5fd7"] = val
				}
			}

			txns = append(txns, txn)
		}
		return nil
	})

	return txns
}

func normalizeFlowDirection(value string, aliases map[string]string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	if mapped, ok := aliases[raw]; ok {
		return mapped
	}
	normalizedKey := parser.NormalizeHeader(raw)
	if mapped, ok := aliases[normalizedKey]; ok {
		return mapped
	}
	normalized := parser.NormalizeDirection(raw)
	if mapped, ok := aliases[normalized]; ok {
		return mapped
	}
	return normalized
}

// checkUnknownDirections checks for direction values that aren't \"\u8fdb\" or \"\u51fa\"
func checkUnknownDirections(txns []model.TransactionRow) []string {
	seen := make(map[string]bool)
	var unknown []string
	for _, txn := range txns {
		dir := txn["\u6536\u4ed8\u6807\u5fd7"]
		if dir != "" && dir != "\u8fdb" && dir != "\u51fa" {
			if !seen[dir] {
				seen[dir] = true
				unknown = append(unknown, dir)
			}
		}
	}
	return unknown
}

// applyFilters applies source/target filters to transactions
func applyFilters(txns []model.TransactionRow, payload map[string]interface{}) []model.TransactionRow {
	sourceFilters, _ := payload["source_filters"].([]interface{})
	targetFilters, _ := payload["target_filters"].([]interface{})
	directions := stringSet(payload["directions"])
	startDate, _ := payload["start_date"].(string)
	endDate, _ := payload["end_date"].(string)
	filtered := make([]model.TransactionRow, 0)

	for _, txn := range txns {
		if matchesFilterGroups(txn, sourceFilters) &&
			matchesFilterGroups(txn, targetFilters) &&
			matchesDirection(txn, directions) &&
			matchesDateRange(txn, startDate, endDate) {
			filtered = append(filtered, txn)
		}
	}
	return filtered
}

func flowEdgeLimit(payload map[string]interface{}) int {
	if requested := intPayloadValue(payload["max_edges"]); requested > 0 {
		if requested > auditFlowEdgeLimit {
			return auditFlowEdgeLimit
		}
		return requested
	}
	sourceFilters, _ := payload["source_filters"].([]interface{})
	targetFilters, _ := payload["target_filters"].([]interface{})
	if hasActiveFilterGroups(sourceFilters) || hasActiveFilterGroups(targetFilters) {
		return auditFlowEdgeLimit
	}
	return defaultFlowEdgeLimit
}

func intPayloadValue(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
}

func hasActiveFilterGroups(filters []interface{}) bool {
	for _, item := range filters {
		filter, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		column, _ := filter["column"].(string)
		if column != "" && len(stringSet(filter["values"])) > 0 {
			return true
		}
	}
	return false
}

func matchesFilterGroups(txn model.TransactionRow, filters []interface{}) bool {
	for _, item := range filters {
		filter, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		column, _ := filter["column"].(string)
		values := stringSet(filter["values"])
		if column == "" || len(values) == 0 {
			continue
		}
		if !values[txn[parser.NormalizeHeader(column)]] {
			return false
		}
	}
	return true
}

func matchesDirection(txn model.TransactionRow, directions map[string]bool) bool {
	if len(directions) == 0 {
		return true
	}
	return directions[txn["\u6536\u4ed8\u6807\u5fd7"]]
}

func matchesDateRange(txn model.TransactionRow, startDate, endDate string) bool {
	if startDate == "" && endDate == "" {
		return true
	}
	tradeTime := parser.NormalizeDatetime(txn["\u4ea4\u6613\u65f6\u95f4"])
	if len(tradeTime) >= 10 {
		tradeTime = tradeTime[:10]
	}
	if tradeTime == "" {
		return false
	}
	if startDate != "" && tradeTime < startDate {
		return false
	}
	if endDate != "" && tradeTime > endDate {
		return false
	}
	return true
}

func stringSet(raw interface{}) map[string]bool {
	values := make(map[string]bool)
	items, ok := raw.([]interface{})
	if !ok {
		return values
	}
	for _, item := range items {
		value := strings.TrimSpace(fmt.Sprint(item))
		if value != "" {
			values[value] = true
		}
	}
	return values
}

// extractColumnValues extracts unique values for a given column from session files
func extractColumnValues(sessionDir string, column string, limit int) []string {
	seen := make(map[string]bool)
	var values []string

	filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !parser.SupportedSuffixes[ext] {
			return nil
		}

		var rows [][]string
		if parser.ExcelSuffixes[ext] {
			sheets, err := parser.ReadExcelFile(path)
			if err != nil {
				return nil
			}
			for _, s := range sheets {
				rows = append(rows, s...)
			}
		} else {
			rows, err = parser.ReadCSVFile(path)
			if err != nil {
				return nil
			}
		}

		if len(rows) < 2 {
			return nil
		}

		headers := rows[0]
		colIdx := -1
		for i, h := range headers {
			if parser.NormalizeHeader(h) == parser.NormalizeHeader(column) {
				colIdx = i
				break
			}
		}
		if colIdx < 0 {
			return nil
		}

		for _, row := range rows[1:] {
			if colIdx < len(row) {
				val := strings.TrimSpace(row[colIdx])
				if val != "" && !seen[val] && len(values) < limit {
					seen[val] = true
					values = append(values, val)
				}
			}
		}
		return nil
	})

	return values
}

// queryEdgeRows queries transaction rows matching source/target
func queryEdgeRows(sessionDir string, p struct {
	SessionID    string `json:"session_id"`
	SourceColumn string `json:"source_column"`
	TargetColumn string `json:"target_column"`
	AmountColumn string `json:"amount_column"`
	TimeColumn   string `json:"time_column"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	Limit        int    `json:"limit"`
}) []map[string]interface{} {
	var result []map[string]interface{}

	filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !parser.SupportedSuffixes[ext] {
			return nil
		}

		var rows [][]string
		if parser.ExcelSuffixes[ext] {
			sheets, err := parser.ReadExcelFile(path)
			if err != nil {
				return nil
			}
			for _, s := range sheets {
				rows = append(rows, s...)
			}
		} else {
			rows, err = parser.ReadCSVFile(path)
			if err != nil {
				return nil
			}
		}

		if len(rows) < 2 {
			return nil
		}

		headers := rows[0]
		colIdx := make(map[string]int)
		for i, h := range headers {
			colIdx[parser.NormalizeHeader(h)] = i
		}

		for _, row := range rows[1:] {
			if len(result) >= p.Limit {
				break
			}
			// Check source match
			sourceIdx, sok := colIdx[parser.NormalizeHeader(p.SourceColumn)]
			targetIdx, tok := colIdx[parser.NormalizeHeader(p.TargetColumn)]
			if !sok || !tok || sourceIdx >= len(row) || targetIdx >= len(row) {
				continue
			}
			if row[sourceIdx] != p.Source || row[targetIdx] != p.Target {
				continue
			}
			m := make(map[string]interface{})
			for j, h := range headers {
				if j < len(row) {
					m[parser.NormalizeHeader(h)] = row[j]
				}
			}
			result = append(result, m)
		}
		return nil
	})

	return result
}
