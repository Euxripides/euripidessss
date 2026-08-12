package smartdownload

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/downloadscheduler"
)

// Handler 智能下载统一 API（挂载于 /api/smart-download/*）。
type Handler struct {
	svc        *Service
	bridge     *LegacyPlanBridge
	planLookup func(planID string) *downloadscheduler.Plan
}

// NewHandler 创建 API handler。
func NewHandler(svc *Service, planLookup func(planID string) *downloadscheduler.Plan) *Handler {
	return &Handler{svc: svc, bridge: NewLegacyPlanBridge(svc), planLookup: planLookup}
}

// ServeHTTP 路由（路径已由 http.StripPrefix 去掉 /api/smart-download）。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodPost && path == "planner-v2":
		h.plannerV2(w, r)
	case r.Method == http.MethodPost && path == "preflight":
		h.preflight(w, r)
	case r.Method == http.MethodPost && path == "batches":
		h.createBatch(w, r)
	case r.Method == http.MethodGet && path == "batches":
		h.listBatches(w, r)
	case r.Method == http.MethodGet && path == "status":
		h.status(w, r)
	case r.Method == http.MethodGet && path == "events":
		h.events(w, r)
	case r.Method == http.MethodPost && path == "legacy/plan":
		h.bridgePlan(w, r)
	case r.Method == http.MethodPost && path == "import":
		h.importFile(w, r)
	case r.Method == http.MethodGet && path == "registry":
		writeSmartJSON(w, http.StatusOK, map[string]any{"results": h.svc.Results().List()})
	case r.Method == http.MethodPost && path == "coverage/query":
		h.coverageQuery(w, r)
	case path == "templates" || strings.HasPrefix(path, "templates/"):
		h.routeTemplates(w, r, strings.TrimPrefix(path, "templates"))
	case r.Method == http.MethodPost && path == "compare":
		h.compareRuns(w, r)
	case r.Method == http.MethodGet && path == "performance-history":
		h.performanceHistory(w)
	case strings.HasPrefix(path, "batches/"):
		h.routeBatch(w, r, strings.TrimPrefix(path, "batches/"))
	case strings.HasPrefix(path, "addresses/"):
		h.routeAddress(w, r, strings.TrimPrefix(path, "addresses/"))
	case strings.HasPrefix(path, "datasets/"):
		h.routeDataset(w, r, strings.TrimPrefix(path, "datasets/"))
	case strings.HasPrefix(path, "results/"):
		h.routeResults(w, r, strings.TrimPrefix(path, "results/"))
	case strings.HasPrefix(path, "jobs/"):
		h.routeJobs(w, r, strings.TrimPrefix(path, "jobs/"))
	default:
		writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "unknown smart-download endpoint: " + path})
	}
}

func (h *Handler) preflight(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var req CreateBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := h.svc.Preflight(ctx, req)
	if err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeSmartJSON(w, http.StatusOK, result)
}

func (h *Handler) plannerV2(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var req CreateBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	plan, err := h.svc.PlannerV2(ctx, req)
	if err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeSmartJSON(w, http.StatusOK, plan)
}

