package cryptodownload

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/cryptodownload/useragent"
)

func TestCSVMailPoolParseValid(t *testing.T) {
	pool, err := ParseCSVMailPool("a@gmail.com|imap.gmail.com|993|a@gmail.com|secret1\nb@gmail.com|imap.gmail.com|a@gmail.com|secret2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pool) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(pool))
	}
	if pool[0].Email != "a@gmail.com" || pool[0].Host != "imap.gmail.com" || pool[0].Port != 993 || pool[0].Username != "a@gmail.com" || pool[0].Password != "secret1" {
		t.Fatalf("unexpected first entry: %+v", pool[0])
	}
	if pool[1].Port != 993 {
		t.Fatalf("expected default port 993, got %d", pool[1].Port)
	}
}

func TestCSVMailPoolParseSkipsBlankAndComments(t *testing.T) {
	pool, err := ParseCSVMailPool("\n# comment\n\ta@gmail.com|imap.gmail.com|993|a@gmail.com|secret1\t\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pool) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(pool))
	}
}

func TestCSVMailPoolParseRejectsInvalid(t *testing.T) {
	for _, input := range []string{
		"too-few-fields",
		"a@gmail.com|imap.gmail.com|notaport|a@gmail.com|pw",
		"|imap.gmail.com|993|a@gmail.com|pw",
		"a@gmail.com|imap.gmail.com|993|a@gmail.com|",
	} {
		if _, err := ParseCSVMailPool(input); err == nil {
			t.Fatalf("expected error for %q", input)
		}
	}
}

func TestCSVMailPoolParseErrorMasksPassword(t *testing.T) {
	for _, input := range []string{
		"a@gmail.com|imap.gmail.com|993|a@gmail.com|supersecret|extra",
		"a@gmail.com|imap.gmail.com|notaport|a@gmail.com|supersecret",
		"a@gmail.com|imap.gmail.com|a@gmail.com",
	} {
		_, err := ParseCSVMailPool(input)
		if err == nil {
			t.Fatalf("expected error for %q", input)
		}
		if strings.Contains(err.Error(), "supersecret") {
			t.Fatalf("error must not leak password: %v", err)
		}
		if !strings.Contains(err.Error(), "***") {
			t.Fatalf("error should mask sensitive fields: %v", err)
		}
	}
}

func TestCSVProxyPinLocksIndex(t *testing.T) {
	if err := SetCSVHTTPProxyPool("150.241.111.246:6750:fgckrfpy:ino2w2kprrkh\n207.228.7.156:7338:fgckrfpy:ino2w2kprrkh"); err != nil {
		t.Fatal(err)
	}
	defer SetCSVHTTPProxyPool("")
	// A pinned task always uses its own IP: repeated consumption never rotates.
	for i := 0; i < 5; i++ {
		if got := csvHTTPProxyIndexForUse(0); got != 0 {
			t.Fatalf("pin 0 must stay on index 0, got %d", got)
		}
	}
	for i := 0; i < 5; i++ {
		if got := csvHTTPProxyIndexForUse(1); got != 1 {
			t.Fatalf("pin 1 must stay on index 1, got %d", got)
		}
	}
	// Out-of-range pin falls back to no-pool behaviour (-1).
	if got := csvHTTPProxyIndexForUse(99); got != -1 {
		t.Fatalf("out-of-range pin must return -1, got %d", got)
	}
	// Unpinned tasks keep rotating normally (affinity disabled for the test).
	previousWindow, previousUses := csvProxyAffinityWindow, csvProxyAffinityMaxUses
	csvProxyAffinityWindow, csvProxyAffinityMaxUses = 0, 0
	defer func() { csvProxyAffinityWindow, csvProxyAffinityMaxUses = previousWindow, previousUses }()
	first := csvHTTPProxyIndexForUse(-1)
	second := csvHTTPProxyIndexForUse(-1)
	if first == second {
		t.Fatalf("unpinned use must rotate, got %d twice", first)
	}
	// Empty pool: pin also yields -1.
	SetCSVHTTPProxyPool("")
	if got := csvHTTPProxyIndexForUse(0); got != -1 {
		t.Fatalf("pin with empty pool must return -1, got %d", got)
	}
}

