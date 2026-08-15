package cryptodownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/cryptodownload/browser_stealth"
)

const (
	defaultCSVBrowserEmailScript = "tools/oklink_browser_email.mjs"
	defaultCSVCrawl4AIScript     = "tools/oklink_crawl4ai_email.py"
	csvBrowserEmailTimeout       = 120 * time.Second
	csvBrowserOutputLimit        = 64 << 10
)

//go:embed tools/oklink_browser_email.mjs
var embeddedCSVBrowserEmailScript []byte

//go:embed tools/oklink_crawl4ai_email.py
var embeddedCSVCrawl4AIScript []byte

type csvBrowserEmailRequest struct {
	URL     string `json:"url"`
	PageURL string `json:"pageUrl"`
	Body    string `json:"body"`
}

type csvBrowserEmailResponse struct {
	Code *int   `json:"code"`
	Msg  string `json:"msg"`
}

type csvBrowserEmailRequester interface {
	Request(context.Context, csvBrowserEmailRequest) error
}

type nodeCSVBrowserEmailRequester struct {
	nodePath   string
	scriptPath string
	setupErr   error
}

type pythonCSVBrowserEmailRequester struct {
	pythonPath string
	scriptPath string
	setupErr   error
}

type csvBrowserEngineNamer interface {
	BrowserEngine() string
}

var (
	csvBrowserCommandContext = exec.CommandContext
	crawl4AIProbeCache       sync.Map
	csvBrowserEmailPattern   = regexp.MustCompile(`(?i)[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}`)
	csvBrowserAddressPattern = regexp.MustCompile(`(?i)0x[0-9a-f]{40}`)
)

func newCSVBrowserEmailRequester(baseURL string) csvBrowserEmailRequester {
	customScript := strings.TrimSpace(os.Getenv("OKLINK_CSV_BROWSER_EMAIL"))
	if customScript != "" {
		return newNodeCSVBrowserEmailRequester(customScript)
	}
	if !csvSignerEnabledForBaseURL(baseURL) {
		return nil
	}

	engine := strings.ToLower(strings.TrimSpace(os.Getenv("OKLINK_CSV_BROWSER_ENGINE")))
	if engine == "" {
		engine = "auto"
	}
	switch engine {
	case "off", "disabled", "none":
		return nil
	case "chrome", "node", "cdp":
		return newNodeCSVBrowserEmailRequester("")
	case "crawl4ai", "patchright", "python":
		return newPythonCSVBrowserEmailRequester(true)
	case "auto":
		if requester := newPythonCSVBrowserEmailRequester(false); requester.setupErr == nil {
			return requester
		}
		return newNodeCSVBrowserEmailRequester("")
	default:
		return &pythonCSVBrowserEmailRequester{
			setupErr: fmt.Errorf("不支持的 OKLINK_CSV_BROWSER_ENGINE=%q", engine),
		}
	}
}

func newNodeCSVBrowserEmailRequester(scriptPath string) *nodeCSVBrowserEmailRequester {
	if strings.TrimSpace(scriptPath) == "" {
		var err error
		scriptPath, err = materializeCSVBrowserEmailScript()
		if err != nil {
			return &nodeCSVBrowserEmailRequester{setupErr: err}
		}
	}
	absolute, err := absoluteExistingFile(scriptPath)
	if err != nil {
		return &nodeCSVBrowserEmailRequester{setupErr: err}
	}
	nodePath := strings.TrimSpace(os.Getenv("OKLINK_CSV_SIGNER_NODE"))
	if nodePath == "" {
		nodePath = "node"
	}
	nodePath, err = resolveExecutable(nodePath)
	if err != nil {
		return &nodeCSVBrowserEmailRequester{setupErr: err}
	}
	return &nodeCSVBrowserEmailRequester{nodePath: nodePath, scriptPath: absolute}
}

