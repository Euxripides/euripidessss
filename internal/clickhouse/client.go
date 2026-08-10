package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	appconfig "github.com/etl/backend/internal/config"
)

const (
	maxErrorBody         = 64 << 10
	maxQueryResponseBody = 4 << 20
)

var (
	ErrDisabled = errors.New("clickhouse is disabled")
	tableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)
)

type Config = appconfig.ClickHouseConfig

type Metrics struct {
	Requests       uint64        `json:"http_requests_total"`
	Failures       uint64        `json:"http_failures_total"`
	InFlight       int64         `json:"http_in_flight"`
	BytesRead      uint64        `json:"bytes_read_total"`
	BytesWritten   uint64        `json:"bytes_written_total"`
	LastLatency    time.Duration `json:"last_http_latency"`
	LastStatusCode int           `json:"last_http_status_code"`
	QueryTotal     uint64        `json:"clickhouse_query_total"`
	QueryLatencyMS int64         `json:"clickhouse_query_latency_ms"`
	QueryErrors    uint64        `json:"clickhouse_query_errors_total"`
}

type HealthStatus struct {
	Enabled bool
	Healthy bool
	Latency time.Duration
	Error   string
}

type Client struct {
	config Config
	base   *url.URL
	http   *http.Client

	requests       atomic.Uint64
	failures       atomic.Uint64
	inFlight       atomic.Int64
	bytesRead      atomic.Uint64
	bytesWritten   atomic.Uint64
	lastLatencyNS  atomic.Int64
	lastStatusCode atomic.Int64
	queryTotal     atomic.Uint64
	queryLatencyNS atomic.Int64
	queryErrors    atomic.Uint64
}

func New(cfg Config) (*Client, error) {
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 10
	}
	if cfg.HTTPPort <= 0 || cfg.HTTPPort > 65535 {
		return nil, fmt.Errorf("invalid ClickHouse HTTP port")
	}
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("ClickHouse host is required")
	}
	host := strings.Trim(strings.TrimSpace(cfg.Host), "[]")
	parsedHost := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (parsedHost == nil || !parsedHost.IsLoopback()) {
		return nil, fmt.Errorf("ClickHouse HTTP client requires a loopback host")
	}
	if strings.TrimSpace(cfg.Database) == "" {
		return nil, fmt.Errorf("ClickHouse database is required")
	}

	base := &url.URL{Scheme: "http", Host: net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.HTTPPort)), Path: "/"}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxConnsPerHost:       cfg.MaxConnections,
		MaxIdleConns:          cfg.MaxConnections,
		MaxIdleConnsPerHost:   cfg.MaxConnections,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ResponseHeaderTimeout: cfg.RequestTimeout,
	}
	return &Client{
		config: cfg,
		base:   base,
		http:   &http.Client{Transport: transport, Timeout: cfg.RequestTimeout},
	}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	if !c.config.Enabled {
		return ErrDisabled
	}
	response, started, err := c.do(ctx, http.MethodGet, "/ping", nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
	c.bytesRead.Add(uint64(len(body)))
	c.finish(response.StatusCode, started, readErr)
	if readErr != nil {
		return c.redact(fmt.Errorf("ClickHouse ping response: %w", readErr))
	}
	if response.StatusCode != http.StatusOK {
		return c.statusError(response.StatusCode, body)
	}
	if strings.TrimSpace(string(body)) != "Ok." {
		return c.redact(fmt.Errorf("unexpected ClickHouse ping response: %q", boundedText(body)))
	}
	return nil
}

func (c *Client) Health(ctx context.Context) HealthStatus {
	started := time.Now()
	err := c.Ping(ctx)
	status := HealthStatus{Enabled: c.config.Enabled, Healthy: err == nil, Latency: time.Since(started)}
	if err != nil {
		status.Error = c.redact(err).Error()
	}
	return status
}

