package dynamicinvestigation

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/logger"
)

// ── REST API ──
//
// 端点：
//   POST /api/dynamic-investigation/start                  {target, config?} 启动调查
//   GET  /api/dynamic-investigation/queue                  ?status=&entity=&depth= 队列列表
//   GET  /api/dynamic-investigation/queue/:address         单地址详情
//   POST /api/dynamic-investigation/queue/:address/approve 手动批准
//   POST /api/dynamic-investigation/queue/:address/ignore  手动忽略
//   GET  /api/dynamic-investigation/config                 当前配置
//   POST /api/dynamic-investigation/config                 {config} 更新配置
//   GET  /api/dynamic-investigation/tasks                  任务列表
//   GET  /api/dynamic-investigation/stats                  引擎统计
//   GET  /api/dynamic-investigation/entities               已知实体列表
//   POST /api/dynamic-investigation/entities               {address,entity,label} 添加已知实体

// Handler 是动态调查引擎的 HTTP 处理器。
type Handler struct {
	engine *Engine
}

// NewHandler 创建 HTTP handler。
func NewHandler(engine *Engine) *Handler {
	return &Handler{engine: engine}
}

// Engine 暴露引擎（测试/装配用）。
func (h *Handler) Engine() *Engine { return h.engine }

// ServeHTTP 路由分发。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		h.json(w, http.StatusServiceUnavailable, map[string]string{"detail": "动态调查引擎未初始化"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/dynamic-investigation")
	path = strings.Trim(path, "/")
	parts := []string{}
	if path != "" {
		parts = strings.Split(path, "/")
	}

	switch {
	case len(parts) == 1 && parts[0] == "start" && r.Method == http.MethodPost:
		h.start(w, r)
	case len(parts) == 1 && parts[0] == "queue" && r.Method == http.MethodGet:
		h.listQueue(w, r)
	case len(parts) == 2 && parts[0] == "queue" && r.Method == http.MethodGet:
		h.getQueueItem(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "queue" && parts[2] == "approve" && r.Method == http.MethodPost:
		h.approve(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "queue" && parts[2] == "ignore" && r.Method == http.MethodPost:
		h.ignore(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "config" && r.Method == http.MethodGet:
		h.getConfig(w, r)
	case len(parts) == 1 && parts[0] == "config" && r.Method == http.MethodPost:
		h.updateConfig(w, r)
	case len(parts) == 1 && parts[0] == "tasks" && r.Method == http.MethodGet:
		h.getTasks(w, r)
	case len(parts) == 1 && parts[0] == "stats" && r.Method == http.MethodGet:
		h.getStats(w, r)
	case len(parts) == 1 && parts[0] == "entities" && r.Method == http.MethodGet:
		h.getEntities(w, r)
	case len(parts) == 1 && parts[0] == "entities" && r.Method == http.MethodPost:
		h.addEntity(w, r)
	default:
		h.json(w, http.StatusNotFound, map[string]string{"detail": "接口不存在"})
	}
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string         `json:"target"`
		Config map[string]any `json:"config,omitempty"`
	}
	// 限制请求体大小（防资源耗尽）
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "请求格式错误: " + err.Error()})
		return
	}
	if !IsValidEVMAddress(req.Target) {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "target 不是合法的 EVM 地址"})
		return
	}
	if len(req.Config) > 0 {
		h.applyConfigUpdate(req.Config)
	}
	if err := h.engine.Start(r.Context(), req.Target); err != nil {
		// 错误脱敏：细节记录服务端日志，不向客户端回显 SQL/路径
		logger.Log.Error().Str("target", req.Target).Err(err).Msg("dynamic_investigation_start_failed")
		h.json(w, http.StatusInternalServerError, map[string]string{"detail": "调查启动失败"})
		return
	}
	h.json(w, http.StatusOK, h.engine.Stats())
}

