package dunetools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

const duneLoginURL = "https://dune.com/auth/login"
const duneHomeURL = "https://dune.com/"

type RodUserModeBrowser struct {
	ProfileRoot string
	ChromePath  string
	Timeout     time.Duration
	ManualWait  time.Duration
	Port        int
}

func (r RodUserModeBrowser) Register(ctx context.Context, account Account) (BrowserResult, error) {
	return BrowserResult{}, fmt.Errorf("Rod user mode does not support registration for %s", account.Email)
}

func (r RodUserModeBrowser) VerifyEmail(ctx context.Context, link string, account Account) (BrowserResult, error) {
	return BrowserResult{}, fmt.Errorf("Rod user mode does not support email verification for %s", account.Email)
}

func (r RodUserModeBrowser) LoginAndExtract(ctx context.Context, account Account) (BrowserResult, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var launch *launcher.Launcher
	var ownsChrome bool

	port := r.remoteDebuggingPort()
	browser, ok := tryConnectExistingChrome(port)
	if ok {
		browser = browser.Context(runCtx)
	} else {
		launch = r.newUserModeLauncher(account)
		url, lerr := launch.Launch()
		if lerr != nil {
			return BrowserResult{}, fmt.Errorf("launch Rod user browser: %w", lerr)
		}
		browser = rod.New().ControlURL(url).Context(runCtx)
		if cerr := browser.Connect(); cerr != nil {
			launch.Kill()
			return BrowserResult{}, fmt.Errorf("connect Rod browser: %w", cerr)
		}
		ownsChrome = true
	}

	doClose := func() {
		if ownsChrome {
			browser.Close()
			if launch != nil {
				launch.Kill()
			}
		}
	}
	defer doClose()

	if !checkRodCFClearance(browser) {
		if err := r.waitForCFClearance(runCtx, browser); err != nil {
			return BrowserResult{}, err
		}
	}

	page, err := r.findOrCreateDunePage(runCtx, browser)
	if err != nil {
		return BrowserResult{}, err
	}
	defer page.Close()
	page = page.Context(runCtx)

	if blocked, _ := isRodBlocked(page); blocked {
		if err := r.waitForManualVerification(runCtx, page); err != nil {
			return BrowserResult{}, err
		}
	}

	if isRodLoggedIn(page) {
		if result, ok := r.extractIfLoggedIn(browser, page); ok {
			return result, nil
		}
	}

	if result, ok := r.extractIfLoggedIn(browser, page); ok && result.Cookie != "" {
		return result, nil
	}

	if err := r.openLoginFromHome(runCtx, page); err != nil {
		return BrowserResult{}, err
	}
	if err := r.fillAndSubmitLogin(runCtx, page, account); err != nil {
		return BrowserResult{}, err
	}
	if err := r.waitForManualVerification(runCtx, page); err != nil {
		return BrowserResult{}, err
	}
	if err := r.waitLoggedInOrManual(runCtx, page, account); err != nil {
		return BrowserResult{}, err
	}
	result, ok := r.extractIfLoggedIn(browser, page)
	if !ok {
		return BrowserResult{}, fmt.Errorf("Rod Dune login did not expose Cookie/Authorization")
	}
	return result, nil
}

func (r RodUserModeBrowser) newUserModeLauncher(account Account) *launcher.Launcher {
	launch := launcher.NewUserMode().RemoteDebuggingPort(r.remoteDebuggingPort())
	if chromePath := r.resolvedChromePath(); chromePath != "" {
		launch = launch.Bin(chromePath)
	}
	if dir := r.profileDir(account); dir != "" {
		launch = launch.UserDataDir(dir)
	}
	return launch
}

type cdpVersionResponse struct {
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func tryConnectExistingChrome(port int) (*rod.Browser, bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	var v cdpVersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, false
	}
	if v.WebSocketDebuggerURL == "" {
		return nil, false
	}
	browser := rod.New().ControlURL(v.WebSocketDebuggerURL)
	if err := browser.Connect(); err != nil {
		return nil, false
	}
	return browser, true
}