func TestCSVProxyPinFailureMarksPinnedIP(t *testing.T) {
	if err := SetCSVHTTPProxyPool("150.241.111.246:6750:fgckrfpy:ino2w2kprrkh\n207.228.7.156:7338:fgckrfpy:ino2w2kprrkh"); err != nil {
		t.Fatal(err)
	}
	defer SetCSVHTTPProxyPool("")
	client := &CSVExportClient{}
	request := httptest.NewRequest(http.MethodPost, "https://www.oklink.com/x", nil)
	request = request.WithContext(context.WithValue(request.Context(), csvProxyIndexKey{}, 1))
	client.noteProxyFailure(request, http.StatusTooManyRequests, nil)
	csvHTTPProxyMu.Lock()
	cooled := csvHTTPProxyOnCooldown("207.228.7.156:7338") // pinned IP (index 1)
	otherCooled := csvHTTPProxyOnCooldown("150.241.111.246:6750")
	csvHTTPProxyMu.Unlock()
	if !cooled {
		t.Fatal("pinned IP must be the one cooled down on failure")
	}
	if otherCooled {
		t.Fatal("unrelated IP must not be cooled")
	}
}

func TestCSVProxyPinExceedsPoolRejected(t *testing.T) {
	dir := t.TempDir()
	settings := defaultGUIPersistedSettings()
	settings.CSVEmail = "a@gmail.com"
	settings.CSVIMAPHost = "imap.gmail.com"
	settings.CSVIMAPUser = "a@gmail.com"
	settings.CSVIMAPPassword = "pw"
	settings.CSVProxyPool = "150.241.111.246:6750:fgckrfpy:ino2w2kprrkh\n207.228.7.156:7338:fgckrfpy:ino2w2kprrkh"
	if err := saveGUISettingsToConfigDir(dir, settings); err != nil {
		t.Fatal(err)
	}
	manager := &GUIManager{configDir: dir}
	request := GUIStartRequest{
		Source: "csv", CSVEmail: "a@gmail.com", CSVIMAPHost: "imap.gmail.com", CSVIMAPPort: 993,
		CSVIMAPUser: "a@gmail.com", CSVIMAPPassword: "pw", CSVProxyPin: 3,
	}
	if _, err := manager.hydrateCSVStartRequest(request); err == nil {
		t.Fatal("pin beyond pool size must be rejected")
	}
	request.CSVProxyPin = 0
	if _, err := manager.hydrateCSVStartRequest(request); err != nil {
		t.Fatalf("auto-rotate pin must pass: %v", err)
	}
	// A request-supplied pool (smaller than the saved one) must be the basis
	// for pin validation too: pin 2 with a 1-entry request pool is rejected.
	request.CSVProxyPin = 2
	request.CSVProxyPool = "150.241.111.246:6750:fgckrfpy:ino2w2kprrkh"
	if _, err := manager.hydrateCSVStartRequest(request); err == nil {
		t.Fatal("pin beyond request-supplied pool must be rejected")
	}
}

func TestCSVProxyPinExceedsPoolAtClientBuild(t *testing.T) {
	// CLI/automation paths bypass API validation: the client must fail
	// loudly at build time instead of silently degrading to the real IP.
	client := NewCSVExportClient(Config{
		CSVHTTPProxyPool: "150.241.111.246:6750:fgckrfpy:ino2w2kprrkh",
		CSVProxyPin:      1, // pool has 1 entry; index 1 is out of range
	})
	if client.poolErr == nil {
		t.Fatal("expected pin-out-of-range error at client build")
	}
	valid := NewCSVExportClient(Config{
		CSVHTTPProxyPool: "150.241.111.246:6750:fgckrfpy:ino2w2kprrkh",
		CSVProxyPin:      0,
	})
	if valid.poolErr != nil {
		t.Fatalf("in-range pin must build cleanly: %v", valid.poolErr)
	}
	SetCSVHTTPProxyPool("")
}

