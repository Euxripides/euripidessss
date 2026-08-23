package cryptodownload

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCSVMailIdentityCountForRows(t *testing.T) {
	cases := []struct {
		rows int64
		want int
	}{
		{0, 1},
		{-5, 1},
		{1, 1},
		{csvMaxRowsPerExport - 1, 1},
		{csvMaxRowsPerExport, 1},
		{csvMaxRowsPerExport + 1, 2},
		{2 * csvMaxRowsPerExport, 2},
		{5*csvMaxRowsPerExport + 1, 6},
		{int64(csvMailIdentityMax) * csvMaxRowsPerExport, csvMailIdentityMax},
		{int64(csvMailIdentityMax) * csvMaxRowsPerExport * 10, csvMailIdentityMax},
	}
	for _, tc := range cases {
		if got := csvMailIdentityCountForRows(tc.rows); got != tc.want {
			t.Errorf("csvMailIdentityCountForRows(%d) = %d, want %d", tc.rows, got, tc.want)
		}
	}
}

func TestBuildCSVMailIdentitiesForwardDomain(t *testing.T) {
	csvForwardDomains["aurore.online"] = true
	defer delete(csvForwardDomains, "aurore.online")

	identities := buildCSVMailIdentities("pool@aurore.online", 6, 4)
	if len(identities) != 6 {
		t.Fatalf("expected 6 identities, got %d", len(identities))
	}
	pattern := regexp.MustCompile(`^okl_[0-9a-f]{8}@aurore\.online$`)
	seen := map[string]bool{}
	seenDevices := map[string]bool{}
	for i, identity := range identities {
		if !pattern.MatchString(identity.Email) {
			t.Fatalf("identity %d email %q does not match forward-domain pattern", i, identity.Email)
		}
		if seen[identity.Email] {
			t.Fatalf("identity %d reuses mailbox %q", i, identity.Email)
		}
		seen[identity.Email] = true
		if identity.ProxyIdx != i%4 {
			t.Fatalf("identity %d proxy idx = %d, want %d", i, identity.ProxyIdx, i%4)
		}
		if identity.DeviceID == "" {
			t.Fatalf("identity %d has empty device ID", i)
		}
		if seenDevices[identity.DeviceID] {
			t.Fatalf("identity %d reuses device ID %q", i, identity.DeviceID)
		}
		seenDevices[identity.DeviceID] = true
	}
}

func TestBuildCSVMailIdentitiesGmailAlias(t *testing.T) {
	identities := buildCSVMailIdentities("user@gmail.com", 3, 0)
	if len(identities) != 3 {
		t.Fatalf("expected 3 identities, got %d", len(identities))
	}
	for i, identity := range identities {
		want := "user+okl" + strconv.Itoa(i+1) + "@gmail.com"
		if identity.Email != want {
			t.Fatalf("identity %d email = %q, want %q", i, identity.Email, want)
		}
		if identity.ProxyIdx != -1 {
			t.Fatalf("identity %d proxy idx = %d, want -1 without a pool", i, identity.ProxyIdx)
		}
	}
}

func TestBuildCSVMailIdentitiesOtherDomainUnchanged(t *testing.T) {
	identities := buildCSVMailIdentities("admin@example.org", 5, 0)
	for i, identity := range identities {
		if identity.Email != "admin@example.org" {
			t.Fatalf("identity %d email = %q, want unchanged base email", i, identity.Email)
		}
	}
}

func TestCSVMailIdentityRoundRobin(t *testing.T) {
	csvForwardDomains["aurore.online"] = true
	defer delete(csvForwardDomains, "aurore.online")

	client := &CSVExportClient{
		mailIdentities: buildCSVMailIdentities("pool@aurore.online", 3, 1),
		identityIdx:    -1,
	}
	first, ok := client.nextMailIdentity()
	if !ok {
		t.Fatal("expected identity from non-empty pool")
	}
	if client.identityIdx != 0 {
		t.Fatalf("identity idx = %d, want 0", client.identityIdx)
	}
	for i := 1; i <= 5; i++ {
		identity, _ := client.nextMailIdentity()
		wantIdx := i % 3
		if client.identityIdx != wantIdx {
			t.Fatalf("round %d identity idx = %d, want %d", i, client.identityIdx, wantIdx)
		}
		if identity.Email != client.mailIdentities[wantIdx].Email {
			t.Fatalf("round %d returned %q, want %q", i, identity.Email, client.mailIdentities[wantIdx].Email)
		}
	}
	if _, ok := client.currentMailIdentity(); !ok {
		t.Fatal("currentMailIdentity should report the consumed identity")
	}
	if _, ok := (&CSVExportClient{}).nextMailIdentity(); ok {
		t.Fatal("empty pool must not report an identity")
	}
	if identity, ok := client.currentMailIdentity(); !ok || identity.DeviceID == "" {
		t.Fatal("current identity must carry a device ID")
	}
	if got := first.ProxyIdx; got != 0 {
		t.Fatalf("proxy idx with 1-entry pool = %d, want 0", got)
	}
}

