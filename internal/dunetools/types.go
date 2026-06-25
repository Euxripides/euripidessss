package dunetools

import (
	"context"
	"time"
)

type AccountStatus string

const (
	AccountStatusPending       AccountStatus = "pending"
	AccountStatusRegister      AccountStatus = "registering"
	AccountStatusVerifyMail    AccountStatus = "verifying"
	AccountStatusLogin         AccountStatus = "logging_in"
	AccountStatusCaptcha       AccountStatus = "captcha"
	AccountStatusDone          AccountStatus = "done"
	AccountStatusFailed        AccountStatus = "failed"
	AccountStatusWaitVerify    AccountStatus = "wait_verify" // Registered, email received, waiting for verify+login
	AccountStatusBanned        AccountStatus = "banned"      // Account blocked/suspended by Dune
)

type TaskStatus string

const (
	TaskStatusIdle    TaskStatus = "idle"
	TaskStatusRunning TaskStatus = "running"
	TaskStatusStopped TaskStatus = "stopped"
	TaskStatusDone    TaskStatus = "done"
)

type Account struct {
	Email         string        `json:"email"`
	Username      string        `json:"username"`
	Password      string        `json:"password"`
	Cookie        string        `json:"cookie,omitempty"`
	Authorization string        `json:"authorization,omitempty"`
	AccessToken   string        `json:"access_token,omitempty"`
	TeamID        int64         `json:"team_id,omitempty"`
	Status        AccountStatus `json:"status"`
	Error         string        `json:"error,omitempty"`
	CreatedAt     string        `json:"created_at,omitempty"`
	CaptchaFile   string        `json:"captcha_file,omitempty"`
	VerifyLink    string        `json:"verify_link,omitempty"`
}

type TaskSnapshot struct {
	ID           string     `json:"id"`
	Total        int        `json:"total"`
	Completed    int        `json:"completed"`
	Failed       int        `json:"failed"`
	Status       TaskStatus `json:"status"`
	Accounts     []Account  `json:"accounts"`
	StartedAt    string     `json:"started_at,omitempty"`
	UpdatedAt    string     `json:"updated_at,omitempty"`
	RedirectedFrom string   `json:"redirected_from,omitempty"`
}

type StartRequest struct {
	Total          int    `json:"total"`
	Domain         string `json:"domain"`
	IntervalSecond int    `json:"interval_seconds"`
	IMAPHost       string `json:"imap_host"`
	IMAPUser       string `json:"imap_user"`
	IMAPPassword   string `json:"imap_password"`
	Mode           string `json:"mode"` // "full" (default), "register", "verify_login", "login"
	LoginEmail     string `json:"login_email"`
	LoginPassword  string `json:"login_password"`
}

const (
	ModeFull        = "full"
	ModeRegister    = "register"
	ModeVerifyLogin = "verify_login"
	ModeLogin       = "login"
	ModeCapture     = "capture"
)

type MailConfig struct {
	Host      string
	Username  string
	Password  string
	Wait      time.Duration
	PollEvery time.Duration
}

type RunConfig struct {
	Domain   string
	Interval time.Duration
	Mail     MailConfig
}

type BrowserResult struct {
	OK            bool   `json:"ok"`
	Captcha       bool   `json:"captcha"`
	CaptchaFile   string `json:"captchaFile"`
	Cookie        string `json:"cookie"`
	Authorization string `json:"authorization"`
	AccessToken   string `json:"access_token"`
	TeamID        int64  `json:"team_id"`
	Error         string `json:"error"`
	HTML          string `json:"html"`
	Banned        bool   `json:"banned"`
}

type BrowserClient interface {
	Register(ctx context.Context, account Account) (BrowserResult, error)
	VerifyEmail(ctx context.Context, link string, account Account) (BrowserResult, error)
	LoginAndExtract(ctx context.Context, account Account) (BrowserResult, error)
}

type Mailbox interface {
	WaitForVerificationLink(ctx context.Context, cfg MailConfig, accountEmail string, since time.Time) (string, error)
}

type LinkVerifier interface {
	VerifyEmailLink(ctx context.Context, link string) error
}

type ManagerOptions struct {
	Browser       BrowserClient
	Mailbox       Mailbox
	Verifier      LinkVerifier
	Now           func() time.Time
	OnAccountDone func(context.Context, Account)
}