func TestChromeTLSFingerprintPerIndex(t *testing.T) {
	for index := 0; index < 21; index++ {
		version := useragent.ChromeVersionForIndex(index)
		agent := useragent.ChromeByIndex(index)
		if !strings.Contains(agent, "Chrome/"+version+".0.0.0") {
			t.Fatalf("UA version %s must match pool version %s for index %d", agent, version, index)
		}
	}
	// The three platforms of one version share a TLS fingerprint (real
	// browsers behave the same); different versions must differ.
	first := fmt.Sprintf("%v", chromeTLSFingerprintForIndex(0))
	for index := 3; index < 21; index += 3 {
		next := fmt.Sprintf("%v", chromeTLSFingerprintForIndex(index))
		if next == first {
			t.Fatalf("version group %d must not reuse fingerprint %s", index, next)
		}
		first = next
	}
}

func TestCSVProxyFailedCooldownForcesRotation(t *testing.T) {
	if err := SetCSVHTTPProxyPool("150.241.111.246:6750:fgckrfpy:ino2w2kprrkh\n207.228.7.156:7338:fgckrfpy:ino2w2kprrkh"); err != nil {
		t.Fatal(err)
	}
	defer SetCSVHTTPProxyPool("")
	previousWindow, previousUses := csvProxyAffinityWindow, csvProxyAffinityMaxUses
	csvProxyAffinityWindow, csvProxyAffinityMaxUses = time.Hour, 100
	defer func() { csvProxyAffinityWindow, csvProxyAffinityMaxUses = previousWindow, previousUses }()

	first := nextCSVHTTPProxy()
	// Marking the current IP failed must clear the affinity slot so the next
	// request rotates away instead of waiting on a flagged IP.
	MarkCSVHTTPProxyFailed(first.Host)
	for i := 0; i < 4; i++ {
		if got := nextCSVHTTPProxy(); got.String() == first.String() {
			t.Fatalf("failed IP must stay skipped (call %d), got %q", i, got)
		}
	}
}

func TestCSVMailPoolFailureCooldown(t *testing.T) {
	pool, err := ParseCSVMailPool("a@gmail.com|imap.gmail.com|993|a@gmail.com|p1\nb@gmail.com|imap.gmail.com|993|b@gmail.com|p2")
	if err != nil {
		t.Fatal(err)
	}
	client := &CSVExportClient{
		mail:     CSVMailConfig{Email: "main@example.com"},
		mailPool: pool,
	}
	loginErr := &csvMailError{Status: csvMailLoginConfigFailure, Op: "wait", Err: errors.New("login failed")}

	// a fails once -> b.
	if !client.advanceMailOnFailure(loginErr) || client.activeMail().Email != "b@gmail.com" {
		t.Fatalf("expected b after first rotation, got %q", client.activeMail().Email)
	}
	// b fails once -> back to a (a has only 1 failure, not cooled).
	if !client.advanceMailOnFailure(loginErr) || client.activeMail().Email != "a@gmail.com" {
		t.Fatalf("expected a after second rotation, got %q", client.activeMail().Email)
	}
	// a fails a second time -> a cools down, rotation skips it -> b.
	if !client.advanceMailOnFailure(loginErr) || client.activeMail().Email != "b@gmail.com" {
		t.Fatalf("cooled-down a must be skipped, expected b, got %q", client.activeMail().Email)
	}
	// b fails a second time -> both cooling; rotation still advances (fallback).
	if !client.advanceMailOnFailure(loginErr) {
		t.Fatal("expected rotation while all cooling")
	}
}

func TestCSVMailPoolActiveAndRotation(t *testing.T) {
	pool, err := ParseCSVMailPool("a@gmail.com|imap.gmail.com|993|a@gmail.com|p1\nb@gmail.com|imap.gmail.com|993|b@gmail.com|p2")
	if err != nil {
		t.Fatal(err)
	}
	client := &CSVExportClient{mail: CSVMailConfig{Email: "main@example.com"}, mailPool: pool}
	if got := client.activeMail(); got.Email != "a@gmail.com" {
		t.Fatalf("expected first pool mailbox, got %q", got.Email)
	}
	if !client.advanceMailOnFailure(&csvMailError{Status: csvMailLoginConfigFailure, Op: "wait", Err: errors.New("login failed")}) {
		t.Fatal("expected rotation on login failure")
	}
	if got := client.activeMail(); got.Email != "b@gmail.com" {
		t.Fatalf("expected second pool mailbox after rotation, got %q", got.Email)
	}
	// Rotating past the end wraps around.
	if !client.advanceMailOnFailure(errCSVEmailTimeout) {
		t.Fatal("expected rotation on email timeout")
	}
	if got := client.activeMail(); got.Email != "a@gmail.com" {
		t.Fatalf("expected wrap to first mailbox, got %q", got.Email)
	}
}

