package dunetools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type Manager struct {
	mu            sync.Mutex
	browser       BrowserClient
	mailbox       Mailbox
	verifier      LinkVerifier
	now           func() time.Time
	onAccountDone func(context.Context, Account)
	cancel        context.CancelFunc
	runConfig     RunConfig
	task          TaskSnapshot
	pendingCfg    *RunConfig
	pendingTotal  int
	pendingMode   string
}

func NewManager(opts ManagerOptions) *Manager {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Manager{
		browser:       opts.Browser,
		mailbox:       opts.Mailbox,
		verifier:      opts.Verifier,
		now:           now,
		onAccountDone: opts.OnAccountDone,
		task:          TaskSnapshot{Status: TaskStatusIdle, Accounts: []Account{}},
	}
}

func (m *Manager) Start(ctx context.Context, req StartRequest) (TaskSnapshot, error) {
	mode := req.Mode
	if mode == "" {
		mode = ModeFull
	}

	// Pre-check: full mode requires auth.json (for CF clearance during login step)
	// If auth.json is missing, automatically redirect to capture mode
	// Note: register-only mode does NOT require auth.json — registration works without it
	redirectedFrom := ""
	if mode == ModeFull {
		if pb, ok := m.browser.(PlaywrightBrowser); ok && !pb.HasValidAuth() {
			log.Info().Str("requested_mode", mode).Msg("dune_auth_missing_redirecting_to_capture")
			redirectedFrom = mode
			// Save original request so capture can auto-restart after success
			origReq := req
			origReq.Mode = mode
			origCfg, origErr := ResolveRunConfig(origReq)
			if origErr == nil {
				m.pendingCfg = &origCfg
				m.pendingTotal = req.Total
				m.pendingMode = mode
			}
			req.Mode = ModeCapture
			mode = ModeCapture
		}
	}

	cfg, err := ResolveRunConfig(req)
	if err != nil {
		return TaskSnapshot{}, err
	}
	if m.browser == nil || m.mailbox == nil || m.verifier == nil {
		return TaskSnapshot{}, fmt.Errorf("dune batch manager is not configured")
	}
	taskCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	now := m.now().UTC()
	total := req.Total
	var verifyLoginAccounts []Account
	if mode == ModeVerifyLogin {
		// verify_login mode: process existing wait_verify accounts
		m.mu.Lock()
		for _, a := range m.task.Accounts {
			if a.Status == AccountStatusWaitVerify {
				verifyLoginAccounts = append(verifyLoginAccounts, a)
			}
		}
		m.mu.Unlock()
		if len(verifyLoginAccounts) == 0 {
			cancel()
			return TaskSnapshot{}, fmt.Errorf("no accounts waiting for verification")
		}
		total = len(verifyLoginAccounts)
	}
	if mode == ModeLogin {
		if req.LoginEmail == "" || req.LoginPassword == "" {
			cancel()
			return TaskSnapshot{}, fmt.Errorf("login_email and login_password required for login mode")
		}
		total = 1
	}
	if mode == ModeCapture {
		total = 1
	}
	task := TaskSnapshot{
		ID:             uuid.NewString(),
		Total:          total,
		Status:         TaskStatusRunning,
		Accounts:       []Account{},
		StartedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
		RedirectedFrom: redirectedFrom,
	}
	if mode == ModeLogin {
		task.Accounts = []Account{{
			Email:    req.LoginEmail,
			Password: req.LoginPassword,
			Status:   AccountStatusLogin,
		}}
	}
	if mode == ModeVerifyLogin {
		task.Accounts = verifyLoginAccounts
	}
	m.mu.Lock()
	if m.task.Status == TaskStatusRunning {
		m.mu.Unlock()
		cancel()
		return TaskSnapshot{}, fmt.Errorf("dune batch task is already running")
	}
	m.cancel = cancel
	m.runConfig = cfg
	m.task = task
	m.mu.Unlock()
	if mode == ModeLogin {
		go m.runLogin(taskCtx, task.Accounts[0])
	} else if mode == ModeCapture {
		go m.runCapture(taskCtx)
	} else {
		go m.runTask(taskCtx, cfg, total, mode)
	}
	return task, nil
}