func TestEmailExportAliasConsumesIdentityPool(t *testing.T) {
	csvForwardDomains["aurore.online"] = true
	defer delete(csvForwardDomains, "aurore.online")

	client := &CSVExportClient{
		mail:           CSVMailConfig{Email: "fallback@gmail.com"},
		mailIdentities: buildCSVMailIdentities("pool@aurore.online", 2, 1),
		identityIdx:    -1,
	}
	first := client.emailExportAlias("ETH", "transactions")
	if !strings.HasSuffix(first, "@aurore.online") {
		t.Fatalf("expected identity mailbox, got %q", first)
	}
	second := client.emailExportAlias("ETH", "transactions")
	if first == second {
		t.Fatalf("expected rotation to a second mailbox, both %q", first)
	}
	identity, ok := client.currentMailIdentity()
	if !ok || identity.Email != second {
		t.Fatalf("active identity = %q (ok=%v), want %q", identity.Email, ok, second)
	}
	// requestPin follows the identity's proxy binding.
	client.proxyPin = 0
	if pin := client.requestPin(); pin != identity.ProxyIdx {
		t.Fatalf("requestPin = %d, want identity proxy %d", pin, identity.ProxyIdx)
	}
}

func TestEmailExportAliasFallsBackWithoutPool(t *testing.T) {
	client := &CSVExportClient{
		mail: CSVMailConfig{Email: "user@gmail.com"},
	}
	alias := client.emailExportAlias("ETH", "transactions")
	if !strings.HasPrefix(alias, "user+okl") || !strings.HasSuffix(alias, "@gmail.com") {
		t.Fatalf("expected gmail +oklN alias fallback, got %q", alias)
	}
}

func TestAdvanceMailOnFailureAdvancesIdentity(t *testing.T) {
	csvForwardDomains["aurore.online"] = true
	defer delete(csvForwardDomains, "aurore.online")

	client := &CSVExportClient{
		mail:           CSVMailConfig{Email: "pool@aurore.online"},
		mailIdentities: buildCSVMailIdentities("pool@aurore.online", 3, 1),
		identityIdx:    -1,
	}
	before, _ := client.nextMailIdentity()
	timeout := &csvMailError{Status: csvMailNotArrived, Op: "wait", Err: errCSVEmailTimeout}
	if !client.advanceMailOnFailure(timeout) {
		t.Fatal("expected rotation on email timeout")
	}
	after, ok := client.currentMailIdentity()
	if !ok {
		t.Fatal("expected an active identity after rotation")
	}
	if after.Email == before.Email {
		t.Fatalf("identity did not advance: %q", after.Email)
	}
	if client.advanceMailOnFailure(nil) {
		t.Fatal("nil error must not rotate")
	}
	if client.advanceMailOnFailure(errors.New("generic error")) {
		t.Fatal("generic error must not rotate")
	}
	// Empty pool: rotation must still report false.
	empty := &CSVExportClient{}
	if empty.advanceMailOnFailure(timeout) {
		t.Fatal("empty pools must not report rotation")
	}
}

func TestCSVEmailWaitTimeoutForAttempt(t *testing.T) {
	token := csvExportKind{Name: "token_transfers", Sheet: "token"}
	tx := csvExportKind{Name: "transactions", Sheet: "transaction"}
	if got := csvEmailWaitTimeoutForAttempt(token, 0); got != csvTokenEmailFastFailTimeout {
		t.Fatalf("token retry 0 = %v, want fast-fail %v", got, csvTokenEmailFastFailTimeout)
	}
	if got := csvEmailWaitTimeoutForAttempt(token, 1); got != 15*time.Minute {
		t.Fatalf("token retry 1 = %v, want 15m", got)
	}
	if got := csvEmailWaitTimeoutForAttempt(token, 2); got != 20*time.Minute {
		t.Fatalf("token retry 2 = %v, want 20m", got)
	}
	if got := csvEmailWaitTimeoutForAttempt(token, 3); got != csvTokenEmailWaitTimeout {
		t.Fatalf("token retry 3 = %v, want last-resort %v", got, csvTokenEmailWaitTimeout)
	}
	if got := csvEmailWaitTimeoutForAttempt(token, 9); got != csvTokenEmailWaitTimeout {
		t.Fatalf("token retry 9 = %v, want capped %v", got, csvTokenEmailWaitTimeout)
	}
	for retry := 0; retry < 5; retry++ {
		if got := csvEmailWaitTimeoutForAttempt(tx, retry); got != csvEmailWaitTimeout {
			t.Fatalf("transactions retry %d = %v, want %v", retry, got, csvEmailWaitTimeout)
		}
	}
}

func TestCSVSegmentRetriesForKind(t *testing.T) {
	token := csvExportKind{Name: "token_transfers", Sheet: "token"}
	tx := csvExportKind{Name: "transactions", Sheet: "transaction"}
	if got := csvSegmentRetriesForKind(token); got != csvTokenMaxSegmentRetries {
		t.Fatalf("token retries = %d, want %d", got, csvTokenMaxSegmentRetries)
	}
	if got := csvSegmentRetriesForKind(tx); got != csvMaxSegmentRetries {
		t.Fatalf("transactions retries = %d, want %d", got, csvMaxSegmentRetries)
	}
}