func (h *Handler) routeTemplates(w http.ResponseWriter, r *http.Request, rest string) {
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			items, err := h.svc.ListTemplates()
			if err != nil {
				writeSmartJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
				return
			}
			writeSmartJSON(w, http.StatusOK, map[string]any{"templates": items, "total": len(items)})
			return
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
			var body struct {
				ID              string              `json:"id,omitempty"`
				Name            string              `json:"name"`
				Description     string              `json:"description,omitempty"`
				ResourceProfile ResourceProfile     `json:"resource_profile,omitempty"`
				Request         *CreateBatchRequest `json:"request,omitempty"`
				Configuration   json.RawMessage     `json:"configuration,omitempty"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
				return
			}
			var request CreateBatchRequest
			if body.Request != nil {
				request = *body.Request
			} else if len(body.Configuration) > 0 && string(body.Configuration) != "null" {
				if err := json.Unmarshal(body.Configuration, &request); err != nil {
					writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "configuration 解析失败: " + err.Error()})
					return
				}
			}
			if body.ResourceProfile != "" {
				request.ResourceProfile = body.ResourceProfile
			}
			input := SaveTemplateRequest{ID: body.ID, Name: body.Name, Description: body.Description, Request: request}
			template, err := h.svc.SaveTemplate(input)
			if err != nil {
				writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
				return
			}
			writeSmartJSON(w, http.StatusCreated, template)
			return
		}
	}
	parts := strings.Split(rest, "/")
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if err := h.svc.DeleteTemplate(id); err != nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": err.Error()})
			return
		}
		writeSmartJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
		return
	}
	if len(parts) == 2 && parts[1] == "instantiate" && r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var overrides TemplateInstantiateOverrides
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&overrides); err != nil && err != io.EOF {
			writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "模板 overrides 解析失败: " + err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		result, err := h.svc.InstantiateTemplateWithOverrides(ctx, id, &overrides)
		if err != nil {
			writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
			return
		}
		writeSmartJSON(w, http.StatusCreated, result)
		return
	}
	writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "unknown template endpoint"})
}

func (h *Handler) compareRuns(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var req CompareRunsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	result, err := h.svc.CompareRuns(req)
	if err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeSmartJSON(w, http.StatusOK, result)
}

func (h *Handler) performanceHistory(w http.ResponseWriter) {
	runs, err := h.svc.PerformanceHistory()
	if err != nil {
		writeSmartJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}
	writeSmartJSON(w, http.StatusOK, map[string]any{"runs": runs, "total": len(runs)})
}

// coverageQuery POST /coverage/query — Coverage Index V2 区间查询（设计 §45）。
func (h *Handler) coverageQuery(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		ChainKey  string `json:"chain_key"`
		Address   string `json:"address"`
		Dataset   string `json:"dataset"`
		FromBlock uint64 `json:"from_block"`
		ToBlock   uint64 `json:"to_block"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if !evmAddressRE.MatchString(body.Address) {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "非法 EVM 地址"})
		return
	}
	if _, err := chain.Resolve(body.ChainKey); err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	if !ValidDataset(body.Dataset) {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "非法数据集: " + body.Dataset})
		return
	}
	if body.ToBlock < body.FromBlock {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "to_block 不能小于 from_block"})
		return
	}
	result := h.svc.CoverageQuery(body.ChainKey, strings.ToLower(body.Address), body.Dataset,
		body.FromBlock, body.ToBlock)
	writeSmartJSON(w, http.StatusOK, result)
}

// routeJobs GET /jobs/{batch_id}/snapshot 与 /jobs/{batch_id}/addresses/{address_job_id}。
func (h *Handler) routeJobs(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" {
		writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "batch id 缺失"})
		return
	}
	batchID := parts[0]
	if len(parts) == 2 && parts[1] == "snapshot" && r.Method == http.MethodGet {
		snap := h.svc.BatchSnapshot(batchID)
		if snap == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "批次不存在: " + batchID})
			return
		}
		writeSmartJSON(w, http.StatusOK, snap)
		return
	}
	if len(parts) == 2 && parts[1] == "summary" && r.Method == http.MethodGet {
		addresses := h.svc.store.ListAddressesByBatch(batchID)
		counts := map[string]int{}
		throughput := 0.0
		for _, a := range addresses {
			counts[string(a.Status)]++
			throughput += a.Progress.SpeedRowsPerSec
		}
		snap := h.svc.BatchSnapshot(batchID)
		if snap == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "批次不存在: " + batchID})
			return
		}
		writeSmartJSON(w, http.StatusOK, map[string]any{
			"snapshot": snap, "counts": counts, "total": len(addresses),
			"running":                 counts["DOWNLOADING"] + counts["VALIDATING"],
			"queued":                  counts["WAITING"],
			"attention":               counts["FAILED"] + counts["PARTIAL"],
			"throughput_rows_per_sec": throughput,
		})
		return
	}
	if len(parts) == 3 && parts[1] == "addresses" && r.Method == http.MethodGet {
		a := h.svc.GetAddress(parts[2])
		if a == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "地址任务不存在: " + parts[2]})
			return
		}
		detail := &AddressDetail{Address: a}
		for _, ds := range h.svc.store.ListDatasetsByAddress(a.ID) {
			detail.Datasets = append(detail.Datasets, &DatasetDetail{
				Dataset: ds, Ranges: h.svc.store.ListRangesByDataset(ds.ID),
			})
		}
		writeSmartJSON(w, http.StatusOK, detail)
		return
	}
	writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "unknown jobs endpoint: " + rest})
}