func (m *Manager) Stop() TaskSnapshot {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	if m.task.Status == TaskStatusRunning {
		m.task.Status = TaskStatusStopped
		m.touchLocked()
	}
	snapshot := cloneTask(m.task)
	m.mu.Unlock()
	return snapshot
}

func (m *Manager) Status() TaskSnapshot {
	m.mu.Lock()
	snapshot := cloneTask(m.task)
	m.mu.Unlock()
	return snapshot
}

func (m *Manager) Accounts() []Account {
	return m.Status().Accounts
}

// RemoveAccounts removes accounts from the current task by email
func (m *Manager) RemoveAccounts(emails []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	emailSet := make(map[string]bool, len(emails))
	for _, e := range emails {
		emailSet[e] = true
	}
	var kept []Account
	for _, a := range m.task.Accounts {
		if !emailSet[a.Email] {
			kept = append(kept, a)
		}
	}
	m.task.Accounts = kept
	m.touchLocked()
}

func (m *Manager) runTask(ctx context.Context, cfg RunConfig, total int, mode string) {
	if mode == ModeVerifyLogin {
		m.runVerifyLogin(ctx, cfg)
		return
	}
	// full or register mode: generate + register accounts
	for i := 0; i < total; i++ {
		select {
		case <-ctx.Done():
			m.setTaskStatus(TaskStatusStopped)
			return
		default:
		}
		account, err := generateAccount(cfg.Domain, m.now().UTC())
		if err != nil {
			m.addFailedAccount(account, fmt.Errorf("generate account: %w", err))
			continue
		}
		// Gmail alias: ldj1009538134+dune_<8hex>@gmail.com
		if strings.HasSuffix(cfg.Domain, "gmail.com") || strings.HasSuffix(cfg.Domain, "googlemail.com") {
			localPart, _, _ := strings.Cut(cfg.Mail.Username, "@")
			account.Email = localPart + "+" + account.Email
		}
		m.upsertAccount(account)
		done := m.runAccount(ctx, cfg, account, mode)
		if done && m.onAccountDone != nil {
			m.onAccountDone(ctx, m.accountByEmail(account.Email))
		}
		if i < total-1 && !m.waitInterval(ctx, cfg.Interval) {
			m.setTaskStatus(TaskStatusStopped)
			return
		}
	}
	m.setTaskStatus(TaskStatusDone)
}

func (m *Manager) runAccount(ctx context.Context, cfg RunConfig, account Account, mode string) bool {
	account.Status = AccountStatusRegister
	m.upsertAccount(account)
	registerResult, err := m.browser.Register(ctx, account)
	if err != nil {
		m.failAccount(account, fmt.Errorf("register: %w", err))
		return false
	}
	if registerResult.Captcha {
		account.Status = AccountStatusCaptcha
		account.Error = "Dune signup requires CAPTCHA"
		account.CaptchaFile = registerResult.CaptchaFile
		m.upsertAccount(account)
		m.incrementFailed()
		return false
	}
	if !registerResult.OK {
		if registerResult.Banned {
			m.banAccount(account, registerResult.Error)
			return false
		}
		m.failAccount(account, fmt.Errorf("register failed: %s", registerResult.Error))
		return false
	}
	account.Status = AccountStatusVerifyMail
	m.upsertAccount(account)
	link, err := m.mailbox.WaitForVerificationLink(ctx, cfg.Mail, account.Email, m.now().UTC())
	if err != nil {
		m.failAccount(account, fmt.Errorf("wait verification email: %w", err))
		return false
	}
	// Register-only mode: save verification link, stop here
	if mode == ModeRegister {
		account.Status = AccountStatusWaitVerify
		account.VerifyLink = link
		account.Error = ""
		m.upsertAccount(account)
		m.incrementCompleted()
		return true
	}
	return m.verifyAndLoginAccount(ctx, account, link)
}

