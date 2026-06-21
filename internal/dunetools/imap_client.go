package dunetools

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type RawIMAPMailbox struct {
	DialTimeout time.Duration
}

func (m RawIMAPMailbox) WaitForVerificationLink(ctx context.Context, cfg MailConfig, accountEmail string, since time.Time) (string, error) {
	wait := cfg.Wait
	if wait <= 0 {
		wait = defaultIMAPWait
	}
	poll := cfg.PollEvery
	if poll <= 0 {
		poll = defaultIMAPPoll
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		link, err := m.fetchOnce(ctx, cfg, accountEmail, since)
		if err == nil {
			return link, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("verification email not found for %s before timeout", accountEmail)
		case <-ticker.C:
		}
	}
}

func (m RawIMAPMailbox) fetchOnce(ctx context.Context, cfg MailConfig, accountEmail string, since time.Time) (string, error) {
	dialer := &net.Dialer{Timeout: maxDuration(m.DialTimeout, 15*time.Second)}
	log.Info().Str("host", cfg.Host).Str("email", accountEmail).Msg("imap_connecting")
	conn, err := tls.DialWithDialer(dialer, "tcp", cfg.Host, &tls.Config{ServerName: strings.Split(cfg.Host, ":")[0], MinVersion: tls.VersionTLS12})
	if err != nil {
		log.Warn().Err(err).Str("host", cfg.Host).Msg("imap_dial_failed")
		return "", fmt.Errorf("dial IMAP: %w", err)
	}
	defer conn.Close()
	client := &imapClient{conn: conn, reader: bufio.NewReader(conn), writer: bufio.NewWriter(conn)}
	if _, err := client.readTagged(""); err != nil {
		log.Warn().Err(err).Msg("imap_greeting_failed")
		return "", fmt.Errorf("read IMAP greeting: %w", err)
	}
	if _, err := client.command(`LOGIN %s %s`, quoteIMAP(cfg.Username), quoteIMAP(cfg.Password)); err != nil {
		log.Warn().Err(err).Str("user", cfg.Username).Msg("imap_login_failed")
		return "", fmt.Errorf("IMAP login: %w", err)
	}
	log.Info().Str("user", cfg.Username).Msg("imap_logged_in")
	defer func() { _, _ = client.command("LOGOUT") }()
	if _, err := client.command("SELECT INBOX"); err != nil {
		log.Warn().Err(err).Msg("imap_select_inbox_failed")
		return "", fmt.Errorf("select inbox: %w", err)
	}
	uids, err := client.search(accountEmail, since)
	if err != nil {
		log.Warn().Err(err).Str("email", accountEmail).Msg("imap_search_failed")
		return "", err
	}
	log.Info().Int("uids", len(uids)).Str("email", accountEmail).Msg("imap_search_done")
	sort.Ints(uids)
	for i := len(uids) - 1; i >= 0; i-- {
		resp, err := client.command("UID FETCH %d (BODY.PEEK[])", uids[i])
		if err != nil {
			log.Warn().Err(err).Int("uid", uids[i]).Msg("imap_fetch_failed")
			continue
		}
		// Verify this email is actually TO the expected recipient
		if !emailMatchesRecipient([]byte(resp), accountEmail) {
			log.Info().Int("uid", uids[i]).Str("email", accountEmail).Msg("imap_skip_wrong_recipient")
			continue
		}
		if link, err := ExtractVerificationLink([]byte(resp)); err == nil {
			log.Info().Str("email", accountEmail).Int("uid", uids[i]).Msg("imap_link_found")
			return link, nil
		}
	}
	return "", fmt.Errorf("no Dune verification link in %d candidate messages", len(uids))
}

type imapClient struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	tag    int
}

func (c *imapClient) command(format string, args ...interface{}) (string, error) {
	c.tag++
	tag := fmt.Sprintf("a%04d", c.tag)
	if _, err := fmt.Fprintf(c.writer, tag+" "+format+"\r\n", args...); err != nil {
		return "", fmt.Errorf("write command: %w", err)
	}
	if err := c.writer.Flush(); err != nil {
		return "", fmt.Errorf("flush command: %w", err)
	}
	return c.readTagged(tag)
}

func (c *imapClient) readTagged(tag string) (string, error) {
	var buf bytes.Buffer
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read response: %w", err)
		}
		buf.WriteString(line)
		if size, ok := literalSize(line); ok {
			if _, err := io.CopyN(&buf, c.reader, int64(size)); err != nil {
				return "", fmt.Errorf("read literal: %w", err)
			}
		}
		if tag == "" || strings.HasPrefix(line, tag+" ") {
			text := buf.String()
			if tag != "" && !strings.Contains(line, " OK ") {
				return text, fmt.Errorf("IMAP command failed: %s", strings.TrimSpace(line))
			}
			return text, nil
		}
	}
}

func (c *imapClient) search(accountEmail string, since time.Time) ([]int, error) {
	// Search for emails TO this specific address from Dune, within last 6 hours
	query := fmt.Sprintf(`X-GM-RAW %s`, quoteIMAP(fmt.Sprintf("to:%s from:hello@dune.com newer_than:6h", accountEmail)))
	resp, err := c.command("UID SEARCH " + query)
	if err != nil {
		// Fallback: SINCE + FROM + SUBJECT
		resp, err = c.command("UID SEARCH SINCE %s FROM %s SUBJECT %s", quoteIMAP(since.Format("2-Jan-2006")), quoteIMAP("hello@dune.com"), quoteIMAP("Verify"))
	}
	if err != nil {
		return nil, fmt.Errorf("search verification email: %w", err)
	}
	uids := parseSearchUIDs(resp)
	if len(uids) == 0 {
		return nil, fmt.Errorf("verification email not found")
	}
	return uids, nil
}

var literalRe = regexp.MustCompile(`\{(\d+)\}\r?\n$`)

func literalSize(line string) (int, bool) {
	matches := literalRe.FindStringSubmatch(line)
	if len(matches) != 2 {
		return 0, false
	}
	size, err := strconv.Atoi(matches[1])
	return size, err == nil
}

func parseSearchUIDs(resp string) []int {
	var uids []int
	for _, line := range strings.Split(resp, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "* SEARCH") {
			continue
		}
		for _, field := range strings.Fields(line)[2:] {
			uid, err := strconv.Atoi(field)
			if err == nil {
				uids = append(uids, uid)
			}
		}
	}
	return uids
}

func emailMatchesRecipient(raw []byte, accountEmail string) bool {
	// Simple check: the raw email should contain the recipient address
	return bytes.Contains(bytes.ToLower(raw), []byte(strings.ToLower(accountEmail)))
}

func quoteIMAP(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func maxDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
