package cryptodownload

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Rotating HTTP and IMAP proxy pools.  Pools are process-wide and empty by
// default, in which case existing behaviour (environment-driven proxies)
// is preserved unchanged.

var (
	csvHTTPProxyMu    sync.Mutex
	csvHTTPProxies    []*url.URL
	csvHTTPProxyIndex atomic.Uint64
	// IP affinity window: a real browser keeps one IP for a while; rotating
	// on every connection looks more suspicious than staying put.  Within the
	// window the same proxy is reused.
	csvHTTPProxyCurrent      *url.URL
	csvHTTPProxyCurrentIdx   int
	csvHTTPProxyCurrentSince time.Time
	csvHTTPProxyCurrentUses  int

	// csvHTTPClients pins one HTTP client (and thus one proxy + TLS
	// fingerprint) per pool entry; rebuilt whenever the pool changes.
	csvHTTPClients []*http.Client

	csvIMAPProxyMu    sync.Mutex
	csvIMAPProxies    []string
	csvIMAPProxyIndex atomic.Uint64
	csvIMAPProxyCurrent      string
	csvIMAPProxyCurrentSince time.Time
	csvIMAPProxyCurrentUses  int

	// csvHTTPProxyCooldown marks proxy IPs that failed recently (429/403/
	// connection errors); rotating selects skip them until the cooldown ends.
	// Guarded by csvHTTPProxyMu (single lock order: ProxyMu → cooldown map).
	csvHTTPProxyCooldown         = map[string]time.Time{}
	csvHTTPProxyCooldownDuration = 10 * time.Minute

	// Health check bookkeeping: consecutive TCP failures put an IP on the
	// cooldown list so rotation skips dead proxies.
	csvProxyHealthMu           sync.Mutex
	csvProxyHealthFailures     = map[string]int{}
	csvProxyHealthCheckOnce    sync.Once
	csvProxyHealthCheckInterval = 5 * time.Minute
	csvProxyHealthFailThreshold = 2
)

// Proxy affinity window before rotating to the next pool entry.
// Overridable via OKLINK_CSV_PROXY_AFFINITY (seconds); 0 disables affinity.
var (
	csvProxyAffinityWindow  = 30 * time.Second
	csvProxyAffinityMaxUses = 12
)

func init() {
	if raw := strings.TrimSpace(os.Getenv("OKLINK_CSV_PROXY_AFFINITY")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			csvProxyAffinityWindow = time.Duration(seconds) * time.Second
		}
	}
	// OKLINK_CSV_PROXY_COOLDOWN (seconds) sets how long a failed proxy IP
	// stays out of rotation; 0 disables the cooldown list.
	if raw := strings.TrimSpace(os.Getenv("OKLINK_CSV_PROXY_COOLDOWN")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			csvHTTPProxyCooldownDuration = time.Duration(seconds) * time.Second
		}
	}
	// OKLINK_CSV_PROXY_HEALTH_INTERVAL (seconds) tunes the proxy health check
	// cadence; 0 disables periodic probing.
	if raw := strings.TrimSpace(os.Getenv("OKLINK_CSV_PROXY_HEALTH_INTERVAL")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			csvProxyHealthCheckInterval = time.Duration(seconds) * time.Second
		}
	}
}

// normalizeCSVProxyEntry converts bare "ip:port:user:pass" (or "ip:port")
// proxy entries into http:// URLs.  Entries that already carry a scheme are
// returned unchanged.  Only IPv4 bare entries are supported (the password may
// contain colons).
func normalizeCSVProxyEntry(entry string) string {
	entry = strings.TrimSpace(entry)
	if entry == "" || strings.Contains(entry, "://") {
		return entry
	}
	parts := strings.SplitN(entry, ":", 3)
	switch len(parts) {
	case 2: // ip:port
		return "http://" + entry
	case 3: // ip:port:user:pass (password may contain colons)
		return "http://" + parts[2] + "@" + parts[0] + ":" + parts[1]
	default:
		return entry
	}
}