func TestCSVMailPoolNoRotationForUnrelatedErrorsOrEmptyPool(t *testing.T) {
	client := &CSVExportClient{mail: CSVMailConfig{Email: "main@example.com"}}
	if client.advanceMailOnFailure(errors.New("http 429")) {
		t.Fatal("must not rotate on unrelated error with empty pool")
	}
	if client.advanceMailOnFailure(&csvMailError{Status: csvMailLoginConfigFailure, Op: "wait", Err: errors.New("login failed")}) {
		t.Fatal("must not rotate with empty pool")
	}
	pool, _ := ParseCSVMailPool("a@gmail.com|imap.gmail.com|993|a@gmail.com|p1")
	clientWithPool := &CSVExportClient{mail: CSVMailConfig{Email: "main@example.com"}, mailPool: pool}
	if clientWithPool.advanceMailOnFailure(errors.New("http 429")) {
		t.Fatal("must not rotate on unrelated error")
	}
}

func TestCSVProxyPoolHTTPRotationAndFallback(t *testing.T) {
	if err := SetCSVHTTPProxyPool("http://127.0.0.1:1\nhttp://127.0.0.1:2"); err != nil {
		t.Fatal(err)
	}
	defer SetCSVHTTPProxyPool("")
	// Affinity disabled: strict round-robin on every call.
	previousWindow, previousUses := csvProxyAffinityWindow, csvProxyAffinityMaxUses
	csvProxyAffinityWindow, csvProxyAffinityMaxUses = 0, 0
	defer func() { csvProxyAffinityWindow, csvProxyAffinityMaxUses = previousWindow, previousUses }()
	first := nextCSVHTTPProxy()
	second := nextCSVHTTPProxy()
	third := nextCSVHTTPProxy()
	if first == nil || second == nil || third == nil {
		t.Fatal("expected proxies")
	}
	if first.String() == second.String() || first.String() != third.String() {
		t.Fatalf("expected round-robin rotation, got %s %s %s", first, second, third)
	}
	SetCSVHTTPProxyPool("")
	if nextCSVHTTPProxy() != nil {
		t.Fatal("expected nil proxy with empty pool")
	}
	// Fallback to environment proxies through the request hook.
	request := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := csvHTTPProxyFunc(request); err != nil {
		t.Fatalf("fallback proxy func must not error: %v", err)
	}
}

func TestCSVProxyPoolHTTPAffinity(t *testing.T) {
	if err := SetCSVHTTPProxyPool("http://127.0.0.1:1\nhttp://127.0.0.1:2"); err != nil {
		t.Fatal(err)
	}
	defer SetCSVHTTPProxyPool("")
	previousWindow, previousUses := csvProxyAffinityWindow, csvProxyAffinityMaxUses
	csvProxyAffinityWindow, csvProxyAffinityMaxUses = time.Hour, 3
	defer func() { csvProxyAffinityWindow, csvProxyAffinityMaxUses = previousWindow, previousUses }()
	first := nextCSVHTTPProxy()
	for i := 0; i < 2; i++ {
		if got := nextCSVHTTPProxy(); got.String() != first.String() {
			t.Fatalf("affinity window must reuse proxy, call %d got %s want %s", i, got, first)
		}
	}
	// Use cap reached (3 uses total): next call rotates.
	if got := nextCSVHTTPProxy(); got.String() == first.String() {
		t.Fatal("expected rotation after affinity use cap")
	}
}

func TestCSVProxyPoolRejectsInvalid(t *testing.T) {
	if err := SetCSVHTTPProxyPool("ftp://bad"); err == nil {
		t.Fatal("expected error for ftp scheme")
	}
	if err := SetCSVIMAPProxyPool("http://bad"); err == nil {
		t.Fatal("expected error for non-socks5 IMAP proxy")
	}
}