func (h *Handler) listQueue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := DiscoveryStatus(q.Get("status"))
	entity := EntityType(q.Get("entity"))
	depth := -1
	if v := q.Get("depth"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			depth = n
		}
	}
	items := h.engine.Queue().List(status, entity, depth)
	h.json(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
}

func (h *Handler) getQueueItem(w http.ResponseWriter, _ *http.Request, address string) {
	if !IsValidEVMAddress(address) {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "地址格式非法"})
		return
	}
	item, ok := h.engine.Queue().Get(address)
	if !ok {
		h.json(w, http.StatusNotFound, map[string]string{"detail": "地址不在扩展队列中"})
		return
	}
	h.json(w, http.StatusOK, item)
}

func (h *Handler) approve(w http.ResponseWriter, _ *http.Request, address string) {
	if !IsValidEVMAddress(address) {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "地址格式非法"})
		return
	}
	item, ok := h.engine.Queue().Get(address)
	if !ok {
		h.json(w, http.StatusNotFound, map[string]string{"detail": "地址不在扩展队列中"})
		return
	}
	// 手动批准：忽略评分直接置为 APPROVED，并补采集路由
	if item.Acquisition == "" {
		route := Route(RouteInput{
			Entity:       item.Entity,
			Decision:     DecisionAcquire,
			Score:        item.Score,
			CurrentLevel: item.DataLevel,
			Depth:        item.Depth,
		}, h.engine.Config())
		h.engine.Queue().SetAcquisition(address, route.Mode, route.TargetLevel)
	}
	if err := h.engine.Queue().Transition(address, StatusApproved); err != nil {
		// 人工复核：终态（IGNORED）允许强制恢复
		h.engine.Queue().SetStatus(address, StatusApproved)
	}
	updated, _ := h.engine.Queue().Get(address)
	h.json(w, http.StatusOK, updated)
}

func (h *Handler) ignore(w http.ResponseWriter, _ *http.Request, address string) {
	if !IsValidEVMAddress(address) {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "地址格式非法"})
		return
	}
	if _, ok := h.engine.Queue().Get(address); !ok {
		h.json(w, http.StatusNotFound, map[string]string{"detail": "地址不在扩展队列中"})
		return
	}
	if err := h.engine.Queue().Transition(address, StatusIgnored); err != nil {
		// 终态允许直接覆盖
		h.engine.Queue().SetStatus(address, StatusIgnored)
	}
	h.engine.Queue().SetIgnoredReason(address, "手动忽略")
	updated, _ := h.engine.Queue().Get(address)
	h.json(w, http.StatusOK, updated)
}

func (h *Handler) getConfig(w http.ResponseWriter, _ *http.Request) {
	h.json(w, http.StatusOK, h.engine.Config())
}

func (h *Handler) updateConfig(w http.ResponseWriter, r *http.Request) {
	// 部分更新：只覆盖请求中显式提供的字段，其余保持当前值
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "请求格式错误: " + err.Error()})
		return
	}
	h.applyConfigUpdate(raw)
	h.json(w, http.StatusOK, h.engine.Config())
}