// validateCSVHTTPProxyPool validates proxy pool entries without applying them.
func validateCSVHTTPProxyPool(raw string) error {
	entries, err := parseCSVProxyList(raw, "HTTP")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		normalized := normalizeCSVProxyEntry(entry)
		u, err := url.Parse(normalized)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
			return fmt.Errorf("CSV HTTP 代理池包含无效地址: %q", sanitiseCSVProxyEntry(entry))
		}
	}
	return nil
}

// sanitiseCSVProxyEntry masks any userinfo in an entry before it appears in
// an error message.  Unparseable entries are fully masked.
func sanitiseCSVProxyEntry(entry string) string {
	u, err := url.Parse(normalizeCSVProxyEntry(entry))
	if err != nil {
		return "***"
	}
	if u.User != nil {
		return u.Scheme + "://***@" + u.Host
	}
	return entry
}

// SetCSVHTTPProxyPool replaces the HTTP proxy pool.  Entries may be
// http://, https:// or socks5:// URLs or bare "ip:port[:user:pass]", one per
// line or separated by ";".  An empty input clears the pool (environment
// proxies apply again).
func SetCSVHTTPProxyPool(raw string) error {
	if err := validateCSVHTTPProxyPool(raw); err != nil {
		return err
	}
	entries, _ := parseCSVProxyList(raw, "HTTP")
	parsed := make([]*url.URL, 0, len(entries))
	for _, entry := range entries {
		u, _ := url.Parse(normalizeCSVProxyEntry(entry))
		parsed = append(parsed, u)
	}
	csvHTTPProxyMu.Lock()
	csvHTTPProxies = parsed
	csvHTTPProxyIndex.Store(0)
	csvHTTPProxyCurrent = nil
	csvHTTPProxyCurrentIdx = -1
	csvHTTPProxyCurrentSince = time.Time{}
	csvHTTPProxyCurrentUses = 0
	csvHTTPProxyMu.Unlock()
	// Rebuild per-IP clients (one proxy + TLS fingerprint per entry).
	csvHTTPProxyMu.Lock()
	csvHTTPClients = nil
	if len(parsed) > 0 {
		csvHTTPClients = newCSVPerProxyClients(parsed, 0)
	}
	csvHTTPProxyMu.Unlock()
	// A new pool starts with a clean slate: drop failure cooldowns and
	// health-check counters from the previous pool.
	csvHTTPProxyMu.Lock()
	csvHTTPProxyCooldown = map[string]time.Time{}
	csvHTTPProxyMu.Unlock()
	csvProxyHealthMu.Lock()
	csvProxyHealthFailures = map[string]int{}
	csvProxyHealthMu.Unlock()
	if len(parsed) > 0 {
		startCSVProxyHealthCheck()
	}
	return nil
}

// startCSVProxyHealthCheck launches the periodic TCP liveness probe (once per
// process).  Proxies that fail csvProxyHealthFailThreshold consecutive probes
// enter the failure cooldown list so rotation skips them.
func startCSVProxyHealthCheck() {
	csvProxyHealthCheckOnce.Do(func() {
		if csvProxyHealthCheckInterval <= 0 {
			return
		}
		go func() {
			ticker := time.NewTicker(csvProxyHealthCheckInterval)
			defer ticker.Stop()
			for range ticker.C {
				csvProbeHTTPProxies()
			}
		}()
	})
}

func csvProbeHTTPProxies() {
	csvHTTPProxyMu.Lock()
	proxies := append([]*url.URL(nil), csvHTTPProxies...)
	csvHTTPProxyMu.Unlock()
	for _, proxy := range proxies {
		host := proxy.Host
		conn, err := net.DialTimeout("tcp", host, 3*time.Second)
		if err == nil {
			_ = conn.Close()
			csvProxyHealthMu.Lock()
			csvProxyHealthFailures[host] = 0
			csvProxyHealthMu.Unlock()
			continue
		}
		csvProxyHealthMu.Lock()
		csvProxyHealthFailures[host]++
		failed := csvProxyHealthFailures[host] >= csvProxyHealthFailThreshold
		if failed {
			csvProxyHealthFailures[host] = 0
		}
		csvProxyHealthMu.Unlock()
		if failed {
			MarkCSVHTTPProxyFailed(host)
		}
	}
}

