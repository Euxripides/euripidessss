package dunetools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

var detectRodChromePath = defaultRodChromePath

func (r RodUserModeBrowser) openLoginFromHome(ctx context.Context, page *rod.Page) error {
	if ok, _ := rodBool(page, rodClickTextJS, []string{"Log in", "Sign in"}); ok {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		if err := r.waitForManualVerification(ctx, page); err != nil {
			return err
		}
		return nil
	}
	if err := page.Navigate(duneLoginURL); err != nil {
		return fmt.Errorf("navigate Dune login page: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("wait Dune login page: %w", err)
	}
	return r.waitForManualVerification(ctx, page)
}

func (r RodUserModeBrowser) remoteDebuggingPort() int {
	if r.Port > 0 {
		return r.Port
	}
	for _, key := range []string{"DUNE_ROD_REMOTE_DEBUGGING_PORT", "DUNE_QUERY_CDP_PORT"} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			if port, err := strconv.Atoi(raw); err == nil && port > 0 {
				return port
			}
		}
	}
	return 37712
}

func (r RodUserModeBrowser) resolvedChromePath() string {
	if path := strings.TrimSpace(r.ChromePath); path != "" {
		return path
	}
	return detectRodChromePath()
}

func defaultRodChromePath() string {
	candidates := []string{
		filepath.Join(os.Getenv("PROGRAMFILES"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Google", "Chrome", "Application", "chrome.exe"),
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func (r RodUserModeBrowser) profileDir(account Account) string {
	if dir := strings.TrimSpace(os.Getenv("DUNE_ROD_USER_DATA_DIR")); dir != "" {
		return dir
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DUNE_ROD_USE_DEFAULT_PROFILE")), "1") {
		return ""
	}
	root := r.ProfileRoot
	if root == "" {
		return ""
	}
	return filepath.Join(root, "profiles", "rod_"+safeProfileName(account.Email))
}

func (r RodUserModeBrowser) waitForManualVerification(ctx context.Context, page *rod.Page) error {
	wait := r.ManualWait
	if wait <= 0 {
		wait = 3 * time.Minute
	}
	deadline := time.Now().Add(wait)
	for {
		blocked, err := rodBool(page, rodPageBlockedJS)
		if err == nil && !blocked {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Dune verification page still active; manual verification timed out")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (r RodUserModeBrowser) fillAndSubmitLogin(ctx context.Context, page *rod.Page, account Account) error {
	_, _ = page.Eval(rodClickTextJS, []string{"Log in with email", "Continue with email", "Email"})
	time.Sleep(1500 * time.Millisecond)
	ok, err := rodBool(page, rodFillLoginJS, account.Email, account.Password)
	if err != nil {
		return fmt.Errorf("fill Dune login form: %w", err)
	}
	if !ok {
		return fmt.Errorf("Dune login form not found")
	}
	_, _ = page.Eval(rodClickTextJS, []string{"Log in", "Sign in", "Continue"})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return nil
	}
}

func (r RodUserModeBrowser) waitLoggedInOrManual(ctx context.Context, page *rod.Page, account Account) error {
	deadline := time.Now().Add(r.loginWait())
	for {
		if blocked, _ := rodBool(page, rodPageBlockedJS); blocked {
			if err := r.waitForManualVerification(ctx, page); err != nil {
				return err
			}
		}
		if ok, _ := rodBool(page, rodLoggedInSurfaceJS); ok {
			_, _ = page.Eval(rodClickTextJS, []string{"Skip", "Maybe later", "Continue", "Next", "Get started"})
			return nil
		}
		_, _ = page.Eval(rodFillUsernameJS, account.Username)
		if time.Now().After(deadline) {
			return fmt.Errorf("Dune login did not reach an authenticated page")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (r RodUserModeBrowser) loginWait() time.Duration {
	if r.ManualWait > 0 {
		return r.ManualWait
	}
	return 3 * time.Minute
}

func (r RodUserModeBrowser) extractIfLoggedIn(browser *rod.Browser, page *rod.Page) (BrowserResult, bool) {
	cookies, err := browser.GetCookies()
	if err != nil {
		return BrowserResult{}, false
	}
	var cookieParts []string
	authorization := ""
	for _, cookie := range cookies {
		if !strings.Contains(cookie.Domain, "dune.com") {
			continue
		}
		cookieParts = append(cookieParts, cookie.Name+"="+cookie.Value)
		if cookie.Name == "auth-id-token" {
			authorization = "Bearer " + cookie.Value
		}
	}
	cookie := strings.Join(cookieParts, "; ")
	accessToken, _ := rodString(page, rodAccessTokenJS)
	teamID := int64(0)
	if authorization != "" {
		if id, err := rodInt(page, rodTeamIDJS, authorization); err == nil {
			teamID = int64(id)
		}
	}
	result := BrowserResult{
		OK:            cookie != "" && (authorization != "" || accessToken != ""),
		Cookie:        cookie,
		Authorization: authorization,
		AccessToken:   accessToken,
		TeamID:        teamID,
	}
	return result, result.OK
}

func rodBool(page *rod.Page, js string, params ...interface{}) (bool, error) {
	value, err := page.Eval(js, params...)
	if err != nil {
		return false, err
	}
	return value.Value.Bool(), nil
}

func rodString(page *rod.Page, js string, params ...interface{}) (string, error) {
	value, err := page.Eval(js, params...)
	if err != nil {
		return "", err
	}
	return value.Value.String(), nil
}

func rodInt(page *rod.Page, js string, params ...interface{}) (int, error) {
	value, err := page.Eval(js, params...)
	if err != nil {
		return 0, err
	}
	return value.Value.Int(), nil
}