// importFile POST /import — 上传 TXT/CSV/XLSX 自动识别地址列。
func (h *Handler) importFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "multipart 解析失败: " + err.Error()})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "缺少 file 字段"})
		return
	}
	defer file.Close()
	result, err := h.svc.ImportAddresses(header.Filename, file)
	if err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeSmartJSON(w, http.StatusOK, result)
}

// routeResults GET /results/{dsID} 与 /results/{dsID}/summary。
func (h *Handler) routeResults(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "dataset id 缺失"})
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "summary" && r.Method == http.MethodGet {
		summary, err := h.svc.Results().ResultSummary(r.Context(), id)
		if err != nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": err.Error()})
			return
		}
		writeSmartJSON(w, http.StatusOK, summary)
		return
	}
	if len(parts) == 2 && parts[1] == "export" && r.Method == http.MethodGet {
		if h.svc.GetDataset(id) == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "数据集不存在: " + id})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
		defer cancel()
		path, format, rows, err := h.svc.Results().ExportDataset(ctx, id)
		if err != nil {
			writeSmartJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		f, err := os.Open(path)
		if err != nil {
			writeSmartJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
			return
		}
		defer f.Close()
		ext := ".xlsx"
		contentType := "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		if format == "csv" {
			ext = ".csv"
			contentType = "text/csv; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(
			`attachment; filename="smart_download_%s_%d%s"`, id[:min(len(id), 8)], rows, ext))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		if h.svc.GetDataset(id) == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "数据集不存在: " + id})
			return
		}
		page, size := pagination(r)
		sortCol := strings.TrimSpace(r.URL.Query().Get("sort"))
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		rows, total, err := h.svc.Results().QueryResults(ctx, id, page, size, sortCol, filter)
		if err != nil {
			if IsResultQueryParamError(err) {
				writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
			} else {
				writeSmartJSON(w, http.StatusInternalServerError, map[string]any{"detail": "结果查询失败"})
			}
			return
		}
		writeSmartJSON(w, http.StatusOK, map[string]any{
			"rows": rows, "total": total, "page": page, "page_size": size,
		})
		return
	}
	writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "unknown results endpoint: " + rest})
}

// ── 批次 ──

func (h *Handler) createBatch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var req CreateBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := h.svc.CreateBatch(ctx, req)
	if err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeSmartJSON(w, http.StatusCreated, resp)
}

func (h *Handler) listBatches(w http.ResponseWriter, r *http.Request) {
	batches := h.svc.ListBatches()
	writeSmartJSON(w, http.StatusOK, map[string]any{
		"batches":  batches,
		"total":    len(batches),
		"adapters": h.svc.AdapterNames(),
		"workers":  h.svc.Options().Workers,
	})
}

