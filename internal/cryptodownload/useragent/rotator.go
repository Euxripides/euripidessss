package useragent

import (
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
	hostUserAgents sync.Map
	randomMu       sync.Mutex
	randomSource   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

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
