package cryptodownload

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const CSVDirectRemainingLimit int64 = 20000

var (
	ErrContradictoryCapabilities = errors.New("contradictory source capabilities")
	ErrInvalidSourcePolicyInput  = errors.New("invalid source policy input")
)

type DatasetCapabilities struct {
	dataset Dataset
	sources map[Source]struct{}
}

func NewDatasetCapabilities(dataset Dataset, sources ...Source) (DatasetCapabilities, error) {
	if !dataset.valid() {
		return DatasetCapabilities{}, fmt.Errorf("capability dataset %q: %w", dataset, ErrUnknownDataset)
	}
	capabilities := DatasetCapabilities{dataset: dataset, sources: make(map[Source]struct{}, len(sources))}
	for _, source := range sources {
		if !source.valid() {
			return DatasetCapabilities{}, fmt.Errorf("capability source %q: %w", source, ErrUnknownSource)
		}
		if _, exists := capabilities.sources[source]; exists {
			return DatasetCapabilities{}, fmt.Errorf("duplicate capability %q: %w", source, ErrContradictoryCapabilities)
		}
		capabilities.sources[source] = struct{}{}
	}
	return capabilities, nil
}

func (capabilities DatasetCapabilities) Supports(source Source) bool {
	_, ok := capabilities.sources[source]
	return ok
}

type SourcePolicyInput struct {
	Mode            DownloadMode
	Dataset         Dataset
	Total           int64
	TotalKnown      bool
	Remaining       int64
	RemainingKnown  bool
	Capabilities    DatasetCapabilities
	EmailConfigured bool
	Health          SourceHealthSnapshot
	Now             time.Time
}

type SourceDecision struct {
	RequestedMode DownloadMode
	Dataset       Dataset
	Chosen        Source
	Reason        string
	FallbackChain []Source
	Forced        bool
	Available     bool
}

func DecideSource(input SourcePolicyInput) (SourceDecision, error) {
	if err := input.validate(); err != nil {
		return SourceDecision{}, err
	}
	switch input.Mode {
	case DownloadModeBrowser:
		return input.forcedDecision(SourceBrowser), nil
	case DownloadModeCSVDirect:
		return input.forcedDecision(SourceCSVDirect), nil
	case DownloadModeCSVEmail:
		return input.forcedDecision(SourceCSVEmail), nil
	case DownloadModeRPC:
		return input.forcedDecision(SourceRPC), nil
	case DownloadModeLegacyCSV:
		return input.rankedDecision([]Source{SourceCSVDirect, SourceCSVEmail}, true), nil
	case DownloadModeAuto:
		return input.rankedDecision([]Source{SourceCSVDirect, SourceCSVEmail, SourceBrowser, SourceRPC}, false), nil
	default:
		return SourceDecision{}, fmt.Errorf("mode %q: %w", input.Mode, ErrUnknownDownloadMode)
	}
}

func (input SourcePolicyInput) validate() error {
	if !input.Mode.valid() || !input.Dataset.valid() {
		return fmt.Errorf("mode=%q dataset=%q: %w", input.Mode, input.Dataset, ErrInvalidSourcePolicyInput)
	}
	if input.Capabilities.dataset != input.Dataset {
		return fmt.Errorf("selected=%q capabilities=%q: %w", input.Dataset, input.Capabilities.dataset, ErrContradictoryCapabilities)
	}
	if input.Total < 0 || input.Remaining < 0 || (!input.TotalKnown && input.Total != 0) || (!input.RemainingKnown && input.Remaining != 0) {
		return fmt.Errorf("total=%d remaining=%d: %w", input.Total, input.Remaining, ErrInvalidSourcePolicyInput)
	}
	if input.TotalKnown && input.RemainingKnown && input.Remaining > input.Total {
		return fmt.Errorf("remaining %d exceeds total %d: %w", input.Remaining, input.Total, ErrInvalidSourcePolicyInput)
	}
	return nil
}

