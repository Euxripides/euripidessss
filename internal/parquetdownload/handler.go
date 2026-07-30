package parquetdownload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/datasourcemanager"
	"github.com/etl/backend/internal/rpcmanager"
)

type Handler struct {
	manager *Manager
	mux     *http.ServeMux
}

func NewHandler(rootDir string, engine *duckdb.Engine) (*Handler, error) {
	manager, err := NewManager(rootDir, engine)
	if err != nil {
		return nil, err
	}
	handler := &Handler{manager: manager, mux: http.NewServeMux()}
	handler.register()
	return handler, nil
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mux.ServeHTTP(writer, request)
}

func (h *Handler) Close() {
	h.manager.Close()
}

func (h *Handler) SetRPCManager(manager *rpcmanager.Manager) {
	h.manager.SetRPCManager(manager)
}

func (h *Handler) SetDataSourceManager(manager *datasourcemanager.Manager) {
	h.manager.SetDataSourceManager(manager)
}

func (h *Handler) register() {
	h.mux.HandleFunc("/settings", h.settings)
	h.mux.HandleFunc("/preview", h.preview)
	h.mux.HandleFunc("/start", h.start)
	h.mux.HandleFunc("/job", h.job)
	h.mux.HandleFunc("/jobs", h.jobs)
	h.mux.HandleFunc("/cancel", h.cancel)
	h.mux.HandleFunc("/retry", h.retry)
	h.mux.HandleFunc("/addresses/upload", h.uploadAddresses)
	h.mux.HandleFunc("/file", h.downloadFile)
	h.mux.HandleFunc("/address/", h.address)
	h.mux.HandleFunc("/crypto/addresses/", h.firstSeen)
}

func (h *Handler) settings(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, h.manager.Settings())
	case http.MethodPost:
		var settings Settings
		if err := decodeJSON(request, &settings); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		saved, err := h.manager.SaveSettings(settings)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		writeJSON(writer, http.StatusOK, saved)
	default:
		writeMethodNotAllowed(writer)
	}
}

func (h *Handler) preview(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowed(writer)
		return
	}
	var payload StartRequest
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	preview, err := h.manager.Preview(request.Context(), payload)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	writeJSON(writer, http.StatusOK, preview)
}

func (h *Handler) start(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowed(writer)
		return
	}
	var payload StartRequest
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	job, err := h.manager.Start(request.Context(), payload)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, job)
}

func (h *Handler) job(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowed(writer)
		return
	}
	job, err := h.manager.Get(request.URL.Query().Get("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, err)
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func (h *Handler) jobs(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowed(writer)
		return
	}
	writeJSON(writer, http.StatusOK, h.manager.List())
}

func (h *Handler) cancel(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowed(writer)
		return
	}
	job, err := h.manager.Cancel(request.URL.Query().Get("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, err)
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func (h *Handler) retry(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowed(writer)
		return
	}
	job, err := h.manager.Retry(request.URL.Query().Get("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, job)
}

func (h *Handler) uploadAddresses(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowed(writer)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 32<<20)
	if err := request.ParseMultipartForm(32 << 20); err != nil {
		writeError(writer, http.StatusBadRequest, fmt.Errorf("地址文件不能超过 32 MB: %w", err))
		return
	}
	file, _, err := request.FormFile("file")
	if err != nil {
		writeError(writer, http.StatusBadRequest, errors.New("请选择地址文件"))
		return
	}
	file.Close()
	header := firstFileHeader(request.MultipartForm, "file")
	if header == nil {
		writeError(writer, http.StatusBadRequest, errors.New("请选择地址文件"))
		return
	}
	response, err := parseAddressUpload(header)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func firstFileHeader(form *multipart.Form, name string) *multipart.FileHeader {
	if form == nil || len(form.File[name]) == 0 {
		return nil
	}
	return form.File[name][0]
}

func (h *Handler) downloadFile(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowed(writer)
		return
	}
	job, err := h.manager.Get(request.URL.Query().Get("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, err)
		return
	}
	requested := filepath.Clean(request.URL.Query().Get("path"))
	allowed := false
	for _, output := range job.Outputs {
		if filepath.Clean(output) == requested {
			allowed = true
			break
		}
	}
	if !allowed {
		writeError(writer, http.StatusForbidden, errors.New("该文件不属于当前任务"))
		return
	}
	info, err := os.Stat(requested)
	if err != nil || info.IsDir() {
		writeError(writer, http.StatusNotFound, errors.New("结果文件不存在"))
		return
	}
	writer.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''`+urlPathEscape(filepath.Base(requested)))
	http.ServeFile(writer, request, requested)
}

func (h *Handler) firstSeen(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowed(writer)
		return
	}
	// Path: /crypto/addresses/{chain}/{address}/first-seen
	path := strings.TrimPrefix(request.URL.Path, "/crypto/addresses/")
	parts := strings.SplitN(strings.Trim(path, "/"), "/", 3)
	if len(parts) < 2 || parts[len(parts)-1] != "first-seen" {
		writeError(writer, http.StatusNotFound, errors.New("接口不存在，请使用 /crypto/addresses/{chain}/{address}/first-seen"))
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(parts[0]))
	address := strings.ToLower(strings.TrimSpace(parts[1]))
	if !isEVMAddress(address) {
		writeError(writer, http.StatusBadRequest, errors.New("EVM 地址格式错误"))
		return
	}
	resp, err := h.manager.queryFirstSeen(request.Context(), chainKey, address)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err)
		return
	}
	writeJSON(writer, http.StatusOK, resp)
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("请求格式错误: %w", err)
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}

func writeError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]string{"detail": err.Error()})
}

func writeMethodNotAllowed(writer http.ResponseWriter) {
	writeError(writer, http.StatusMethodNotAllowed, errors.New("请求方法不支持"))
}

func urlPathEscape(value string) string {
	replacer := strings.NewReplacer("%", "%25", " ", "%20", "#", "%23", "?", "%3F")
	value = replacer.Replace(value)
	var builder strings.Builder
	for _, char := range value {
		if char <= 127 {
			builder.WriteRune(char)
		} else {
			for _, item := range []byte(string(char)) {
				builder.WriteString("%")
				builder.WriteString(strings.ToUpper(strconv.FormatInt(int64(item), 16)))
			}
		}
	}
	return builder.String()
}