func (c *Client) Exec(ctx context.Context, query string) error {
	response, started, err := c.query(ctx, query, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
	c.bytesRead.Add(uint64(len(body)))
	c.finish(response.StatusCode, started, readErr)
	if readErr != nil {
		return c.redact(fmt.Errorf("read ClickHouse response: %w", readErr))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.statusError(response.StatusCode, body)
	}
	if len(body) > maxErrorBody {
		return fmt.Errorf("ClickHouse response exceeded %d bytes", maxErrorBody)
	}
	return nil
}

func (c *Client) QueryJSONEachRow(ctx context.Context, query string) (io.ReadCloser, error) {
	return c.queryStream(ctx, query, "JSONEachRow")
}

func (c *Client) QueryCSV(ctx context.Context, query string) (io.ReadCloser, error) {
	return c.queryStream(ctx, query, "CSV")
}

func (c *Client) QueryJSON(ctx context.Context, query string) ([]map[string]any, error) {
	stream, err := c.QueryJSONEachRow(ctx, query)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	body, err := io.ReadAll(io.LimitReader(stream, maxQueryResponseBody+1))
	if err != nil {
		return nil, c.redact(fmt.Errorf("read ClickHouse JSONEachRow: %w", err))
	}
	if len(body) > maxQueryResponseBody {
		return nil, fmt.Errorf("ClickHouse response exceeded %d bytes", maxQueryResponseBody)
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	rows := make([]map[string]any, 0)
	for {
		var row map[string]any
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, c.redact(fmt.Errorf("decode ClickHouse JSONEachRow: %w", err))
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (c *Client) InsertCSV(ctx context.Context, table string, columns []string, rows io.Reader) error {
	if !tableNameRE.MatchString(table) {
		return fmt.Errorf("invalid ClickHouse table name")
	}
	if len(columns) == 0 {
		return fmt.Errorf("ClickHouse insert columns are required")
	}
	for _, column := range columns {
		if !tableNameRE.MatchString(column) || strings.Contains(column, ".") {
			return fmt.Errorf("invalid ClickHouse column name")
		}
	}
	if rows == nil {
		return fmt.Errorf("CSV input is required")
	}
	prefix := "INSERT INTO " + table + " (" + strings.Join(columns, ",") + ") FORMAT CSV\n"
	body := io.MultiReader(strings.NewReader(prefix), rows)
	response, started, err := c.do(ctx, http.MethodPost, "/", body, "text/csv")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
	c.bytesRead.Add(uint64(len(responseBody)))
	c.finish(response.StatusCode, started, readErr)
	if readErr != nil {
		return c.redact(fmt.Errorf("read ClickHouse insert response: %w", readErr))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.statusError(response.StatusCode, responseBody)
	}
	if len(responseBody) > maxErrorBody {
		return fmt.Errorf("ClickHouse response exceeded %d bytes", maxErrorBody)
	}
	return nil
}

func (c *Client) Metrics() Metrics {
	return Metrics{
		Requests:       c.requests.Load(),
		Failures:       c.failures.Load(),
		InFlight:       c.inFlight.Load(),
		BytesRead:      c.bytesRead.Load(),
		BytesWritten:   c.bytesWritten.Load(),
		LastLatency:    time.Duration(c.lastLatencyNS.Load()),
		LastStatusCode: int(c.lastStatusCode.Load()),
		QueryTotal:     c.queryTotal.Load(),
		QueryLatencyMS: time.Duration(c.queryLatencyNS.Load()).Milliseconds(),
		QueryErrors:    c.queryErrors.Load(),
	}
}

func (c *Client) query(ctx context.Context, query, format string) (*http.Response, time.Time, error) {
	if !c.config.Enabled {
		return nil, time.Time{}, ErrDisabled
	}
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if query == "" {
		return nil, time.Time{}, fmt.Errorf("ClickHouse query is required")
	}
	if format != "" {
		query += " FORMAT " + format
	}
	return c.do(ctx, http.MethodPost, "/", strings.NewReader(query), "text/plain; charset=utf-8")
}

func (c *Client) queryStream(ctx context.Context, query, format string) (io.ReadCloser, error) {
	c.queryTotal.Add(1)
	queryStarted := time.Now()
	response, started, err := c.query(ctx, query, format)
	if err != nil {
		c.queryErrors.Add(1)
		c.queryLatencyNS.Store(int64(time.Since(queryStarted)))
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
		c.bytesRead.Add(uint64(len(body)))
		c.finish(response.StatusCode, started, readErr)
		if readErr != nil {
			c.queryErrors.Add(1)
			c.queryLatencyNS.Store(int64(time.Since(queryStarted)))
			return nil, c.redact(fmt.Errorf("read ClickHouse error response: %w", readErr))
		}
		c.queryErrors.Add(1)
		c.queryLatencyNS.Store(int64(time.Since(queryStarted)))
		return nil, c.statusError(response.StatusCode, body)
	}
	c.lastStatusCode.Store(int64(response.StatusCode))
	c.lastLatencyNS.Store(int64(time.Since(started)))
	return &meteredReadCloser{ReadCloser: response.Body, client: c, queryStarted: queryStarted}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, time.Time, error) {
	if !c.config.Enabled {
		return nil, time.Time{}, ErrDisabled
	}
	endpoint := *c.base
	endpoint.Path = path
	if path != "/ping" {
		values := endpoint.Query()
		values.Set("database", c.config.Database)
		endpoint.RawQuery = values.Encode()
	}
	counted := &countingReader{reader: body}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), counted)
	if err != nil {
		return nil, time.Time{}, c.redact(fmt.Errorf("create ClickHouse request: %w", err))
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.SetBasicAuth(c.config.User, c.config.Password)
	c.requests.Add(1)
	c.inFlight.Add(1)
	started := time.Now()
	response, err := c.http.Do(request)
	c.bytesWritten.Add(counted.count.Load())
	if err != nil {
		c.finish(0, started, err)
		return nil, time.Time{}, c.redact(fmt.Errorf("ClickHouse request failed: %w", err))
	}
	return response, started, nil
}

func (c *Client) finish(statusCode int, started time.Time, err error) {
	c.inFlight.Add(-1)
	c.lastStatusCode.Store(int64(statusCode))
	c.lastLatencyNS.Store(int64(time.Since(started)))
	if err != nil || statusCode < 200 || statusCode >= 300 {
		c.failures.Add(1)
	}
}

func (c *Client) statusError(statusCode int, body []byte) error {
	message := boundedText(body)
	if len(body) > maxErrorBody {
		message += " [truncated]"
	}
	return c.redact(fmt.Errorf("ClickHouse HTTP %d: %s", statusCode, message))
}

func (c *Client) redact(err error) error {
	if err == nil || c.config.Password == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.config.Password, "***"))
}

func boundedText(body []byte) string {
	if len(body) > maxErrorBody {
		body = body[:maxErrorBody]
	}
	return strings.TrimSpace(string(body))
}

type countingReader struct {
	reader io.Reader
	count  atomic.Uint64
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	if r.reader == nil {
		return 0, io.EOF
	}
	n, err := r.reader.Read(buffer)
	r.count.Add(uint64(n))
	return n, err
}

type meteredReadCloser struct {
	io.ReadCloser
	client       *Client
	closed       atomic.Bool
	queryStarted time.Time
	queryFailed  atomic.Bool
}

func (r *meteredReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.ReadCloser.Read(buffer)
	r.client.bytesRead.Add(uint64(n))
	if err != nil && !errors.Is(err, io.EOF) && r.queryFailed.CompareAndSwap(false, true) {
		r.client.queryErrors.Add(1)
	}
	return n, err
}

func (r *meteredReadCloser) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	err := r.ReadCloser.Close()
	r.client.inFlight.Add(-1)
	r.client.queryLatencyNS.Store(int64(time.Since(r.queryStarted)))
	if err != nil {
		r.client.failures.Add(1)
		if r.queryFailed.CompareAndSwap(false, true) {
			r.client.queryErrors.Add(1)
		}
	}
	return err
}
