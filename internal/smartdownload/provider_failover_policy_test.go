package smartdownload

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type policyTestProvider struct {
	name       string
	fail       bool
	waitForCtx bool
	mu         sync.Mutex
	calls      int
}

func (p *policyTestProvider) Name() string         { return p.name }
func (p *policyTestProvider) Available() bool      { return true }
func (p *policyTestProvider) Supports(string) bool { return true }
func (p *policyTestProvider) Probe(context.Context, ProbeRequest) (ProbeResult, error) {
	return ProbeResult{EstimatedRows: 1, EstimatedBytes: 128, Confidence: 0.9}, nil
}
func (p *policyTestProvider) ExecuteRange(ctx context.Context, req RangeRequest) (*ProviderResult, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.waitForCtx {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if p.fail {
		return nil, fmt.Errorf("%s injected failure", p.name)
	}
	return NewMockNamedProvider(p.name).ExecuteRange(ctx, req)
}
func (p *policyTestProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newPolicyService(t *testing.T, addresses []string, dataset string, retryLimit int, timeout time.Duration, adapters ...ProviderAdapter) (*Store, *Service, string) {
	t.Helper()
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 1
	opts.RetryLimit = retryLimit
	opts.DefaultEndBlock = 1
	opts.RangeChunkSize = 2
	opts.ProviderAttemptTimeout = timeout
	svc := NewService(store, opts, NewJSONLPartWriter(root))
	for _, adapter := range adapters {
		svc.RegisterAdapter(adapter)
	}
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: addresses, Datasets: []string{dataset},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 1, ToBlock: 1},
	})
	if err != nil {
		svc.Shutdown()
		t.Fatal(err)
	}
	return store, svc, resp.Batch.ID
}

func TestProviderAttemptTimeoutSwitchesWithoutWaitingForFallback(t *testing.T) {
	stuck := &policyTestProvider{name: "csv", waitForCtx: true}
	sqd := &policyTestProvider{name: "sqd"}
	store, svc, batchID := newPolicyService(t, []string{addrA}, DatasetTransactions, 0, 40*time.Millisecond, stuck, sqd)
	t.Cleanup(svc.Shutdown)
	started := time.Now()
	if err := svc.Start(batchID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, batchID)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("provider timeout failover took %s", elapsed)
	}
	for _, r := range store.ListRanges() {
		if r.Provider != "sqd" || r.Attempts != 1 {
			t.Fatalf("range did not switch after bounded timeout: %+v", r)
		}
	}
	if stuck.Calls() != 1 || sqd.Calls() != 1 {
		t.Fatalf("calls csv=%d sqd=%d, want 1/1", stuck.Calls(), sqd.Calls())
	}
}

func TestHighFrequencyFailuresSkipProviderForLaterAddresses(t *testing.T) {
	csv := &policyTestProvider{name: "csv", fail: true}
	sqd := &policyTestProvider{name: "sqd"}
	addresses := []string{
		"0x0000000000000000000000000000000000000011",
		"0x0000000000000000000000000000000000000012",
		"0x0000000000000000000000000000000000000013",
	}
	_, svc, batchID := newPolicyService(t, addresses, DatasetTransactions, 0, time.Second, csv, sqd)
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(batchID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, batchID)
	if csv.Calls() != 2 {
		t.Fatalf("high-frequency failed provider calls=%d, want 2 before cooldown", csv.Calls())
	}
	if sqd.Calls() != len(addresses) {
		t.Fatalf("next provider calls=%d, want %d", sqd.Calls(), len(addresses))
	}
	health := svc.scheduler.Health().Snapshot()["csv"]
	if remaining := time.Until(health.CooldownUntil); remaining < 9*time.Minute {
		t.Fatalf("cooldown remaining=%s, want about 10m", remaining)
	}
}

func TestAllRegularProvidersRunBeforeCloudDespiteRetryLimit(t *testing.T) {
	csv := &policyTestProvider{name: "csv", fail: true}
	sqd := &policyTestProvider{name: "sqd", fail: true}
	rpc := &policyTestProvider{name: "rpc", fail: true}
	cloud := NewMockCloudProvider()
	store, svc, batchID := newPolicyService(t, []string{addrA}, DatasetTokenTransfers, 0, time.Second, csv, sqd, rpc, cloud)
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(batchID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, batchID)
	if csv.Calls() != 1 || sqd.Calls() != 1 || rpc.Calls() != 1 {
		t.Fatalf("regular calls csv=%d sqd=%d rpc=%d, want each once", csv.Calls(), sqd.Calls(), rpc.Calls())
	}
	for _, r := range store.ListRanges() {
		if r.Provider != "sqd_cloud" || r.Attempts != 3 {
			t.Fatalf("cloud was not the final provider: %+v", r)
		}
	}
}

func TestProviderAttemptTimeoutIsCappedAtTenMinutes(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.ProviderAttemptTimeout = 45 * time.Minute
	svc := NewService(store, opts, NewJSONLPartWriter(root))
	defer svc.Shutdown()
	if svc.Options().ProviderAttemptTimeout != 10*time.Minute {
		t.Fatalf("attempt timeout=%s, want hard cap 10m", svc.Options().ProviderAttemptTimeout)
	}
}

func TestProviderHealthUsesRollingFailureWindow(t *testing.T) {
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	health := NewProviderHealthTracker()
	health.now = func() time.Time { return now }
	health.RecordFailure("sqd", fmt.Errorf("503 temporary"))
	now = now.Add(providerFailureWindow + time.Second)
	health.RecordFailure("sqd", fmt.Errorf("503 temporary"))
	if health.Exhausted("sqd") {
		t.Fatal("sparse failures must not open the circuit")
	}
	health.RecordFailure("sqd", fmt.Errorf("503 temporary"))
	if !health.Exhausted("sqd") {
		t.Fatal("two failures inside the rolling window must open the circuit")
	}
	if remaining := health.Snapshot()["sqd"].CooldownUntil.Sub(now); remaining != providerCooldown {
		t.Fatalf("cooldown=%s, want %s", remaining, providerCooldown)
	}
}

func TestProviderTimeoutImmediatelyOpensCooldown(t *testing.T) {
	health := NewProviderHealthTracker()
	health.RecordFailure("csv", context.DeadlineExceeded)
	if !health.Exhausted("csv") {
		t.Fatal("a full provider timeout must immediately open cooldown")
	}
}
