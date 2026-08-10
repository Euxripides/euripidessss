package api

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/writer"
	"github.com/gin-gonic/gin"
)

var (
	systemSettingsStartedAt = time.Now().UTC()
	settingsBackupIDRE      = regexp.MustCompile(`^settings-\d{8}T\d{6}-[a-f0-9]{8}$`)
	systemSettingsMu        sync.Mutex
	systemAuditMu           sync.Mutex
)

type systemAuditEntry struct {
	ID          string    `json:"id"`
	Action      string    `json:"action"`
	Status      string    `json:"status"`
	Actor       string    `json:"actor"`
	Summary     string    `json:"summary"`
	ChangedKeys []string  `json:"changed_keys,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type settingsBackup struct {
	ID          string                     `json:"id"`
	Description string                     `json:"description,omitempty"`
	SizeBytes   int64                      `json:"size_bytes"`
	CreatedAt   time.Time                  `json:"created_at"`
	Settings    *config.PersistentSettings `json:"settings,omitempty"`
}

type cleanupRequest struct {
	Categories    []string `json:"categories"`
	OlderThanDays int      `json:"older_than_days"`
	PreviewID     string   `json:"preview_id,omitempty"`
	Confirmation  string   `json:"confirmation,omitempty"`
	ConfirmPhrase string   `json:"confirm_phrase,omitempty"`
}

type cleanupCandidate struct {
	Path     string
	Category string
	Size     int64
	ModTime  time.Time
}

func registerSystemSettingsRoutes(api *gin.RouterGroup) {
	api.GET("/system/settings", handleSystemSettingsGet)
	api.PATCH("/system/settings", handleSystemSettingsPatch)
	api.GET("/system/settings/audit", handleSystemSettingsAudit)
	api.GET("/system/settings/backups", handleSystemSettingsBackupsList)
	api.POST("/system/settings/backups", handleSystemSettingsBackupCreate)
	api.POST("/system/settings/backups/:id/restore", handleSystemSettingsBackupRestore)
	api.POST("/system/settings/cleanup/preview", handleSystemSettingsCleanupPreview)
	api.POST("/system/settings/cleanup/execute", handleSystemSettingsCleanupExecute)
}

func handleSystemSettingsGet(c *gin.Context) { c.JSON(http.StatusOK, buildSystemSettingsSnapshot(c)) }

func handleSystemSettingsPatch(c *gin.Context) {
	if !requireLocalSystemAction(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		Settings config.PersistentSettingsPatch `json:"settings"`
	}
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	systemSettingsMu.Lock()
	defer systemSettingsMu.Unlock()
	current := requestedSystemSettings()
	next, changed, err := current.ApplyPatch(body.Settings)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	if err := config.SavePersistentSettings(cfg.ConfigDir, next); err != nil {
		appendSystemAudit(c, "SETTINGS_PATCH", "FAILED", nil, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	appendSystemAudit(c, "SETTINGS_PATCH", "OK", changed, fmt.Sprintf("已保存 %d 项设置", len(changed)))
	c.JSON(http.StatusOK, buildSystemSettingsSnapshot(c))
}

func handleSystemSettingsAudit(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"audit": readSystemAudit(100)})
}

func handleSystemSettingsBackupsList(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"backups": listSettingsBackups()})
}

func handleSystemSettingsBackupCreate(c *gin.Context) {
	if !requireLocalSystemAction(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<16)
	var body struct {
		Description string `json:"description"`
	}
	_ = json.NewDecoder(c.Request.Body).Decode(&body)
	body.Description = strings.TrimSpace(body.Description)
	if len([]rune(body.Description)) > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "description 最多 120 字"})
		return
	}
	systemSettingsMu.Lock()
	backup, err := createSettingsBackup(body.Description)
	systemSettingsMu.Unlock()
	if err != nil {
		appendSystemAudit(c, "BACKUP_CREATE", "FAILED", nil, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	appendSystemAudit(c, "BACKUP_CREATE", "OK", nil, "已创建设置快照 "+backup.ID)
	c.JSON(http.StatusCreated, gin.H{"backup": backup})
}

func handleSystemSettingsBackupRestore(c *gin.Context) {
	if !requireLocalSystemAction(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if !settingsBackupIDRE.MatchString(id) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "非法备份 ID"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<16)
	var body struct {
		Confirmation  string `json:"confirmation"`
		ConfirmPhrase string `json:"confirm_phrase"`
	}
	if json.NewDecoder(c.Request.Body).Decode(&body) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败"})
		return
	}
	confirm := body.Confirmation
	if confirm == "" {
		confirm = body.ConfirmPhrase
	}
	if confirm != "RESTORE SETTINGS" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "确认短语必须为 RESTORE SETTINGS"})
		return
	}
	systemSettingsMu.Lock()
	defer systemSettingsMu.Unlock()
	backup, err := loadSettingsBackup(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": err.Error()})
		return
	}
	if backup.Settings == nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "设置快照缺少配置内容"})
		return
	}
	if err := config.SavePersistentSettings(cfg.ConfigDir, *backup.Settings); err != nil {
		appendSystemAudit(c, "BACKUP_RESTORE", "FAILED", nil, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	appendSystemAudit(c, "BACKUP_RESTORE", "OK", systemRestartKeys(), "已恢复设置快照 "+id)
	c.JSON(http.StatusOK, buildSystemSettingsSnapshot(c))
}

func handleSystemSettingsCleanupPreview(c *gin.Context) {
	if !requireLocalSystemAction(c) {
		return
	}
	req, ok := decodeCleanupRequest(c)
	if !ok {
		return
	}
	candidates, warnings, err := cleanupCandidates(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"preview": cleanupPreview(req, candidates, warnings)})
}

func handleSystemSettingsCleanupExecute(c *gin.Context) {
	if !requireLocalSystemAction(c) {
		return
	}
	req, ok := decodeCleanupRequest(c)
	if !ok {
		return
	}
	confirm := req.Confirmation
	if confirm == "" {
		confirm = req.ConfirmPhrase
	}
	if confirm != "DELETE EXPIRED FILES" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "确认短语必须为 DELETE EXPIRED FILES"})
		return
	}
	candidates, warnings, err := cleanupCandidates(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	preview := cleanupPreview(req, candidates, warnings)
	if req.PreviewID == "" || req.PreviewID != preview["preview_id"] {
		c.JSON(http.StatusConflict, gin.H{"detail": "清理预览已变化，请重新预览"})
		return
	}
	deleted, reclaimed := 0, int64(0)
	for _, item := range candidates {
		info, err := os.Lstat(item.Path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != item.Size || !info.ModTime().Equal(item.ModTime) {
			continue
		}
		if err := os.Remove(item.Path); err == nil {
			deleted++
			reclaimed += item.Size
		}
	}
	appendSystemAudit(c, "CLEANUP_EXECUTE", "OK", nil, fmt.Sprintf("删除 %d 个过期文件，释放 %d bytes", deleted, reclaimed))
	response := buildSystemSettingsSnapshot(c)
	response["cleanup_result"] = gin.H{"file_count": deleted, "reclaimed_bytes": reclaimed}
	c.JSON(http.StatusOK, response)
}

func decodeCleanupRequest(c *gin.Context) (cleanupRequest, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var req cleanupRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return req, false
	}
	if req.OlderThanDays < 1 || req.OlderThanDays > 3650 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "older_than_days 必须在 1-3650"})
		return req, false
	}
	seen := map[string]bool{}
	var categories []string
	for _, category := range req.Categories {
		category = strings.ToLower(strings.TrimSpace(category))
		if category != "outputs" && category != "logs" {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "categories 仅支持 outputs/logs"})
			return req, false
		}
		if !seen[category] {
			seen[category] = true
			categories = append(categories, category)
		}
	}
	if len(categories) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "至少选择一个清理分类"})
		return req, false
	}
	sort.Strings(categories)
	req.Categories = categories
	return req, true
}

func requireLocalSystemAction(c *gin.Context) bool {
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		host = c.Request.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	local := ip != nil && ip.IsLoopback()
	if !local {
		c.JSON(http.StatusForbidden, gin.H{"detail": "系统设置写操作仅允许本机访问"})
		return false
	}
	if c.GetHeader("X-System-Settings-Action") != "local-console" {
		c.JSON(http.StatusPreconditionRequired, gin.H{"detail": "缺少本机控制台操作标记"})
		return false
	}
	return true
}

func requestedSystemSettings() config.PersistentSettings {
	if saved, ok, err := config.LoadPersistentSettings(cfg.ConfigDir); err == nil && ok {
		return saved
	}
	return cfg.System
}

func effectiveSystemSettings() config.PersistentSettings { return config.SettingsFromConfig(cfg) }

func systemRestartKeys() []string {
	return []string{"concurrency_level", "max_file_size_mb", "analytics_data_source", "clickhouse_enabled", "clickhouse_required", "price_engine_enabled"}
}

func pendingSystemRestart(requested, effective config.PersistentSettings) []string {
	var keys []string
	if requested.ConcurrencyLevel != effective.ConcurrencyLevel {
		keys = append(keys, "concurrency_level")
	}
	if requested.MaxFileSizeMB != effective.MaxFileSizeMB {
		keys = append(keys, "max_file_size_mb")
	}
	if requested.AnalyticsDataSource != effective.AnalyticsDataSource {
		keys = append(keys, "analytics_data_source")
	}
	if requested.ClickHouseEnabled != effective.ClickHouseEnabled {
		keys = append(keys, "clickhouse_enabled")
	}
	if requested.ClickHouseRequired != effective.ClickHouseRequired {
		keys = append(keys, "clickhouse_required")
	}
	if requested.PriceEngineEnabled != effective.PriceEngineEnabled {
		keys = append(keys, "price_engine_enabled")
	}
	return keys
}

func buildSystemSettingsSnapshot(c *gin.Context) gin.H {
	requested, effective := requestedSystemSettings(), effectiveSystemSettings()
	pending := pendingSystemRestart(requested, effective)
	storage := systemStorageSnapshot(requested)
	return gin.H{
		"settings": requested, "effective": effective, "pending_restart": pending, "pending_restart_keys": pending,
		"runtime": systemRuntimeSnapshot(), "components": systemComponentSnapshots(), "storage": storage,
		"backups": listSettingsBackups(), "audit": readSystemAudit(50),
		"capabilities": gin.H{"local_mutation": isLoopbackRequest(c.Request), "settings_backup": true, "cleanup_preview": true, "credentials_exposed": false},
		"updated_at":   time.Now().UTC(),
	}
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func systemRuntimeSnapshot() gin.H {
	version := "development"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	return gin.H{"version": version, "go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "uptime_seconds": int64(time.Since(systemSettingsStartedAt).Seconds()), "server_port": cfg.ServerPort, "debug": cfg.Debug, "current_time": time.Now().UTC()}
}

func systemComponentSnapshots() []gin.H {
	components := []gin.H{{"key": "api", "name": "API 服务", "status": "healthy", "detail": "服务响应正常"}}
	clickStatus := "disabled"
	if cfg.ClickHouse.Enabled {
		clickStatus = "configured"
	}
	components = append(components, gin.H{"key": "clickhouse", "name": "ClickHouse", "status": clickStatus, "detail": map[bool]string{true: "已启用", false: "未启用"}[cfg.ClickHouse.Enabled]})
	rpcStatus, rpcDetail := "unavailable", "RPC Manager 未装配"
	if rpcManager != nil {
		if endpoints, err := rpcManager.Endpoints(); err == nil {
			enabled := 0
			for _, e := range endpoints {
				if e.Enabled {
					enabled++
				}
			}
			rpcStatus = "healthy"
			rpcDetail = fmt.Sprintf("%d 个节点，%d 个启用", len(endpoints), enabled)
		}
	}
	components = append(components, gin.H{"key": "rpc", "name": "RPC Pool", "status": rpcStatus, "detail": rpcDetail})
	components = append(components, gin.H{"key": "smart_download", "name": "智能下载", "status": map[bool]string{true: "healthy", false: "unavailable"}[smartDownloadService != nil], "detail": "任务编排与断点恢复"})
	cloudStatus, cloudDetail := "unavailable", "Cloud Runtime 未装配"
	if smartCloudRuntime != nil {
		st := smartCloudRuntime.Status()
		cloudStatus = string(st.State)
		cloudDetail = st.Reason
	}
	components = append(components, gin.H{"key": "cloud", "name": "SQD Cloud", "status": cloudStatus, "detail": cloudDetail})
	return components
}

func systemStorageSnapshot(settings config.PersistentSettings) gin.H {
	used, files, latest := int64(0), int64(0), time.Time{}
	for _, dir := range []string{cfg.UploadDir, cfg.OutputDir, cfg.LogDir, cfg.RuleSamplesDir} {
		size, count, mod := fixedDirectoryStats(dir)
		used += size
		files += count
		if mod.After(latest) {
			latest = mod
		}
	}
	free, _ := smartDownloadDiskFreeBytes(cfg.BackendDir)
	reclaim := int64(0)
	for category, days := range map[string]int{"logs": settings.LogRetentionDays, "outputs": settings.OutputRetentionDays} {
		candidates, _, _ := cleanupCandidates(cleanupRequest{Categories: []string{category}, OlderThanDays: days})
		for _, item := range candidates {
			reclaim += item.Size
		}
	}
	return gin.H{"path_hint": "backend/data", "used_bytes": used, "free_bytes": free, "file_count": files, "reclaimable_bytes": reclaim, "last_modified": latest}
}

func fixedDirectoryStats(root string) (int64, int64, time.Time) {
	var size, count int64
	var latest time.Time
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode().IsRegular() {
			size += info.Size()
			count++
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
		}
		return nil
	})
	return size, count, latest
}

func settingsBackupDir() string { return filepath.Join(cfg.BackendDir, "data", "system_backups") }

func createSettingsBackup(description string) (settingsBackup, error) {
	if err := os.MkdirAll(settingsBackupDir(), 0o755); err != nil {
		return settingsBackup{}, err
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return settingsBackup{}, err
	}
	id := "settings-" + time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(random[:])
	settings := requestedSystemSettings()
	backup := settingsBackup{ID: id, Description: description, CreatedAt: time.Now().UTC(), Settings: &settings}
	path := filepath.Join(settingsBackupDir(), id+".json")
	if err := writer.WriteJSONAtomic(path, backup); err != nil {
		return settingsBackup{}, err
	}
	if st, e := os.Stat(path); e == nil {
		backup.SizeBytes = st.Size()
	}
	pruneSettingsBackups(requestedSystemSettings().BackupRetentionCount)
	return backup, nil
}

func listSettingsBackups() []settingsBackup {
	entries, err := os.ReadDir(settingsBackupDir())
	if err != nil {
		return []settingsBackup{}
	}
	var out []settingsBackup
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !settingsBackupIDRE.MatchString(id) {
			continue
		}
		backup, e := loadSettingsBackup(id)
		if e != nil {
			continue
		}
		if st, e := entry.Info(); e == nil {
			backup.SizeBytes = st.Size()
		}
		backup.Settings = nil
		out = append(out, backup)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func loadSettingsBackup(id string) (settingsBackup, error) {
	if !settingsBackupIDRE.MatchString(id) {
		return settingsBackup{}, fmt.Errorf("非法备份 ID")
	}
	b, err := os.ReadFile(filepath.Join(settingsBackupDir(), id+".json"))
	if err != nil {
		return settingsBackup{}, fmt.Errorf("设置快照不存在")
	}
	var backup settingsBackup
	if err = json.Unmarshal(b, &backup); err != nil {
		return settingsBackup{}, err
	}
	if backup.Settings == nil {
		return settingsBackup{}, fmt.Errorf("设置快照缺少配置内容")
	}
	if err = backup.Settings.Validate(); err != nil {
		return settingsBackup{}, err
	}
	return backup, nil
}
func pruneSettingsBackups(limit int) {
	items := listSettingsBackups()
	for i := limit; i < len(items); i++ {
		_ = os.Remove(filepath.Join(settingsBackupDir(), items[i].ID+".json"))
	}
}

func cleanupCandidates(req cleanupRequest) ([]cleanupCandidate, []string, error) {
	cutoff := time.Now().Add(-time.Duration(req.OlderThanDays) * 24 * time.Hour)
	roots := map[string]string{"outputs": cfg.OutputDir, "logs": cfg.LogDir}
	var out []cleanupCandidate
	for _, category := range req.Categories {
		root, ok := roots[category]
		if !ok {
			return nil, nil, fmt.Errorf("非法清理分类")
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if path == root {
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
				return nil
			}
			base := strings.ToLower(filepath.Base(path))
			if category == "logs" && (base == "app.log" || base == "system_settings_audit.ndjson") {
				return nil
			}
			out = append(out, cleanupCandidate{Path: path, Category: category, Size: info.Size(), ModTime: info.ModTime()})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, []string{"只处理固定数据目录中的过期普通文件；目录、符号链接和当前日志受保护"}, nil
}

func cleanupPreview(req cleanupRequest, candidates []cleanupCandidate, warnings []string) gin.H {
	h := sha256.New()
	fmt.Fprintf(h, "%v|%d|", req.Categories, req.OlderThanDays)
	reclaim := int64(0)
	for _, item := range candidates {
		fmt.Fprintf(h, "%s|%d|%d;", item.Path, item.Size, item.ModTime.UnixNano())
		reclaim += item.Size
	}
	return gin.H{"preview_id": hex.EncodeToString(h.Sum(nil))[:24], "file_count": len(candidates), "reclaimable_bytes": reclaim, "categories": req.Categories, "warnings": warnings}
}

func systemAuditPath() string { return filepath.Join(cfg.LogDir, "system_settings_audit.ndjson") }
func appendSystemAudit(c *gin.Context, action, status string, changed []string, summary string) {
	systemAuditMu.Lock()
	defer systemAuditMu.Unlock()
	_ = os.MkdirAll(cfg.LogDir, 0o755)
	var random [4]byte
	_, _ = rand.Read(random[:])
	entry := systemAuditEntry{ID: hex.EncodeToString(random[:]), Action: action, Status: status, Actor: "local-console", Summary: summary, ChangedKeys: changed, CreatedAt: time.Now().UTC()}
	b, _ := json.Marshal(entry)
	f, err := os.OpenFile(systemAuditPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.Write(append(b, '\n'))
		_ = f.Sync()
		_ = f.Close()
	}
}
func readSystemAudit(limit int) []systemAuditEntry {
	systemAuditMu.Lock()
	defer systemAuditMu.Unlock()
	f, err := os.Open(systemAuditPath())
	if err != nil {
		return []systemAuditEntry{}
	}
	defer f.Close()
	var out []systemAuditEntry
	s := bufio.NewScanner(f)
	for s.Scan() {
		var entry systemAuditEntry
		if json.Unmarshal(s.Bytes(), &entry) == nil {
			out = append(out, entry)
			if len(out) > limit {
				out = out[1:]
			}
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
