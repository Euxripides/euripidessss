package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/dunetools"
)

type duneAccountLoginFunc func(ctx context.Context, account dunetools.Account) (duneStoredAuth, error)

var duneQueryAccountLogin duneAccountLoginFunc = loginDuneQueryAccountWithCache

func SetDuneAccountLoginForTest(fn duneAccountLoginFunc) duneAccountLoginFunc {
	prev := duneQueryAccountLogin
	duneQueryAccountLogin = fn
	duneLoginCacheMu.Lock()
	for k := range duneLoginCache {
		delete(duneLoginCache, k)
	}
	duneLoginCacheMu.Unlock()
	return prev
}

type cachedDuneAuth struct {
	auth      duneStoredAuth
	expiresAt time.Time
}

const duneLoginCacheTTL = 5 * time.Minute

var (
	duneLoginCache   = make(map[string]*cachedDuneAuth)
	duneLoginCacheMu sync.Mutex
)

func loginDuneQueryAccountWithCache(ctx context.Context, account dunetools.Account) (duneStoredAuth, error) {
	key := account.Email
	now := time.Now()
	duneLoginCacheMu.Lock()
	if cached, ok := duneLoginCache[key]; ok && now.Before(cached.expiresAt) && !duneStoredAuthNeedsRefresh(cached.auth, now) {
		c := *cached
		duneLoginCacheMu.Unlock()
		return c.auth, nil
	}
	delete(duneLoginCache, key)
	duneLoginCacheMu.Unlock()

	auth, err := loginDuneQueryAccount(ctx, account)
	if err != nil {
		return duneStoredAuth{}, err
	}

	duneLoginCacheMu.Lock()
	duneLoginCache[key] = &cachedDuneAuth{auth: auth, expiresAt: duneStoredAuthCacheExpiry(auth, time.Now(), duneLoginCacheTTL)}
	duneLoginCacheMu.Unlock()

	return auth, nil
}

func applyDuneAccountAuth(ctx context.Context, payload *duneQueryRequest) error {
	email := strings.TrimSpace(payload.AccountEmail)
	if email == "" {
		return nil
	}
	account, err := findDuneQueryAccount(email)
	if err != nil {
		return err
	}
	if auth, ok := storedDuneAuthFromAccount(account); ok && !duneStoredAuthNeedsRefresh(auth, time.Now()) {
		applyDuneStoredAuth(payload, auth)
		return persistDuneQueryAuth(auth)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
	}
	auth, err := duneQueryAccountLogin(ctx, account)
	if err != nil {
		return fmt.Errorf("Dune 账号后台登录失败: %w", err)
	}
	if strings.TrimSpace(auth.Cookie) == "" || (strings.TrimSpace(auth.Authorization) == "" && strings.TrimSpace(auth.AccessToken) == "") {
		return fmt.Errorf("Dune 账号后台登录未获取到完整 Cookie/Authorization/access_token")
	}
	applyDuneStoredAuth(payload, auth)
	account.Cookie = auth.Cookie
	account.Authorization = auth.Authorization
	account.AccessToken = auth.AccessToken
	account.TeamID = auth.TeamID
	account.Status = dunetools.AccountStatusDone
	account.Error = ""
	persistAccount(duneRootDir(), account)
	return persistDuneQueryAuth(auth)
}

func storedDuneAuthFromAccount(account dunetools.Account) (duneStoredAuth, bool) {
	auth := duneStoredAuth{
		Cookie:        strings.TrimSpace(account.Cookie),
		Authorization: normalizeDuneAuthorization(account.Authorization),
		AccessToken:   strings.TrimSpace(account.AccessToken),
		TeamID:        account.TeamID,
	}
	return auth, auth.Cookie != "" && (auth.Authorization != "" || auth.AccessToken != "")
}

func applyDuneStoredAuth(payload *duneQueryRequest, auth duneStoredAuth) {
	payload.Cookie = auth.Cookie
	payload.Authorization = auth.Authorization
	payload.AccessToken = auth.AccessToken
	if payload.TeamID <= 0 {
		payload.TeamID = auth.TeamID
	}
	payload.WebQuery = true
}

