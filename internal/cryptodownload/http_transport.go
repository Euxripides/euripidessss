package cryptodownload

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"

	"github.com/etl/backend/internal/cryptodownload/useragent"
)

const (
	sharedHTTPConnectTimeout       = 10 * time.Second
	sharedHTTPTLSHandshakeTimeout  = 10 * time.Second
	sharedHTTPIdleConnTimeout      = 90 * time.Second
	sharedHTTPKeepAlive            = 30 * time.Second
	sharedHTTPDefaultHeaderTimeout = 30 * time.Second
	sharedHTTPMaxIdleConns         = 100
	sharedHTTPMaxIdleConnsPerHost  = 16
	sharedHTTPMaxConnsPerHost      = 32
)

var sharedHTTPTransports sync.Map

func newSharedHTTPClient(responseHeaderTimeout time.Duration) *http.Client {
	return &http.Client{Transport: newSharedHTTPTransport(responseHeaderTimeout, utls.HelloChrome_Auto)}
}

// chromeTLSFingerprintForIndex returns the utls ClientHello fingerprint that
// matches the Chrome version bound to a proxy pool index (UA and TLS always
// describe the same browser generation).
func chromeTLSFingerprintForIndex(index int) utls.ClientHelloID {
	switch useragent.ChromeVersionForIndex(index) {
	case "72":
		return utls.HelloChrome_72
	case "83":
		return utls.HelloChrome_83
	case "87":
		return utls.HelloChrome_87
	case "96":
		return utls.HelloChrome_96
	case "100":
		return utls.HelloChrome_100
	case "102":
		return utls.HelloChrome_102
	default:
		return utls.HelloChrome_120
	}
}

// newCSVPerProxyClients builds one HTTP client per proxy pool entry.  Each
// client pins its transport to a single proxy IP and uses the TLS fingerprint
// matching that IP's browser identity, so every IP presents a fully
// consistent browser stack (UA + client hints + TLS).
func newCSVPerProxyClients(proxies []*url.URL, responseHeaderTimeout time.Duration) []*http.Client {
	clients := make([]*http.Client, 0, len(proxies))
	for index, proxyURL := range proxies {
		transport := newSharedHTTPTransport(responseHeaderTimeout, chromeTLSFingerprintForIndex(index))
		transport.Proxy = func(request *http.Request) (*url.URL, error) { return proxyURL, nil }
		clients = append(clients, &http.Client{Transport: transport})
	}
	return clients
}

func newSharedHTTPTransport(responseHeaderTimeout time.Duration, chromeID utls.ClientHelloID) *http.Transport {
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = sharedHTTPDefaultHeaderTimeout
	}
	dialer := &net.Dialer{
		Timeout:   sharedHTTPConnectTimeout,
		KeepAlive: sharedHTTPKeepAlive,
	}
	transport := &http.Transport{
		Proxy:                 csvHTTPProxyFunc,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          sharedHTTPMaxIdleConns,
		MaxIdleConnsPerHost:   sharedHTTPMaxIdleConnsPerHost,
		MaxConnsPerHost:       sharedHTTPMaxConnsPerHost,
		IdleConnTimeout:       sharedHTTPIdleConnTimeout,
		TLSHandshakeTimeout:   sharedHTTPTLSHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
	}
	transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = strings.TrimSpace(addr)
		}
		// When the caller supplies a custom TLS config (e.g. tests with
		// self-signed certificates), use standard Go TLS so that the
		// trusted root CAs are honoured.
		if transport.TLSClientConfig != nil {
			cfg := transport.TLSClientConfig.Clone()
			if cfg.ServerName == "" {
				cfg.ServerName = host
			}
			if len(cfg.NextProtos) == 0 {
				cfg.NextProtos = []string{"h2", "http/1.1"}
			}
			tlsConn := tls.Client(conn, cfg)
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return tlsConn, nil
		}
		utlsConn := utls.UClient(conn, &utls.Config{
			ServerName: host,
			NextProtos: []string{"h2", "http/1.1"},
		}, chromeID)
		if err := utlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return utlsConn, nil
	}
	// Chrome-like HTTP/2 SETTINGS so the h2 handshake does not betray a stock
	// Go client: 1000 concurrent streams, 64 KiB header table, 16 KiB frames,
	// ~4 MiB stream receive window (Chrome sends 6 MiB; Go caps below 4 MiB)
	// and 256 KiB max header list.
	transport.HTTP2 = &http.HTTP2Config{
		MaxConcurrentStreams:      1000,
		MaxDecoderHeaderTableSize: 65536,
		MaxEncoderHeaderTableSize: 65536,
		MaxReadFrameSize:          16384,
		MaxReceiveBufferPerStream: 4194303,
	}
	if http2Transport, err := http2.ConfigureTransports(transport); err == nil {
		http2Transport.MaxHeaderListSize = 262144
	}
	sharedHTTPTransports.Store(transport, transport)
	return transport
}

// MarkHostStale closes idle connections after a host reports connection-level throttling.
func MarkHostStale(host string) {
	sharedHTTPTransports.Range(func(_, value any) bool {
		if transport, ok := value.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
		return true
	})
}
