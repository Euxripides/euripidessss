package dunetools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type PlaywrightBrowser struct {
	ScriptPath  string
	ProfileRoot string
	ProxyServer string
	Channel     string
	AuthFile    string
	Timeout     time.Duration
}

func NewDefaultManager(root string, onAccountDone ...func(context.Context, Account)) *Manager {
	scriptPath := filepath.Join(root, "tools", "dune-playwright", "register-login.mjs")
	var sink func(context.Context, Account)
	if len(onAccountDone) > 0 {
		sink = onAccountDone[0]
	}
	channel := os.Getenv("DUNE_BATCH_CHANNEL")
	return NewManager(ManagerOptions{
		Browser: PlaywrightBrowser{
			ScriptPath:  scriptPath,
			ProfileRoot: filepath.Join(root, "backend", "data", "dune"),
			ProxyServer: os.Getenv("DUNE_BATCH_PROXY"),
			Channel:     channel,
			AuthFile:    filepath.Join(root, "backend", "data", "dune", "auth.json"),
			Timeout:     16 * time.Minute,
		},
		Mailbox:       RawIMAPMailbox{},
		Verifier:      HTTPLinkVerifier{},
		OnAccountDone: sink,
	})
}

func (p PlaywrightBrowser) Register(ctx context.Context, account Account) (BrowserResult, error) {
	return p.run(ctx, "register", account)
}

func (p PlaywrightBrowser) VerifyEmail(ctx context.Context, link string, account Account) (BrowserResult, error) {
	return p.run(ctx, "verify", account, link)
}

func (p PlaywrightBrowser) LoginAndExtract(ctx context.Context, account Account) (BrowserResult, error) {
	return p.run(ctx, "login", account)
}

// Run executes the Playwright script with a custom mode (e.g., "capture")
func (p PlaywrightBrowser) Run(ctx context.Context, mode string, account Account) (BrowserResult, error) {
	return p.run(ctx, mode, account)
}

// HasValidAuth checks whether auth.json exists and contains usable credentials
func (p PlaywrightBrowser) HasValidAuth() bool {
	if p.AuthFile == "" {
		return false
	}
	data, err := os.ReadFile(p.AuthFile)
	if err != nil {
		return false
	}
	var auth duneStoredAuth
	if err := json.Unmarshal(data, &auth); err != nil {
		return false
	}
	return auth.Cookie != ""
}

type duneStoredAuth struct {
	Cookie        string `json:"cookie"`
	Authorization string `json:"authorization"`
	AccessToken   string `json:"access_token"`
}

func (p PlaywrightBrowser) loadAuthCookie() string {
	if p.AuthFile == "" {
		return ""
	}
	data, err := os.ReadFile(p.AuthFile)
	if err != nil {
		return ""
	}
	var auth duneStoredAuth
	if err := json.Unmarshal(data, &auth); err != nil {
		return ""
	}
	return auth.Cookie
}

func (p PlaywrightBrowser) run(ctx context.Context, mode string, account Account, verifyLink ...string) (BrowserResult, error) {
	if _, err := os.Stat(p.ScriptPath); err != nil {
		return BrowserResult{}, fmt.Errorf("find Playwright script: %w", err)
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	// Verification mode doesn't need the full timeout
	if mode == "verify" && timeout > 2*time.Minute {
		timeout = 2 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	profileDir := filepath.Join(p.ProfileRoot, "profiles", "master") // Shared master profile for CF clearance persistence
	input := map[string]interface{}{
		"mode":       mode,
		"email":      account.Email,
		"username":   account.Username,
		"password":    account.Password,
		"profileDir": profileDir,
		"timeoutMs":  int(timeout.Milliseconds()),
	}
	if len(verifyLink) > 0 && verifyLink[0] != "" {
		input["verifyLink"] = verifyLink[0]
	}
	if p.ProxyServer != "" {
		input["proxyServer"] = p.ProxyServer
	}
	if p.Channel != "" {
		input["channel"] = p.Channel
	}
	if cookie := p.loadAuthCookie(); cookie != "" {
		input["cookie"] = cookie
	}
	data, err := json.Marshal(input)
	if err != nil {
		return BrowserResult{}, fmt.Errorf("marshal Playwright input: %w", err)
	}
	cmd := exec.CommandContext(runCtx, "node", p.ScriptPath)
	cmd.Dir = filepath.Dir(p.ScriptPath)
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if strings.Contains(stderrStr, "CAPTCHA_DETECTED") {
			return BrowserResult{Captcha: true, Error: "CAPTCHA required"}, nil
		}
		if result, parseErr := parseBrowserResult(stderr.Bytes()); parseErr == nil {
			log.Warn().Str("mode", mode).Str("stderr", trunc(stderrStr, 1000)).Str("email", account.Email).Msg("pw_bridge_failed")
			return result, fmt.Errorf("%s failed: %s", mode, result.Error)
		}
		log.Warn().Str("mode", mode).Str("stderr", trunc(stderrStr, 1000)).Str("email", account.Email).Msg("pw_bridge_crashed")
		return BrowserResult{}, fmt.Errorf("%s via Playwright: %w: %s", mode, err, trunc(stderrStr, 500))
	}
	stderrStr := strings.TrimSpace(stderr.String())
	if strings.Contains(stderrStr, "CAPTCHA_DETECTED") {
		return BrowserResult{Captcha: true}, nil
	}
	stderrStr = strings.TrimSpace(stderr.String())
	if stderrStr != "" {
		log.Info().Str("mode", mode).Str("stderr", trunc(stderrStr, 500)).Str("email", account.Email).Msg("pw_bridge_stderr")
	}
	result, err := parseBrowserResult(stdout.Bytes())
	if err != nil {
		return BrowserResult{}, fmt.Errorf("parse Playwright output: %w", err)
	}
	if !result.OK && result.HTML != "" {
		log.Warn().Str("mode", mode).Str("error", result.Error).Str("html", trunc(result.HTML, 500)).Msg("pw_bridge_detection_html")
	}
	return result, nil
}

func parseBrowserResult(data []byte) (BrowserResult, error) {
	var result BrowserResult
	if err := json.Unmarshal(bytes.TrimSpace(data), &result); err != nil {
		return BrowserResult{}, err
	}
	return result, nil
}

func safeProfileName(email string) string {
	replacer := strings.NewReplacer("@", "_at_", ".", "_", "+", "_", ":", "_")
	return replacer.Replace(email)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
