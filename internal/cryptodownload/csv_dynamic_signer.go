package cryptodownload

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultCSVSignerScript = "tools/oklink_csv_signer.mjs"

type csvDownloadSigner struct {
	nodePath    string
	scriptPath  string
	deviceID    string
	deviceIDErr error
	setupErr    error
	process     *csvSignerProcess
}

var csvSignerDeviceIdentity = sync.OnceValues(func() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成 OKLink 设备标识失败: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
})

func newCSVDownloadSigner(baseURL string) *csvDownloadSigner {
	scriptPath := strings.TrimSpace(os.Getenv("OKLINK_CSV_SIGNER"))
	if scriptPath == "" {
		if !csvSignerEnabledForBaseURL(baseURL) {
			return nil
		}
		var err error
		scriptPath, err = materializeEmbeddedCSVSigner()
		if err != nil {
			return &csvDownloadSigner{setupErr: err}
		}
	}
	if !filepath.IsAbs(scriptPath) {
		// 优先解析为 exe 所在目录下的脚本：批次脚本可能从任意工作目录启动 exe，
		// 相对 cwd 的路径会找不到签名器，导致直连请求无签名（OKLink 50113）。
		if exePath, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(exePath), scriptPath)
			if _, statErr := os.Stat(candidate); statErr == nil {
				scriptPath = candidate
			} else if abs, absErr := filepath.Abs(scriptPath); absErr == nil {
				scriptPath = abs
			}
		} else if abs, err := filepath.Abs(scriptPath); err == nil {
			scriptPath = abs
		}
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return &csvDownloadSigner{scriptPath: scriptPath, setupErr: fmt.Errorf("CSV signer 脚本不可用: %w", err)}
	}
	nodePath := strings.TrimSpace(os.Getenv("OKLINK_CSV_SIGNER_NODE"))
	if nodePath == "" {
		nodePath = "node"
	}
	deviceID, deviceIDErr := csvSignerDeviceIdentity()
	return &csvDownloadSigner{
		nodePath: nodePath, scriptPath: scriptPath, deviceID: deviceID, deviceIDErr: deviceIDErr,
		process: newCSVSignerProcess(nodePath, scriptPath),
	}
}

func csvSignerEnabledForBaseURL(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "www.oklink.com" || strings.HasSuffix(host, ".oklink.com")
}

func (c *CSVExportClient) applyCSVDownloadSignature(ctx context.Context, req *http.Request, body []byte, chain, address string) error {
	_, err := c.applyCSVDownloadSignatureWithVersion(ctx, req, body, chain, address)
	return err
}

func (c *CSVExportClient) Close() error {
	if c == nil || c.downloadSigner == nil {
		return nil
	}
	return c.downloadSigner.Close()
}

func (c *CSVExportClient) applyCSVDownloadSignatureWithVersion(ctx context.Context, req *http.Request, body []byte, chain, address string) (csvSignerVersion, error) {
	if c == nil || c.downloadSigner == nil {
		return csvSignerVersion{}, nil
	}
	return c.downloadSigner.ApplyWithVersion(ctx, req, body, chain, address)
}

func (s *csvDownloadSigner) Apply(ctx context.Context, req *http.Request, body []byte, chain, address string) error {
	_, err := s.ApplyWithVersion(ctx, req, body, chain, address)
	return err
}

func (s *csvDownloadSigner) ApplyWithVersion(ctx context.Context, req *http.Request, body []byte, chain, address string) (csvSignerVersion, error) {
	if s == nil {
		return csvSignerVersion{}, nil
	}
	if s.setupErr != nil {
		return csvSignerVersion{}, s.setupErr
	}
	if s.deviceIDErr != nil {
		return csvSignerVersion{}, s.deviceIDErr
	}
	// Budget one sign request generously: a fresh signer process must discover
	// the OKLink asset graph, boot its worker sandbox and probe the encrypt
	// export on the first call (the in-service deadline is 30s).
	signCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	payload := csvSignerRequest{
		Method:   req.Method,
		URL:      req.URL.String(),
		Body:     string(body),
		Chain:    strings.ToLower(strings.TrimSpace(chain)),
		Address:  strings.TrimSpace(address),
		DeviceID: s.deviceID,
	}
	process := s.process
	var response csvSignerResponse
	var err error
	if process == nil {
		process = newCSVSignerProcess(s.nodePath, s.scriptPath)
		response, err = process.oneShot(signCtx, payload, true)
	} else {
		response, err = process.request(signCtx, "sign", &payload)
	}
	if err != nil {
		return process.Version(), fmt.Errorf("生成 OKLink CSV 动态签名失败: %w", err)
	}
	if len(response.Headers) == 0 {
		return process.Version(), &csvSignerFailure{Kind: ErrCSVSignerProtocol, Version: process.Version(), Detail: "empty signer headers"}
	}
	for name, value := range response.Headers {
		req.Header.Set(name, value)
	}
	return versionFromResponse(response), nil
}

func (s *csvDownloadSigner) Version() csvSignerVersion {
	if s == nil || s.process == nil {
		return csvSignerVersion{}
	}
	return s.process.Version()
}

func (s *csvDownloadSigner) Reload(ctx context.Context) (csvSignerVersion, error) {
	if s == nil || s.process == nil {
		return csvSignerVersion{}, nil
	}
	return s.process.Reload(ctx)
}

func (s *csvDownloadSigner) Close() error {
	if s == nil || s.process == nil {
		return nil
	}
	return s.process.Close()
}

// ValidateCSVAutomationRuntime checks the local executable/runtime boundary
// without issuing a network request or exposing generated signature headers.
func ValidateCSVAutomationRuntime(baseURL string) error {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	signer := newCSVDownloadSigner(baseURL)
	if signer == nil {
		return fmt.Errorf("CSV signer 不支持 Base URL")
	}
	if signer.setupErr != nil {
		return signer.setupErr
	}
	if _, err := exec.LookPath(signer.nodePath); err != nil {
		return fmt.Errorf("CSV signer 缺少 Node.js 运行时: %w", err)
	}
	return signer.Close()
}

func (s *csvDownloadSigner) MarkStale() {
	if s != nil && s.process != nil {
		s.process.markStale()
	}
}

func csvSignerVersionedPermanentFailure(err error, version csvSignerVersion) error {
	if err == nil || !isCSVDirectPermanentFailure(err.Error()) {
		return nil
	}
	return &csvSignerVersionFailure{Version: version, Cause: err}
}
