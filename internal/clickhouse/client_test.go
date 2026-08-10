package clickhouse

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testConfig(t *testing.T, server *httptest.Server) Config {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := 0
	if _, err := fmt.Sscan(parsed.Port(), &port); err != nil {
		t.Fatal(err)
	}
	return Config{
		Enabled:        true,
		Host:           parsed.Hostname(),
		HTTPPort:       port,
		Database:       "onchain",
		User:           "etl_app",
		Password:       "top-secret",
		ConnectTimeout: time.Second,
		RequestTimeout: time.Second,
		MaxConnections: 2,
	}
}

func TestClientAuthQueryAndInsertStreaming(t *testing.T) {
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "etl_app" || password != "top-secret" {
			t.Errorf("unexpected auth: ok=%v user=%q", ok, user)
		}
		if r.URL.Path != "/ping" && r.URL.Query().Get("database") != "onchain" {
			t.Errorf("unexpected database: %q", r.URL.Query().Get("database"))
		}
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		switch {
		case r.URL.Path == "/ping":
			_, _ = io.WriteString(w, "Ok.\n")
		case strings.Contains(string(body), "JSONEachRow"):
			_, _ = io.WriteString(w, "{\"id\":1}\n{\"id\":2}\n")
		case strings.Contains(string(body), "FORMAT CSV") && strings.HasPrefix(string(body), "SELECT"):
			_, _ = io.WriteString(w, "1,alpha\n")
		}
	}))
	defer server.Close()
	client, err := New(testConfig(t, server))
	if err != nil {
		t.Fatal(err)
	}

	if err := client.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := client.QueryJSON(context.Background(), "SELECT 1;")
	if err != nil || len(rows) != 2 {
		t.Fatalf("QueryJSON rows=%v err=%v", rows, err)
	}
	csvStream, err := client.QueryCSV(context.Background(), "SELECT 1, 'alpha'")
	if err != nil {
		t.Fatal(err)
	}
	csvBody, err := io.ReadAll(csvStream)
	if err != nil {
		t.Fatal(err)
	}
	if err := csvStream.Close(); err != nil {
		t.Fatal(err)
	}
	if string(csvBody) != "1,alpha\n" {
		t.Fatalf("unexpected CSV: %q", csvBody)
	}
	if err := client.InsertCSV(context.Background(), "onchain.blocks", []string{"chain_id", "block_number"}, strings.NewReader("bsc,1\n")); err != nil {
		t.Fatal(err)
	}

	seen := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		seen = append(seen, <-requests)
	}
	if seen[1] != "SELECT 1 FORMAT JSONEachRow" {
		t.Fatalf("unexpected JSON query: %q", seen[1])
	}
	if seen[2] != "SELECT 1, 'alpha' FORMAT CSV" {
		t.Fatalf("unexpected CSV query: %q", seen[2])
	}
	if seen[3] != "INSERT INTO onchain.blocks (chain_id,block_number) FORMAT CSV\nbsc,1\n" {
		t.Fatalf("unexpected insert: %q", seen[3])
	}
	metrics := client.Metrics()
	if metrics.Requests != 4 || metrics.Failures != 0 || metrics.InFlight != 0 || metrics.BytesRead == 0 || metrics.BytesWritten == 0 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}

func TestClientTimeoutAndServerErrorRedactPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "slow") {
			time.Sleep(100 * time.Millisecond)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, strings.Repeat("top-secret ", 10000))
	}))
	defer server.Close()
	cfg := testConfig(t, server)
	cfg.RequestTimeout = 20 * time.Millisecond
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	err = client.Exec(context.Background(), "SELECT 'slow'")
	if err == nil || strings.Contains(err.Error(), cfg.Password) {
		t.Fatalf("timeout missing or leaked password: %v", err)
	}
	cfg.RequestTimeout = time.Second
	client, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Exec(context.Background(), "SELECT 'fail'")
	if err == nil || strings.Contains(err.Error(), cfg.Password) || len(err.Error()) > maxErrorBody+100 {
		t.Fatalf("server error not bounded/redacted: length=%d err=%v", len(err.Error()), err)
	}
	if !strings.Contains(err.Error(), "[truncated]") || !strings.Contains(err.Error(), "***") {
		t.Fatalf("missing truncation/redaction marker: %v", err)
	}
}

func TestClientRejectsUnsafeInsertIdentifiers(t *testing.T) {
	client, err := New(Config{Enabled: true, Host: "127.0.0.1", HTTPPort: 8123, Database: "onchain"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertCSV(context.Background(), "blocks; DROP TABLE blocks", []string{"id"}, strings.NewReader("1\n")); err == nil {
		t.Fatal("expected unsafe table rejection")
	}
	if err := client.InsertCSV(context.Background(), "blocks", []string{"id) FORMAT CSV"}, strings.NewReader("1\n")); err == nil {
		t.Fatal("expected unsafe column rejection")
	}
}

func TestClientRejectsRemotePlainHTTPHost(t *testing.T) {
	_, err := New(Config{Enabled: true, Host: "192.0.2.10", HTTPPort: 8123, Database: "onchain"})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("remote plaintext ClickHouse host was accepted: %v", err)
	}
}