func newPythonCSVBrowserEmailRequester(required bool) *pythonCSVBrowserEmailRequester {
	pythonPath, err := resolveCrawl4AIPython()
	if err == nil {
		err = probeCrawl4AIRuntime(pythonPath)
	}
	if err != nil {
		if required {
			err = fmt.Errorf("Crawl4AI/Patchright 运行时不可用: %w", err)
		}
		return &pythonCSVBrowserEmailRequester{setupErr: err}
	}
	scriptPath, err := materializeCSVCrawl4AIScript()
	if err != nil {
		return &pythonCSVBrowserEmailRequester{setupErr: err}
	}
	return &pythonCSVBrowserEmailRequester{pythonPath: pythonPath, scriptPath: scriptPath}
}

func resolveCrawl4AIPython() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("OKLINK_CRAWL4AI_PYTHON")); configured != "" {
		return resolveExecutable(configured)
	}
	candidates := []string{"python", "python3"}
	if runtime.GOOS == "windows" {
		candidates = append([]string{`D:\app\cx\python\python.exe`}, candidates...)
	}
	var lastErr error
	for _, candidate := range candidates {
		path, err := resolveExecutable(candidate)
		if err == nil {
			return path, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("未找到可用 Python: %w", lastErr)
}

func resolveExecutable(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("可执行文件路径为空")
	}
	if filepath.IsAbs(path) {
		return absoluteExistingFile(path)
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func absoluteExistingFile(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("路径是目录而不是文件: %s", absolute)
	}
	return absolute, nil
}

func probeCrawl4AIRuntime(pythonPath string) error {
	cacheKey := pythonPath + "|" + os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	if cached, ok := crawl4AIProbeCache.Load(cacheKey); ok {
		if cached == "" {
			return nil
		}
		return fmt.Errorf("%s", cached)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	command := csvBrowserCommandContext(ctx, pythonPath, "-c", "import crawl4ai, patchright, playwright")
	command.Env = csvBrowserSubprocessEnv()
	stdout, stderr, err := runCSVBrowserCommand(command, nil)
	if err != nil {
		detail := sanitiseCSVBrowserDiagnostic(string(stderr))
		if detail == "" {
			detail = sanitiseCSVBrowserDiagnostic(string(stdout))
		}
		if detail == "" {
			detail = err.Error()
		}
		crawl4AIProbeCache.Store(cacheKey, detail)
		return fmt.Errorf("%s", detail)
	}
	crawl4AIProbeCache.Store(cacheKey, "")
	return nil
}

func materializeCSVBrowserEmailScript() (string, error) {
	return materializeCSVBrowserAsset("oklink_browser_email", ".mjs", embeddedCSVBrowserEmailScript)
}

func materializeCSVCrawl4AIScript() (string, error) {
	return materializeCSVBrowserAsset("oklink_crawl4ai_email", ".py", embeddedCSVCrawl4AIScript)
}

func materializeCSVBrowserAsset(name, extension string, content []byte) (string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("定位用户缓存目录: %w", err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(content))[:16]
	directory := filepath.Join(cacheRoot, "wallet-exporter", "browser")
	path := filepath.Join(directory, name+"_"+hash+extension)
	if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, content) {
		return path, nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("创建浏览器脚本缓存目录: %w", err)
	}
	temporary, err := os.CreateTemp(directory, name+"_*.tmp")
	if err != nil {
		return "", fmt.Errorf("创建浏览器脚本缓存文件: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return "", fmt.Errorf("写入浏览器脚本缓存: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("关闭浏览器脚本缓存: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, content) {
			return path, nil
		}
		return "", fmt.Errorf("发布浏览器脚本缓存: %w", err)
	}
	return path, nil
}

func (r *nodeCSVBrowserEmailRequester) BrowserEngine() string {
	return "Chrome/CDP"
}

func (r *pythonCSVBrowserEmailRequester) BrowserEngine() string {
	return "Crawl4AI/Patchright"
}

func csvBrowserEmailEngine(requester csvBrowserEmailRequester) string {
	if named, ok := requester.(csvBrowserEngineNamer); ok {
		return named.BrowserEngine()
	}
	return "browser"
}

func (r *nodeCSVBrowserEmailRequester) Request(ctx context.Context, request csvBrowserEmailRequest) error {
	if r.setupErr != nil {
		return fmt.Errorf("准备浏览器 CSV 邮箱请求失败: %w", r.setupErr)
	}
	return requestCSVBrowserProcess(ctx, r.nodePath, r.scriptPath, request, "浏览器 CSV 邮箱")
}

func (r *pythonCSVBrowserEmailRequester) Request(ctx context.Context, request csvBrowserEmailRequest) error {
	if r.setupErr != nil {
		return fmt.Errorf("准备 Crawl4AI CSV 邮箱请求失败: %w", r.setupErr)
	}
	return requestCSVBrowserProcess(ctx, r.pythonPath, r.scriptPath, request, "Crawl4AI CSV 邮箱")
}

func requestCSVBrowserProcess(ctx context.Context, executable, script string, request csvBrowserEmailRequest, label string) error {
	requestCtx, cancel := context.WithTimeout(ctx, csvBrowserEmailTimeout)
	defer cancel()
	input, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("构造%s请求失败: %w", label, err)
	}
	command := csvBrowserCommandContext(requestCtx, executable, script)
	command.Env = csvBrowserSubprocessEnv()
	command.Env = append(command.Env, "OKLINK_STEALTH_SCRIPT="+base64.StdEncoding.EncodeToString([]byte(browserstealth.AllStealthScripts())))
	stdout, stderr, err := runCSVBrowserCommand(command, input)
	if err != nil {
		detail := sanitiseCSVBrowserDiagnostic(string(stderr))
		if detail == "" {
			detail = sanitiseCSVBrowserDiagnostic(string(stdout))
		}
		return fmt.Errorf("%s请求失败: %w; detail=%s", label, err, detail)
	}
	var response csvBrowserEmailResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &response); err != nil {
		return fmt.Errorf("解析%s响应失败: %w; output=%s", label, err, sanitiseCSVBrowserDiagnostic(string(stdout)))
	}
	if response.Code == nil {
		return fmt.Errorf("解析%s响应失败: 缺少 code 字段", label)
	}
	if *response.Code != 0 {
		return fmt.Errorf("%s code=%d msg=%s", label, *response.Code, sanitiseCSVBrowserDiagnostic(response.Msg))
	}
	return nil
}

type cappedCSVBrowserBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *cappedCSVBrowserBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = b.buffer.Write(data)
	}
	return originalLength, nil
}

func runCSVBrowserCommand(command *exec.Cmd, input []byte) ([]byte, []byte, error) {
	stdout := &cappedCSVBrowserBuffer{limit: csvBrowserOutputLimit}
	stderr := &cappedCSVBrowserBuffer{limit: csvBrowserOutputLimit}
	command.Stdin = bytes.NewReader(input)
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 3 * time.Second
	prepareCSVBrowserProcess(command)
	if err := command.Start(); err != nil {
		return stdout.buffer.Bytes(), stderr.buffer.Bytes(), err
	}
	cleanup, err := attachCSVBrowserProcessTree(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return stdout.buffer.Bytes(), stderr.buffer.Bytes(), fmt.Errorf("保护浏览器进程树: %w", err)
	}
	defer cleanup()
	err = command.Wait()
	return stdout.buffer.Bytes(), stderr.buffer.Bytes(), err
}

func csvBrowserSubprocessEnv() []string {
	allowed := []string{
		"SystemRoot", "WINDIR", "TEMP", "TMP", "LOCALAPPDATA", "APPDATA",
		"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "PATH", "PATHEXT", "COMSPEC",
		"PLAYWRIGHT_BROWSERS_PATH", "OKLINK_CRAWL4AI_HEADLESS", "OKLINK_CRAWL4AI_PROFILE_DIR",
		"OKLINK_CHROME_PATH", "OKLINK_CSV_BROWSER_PROFILE_DIR",
		"OKLINK_PROXY", "HTTPS_PROXY", "HTTP_PROXY",
	}
	environment := make([]string, 0, len(allowed)+2)
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			environment = append(environment, key+"="+value)
		}
	}
	environment = append(environment, "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	return environment
}

func sanitiseCSVBrowserDiagnostic(value string) string {
	value = csvBrowserEmailPattern.ReplaceAllString(value, "<email>")
	value = csvBrowserAddressPattern.ReplaceAllString(value, "<evm-address>")
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 1000 {
		value = value[:1000]
	}
	return value
}
