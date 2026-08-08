package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/etl/backend/internal/reportengine"
)

// setupReportEngine 装配 Investigation Report Engine V2。
func setupReportEngine() {
	if reportEngine != nil {
		return
	}
	root := filepath.Join(cfg.RootDir, "backend", "data", "investigation")
	store := reportengine.NewStore(root)
	reportEngine = reportengine.NewEngine(store, fundFlowEngine, entityResolver, coverageForReport, investigationCacheStore)
	log.Info().Str("root", root).Msg("report_engine_v2_ready")
}

// coverageForReport 将 smartdownload Coverage Index 适配为报告引擎的覆盖查询。
func coverageForReport(chainKey, address, dataset string, from, to uint64) (float64, bool, string) {
	if smartDownloadService == nil {
		return 0, false, "UNKNOWN"
	}
	r := smartDownloadService.CoverageQuery(chainKey, address, dataset, from, to)
	return r.CoverageRatio, r.FullHit, r.Certification
}

// registerReportEngineRoutes 注册报告 API（设计 §65-§68）。
func registerReportEngineRoutes(api *gin.RouterGroup) {
	api.POST("/investigations/:id/reports", HandleReportCreate)
	api.GET("/investigations/:id/reports", HandleReportList)
	api.GET("/investigations/:id/reports/:report_id", HandleReportGet)
	api.POST("/investigations/:id/reports/:report_id/regenerate", HandleReportRegenerate)
	api.POST("/investigations/:id/reports/:report_id/lock", HandleReportLock)
	api.POST("/investigations/:id/reports/:report_id/review", HandleReportReview)
	api.POST("/investigations/:id/reports/:report_id/outdated", HandleReportOutdated)
	api.POST("/investigations/:id/reports/:report_id/sign", HandleReportSign)
	api.POST("/investigations/:id/reports/:report_id/polish", HandleReportPolish)
	api.POST("/investigations/:id/reports/:report_id/export", HandleReportExport)
	api.GET("/investigations/:id/reports/diff/:a/:b", HandleReportDiff)
	api.GET("/investigations/:id/evidence/:evidence_id", HandleReportEvidence)
}

