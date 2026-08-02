package intelligence

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// ── V2 调查请求 API（设计 §14）──
//
// 端点（挂载于 /api/investigation/*）：
//   POST /api/investigation/create   {address, chain?, objective, expected_result?, mode?} 创建请求并启动调查
//   GET  /api/investigation/requests  调查请求列表
//   GET  /api/investigation/{id}      调查详情（含 request/plan/tasks/score）
//   GET  /api/investigation/{id}/plan 调查计划
//   GET  /api/investigation/{id}/tasks 调查任务队列

// InvestigationHandler 是 V2 调查请求 HTTP 处理器。
type InvestigationHandler struct {
	agent    *InvestigationAgent
	requests *RequestStore
	intent   *IntentAnalyzer
}

// NewInvestigationHandler 创建 V2 调查请求 handler。
func NewInvestigationHandler(agent *InvestigationAgent, requests *RequestStore, intent *IntentAnalyzer) *InvestigationHandler {
	return &InvestigationHandler{agent: agent, requests: requests, intent: intent}
}

// ServeHTTP 路由分发。
func (h *InvestigationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.agent == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "调查代理未初始化"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/investigation")
	path = strings.Trim(path, "/")
	var parts []string
	if path != "" {
		parts = strings.Split(path, "/")
	}
	switch {
	case len(parts) == 1 && parts[0] == "create" && r.Method == http.MethodPost:
		h.create(w, r)
	case len(parts) == 1 && parts[0] == "requests" && r.Method == http.MethodGet:
		h.listRequests(w, r)
	case len(parts) == 2 && parts[0] == "memory" && parts[1] == "search" && r.Method == http.MethodGet:
		h.memorySearch(w, r)
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.detail(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "plan" && r.Method == http.MethodGet:
		h.plan(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "tasks" && r.Method == http.MethodGet:
		h.tasks(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "evidence" && r.Method == http.MethodGet:
		h.evidence(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "budget" && r.Method == http.MethodGet:
		h.budget(w, r, parts[0])
	// ── Runtime V2（设计 §14）：/runtime/start|status|tasks ──
	case len(parts) == 3 && parts[1] == "runtime" && parts[2] == "start" && r.Method == http.MethodPost:
		h.runtimeStart(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "runtime" && parts[2] == "status" && r.Method == http.MethodGet:
		h.runtimeStatus(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "runtime" && parts[2] == "tasks" && r.Method == http.MethodGet:
		h.runtimeTasks(w, r, parts[0])
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "接口不存在"})
	}
}

// create 创建调查请求并启动调查（设计 §3/§4/§14）。
func (h *InvestigationHandler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var req struct {
		Address        string   `json:"address"`
		Chain          string   `json:"chain,omitempty"`
		ChainID        string   `json:"chain_id,omitempty"` // 兼容旧字段名
		Objective      string   `json:"objective"`
		ExpectedResult []string `json:"expected_result"`
		Mode           string   `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "请求格式错误: " + err.Error()})
		return
	}
	chain := req.Chain
	if chain == "" {
		chain = req.ChainID
	}
	address, chainID, mode, err := ValidateInvestigationRequest(req.Address, chain, req.Objective, req.ExpectedResult, req.Mode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": err.Error()})
		return
	}
	// MEDIUM-2 轻量防御：进行中调查并发上限（超限 429，防异步负载洪泛）
	if h.agent.ActiveCount() >= maxActiveInvestigations {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"detail": "进行中的调查过多，请稍后再试"})
		return
	}
	request := &InvestigationRequest{
		Address:        address,
		ChainID:        chainID,
		Objective:      strings.TrimSpace(req.Objective),
		ExpectedResult: filterEmpty(req.ExpectedResult),
		Mode:           mode,
	}
	// 意图分析（规则引擎；auto 模式回填推断结果）
	if h.intent != nil {
		intent := h.intent.Analyze(request)
		request.Intent = intent
		request.Mode = intent.Mode
	}
	created, err := h.requests.Create(request)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": "请求持久化失败: " + err.Error()})
		return
	}
	inv, err := h.agent.StartWithRequest(r.Context(), created.Address, created.ChainID, created)
	if err != nil {
		// 调查启动失败：请求标记 failed，避免遗留无调查关联的孤儿请求
		_ = h.requests.Link(created.ID, "", RequestFailed)
		// MEDIUM-2：并发超限返回 429
		if errors.Is(err, ErrTooManyActive) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": err.Error()})
		return
	}
	if err := h.requests.Link(created.ID, inv.ID, RequestStarted); err != nil {
		log.Printf("investigation_request_link_failed id=%s err=%v", created.ID, err)
	}
	// 调查启动时已由 StartWithRequest 在锁内回填 inv.Request 的关联字段；
	// Link 更新的是 store 内对象，重新读取最新副本返回（含回填的调查 ID）
	linked, ok := h.requests.Get(created.ID)
	if !ok {
		linked = created
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"request":       linked,
		"investigation": inv,
	})
}

// filterEmpty 过滤空字符串元素。
func filterEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s := strings.TrimSpace(it); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// listRequests 返回调查请求列表。
func (h *InvestigationHandler) listRequests(w http.ResponseWriter, _ *http.Request) {
	items := h.requests.List()
	writeJSON(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
}

// detail 返回调查详情（含 request/plan/tasks/score）。
func (h *InvestigationHandler) detail(w http.ResponseWriter, _ *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// plan 返回调查计划。
func (h *InvestigationHandler) plan(w http.ResponseWriter, _ *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"investigation_id": id,
		"status":           inv.Status,
		"plan":             inv.Plan,
	})
}

// tasks 返回调查任务队列。
func (h *InvestigationHandler) tasks(w http.ResponseWriter, _ *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	tasks := inv.Tasks
	if tasks == nil {
		tasks = []InvestigationTask{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"investigation_id": id,
		"status":           inv.Status,
		"tasks":            tasks,
	})
}

// ── Runtime V2 API（设计 §14）──

// runtimeStatus 返回运行时状态（controller 状态 + 任务统计 + 心跳时间）。
// GET /api/investigation/{id}/runtime/status
// 纯读端点：任务统计从调查任务快照计算（不写 controller，避免 GET 副作用）。
func (h *InvestigationHandler) runtimeStatus(w http.ResponseWriter, _ *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	ctrl := h.agent.Controller(id)
	base := ctrl.Status(id)
	// 任务统计（纯计算，不修改 controller 状态）
	waiting, running, done, failed, total := 0, 0, 0, 0, 0
	for _, t := range inv.Tasks {
		total++
		switch t.Status {
		case TaskPending:
			if len(t.Dependencies) > 0 {
				waiting++
			}
		case TaskRunning:
			running++
		case TaskDone, TaskSkipped:
			done++
		case TaskFailed:
			failed++
		}
	}
	base.WaitingTasks, base.RunningTasks = waiting, running
	base.CompletedTasks, base.FailedTasks, base.TotalTasks = done, failed, total
	writeJSON(w, http.StatusOK, base)
}

// runtimeTasks 返回任务视图（含依赖/重试/超时字段）。
// GET /api/investigation/{id}/runtime/tasks
func (h *InvestigationHandler) runtimeTasks(w http.ResponseWriter, _ *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	tasks := inv.Tasks
	if tasks == nil {
		tasks = []InvestigationTask{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"investigation_id": id,
		"state":            statusToRuntime(inv.Status),
		"tasks":            tasks,
	})
}

// runtimeStart 从持久化状态恢复执行（设计 §11 恢复机制）。
// POST /api/investigation/{id}/runtime/start
// 若调查已终态返回 409；否则调用 Resume 恢复未完成任务（heartbeat 超时标记 failed 可重试）
// 并后台重新执行。
func (h *InvestigationHandler) runtimeStart(w http.ResponseWriter, _ *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	if TerminalStatuses[inv.Status] {
		writeJSON(w, http.StatusConflict, map[string]string{"detail": "调查已结束，无法重新启动"})
		return
	}
	recovered, err := h.agent.Resume(id)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"detail": err.Error()})
		return
	}
	state := statusToRuntime(inv.Status)
	writeJSON(w, http.StatusOK, map[string]any{
		"investigation_id": id,
		"state":            state,
		"recovered_tasks":  recovered,
	})
}

// evidence 返回调查证据链（V2.1 Evidence Layer）。
// 优先从 EvidenceStore 读取（持久化权威），store 未配置时回退调查内存挂载。
func (h *InvestigationHandler) evidence(w http.ResponseWriter, _ *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	var evs []Evidence
	if h.agent.evidence != nil {
		evs = h.agent.evidence.List(id)
	}
	if len(evs) == 0 && len(inv.Evidence) > 0 {
		evs = inv.Evidence
	}
	if evs == nil {
		evs = []Evidence{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"investigation_id": id,
		"status":           inv.Status,
		"total":            len(evs),
		"evidence":         evs,
	})
}

// writeJSON 写入 JSON 响应（包级工具，供 V2 handler 使用）。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// budget 返回调查预算与已消耗（V2.1 设计 §3）。
func (h *InvestigationHandler) budget(w http.ResponseWriter, _ *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	cfg := h.agent.Config()
	used := len(inv.Tasks)
	if inv.Tasks == nil {
		used = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"investigation_id": id,
		"status":           inv.Status,
		"budget": map[string]any{
			"max_tasks":      cfg.MaxTasks,
			"max_hops":       cfg.MaxHops,
			"max_rounds":     cfg.MaxRounds,
			"max_addresses":  cfg.MaxAddresses,
			"max_runtime_ms": cfg.MaxRuntimeMS,
		},
		"used": map[string]any{
			"tasks":      used,
			"round":      inv.Round,
			"addresses":  len(inv.Entities) + len(inv.Expansions),
			"elapsed_ms": int(time.Since(inv.CreatedAt).Milliseconds()),
		},
	})
}

// memorySearch 查询跨案件知识记忆（V2.1 设计 §9：地址/实体/案件历史关联）。
// GET /api/investigation/memory/search?address=0x...
func (h *InvestigationHandler) memorySearch(w http.ResponseWriter, r *http.Request) {
	address := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("address")))
	if !validEVMAddress(address) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "address 不是合法的 EVM 地址"})
		return
	}
	if h.agent.knowledge == nil {
		writeJSON(w, http.StatusOK, map[string]any{"address": address, "total": 0, "relations": []MemoryRelation{}})
		return
	}
	rels := h.agent.knowledge.Search(address)
	if rels == nil {
		rels = []MemoryRelation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"address":   address,
		"total":     len(rels),
		"relations": rels,
		"hint":      "历史案件存在关联，可能指向相同实体或资金路径",
	})
}