func persistDuneQueryAuth(auth duneStoredAuth) error {
	if existing, err := loadDuneStoredAuth(); err == nil {
		auth.APIKey = existing.APIKey
		if auth.TeamID <= 0 {
			auth.TeamID = existing.TeamID
		}
	}
	auth.UpdatedAt = time.Now().UTC()
	return saveDuneStoredAuth(auth)
}

func findDuneQueryAccount(email string) (dunetools.Account, error) {
	manager := currentDuneBatchManager()
	allAccountsMu.Lock()
	saved := make([]dunetools.Account, len(allAccounts))
	copy(saved, allAccounts)
	allAccountsMu.Unlock()
	accounts := mergeAccounts(saved, manager.Accounts())
	for _, account := range accounts {
		if account.Email != email {
			continue
		}
		if account.Status != dunetools.AccountStatusDone {
			return dunetools.Account{}, fmt.Errorf("Dune 账号 %s 状态不是 done，当前状态=%s", email, account.Status)
		}
		if strings.TrimSpace(account.Password) == "" {
			return dunetools.Account{}, fmt.Errorf("Dune 账号 %s 缺少密码，无法后台登录", email)
		}
		if strings.TrimSpace(account.Error) != "" {
			return dunetools.Account{}, fmt.Errorf("Dune 账号 %s 状态异常: %s", email, account.Error)
		}
		return account, nil
	}
	return dunetools.Account{}, fmt.Errorf("Dune 账号 %s 不存在或未保存", email)
}

func loginDuneQueryAccount(ctx context.Context, account dunetools.Account) (duneStoredAuth, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("DUNE_QUERY_LOGIN_BROWSER")), "playwright") {
		auth, err := loginDuneQueryAccountWithRod(ctx, account)
		if err == nil {
			return auth, nil
		}
		if !strings.EqualFold(strings.TrimSpace(os.Getenv("DUNE_QUERY_LOGIN_BROWSER")), "rod") {
			return loginDuneQueryAccountWithPlaywright(ctx, account)
		}
		return duneStoredAuth{}, err
	}
	return loginDuneQueryAccountWithPlaywright(ctx, account)
}

func loginDuneQueryAccountWithRod(ctx context.Context, account dunetools.Account) (duneStoredAuth, error) {
	root := duneRootDir()
	browser := dunetools.RodUserModeBrowser{
		ProfileRoot: filepath.Join(root, "backend", "data", "dune"),
		ChromePath:  os.Getenv("DUNE_CHROME_PATH"),
		Timeout:     10 * time.Minute,
		ManualWait:  5 * time.Minute,
	}
	result, err := browser.LoginAndExtract(ctx, account)
	if err != nil {
		return duneStoredAuth{}, err
	}
	if !result.OK {
		return duneStoredAuth{}, fmt.Errorf("rod login failed: %s", result.Error)
	}
	return duneStoredAuth{
		Cookie:        result.Cookie,
		Authorization: result.Authorization,
		AccessToken:   result.AccessToken,
		TeamID:        result.TeamID,
		UpdatedAt:     time.Now().UTC(),
	}, nil
}

func loginDuneQueryAccountWithPlaywright(ctx context.Context, account dunetools.Account) (duneStoredAuth, error) {
	root := duneRootDir()
	browser := dunetools.PlaywrightBrowser{
		ScriptPath:  filepath.Join(root, "tools", "dune-playwright", "register-login.mjs"),
		ProfileRoot: filepath.Join(root, "backend", "data", "dune"),
		ProxyServer: os.Getenv("DUNE_BATCH_PROXY"),
		Channel:     os.Getenv("DUNE_BATCH_CHANNEL"),
		AuthFile:    filepath.Join(root, "backend", "data", "dune", "auth.json"),
		Timeout:     8 * time.Minute,
		Headless:    false,
	}
	result, err := browser.LoginAndExtract(ctx, account)
	if err != nil {
		return duneStoredAuth{}, err
	}
	if !result.OK {
		return duneStoredAuth{}, fmt.Errorf("login failed: %s", result.Error)
	}
	return duneStoredAuth{
		Cookie:        result.Cookie,
		Authorization: result.Authorization,
		AccessToken:   result.AccessToken,
		TeamID:        result.TeamID,
		UpdatedAt:     time.Now().UTC(),
	}, nil
}

func duneRootDir() string {
	if cfg != nil && strings.TrimSpace(cfg.RootDir) != "" {
		return cfg.RootDir
	}
	return "."
}
