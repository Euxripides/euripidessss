package cryptodownload

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type FailureClass string

const (
	FailureUnknown            FailureClass = "unknown"
	FailurePermanentSignature FailureClass = "permanent-signature"
	FailureAuthConfig         FailureClass = "auth-config"
	FailureRateLimit          FailureClass = "rate-limit"
	FailureUpstreamTransient  FailureClass = "upstream-transient"
	FailureObjectNotReady     FailureClass = "object-not-ready"
	FailureTransportEOF       FailureClass = "transport-eof"
	FailurePayloadInvalid     FailureClass = "payload-invalid"
	FailureIncomplete         FailureClass = "incomplete"
)

var (
	ErrUnknownFailureClass = errors.New("unknown failure class")
	ErrUnknownHealthState  = errors.New("unknown health state")
	ErrContradictoryHealth = errors.New("contradictory source health")
	ErrPermanentSignature  = errors.New("permanent signature failure")
	ErrAuthConfig          = errors.New("authentication or configuration failure")
	ErrRateLimit           = errors.New("rate limit failure")
	ErrUpstreamTransient   = errors.New("transient upstream failure")
	ErrObjectNotReady      = errors.New("object not ready")
	ErrTransportEOF        = errors.New("transport EOF")
	ErrPayloadInvalid      = errors.New("invalid payload")
	ErrIncomplete          = errors.New("incomplete result")
)

type HealthState string

const (
	HealthHealthy     HealthState = "healthy"
	HealthDegraded    HealthState = "degraded"
	HealthCircuitOpen HealthState = "circuit-open"
)

type SourceHealth struct {
	Source           Source
	State            HealthState
	CircuitOpenUntil time.Time
	LastFailure      FailureClass
}

type SourceHealthSnapshot struct {
	statuses map[Source]SourceHealth
}

func NewSourceHealthSnapshot(statuses ...SourceHealth) (SourceHealthSnapshot, error) {
	snapshot := SourceHealthSnapshot{statuses: make(map[Source]SourceHealth, len(statuses))}
	for _, status := range statuses {
		if !status.Source.valid() {
			return SourceHealthSnapshot{}, fmt.Errorf("health source %q: %w", status.Source, ErrUnknownSource)
		}
		if !status.State.valid() {
			return SourceHealthSnapshot{}, fmt.Errorf("health state %q: %w", status.State, ErrUnknownHealthState)
		}
		if _, exists := snapshot.statuses[status.Source]; exists {
			return SourceHealthSnapshot{}, fmt.Errorf("duplicate health for %q: %w", status.Source, ErrContradictoryHealth)
		}
		snapshot.statuses[status.Source] = status
	}
	return snapshot, nil
}

func ParseHealthState(raw string) (HealthState, error) {
	state := HealthState(NormalizeRequestedSource(raw))
	if !state.valid() {
		return "", fmt.Errorf("health state %q: %w", raw, ErrUnknownHealthState)
	}
	return state, nil
}

func (state HealthState) valid() bool {
	switch state {
	case HealthHealthy, HealthDegraded, HealthCircuitOpen:
		return true
	default:
		return false
	}
}

type SourceFailureError struct {
	Source Source
	Class  FailureClass
	Op     string
	Cause  error
}

func NewSourceFailure(source Source, class FailureClass, op string, cause error) error {
	return &SourceFailureError{Source: source, Class: class, Op: op, Cause: cause}
}

func NewHTTPSourceFailure(source Source, status int, providerCode string, cause error) error {
	class := FailurePayloadInvalid
	switch {
	case strings.EqualFold(strings.TrimSpace(providerCode), "50113"):
		class = FailurePermanentSignature
	case status == 401 || status == 403:
		class = FailureAuthConfig
	case status == 429:
		class = FailureRateLimit
	case status == 404 && strings.EqualFold(strings.TrimSpace(providerCode), "NoSuchKey"):
		class = FailureObjectNotReady
	case status >= 500 && status <= 599:
		class = FailureUpstreamTransient
	}
	return NewSourceFailure(source, class, fmt.Sprintf("http status %d", status), cause)
}

func (e *SourceFailureError) Error() string {
	message := fmt.Sprintf("%s %s: %s", e.Source, e.Op, e.Class)
	if e.Cause != nil {
		return message + ": " + e.Cause.Error()
	}
	return message
}

func (e *SourceFailureError) Unwrap() error {
	return e.Cause
}

func (e *SourceFailureError) Is(target error) bool {
	switch target {
	case ErrPermanentSignature:
		return e.Class == FailurePermanentSignature
	case ErrAuthConfig:
		return e.Class == FailureAuthConfig
	case ErrRateLimit:
		return e.Class == FailureRateLimit
	case ErrUpstreamTransient:
		return e.Class == FailureUpstreamTransient
	case ErrObjectNotReady:
		return e.Class == FailureObjectNotReady
	case ErrTransportEOF:
		return e.Class == FailureTransportEOF
	case ErrPayloadInvalid:
		return e.Class == FailurePayloadInvalid
	case ErrIncomplete:
		return e.Class == FailureIncomplete
	default:
		return false
	}
}

func ClassifyFailure(err error) FailureClass {
	if err == nil {
		return FailureUnknown
	}
	var typed *SourceFailureError
	if errors.As(err, &typed) {
		return typed.Class
	}
	switch {
	case errors.Is(err, ErrPermanentSignature):
		return FailurePermanentSignature
	case errors.Is(err, ErrAuthConfig):
		return FailureAuthConfig
	case errors.Is(err, ErrRateLimit):
		return FailureRateLimit
	case errors.Is(err, ErrUpstreamTransient):
		return FailureUpstreamTransient
	case errors.Is(err, ErrObjectNotReady):
		return FailureObjectNotReady
	case errors.Is(err, ErrTransportEOF), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return FailureTransportEOF
	case errors.Is(err, ErrPayloadInvalid):
		return FailurePayloadInvalid
	case errors.Is(err, ErrIncomplete):
		return FailureIncomplete
	default:
		return FailureUnknown
	}
}

func ParseFailureClass(raw string) (FailureClass, error) {
	class := FailureClass(NormalizeRequestedSource(raw))
	switch class {
	case FailurePermanentSignature,
		FailureAuthConfig,
		FailureRateLimit,
		FailureUpstreamTransient,
		FailureObjectNotReady,
		FailureTransportEOF,
		FailurePayloadInvalid,
		FailureIncomplete:
		return class, nil
	default:
		return "", fmt.Errorf("failure class %q: %w", raw, ErrUnknownFailureClass)
	}
}
