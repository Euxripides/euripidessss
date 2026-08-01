package intelligence

import (
	"encoding/json"
	"net/http"
	"strings"
)

// ── REST API ──
//
// 端点：
//   POST /api/intelligence/investigations          {target, chain_id, config?} 启动调查
//   GET  /api/intelligence/investigations          调查列表
//   GET  /api/intelligence/investigations/:id      调查详情（进度/路径/风险/AI）
//   GET  /api/intelligence/investigations/:id/report?format=markdown|html|json  报告
//   GET  /api/intelligence/investigations/:id/memory 调查记忆
//   GET  /api/intelligence/memories                全部调查记忆
//   GET  /api/intelligence/config                  当前配置
//   POST /api/intelligence/config                  {config} 更新配置（部分更新）

// Handler 是调查平台 HTTP 处理器。
type Handler struct {
	agent *InvestigationAgent
}

// NewHandler 创建 HTTP handler。
func NewHandler(agent *InvestigationAgent) *Handler {
	return &Handler{agent: agent}
}

// ServeHTTP 路由分发。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.agent == nil {
		h.json(w, http.StatusServiceUnavailable, map[string]string{"detail": "调查代理未初始化"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/intelligence")
	path = strings.Trim(path, "/")
	parts := []string{}
	if path != "" {
		parts = strings.Split(path, "/")
	}

	switch {
	case len(parts) == 1 && parts[0] == "investigations" && r.Method == http.MethodPost:
		h.start(w, r)
	case len(parts) == 1 && parts[0] == "investigations" && r.Method == http.MethodGet:
		h.list(w, r)
	case len(parts) == 2 && parts[0] == "investigations" && r.Method == http.MethodGet:
		h.get(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "investigations" && parts[2] == "report" && r.Method == http.MethodGet:
		h.report(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "investigations" && parts[2] == "memory" && r.Method == http.MethodGet:
		h.memory(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "memories" && r.Method == http.MethodGet:
		h.memories(w, r)
	case len(parts) == 1 && parts[0] == "config" && r.Method == http.MethodGet:
		h.getConfig(w, r)
	case len(parts) == 1 && parts[0] == "config" && r.Method == http.MethodPost:
		h.updateConfig(w, r)
	default:
		h.json(w, http.StatusNotFound, map[string]string{"detail": "接口不存在"})
	}
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req struct {
		Target   string             `json:"target"`
		ChainID  string             `json:"chain_id,omitempty"`
		Config   map[string]any     `json:"config,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "请求格式错误: " + err.Error()})
		return
	}
	if !validEVMAddress(req.Target) {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "target 不是合法的 EVM 地址"})
		return
	}
	// 启动配置仅作用于本次调查（副本），不修改全局配置，避免影响并发/后续调查
	var override *IntelligenceConfig
	if len(req.Config) > 0 {
		cfg := h.agent.Config()
		applyConfigFields(&cfg, req.Config)
		override = &cfg
	}
	inv, err := h.agent.Start(r.Context(), req.Target, req.ChainID, override)
	if err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": err.Error()})
		return
	}
	h.json(w, http.StatusAccepted, inv)
}

func (h *Handler) list(w http.ResponseWriter, _ *http.Request) {
	items := h.agent.List()
	h.json(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
}

func (h *Handler) get(w http.ResponseWriter, _ *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		h.json(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	h.json(w, http.StatusOK, inv)
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request, id string) {
	inv, ok := h.agent.Get(id)
	if !ok {
		h.json(w, http.StatusNotFound, map[string]string{"detail": "调查不存在"})
		return
	}
	format := ReportFormat(r.URL.Query().Get("format"))
	if format == "" {
		format = ReportMarkdown
	}
	out, err := h.agent.GenerateReport(inv, format)
	if err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": err.Error()})
		return
	}
	switch format {
	case ReportJSON:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(out.Content))
	case ReportHTML:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(out.Content))
	default:
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(out.Content))
	}
}

func (h *Handler) memory(w http.ResponseWriter, _ *http.Request, id string) {
	mem, ok := h.agent.memories.Get(id)
	if !ok {
		h.json(w, http.StatusNotFound, map[string]string{"detail": "调查记忆不存在"})
		return
	}
	h.json(w, http.StatusOK, mem)
}

func (h *Handler) memories(w http.ResponseWriter, _ *http.Request) {
	items := h.agent.memories.List()
	h.json(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
}

func (h *Handler) getConfig(w http.ResponseWriter, _ *http.Request) {
	h.json(w, http.StatusOK, h.agent.Config())
}

func (h *Handler) updateConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "请求格式错误: " + err.Error()})
		return
	}
	cfg := h.agent.Config()
	applyConfigFields(&cfg, raw)
	h.agent.UpdateConfig(cfg)
	h.json(w, http.StatusOK, cfg)
}

// applyConfigFields 将原始配置部分更新到 cfg 并钳制非法值（不访问 agent，
// 供 POST /config 与 POST /start 的启动覆盖共用）。
func applyConfigFields(cfg *IntelligenceConfig, raw map[string]any) {
	applyInt(raw, "max_hops", &cfg.MaxHops)
	applyInt(raw, "beam_width", &cfg.BeamWidth)
	applyInt(raw, "top_paths", &cfg.TopPaths)
	applyInt(raw, "ai_timeout_ms", &cfg.AITimeoutMS)
	applyInt(raw, "max_expansion", &cfg.MaxExpansion)
	applyInt(raw, "max_rounds", &cfg.MaxRounds)
	applyInt(raw, "max_runtime_ms", &cfg.MaxRuntimeMS)
	applyInt(raw, "max_addresses", &cfg.MaxAddresses)
	applyInt(raw, "max_tokens", &cfg.MaxTokens)
	applyInt(raw, "max_ai_calls", &cfg.MaxAICalls)
	applyFloat(raw, "expansion_threshold", &cfg.ExpansionThreshold)
	applyString(raw, "min_amount", &cfg.MinAmount)
	applyString(raw, "ai_model", &cfg.AIModel)
	applyBool(raw, "use_ai", &cfg.UseAI)
	// 钳制
	if cfg.MaxHops < 1 {
		cfg.MaxHops = 1
	}
	if cfg.MaxHops > 8 {
		cfg.MaxHops = 8
	}
	if cfg.BeamWidth < 1 {
		cfg.BeamWidth = 1
	}
	if cfg.BeamWidth > 32 {
		cfg.BeamWidth = 32
	}
	if cfg.TopPaths < 1 {
		cfg.TopPaths = 1
	}
	if cfg.TopPaths > 50 {
		cfg.TopPaths = 50
	}
	if cfg.MaxExpansion < 0 {
		cfg.MaxExpansion = 0
	}
	if cfg.MaxExpansion > 1000 {
		cfg.MaxExpansion = 1000
	}
	if cfg.MaxRounds < 1 {
		cfg.MaxRounds = 1
	}
	if cfg.MaxRounds > 10 {
		cfg.MaxRounds = 10
	}
	if cfg.MaxRuntimeMS < 0 {
		cfg.MaxRuntimeMS = 0
	}
	if cfg.MaxRuntimeMS > 0 && cfg.MaxRuntimeMS < 1000 {
		cfg.MaxRuntimeMS = 1000
	}
	if cfg.MaxRuntimeMS > 3600000 {
		cfg.MaxRuntimeMS = 3600000
	}
	if cfg.MaxAddresses < 1 {
		cfg.MaxAddresses = 1
	}
	if cfg.MaxAddresses > 100000 {
		cfg.MaxAddresses = 100000
	}
	if cfg.MaxTokens < 256 {
		cfg.MaxTokens = 256
	}
	if cfg.MaxTokens > 8000 {
		cfg.MaxTokens = 8000
	}
	if cfg.MaxAICalls < 0 {
		cfg.MaxAICalls = 0
	}
	if cfg.MaxAICalls > 50 {
		cfg.MaxAICalls = 50
	}
	if cfg.ExpansionThreshold < 0 {
		cfg.ExpansionThreshold = 0
	}
	if cfg.ExpansionThreshold > 100 {
		cfg.ExpansionThreshold = 100
	}
}

func applyInt(raw map[string]any, key string, dst *int) {
	if v, ok := raw[key]; ok {
		if f, ok2 := v.(float64); ok2 {
			*dst = int(f)
		}
	}
}

func applyString(raw map[string]any, key string, dst *string) {
	if v, ok := raw[key]; ok {
		if s, ok2 := v.(string); ok2 {
			*dst = s
		}
	}
}

func applyFloat(raw map[string]any, key string, dst *float64) {
	if v, ok := raw[key]; ok {
		if f, ok2 := v.(float64); ok2 {
			*dst = f
		}
	}
}

func applyBool(raw map[string]any, key string, dst *bool) {
	if v, ok := raw[key]; ok {
		if b, ok2 := v.(bool); ok2 {
			*dst = b
		}
	}
}

func (h *Handler) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