func (input SourcePolicyInput) forcedDecision(source Source) SourceDecision {
	decision := SourceDecision{
		RequestedMode: input.Mode, Dataset: input.Dataset, Chosen: source,
		Reason: "forced " + string(input.Mode) + " mode; fallback disabled", Forced: true, Available: true,
	}
	if !input.Capabilities.Supports(source) {
		decision.Available = false
		decision.Reason += "; source is not capable for dataset"
	}
	if source == SourceCSVEmail && !input.EmailConfigured {
		decision.Available = false
		decision.Reason += "; email is not configured"
	}
	if blocked, reason := input.blockReason(source); blocked {
		decision.Available = false
		decision.Reason += "; " + reason
	}
	return decision
}

func (input SourcePolicyInput) rankedDecision(order []Source, legacyCSV bool) SourceDecision {
	available := make([]Source, 0, len(order))
	skipped := make([]string, 0, len(order))
	degraded := make([]Source, 0, len(order))
	for _, source := range order {
		if !input.Capabilities.Supports(source) {
			continue
		}
		if blocked, reason := input.blockReason(source); blocked {
			skipped = append(skipped, string(source)+" skipped because "+reason)
			continue
		}
		if input.healthFor(source).State == HealthDegraded {
			degraded = append(degraded, source)
			continue
		}
		available = append(available, source)
	}
	available = append(available, degraded...)
	if len(available) == 0 {
		return SourceDecision{RequestedMode: input.Mode, Dataset: input.Dataset, Chosen: SourceUnavailable, Reason: "no capable healthy source for dataset " + string(input.Dataset)}
	}
	chosen := available[0]
	reason := input.decisionReason(chosen, skipped, legacyCSV)
	return SourceDecision{
		RequestedMode: input.Mode, Dataset: input.Dataset, Chosen: chosen, Reason: reason,
		FallbackChain: append([]Source(nil), available[1:]...), Available: true,
	}
}

func (input SourcePolicyInput) blockReason(source Source) (bool, string) {
	if source == SourceCSVEmail && !input.EmailConfigured {
		return true, "email is not configured"
	}
	if source == SourceCSVDirect && input.RemainingKnown && input.Remaining > CSVDirectRemainingLimit {
		return true, fmt.Sprintf("remaining %d exceeds direct limit %d", input.Remaining, CSVDirectRemainingLimit)
	}
	health := input.healthFor(source)
	if health.State == HealthCircuitOpen && health.CircuitOpenUntil.After(input.Now) {
		reason := "circuit is open"
		if health.LastFailure != "" {
			reason += " after " + string(health.LastFailure)
		}
		return true, reason
	}
	return false, ""
}

func (input SourcePolicyInput) healthFor(source Source) SourceHealth {
	if health, ok := input.Health.statuses[source]; ok {
		return health
	}
	return SourceHealth{Source: source, State: HealthHealthy}
}

func (input SourcePolicyInput) decisionReason(chosen Source, skipped []string, legacyCSV bool) string {
	if legacyCSV {
		if chosen == SourceCSVDirect {
			return "legacy csv selected csv-direct; fallback restricted to csv-email"
		}
		return "legacy csv selected " + string(chosen) + "; " + strings.Join(skipped, "; ")
	}
	if input.healthFor(chosen).State == HealthDegraded {
		if len(skipped) > 0 {
			return "auto selected " + string(chosen) + "; " + strings.Join(skipped, "; ") + "; source health is degraded"
		}
		return "auto selected " + string(chosen) + "; source health is degraded"
	}
	if len(skipped) > 0 {
		return "auto selected " + string(chosen) + "; " + strings.Join(skipped, "; ")
	}
	if chosen == SourceCSVDirect && input.RemainingKnown {
		return fmt.Sprintf("auto selected csv-direct; healthy and remaining %d is within direct limit %d", input.Remaining, CSVDirectRemainingLimit)
	}
	return "auto selected " + string(chosen) + "; first healthy capable source"
}
