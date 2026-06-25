package dunetools

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultDomain      = "gmail.com"
	defaultIMAPHost    = "imap.gmail.com:993"
	defaultIMAPWait    = 5 * time.Minute
	defaultIMAPPoll    = 10 * time.Second
	defaultInterval    = 90 * time.Second
	maxBatchTotal      = 100
	maxIntervalSeconds = 24 * 60 * 60
	envDomain          = "DUNE_BATCH_DOMAIN"
	envIMAPHost        = "DUNE_BATCH_IMAP_HOST"
	envIMAPUser        = "DUNE_BATCH_IMAP_USER"
	envIMAPPassword    = "DUNE_BATCH_IMAP_PASSWORD"
	envIntervalSeconds = "DUNE_BATCH_INTERVAL_SECONDS"
)

func ResolveRunConfig(req StartRequest) (RunConfig, error) {
	total := req.Total
	mode := req.Mode
	if mode == "" {
		mode = ModeFull
	}
	needsIMAP := mode == ModeFull || mode == ModeRegister
	if needsIMAP && (total <= 0 || total > maxBatchTotal) {
		return RunConfig{}, fmt.Errorf("total must be between 1 and %d", maxBatchTotal)
	}
	domain := firstNonEmpty(req.Domain, os.Getenv(envDomain), defaultDomain)
	imapHost := firstNonEmpty(req.IMAPHost, os.Getenv(envIMAPHost), defaultIMAPHost)
	imapUser := firstNonEmpty(req.IMAPUser, os.Getenv(envIMAPUser))
	imapPassword := firstNonEmpty(req.IMAPPassword, os.Getenv(envIMAPPassword))
	if needsIMAP {
		if imapUser == "" {
			return RunConfig{}, fmt.Errorf("IMAP user is required")
		}
		if imapPassword == "" {
			return RunConfig{}, fmt.Errorf("IMAP password is required")
		}
	}
	interval, err := resolveInterval(req.IntervalSecond)
	if err != nil {
		return RunConfig{}, err
	}
	return RunConfig{
		Domain:   domain,
		Interval: interval,
		Mail: MailConfig{
			Host:      imapHost,
			Username:  imapUser,
			Password:  imapPassword,
			Wait:      defaultIMAPWait,
			PollEvery: defaultIMAPPoll,
		},
	}, nil
}

func resolveInterval(value int) (time.Duration, error) {
	if value < 0 || value > maxIntervalSeconds {
		return 0, fmt.Errorf("interval_seconds must be between 0 and %d", maxIntervalSeconds)
	}
	if value == 0 {
		raw := strings.TrimSpace(os.Getenv(envIntervalSeconds))
		if raw == "" {
			return defaultInterval, nil
		}
		var parsed int
		if _, err := fmt.Sscanf(raw, "%d", &parsed); err != nil {
			return 0, fmt.Errorf("invalid %s: %w", envIntervalSeconds, err)
		}
		if parsed < 0 || parsed > maxIntervalSeconds {
			return 0, fmt.Errorf("%s must be between 0 and %d", envIntervalSeconds, maxIntervalSeconds)
		}
		value = parsed
	}
	return time.Duration(value) * time.Second, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