func (h *Handler) routeBatch(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "batch id 缺失"})
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		detail := h.svc.SnapshotBatch(id)
		if detail == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "批次不存在: " + id})
			return
		}
		writeSmartJSON(w, http.StatusOK, detail)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		switch parts[1] {
		case "mode":
			r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
			var body struct {
				Mode DownloadMode `json:"mode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
				return
			}
			batch, err := h.svc.SetBatchMode(id, body.Mode)
			writeLifecycle(w, batch, err)
			return
		case "start":
			err := h.svc.Start(id)
			if err != nil {
				writeSmartJSON(w, http.StatusConflict, map[string]any{"detail": err.Error()})
				return
			}
			writeSmartJSON(w, http.StatusOK, h.svc.GetBatch(id))
			return
		case "plan":
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			plan, err := h.svc.PlanBatch(ctx, id)
			if err != nil {
				writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": err.Error()})
				return
			}
			writeSmartJSON(w, http.StatusOK, plan)
			return
		case "pause":
			batch, err := h.svc.PauseBatch(id)
			writeLifecycle(w, batch, err)
			return
		case "resume":
			batch, err := h.svc.ResumeBatch(id)
			writeLifecycle(w, batch, err)
			return
		case "cancel":
			batch, err := h.svc.CancelBatch(id)
			writeLifecycle(w, batch, err)
			return
		}
	}
	if len(parts) == 2 && parts[1] == "turbo-status" && r.Method == http.MethodGet {
		status, err := h.svc.TurboStatus(id)
		if err != nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": err.Error()})
			return
		}
		writeSmartJSON(w, http.StatusOK, status)
		return
	}
	if len(parts) == 2 && parts[1] == "hardening" && r.Method == http.MethodGet {
		status, err := h.svc.HardeningStatus(id)
		if err != nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": err.Error()})
			return
		}
		writeSmartJSON(w, http.StatusOK, status)
		return
	}
	if len(parts) == 2 && parts[1] == "accelerator" && r.Method == http.MethodGet {
		plan := h.svc.BatchAccelerator(id)
		if plan == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "批次不存在: " + id})
			return
		}
		writeSmartJSON(w, http.StatusOK, plan)
		return
	}
	if len(parts) == 2 && parts[1] == "report" && r.Method == http.MethodGet {
		report, err := h.svc.GetJobReport(id)
		if err != nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": err.Error()})
			return
		}
		writeSmartJSON(w, http.StatusOK, report)
		return
	}
	if len(parts) == 2 && parts[1] == "plan" && r.Method == http.MethodGet {
		plan := h.planView(id)
		if plan == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "批次不存在: " + id})
			return
		}
		writeSmartJSON(w, http.StatusOK, plan)
		return
	}
	if len(parts) == 2 && parts[1] == "addresses" && r.Method == http.MethodGet {
		h.listBatchAddresses(w, r, id)
		return
	}
	writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "unknown batch endpoint: " + rest})
}

// planView 从已保存的估算值构建执行计划视图（不重新探测）。
func (h *Handler) planView(batchID string) *ExecutionPlan {
	detail := h.svc.SnapshotBatch(batchID)
	if detail == nil {
		return nil
	}
	plan := &ExecutionPlan{BatchID: batchID}
	for _, a := range detail.Addresses {
		for _, dd := range a.Datasets {
			plan.Datasets = append(plan.Datasets, &DatasetPlan{
				Dataset:           dd.Dataset.Dataset,
				Address:           a.Address.Address,
				ChainKey:          a.Address.ChainKey,
				EstimatedRows:     dd.Dataset.EstimatedRows,
				EstimatedBytes:    dd.Dataset.EstimatedBytes,
				SizeClass:         sizeClass(dd.Dataset.EstimatedRows),
				PreferredProvider: dd.Dataset.PreferredProvider,
			})
		}
	}
	return plan
}

func (h *Handler) listBatchAddresses(w http.ResponseWriter, r *http.Request, batchID string) {
	addresses := h.svc.store.ListAddressesByBatch(batchID)
	if addresses == nil {
		writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "批次不存在: " + batchID})
		return
	}
	page, size := pagination(r)
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	// 每地址活跃 Dataset（RUNNING/VALIDATING/REPAIRING/PENDING 中更新时间最新者）→ 当前数据/Provider/云档
	active := map[string]*DatasetJob{}
	for _, d := range h.svc.store.ListDatasets() {
		if d.BatchID != batchID || d.Status.Terminal() {
			continue
		}
		cur := active[d.AddressJobID]
		if cur == nil || d.UpdatedAt.After(cur.UpdatedAt) {
			active[d.AddressJobID] = d
		}
	}
	filtered := make([]*AddressJob, 0, len(addresses))
	for _, a := range addresses {
		if status != "" && strings.ToLower(string(a.Status)) != status {
			continue
		}
		if d := active[a.ID]; d != nil {
			a.CurrentDataset = d.Dataset
			a.CurrentProvider = d.CurrentProvider
			a.CloudTier = d.CloudTier
		}
		filtered = append(filtered, a)
	}
	start := (page - 1) * size
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + size
	if end > len(filtered) {
		end = len(filtered)
	}
	writeSmartJSON(w, http.StatusOK, map[string]any{
		"addresses": filtered[start:end],
		"total":     len(filtered),
		"page":      page,
		"page_size": size,
	})
}

// ── 地址 ──

func (h *Handler) routeAddress(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "address id 缺失"})
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		a := h.svc.GetAddress(id)
		if a == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "地址任务不存在: " + id})
			return
		}
		detail := &AddressDetail{Address: a}
		for _, ds := range h.svc.store.ListDatasetsByAddress(id) {
			detail.Datasets = append(detail.Datasets, &DatasetDetail{
				Dataset: ds,
				Ranges:  h.svc.store.ListRangesByDataset(ds.ID),
			})
		}
		writeSmartJSON(w, http.StatusOK, detail)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		switch parts[1] {
		case "pause":
			a, err := h.svc.PauseAddress(id)
			writeLifecycle(w, a, err)
			return
		case "resume":
			a, err := h.svc.ResumeAddress(id)
			writeLifecycle(w, a, err)
			return
		case "cancel":
			a, err := h.svc.CancelAddress(id)
			writeLifecycle(w, a, err)
			return
		}
	}
	writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "unknown address endpoint: " + rest})
}

// ── 数据集 ──

func (h *Handler) routeDataset(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "dataset id 缺失"})
		return
	}
	id := parts[0]
	if len(parts) == 2 && r.Method == http.MethodPost {
		switch parts[1] {
		case "pause":
			ds, err := h.svc.PauseDataset(id)
			writeLifecycle(w, ds, err)
			return
		case "resume":
			ds, err := h.svc.ResumeDataset(id)
			writeLifecycle(w, ds, err)
			return
		case "cancel":
			ds, err := h.svc.CancelDataset(id)
			writeLifecycle(w, ds, err)
			return
		}
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		switch parts[1] {
		case "ledger":
			if h.svc.GetDataset(id) == nil {
				writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "数据集不存在: " + id})
				return
			}
			entries, err := h.svc.LedgerEntries(id)
			if err != nil {
				writeSmartJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
				return
			}
			writeSmartJSON(w, http.StatusOK, map[string]any{"ledger": entries})
			return
		case "checkpoint":
			cp, err := h.svc.Checkpoint(id)
			if err != nil {
				writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": err.Error()})
				return
			}
			writeSmartJSON(w, http.StatusOK, cp)
			return
		case "validation":
			ds := h.svc.GetDataset(id)
			if ds == nil {
				writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "数据集不存在: " + id})
				return
			}
			writeSmartJSON(w, http.StatusOK, ds.Validation)
			return
		}
	}
	if len(parts) == 2 && parts[1] == "repair" && r.Method == http.MethodPost {
		report, err := h.svc.RepairDatasetGaps(id)
		if err != nil {
			writeSmartJSON(w, http.StatusConflict, map[string]any{"detail": err.Error()})
			return
		}
		writeSmartJSON(w, http.StatusOK, report)
		return
	}
	writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "unknown dataset endpoint: " + rest})
}

// events GET /events — SSE 实时事件流（Progress Aggregator 300ms 合并）。
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "SSE 需要 Flusher"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	batchID := strings.TrimSpace(r.URL.Query().Get("batch_id"))
	addressID := strings.TrimSpace(r.URL.Query().Get("address_job_id"))
	lastID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if lastID == "" {
		lastID = strings.TrimSpace(r.URL.Query().Get("last_event_id"))
	}
	var afterSeq uint64
	if lastID != "" {
		afterSeq, _ = strconv.ParseUint(lastID, 10, 64)
	}
	id, ch := h.svc.Events().Subscribe()
	defer h.svc.Events().Unsubscribe(id)
	// Last-Event-ID 回放（设计 §32）：超出缓冲 → resync_required
	if lastID != "" {
		events, ok := h.svc.Events().Replay(batchID, afterSeq)
		if !ok {
			_, _ = fmt.Fprintf(w, "id: %d\nevent: resync_required\ndata: {\"reason\":\"buffer_expired\"}\n\n",
				h.svc.Events().CurrentSequence(batchID))
			flusher.Flush()
		} else {
			for _, ev := range events {
				writeSSE(w, ev, flusher)
			}
		}
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			if batchID != "" && ev.BatchID != "" && ev.BatchID != batchID {
				continue
			}
			if addressID != "" && ev.AddressJobID != "" && ev.AddressJobID != addressID {
				continue
			}
			writeSSE(w, ev, flusher)
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, ev Event, flusher http.Flusher) {
	payload, _ := json.Marshal(ev)
	if ev.Sequence > 0 {
		_, _ = fmt.Fprintf(w, "id: %d\n", ev.Sequence)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload)
	flusher.Flush()
}

// ── Legacy 桥接 ──

func (h *Handler) bridgePlan(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var body struct {
		PlanID string                  `json:"plan_id,omitempty"`
		Plan   *downloadscheduler.Plan `json:"plan,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	var plan *downloadscheduler.Plan
	if body.Plan != nil {
		plan = body.Plan
	} else if body.PlanID != "" && h.planLookup != nil {
		plan = h.planLookup(body.PlanID)
		if plan == nil {
			writeSmartJSON(w, http.StatusNotFound, map[string]any{"detail": "plan 不存在: " + body.PlanID})
			return
		}
	} else {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": "需要 plan 或 plan_id"})
		return
	}
	resp, err := h.bridge.BridgePlan(r.Context(), plan)
	if err != nil {
		writeSmartJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	writeSmartJSON(w, http.StatusCreated, resp)
}

// ── 汇总 ──

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	chainKey := strings.TrimSpace(r.URL.Query().Get("chain_key"))
	if chainKey == "" {
		chainKey = "bsc"
	}
	mode := DownloadMode(strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("mode"))))
	if !mode.Valid() {
		mode = DownloadModeAuto
	}
	availableDatasets := h.svc.AvailableDatasets(chainKey, mode)
	// Creation-form capability refresh is latency-sensitive and does not need
	// lifecycle counts. Avoid contending on the persisted task store while
	// startup reconciliation or large-batch progress updates are active.
	if r.URL.Query().Get("capabilities_only") == "true" {
		writeSmartJSON(w, http.StatusOK, map[string]any{
			"chain_key":          chainKey,
			"mode":               mode,
			"available_datasets": availableDatasets,
		})
		return
	}
	batches := h.svc.ListBatches()
	counts := map[string]int{}
	for _, b := range batches {
		counts[string(b.Status)]++
	}
	writeSmartJSON(w, http.StatusOK, map[string]any{
		"batches":            len(batches),
		"counts":             counts,
		"adapters":           h.svc.AdapterNames(),
		"workers":            h.svc.Options().Workers,
		"retry_limit":        h.svc.Options().RetryLimit,
		"chain_key":          chainKey,
		"mode":               mode,
		"available_datasets": availableDatasets,
	})
}

// ── 辅助 ──

func writeLifecycle(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeSmartJSON(w, http.StatusConflict, map[string]any{"detail": err.Error()})
		return
	}
	writeSmartJSON(w, http.StatusOK, v)
}

func pagination(r *http.Request) (page, size int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	size, _ = strconv.Atoi(r.URL.Query().Get("page_size"))
	if size <= 0 {
		size = 50
	}
	if size > 500 {
		size = 500
	}
	return page, size
}

func writeSmartJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
