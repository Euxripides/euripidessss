package cryptodownload

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (m *GUIManager) handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	m.mu.Lock()
	job := m.jobs[id]
	m.mu.Unlock()
	if job == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	job.mu.Lock()
	request := job.request
	job.mu.Unlock()
	request, err := m.hydrateCSVStartRequest(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	job.mu.Lock()
	job.request = request
	job.NeedsCredentials = false
	job.mu.Unlock()
	req, entries, startIndex, ctx, err := job.prepareResume()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	go runGUIJob(ctx, job, req, entries, startIndex)
	writeJSON(w, job.snapshot())
}

func (j *GUIJob) prepareResume() (GUIStartRequest, []GUIAddressChain, int, context.Context, error) {
	ctx, cancel := context.WithCancel(context.Background())
	j.mu.Lock()
	if (j.Status != "paused" && j.Status != "cooling") || j.Running {
		j.mu.Unlock()
		cancel()
		return GUIStartRequest{}, nil, 0, nil, errors.New("当前任务不是暂停状态")
	}
	if j.NeedsCredentials {
		j.mu.Unlock()
		cancel()
		return GUIStartRequest{}, nil, 0, nil, errors.New("继续下载需要凭据：请先在设置中补充邮箱密码")
	}
	cooldown := time.Time{}
	if j.scheduler != nil {
		cooldown = j.scheduler.Snapshot().CooldownUntil
	}
	if persisted, ok := guiCooldownUntil(j.CooldownUntil); ok && persisted.After(cooldown) {
		cooldown = persisted
	}
	if cooldown.After(time.Now()) {
		j.mu.Unlock()
		cancel()
		return GUIStartRequest{}, nil, 0, nil, fmt.Errorf("429 冷却中，%s 后可继续下载", cooldown.Format("15:04:05"))
	}
	entries := append([]GUIAddressChain(nil), j.entries...)
	if len(entries) == 0 {
		j.mu.Unlock()
		cancel()
		return GUIStartRequest{}, nil, 0, nil, errors.New("暂停任务缺少续传地址")
	}
	startIndex := firstResumableGUIAddressLocked(j.Addresses)
	if startIndex < 0 || startIndex >= len(entries) {
		j.mu.Unlock()
		cancel()
		return GUIStartRequest{}, nil, 0, nil, errors.New("没有可继续下载的地址")
	}
	previousCancel := j.cancel
	previousAddressCancels := j.addressCancels
	previousStatus, previousMessage := j.Status, j.Message
	previousRunning, previousFinishedAt := j.Running, j.FinishedAt
	previousAddress := j.Addresses[startIndex]
	previousErrors := append([]string(nil), j.Errors...)
	previousLogLength := len(j.Logs)
	j.cancel, j.addressCancels = cancel, map[int]context.CancelFunc{}
	j.Running = true
	j.Status = "running"
	j.Message = "继续下载"
	j.FinishedAt = ""
	j.CooldownUntil = ""
	if startIndex >= 0 && startIndex < len(j.Addresses) {
		address := &j.Addresses[startIndex]
		for _, resolved := range address.Errors {
			j.Errors = removeGUIJobError(j.Errors, resolved)
		}
		address.Errors = nil
		address.CancelRequested = false
		address.FinishedAt = ""
		for partIndex := range address.Parts {
			if address.Parts[partIndex].Status == "failed" {
				address.Parts[partIndex].Status = "pending"
			}
		}
	}
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+"  继续下载")
	j.mu.Unlock()
	if err := j.save("resume start"); err != nil {
		j.mu.Lock()
		j.cancel, j.addressCancels = previousCancel, previousAddressCancels
		j.Status, j.Message = previousStatus, previousMessage
		j.Running, j.FinishedAt = previousRunning, previousFinishedAt
		j.Errors = previousErrors
		j.Addresses[startIndex] = previousAddress
		j.Logs = j.Logs[:previousLogLength]
		j.mu.Unlock()
		cancel()
		return GUIStartRequest{}, nil, 0, nil, err
	}
	return j.request, entries, startIndex, ctx, nil
}

func removeGUIJobError(errors []string, resolved string) []string {
	for index, candidate := range errors {
		if candidate == resolved {
			return append(errors[:index:index], errors[index+1:]...)
		}
	}
	return errors
}

func firstResumableGUIAddressLocked(addresses []GUIAddressProgress) int {
	for i, addr := range addresses {
		if !isTerminalGUIAddressStatus(addr.Status) {
			return i
		}
	}
	return -1
}

func (j *GUIJob) pauseAddress(index int, err error) {
	if err == nil {
		return
	}
	defer j.persist()
	now := time.Now().Format("2006-01-02 15:04:05")
	msg := guiPauseMessage(err)
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "paused"
	j.Running = false
	j.Message = msg
	j.FinishedAt = now
	if index >= 0 && index < len(j.Addresses) {
		addr := &j.Addresses[index]
		addr.Status = "paused"
		addr.Message = msg
		addr.CancelRequested = false
		addr.FinishedAt = now
		delete(j.addressCancels, index)
	}
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+"  "+msg)
	j.syncOverallProgressLocked()
}

func guiPauseMessage(err error) string {
	reason := err.Error()
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "imap") || strings.Contains(lower, "getaddrinfo") || strings.Contains(lower, "lookup"):
		return fmt.Sprintf("已暂停：IMAP 连接失败，请检查 IMAP 主机、端口、用户名和网络后点击继续下载。原因：%s", reason)
	case strings.Contains(lower, "50113") || strings.Contains(lower, "signature") || strings.Contains(lower, "request sign"):
		return fmt.Sprintf("已暂停：OKLink 当前会话或请求签名失效，请刷新有效会话后点击继续下载。原因：%s", reason)
	default:
		return fmt.Sprintf("已暂停：请检查下载配置和网络后点击继续下载。原因：%s", reason)
	}
}