func TestCSVProxyPoolIMAPRotation(t *testing.T) {
	if err := SetCSVIMAPProxyPool("socks5://127.0.0.1:1080;socks5://127.0.0.1:1081"); err != nil {
		t.Fatal(err)
	}
	defer SetCSVIMAPProxyPool("")
	previousWindow, previousUses := csvProxyAffinityWindow, csvProxyAffinityMaxUses
	csvProxyAffinityWindow, csvProxyAffinityMaxUses = 0, 0
	defer func() { csvProxyAffinityWindow, csvProxyAffinityMaxUses = previousWindow, previousUses }()
	first := nextCSVIMAPProxy()
	second := nextCSVIMAPProxy()
	if first == "" || second == "" || first == second {
		t.Fatalf("expected rotation, got %q %q", first, second)
	}
	SetCSVIMAPProxyPool("")
	if nextCSVIMAPProxy() != "" {
		t.Fatal("expected empty with empty pool")
	}
}

func TestUserAgentClientHints(t *testing.T) {
	chromeUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	brand := useragent.SecCHUABrand(chromeUA)
	if brand == "" || !strings.Contains(brand, "131") {
		t.Fatalf("unexpected brand %q", brand)
	}
	full := useragent.SecCHUAFullVersionList(chromeUA)
	if full == "" || !strings.Contains(full, "131.0.0.0") {
		t.Fatalf("unexpected full version list %q", full)
	}
	if got := useragent.SecCHUAPlatform(chromeUA); got != "Windows" {
		t.Fatalf("expected Windows platform, got %q", got)
	}
	if useragent.IsMobileUA(chromeUA) {
		t.Fatal("desktop UA must not be mobile")
	}
	firefoxUA := "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0"
	if brand := useragent.SecCHUABrand(firefoxUA); brand != "" {
		t.Fatalf("Firefox must not get Sec-CH-UA brand, got %q", brand)
	}
	if full := useragent.SecCHUAFullVersionList(firefoxUA); full != "" {
		t.Fatalf("Firefox must not get full version list, got %q", full)
	}
	if got := useragent.SecCHUAPlatform(firefoxUA); got != "Linux" {
		t.Fatalf("expected Linux platform for Firefox, got %q", got)
	}
	iphoneUA := "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1"
	if !useragent.IsMobileUA(iphoneUA) {
		t.Fatal("iPhone UA must be mobile")
	}
	if got := useragent.SecCHUAPlatform(iphoneUA); got != "iOS" {
		t.Fatalf("expected iOS platform, got %q", got)
	}
}

func TestUserAgentGetChromeAlwaysChrome(t *testing.T) {
	agent := useragent.GetChrome("oklink.com")
	if !strings.Contains(agent, "Chrome/") {
		t.Fatalf("CSV UA must be Chrome (matches TLS fingerprint), got %q", agent)
	}
	if agent != useragent.GetChrome("oklink.com") {
		t.Fatal("expected stable per-host Chrome UA")
	}
}

func TestUserAgentAcceptLanguageStablePerHost(t *testing.T) {
	first := useragent.AcceptLanguage("oklink.com")
	second := useragent.AcceptLanguage("oklink.com")
	if first != second {
		t.Fatalf("Accept-Language must be stable per host, got %q vs %q", first, second)
	}
	if first == "" {
		t.Fatal("expected non-empty Accept-Language")
	}
}

