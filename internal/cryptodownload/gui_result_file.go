package cryptodownload

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (m *GUIManager) handleResultFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	if err := validateGUIDownloadHistoryID(id); err != nil || target == "" {
		http.Error(w, "invalid result file request", http.StatusBadRequest)
		return
	}
	allowed, err := m.authorizedResultFiles(id)
	if err != nil {
		if errors.Is(err, errGUIDownloadHistoryNotFound) {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		http.Error(w, "invalid result file path", http.StatusBadRequest)
		return
	}
	authorized := false
	for _, candidate := range allowed {
		candidateAbs, candidateErr := filepath.Abs(candidate)
		if candidateErr == nil && strings.EqualFold(filepath.Clean(candidateAbs), filepath.Clean(targetAbs)) {
			authorized = true
			break
		}
	}
	if !authorized {
		http.Error(w, "result file is not part of this job", http.StatusForbidden)
		return
	}
	file, err := os.Open(targetAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "result file not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "result file is unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": filepath.Base(targetAbs),
	}))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	http.ServeContent(w, r, filepath.Base(targetAbs), info.ModTime(), file)
}

func (m *GUIManager) authorizedResultFiles(id string) ([]string, error) {
	m.mu.Lock()
	job := m.jobs[id]
	m.mu.Unlock()
	if job != nil {
		snapshot := job.snapshot()
		return collectAuthorizedResultFiles(snapshot.TaskDir, snapshot.Results, snapshot.Addresses), nil
	}
	if m.history == nil {
		return nil, errGUIDownloadHistoryNotFound
	}
	record, err := m.history.Find(id)
	if err != nil {
		return nil, err
	}
	return collectAuthorizedResultFiles(record.TaskDir, nil, record.Addresses), nil
}

func collectAuthorizedResultFiles(taskDir string, results []string, addresses []GUIAddressProgress) []string {
	files := make([]string, 0, len(results)+len(addresses)+1)
	for _, result := range results {
		files = append(files, splitGUIResultPaths(result)...)
	}
	for _, address := range addresses {
		files = append(files, splitGUIResultPaths(address.Result)...)
	}
	if strings.TrimSpace(taskDir) != "" {
		files = append(files, filepath.Join(taskDir, "下载情况.xlsx"))
	}
	return files
}

func splitGUIResultPaths(value string) []string {
	parts := strings.Split(value, ";")
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files
}
