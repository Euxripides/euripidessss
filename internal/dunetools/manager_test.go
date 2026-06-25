package dunetools

import (
	"context"
	"testing"
	"time"
)

type fakeBrowser struct {
	registerErr  error
	verifyResult BrowserResult
	verifyErr    error
	loginResult  BrowserResult
	loginErr     error
}

func (f fakeBrowser) Register(ctx context.Context, account Account) (BrowserResult, error) {
	return BrowserResult{OK: f.registerErr == nil}, f.registerErr
}

func (f fakeBrowser) VerifyEmail(ctx context.Context, link string, account Account) (BrowserResult, error) {
	if f.verifyErr != nil {
		return BrowserResult{}, f.verifyErr
	}
	if f.verifyResult.OK || f.verifyResult.Cookie != "" {
		return f.verifyResult, nil
	}
	return BrowserResult{OK: true, Cookie: "auth-user=u; auth-id-token=id", Authorization: "Bearer id", AccessToken: "access", TeamID: 42}, nil
}

func (f fakeBrowser) LoginAndExtract(ctx context.Context, account Account) (BrowserResult, error) {
	return f.loginResult, f.loginErr
}

type fakeMailbox struct {
	link string
	err  error
}

func (f fakeMailbox) WaitForVerificationLink(ctx context.Context, cfg MailConfig, accountEmail string, since time.Time) (string, error) {
	return f.link, f.err
}

type fakeVerifier struct {
	err error
}

func (f fakeVerifier) VerifyEmailLink(ctx context.Context, link string) error {
	return f.err
}