// MarkCSVHTTPProxyFailed records a failure for the given proxy IP: it enters
// the cooldown list and, if it is the current affinity proxy, the affinity
// slot is cleared so the next request rotates to another IP (429/403 means
// the IP itself is flagged; waiting on the same IP is counterproductive).
func MarkCSVHTTPProxyFailed(host string) {
	host = strings.TrimSpace(host)
	if host == "" {
		return
	}
	csvHTTPProxyMu.Lock()
	csvHTTPProxyCooldown[host] = time.Now().Add(csvHTTPProxyCooldownDuration)
	if csvHTTPProxyCurrent != nil && csvHTTPProxyCurrent.Host == host {
		csvHTTPProxyCurrent = nil
	}
	csvHTTPProxyMu.Unlock()
}

// csvHTTPProxyOnCooldown reports whether the proxy IP is cooling down,
// expiring stale entries.  Caller must hold csvHTTPProxyMu.
func csvHTTPProxyOnCooldown(host string) bool {
	until, cooling := csvHTTPProxyCooldown[host]
	if cooling && !time.Now().After(until) {
		return true
	}
	if cooling {
		delete(csvHTTPProxyCooldown, host)
	}
	return false
}

// csvHTTPProxyIndexForUse returns the pool index this request should use
// (consuming the affinity counter or rotating, skipping cooled-down IPs).
// A non-negative pin locks the request to one pool entry (per-IP task mode);
// -1 means no pool configured.
func csvHTTPProxyIndexForUse(pin int) int {
	if pin >= 0 {
		csvHTTPProxyMu.Lock()
		defer csvHTTPProxyMu.Unlock()
		if pin < len(csvHTTPProxies) {
			return pin
		}
		return -1
	}
	csvHTTPProxyMu.Lock()
	defer csvHTTPProxyMu.Unlock()
	if len(csvHTTPProxies) == 0 {
		return -1
	}
	now := time.Now()
	if csvHTTPProxyCurrent != nil &&
		now.Sub(csvHTTPProxyCurrentSince) < csvProxyAffinityWindow &&
		csvHTTPProxyCurrentUses < csvProxyAffinityMaxUses {
		csvHTTPProxyCurrentUses++
		return csvHTTPProxyCurrentIdx
	}
	index := int(csvHTTPProxyIndex.Add(1)-1) % len(csvHTTPProxies)
	for attempts := 0; attempts < len(csvHTTPProxies); attempts++ {
		candidate := csvHTTPProxies[index]
		if !csvHTTPProxyOnCooldown(candidate.Host) {
			csvHTTPProxyCurrent = candidate
			csvHTTPProxyCurrentIdx = index
			csvHTTPProxyCurrentSince = now
			csvHTTPProxyCurrentUses = 1
			return index
		}
		index = (index + 1) % len(csvHTTPProxies)
	}
	// Every IP is cooling down: fall back to the first candidate.
	csvHTTPProxyCurrent = csvHTTPProxies[index]
	csvHTTPProxyCurrentIdx = index
	csvHTTPProxyCurrentSince = now
	csvHTTPProxyCurrentUses = 1
	return index
}

// csvHTTPProxyByIndex returns the pool entry for an index, or nil when out
// of range or the pool is empty.
func csvHTTPProxyByIndex(index int) *url.URL {
	csvHTTPProxyMu.Lock()
	defer csvHTTPProxyMu.Unlock()
	if index < 0 || index >= len(csvHTTPProxies) {
		return nil
	}
	return csvHTTPProxies[index]
}

