package datasourcemanager

import (
	"encoding/json"
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
	case (path == "/list" || path == "/health" || path == "/metrics") && request.Method == http.MethodGet:
		snapshot, err := h.manager.Snapshot()
		writeResult(writer, snapshot, err, http.StatusOK)
	case path == "/test" && request.Method == http.MethodPost:
		var input struct {
			SourceID string `json:"source_id"`
		}
		if !decode(writer, request, &input) {
			return
		}
		result := h.manager.Test(request.Context(), input.SourceID)
		status := http.StatusOK
		if !result.Success {
			status = http.StatusBadGateway
		}
		writeResult(writer, result, nil, status)
	case path == "/save" && request.Method == http.MethodPost:
		var input ConfigInput
		if !decode(writer, request, &input) {
			return
		}
		source, err := h.manager.Save(request.Context(), input)
		writeResult(writer, source, err, http.StatusOK)
	case path == "/config" && request.Method == http.MethodGet:
		config, err := h.manager.Config(request.URL.Query().Get("id"))
		writeResult(writer, config, err, http.StatusOK)
	case path == "/delete" && request.Method == http.MethodDelete:
		err := h.manager.Delete(request.URL.Query().Get("id"))
		writeResult(writer, map[string]bool{"success": err == nil}, err, http.StatusOK)
	default:
		writeResult(writer, nil, &apiError{status: http.StatusNotFound, message: "数据源接口不存在"}, http.StatusNotFound)
	}
}

type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return e.message }

func decode(writer http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeResult(writer, nil, &apiError{status: http.StatusBadRequest, message: "请求参数无效：" + err.Error()}, http.StatusBadRequest)
		return false
	}
	return true
}

func writeResult(writer http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		if typed, ok := err.(*apiError); ok {
			status = typed.status
		} else {
			status = http.StatusBadRequest
		}
		value = map[string]string{"detail": err.Error()}
	}
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
