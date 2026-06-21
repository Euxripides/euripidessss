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
	cfg, err := ResolveRunConfig(req)
	if err != nil {
		return TaskSnapshot{}, err
	}
	if m.browser == nil || m.mailbox == nil || m.verifier == nil {
		return TaskSnapshot{}, fmt.Errorf("dune batch manager is not configured")
	}
	taskCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	now := m.now().UTC()
	task := TaskSnapshot{
		ID:        uuid.NewString(),
		Total:     req.Total,
		Status:    TaskStatusRunning,
		Accounts:  []Account{},
		StartedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
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
	go m.runTask(taskCtx, cfg, req.Total)
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

func (m *Manager) runTask(ctx context.Context, cfg RunConfig, total int) {
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
		done := m.runAccount(ctx, cfg, account)
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

func (m *Manager) runAccount(ctx context.Context, cfg RunConfig, account Account) bool {
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
