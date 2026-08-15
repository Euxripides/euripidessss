package cryptodownload

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CSVMailPoolSeparator separates fields inside one pool entry line.
const CSVMailPoolSeparator = "|"

// A mailbox is cooled down after this many consecutive failures, then
// skipped by rotation until the cooldown elapses.
const (
	csvMailFailureThreshold      = 2
	csvMailCooldownAfterFailures = time.Hour
)

// ParseCSVMailPool parses mail pool entries from multi-line text.  Every
// non-empty, non-comment line has the form
//
//	email|imapHost|imapPort|imapUser|imapPassword
//
// (imapPort is optional, defaulting to 993).  Passwords stay local to the
// process; the parsed pool is never returned through an HTTP response.
func ParseCSVMailPool(text string) ([]CSVMailConfig, error) {
	entries := make([]CSVMailConfig, 0, 2)
	for lineNumber, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, CSVMailPoolSeparator)
		if len(parts) < 4 || len(parts) > 5 {
			return nil, fmt.Errorf("CSV 邮箱池第 %d 行格式错误（应为 邮箱|IMAP主机|IMAP端口|IMAP用户|IMAP密码）: %q", lineNumber+1, sanitiseCSVMailPoolLine(line))
		}
		email := strings.TrimSpace(parts[0])
		host := strings.TrimSpace(parts[1])
		port := 993
		if len(parts) == 5 {
			parsed, err := strconv.Atoi(strings.TrimSpace(parts[2]))
			if err != nil || parsed <= 0 || parsed > 65535 {
				return nil, fmt.Errorf("CSV 邮箱池第 %d 行 IMAP 端口无效: %q", lineNumber+1, sanitiseCSVMailPoolLine(line))
			}
			port = parsed
		}
		user := strings.TrimSpace(parts[len(parts)-2])
		password := strings.TrimSpace(parts[len(parts)-1])
		if email == "" || host == "" || user == "" || password == "" {
			return nil, fmt.Errorf("CSV 邮箱池第 %d 行字段不完整（邮箱/主机/用户/密码均必填）", lineNumber+1)
		}
		entries = append(entries, CSVMailConfig{
			Email:    email,
			Host:     host,
			Port:     port,
			Username: user,
			Password: password,
		})
	}
	return entries, nil
}

// sanitiseCSVMailPoolLine masks everything after email|host of a pool line
// before it is included in an error message: user and password fields stay
// out of logs and HTTP responses even when field counts are wrong.
func sanitiseCSVMailPoolLine(line string) string {
	parts := strings.Split(line, CSVMailPoolSeparator)
	if len(parts) > 2 {
		return strings.Join(parts[:2], CSVMailPoolSeparator) + "|***|***"
	}
	return line
}

// activeMail returns the mailbox currently in use.  With a configured pool
// the pool mailbox (rotated on failure) wins; otherwise the configured
// single mailbox is used.  c.mail is kept in sync so IMAP watchers and
// correlation always use the same identity that requested the CSV.
func (c *CSVExportClient) activeMail() CSVMailConfig {
	c.mailPoolMu.Lock()
	defer c.mailPoolMu.Unlock()
	if len(c.mailPool) == 0 {
		return c.mail
	}
	mail := c.mailPool[c.mailPoolIdx]
	c.mail = mail
	return mail
}

// advanceMailOnFailure rotates to the next pool mailbox when err indicates a
// mailbox-level failure (IMAP login/config failure or email timeout with no
// link).  Failures are remembered per mailbox: after csvMailFailureThreshold
// accumulated failures a mailbox is cooled down for
// csvMailCooldownAfterFailures and skipped by rotation until the cooldown
// elapses (the counter resets when the cooldown is triggered).  It reports
// whether the mailbox was rotated.  With an empty pool or an unrelated error
// it does nothing and returns false.
func (c *CSVExportClient) advanceMailOnFailure(err error) bool {
	if !csvMailFailureNeedsRotation(err) {
		return false
	}
	c.mailPoolMu.Lock()
	defer c.mailPoolMu.Unlock()
	if len(c.mailPool) == 0 {
		return false
	}
	if c.mailFailures == nil {
		c.mailFailures = map[int]int{}
	}
	if c.mailCooldownUntil == nil {
		c.mailCooldownUntil = map[int]time.Time{}
	}
	now := time.Now()
	c.mailFailures[c.mailPoolIdx]++
	if c.mailFailures[c.mailPoolIdx] >= csvMailFailureThreshold {
		c.mailCooldownUntil[c.mailPoolIdx] = now.Add(csvMailCooldownAfterFailures)
		c.mailFailures[c.mailPoolIdx] = 0
	}
	for attempts := 0; attempts < len(c.mailPool); attempts++ {
		c.mailPoolIdx = (c.mailPoolIdx + 1) % len(c.mailPool)
		if until, cooling := c.mailCooldownUntil[c.mailPoolIdx]; !cooling || now.After(until) {
			delete(c.mailCooldownUntil, c.mailPoolIdx)
			c.mail = c.mailPool[c.mailPoolIdx]
			return true
		}
	}
	// Every mailbox is cooling down: fall back to the next one anyway.
	c.mailPoolIdx = (c.mailPoolIdx + 1) % len(c.mailPool)
	c.mail = c.mailPool[c.mailPoolIdx]
	return true
}

// csvMailFailureNeedsRotation reports whether err is a mailbox-level failure
// worth rotating away from the current mailbox.  Network/429/signature
// failures are deliberately excluded: they are not mailbox problems.
func csvMailFailureNeedsRotation(err error) bool {
	if err == nil {
		return false
	}
	var mailErr *csvMailError
	if errors.As(err, &mailErr) && mailErr.Status == csvMailLoginConfigFailure {
		return true
	}
	return isCSVEmailNoLinkTimeout(err)
}
