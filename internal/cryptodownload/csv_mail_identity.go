package cryptodownload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
)

// csvMailIdentity binds one generated mailbox to a full independent browser
// identity: a fixed recipient address, a pinned proxy pool entry and a
// per-identity signer device ID.  OKLink therefore sees every identity as a
// separate user instead of one mailbox reusing the same device fingerprint.
type csvMailIdentity struct {
	// Email is the fixed recipient used for CSV download requests, e.g.
	// okl_a1b2c3d4@aurore.online (catch-all forward domains) or a Gmail
	// +oklN alias.
	Email string
	// ProxyIdx pins this identity to one HTTP proxy pool entry (UA, client
	// hints and TLS fingerprint follow the IP).  -1 means no pool binding.
	ProxyIdx int
	// DeviceID is the OKLink signer device identity used for every request
	// carrying this mailbox, unique per identity.
	DeviceID string
}

const (
	// csvMailIdentityMax caps the generated identity pool so an extreme
	// estimate cannot create an unbounded mailbox list.
	csvMailIdentityMax = 300
	// csvMailIdentityMin guarantees at least one identity per task.
	csvMailIdentityMin = 1
)

// csvMailIdentityCountForRows returns how many email CSV segments a row
// count needs: every OKLink email export is capped at csvMaxRowsPerExport
// rows per request, so segments = ceil(rows / csvMaxRowsPerExport), never
// below 1.
func csvMailIdentityCountForRows(rows int64) int {
	if rows <= 0 {
		return csvMailIdentityMin
	}
	segments := int(math.Ceil(float64(rows) / float64(csvMaxRowsPerExport)))
	if segments < csvMailIdentityMin {
		return csvMailIdentityMin
	}
	if segments > csvMailIdentityMax {
		return csvMailIdentityMax
	}
	return segments
}

// estimateCSVMailIdentityCount estimates how many mailboxes this task needs
// from the data volume: for every chain × enabled export kind it asks OKLink
// for a row count (window count for token, all-time total for transactions)
// and converts it to CSV segments.  A failed count keeps the conservative
// minimum of one mailbox per combination so the task never stalls on an
// empty pool.  The result is clamped to [csvMailIdentityMin,
// csvMailIdentityMax].
func (c *CSVExportClient) estimateCSVMailIdentityCount(ctx context.Context, cfg Config) int {
	start := cfg.CSVStartTime
	end := cfg.CSVEndTime
	if end <= 0 {
		end = time.Now().Unix()
	}
	if start <= 0 {
		start = defaultCSVStartTime
	}
	total := 0
	for _, chain := range cfg.Chains {
		chain = strings.ToUpper(strings.TrimSpace(chain))
		if chain == "" {
			continue
		}
		for _, kind := range csvExportKinds {
			if !csvKindEnabled(kind, cfg.Protocols) {
				continue
			}
			segments := csvMailIdentityMin
			var rows int64
			var err error
			if kind.Sheet == "token" {
				var count int
				count, err = c.countCSV(ctx, cfg, chain, kind, start, end)
				rows = int64(count)
			} else {
				rows, err = c.fetchCSVListTotal(ctx, cfg, chain, kind)
			}
			if err == nil {
				segments = csvMailIdentityCountForRows(rows)
				reportProgress(cfg, "CSV 邮箱估算 %s: %s rows=%d segments=%d", strings.ToUpper(chain), kind.Name, rows, segments)
			} else {
				reportProgress(cfg, "CSV 邮箱估算 %s: %s count 失败（按 1 段计）: %v", strings.ToUpper(chain), kind.Name, err)
			}
			total += segments
		}
	}
	if total < csvMailIdentityMin {
		return csvMailIdentityMin
	}
	if total > csvMailIdentityMax {
		return csvMailIdentityMax
	}
	return total
}

// buildCSVMailIdentities generates count identities on top of baseEmail.
// The destination rules mirror rotateCSVEmailAlias: catch-all forward
// domains (OKLINK_CSV_FORWARD_DOMAINS) get a fixed random-prefix mailbox per
// identity, Gmail gets a +oklN alias, any other domain is returned as-is
// (the pool then degrades to one usable identity).  Identities round-robin
// over the proxy pool (ProxyIdx = i % proxyCount) and each carries its own
// random device ID.
func buildCSVMailIdentities(baseEmail string, count, proxyCount int) []csvMailIdentity {
	identities := make([]csvMailIdentity, 0, count)
	for i := 0; i < count; i++ {
		email := rotatedMailIdentityEmail(baseEmail, i+1)
		proxyIdx := -1
		if proxyCount > 0 {
			proxyIdx = i % proxyCount
		}
		identities = append(identities, csvMailIdentity{
			Email:    email,
			ProxyIdx: proxyIdx,
			DeviceID: randomCSVMailDeviceID(),
		})
	}
	return identities
}