// HandleReportCreate POST /api/investigations/{id}/reports — 生成综合报告。
func HandleReportCreate(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	depth := 4
	language := "zh"
	institution := ""
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		Language    string `json:"language"`
		Institution string `json:"institution"`
		MaxDepth    int    `json:"max_depth"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err == nil {
		if body.MaxDepth > 0 {
			depth = body.MaxDepth
		}
		if body.Language != "" {
			language = body.Language
		}
		institution = body.Institution
	}
	if v := strings.TrimSpace(c.Query("max_depth")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			depth = n
		}
	}
	res, err := reportEngine.GenerateWithOptions(c.Request.Context(), c.Param("id"), depth, language, institution)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, res)
}

// HandleReportList GET /api/investigations/{id}/reports。
func HandleReportList(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"investigation_id": c.Param("id"), "reports": reportEngine.List(c.Param("id"))})
}

// HandleReportGet GET /api/investigations/{id}/reports/{report_id}。
func HandleReportGet(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	report, snapshot, timeline, findings, evidence := reportEngine.Get(c.Param("id"), c.Param("report_id"))
	if report == nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "报告不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"report": report, "snapshot": snapshot, "timeline": timeline,
		"findings": findings, "evidence": evidence,
	})
}

// HandleReportRegenerate POST /api/investigations/{id}/reports/{report_id}/regenerate — 生成新版本。
func HandleReportRegenerate(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	res, err := reportEngine.GenerateWithOptions(c.Request.Context(), c.Param("id"), 4, "zh", "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"detail": "已生成新版本", "result": res})
}

// HandleReportSign POST .../sign — 电子签名（SHA256 本地归档）。
func HandleReportSign(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	sig, err := reportEngine.SignReport(c.Param("id"), c.Param("report_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"signature": sig, "detail": "报告已签名"})
}

// HandleReportPolish POST .../polish — LLM 叙事润色（数字一致性校验）。
func HandleReportPolish(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		SectionID string `json:"section_id"`
	}
	_ = json.NewDecoder(c.Request.Body).Decode(&body)
	if body.SectionID == "" {
		body.SectionID = "summary"
	}
	text, ok, err := reportEngine.PolishSection(c.Request.Context(), c.Param("id"), c.Param("report_id"), body.SectionID)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "未配置") {
			status = http.StatusNotImplemented
		}
		c.JSON(status, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"section_id": body.SectionID, "narrative": text, "consistency": ok})
}

// HandleReportLock POST .../lock — 锁定报告（内容不可变，§60）。
func HandleReportLock(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	if err := reportEngine.SetStatus(c.Param("id"), c.Param("report_id"), reportengine.StatusLocked); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"detail": "报告已锁定（后续更新将生成新版本）"})
}

// HandleReportReview POST .../review — 人工审阅（§33、§59）。
func HandleReportReview(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	if err := reportEngine.SetStatus(c.Param("id"), c.Param("report_id"), reportengine.StatusReviewed); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"detail": "报告已标记为已审阅"})
}

// HandleReportOutdated POST .../outdated — 标记过期（数据变更后不自动覆盖，§58）。
func HandleReportOutdated(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	if err := reportEngine.SetStatus(c.Param("id"), c.Param("report_id"), reportengine.StatusOutdated); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"detail": "报告已标记为过期（OUTDATED）"})
}

// HandleReportExport POST /api/investigations/{id}/reports/{report_id}/export（设计 §66）。
func HandleReportExport(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		Format string `json:"format"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	format := strings.ToLower(strings.TrimSpace(body.Format))
	if format == "" {
		format = "json"
	}
	report, snapshot, timeline, findings, evidence := reportEngine.Get(c.Param("id"), c.Param("report_id"))
	if report == nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "报告不存在"})
		return
	}
	var data []byte
	var contentType string
	var ext string
	switch format {
	case "xlsx":
		var err error
		data, err = reportengine.ExportXLSX(report, findings, evidence)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		ext = "xlsx"
	case "docx":
		var err error
		data, err = reportengine.ExportDOCX(report, snapshot, timeline, findings, evidence)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		ext = "docx"
	case "pdf":
		var err error
		data, err = reportengine.ExportPDF(report, timeline, findings)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}
		contentType = "application/pdf"
		ext = "pdf"
	case "case_package":
		var err error
		data, err = reportengine.ExportCasePackage(c.Param("id"), report.Version, report, snapshot, timeline, findings, evidence)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}
		contentType = "application/zip"
		ext = "zip"
	default: // json
		var err error
		data, err = reportengine.ExportJSON(report, snapshot, timeline, findings, evidence)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}
		contentType = "application/json; charset=utf-8"
		ext = "json"
	}
	// 留档到 exports 目录
	exportDir := reportStoreDir(c.Param("id"), report.Version)
	if exportDir != "" {
		_ = os.MkdirAll(exportDir, 0o755)
		_ = os.WriteFile(filepath.Join(exportDir, "report."+ext), data, 0o644)
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="report_v%d.%s"`, report.Version, ext))
	_, _ = c.Writer.Write(data)
}

// HandleReportDiff GET /api/investigations/{id}/reports/{a}/diff/{b}。
func HandleReportDiff(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	c.JSON(http.StatusOK, reportEngine.Diff(c.Param("id"), c.Param("a"), c.Param("b")))
}

// HandleReportEvidence GET /api/investigations/{id}/evidence/{evidence_id}。
func HandleReportEvidence(c *gin.Context) {
	if reportEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "报告引擎未装配"})
		return
	}
	reports := reportEngine.List(c.Param("id"))
	for _, r := range reports {
		_, _, _, _, evidence := reportEngine.Get(c.Param("id"), r.ID)
		for i := range evidence {
			if evidence[i].ID == c.Param("evidence_id") {
				c.JSON(http.StatusOK, evidence[i])
				return
			}
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"detail": "证据不存在"})
}

// reportStoreDir 计算报告导出目录（与 Store 布局一致）。
func reportStoreDir(invID string, version int) string {
	if reportEngine == nil {
		return ""
	}
	return filepath.Join(cfg.RootDir, "backend", "data", "investigation",
		sanitizePathPart(invID), "reports", "report_v"+strconv.Itoa(version), "exports")
}

func sanitizePathPart(id string) string {
	id = strings.TrimSpace(id)
	for _, r := range `/\:*?"<>|` {
		id = strings.ReplaceAll(id, string(r), "_")
	}
	if id == "" || id == "." || id == ".." {
		return "default"
	}
	return id
}
