package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/datasetevents"
	"github.com/etl/backend/internal/downloadscheduler"
	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/objectiveplanner"
	"github.com/etl/backend/internal/parquetdownload"
	"github.com/gin-gonic/gin"
)

// HandleSchedulerAPI 是 /api/scheduler/* 的 Gin 转发入口。
func HandleSchedulerAPI(c *gin.Context) {
	if schedulerAPI == nil {
		c.JSON(503, map[string]any{"detail": "智能下载调度服务不可用"})
		return
	}
	schedulerAPI.ServeHTTP(c.Writer, c.Request)
}

// SchedulerHandler 智能下载调度 API（设计文档 §15 前端展示：智能数据补充）。
type SchedulerHandler struct {
	scheduler *downloadscheduler.Scheduler
}

// NewSchedulerHandler 创建调度 API handler。
func NewSchedulerHandler(scheduler *downloadscheduler.Scheduler) *SchedulerHandler {
	return &SchedulerHandler{scheduler: scheduler}
}

// ServeHTTP 路由 /api/scheduler/*。
func (h *SchedulerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodPost && path == "coverage":
		h.coverage(w, r)
	case r.Method == http.MethodPost && path == "plan":
		h.plan(w, r)
	case r.Method == http.MethodPost && path == "run":
		h.run(w, r)
	case r.Method == http.MethodPost && path == "expand":
		h.expand(w, r)
	case r.Method == http.MethodGet && path == "status":
		h.status(w, r)
	case r.Method == http.MethodGet && path == "plans":
		h.plans(w, r)
	case r.Method == http.MethodGet && path == "budget":
		h.budget(w, r)
	case r.Method == http.MethodGet && path == "providers/health":
		h.providerHealth(w, r)
	case r.Method == http.MethodGet && path == "cloud/runtime":
		h.cloudRuntime(w, r)
	case r.Method == http.MethodGet && path == "cloud/jobs":
		h.cloudJobs(w, r)
	case r.Method == http.MethodPost && path == "cloud/sync":
		h.cloudSync(w, r)
	case r.Method == http.MethodPost && path == "cloud/jobs/cancel":
		h.cloudJobCancel(w, r)
	case r.Method == http.MethodGet && path == "cloud/usage":
		h.cloudUsage(w, r)
	case r.Method == http.MethodGet && path == "metrics":
		h.metrics(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "unknown scheduler endpoint: " + path})
	}
}

// metrics GET /metrics — Phase 5.4.1 可观测性指标（可用字段，缺失项显式 0）。
func (h *SchedulerHandler) metrics(w http.ResponseWriter, r *http.Request) {
	reg := h.scheduler.CloudRegistry()
	var registryRows, registryFiles int64
	if reg != nil {
		registryRows, registryFiles, _ = reg.Stats()
	}
	events := []datasetevents.Event{}
	if datasetEventBus != nil {
		events = datasetEventBus.Events()
	}
	resumeTotal, cancelTotal := 0, 0
	for _, e := range events {
		switch e.Type {
		case datasetevents.InvestigationResumed:
			resumeTotal++
		case datasetevents.InvestigationCancelled:
			cancelTotal++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requirements_total":     len(h.scheduler.Plans()),
		"coverage_hit_ratio":     0, // 需 Coverage Resolver 统计接口，暂缺
		"provider_success_rate":  0,
		"provider_p95_ms":        0,
		"cloud_fallback_ratio":   h.scheduler.CloudFallbackRatio(),
		"event_total":            len(events),
		"event_lag_ms":           0,
		"investigation_resume_ms": 0,
		"graph_increment_ms":      0,
		"registry_rows":           registryRows,
		"multipart_parts_total":   registryFiles,
		"resume_total":            resumeTotal,
		"cancel_total":            cancelTotal,
		"local_sync_retry_total":  0,
	})
}

// cloudJobCancel POST /cloud/jobs/cancel — 写入 Cloud Cancel Marker（Phase 5.2 §6）。
func (h *SchedulerHandler) cloudJobCancel(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if body.JobID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "缺少 job_id"})
		return
	}
	if err := h.scheduler.CancelCloudJob(r.Context(), body.JobID); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id": body.JobID, "status": "cancel_requested",
		"marker": "bsc/jobs/cancel/" + body.JobID + ".json",
	})
}

