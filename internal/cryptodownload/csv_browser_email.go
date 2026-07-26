package cryptodownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultCSVBrowserEmailScript = "tools/oklink_browser_email.mjs"
	csvBrowserEmailTimeout       = 90 * time.Second
)

//go:embed tools/oklink_browser_email.mjs
var embeddedCSVBrowserEmailScript []byte

type csvBrowserEmailRequest struct {
	URL     string `json:"url"`
	PageURL string `json:"pageUrl"`
	Body    string `json:"body"`
}

type csvBrowserEmailResponse struct {
	Code int    `json:"code"`
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

func newCSVBrowserEmailRequester(baseURL string) csvBrowserEmailRequester {
	scriptPath := strings.TrimSpace(os.Getenv("OKLINK_CSV_BROWSER_EMAIL"))
	if scriptPath == "" {
		if !csvSignerEnabledForBaseURL(baseURL) {
			return nil
		}
		var err error
		scriptPath, err = materializeCSVBrowserEmailScript()
		if err != nil {
			return &nodeCSVBrowserEmailRequester{setupErr: err}
		}
	}
	if !filepath.IsAbs(scriptPath) {
		absolute, err := filepath.Abs(scriptPath)
		if err != nil {
			return nil
		}
		scriptPath = absolute
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return &nodeCSVBrowserEmailRequester{setupErr: err}
	}
	nodePath := strings.TrimSpace(os.Getenv("OKLINK_CSV_SIGNER_NODE"))
	if nodePath == "" {
		nodePath = "node"
	}
	return &nodeCSVBrowserEmailRequester{nodePath: nodePath, scriptPath: scriptPath}
}

func materializeCSVBrowserEmailScript() (string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("定位用户缓存目录: %w", err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(embeddedCSVBrowserEmailScript))[:16]
	directory := filepath.Join(cacheRoot, "wallet-exporter", "browser")
	path := filepath.Join(directory, "oklink_browser_email_"+hash+".mjs")
	if info, statErr := os.Stat(path); statErr == nil && info.Size() == int64(len(embeddedCSVBrowserEmailScript)) {
		return path, nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("创建浏览器脚本缓存目录: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "oklink_browser_email_*.tmp")
	if err != nil {
		return "", fmt.Errorf("创建浏览器脚本缓存文件: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(embeddedCSVBrowserEmailScript); err != nil {
		temporary.Close()
		return "", fmt.Errorf("写入浏览器脚本缓存: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("关闭浏览器脚本缓存: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if info, statErr := os.Stat(path); statErr == nil && info.Size() == int64(len(embeddedCSVBrowserEmailScript)) {
			return path, nil
		}
		return "", fmt.Errorf("发布浏览器脚本缓存: %w", err)
	}
	return path, nil
}

func (r *nodeCSVBrowserEmailRequester) Request(ctx context.Context, request csvBrowserEmailRequest) error {
	if r.setupErr != nil {
		return fmt.Errorf("准备浏览器 CSV 邮箱请求失败: %w", r.setupErr)
	}
	requestCtx, cancel := context.WithTimeout(ctx, csvBrowserEmailTimeout)
	defer cancel()
	input, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("构造浏览器 CSV 邮箱请求失败: %w", err)
	}
	command := exec.CommandContext(requestCtx, r.nodePath, r.scriptPath)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("浏览器 CSV 邮箱请求失败: %w; output=%s", err, truncate(output, 500))
	}
	var response csvBrowserEmailResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("解析浏览器 CSV 邮箱响应失败: %w; output=%s", err, truncate(output, 500))
	}
	if response.Code != 0 {
		return fmt.Errorf("浏览器 CSV 邮箱 code=%d msg=%s", response.Code, response.Msg)
	}
	return nil
}