func TestCSVTokenAddressCooldown(t *testing.T) {
	oldWindow := csvTokenAddressWindowVar
	oldMax := csvTokenAddressMaxVar
	csvTokenAddressWindowVar = 80 * time.Millisecond
	csvTokenAddressMaxVar = 2
	defer func() {
		csvTokenAddressWindowVar = oldWindow
		csvTokenAddressMaxVar = oldMax
		delete(csvTokenAddressTimes, "0xcooldown")
	}()
	client := &CSVExportClient{baseURL: "https://www.oklink.com"}
	tokenKind := csvExportKind{Name: "token_transfers", Sheet: "token"}
	txKind := csvExportKind{Name: "transactions", Sheet: "transaction"}

	// First two token requests on the address are allowed immediately.
	finish, err := client.beginCSVEmailRequest(context.Background(), Config{Address: "0xCOOLDOWN"}, "BSC", tokenKind)
	if err != nil {
		t.Fatalf("first begin: %v", err)
	}
	finish()
	finish2, err := client.beginCSVEmailRequest(context.Background(), Config{Address: "0xCOOLDOWN"}, "ETH", tokenKind)
	if err != nil {
		t.Fatalf("second begin: %v", err)
	}
	finish2()
	if got := len(csvTokenAddressTimes["0xcooldown"]); got != 2 {
		t.Fatalf("recorded %d token timestamps, want 2", got)
	}

	// Third token request within the window must wait (sliding window max).
	start := time.Now()
	finish3, err := client.beginCSVEmailRequest(context.Background(), Config{Address: "0xCOOLDOWN"}, "POLYGON", tokenKind)
	if err != nil {
		t.Fatalf("third begin: %v", err)
	}
	finish3()
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("third token request waited %s, want >= 60ms window pacing", elapsed)
	}

	// A transactions request on the same address must NOT wait: the token
	// address window is scoped to token exports only.
	start = time.Now()
	finishTx, err := client.beginCSVEmailRequest(context.Background(), Config{Address: "0xCOOLDOWN"}, "OP", txKind)
	if err != nil {
		t.Fatalf("tx begin: %v", err)
	}
	finishTx()
	if elapsed := time.Since(start); elapsed >= 60*time.Millisecond {
		t.Fatalf("transactions request waited %s, want no token address pacing", elapsed)
	}
}

func TestCSVTokenAddressWaitLocked(t *testing.T) {
	oldWindow := csvTokenAddressWindowVar
	oldMax := csvTokenAddressMaxVar
	csvTokenAddressWindowVar = time.Minute
	csvTokenAddressMaxVar = 2
	defer func() {
		csvTokenAddressWindowVar = oldWindow
		csvTokenAddressMaxVar = oldMax
		delete(csvTokenAddressTimes, "addr")
	}()
	now := time.Now()
	// Below the limit: no wait.
	csvTokenAddressTimes["addr"] = []time.Time{now.Add(-10 * time.Second)}
	if got := csvTokenAddressWaitLocked("addr", now); got != 0 {
		t.Fatalf("wait below limit = %v, want 0", got)
	}
	// At the limit: wait until the oldest request leaves the window.
	csvTokenAddressTimes["addr"] = []time.Time{now.Add(-20 * time.Second), now.Add(-10 * time.Second)}
	if got := csvTokenAddressWaitLocked("addr", now); got != 40*time.Second {
		t.Fatalf("wait at limit = %v, want 40s", got)
	}
	// Stale timestamps are pruned.
	csvTokenAddressTimes["addr"] = []time.Time{now.Add(-2 * time.Minute), now.Add(-10 * time.Second)}
	if got := csvTokenAddressWaitLocked("addr", now); got != 0 {
		t.Fatalf("wait after pruning = %v, want 0", got)
	}
	if len(csvTokenAddressTimes["addr"]) != 1 {
		t.Fatalf("pruned timestamps = %d, want 1", len(csvTokenAddressTimes["addr"]))
	}
}

func TestCSVEmailTimeoutBackoffCapsAtThirtyMinutes(t *testing.T) {
	backoff := csvEmailTimeoutBackoff(10)
	if backoff != 30*time.Minute {
		t.Fatalf("backoff(10) = %v, want 30m cap", backoff)
	}
	backoff = csvEmailTimeoutBackoff(0)
	if backoff != csvEmailTimeoutBackoffBase {
		t.Fatalf("backoff(0) = %v, want base %v", backoff, csvEmailTimeoutBackoffBase)
	}
	if got := csvEmailTimeoutBackoff(1); got != 2*csvEmailTimeoutBackoffBase {
		t.Fatalf("backoff(1) = %v, want doubled base %v", got, 2*csvEmailTimeoutBackoffBase)
	}
}