func (m *Manager) verifyAndLoginAccount(ctx context.Context, account Account, link string) bool {
	// Use Playwright browser to verify email AND login in same session (avoids CF re-detection)
	verifyResult, err := m.browser.VerifyEmail(ctx, link, account)
	if err != nil || !verifyResult.OK {
		// Fallback: try HTTP verification + separate login
		if httpErr := m.verifier.VerifyEmailLink(ctx, link); httpErr != nil {
			errMsg := "verify email link: browser failed"
			if err != nil {
				errMsg = "verify email link: " + err.Error()
			} else if verifyResult.Error != "" {
				errMsg = "verify email link: " + verifyResult.Error
			}
			m.failAccount(account, fmt.Errorf("%s (HTTP fallback also failed: %s)", errMsg, httpErr.Error()))
			return false
		}
		log.Info().Str("email", account.Email).Msg("email_verified_via_http_fallback")
		// Continue to separate login below
	} else if verifyResult.Cookie != "" && verifyResult.Authorization != "" {
		// Combined verify+login succeeded — credentials extracted!
		log.Info().Str("email", account.Email).Int64("team_id", verifyResult.TeamID).Msg("verify_and_login_combined_success")
		account.Cookie = verifyResult.Cookie
		account.Authorization = verifyResult.Authorization
		account.AccessToken = verifyResult.AccessToken
		account.TeamID = verifyResult.TeamID
		account.Status = AccountStatusDone
		m.upsertAccount(account)
		m.incrementCompleted()
		return true
	}
	// If we completed via combined verify+login above, skip separate login
	if account.Status == AccountStatusDone {
		return true
	}
	// If verify+login ran and returned OK but without credentials, fail gracefully
	if verifyResult.OK {
		m.failAccount(account, fmt.Errorf("verify+login completed but no credentials extracted"))
		return false
	}
	// HTTP fallback path: need separate login (only used when browser verify completely failed)
	account.Status = AccountStatusLogin
	m.upsertAccount(account)
	loginResult, err := m.browser.LoginAndExtract(ctx, account)
	if err != nil {
		m.failAccount(account, fmt.Errorf("login: %w", err))
		return false
	}
	if !loginResult.OK {
		m.failAccount(account, fmt.Errorf("login failed: %s", loginResult.Error))
		return false
	}
	if loginResult.Cookie == "" || loginResult.Authorization == "" || loginResult.AccessToken == "" {
		m.failAccount(account, fmt.Errorf("login did not return complete Dune credentials"))
		return false
	}
	account.Cookie = loginResult.Cookie
	account.Authorization = loginResult.Authorization
	account.AccessToken = loginResult.AccessToken
	account.TeamID = loginResult.TeamID
	account.Status = AccountStatusDone
	m.upsertAccount(account)
	m.incrementCompleted()
	return true
}

func (m *Manager) runCapture(ctx context.Context) {
	// Directly call the Playwright bridge with "capture" mode
	// The browser opens and waits for the user to manually log in
	pb, ok := m.browser.(PlaywrightBrowser)
	if !ok {
		m.failAccount(Account{Email: "capture"}, fmt.Errorf("capture requires Playwright browser"))
		m.setTaskStatus(TaskStatusDone)
		return
	}
	result, err := pb.Run(ctx, "capture", Account{})
	if err != nil {
		m.failAccount(Account{Email: "capture"}, fmt.Errorf("capture: %w", err))
		m.setTaskStatus(TaskStatusDone)
		return
	}
	if !result.OK {
		m.failAccount(Account{Email: "capture"}, fmt.Errorf("capture failed: %s", result.Error))
		m.setTaskStatus(TaskStatusDone)
		return
	}
	account := Account{
		Email:         "capture",
		Cookie:        result.Cookie,
		Authorization: result.Authorization,
		AccessToken:   result.AccessToken,
		TeamID:        result.TeamID,
		Status:        AccountStatusDone,
	}
	m.upsertAccount(account)
	m.incrementCompleted()
	if m.onAccountDone != nil {
		m.onAccountDone(ctx, account)
	}

	// Restart original task if capture was auto-redirected from full mode
	if m.pendingCfg != nil && m.pendingMode != "" {
		cfg := *m.pendingCfg
		total := m.pendingTotal
		mode := m.pendingMode
		m.pendingCfg = nil
		m.pendingMode = ""
		log.Info().Str("mode", mode).Int("total", total).Msg("dune_auth_captured_restarting_task")
		go m.runTask(ctx, cfg, total, mode)
		return
	}
	m.setTaskStatus(TaskStatusDone)
}