// csvHTTPClientForIndex returns the per-IP HTTP client for a pool index, or
// nil when out of range.
func csvHTTPClientForIndex(index int) *http.Client {
	csvHTTPProxyMu.Lock()
	defer csvHTTPProxyMu.Unlock()
	if index < 0 || index >= len(csvHTTPClients) {
		return nil
	}
	return csvHTTPClients[index]
}

// nextCSVHTTPProxy returns the proxy in use, honouring the IP affinity
// window and the failure cooldown list.  Empty pool returns nil.
func nextCSVHTTPProxy() *url.URL {
	index := csvHTTPProxyIndexForUse(-1)
	if index < 0 {
		return nil
	}
	csvHTTPProxyMu.Lock()
	defer csvHTTPProxyMu.Unlock()
	return csvHTTPProxies[index]
}

// csvActiveHTTPProxyIndex mirrors csvHTTPProxyIndexForUse's rotation decision
// (without consuming counters) and returns the pool index of the proxy the
// next request will use, or -1 with an empty pool.
func csvActiveHTTPProxyIndex() int {
	csvHTTPProxyMu.Lock()
	defer csvHTTPProxyMu.Unlock()
	if len(csvHTTPProxies) == 0 {
		return -1
	}
	now := time.Now()
	if csvHTTPProxyCurrent != nil &&
		now.Sub(csvHTTPProxyCurrentSince) < csvProxyAffinityWindow &&
		csvHTTPProxyCurrentUses < csvProxyAffinityMaxUses {
		return csvHTTPProxyCurrentIdx
	}
	return int(csvHTTPProxyIndex.Load() % uint64(len(csvHTTPProxies)))
}

// csvCurrentHTTPProxyHost returns the proxy IP the current request actually
// used (nil-proxy = ""), without any preview/rotation logic.  Used by the
// failure notifier so the IP being cooled is exactly the one that failed.
func csvCurrentHTTPProxyHost() string {
	csvHTTPProxyMu.Lock()
	defer csvHTTPProxyMu.Unlock()
	if csvHTTPProxyCurrent == nil {
		return ""
	}
	return csvHTTPProxyCurrent.Host
}

// csvActiveHTTPProxyKey returns the identity of the proxy currently in use
// (or about to be used when the pool has not rotated yet; nil-pool = ""), for
// binding per-IP browser fingerprints.  It mirrors the affinity-window
// rotation decision so the fingerprint always matches the IP that actually
// carries the request.
func csvActiveHTTPProxyKey() string {
	csvHTTPProxyMu.Lock()
	defer csvHTTPProxyMu.Unlock()
	if len(csvHTTPProxies) == 0 {
		return ""
	}
	now := time.Now()
	if csvHTTPProxyCurrent != nil &&
		now.Sub(csvHTTPProxyCurrentSince) < csvProxyAffinityWindow &&
		csvHTTPProxyCurrentUses < csvProxyAffinityMaxUses {
		return csvHTTPProxyCurrent.Host
	}
	index := int(csvHTTPProxyIndex.Load()) % len(csvHTTPProxies)
	return csvHTTPProxies[index].Host
}

// csvHTTPProxyFunc rotates proxies per request when a pool is configured,
// otherwise falls back to environment proxies.
func csvHTTPProxyFunc(request *http.Request) (*url.URL, error) {
	if proxy := nextCSVHTTPProxy(); proxy != nil {
		return proxy, nil
	}
	return http.ProxyFromEnvironment(request)
}

// validateCSVIMAPProxyPool validates IMAP SOCKS5 proxy entries without applying them.
func validateCSVIMAPProxyPool(raw string) error {
	entries, err := parseCSVProxyList(raw, "IMAP")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry, "socks5://") {
			return fmt.Errorf("CSV IMAP 代理池仅支持 socks5:// 地址: %q", sanitiseCSVProxyEntry(entry))
		}
		if u, err := url.Parse(entry); err == nil && u.User != nil {
			return fmt.Errorf("CSV IMAP 代理池不支持带认证的 socks5 地址: %q", "socks5://***@"+u.Host)
		}
	}
	return nil
}

