package cryptodownload

import (
	"net/http"
	"sort"
)

func (m *GUIManager) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m.mu.Lock()
	jobs := make([]*GUIJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	m.mu.Unlock()

	snapshots := make([]*GUIJob, 0, len(jobs))
	for _, job := range jobs {
		snapshots = append(snapshots, job.snapshot())
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].ID < snapshots[j].ID
	})
	writeJSON(w, snapshots)
}