// applyConfigUpdate 将请求中的显式配置字段合并到引擎当前配置（部分更新），
// 并钳制非法值（负深度/数量、越界权重）。
func (h *Handler) applyConfigUpdate(raw map[string]any) {
	cfg := h.engine.Config()
	applyInt(raw, "max_depth", &cfg.MaxDepth)
	applyInt(raw, "max_addresses", &cfg.MaxAddresses)
	applyInt(raw, "relations_per_address", &cfg.RelationsPerAddress)
	applyFloat(raw, "min_score", &cfg.MinScore)
	applyFloat(raw, "risk_weight", &cfg.RiskWeight)
	applyFloat(raw, "relation_weight", &cfg.RelationWeight)
	applyFloat(raw, "activity_weight", &cfg.ActivityWeight)
	applyFloat(raw, "amount_weight", &cfg.AmountWeight)
	applyFloat(raw, "entity_penalty", &cfg.EntityPenalty)
	applyString(raw, "amount_threshold", &cfg.AmountThreshold)
	applyString(raw, "chain_id", &cfg.ChainID)
	applyBool(raw, "use_sqd", &cfg.UseSQD)
	applyBool(raw, "use_csv_direct", &cfg.UseCSVDirect)
	// 钳制：深度≥0、数量≥1、每地址关系≥1、评分≥0、权重≥0
	// 钳制：深度 [0,10]、数量 [1,10000]、每地址关系 [1,500]、评分 [0,100]、权重 [0,100]
	if cfg.MaxDepth < 0 {
		cfg.MaxDepth = 0
	}
	if cfg.MaxDepth > 10 {
		cfg.MaxDepth = 10
	}
	if cfg.MaxAddresses < 1 {
		cfg.MaxAddresses = 1
	}
	if cfg.MaxAddresses > 10000 {
		cfg.MaxAddresses = 10000
	}
	if cfg.RelationsPerAddress < 1 {
		cfg.RelationsPerAddress = 1
	}
	if cfg.RelationsPerAddress > 500 {
		cfg.RelationsPerAddress = 500
	}
	if cfg.MinScore < 0 {
		cfg.MinScore = 0
	}
	if cfg.MinScore > 100 {
		cfg.MinScore = 100
	}
	clampWeight := func(w *float64) {
		if *w < 0 {
			*w = 0
		}
		if *w > 100 {
			*w = 100
		}
	}
	clampWeight(&cfg.RiskWeight)
	clampWeight(&cfg.RelationWeight)
	clampWeight(&cfg.ActivityWeight)
	clampWeight(&cfg.AmountWeight)
	clampWeight(&cfg.EntityPenalty)
	// chain_id 白名单：仅允许已注册 EVM 链
	if cfg.ChainID != "" {
		if _, err := chain.Resolve(cfg.ChainID); err != nil {
			cfg.ChainID = "bsc" // 回退默认链
		}
	}
	h.engine.UpdateConfig(cfg)
}

func applyInt(raw map[string]any, key string, dst *int) {
	if v, ok := raw[key]; ok {
		if f, ok2 := v.(float64); ok2 {
			*dst = int(f)
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

func applyString(raw map[string]any, key string, dst *string) {
	if v, ok := raw[key]; ok {
		if s, ok2 := v.(string); ok2 {
			*dst = s
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

func (h *Handler) getTasks(w http.ResponseWriter, _ *http.Request) {
	tasks := h.engine.Tasks()
	h.json(w, http.StatusOK, map[string]any{"total": len(tasks), "items": tasks})
}

func (h *Handler) getStats(w http.ResponseWriter, _ *http.Request) {
	h.json(w, http.StatusOK, h.engine.Stats())
}

func (h *Handler) getEntities(w http.ResponseWriter, r *http.Request) {
	recognizer := h.engine.recognizer
	if recognizer == nil {
		h.json(w, http.StatusOK, map[string]any{"total": 0, "items": []KnownEntity{}})
		return
	}
	items := recognizer.Known()
	h.json(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
}

func (h *Handler) addEntity(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string     `json:"address"`
		Entity  EntityType `json:"entity"`
		Label   string     `json:"label"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "请求格式错误: " + err.Error()})
		return
	}
	if !IsValidEVMAddress(req.Address) {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "address 不是合法的 EVM 地址"})
		return
	}
	if req.Entity == "" {
		h.json(w, http.StatusBadRequest, map[string]string{"detail": "entity 不能为空"})
		return
	}
	h.engine.recognizer.AddKnown(KnownEntity{
		Address: strings.ToLower(req.Address),
		Entity:  req.Entity,
		Label:   req.Label,
	})
	h.json(w, http.StatusOK, map[string]string{"detail": "已添加已知实体"})
}

// ── 工具 ──

func (h *Handler) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
