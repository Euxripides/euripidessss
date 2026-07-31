package downloadengine

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// JobAPIHandler provides REST API for V2 Job lifecycle.
type JobAPIHandler struct {
	storeDir string
	jobs     map[string]*Job
}

func NewJobAPIHandler(storeDir string) *JobAPIHandler {
	return &JobAPIHandler{
		storeDir: storeDir,
		jobs:     make(map[string]*Job),
	}
}

func (h *JobAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// POST /jobs -> create
	// GET  /jobs/{id} -> get
	// POST /jobs/{id}/pause|resume|cancel|retry-failed -> lifecycle

	path := strings.TrimPrefix(r.URL.Path, "/api/download-engine/jobs")
	path = strings.Trim(path, "/")

	switch {
	case path == "" && r.Method == http.MethodPost:
		h.createJob(w, r)
	case strings.HasSuffix(path, "/pause") && r.Method == http.MethodPost:
		h.pauseJob(w, r, strings.TrimSuffix(path, "/pause"))
	case strings.HasSuffix(path, "/resume") && r.Method == http.MethodPost:
		h.resumeJob(w, r, strings.TrimSuffix(path, "/resume"))
	case strings.HasSuffix(path, "/cancel") && r.Method == http.MethodPost:
		h.cancelJob(w, r, strings.TrimSuffix(path, "/cancel"))
	case strings.HasSuffix(path, "/retry-failed") && r.Method == http.MethodPost:
		h.retryFailed(w, r, strings.TrimSuffix(path, "/retry-failed"))
	case path != "" && r.Method == http.MethodGet:
		h.getJob(w, r, path)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "接口不存在"})
	}
}

func (h *JobAPIHandler) createJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JobType     string   `json:"job_type"`
		ChainID     string   `json:"chain_id"`
		Addresses   []string `json:"addresses"`
		Datasets    []string `json:"datasets"`
		RangeMode   string   `json:"range_mode"`
		Priority    int      `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "请求格式错误"})
		return
	}

	job := &Job{
		ID:        "job-" + time.Now().Format("20060102-150405"),
		Type:      JobType(req.JobType),
		ChainID:   req.ChainID,
		Status:    StatusCreated,
		Stage:     StageIdle,
		Priority:  Priority(req.Priority),
		RangeMode: RangeMode(req.RangeMode),
		CreatedAt: time.Now().UTC(),
	}
	_ = job.Transition(StatusValidating)
	h.jobs[job.ID] = job

	writeJSON(w, http.StatusAccepted, job)
}

func (h *JobAPIHandler) getJob(w http.ResponseWriter, _ *http.Request, jobID string) {
	job, ok := h.jobs[jobID]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "job not found"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *JobAPIHandler) pauseJob(w http.ResponseWriter, _ *http.Request, jobID string) {
	job, ok := h.jobs[jobID]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "job not found"})
		return
	}
	if err := job.Transition(StatusPausing); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"detail": err.Error()})
		return
	}
	job.Transition(StatusPaused)
	writeJSON(w, http.StatusOK, job)
}

func (h *JobAPIHandler) resumeJob(w http.ResponseWriter, _ *http.Request, jobID string) {
	job, ok := h.jobs[jobID]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "job not found"})
		return
	}
	if err := job.Transition(StatusRunning); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *JobAPIHandler) cancelJob(w http.ResponseWriter, _ *http.Request, jobID string) {
	job, ok := h.jobs[jobID]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "job not found"})
		return
	}
	if err := job.Transition(StatusCanceling); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"detail": err.Error()})
		return
	}
	job.Transition(StatusCancelled)
	writeJSON(w, http.StatusOK, job)
}

func (h *JobAPIHandler) retryFailed(w http.ResponseWriter, _ *http.Request, jobID string) {
	job, ok := h.jobs[jobID]
	if !ok || job.Status != StatusFailed {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "only failed jobs can be retried"})
		return
	}
	job.Transition(StatusQueued)
	writeJSON(w, http.StatusOK, job)
}
