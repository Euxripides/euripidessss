package duckdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Engine struct {
	mu           sync.Mutex
	exePath      string
	dbPath       string
	extensionDir string
	version      string
	errText      string
}

type Status struct {
	Available bool   `json:"available"`
	Mode      string `json:"mode"`
	ExePath   string `json:"exe_path,omitempty"`
	Database  string `json:"database,omitempty"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

type AnalyticsConfig struct {
	DuckDBPath     string
	DuckDBDatabase string
}

type ExcelSheetRead struct {
	Path   string
	Sheet  string
	Header bool
}

func Open(rootDir, dataDir string, cfg AnalyticsConfig) *Engine {
	dbPath := resolveDatabasePath(dataDir, cfg.DuckDBDatabase)
	engine := &Engine{dbPath: dbPath}
	exePath, err := resolveExecutablePath(rootDir, cfg.DuckDBPath)
	if err != nil {
		engine.errText = err.Error()
		return engine
	}
	engine.exePath = exePath
	engine.extensionDir = resolveExtensionDir(exePath)
	version, err := readVersion(exePath)
	if err != nil {
		engine.errText = err.Error()
		return engine
	}
	engine.version = version
	return engine
}

func (e *Engine) Status() Status {
	if e == nil {
		return Status{Available: false, Mode: "duckdb-cli", Error: "analysis engine not initialized"}
	}
	status := Status{
		Available: e.errText == "",
		Mode:      "duckdb-cli",
		ExePath:   e.exePath,
		Database:  e.dbPath,
		Version:   e.version,
	}
	if e.errText != "" {
		status.Error = e.errText
	}
	return status
}

func (e *Engine) Available() bool {
	return e != nil && e.errText == "" && e.exePath != "" && e.dbPath != ""
}

func (e *Engine) ExecSQL(ctx context.Context, sqlText string) ([]byte, error) {
	if !e.Available() {
		return nil, errors.New("duckdb analysis engine is not available")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(e.dbPath), 0755); err != nil {
		return nil, err
	}
	cmd := duckDBCommand(ctx, e.exePath, e.dbPath, "-c", sqlText)
	return cmd.CombinedOutput()
}

func (e *Engine) ExecSQLJSON(ctx context.Context, sqlText string) ([]map[string]interface{}, error) {
	if !e.Available() {
		return nil, errors.New("duckdb analysis engine is not available")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(e.dbPath), 0755); err != nil {
		return nil, err
	}
	cmd := duckDBCommand(ctx, e.exePath, "-json", e.dbPath, "-c", sqlText)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("duckdb query failed: %s: %w", string(output), err)
	}
	if len(output) == 0 {
		return nil, nil
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, fmt.Errorf("duckdb json parse: %w", err)
	}
	return rows, nil
}

func (e *Engine) CreateTableFromCSV(ctx context.Context, tableName, csvPath string) error {
	return e.CreateTableFromCSVFiles(ctx, tableName, []string{csvPath})
}

func (e *Engine) CreateTableFromCSVFiles(ctx context.Context, tableName string, csvPaths []string) error {
	if !e.Available() {
		return errors.New("duckdb analysis engine is not available")
	}
	if len(csvPaths) == 0 {
		return errors.New("duckdb csv path list is empty")
	}
	if err := os.MkdirAll(filepath.Dir(e.dbPath), 0755); err != nil {
		return err
	}
	escapedTable := quoteIdentifier(tableName)
	sql := fmt.Sprintf(
		`DROP TABLE IF EXISTS %s; CREATE TABLE %s AS SELECT * FROM read_csv(%s, header=true, all_varchar=true, ignore_errors=true, strict_mode=false, null_padding=true, quote='"', escape='"')`,
		escapedTable, escapedTable, csvPathSQL(csvPaths),
	)
	output, err := e.ExecSQL(ctx, sql)
	if err != nil {
		return fmt.Errorf("duckdb create table from csv: %s: %w", string(output), err)
	}
	return nil
}

func (e *Engine) CreateTableFromXLSXFiles(ctx context.Context, tableName string, sheets []ExcelSheetRead) error {
	if !e.Available() {
		return errors.New("duckdb analysis engine is not available")
	}
	if len(sheets) == 0 {
		return errors.New("duckdb xlsx sheet list is empty")
	}
	if ifErr := os.MkdirAll(filepath.Dir(e.dbPath), 0755); ifErr != nil {
		return ifErr
	}

	escapedTable := quoteIdentifier(tableName)
	statements := []string{
		excelExtensionSQL(e.extensionDir),
		fmt.Sprintf("DROP TABLE IF EXISTS %s", escapedTable),
		fmt.Sprintf("CREATE TABLE %s AS SELECT * FROM %s", escapedTable, readXLSXSQL(sheets[0])),
	}
	for _, sheet := range sheets[1:] {
		if sheet.Header {
			statements = append(statements, fmt.Sprintf("INSERT INTO %s BY NAME SELECT * FROM %s", escapedTable, readXLSXSQL(sheet)))
		} else {
			statements = append(statements, fmt.Sprintf("INSERT INTO %s SELECT * FROM %s", escapedTable, readXLSXSQL(sheet)))
		}
	}

	output, err := e.ExecSQL(ctx, strings.Join(statements, "; "))
	if err != nil {
		return fmt.Errorf("duckdb create table from xlsx: %s: %w", string(output), err)
	}
	return nil
}

func (e *Engine) DropTable(ctx context.Context, tableName string) error {
	if !e.Available() {
		return errors.New("duckdb analysis engine is not available")
	}
	escapedTable := quoteIdentifier(tableName)
	sql := fmt.Sprintf("DROP TABLE IF EXISTS %s", escapedTable)
	output, err := e.ExecSQL(ctx, sql)
	if err != nil {
		return fmt.Errorf("duckdb drop table: %s: %w", string(output), err)
	}
	return nil
}

func (e *Engine) TableRowCount(ctx context.Context, tableName string) (int, error) {
	if !e.Available() {
		return 0, errors.New("duckdb analysis engine is not available")
	}
	escapedTable := quoteIdentifier(tableName)
	sql := fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s", escapedTable)
	rows, err := e.ExecSQLJSON(ctx, sql)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	cnt, _ := rows[0]["cnt"].(float64)
	return int(cnt), nil
}

func quoteIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteSQLString(s string) string {
	return `'` + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "/"), `'`, `''`) + `'`
}

