package cryptodownload

import (
	"strings"
	"time"
)

const defaultGUIRiskCooldown = 30 * time.Minute

func guiRiskCooldown(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultGUIRiskCooldown
	}
	return time.Duration(seconds) * time.Second
}

func guiCooldownUntil(value string) (time.Time, bool) {
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil || !until.After(time.Now()) {
		return time.Time{}, false
	}
	return until, true
}

func isGUIRateLimitData(data ExportData) bool {
	for _, message := range data.Errors {
		lower := strings.ToLower(message)
		if strings.Contains(lower, "429") || strings.Contains(lower, "risk control") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests") {
			return true
		}
	}
	return false
}

func (j *GUIJob) markAddressQueued(index int, message string) {
	defer j.persist()
	j.mu.Lock()
	defer j.mu.Unlock()
	if index < 0 || index >= len(j.Addresses) {
		return
	}
	queue := GUIBatchSchedulerSnapshot{}
	if j.scheduler != nil {
		queue = j.scheduler.Snapshot()
	}
	addr := &j.Addresses[index]
	addr.Status = "queued"
	addr.Message = message
	addr.UpdatedAt = nowGUIActivityTime()
	j.QueuePosition = queue.Waiting
	j.Message = message
}

func (j *GUIJob) coolAddress(index int, until time.Time, message string) {
	defer j.persist()
	j.mu.Lock()
	defer j.mu.Unlock()
	if index < 0 || index >= len(j.Addresses) {
		return
	}
	formatted := until.Format(time.RFC3339)
	addr := &j.Addresses[index]
	addr.Status = "cooling"
	addr.Message = message
	addr.UpdatedAt = nowGUIActivityTime()
	addr.FinishedAt = nowGUIActivityTime()
	j.Status = "cooling"
	j.Running = false
	j.CooldownUntil = formatted
	j.Message = message
	j.FinishedAt = nowGUIActivityTime()
	j.syncOverallProgressLocked()
}
