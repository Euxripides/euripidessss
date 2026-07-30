package datasourcemanager

import "time"

const (
	TypeStream  = "STREAM"
	TypeDataset = "DATASET"
	TypeRPC     = "RPC"

	StatusHealthy     = "HEALTHY"
	StatusDegraded    = "DEGRADED"
	StatusRateLimited = "RATE_LIMITED"
	StatusUnavailable = "UNAVAILABLE"
	StatusDisabled    = "DISABLED"
	StatusUnknown     = "UNKNOWN"
)

type ConfigInput struct {
	ID             string `json:"source_id,omitempty"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	Endpoint       string `json:"endpoint"`
	APIKey         string `json:"api_key,omitempty"`
	Bucket         string `json:"bucket,omitempty"`
	Region         string `json:"region,omitempty"`
	Prefix         string `json:"prefix,omitempty"`
	CacheDirectory string `json:"cache_directory,omitempty"`
	TimeoutMS      int    `json:"timeout_ms"`
	MaxConcurrency int    `json:"max_concurrency"`
	RetryCount     int    `json:"retry_count"`
	Enabled        bool   `json:"enabled"`
}

type Source struct {
	ID               string       `json:"source_id"`
	Type             string       `json:"type"`
	Provider         string       `json:"provider"`
	Name             string       `json:"name"`
	Description      string       `json:"description"`
	EndpointMasked   string       `json:"endpoint_masked"`
	SecretConfigured bool         `json:"secret_configured"`
	ChainKeys        []string     `json:"chain_keys"`
	Enabled          bool         `json:"enabled"`
	Status           string       `json:"status"`
	HealthScore      float64      `json:"health_score"`
	LatencyP50MS     float64      `json:"latency_p50_ms"`
	LatencyP95MS     float64      `json:"latency_p95_ms"`
	SuccessRate      float64      `json:"success_rate"`
	TodayRequests    int64        `json:"today_requests"`
	SuccessCount     int64        `json:"success_count"`
	FailureCount     int64        `json:"failure_count"`
	RateLimitedCount int64        `json:"rate_limited_count"`
	TimeoutCount     int64        `json:"timeout_count"`
	AverageSpeedBPS  float64      `json:"average_speed_bps"`
	LastSuccessAt    *time.Time   `json:"last_success_at,omitempty"`
	LastFailureAt    *time.Time   `json:"last_failure_at,omitempty"`
	LastError        string       `json:"last_error,omitempty"`
	CheckedAt        *time.Time   `json:"checked_at,omitempty"`
	Config           PublicConfig `json:"config"`
}

type PublicConfig struct {
	Bucket         string `json:"bucket,omitempty"`
	Region         string `json:"region,omitempty"`
	Prefix         string `json:"prefix,omitempty"`
	CacheDirectory string `json:"cache_directory,omitempty"`
	TimeoutMS      int    `json:"timeout_ms"`
	MaxConcurrency int    `json:"max_concurrency"`
	RetryCount     int    `json:"retry_count"`
}

type Overview struct {
	SourceCount   int     `json:"source_count"`
	HealthyCount  int     `json:"healthy_count"`
	AbnormalCount int     `json:"abnormal_count"`
	TodayRequests int64   `json:"today_requests"`
	CacheHitRate  float64 `json:"cache_hit_rate"`
}

type Event struct {
	SourceID   string    `json:"source_id"`
	SourceName string    `json:"source_name"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	OccurredAt time.Time `json:"occurred_at"`
}

type Snapshot struct {
	Overview Overview `json:"overview"`
	Sources  []Source `json:"sources"`
	Events   []Event  `json:"events"`
}

type TestResult struct {
	Success     bool      `json:"success"`
	SourceID    string    `json:"source_id"`
	Status      string    `json:"status"`
	LatencyMS   int64     `json:"latency_ms"`
	Dataset     string    `json:"dataset,omitempty"`
	LatestBlock uint64    `json:"latest_block,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	Message     string    `json:"message"`
}