func (m *Manager) runLogin(ctx context.Context, account Account) {
	loginResult, err := m.browser.LoginAndExtract(ctx, account)
	if err != nil {
		m.failAccount(account, fmt.Errorf("login: %w", err))
		m.setTaskStatus(TaskStatusDone)
		return
	}
	if !loginResult.OK {
		m.failAccount(account, fmt.Errorf("login failed: %s", loginResult.Error))
		m.setTaskStatus(TaskStatusDone)
		return
	}
	account.Cookie = loginResult.Cookie
	account.Authorization = loginResult.Authorization
	account.AccessToken = loginResult.AccessToken
	account.TeamID = loginResult.TeamID
	account.Status = AccountStatusDone
	m.upsertAccount(account)
	m.incrementCompleted()
	if m.onAccountDone != nil {
		m.onAccountDone(ctx, account)
	}
	m.setTaskStatus(TaskStatusDone)
}

func (m *Manager) runVerifyLogin(ctx context.Context, cfg RunConfig) {
	m.mu.Lock()
	var waiting []Account
	for _, a := range m.task.Accounts {
		if a.Status == AccountStatusWaitVerify && a.VerifyLink != "" {
			waiting = append(waiting, a)
		}
	}
	m.mu.Unlock()

	for i, account := range waiting {
		select {
		case <-ctx.Done():
			m.setTaskStatus(TaskStatusStopped)
			return
		default:
		}
		account.Status = AccountStatusLogin
		m.upsertAccount(account)
		done := m.verifyAndLoginAccount(ctx, account, account.VerifyLink)
		if done && m.onAccountDone != nil {
			m.onAccountDone(ctx, m.accountByEmail(account.Email))
		}
		if i < len(waiting)-1 && !m.waitInterval(ctx, cfg.Interval) {
			m.setTaskStatus(TaskStatusStopped)
			return
		}
	}
	m.setTaskStatus(TaskStatusDone)
}

func (m *Manager) waitInterval(ctx context.Context, interval time.Duration) bool {
	if interval <= 0 {
		return true
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (m *Manager) addFailedAccount(account Account, err error) {
	account.Status = AccountStatusFailed
	account.Error = err.Error()
	m.upsertAccount(account)
	m.incrementFailed()
}

func (m *Manager) banAccount(account Account, reason string) {
	account.Status = AccountStatusBanned
	account.Error = reason
	m.upsertAccount(account)
	m.incrementFailed()
}

func (m *Manager) failAccount(account Account, err error) {
	m.addFailedAccount(account, err)
}

func (m *Manager) upsertAccount(account Account) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.task.Accounts {
		if m.task.Accounts[i].Email == account.Email {
			m.task.Accounts[i] = account
			m.touchLocked()
			return
		}
	}
	m.task.Accounts = append(m.task.Accounts, account)
	m.touchLocked()
}

func (m *Manager) accountByEmail(email string) Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, account := range m.task.Accounts {
		if account.Email == email {
			return account
		}
	}
	return Account{}
}

func (m *Manager) incrementCompleted() {
	m.mu.Lock()
	m.task.Completed++
	m.touchLocked()
	m.mu.Unlock()
}

func (m *Manager) incrementFailed() {
	m.mu.Lock()
	m.task.Failed++
	m.touchLocked()
	m.mu.Unlock()
}

func (m *Manager) setTaskStatus(status TaskStatus) {
	m.mu.Lock()
	m.task.Status = status
	m.touchLocked()
	m.mu.Unlock()
}

func (m *Manager) touchLocked() {
	m.task.UpdatedAt = m.now().UTC().Format(time.RFC3339)
}

func cloneTask(task TaskSnapshot) TaskSnapshot {
	accounts := make([]Account, len(task.Accounts))
	copy(accounts, task.Accounts)
	task.Accounts = accounts
	return task
}