// coverage POST /coverage — 覆盖检查（Coverage Resolver）。
func (h *SchedulerHandler) coverage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB 请求体上限
	var body struct {
		ChainKey  string   `json:"chain_key"`
		Addresses []string `json:"addresses"`
		Datasets  []string `json:"datasets,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if len(body.Addresses) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "缺少 addresses"})
		return
	}
	if len(body.Addresses) > 100 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "覆盖检查地址数上限 100"})
		return
	}
	// EVM 地址校验（防 SQL 注入：地址将进入 analyticsapi.Flows 的 DuckDB 查询）
	for _, a := range body.Addresses {
		if !evmAddressPattern.MatchString(strings.TrimSpace(a)) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "非法 EVM 地址: " + truncateForError(a)})
			return
		}
	}
	datasets := make([]downloadscheduler.Dataset, 0, len(body.Datasets))
	for _, d := range body.Datasets {
		ds := downloadscheduler.Dataset(d)
		if downloadscheduler.ValidDataset(ds) {
			datasets = append(datasets, ds)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := h.scheduler.Coverage().Check(ctx, body.ChainKey, body.Addresses, datasets)
	if err != nil {
		// 错误脱敏：不向客户端泄露 DuckDB 路径/SQL 文本
		logger.Log.Error().Err(err).Msg("scheduler_coverage_failed")
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "覆盖检查失败（内部错误）"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// plan POST /plan — 分析需求生成下载计划。
func (h *SchedulerHandler) plan(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB 请求体上限
	var body struct {
		Requirements []struct {
			Dataset       string   `json:"dataset"`
			ChainKey      string   `json:"chain_key"`
			Addresses     []string `json:"addresses"`
			StartDate     string   `json:"start_date,omitempty"`
			EndDate       string   `json:"end_date,omitempty"`
			FromBlock     uint64   `json:"from_block,omitempty"`
			ToBlock       uint64   `json:"to_block,omitempty"`
			Direction     string   `json:"direction,omitempty"`
			Depth         int      `json:"depth,omitempty"`
			CloudEligible *bool    `json:"cloud_eligible,omitempty"`
			ObjectiveType string   `json:"objective_type,omitempty"`
			ObjectiveDescription string `json:"objective_description,omitempty"`
			ObjectiveConstraints objectiveplanner.Constraints `json:"objective_constraints,omitempty"`
		} `json:"requirements"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if len(body.Requirements) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "缺少 requirements"})
		return
	}
	if len(body.Requirements) > 20 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "requirements 上限 20"})
		return
	}
	reqs := make([]downloadscheduler.Requirement, 0, len(body.Requirements))
	for _, q := range body.Requirements {
		reqs = append(reqs, downloadscheduler.Requirement{
			Dataset:       downloadscheduler.Dataset(q.Dataset),
			ChainKey:      q.ChainKey,
			Addresses:     q.Addresses,
			StartDate:     q.StartDate,
			EndDate:       q.EndDate,
			FromBlock:     q.FromBlock,
			ToBlock:       q.ToBlock,
			Direction:     q.Direction,
			Depth:         q.Depth,
			CloudEligible: q.CloudEligible,
			ObjectiveType: q.ObjectiveType,
			ObjectiveDescription: q.ObjectiveDescription,
			ObjectiveConstraints: q.ObjectiveConstraints,
		})
	}
	plan, err := h.scheduler.Submit(r.Context(), reqs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// run POST /run — 执行计划（异步）。
func (h *SchedulerHandler) run(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB 请求体上限
	var body struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if body.PlanID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "缺少 plan_id"})
		return
	}
	if err := h.scheduler.Run(r.Context(), body.PlanID); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, h.scheduler.Plan(body.PlanID))
}