// SetCSVIMAPProxyPool replaces the IMAP SOCKS5 proxy pool.  Entries must be
// socks5://host:port URLs, one per line or separated by ";".  An empty input
// clears the pool (OKLINK_IMAP_PROXY / ALL_PROXY apply again).
func SetCSVIMAPProxyPool(raw string) error {
	if err := validateCSVIMAPProxyPool(raw); err != nil {
		return err
	}
	entries, _ := parseCSVProxyList(raw, "IMAP")
	normalised := make([]string, 0, len(entries))
	for _, entry := range entries {
		normalised = append(normalised, strings.TrimPrefix(entry, "socks5://"))
	}
	csvIMAPProxyMu.Lock()
	csvIMAPProxies = normalised
	csvIMAPProxyIndex.Store(0)
	csvIMAPProxyCurrent = ""
	csvIMAPProxyCurrentSince = time.Time{}
	csvIMAPProxyCurrentUses = 0
	csvIMAPProxyMu.Unlock()
	return nil
}

// nextCSVIMAPProxy returns the SOCKS5 address in use, honouring the IP
// affinity window, or "" when the pool is empty.
func nextCSVIMAPProxy() string {
	csvIMAPProxyMu.Lock()
	defer csvIMAPProxyMu.Unlock()
	if len(csvIMAPProxies) == 0 {
		return ""
	}
	now := time.Now()
	if csvIMAPProxyCurrent != "" &&
		now.Sub(csvIMAPProxyCurrentSince) < csvProxyAffinityWindow &&
		csvIMAPProxyCurrentUses < csvProxyAffinityMaxUses {
		csvIMAPProxyCurrentUses++
		return csvIMAPProxyCurrent
	}
	index := int(csvIMAPProxyIndex.Add(1)-1) % len(csvIMAPProxies)
	csvIMAPProxyCurrent = csvIMAPProxies[index]
	csvIMAPProxyCurrentSince = now
	csvIMAPProxyCurrentUses = 1
	return csvIMAPProxyCurrent
}

func parseCSVProxyList(raw, kind string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var entries []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ';' || r == ',' }) {
		line := strings.TrimSpace(part)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(entries) >= 64 {
			return nil, fmt.Errorf("CSV %s 代理池最多支持 64 个代理", kind)
		}
		entries = append(entries, line)
	}
	return entries, nil
}

// maskCSVProxyPoolUserinfo masks userinfo credentials embedded in proxy URLs
// (http://user:pass@host:port) before the pool text leaves the process.
func maskCSVProxyPoolUserinfo(raw string) string {
	entries, err := parseCSVProxyList(raw, "HTTP")
	if err != nil {
		// Never echo the raw pool text back: it may contain credentials.
		return "***"
	}
	if len(entries) == 0 {
		return raw
	}
	masked := make([]string, 0, len(entries))
	for _, entry := range entries {
		u, err := url.Parse(normalizeCSVProxyEntry(entry))
		if err == nil && u.User != nil {
			masked = append(masked, u.Scheme+"://***@"+u.Host)
		} else if err == nil {
			masked = append(masked, u.String())
		} else {
			// Never echo an unparseable entry back verbatim: it may contain
			// embedded credentials.
			masked = append(masked, "***")
		}
	}
	return strings.Join(masked, "\n")
}

// csvIMAPSOCKS5Addr prefers the configured IMAP proxy pool, then falls back
// to OKLINK_IMAP_PROXY / ALL_PROXY environment variables.
func csvIMAPSOCKS5Addr() string {
	if pooled := nextCSVIMAPProxy(); pooled != "" {
		return pooled
	}
	raw := firstNonEmpty(
		strings.TrimSpace(os.Getenv("OKLINK_IMAP_PROXY")),
		strings.TrimSpace(os.Getenv("ALL_PROXY")),
	)
	if raw == "" || !strings.HasPrefix(raw, "socks5://") {
		return ""
	}
	return strings.TrimPrefix(raw, "socks5://")
}
