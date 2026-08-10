package eventdecoder

import (
	"context"
	"sort"
	"strings"
	"sync"
)

type MemoryRegistry struct {
	mu          sync.RWMutex
	definitions []EventDefinition
}

func NewMemoryRegistry(definitions ...EventDefinition) *MemoryRegistry {
	r := &MemoryRegistry{}
	for _, definition := range definitions {
		r.Add(definition)
	}
	return r
}

func (r *MemoryRegistry) Add(definition EventDefinition) {
	definition.Topic0 = normalizeHex(definition.Topic0)
	definition.Source = normalizeSource(definition.Source)
	if definition.Confidence == "" {
		definition.Confidence = defaultConfidence(definition.Source)
	}
	r.mu.Lock()
	r.definitions = append(r.definitions, definition)
	r.mu.Unlock()
}

func (r *MemoryRegistry) LookupEvent(_ context.Context, query Query) ([]EventDefinition, error) {
	topic0 := normalizeHex(query.Topic0)
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]EventDefinition, 0)
	for _, definition := range r.definitions {
		if definition.Topic0 == topic0 {
			result = append(result, cloneDefinition(definition))
		}
	}
	return result, nil
}

type MultiRegistry struct {
	registries []Registry
}

func NewMultiRegistry(registries ...Registry) *MultiRegistry {
	filtered := make([]Registry, 0, len(registries))
	for _, registry := range registries {
		if registry != nil {
			filtered = append(filtered, registry)
		}
	}
	return &MultiRegistry{registries: filtered}
}

func (r *MultiRegistry) LookupEvent(ctx context.Context, query Query) ([]EventDefinition, error) {
	result := make([]EventDefinition, 0)
	for _, registry := range r.registries {
		definitions, err := registry.LookupEvent(ctx, query)
		if err != nil {
			return nil, err
		}
		result = append(result, definitions...)
	}
	sort.SliceStable(result, func(i, j int) bool {
		pi, pj := sourcePriority(result[i].Source), sourcePriority(result[j].Source)
		if pi != pj {
			return pi < pj
		}
		return result[i].Signature < result[j].Signature
	})
	return result, nil
}

func cloneDefinition(value EventDefinition) EventDefinition {
	value.Inputs = append([]Input(nil), value.Inputs...)
	return value
}

func normalizeSource(source string) string {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case SourceVerifiedABI:
		return SourceVerifiedABI
	case SourceProtocolABI:
		return SourceProtocolABI
	case SourceLocalABI:
		return SourceLocalABI
	case SourceTopic0:
		return SourceTopic0
	default:
		return SourceRaw
	}
}

func sourcePriority(source string) int {
	switch normalizeSource(source) {
	case SourceVerifiedABI:
		return 0
	case SourceProtocolABI:
		return 1
	case SourceLocalABI:
		return 2
	case SourceTopic0:
		return 3
	default:
		return 4
	}
}

func defaultConfidence(source string) string {
	switch normalizeSource(source) {
	case SourceVerifiedABI:
		return ConfidenceHigh
	case SourceProtocolABI, SourceLocalABI:
		return ConfidenceMedium
	case SourceTopic0:
		return ConfidenceLow
	default:
		return ConfidenceUnknown
	}
}