func TestManager_completesAccount_whenRegistrationVerificationAndLoginSucceed(t *testing.T) {
	// Given
	manager := NewManager(ManagerOptions{
		Browser: fakeBrowser{loginResult: BrowserResult{
			OK:            true,
			Cookie:        "auth-user=u; auth-id-token=id",
			Authorization: "Bearer id",
			AccessToken:   "access",
			TeamID:        42,
		}},
		Mailbox:  fakeMailbox{link: "https://dune.com/verify-email?token=abc"},
		Verifier: fakeVerifier{},
		Now:      func() time.Time { return time.Unix(100, 0).UTC() },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// When
	if _, err := manager.Start(ctx, StartRequest{
		Total:          1,
		Domain:         "aurore.online",
		IntervalSecond: 0,
		IMAPUser:       "ldj1009538134@gmail.com",
		IMAPPassword:   "secret",
	}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	// Then
	snapshot := waitForTaskStatus(t, ctx, manager, TaskStatusDone)
	if snapshot.Completed != 1 {
		t.Fatalf("completed = %d", snapshot.Completed)
	}
	if len(snapshot.Accounts) != 1 {
		t.Fatalf("accounts length = %d", len(snapshot.Accounts))
	}
	account := snapshot.Accounts[0]
	if account.Status != AccountStatusDone {
		t.Fatalf("account status = %q, error=%q", account.Status, account.Error)
	}
	if account.Cookie == "" || account.Authorization == "" || account.AccessToken == "" {
		t.Fatalf("expected extracted credentials, got cookie=%q authorization=%q access=%q", account.Cookie, account.Authorization, account.AccessToken)
	}
}

func TestManager_rejectsStart_whenIMAPPasswordMissing(t *testing.T) {
	// Given
	manager := NewManager(ManagerOptions{
		Browser:  fakeBrowser{},
		Mailbox:  fakeMailbox{},
		Verifier: fakeVerifier{},
	})

	// When
	_, err := manager.Start(context.Background(), StartRequest{
		Total:    1,
		Domain:   "aurore.online",
		IMAPUser: "ldj1009538134@gmail.com",
	})

	// Then
	if err == nil {
		t.Fatalf("expected missing password error")
	}
}

func TestManager_VerifyLogin_completesWaitingAccount_whenVerificationLinkExists(t *testing.T) {
	// Given
	manager := NewManager(ManagerOptions{
		Browser:  fakeBrowser{},
		Mailbox:  fakeMailbox{},
		Verifier: fakeVerifier{},
		Now:      func() time.Time { return time.Unix(100, 0).UTC() },
	})
	manager.task = TaskSnapshot{
		Status: TaskStatusDone,
		Accounts: []Account{{
			Email:      "dune_waiting@aurore.online",
			Username:   "u_waiting",
			Password:   "AaBbCcDd1234!@#$",
			Status:     AccountStatusWaitVerify,
			VerifyLink: "https://dune.com/verify-email?token=abc",
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// When
	if _, err := manager.Start(ctx, StartRequest{
		Mode:           ModeVerifyLogin,
		IntervalSecond: 1,
	}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	// Then
	snapshot := waitForTaskStatus(t, ctx, manager, TaskStatusDone)
	if snapshot.Completed != 1 {
		t.Fatalf("completed = %d, want 1; snapshot=%+v", snapshot.Completed, snapshot)
	}
	if len(snapshot.Accounts) != 1 {
		t.Fatalf("accounts length = %d, want 1", len(snapshot.Accounts))
	}
	account := snapshot.Accounts[0]
	if account.Status != AccountStatusDone {
		t.Fatalf("account status = %q, error=%q", account.Status, account.Error)
	}
	if account.Cookie == "" || account.Authorization == "" || account.AccessToken == "" {
		t.Fatalf("expected extracted credentials, got cookie=%q authorization=%q access=%q", account.Cookie, account.Authorization, account.AccessToken)
	}
}

func TestManager_RetryCaptcha_countsCompletedAccountOnce_whenRetrySucceeds(t *testing.T) {
	// Given
	manager := NewManager(ManagerOptions{
		Browser: fakeBrowser{loginResult: BrowserResult{
			OK:            true,
			Cookie:        "auth-user=u; auth-id-token=id",
			Authorization: "Bearer id",
			AccessToken:   "access",
			TeamID:        42,
		}},
		Mailbox:  fakeMailbox{link: "https://dune.com/verify-email?token=abc"},
		Verifier: fakeVerifier{},
		Now:      func() time.Time { return time.Unix(100, 0).UTC() },
	})
	manager.runConfig = RunConfig{
		Domain: "aurore.online",
		Mail: MailConfig{
			Host:      "imap.gmail.com:993",
			Username:  "ldj1009538134@gmail.com",
			Password:  "secret",
			Wait:      time.Second,
			PollEvery: time.Millisecond,
		},
	}
	manager.task = TaskSnapshot{
		Status: TaskStatusRunning,
		Accounts: []Account{{
			Email:    "dune_retry@aurore.online",
			Username: "u_retry",
			Password: "AaBbCcDd1234!@#$",
			Status:   AccountStatusCaptcha,
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// When
	manager.RetryCaptcha(Account{Email: "dune_retry@aurore.online"})

	// Then
	snapshot := waitForCompletedCount(t, ctx, manager, 1)
	if snapshot.Completed != 1 {
		t.Fatalf("completed = %d, want 1", snapshot.Completed)
	}
	if snapshot.Accounts[0].Status != AccountStatusDone {
		t.Fatalf("account status = %q, error=%q", snapshot.Accounts[0].Status, snapshot.Accounts[0].Error)
	}
}

func waitForTaskStatus(t *testing.T, ctx context.Context, manager *Manager, want TaskStatus) TaskSnapshot {
	t.Helper()
	for {
		snapshot := manager.Status()
		if snapshot.Status == want {
			return snapshot
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for status %q, last=%+v", want, snapshot)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForCompletedCount(t *testing.T, ctx context.Context, manager *Manager, want int) TaskSnapshot {
	t.Helper()
	for {
		snapshot := manager.Status()
		if snapshot.Completed >= want {
			return snapshot
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for completed count %d, last=%+v", want, snapshot)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
