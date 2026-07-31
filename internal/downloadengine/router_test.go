package downloadengine

import (
	"context"
	"testing"
	"time"
)

// ── Mock Providers for testing ──

type mockStreamingProvider struct {
	name         string
	caps         ProviderCapabilities
	health       ProviderHealth
	datasetTypes []string
}

func (m *mockStreamingProvider) Name() string                          { return m.name }
func (m *mockStreamingProvider) Capabilities() ProviderCapabilities     { return m.caps }
func (m *mockStreamingProvider) Health(_ context.Context) ProviderHealth { return m.health }
func (m *mockStreamingProvider) Estimate(_ context.Context, _ StreamEstimateRequest) (*EstimateResult, error) {
	return &EstimateResult{}, nil
}
func (m *mockStreamingProvider) ExecuteStream(_ context.Context, _ StreamRequest) (<-chan StreamRecord, <-chan error) {
	return nil, nil
}

type mockObjectProvider struct {
	name         string
	caps         ProviderCapabilities
	health       ProviderHealth
	datasetTypes []string
}

func (m *mockObjectProvider) Name() string                          { return m.name }
func (m *mockObjectProvider) Capabilities() ProviderCapabilities     { return m.caps }
func (m *mockObjectProvider) Health(_ context.Context) ProviderHealth { return m.health }
func (m *mockObjectProvider) Estimate(_ context.Context, _ ObjectEstimateRequest) (*EstimateResult, error) {
	return &EstimateResult{}, nil
}
func (m *mockObjectProvider) ExecuteObject(_ context.Context, _ ObjectRequest) (*ObjectResult, error) {
	return &ObjectResult{}, nil
}

func TestRouterResolveStreaming(t *testing.T) {
	r := NewRouter()
	r.RegisterStreaming(&mockStreamingProvider{
		name: "SQD",
		caps: ProviderCapabilities{
			Name:              "SQD",
			SupportsStreaming: true,
			DatasetTypes:      []string{"transactions", "logs", "traces"},
		},
		health: ProviderHealth{Name: "SQD", Status: ProviderHealthy, LastCheck: time.Now()},
	})

	p, ok := r.ResolveStreaming("transactions", "bsc")
	if !ok {
		t.Fatal("should resolve SQD for transactions")
	}
	if p.Name() != "SQD" {
		t.Errorf("expected SQD, got %s", p.Name())
	}
}

func TestRouterResolveFailsWhenUnhealthy(t *testing.T) {
	r := NewRouter()
	r.RegisterStreaming(&mockStreamingProvider{
		name: "SQD",
		caps: ProviderCapabilities{
			Name:              "SQD",
			SupportsStreaming: true,
			DatasetTypes:      []string{"transactions"},
		},
		health: ProviderHealth{Name: "SQD", Status: ProviderUnavailable, LastCheck: time.Now()},
	})
	r.UpdateHealth(context.Background()) // populate health cache

	_, ok := r.ResolveStreaming("transactions", "bsc")
	if ok {
		t.Fatal("should NOT resolve unhealthy SQD")
	}
}

func TestRouterResolveObject(t *testing.T) {
	r := NewRouter()
	r.RegisterObject(&mockObjectProvider{
		name: "AWS",
		caps: ProviderCapabilities{
			Name:           "AWS",
			SupportsObject: true,
			DatasetTypes:   []string{"transactions"},
		},
		health: ProviderHealth{Name: "AWS", Status: ProviderHealthy, LastCheck: time.Now()},
	})

	p, ok := r.ResolveObject("transactions", "bsc")
	if !ok {
		t.Fatal("should resolve AWS for transactions")
	}
	if p.Name() != "AWS" {
		t.Errorf("expected AWS, got %s", p.Name())
	}
}

func TestRouterCapabilitiesCache(t *testing.T) {
	r := NewRouter()
	r.RegisterStreaming(&mockStreamingProvider{
		name: "SQD",
		caps: ProviderCapabilities{Name: "SQD", SupportsStreaming: true, DatasetTypes: []string{"transactions"}},
		health: ProviderHealth{Name: "SQD", Status: ProviderHealthy, LastCheck: time.Now()},
	})
	r.RegisterObject(&mockObjectProvider{
		name: "AWS",
		caps: ProviderCapabilities{Name: "AWS", SupportsObject: true, DatasetTypes: []string{"transactions"}},
		health: ProviderHealth{Name: "AWS", Status: ProviderHealthy, LastCheck: time.Now()},
	})

	caps := r.ResolveCapabilities()
	if len(caps) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(caps))
	}
}
