package dunetools

import (
	"context"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
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

	launch := r.newUserModeLauncher(account)
	url, err := launch.Launch()
	if err != nil {
		return BrowserResult{}, fmt.Errorf("launch Rod user browser: %w", err)
	}
	defer launch.Kill()

	browser := rod.New().ControlURL(url).Context(runCtx)
	if err := browser.Connect(); err != nil {
		return BrowserResult{}, fmt.Errorf("connect Rod browser: %w", err)
	}
	defer browser.Close()

	page, err := browser.Page(proto.TargetCreateTarget{URL: duneHomeURL})
	if err != nil {
		return BrowserResult{}, fmt.Errorf("open Dune home page: %w", err)
	}
	defer page.Close()
	page = page.Context(runCtx)
	if err := page.WaitLoad(); err != nil {
		return BrowserResult{}, fmt.Errorf("wait Dune home page: %w", err)
	}
	if err := r.waitForManualVerification(runCtx, page); err != nil {
		return BrowserResult{}, err
	}
	if result, ok := r.extractIfLoggedIn(browser, page); ok {
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