func TestSetCSVUserAgentHeadersRotated(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "https://www.oklink.com/download", nil)
	request = setCSVUserAgentHeaders(request, "https://www.oklink.com", -1)
	agent := request.Header.Get("User-Agent")
	if agent == "" {
		t.Fatal("expected rotated User-Agent")
	}
	if !strings.HasPrefix(agent, "Mozilla/5.0") {
		t.Fatalf("unexpected User-Agent %q", agent)
	}
	if brand := request.Header.Get("Sec-CH-UA"); brand == "" && strings.Contains(agent, "Chrome/") {
		t.Fatalf("Chrome UA must carry Sec-CH-UA, got agent %q", agent)
	}
	if got := request.Header.Get("Accept-Language"); got == "" {
		t.Fatal("expected rotated Accept-Language")
	}
	if got := request.Header.Get("Sec-Fetch-Site"); got != "same-origin" {
		t.Fatalf("expected Sec-Fetch-Site same-origin, got %q", got)
	}
	if got := request.Header.Get("Sec-Fetch-Mode"); got != "cors" {
		t.Fatalf("expected Sec-Fetch-Mode cors, got %q", got)
	}
	if got := request.Header.Get("Sec-Fetch-Dest"); got != "empty" {
		t.Fatalf("expected Sec-Fetch-Dest empty, got %q", got)
	}
	if strings.Contains(agent, "Chrome/") && request.Header.Get("Sec-CH-UA-Full-Version-List") == "" {
		t.Fatal("Chrome UA must carry Sec-CH-UA-Full-Version-List")
	}
}

func TestRotateCSVEmailAliasForwardDomain(t *testing.T) {
	previous := csvForwardDomains
	csvForwardDomains = map[string]bool{"aurore.online": true}
	defer func() { csvForwardDomains = previous }()
	first := rotateCSVEmailAlias("owner@aurore.online")
	second := rotateCSVEmailAlias("owner@aurore.online")
	if first == second {
		t.Fatalf("forward domain must rotate per call, got identical %q", first)
	}
	for _, alias := range []string{first, second} {
		if !strings.HasPrefix(alias, "okl") || !strings.HasSuffix(alias, "@aurore.online") {
			t.Fatalf("unexpected forward-domain alias %q (want okl<hex>@aurore.online)", alias)
		}
	}
	// Unconfigured domains stay unchanged.
	if got := rotateCSVEmailAlias("x@example.com"); got != "x@example.com" {
		t.Fatalf("unconfigured domain must stay unchanged, got %q", got)
	}
	// Gmail still uses +oklN aliases.
	if got := rotateCSVEmailAlias("a@gmail.com"); !strings.Contains(got, "+okl") {
		t.Fatalf("Gmail must use +oklN alias, got %q", got)
	}
}

func TestParseCSVForwardDomains(t *testing.T) {
	domains := parseCSVForwardDomains("aurore.online, Example.COM;foo\tbar\nbaz")
	want := map[string]bool{"aurore.online": true, "example.com": true, "foo": true, "bar": true, "baz": true}
	if len(domains) != len(want) {
		t.Fatalf("expected %d domains, got %d: %v", len(want), len(domains), domains)
	}
	for domain := range want {
		if !domains[domain] {
			t.Fatalf("missing domain %q in %v", domain, domains)
		}
	}
	if got := parseCSVForwardDomains(""); len(got) != 0 {
		t.Fatalf("empty input must yield empty map, got %v", got)
	}
	if got := parseCSVForwardDomains(" ,;\n\t "); len(got) != 0 {
		t.Fatalf("separator-only input must yield empty map, got %v", got)
	}
}