// rotatedMailIdentityEmail derives one fixed identity mailbox from the base
// destination, following the same rules as rotateCSVEmailAlias.
func rotatedMailIdentityEmail(baseEmail string, sequence int) string {
	baseEmail = strings.TrimSpace(baseEmail)
	local, domain, found := strings.Cut(baseEmail, "@")
	if !found || local == "" {
		return baseEmail
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "gmail.com" || domain == "googlemail.com" {
		return fmt.Sprintf("%s+okl%d@%s", local, sequence, domain)
	}
	if csvForwardDomains[domain] {
		return fmt.Sprintf("okl_%s@%s", randomCSVMailHex(4), domain)
	}
	return baseEmail
}

// randomCSVMailDeviceID returns a random UUID v4 string used as the OKLink
// signer device identity, unique per mailbox identity.
func randomCSVMailDeviceID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func randomCSVMailHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%0*x", bytes*2, 0)
	}
	return hex.EncodeToString(buf)
}

// buildMailIdentities estimates the needed mailbox count from the data
// volume and fills c.mailIdentities.  It runs at most once per client (the
// caller guards it with sync.Once); a failed estimate leaves the pool empty
// and the download falls back to the legacy alias rotation.
func (c *CSVExportClient) buildMailIdentities(ctx context.Context, cfg Config) {
	estimate := c.estimateCSVMailIdentityCount(ctx, cfg)
	proxyCount := csvHTTPProxyCount()
	baseEmail := c.baseMailIdentityEmail(cfg)
	c.mailPoolMu.Lock()
	c.mailIdentities = buildCSVMailIdentities(baseEmail, estimate, proxyCount)
	c.identityIdx = -1
	c.mailPoolMu.Unlock()
	reportProgress(cfg, "CSV 邮箱身份池：按数据量估算生成 %d 个邮箱（代理池 %d 个，每邮箱独立 deviceID/指纹）", len(c.mailIdentities), proxyCount)
}

// baseMailIdentityEmail returns the recipient whose domain seeds the
// identity pool: the first pool mailbox wins, otherwise the configured
// single mailbox.
func (c *CSVExportClient) baseMailIdentityEmail(cfg Config) string {
	c.mailPoolMu.Lock()
	defer c.mailPoolMu.Unlock()
	if len(c.mailPool) > 0 {
		return c.mailPool[0].Email
	}
	if email := strings.TrimSpace(cfg.CSVEmail); email != "" {
		return email
	}
	return c.mail.Email
}

// nextMailIdentity advances the identity pool round-robin and returns the
// identity to use for the next request.  With an empty pool it returns the
// zero identity and the caller falls back to legacy alias rotation.
func (c *CSVExportClient) nextMailIdentity() (csvMailIdentity, bool) {
	c.mailPoolMu.Lock()
	defer c.mailPoolMu.Unlock()
	if len(c.mailIdentities) == 0 {
		return csvMailIdentity{}, false
	}
	c.identityIdx = (c.identityIdx + 1) % len(c.mailIdentities)
	c.identityActive = c.mailIdentities[c.identityIdx]
	return c.identityActive, true
}

// currentMailIdentity returns the identity bound to the request currently
// being prepared (set by emailExportAlias/nextMailIdentity), so signing and
// proxy pinning reuse exactly the same device and IP as the payload email.
func (c *CSVExportClient) currentMailIdentity() (csvMailIdentity, bool) {
	c.mailPoolMu.Lock()
	defer c.mailPoolMu.Unlock()
	if c.identityActive.Email == "" {
		return csvMailIdentity{}, false
	}
	return c.identityActive, true
}

// requestPin returns the proxy pin for the active identity (per-IP binding)
// or the configured global pin when no identity is bound.
func (c *CSVExportClient) requestPin() int {
	if identity, ok := c.currentMailIdentity(); ok && identity.ProxyIdx >= 0 {
		return identity.ProxyIdx
	}
	return c.proxyPin
}

// advanceIdentityOnFailure moves the identity pool forward (new mailbox,
// new proxy binding, new device ID) after a mailbox-level failure.
func (c *CSVExportClient) advanceIdentityOnFailure() {
	c.mailPoolMu.Lock()
	defer c.mailPoolMu.Unlock()
	if len(c.mailIdentities) == 0 {
		return
	}
	c.identityIdx = (c.identityIdx + 1) % len(c.mailIdentities)
	c.identityActive = c.mailIdentities[c.identityIdx]
}
