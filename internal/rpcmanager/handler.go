package rpcmanager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type Handler struct {
	manager *Manager
}

func NewHandler(manager *Manager) http.Handler {
	return &Handler{manager: manager}
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	path := "/" + strings.Trim(strings.TrimPrefix(request.URL.Path, "/"), "/")
	switch {
	case path == "/rpc/endpoints":
		h.endpoints(writer, request)
	case path == "/rpc/endpoints/batch" && request.Method == http.MethodPost:
		var input BatchCreateInput
		if !decodeBody(writer, request, &input) {
			return
		}
		result, err := h.manager.CreateBatch(request.Context(), input.Items)
		if err != nil {
			writeResult(writer, nil, err)
			return
		}
		status := http.StatusCreated
		if result.FailureCount > 0 {
			status = http.StatusMultiStatus
		}
		writeJSON(writer, status, result)
	case path == "/rpc/test" && request.Method == http.MethodPost:
		var input EndpointInput
		if !decodeBody(writer, request, &input) {
			return
		}
		writeJSON(writer, http.StatusOK, h.manager.TestInput(request.Context(), input))
	case path == "/rpc/health" && request.Method == http.MethodGet:
		response, err := h.manager.HealthResponse()
		writeResult(writer, response, err)
	case strings.HasPrefix(path, "/rpc/routing/") && request.Method == http.MethodPut:
		var input RoutingInput
		if !decodeBody(writer, request, &input) {
			return
		}
		err := h.manager.UpdateRouting(strings.TrimPrefix(path, "/rpc/routing/"), input)
		writeResult(writer, map[string]any{"success": err == nil}, err)
	case strings.HasPrefix(path, "/rpc/endpoints/"):
		h.endpoint(writer, request, strings.TrimPrefix(path, "/rpc/endpoints/"))
	case path == "/rpc/address/enrich" && request.Method == http.MethodPost:
		var input struct {
			ChainKey string `json:"chain_key"`
			Address  string `json:"address"`
			Force    bool   `json:"force"`
		}
		if !decodeBody(writer, request, &input) {
			return
		}
		result, err := h.manager.Address(request.Context(), input.ChainKey, input.Address, input.Force)
		writeResult(writer, result, err)
	case path == "/rpc/token/metadata" && request.Method == http.MethodPost:
		var input struct {
			ChainKey     string `json:"chain_key"`
			TokenAddress string `json:"token_address"`
			Force        bool   `json:"force"`
		}
		if !decodeBody(writer, request, &input) {
			return
		}
		result, err := h.manager.Token(request.Context(), input.ChainKey, input.TokenAddress, input.Force)
		writeResult(writer, result, err)
	case path == "/enrichment/jobs":
		h.jobs(writer, request)
	case strings.HasPrefix(path, "/enrichment/jobs/"):
		h.job(writer, request, strings.TrimPrefix(path, "/enrichment/jobs/"))
	default:
		writeError(writer, http.StatusNotFound, "RPC 接口不存在")
	}
}

func (h *Handler) endpoints(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		items, err := h.manager.Endpoints()
		writeResult(writer, map[string]any{"items": items}, err)
	case http.MethodPost:
		var input EndpointInput
		if !decodeBody(writer, request, &input) {
			return
		}
		item, err := h.manager.Create(request.Context(), input)
		writeResultStatus(writer, item, err, http.StatusCreated)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "请求方法不支持")
	}
}

func (h *Handler) endpoint(writer http.ResponseWriter, request *http.Request, suffix string) {
	if strings.HasSuffix(suffix, "/test") && request.Method == http.MethodPost {
		result, err := h.manager.TestEndpoint(request.Context(), strings.TrimSuffix(suffix, "/test"))
		writeResult(writer, result, err)
		return
	}
	id := strings.Trim(suffix, "/")
	switch request.Method {
	case http.MethodPut, http.MethodPatch:
		var patch EndpointPatch
		if !decodeBody(writer, request, &patch) {
			return
		}
		item, err := h.manager.Update(request.Context(), id, patch)
		writeResult(writer, item, err)
	case http.MethodDelete:
		err := h.manager.Delete(id)
		if errors.Is(err, ErrEndpointNotFound) {
			writeError(writer, http.StatusNotFound, err.Error())
			return
		}
		writeResult(writer, map[string]any{"success": err == nil}, err)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "请求方法不支持")
	}
}

func (h *Handler) jobs(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		items, err := h.manager.Jobs()
		writeResult(writer, map[string]any{"items": items}, err)
	case http.MethodPost:
		var input JobRequest
		if !decodeBody(writer, request, &input) {
			return
		}
		item, err := h.manager.StartJob(input)
		writeResultStatus(writer, item, err, http.StatusAccepted)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "请求方法不支持")
	}
}

func (h *Handler) job(writer http.ResponseWriter, request *http.Request, suffix string) {
	id := strings.TrimSuffix(strings.Trim(suffix, "/"), "/cancel")
	if strings.HasSuffix(suffix, "/cancel") && request.Method == http.MethodPost {
		item, err := h.manager.CancelJob(id)
		writeResult(writer, item, err)
		return
	}
	if request.Method == http.MethodGet {
		item, err := h.manager.Job(id)
		writeResult(writer, item, err)
		return
	}
	writeError(writer, http.StatusMethodNotAllowed, "请求方法不支持")
}

func decodeBody(writer http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(writer, http.StatusBadRequest, "请求参数无效："+err.Error())
		return false
	}
	return true
}

func writeResult(writer http.ResponseWriter, value any, err error) {
	writeResultStatus(writer, value, err, http.StatusOK)
}

func writeResultStatus(writer http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			code = http.StatusNotFound
		}
		writeError(writer, code, err.Error())
		return
	}
	writeJSON(writer, status, value)
}

func writeError(writer http.ResponseWriter, status int, detail string) {
	writeJSON(writer, status, map[string]string{"detail": redactMessage(detail)})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
