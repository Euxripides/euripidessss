package dunetools

import (
	"context"
	"fmt"
	"time"
)

func (m *Manager) RetryCaptcha(acct Account) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, a := range m.task.Accounts {
		if a.Email == acct.Email && string(a.Status) == string(AccountStatusCaptcha) {
			m.task.Accounts[i].Status = AccountStatusRegister
			m.task.Accounts[i].Error = ""
			go m.retryAccount(context.Background(), m.runConfig, m.task.Accounts[i])
			return
		}
	}
}

func (m *Manager) retryAccount(parentCtx context.Context, cfg RunConfig, account Account) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), 10*time.Minute)
	defer cancel()
	if m.runAccount(ctx, cfg, account) && m.onAccountDone != nil {
		m.onAccountDone(ctx, m.accountByEmail(account.Email))
	}
	m.mu.Lock()
	m.touchLocked()
	m.mu.Unlock()
}

func (m *Manager) GetCaptchaInfo(email string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.task.Accounts {
		if a.Email == email {
			return a.CaptchaFile, nil
		}
	}
	return "", fmt.Errorf("email not found")
}