func readXLSXSQL(sheet ExcelSheetRead) string {
	header := "false"
	if sheet.Header {
		header = "true"
	}
	return fmt.Sprintf(
		"read_xlsx(%s, sheet=%s, header=%s, all_varchar=true, stop_at_empty=false)",
		quoteSQLString(sheet.Path),
		quoteSQLString(sheet.Sheet),
		header,
	)
}

func excelExtensionSQL(extensionDir string) string {
	if strings.TrimSpace(extensionDir) == "" {
		return "LOAD excel"
	}
	return fmt.Sprintf("SET extension_directory=%s; LOAD excel", quoteSQLString(extensionDir))
}

func csvPathSQL(paths []string) string {
	if len(paths) == 1 {
		return quoteSQLString(paths[0])
	}
	items := make([]string, 0, len(paths))
	for _, path := range paths {
		items = append(items, quoteSQLString(path))
	}
	return "[" + strings.Join(items, ", ") + "]"
}

func (e *Engine) DBPath() string {
	return e.dbPath
}

func resolveExecutablePath(rootDir, configured string) (string, error) {
	var candidates []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if filepath.IsAbs(path) {
			candidates = append(candidates, path)
			return
		}
		candidates = append(candidates, filepath.Join(rootDir, path))
	}
	add(configured)
	add("tools/duckdb/duckdb.exe")
	add("duckdb.exe")
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("duckdb.exe not found; place it under tools/duckdb for offline analysis")
}

func resolveExtensionDir(exePath string) string {
	candidate := filepath.Join(filepath.Dir(exePath), "extensions")
	info, err := os.Stat(candidate)
	if err == nil && info.IsDir() {
		return candidate
	}
	return ""
}

func resolveDatabasePath(dataDir, configured string) string {
	if strings.TrimSpace(configured) != "" {
		if filepath.IsAbs(configured) {
			return configured
		}
		return filepath.Join(dataDir, configured)
	}
	return filepath.Join(dataDir, "analysis", "flow.duckdb")
}

func readVersion(exePath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := duckDBCommand(ctx, exePath, "--version").CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func duckDBCommand(ctx context.Context, exePath string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exePath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

// AcquireDataLock 跨包独占数据目录（真实数据验证测试并行互斥，#8 优化）。
// 多个测试进程（go test ./... 并行跑各包）同时打开同一 flow.duckdb 会文件锁冲突；
// 本函数用 O_EXCL 独占锁文件保证同一时刻仅一个测试进程执行真实数据验证。
// 若锁文件残留（进程被 kill）且记录 PID 已不存在，则清除后重试一次。
// 返回 (release, true) 表示获得锁；false 表示其他进程正在使用 → 调用方应 t.Skip。
func AcquireDataLock(dataRoot string) (release func(), ok bool) {
	lockPath := filepath.Join(dataRoot, ".real-data.lock")
	pid := os.Getpid()
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", pid)
			_ = f.Close()
			// release 前校验锁文件 PID 仍属本进程（security review MEDIUM：
			// 防误删其他进程刚获取的锁导致双持锁并发写 flow.duckdb）
			return func() {
				if data, err := os.ReadFile(lockPath); err == nil {
					var cur int
					if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &cur); err == nil && cur == pid {
						_ = os.Remove(lockPath)
					}
				}
			}, true
		}
		if attempt == 0 && reclaimStaleLock(lockPath) {
			continue // 残留锁已清除，重试
		}
		return nil, false
	}
	return nil, false
}

// reclaimStaleLock 读取锁文件记录的 PID，进程不存在则删除残留锁（防测试永久 Skip）。
func reclaimStaleLock(lockPath string) bool {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return false
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid <= 0 {
		return false
	}
	if processAlive(pid) {
		return false // 进程存在 → 锁有效
	}
	_ = os.Remove(lockPath)
	return true
}