// expand POST /expand — 图联动一站式：覆盖检查 → 计划 → 执行，返回计划供前端轮询。
func (h *SchedulerHandler) expand(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB 请求体上限
	var body struct {
		Address       string   `json:"address"`
		ChainKey      string   `json:"chain_key"`
		Datasets      []string `json:"datasets,omitempty"` // 默认 transactions+balance
		Direction     string   `json:"direction,omitempty"`
		Depth         int      `json:"depth,omitempty"`
		FromBlock     uint64   `json:"from_block,omitempty"`
		ToBlock       uint64   `json:"to_block,omitempty"`
		CloudEligible *bool    `json:"cloud_eligible,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	address := strings.ToLower(strings.TrimSpace(body.Address))
	if !evmAddressPattern.MatchString(address) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "非法 EVM 地址"})
		return
	}
	datasets := body.Datasets
	if len(datasets) == 0 {
		datasets = []string{"transactions", "balance"}
	}
	reqs := make([]downloadscheduler.Requirement, 0, len(datasets))
	for _, d := range datasets {
		ds := downloadscheduler.Dataset(d)
		if !downloadscheduler.ValidDataset(ds) {
			continue
		}
		reqs = append(reqs, downloadscheduler.Requirement{
			Dataset:       ds,
			ChainKey:      body.ChainKey,
			Addresses:     []string{address},
			Direction:     body.Direction,
			Depth:         body.Depth,
			FromBlock:     body.FromBlock,
			ToBlock:       body.ToBlock,
			CloudEligible: body.CloudEligible,
		})
	}
	if len(reqs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "没有可执行的数据集"})
		return
	}
	plan, err := h.scheduler.Submit(r.Context(), reqs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if runErr := h.scheduler.Run(r.Context(), plan.ID); runErr != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"detail": runErr.Error()})
		return
	}
	// 返回 Run 之后的最新计划（EXECUTING，含已选 Provider）
	live := h.scheduler.Plan(plan.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"plan":       live,
		"plan_id":    plan.ID,
		"status":     live.Status,
		"poll":       "/api/scheduler/status?plan_id=" + plan.ID,
		"message":    "智能数据补充已启动，轮询 status 获取进度",
		"created_at": time.Now(),
	})
}

// status GET /status?plan_id= — 计划状态（默认返回运行中或最近计划）。
func (h *SchedulerHandler) status(w http.ResponseWriter, r *http.Request) {
	planID := r.URL.Query().Get("plan_id")
	var plan *downloadscheduler.Plan
	if planID != "" {
		plan = h.scheduler.Plan(planID)
		if plan == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "计划不存在: " + planID})
			return
		}
	} else {
		running := h.scheduler.Running()
		if running != "" {
			plan = h.scheduler.Plan(running)
		} else {
			all := h.scheduler.Plans()
			if len(all) > 0 {
				plan = all[0]
			}
		}
	}
	if plan == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plan": nil, "running": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plan":    plan,
		"running": h.scheduler.Running() == plan.ID,
	})
}

// plans GET /plans — 全部计划（新→旧）。
func (h *SchedulerHandler) plans(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"plans": h.scheduler.Plans()})
}

// budget GET /budget — 预算配置。
func (h *SchedulerHandler) budget(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.scheduler.Budget())
}

// providerHealth GET /providers/health — Provider Tier/State/健康快照（设计 §63）。
func (h *SchedulerHandler) providerHealth(w http.ResponseWriter, r *http.Request) {
	type view struct {
		Kind                downloadscheduler.ProviderKind  `json:"kind"`
		Name                string                          `json:"name"`
		Tier                downloadscheduler.ProviderTier  `json:"tier"`
		State               downloadscheduler.ProviderState `json:"state"`
		Available           bool                            `json:"available"`
		ManualOnly          bool                            `json:"manual_only"`
		Reasons             []string                        `json:"reasons,omitempty"`
		ConsecutiveFailures int                             `json:"consecutive_failures,omitempty"`
	}
	health := h.scheduler.ProviderHealth()
	views := make([]view, 0)
	for _, p := range h.scheduler.Registry().All() {
		state := downloadscheduler.ProviderHealthy
		reasons := ([]string)(nil)
		if sp, ok := p.(downloadscheduler.StateProvider); ok {
			state = sp.State()
			reasons = sp.StateReasons()
		}
		v := view{
			Kind: p.Kind(), Name: p.Name(), Tier: p.Tier(),
			State: state, Available: p.Available(), ManualOnly: p.ManualOnly(),
			Reasons: reasons,
		}
		if info, ok := health[p.Kind()]; ok {
			v.State = info.State
			v.Reasons = info.Reasons
			v.ConsecutiveFailures = info.ConsecutiveFailures
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers":       views,
		"fault_injection": downloadscheduler.FaultInjectionFromEnv(),
	})
}

// cloudRuntime GET /cloud/runtime — Cloud Worker 状态/预算/用量（设计 §63）。
func (h *SchedulerHandler) cloudRuntime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime": h.scheduler.CloudRuntimeStatus(),
		"budget":  h.scheduler.Budget().Cloud,
		"usage":   h.scheduler.CloudUsage(),
	})
}

// cloudJobs GET /cloud/jobs — Cloud 任务列表 + Registry 汇总（Phase 4 §48）。
func (h *SchedulerHandler) cloudJobs(w http.ResponseWriter, r *http.Request) {
	reg := h.scheduler.CloudRegistry()
	stats := map[string]any{"entries": 0, "rows": 0, "files": 0, "bytes": 0}
	if reg != nil {
		rows, files, bytes := reg.Stats()
		stats = map[string]any{"entries": len(reg.Completed()), "rows": rows, "files": files, "bytes": bytes}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":     h.scheduler.CloudJobs(),
		"registry": stats,
	})
}

// cloudSync POST /cloud/sync — 手动触发 Local Sync（Phase 4 §26）。
func (h *SchedulerHandler) cloudSync(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	results, err := h.scheduler.CloudSync(ctx)
	if err != nil {
		logger.Log.Error().Err(err).Msg("scheduler_cloud_sync_failed")
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Cloud 数据同步失败（内部错误）"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// cloudUsage GET /cloud/usage — Cloud 用量 + Registry 汇总（Phase 4 §47）。
func (h *SchedulerHandler) cloudUsage(w http.ResponseWriter, r *http.Request) {
	reg := h.scheduler.CloudRegistry()
	stats := map[string]any{"entries": 0, "rows": 0, "files": 0, "bytes": 0}
	if reg != nil {
		rows, files, bytes := reg.Stats()
		stats = map[string]any{"entries": len(reg.Completed()), "rows": rows, "files": files, "bytes": bytes}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"usage":                     h.scheduler.CloudUsage(),
		"registry":                  stats,
		"deployment_key_configured": h.scheduler.CloudRuntimeStatus().DeploymentKeyConfigured,
		"r2_configured":             h.scheduler.CloudRuntimeStatus().R2Configured,
		"fallback_ratio":            h.scheduler.CloudFallbackRatio(),
	})
}

// ── 辅助 ──

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// truncateForError 截断错误消息中回显的用户输入（防日志/响应膨胀）。
func truncateForError(s string) string {
	if len(s) > 64 {
		return s[:64] + "…"
	}
	return s
}

// analyticsCoverageSource 将 analyticsapi.Service 适配为覆盖查询源。
type analyticsCoverageSource struct {
	svc *analyticsapi.Service
}

func (a *analyticsCoverageSource) AddressTxCount(ctx context.Context, address string) (int64, error) {
	if a == nil || a.svc == nil {
		return 0, fmt.Errorf("分析服务不可用")
	}
	edges, err := a.svc.Flows(ctx, address, "")
	if err != nil {
		logger.Log.Warn().Str("address", address).Err(err).Msg("scheduler_coverage_query_failed")
		return 0, err
	}
	return int64(len(edges)), nil
}

// sqdHealthAdapter 将 parquetdownload.Manager.SQDStatus() 适配为 downloadscheduler.HealthSource
// （V3 动态评分数据源；避免 parquetdownload ↔ downloadscheduler 循环依赖）。
type sqdHealthAdapter struct {
	manager *parquetdownload.Manager
}

func (a *sqdHealthAdapter) SQDHealth() downloadscheduler.SQDHealthSnapshot {
	if a == nil || a.manager == nil {
		return downloadscheduler.SQDHealthSnapshot{}
	}
	s := a.manager.SQDStatus()
	rate := 0.0
	if s.Metrics.RequestTotal > 0 {
		rate = float64(s.Metrics.SuccessTotal) / float64(s.Metrics.RequestTotal)
	}
	return downloadscheduler.SQDHealthSnapshot{
		CooldownActive: s.Cooldown,
		CooldownUntil:  s.CooldownFor,
		BreakerState:   s.Circuit.State,
		Workers:        s.Workers.CurrentWorkers,
		WorkerTier:     s.Workers.Tier,
		Consecutive503: s.Workers.Consecutive503,
		SuccessRate:    rate,
		Requests:       s.Metrics.RequestTotal,
		Failures:       s.Metrics.FailedTotal,
	}
}
