package cryptodownload

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
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
	return &http.Client{Transport: newSharedHTTPTransport(responseHeaderTimeout)}
}

func newSharedHTTPTransport(responseHeaderTimeout time.Duration) *http.Transport {
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = sharedHTTPDefaultHeaderTimeout
	}
	dialer := &net.Dialer{
		Timeout:   sharedHTTPConnectTimeout,
		KeepAlive: sharedHTTPKeepAlive,
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
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
		}, utls.HelloChrome_Auto)
		if err := utlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return utlsConn, nil
	}
	if http2Transport, err := http2.ConfigureTransports(transport); err == nil {
		http2Transport.MaxDecoderHeaderTableSize = 65536
		http2Transport.MaxEncoderHeaderTableSize = 65536
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