func TestNormalizeCSVProxyEntry(t *testing.T) {
	cases := map[string]string{
		"150.241.111.246:6750:fgckrfpy:ino2w2kprrkh": "http://fgckrfpy:ino2w2kprrkh@150.241.111.246:6750",
		"150.241.111.246:6750":                      "http://150.241.111.246:6750",
		"http://127.0.0.1:7890":                     "http://127.0.0.1:7890",
		"socks5://127.0.0.1:1080":                   "socks5://127.0.0.1:1080",
		"not-a-proxy":                               "not-a-proxy",
	}
	for input, want := range cases {
		if got := normalizeCSVProxyEntry(input); got != want {
			t.Fatalf("normalizeCSVProxyEntry(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSetCSVHTTPProxyPoolBareFormat(t *testing.T) {
	if err := SetCSVHTTPProxyPool("150.241.111.246:6750:fgckrfpy:ino2w2kprrkh\n207.228.7.156:7338:fgckrfpy:ino2w2kprrkh"); err != nil {
		t.Fatal(err)
	}
	defer SetCSVHTTPProxyPool("")
	proxy := nextCSVHTTPProxy()
	if proxy == nil || proxy.Scheme != "http" || proxy.User == nil {
		t.Fatalf("expected authenticated http proxy, got %v", proxy)
	}
	if proxy.Host != "150.241.111.246:6750" {
		t.Fatalf("unexpected host %q", proxy.Host)
	}
}

func TestUserAgentBoundToProxyIP(t *testing.T) {
	if err := SetCSVHTTPProxyPool("150.241.111.246:6750:fgckrfpy:ino2w2kprrkh\n207.228.7.156:7338:fgckrfpy:ino2w2kprrkh"); err != nil {
		t.Fatal(err)
	}
	defer SetCSVHTTPProxyPool("")
	previousWindow, previousUses := csvProxyAffinityWindow, csvProxyAffinityMaxUses
	csvProxyAffinityWindow, csvProxyAffinityMaxUses = 0, 0
	defer func() { csvProxyAffinityWindow, csvProxyAffinityMaxUses = previousWindow, previousUses }()

	request := httptest.NewRequest(http.MethodPost, "https://www.oklink.com/x", nil)
	request = setCSVUserAgentHeaders(request, "https://www.oklink.com", -1)
	firstIndex, firstUA := request.Context().Value(csvProxyIndexKey{}).(int), request.Header.Get("User-Agent")

	request2 := httptest.NewRequest(http.MethodPost, "https://www.oklink.com/x", nil)
	request2 = setCSVUserAgentHeaders(request2, "https://www.oklink.com", -1)
	secondIndex, secondUA := request2.Context().Value(csvProxyIndexKey{}).(int), request2.Header.Get("User-Agent")

	if firstIndex == secondIndex {
		t.Fatalf("expected rotation, got same index %d", firstIndex)
	}
	if firstUA == secondUA {
		t.Fatalf("different proxy IPs must carry different browser identities, both %q", firstUA)
	}
	// Index-based assignment is collision-free and stable.
	if useragent.ChromeByIndex(firstIndex) != firstUA || useragent.ChromeByIndex(secondIndex) != secondUA {
		t.Fatal("per-IP fingerprint must be stable across calls")
	}
}

func TestCSVProxyAffinityBoundaryPreviewsNext(t *testing.T) {
	if err := SetCSVHTTPProxyPool("150.241.111.246:6750:fgckrfpy:ino2w2kprrkh\n207.228.7.156:7338:fgckrfpy:ino2w2kprrkh"); err != nil {
		t.Fatal(err)
	}
	defer SetCSVHTTPProxyPool("")
	previousWindow, previousUses := csvProxyAffinityWindow, csvProxyAffinityMaxUses
	csvProxyAffinityWindow, csvProxyAffinityMaxUses = time.Hour, 3
	defer func() { csvProxyAffinityWindow, csvProxyAffinityMaxUses = previousWindow, previousUses }()

	for i := 0; i < 3; i++ {
		nextCSVHTTPProxy() // exhaust the affinity cap (3 uses)
	}
	// Next request will rotate to the other IP: the fingerprint preview must
	// already reflect it (no one-request IP↔fingerprint mismatch).
	if got := csvActiveHTTPProxyIndex(); got != 1 {
		t.Fatalf("affinity boundary must preview the next IP, got index %d", got)
	}
}

func TestCSVIMAPProxyPoolRejectsUserinfo(t *testing.T) {
	if err := SetCSVIMAPProxyPool("socks5://user:pass@127.0.0.1:1080"); err == nil {
		t.Fatal("expected rejection of authenticated socks5 IMAP entry")
	}
	if err := SetCSVIMAPProxyPool("socks5://127.0.0.1:1080"); err != nil {
		t.Fatalf("unauthenticated socks5 entry must be accepted: %v", err)
	}
	SetCSVIMAPProxyPool("")
}

func TestMaskCSVProxyPoolUserinfoBareFormat(t *testing.T) {
	masked := maskCSVProxyPoolUserinfo("150.241.111.246:6750:fgckrfpy:ino2w2kprrkh")
	if strings.Contains(masked, "fgckrfpy") || strings.Contains(masked, "ino2w2kprrkh") {
		t.Fatalf("bare-format credentials must be masked: %s", masked)
	}
	if !strings.Contains(masked, "***@150.241.111.246:6750") {
		t.Fatalf("expected masked userinfo with host: %s", masked)
	}
	// Passwords containing colons must not leak either.
	maskedColon := maskCSVProxyPoolUserinfo("150.241.111.246:6750:fgckrfpy:ino2w2k:prrkh")
	if strings.Contains(maskedColon, "ino2w2k") || strings.Contains(maskedColon, "prrkh") {
		t.Fatalf("colon-in-password credentials must be masked: %s", maskedColon)
	}
}

func TestMaskCSVProxyPoolUserinfo(t *testing.T) {
	masked := maskCSVProxyPoolUserinfo("http://user:pass@127.0.0.1:7890\nhttp://127.0.0.1:7891")
	if strings.Contains(masked, "pass") {
		t.Fatalf("must mask proxy credentials: %s", masked)
	}
	if !strings.Contains(masked, "***@127.0.0.1") {
		t.Fatalf("expected masked userinfo: %s", masked)
	}
	if !strings.Contains(masked, "http://127.0.0.1:7891") {
		t.Fatalf("plain proxy must stay intact: %s", masked)
	}
}

func TestHandleGUISettingsDoesNotEchoSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("APPDATA", dir)
	settings := defaultGUIPersistedSettings()
	settings.CSVIMAPPassword = "imap-secret"
	settings.CSVMailPool = "a@gmail.com|imap.gmail.com|993|a@gmail.com|pool-secret"
	settings.CSVProxyPool = "http://proxyuser:proxypass@127.0.0.1:7890"
	settings.CSVIMAPProxyPool = "socks5://imapuser:imappass@127.0.0.1:1080"
	if err := saveGUIPersistedSettings(settings); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	response := httptest.NewRecorder()
	handleGUISettings(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, "imap-secret") || strings.Contains(body, "pool-secret") || strings.Contains(body, "proxypass") || strings.Contains(body, "imappass") {
		t.Fatalf("GET /api/settings must not echo secrets: %s", body)
	}
	if !strings.Contains(body, "***@127.0.0.1") {
		t.Fatalf("GET /api/settings must mask proxy credentials: %s", body)
	}

	// POST response must mask both proxy pools too (IMAP pool rejects
	// authenticated entries, so use an unauthenticated one here).
	payload := `{"source":"csv","csvEmail":"a@gmail.com","csvImapHost":"imap.gmail.com","csvImapPort":993,"csvImapUser":"a@gmail.com","csvProxyPool":"http://proxyuser:proxypass@127.0.0.1:7890","csvImapProxyPool":"socks5://127.0.0.1:1080"}`
	postRequest := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(payload))
	postResponse := httptest.NewRecorder()
	handleGUISettings(postResponse, postRequest)
	if postResponse.Code != http.StatusOK {
		t.Fatalf("POST expected 200, got %d: %s", postResponse.Code, postResponse.Body.String())
	}
	postBody := postResponse.Body.String()
	if strings.Contains(postBody, "proxypass") || strings.Contains(postBody, "imappass") {
		t.Fatalf("POST response must mask proxy credentials: %s", postBody)
	}
	if !strings.Contains(postBody, "***@127.0.0.1") {
		t.Fatalf("POST response must mask proxy userinfo: %s", postBody)
	}
}

func TestHandleGUISettingsPostKeepsSecretsWhenBlank(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("APPDATA", dir)
	settings := defaultGUIPersistedSettings()
	settings.CSVIMAPPassword = "saved-pw"
	settings.CSVMailPool = "a@gmail.com|imap.gmail.com|993|a@gmail.com|pool-pw"
	if err := saveGUIPersistedSettings(settings); err != nil {
		t.Fatal(err)
	}

	payload := `{"source":"csv","csvEmail":"a@gmail.com","csvImapHost":"imap.gmail.com","csvImapPort":993,"csvImapUser":"a@gmail.com"}`
	request := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(payload))
	response := httptest.NewRecorder()
	handleGUISettings(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	loaded, err := loadGUIPersistedSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CSVIMAPPassword != "saved-pw" {
		t.Fatalf("password not preserved: %q", loaded.CSVIMAPPassword)
	}
	if !strings.Contains(loaded.CSVMailPool, "pool-pw") {
		t.Fatalf("pool not preserved: %q", loaded.CSVMailPool)
	}
}
