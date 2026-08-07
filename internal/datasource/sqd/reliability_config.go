package sqd

import "time"

// ReliabilityConfig holds all configuration for the SQD reliability layer.
// It is designed to be serializable (JSON/YAML) and self-documenting.
type ReliabilityConfig struct {
	Retry   RetryConfig   `json:"retry"`
	Backoff BackoffConfig `json:"backoff"`
	Workers WorkersConfig `json:"workers"`
	Circuit CircuitConfig `json:"circuit_breaker"`
}

// RetryConfig controls which errors are retryable and how many attempts.
type RetryConfig struct {
	MaxAttempts int `json:"max_attempts"` // maximum retry attempts (default 5)
}

// BackoffConfig controls exponential backoff timing.
type BackoffConfig struct {
	Enabled  bool            `json:"enabled"` // enable backoff (default true)
	Base     time.Duration   `json:"base"`    // base interval (default 2s)
	Max      time.Duration   `json:"max"`     // max interval (default 60s)
	Interval []time.Duration `json:"-"`       // explicit backoff sequence (derived)
}

// WorkersConfig controls the adaptive worker pool.
type WorkersConfig struct {
	Normal    int `json:"normal"`    // normal worker count (default 8)
	Degraded  int `json:"degraded"`  // degraded worker count (default 4)
	Emergency int `json:"emergency"` // emergency worker count (default 1)
}

// CircuitConfig controls the circuit breaker.
type CircuitConfig struct {
	Threshold int           `json:"threshold"` // consecutive failures to open (default 5)
	Cooldown  time.Duration `json:"cooldown"`  // time to wait before half-open (default 60s)
}

// DefaultReliabilityConfig returns sensible defaults aligned with V2.1 RC2 spec.
func DefaultReliabilityConfig() ReliabilityConfig {
	return ReliabilityConfig{
		Retry: RetryConfig{
			MaxAttempts: 5,
		},
		Backoff: BackoffConfig{
			Enabled: true,
			Base:    2 * time.Second,
			Max:     60 * time.Second,
			Interval: []time.Duration{
				2 * time.Second,
				5 * time.Second,
				15 * time.Second,
				30 * time.Second,
				60 * time.Second,
			},
		},
		Workers: WorkersConfig{
			Normal:    8,
			Degraded:  4,
			Emergency: 1,
		},
		Circuit: CircuitConfig{
			Threshold: 5,
			Cooldown:  60 * time.Second,
		},
	}
}

// Validate fills zero values with defaults and returns any configuration errors.
func (c *ReliabilityConfig) Validate() {
	if c.Retry.MaxAttempts <= 0 {
		c.Retry.MaxAttempts = 5
	}
	if c.Backoff.Base <= 0 {
		c.Backoff.Base = 2 * time.Second
	}
	if c.Backoff.Max <= 0 {
		c.Backoff.Max = 60 * time.Second
	}
	if len(c.Backoff.Interval) == 0 {
		c.Backoff.Interval = DefaultReliabilityConfig().Backoff.Interval
	}
	if c.Workers.Normal <= 0 {
		c.Workers.Normal = 8
	}
	if c.Workers.Degraded <= 0 {
		c.Workers.Degraded = 4
	}
	if c.Workers.Emergency <= 0 {
		c.Workers.Emergency = 1
	}
	if c.Circuit.Threshold <= 0 {
		c.Circuit.Threshold = 5
	}
	if c.Circuit.Cooldown <= 0 {
		c.Circuit.Cooldown = 60 * time.Second
	}
}
