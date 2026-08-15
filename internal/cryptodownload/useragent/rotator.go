package useragent

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"strings"
	"sync"
	"time"
)

var browserUserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 13_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1",
}

var (
	hostUserAgents      sync.Map
	hostChromeUserAgents sync.Map
	hostAcceptLanguages  sync.Map
	randomMu            sync.Mutex
	randomSource        = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// chromeUserAgents is the Chrome-only pool used by the CSV download path,
// whose TLS fingerprint is fixed to utls HelloChrome_Auto — pairing a Chrome
// UA with a Chrome TLS fingerprint is the only consistent combination.  The
// pool is generated from chromeVersions × platform so every proxy IP can be
// bound to its own browser identity.
var chromeUserAgents = buildChromeUserAgents()

// chromeVersions are the Chrome versions whose TLS fingerprints are available
// in the vendored utls (v1.6.7, unsuffixed parrots: 72/83/87/96/100/102/120), so
// every pool identity pairs a UA version with a matching TLS fingerprint.
var chromeVersions = []string{"72", "83", "87", "96", "100", "102", "120"}

func buildChromeUserAgents() []string {
	var list []string
	for _, version := range chromeVersions {
		list = append(list,
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/"+version+".0.0.0 Safari/537.36",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/"+version+".0.0.0 Safari/537.36",
			"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/"+version+".0.0.0 Safari/537.36",
		)
	}
	return list
}

var acceptLanguages = []string{
	"zh-CN,zh;q=0.9,en;q=0.8",
	"zh-CN,zh;q=0.8,en;q=0.8,en-US;q=0.6",
	"zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7",
	"zh-CN,zh;q=1.0,en;q=0.9",
	"en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7",
}

// Get returns a stable browser User-Agent for a host.
func Get(host string) string {
	key := strings.ToLower(strings.TrimSpace(host))
	if value, ok := hostUserAgents.Load(key); ok {
		return value.(string)
	}
	value := Random()
	actual, _ := hostUserAgents.LoadOrStore(key, value)
	return actual.(string)
}

// Random returns a browser User-Agent selected from the configured rotation.
func Random() string {
	randomMu.Lock()
	defer randomMu.Unlock()
	return browserUserAgents[randomSource.Intn(len(browserUserAgents))]
}

// GetChrome returns a stable Chrome User-Agent for a host, drawn from the
// Chrome-only pool so the UA always matches the Chrome TLS fingerprint used
// by the shared transports.  Selection is deterministic per identity: the
// same proxy IP/host maps to the same browser fingerprint across restarts,
// so every pool IP behaves like one fixed browser user.
func GetChrome(host string) string {
	key := strings.ToLower(strings.TrimSpace(host))
	if value, ok := hostChromeUserAgents.Load(key); ok {
		return value.(string)
	}
	value := chromeUserAgents[identityIndex(key, len(chromeUserAgents))]
	actual, _ := hostChromeUserAgents.LoadOrStore(key, value)
	return actual.(string)
}

// identityIndex maps an identity string to a deterministic pool index.
func identityIndex(key string, size int) uint32 {
	hash := fnv.New32a()
	hash.Write([]byte(key))
	return hash.Sum32() % uint32(size)
}

// ChromeVersionForIndex returns the Chrome major version bound to a proxy
// pool index, so the caller can pair the UA with a matching TLS fingerprint.
// The UA pool lists three platforms per version, so the version advances
// every three indexes.
func ChromeVersionForIndex(index int) string {
	if index < 0 {
		index = 0
	}
	return chromeVersions[(index/3)%len(chromeVersions)]
}

// ChromeByIndex returns the Chrome User-Agent assigned to a proxy pool index.
// Indexes map directly onto distinct pool entries, so every proxy IP gets a
// unique fingerprint (no hash collisions) while staying deterministic across
// restarts (pool order is stable).
func ChromeByIndex(index int) string {
	if index < 0 {
		index = 0
	}
	return chromeUserAgents[index%len(chromeUserAgents)]
}

// AcceptLanguageByIndex returns the Accept-Language value assigned to a proxy
// pool index, in lockstep with ChromeByIndex.
func AcceptLanguageByIndex(index int) string {
	if index < 0 {
		index = 0
	}
	return acceptLanguages[index%len(acceptLanguages)]
}

// AcceptLanguage returns a stable Accept-Language value for a host.  The
// value is cached per host (like Get/GetChrome) so a host presents one
// consistent language preference; selection is deterministic per identity.
func AcceptLanguage(host string) string {
	key := strings.ToLower(strings.TrimSpace(host))
	if value, ok := hostAcceptLanguages.Load(key); ok {
		return value.(string)
	}
	value := acceptLanguages[identityIndex(key, len(acceptLanguages))]
	actual, _ := hostAcceptLanguages.LoadOrStore(key, value)
	return actual.(string)
}

// SecCHUABrand returns the Sec-CH-UA header value matching a Chrome User-Agent,
// or "" for non-Chrome browsers (Safari/Firefox do not send Sec-CH-UA).
func SecCHUABrand(ua string) string {
	version := chromeVersion(ua)
	if version == "" {
		return ""
	}
	return fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", "Not.A/Brand";v="24"`, version, version)
}

// SecCHUAPlatform returns the Sec-CH-UA-Platform value matching a User-Agent,
// or "" when the platform cannot be derived.
func SecCHUAPlatform(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		return "Windows"
	case strings.Contains(ua, "Macintosh"):
		return "macOS"
	case strings.Contains(ua, "X11"):
		return "Linux"
	case strings.Contains(ua, "iPhone"):
		return "iOS"
	}
	return ""
}

// IsMobileUA reports whether the User-Agent describes a mobile browser.
func IsMobileUA(ua string) bool {
	return strings.Contains(ua, "iPhone")
}

// SecCHUAFullVersionList returns the Sec-CH-UA-Full-Version-List header value
// matching a Chrome User-Agent, or "" for non-Chrome browsers.
func SecCHUAFullVersionList(ua string) string {
	version := chromeFullVersion(ua)
	if version == "" {
		return ""
	}
	return fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", "Not.A/Brand";v="24.0.0.0"`, version, version)
}

func chromeVersion(ua string) string {
	index := strings.Index(ua, "Chrome/")
	if index < 0 {
		return ""
	}
	rest := ua[index+len("Chrome/"):]
	end := strings.IndexAny(rest, " .;")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

func chromeFullVersion(ua string) string {
	index := strings.Index(ua, "Chrome/")
	if index < 0 {
		return ""
	}
	rest := ua[index+len("Chrome/"):]
	end := strings.IndexAny(rest, " ")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}
