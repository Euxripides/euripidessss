package cryptodownload

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-imap/client"
	"golang.org/x/net/proxy"
)

type csvContextDialer struct {
	ctx     context.Context
	timeout time.Duration
	dialer  proxy.ContextDialer
}

func (d csvContextDialer) Dial(network, address string) (net.Conn, error) {
	if d.dialer != nil {
		return d.dialer.DialContext(d.ctx, network, address)
	}
	return (&net.Dialer{Timeout: d.timeout}).DialContext(d.ctx, network, address)
}

func dialCSVIMAPTLS(ctx context.Context, config CSVMailConfig) (*client.Client, error) {
	address := fmt.Sprintf("%s:%d", config.Host, config.Port)

	// Resolve a SOCKS5 proxy when OKLINK_IMAP_PROXY or ALL_PROXY is set.
	var dialer proxy.ContextDialer = proxy.Direct
	if socksAddr := csvIMAPSOCKS5Addr(); socksAddr != "" {
		socks, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
		if err == nil {
			dialer = &socksContextDialer{socks}
		}
	}
	ctxDialer := csvContextDialer{ctx: ctx, timeout: csvIMAPCommandTimeout, dialer: dialer}

	conn, err := ctxDialer.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	bootstrapDeadline := time.Now().Add(ctxDialer.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(bootstrapDeadline) {
		bootstrapDeadline = contextDeadline
	}
	if err := conn.SetDeadline(bootstrapDeadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	serverName, _, _ := net.SplitHostPort(address)
	tlsConn := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	mail, err := client.New(tlsConn)
	if err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	if err := tlsConn.SetDeadline(time.Time{}); err != nil {
		_ = mail.Logout()
		return nil, err
	}
	return mail, nil
}

// csvIMAPSOCKS5Addr returns the SOCKS5 proxy address when OKLINK_IMAP_PROXY
// or ALL_PROXY is set to a socks5:// URL, otherwise "".
func csvIMAPSOCKS5Addr() string {
	raw := firstNonEmpty(
		strings.TrimSpace(os.Getenv("OKLINK_IMAP_PROXY")),
		strings.TrimSpace(os.Getenv("ALL_PROXY")),
	)
	if raw == "" || !strings.HasPrefix(raw, "socks5://") {
		return ""
	}
	return strings.TrimPrefix(raw, "socks5://")
}

// socksContextDialer wraps a proxy.Dialer so it satisfies proxy.ContextDialer.
type socksContextDialer struct{ proxy.Dialer }

func (d *socksContextDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := d.Dialer.Dial(network, addr)
		ch <- result{conn, err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func waitCSVIMAPBackoff(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func maxCSVUID(uids []uint32) uint32 {
	var maximum uint32
	for _, uid := range uids {
		if uid > maximum {
			maximum = uid
		}
	}
	return maximum
}
