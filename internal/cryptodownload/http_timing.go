package cryptodownload

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

type RequestTiming struct {
	Host  string        `json:"host"`
	DNS   time.Duration `json:"dns"`
	TCP   time.Duration `json:"tcp"`
	TLS   time.Duration `json:"tls"`
	TTFB  time.Duration `json:"ttfb"`
	Body  time.Duration `json:"body"`
	Total time.Duration `json:"total"`
}

type requestTimingObserver func(RequestTiming)

type requestTimer struct {
	mu         sync.Mutex
	finishOnce sync.Once
	now        func() time.Time
	observer   requestTimingObserver
	timing     RequestTiming
	started    time.Time
	dnsStart   time.Time
	tcpStart   time.Time
	tlsStart   time.Time
	firstByte  time.Time
}

func newRequestTimer(req *http.Request, observer requestTimingObserver, now func() time.Time) (*http.Request, *requestTimer) {
	started := now()
	timer := &requestTimer{
		now:      now,
		observer: observer,
		started:  started,
		timing:   RequestTiming{Host: req.URL.Hostname()},
	}
	trace := &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { timer.markStart(&timer.dnsStart) },
		DNSDone:              func(httptrace.DNSDoneInfo) { timer.markDuration(&timer.timing.DNS, &timer.dnsStart) },
		ConnectStart:         func(_, _ string) { timer.markStart(&timer.tcpStart) },
		ConnectDone:          func(_, _ string, _ error) { timer.markDuration(&timer.timing.TCP, &timer.tcpStart) },
		TLSHandshakeStart:    func() { timer.markStart(&timer.tlsStart) },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { timer.markDuration(&timer.timing.TLS, &timer.tlsStart) },
		GotFirstResponseByte: timer.markFirstByte,
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace)), timer
}

func (t *requestTimer) markStart(destination *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	*destination = t.now()
}

func (t *requestTimer) markDuration(destination *time.Duration, started *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !started.IsZero() {
		*destination = t.now().Sub(*started)
	}
}

func (t *requestTimer) markFirstByte() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.firstByte = t.now()
	t.timing.TTFB = t.firstByte.Sub(t.started)
}

func (t *requestTimer) finish() {
	t.finishOnce.Do(func() {
		t.mu.Lock()
		finished := t.now()
		t.timing.Total = finished.Sub(t.started)
		if !t.firstByte.IsZero() {
			t.timing.Body = finished.Sub(t.firstByte)
		}
		timing := t.timing
		t.mu.Unlock()
		t.observer(timing)
	})
}

func doHTTPRequest(client *http.Client, req *http.Request, observer requestTimingObserver) (*http.Response, error) {
	host := req.URL.Hostname()
	if shouldPaceHTTPHost(host) {
		if err := sharedHTTPHostPacer.wait(req.Context(), host); err != nil {
			return nil, err
		}
	}
	if observer == nil {
		response, err := client.Do(req)
		if response != nil && shouldPaceHTTPHost(host) {
			sharedHTTPHostPacer.observe(host, response.StatusCode, response.Header.Get("Retry-After"))
		}
		return response, err
	}
	tracedRequest, timer := newRequestTimer(req, observer, time.Now)
	response, err := client.Do(tracedRequest)
	if response != nil && shouldPaceHTTPHost(host) {
		sharedHTTPHostPacer.observe(host, response.StatusCode, response.Header.Get("Retry-After"))
	}
	if err != nil {
		timer.finish()
		return nil, err
	}
	response.Body = &timedResponseBody{ReadCloser: response.Body, timer: timer}
	return response, nil
}

type timedResponseBody struct {
	io.ReadCloser
	timer *requestTimer
}

func (b *timedResponseBody) Read(buffer []byte) (int, error) {
	read, err := b.ReadCloser.Read(buffer)
	if err != nil {
		b.timer.finish()
	}
	return read, err
}

func (b *timedResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.timer.finish()
	return err
}
