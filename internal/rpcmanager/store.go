package rpcmanager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type endpointRecord struct {
	Endpoint
	EncryptedURL     []byte
	EncryptedTestURL []byte
}

type store struct {
	db   *sql.DB
	path string
}

func openStore(dataRoot string) (*store, error) {
	dbPath := filepath.Join(dataRoot, "config", "rpc_control.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db, path: dbPath}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *store) init() error {
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		`CREATE TABLE IF NOT EXISTS rpc_endpoints (
			endpoint_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			chain_key TEXT NOT NULL,
			chain_id INTEGER NOT NULL,
			display_name TEXT NOT NULL,
			endpoint_host TEXT NOT NULL,
			endpoint_encrypted BLOB NOT NULL,
			test_endpoint_encrypted BLOB,
			secret_encrypted BLOB,
			priority INTEGER NOT NULL,
			enabled INTEGER NOT NULL,
			max_rps REAL NOT NULL,
			max_concurrency INTEGER NOT NULL,
			request_timeout_ms INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rpc_endpoints_chain_priority
		 ON rpc_endpoints(chain_key, enabled, priority)`,
		`CREATE TABLE IF NOT EXISTS rpc_endpoint_health (
			endpoint_id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			health_score REAL NOT NULL,
			latest_block INTEGER,
			block_lag INTEGER,
			latency_p50_ms REAL,
			latency_p95_ms REAL,
			success_rate_5m REAL,
			consecutive_failures INTEGER,
			circuit_state TEXT,
			circuit_open_until TEXT,
			last_success_at TEXT,
			last_failure_at TEXT,
			last_error_code TEXT,
			last_error_message_redacted TEXT,
			checked_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS rpc_request_metrics (
			minute TEXT NOT NULL,
			endpoint_id TEXT NOT NULL,
			chain_id INTEGER NOT NULL,
			method TEXT NOT NULL,
			request_count INTEGER NOT NULL,
			success_count INTEGER NOT NULL,
			failure_count INTEGER NOT NULL,
			rate_limited_count INTEGER NOT NULL,
			timeout_count INTEGER NOT NULL,
			latency_sum_ms REAL NOT NULL,
			PRIMARY KEY(minute, endpoint_id, method)
		)`,
		`CREATE TABLE IF NOT EXISTS enrichment_jobs (
			job_id TEXT PRIMARY KEY,
			job_type TEXT NOT NULL,
			chain_key TEXT NOT NULL,
			chain_id INTEGER NOT NULL,
			status TEXT NOT NULL,
			total_items INTEGER NOT NULL,
			completed_items INTEGER NOT NULL,
			succeeded_items INTEGER NOT NULL,
			failed_items INTEGER NOT NULL,
			skipped_items INTEGER NOT NULL,
			cache_hits INTEGER NOT NULL DEFAULT 0,
			started_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			finished_at TEXT,
			cancellation_requested INTEGER NOT NULL,
			error_summary TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS rpc_enrichment_cache (
			chain_key TEXT NOT NULL,
			chain_id INTEGER NOT NULL,
			cache_type TEXT NOT NULL,
			cache_key TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			status TEXT NOT NULL,
			expires_at TEXT,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(chain_key, cache_type, cache_key)
		)`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec("ALTER TABLE rpc_endpoints ADD COLUMN test_endpoint_encrypted BLOB"); err != nil &&
		!strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	return nil
}

func (s *store) endpoints(chainKey string, enabledOnly bool) ([]endpointRecord, error) {
	query := `SELECT endpoint_id, provider, chain_key, chain_id, display_name, endpoint_host,
endpoint_encrypted, test_endpoint_encrypted, priority, enabled, max_rps, max_concurrency, request_timeout_ms, created_at, updated_at
FROM rpc_endpoints WHERE 1=1`
	args := []any{}
	if chainKey != "" {
		query += " AND chain_key = ?"
		args = append(args, chainKey)
	}
	if enabledOnly {
		query += " AND enabled = 1"
	}
	query += " ORDER BY chain_key, priority, created_at"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var result []endpointRecord
	for rows.Next() {
		var item endpointRecord
		var enabled int
		var createdAt, updatedAt string
		if err := rows.Scan(
			&item.ID, &item.Provider, &item.ChainKey, &item.ChainID, &item.DisplayName,
			&item.EndpointHost, &item.EncryptedURL, &item.EncryptedTestURL, &item.Priority, &enabled, &item.MaxRPS,
			&item.MaxConcurrency, &item.RequestTimeoutMS, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		item.Enabled = enabled == 1
		item.SecretConfigured = len(item.EncryptedURL) > 0
		item.TestEndpointConfigured = len(item.EncryptedTestURL) > 0
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		result[index].Health = s.health(result[index].ID)
	}
	return result, nil
}

func (s *store) endpoint(id string) (endpointRecord, error) {
	items, err := s.endpoints("", false)
	if err != nil {
		return endpointRecord{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return endpointRecord{}, sql.ErrNoRows
}

func (s *store) insertEndpoint(item endpointRecord) error {
	_, err := s.db.Exec(`INSERT INTO rpc_endpoints (
endpoint_id, provider, chain_key, chain_id, display_name, endpoint_host, endpoint_encrypted,
test_endpoint_encrypted, priority, enabled, max_rps, max_concurrency, request_timeout_ms, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.Provider, item.ChainKey, item.ChainID, item.DisplayName, item.EndpointHost,
		item.EncryptedURL, item.EncryptedTestURL, item.Priority, boolInt(item.Enabled), item.MaxRPS, item.MaxConcurrency,
		item.RequestTimeoutMS, item.CreatedAt.Format(time.RFC3339Nano), item.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}
	return s.saveHealth(Health{EndpointID: item.ID, Status: StatusUnavailable, CircuitState: CircuitClosed})
}

func (s *store) updateEndpoint(item endpointRecord) error {
	_, err := s.db.Exec(`UPDATE rpc_endpoints SET provider=?, chain_key=?, chain_id=?, display_name=?,
endpoint_host=?, endpoint_encrypted=?, test_endpoint_encrypted=?, priority=?, enabled=?, max_rps=?,
max_concurrency=?, request_timeout_ms=?, updated_at=? WHERE endpoint_id=?`,
		item.Provider, item.ChainKey, item.ChainID, item.DisplayName, item.EndpointHost, item.EncryptedURL,
		item.EncryptedTestURL, item.Priority, boolInt(item.Enabled), item.MaxRPS, item.MaxConcurrency, item.RequestTimeoutMS,
		item.UpdatedAt.Format(time.RFC3339Nano), item.ID,
	)
	return err
}

func (s *store) deleteEndpoint(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, query := range []string{
		"DELETE FROM rpc_request_metrics WHERE endpoint_id=?",
		"DELETE FROM rpc_endpoint_health WHERE endpoint_id=?",
		"DELETE FROM rpc_endpoints WHERE endpoint_id=?",
	} {
		if _, err := tx.Exec(query, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *store) updateRouting(chainKey string, endpointIDs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for index, id := range endpointIDs {
		result, err := tx.Exec("UPDATE rpc_endpoints SET priority=?, updated_at=? WHERE endpoint_id=? AND chain_key=?",
			(index+1)*10, time.Now().UTC().Format(time.RFC3339Nano), id, chainKey)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			_ = tx.Rollback()
			return errors.New("路由包含不存在或链不匹配的节点")
		}
	}
	return tx.Commit()
}

func (s *store) health(id string) Health {
	item := Health{EndpointID: id, Status: StatusUnavailable, CircuitState: CircuitClosed}
	var openUntil, lastSuccess, lastFailure, checked sql.NullString
	err := s.db.QueryRow(`SELECT status, health_score, latest_block, block_lag, latency_p50_ms,
latency_p95_ms, success_rate_5m, consecutive_failures, circuit_state, circuit_open_until,
last_success_at, last_failure_at, last_error_code, last_error_message_redacted, checked_at
FROM rpc_endpoint_health WHERE endpoint_id=?`, id).Scan(
		&item.Status, &item.HealthScore, &item.LatestBlock, &item.BlockLag, &item.LatencyP50MS,
		&item.LatencyP95MS, &item.SuccessRate5M, &item.ConsecutiveFailures, &item.CircuitState,
		&openUntil, &lastSuccess, &lastFailure, &item.LastErrorCode, &item.LastErrorMessageRedacted, &checked,
	)
	if err != nil {
		return item
	}
	item.CircuitOpenUntil = parseNullTime(openUntil)
	item.LastSuccessAt = parseNullTime(lastSuccess)
	item.LastFailureAt = parseNullTime(lastFailure)
	item.CheckedAt = parseNullTime(checked)
	return item
}

func (s *store) saveHealth(item Health) error {
	_, err := s.db.Exec(`INSERT INTO rpc_endpoint_health (
endpoint_id,status,health_score,latest_block,block_lag,latency_p50_ms,latency_p95_ms,
success_rate_5m,consecutive_failures,circuit_state,circuit_open_until,last_success_at,
last_failure_at,last_error_code,last_error_message_redacted,checked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(endpoint_id) DO UPDATE SET status=excluded.status,health_score=excluded.health_score,
latest_block=excluded.latest_block,block_lag=excluded.block_lag,latency_p50_ms=excluded.latency_p50_ms,
latency_p95_ms=excluded.latency_p95_ms,success_rate_5m=excluded.success_rate_5m,
consecutive_failures=excluded.consecutive_failures,circuit_state=excluded.circuit_state,
circuit_open_until=excluded.circuit_open_until,last_success_at=excluded.last_success_at,
last_failure_at=excluded.last_failure_at,last_error_code=excluded.last_error_code,
last_error_message_redacted=excluded.last_error_message_redacted,checked_at=excluded.checked_at`,
		item.EndpointID, item.Status, item.HealthScore, item.LatestBlock, item.BlockLag,
		item.LatencyP50MS, item.LatencyP95MS, item.SuccessRate5M, item.ConsecutiveFailures,
		item.CircuitState, formatTimePointer(item.CircuitOpenUntil), formatTimePointer(item.LastSuccessAt),
		formatTimePointer(item.LastFailureAt), item.LastErrorCode, item.LastErrorMessageRedacted,
		formatTimePointer(item.CheckedAt),
	)
	return err
}

func (s *store) recordMetric(endpointID string, chainID int64, method string, success, rateLimited, timeout bool, latency time.Duration) {
	minute := time.Now().UTC().Truncate(time.Minute).Format(time.RFC3339)
	_, _ = s.db.Exec(`INSERT INTO rpc_request_metrics (
minute,endpoint_id,chain_id,method,request_count,success_count,failure_count,rate_limited_count,timeout_count,latency_sum_ms
) VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?)
ON CONFLICT(minute,endpoint_id,method) DO UPDATE SET
request_count=request_count+1,success_count=success_count+excluded.success_count,
failure_count=failure_count+excluded.failure_count,rate_limited_count=rate_limited_count+excluded.rate_limited_count,
timeout_count=timeout_count+excluded.timeout_count,latency_sum_ms=latency_sum_ms+excluded.latency_sum_ms`,
		minute, endpointID, chainID, method, boolInt(success), boolInt(!success),
		boolInt(rateLimited), boolInt(timeout), float64(latency.Microseconds())/1000,
	)
}

func (s *store) overview(cacheHits, cacheMisses int64) Overview {
	var result Overview
	_ = s.db.QueryRow("SELECT COUNT(*) FROM rpc_endpoints").Scan(&result.ConfiguredEndpoints)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM rpc_endpoint_health WHERE status=?", StatusHealthy).Scan(&result.HealthyEndpoints)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM rpc_endpoint_health WHERE status IN (?,?)", StatusDegraded, StatusRateLimited).Scan(&result.DegradedEndpoints)
	today := time.Now().UTC().Format("2006-01-02") + "%"
	_ = s.db.QueryRow("SELECT COALESCE(SUM(request_count),0), COALESCE(SUM(rate_limited_count),0) FROM rpc_request_metrics WHERE minute LIKE ?", today).
		Scan(&result.TodayRequests, &result.RateLimitedCount)
	total := cacheHits + cacheMisses
	if total > 0 {
		result.CacheHitRate = float64(cacheHits) / float64(total) * 100
	}
	return result
}

func (s *store) cacheGet(chainKey, cacheType, key string, target any) (bool, error) {
	var payload, expires string
	err := s.db.QueryRow(`SELECT payload_json, COALESCE(expires_at,'') FROM rpc_enrichment_cache
WHERE chain_key=? AND cache_type=? AND cache_key=?`, chainKey, cacheType, key).Scan(&payload, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if expires != "" {
		expiry, parseErr := time.Parse(time.RFC3339Nano, expires)
		if parseErr == nil && time.Now().UTC().After(expiry) {
			return false, nil
		}
	}
	return true, json.Unmarshal([]byte(payload), target)
}

func (s *store) cachePut(chainKey string, chainID int64, cacheType, key, status string, value any, ttl time.Duration) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var expires any
	if ttl > 0 {
		expires = now.Add(ttl).Format(time.RFC3339Nano)
	}
	_, err = s.db.Exec(`INSERT INTO rpc_enrichment_cache (
chain_key,chain_id,cache_type,cache_key,payload_json,status,expires_at,updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(chain_key,cache_type,cache_key) DO UPDATE SET payload_json=excluded.payload_json,
status=excluded.status,expires_at=excluded.expires_at,updated_at=excluded.updated_at`,
		chainKey, chainID, cacheType, key, string(payload), status, expires, now.Format(time.RFC3339Nano),
	)
	return err
}

func (s *store) saveJob(item Job) error {
	_, err := s.db.Exec(`INSERT INTO enrichment_jobs (
job_id,job_type,chain_key,chain_id,status,total_items,completed_items,succeeded_items,
failed_items,skipped_items,cache_hits,started_at,updated_at,finished_at,cancellation_requested,error_summary
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(job_id) DO UPDATE SET status=excluded.status,completed_items=excluded.completed_items,
succeeded_items=excluded.succeeded_items,failed_items=excluded.failed_items,
skipped_items=excluded.skipped_items,cache_hits=excluded.cache_hits,updated_at=excluded.updated_at,
finished_at=excluded.finished_at,cancellation_requested=excluded.cancellation_requested,
error_summary=excluded.error_summary`,
		item.ID, item.JobType, item.ChainKey, item.ChainID, item.Status, item.TotalItems,
		item.CompletedItems, item.SucceededItems, item.FailedItems, item.SkippedItems, item.CacheHits,
		item.StartedAt.Format(time.RFC3339Nano), item.UpdatedAt.Format(time.RFC3339Nano),
		formatTimePointer(item.FinishedAt), boolInt(item.CancellationRequested), item.ErrorSummary)
	return err
}

func (s *store) job(id string) (Job, error) {
	var item Job
	var started, updated string
	var finished sql.NullString
	var canceled int
	err := s.db.QueryRow(`SELECT job_id,job_type,chain_key,chain_id,status,total_items,
completed_items,succeeded_items,failed_items,skipped_items,cache_hits,started_at,updated_at,
finished_at,cancellation_requested,error_summary FROM enrichment_jobs WHERE job_id=?`, id).Scan(
		&item.ID, &item.JobType, &item.ChainKey, &item.ChainID, &item.Status, &item.TotalItems,
		&item.CompletedItems, &item.SucceededItems, &item.FailedItems, &item.SkippedItems,
		&item.CacheHits, &started, &updated, &finished, &canceled, &item.ErrorSummary)
	if err != nil {
		return Job{}, err
	}
	item.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	item.FinishedAt = parseNullTime(finished)
	item.CancellationRequested = canceled == 1
	return item, nil
}

func (s *store) jobs() ([]Job, error) {
	rows, err := s.db.Query("SELECT job_id FROM enrichment_jobs ORDER BY started_at DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]Job, 0, len(ids))
	for _, id := range ids {
		item, err := s.job(id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func parseNullTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func formatTimePointer(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
