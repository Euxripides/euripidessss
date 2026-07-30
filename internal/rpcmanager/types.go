package rpcmanager

import "time"

const (
	StatusHealthy       = "HEALTHY"
	StatusDegraded      = "DEGRADED"
	StatusRateLimited   = "RATE_LIMITED"
	StatusUnavailable   = "UNAVAILABLE"
	StatusMisconfigured = "MISCONFIGURED"
	StatusDisabled      = "DISABLED"

	CircuitClosed   = "CLOSED"
	CircuitOpen     = "OPEN"
	CircuitHalfOpen = "HALF_OPEN"
)

type EndpointInput struct {
	Provider         string  `json:"provider"`
	ChainKey         string  `json:"chain_key"`
	DisplayName      string  `json:"display_name"`
	EndpointURL      string  `json:"endpoint_url"`
	TestEndpointURL  string  `json:"test_endpoint_url,omitempty"`
	Priority         int     `json:"priority"`
	Enabled          bool    `json:"enabled"`
	MaxRPS           float64 `json:"max_rps"`
	MaxConcurrency   int     `json:"max_concurrency"`
	RequestTimeoutMS int     `json:"request_timeout_ms"`
}

type EndpointPatch struct {
	Provider         *string  `json:"provider,omitempty"`
	ChainKey         *string  `json:"chain_key,omitempty"`
	DisplayName      *string  `json:"display_name,omitempty"`
	EndpointURL      *string  `json:"endpoint_url,omitempty"`
	TestEndpointURL  *string  `json:"test_endpoint_url,omitempty"`
	Priority         *int     `json:"priority,omitempty"`
	Enabled          *bool    `json:"enabled,omitempty"`
	MaxRPS           *float64 `json:"max_rps,omitempty"`
	MaxConcurrency   *int     `json:"max_concurrency,omitempty"`
	RequestTimeoutMS *int     `json:"request_timeout_ms,omitempty"`
}

type Endpoint struct {
	ID                     string    `json:"endpoint_id"`
	Provider               string    `json:"provider"`
	ChainKey               string    `json:"chain_key"`
	ChainID                int64     `json:"chain_id"`
	DisplayName            string    `json:"display_name"`
	EndpointHost           string    `json:"endpoint_host"`
	EndpointMasked         string    `json:"endpoint_masked"`
	SecretConfigured       bool      `json:"secret_configured"`
	TestEndpointMasked     string    `json:"test_endpoint_masked,omitempty"`
	TestEndpointConfigured bool      `json:"test_endpoint_configured"`
	Priority               int       `json:"priority"`
	Enabled                bool      `json:"enabled"`
	MaxRPS                 float64   `json:"max_rps"`
	CurrentRPS             float64   `json:"current_rps"`
	MaxConcurrency         int       `json:"max_concurrency"`
	RequestTimeoutMS       int       `json:"request_timeout_ms"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	Health                 Health    `json:"health"`
}

type Health struct {
	EndpointID               string     `json:"endpoint_id"`
	Status                   string     `json:"status"`
	HealthScore              float64    `json:"health_score"`
	LatestBlock              uint64     `json:"latest_block"`
	BlockLag                 uint64     `json:"block_lag"`
	LatencyP50MS             float64    `json:"latency_p50_ms"`
	LatencyP95MS             float64    `json:"latency_p95_ms"`
	SuccessRate5M            float64    `json:"success_rate_5m"`
	ConsecutiveFailures      int        `json:"consecutive_failures"`
	CircuitState             string     `json:"circuit_state"`
	CircuitOpenUntil         *time.Time `json:"circuit_open_until,omitempty"`
	LastSuccessAt            *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt            *time.Time `json:"last_failure_at,omitempty"`
	LastErrorCode            string     `json:"last_error_code,omitempty"`
	LastErrorMessageRedacted string     `json:"last_error_message_redacted,omitempty"`
	CheckedAt                *time.Time `json:"checked_at,omitempty"`
}

type TestResult struct {
	Success      bool   `json:"success"`
	Provider     string `json:"provider"`
	ChainKey     string `json:"chain_key"`
	ChainID      int64  `json:"chain_id"`
	LatestBlock  uint64 `json:"latest_block"`
	LatencyMS    int64  `json:"latency_ms"`
	Status       string `json:"status"`
	ErrorClass   string `json:"error_class,omitempty"`
	Suggestion   string `json:"suggestion,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	EndpointRole string `json:"endpoint_role"`
}

type Overview struct {
	ConfiguredEndpoints int     `json:"configured_endpoints"`
	HealthyEndpoints    int     `json:"healthy_endpoints"`
	DegradedEndpoints   int     `json:"degraded_endpoints"`
	TodayRequests       int64   `json:"today_requests"`
	CacheHitRate        float64 `json:"cache_hit_rate"`
	RateLimitedCount    int64   `json:"rate_limited_count"`
}

type HealthResponse struct {
	Overview  Overview              `json:"overview"`
	Endpoints []Endpoint            `json:"endpoints"`
	Routing   map[string][]Endpoint `json:"routing"`
}

type RoutingInput struct {
	EndpointIDs []string `json:"endpoint_ids"`
}

type AddressEnrichment struct {
	ChainKey         string    `json:"chain_key"`
	ChainID          int64     `json:"chain_id"`
	Address          string    `json:"address"`
	AddressType      string    `json:"address_type"`
	NativeBalanceRaw string    `json:"native_balance_raw"`
	NativeBalance    string    `json:"native_balance"`
	NativeSymbol     string    `json:"native_symbol"`
	Status           string    `json:"status"`
	Reason           string    `json:"reason"`
	Cached           bool      `json:"cached"`
	CheckedAt        time.Time `json:"checked_at"`
}

type TokenMetadata struct {
	ChainKey     string    `json:"chain_key"`
	ChainID      int64     `json:"chain_id"`
	TokenAddress string    `json:"token_address"`
	Name         string    `json:"name"`
	Symbol       string    `json:"symbol"`
	Decimals     *uint8    `json:"decimals,omitempty"`
	TotalSupply  string    `json:"total_supply,omitempty"`
	Status       string    `json:"status"`
	Cached       bool      `json:"cached"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type JobRequest struct {
	JobType  string   `json:"job_type"`
	ChainKey string   `json:"chain_key"`
	Items    []string `json:"items"`
}

type Job struct {
	ID                    string     `json:"job_id"`
	JobType               string     `json:"job_type"`
	ChainKey              string     `json:"chain_key"`
	ChainID               int64      `json:"chain_id"`
	Status                string     `json:"status"`
	TotalItems            int64      `json:"total_items"`
	CompletedItems        int64      `json:"completed_items"`
	SucceededItems        int64      `json:"succeeded_items"`
	FailedItems           int64      `json:"failed_items"`
	SkippedItems          int64      `json:"skipped_items"`
	CacheHits             int64      `json:"cache_hits"`
	StartedAt             time.Time  `json:"started_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	FinishedAt            *time.Time `json:"finished_at,omitempty"`
	CancellationRequested bool       `json:"cancellation_requested"`
	ErrorSummary          string     `json:"error_summary,omitempty"`
}
