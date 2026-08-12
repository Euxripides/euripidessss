package downloadscheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testGate(t *testing.T, usage *CloudUsageStore, budget CloudBudget) *CloudAdmissionGate {
	t.Helper()
	return NewCloudAdmissionGate(usage, NewProviderHealthTracker(DefaultProviderHealthConfig()), budget)
}

func testRequirement(d Dataset) Requirement {
	return Requirement{
		ID: "t1", PlanID: "p1", Dataset: d, ChainKey: "bsc",
		Addresses: []string{"0x1111111111111111111111111111111111111111"},
	}
}

func testRuntime() CloudRuntimeStatus {
	return CloudRuntimeStatus{State: "READY", Available: true, Mode: "local"}
}

func testStates(states ...ProviderState) map[ProviderKind]ProviderStateInfo {
	out := map[ProviderKind]ProviderStateInfo{}
	kinds := []ProviderKind{ProviderSQD, ProviderRPC, ProviderAWS, ProviderBrowser}
	for i, k := range kinds {
		st := ProviderHealthy
		if i < len(states) {
			st = states[i]
		}
		out[k] = ProviderStateInfo{Provider: k, State: st}
	}
	return out
}

func TestAdmissionLocalCoverageFull(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	coverage := &CoverageResult{
		Items: []Coverage{{Dataset: DatasetTokenTransfer, Have: true, TxCount: 5}},
	}
	d := gate.CanUseSQDCloud(testRequirement(DatasetTokenTransfer), coverage, nil, testStates(), testRuntime())
	if d.Allowed {
		t.Fatal("full local coverage must reject cloud")
	}
	if d.Reason == "" {
		t.Fatal("missing reject reason")
	}
}

func TestAdmissionNormalProviderAvailable(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	candidates := []ProviderScore{{Provider: ProviderSQD, Tier: TierNormal, Available: true}}
	d := gate.CanUseSQDCloud(testRequirement(DatasetTokenTransfer), nil, candidates, testStates(ProviderDegraded), testRuntime())
	if d.Allowed {
		t.Fatal("single degraded provider must not trigger cloud")
	}
	if d.NormalProvidersExhausted {
		t.Fatal("DEGRADED still counts as usable")
	}
}

func TestAdmissionAllNormalExhausted(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	candidates := []ProviderScore{
		{Provider: ProviderSQD, Tier: TierNormal},
		{Provider: ProviderRPC, Tier: TierNormal},
	}
	d := gate.CanUseSQDCloud(
		testRequirement(DatasetTokenTransfer), nil, candidates,
		testStates(ProviderCircuitOpen, ProviderRateLimited), testRuntime(),
	)
	if !d.Allowed {
		t.Fatalf("all normal providers exhausted should allow cloud, got reason=%s", d.Reason)
	}
}

func TestAdmissionBudgetExceeded(t *testing.T) {
	dir := t.TempDir()
	usage := NewCloudUsageStore(filepath.Join(dir, "cloud_usage.json"))
	rec := CloudUsageRecord{
		JobID: "j1", StartedAt: time.Now().Add(-10 * time.Minute),
		FinishedAt: time.Now(), DurationMinutes: 60,
	}
	if err := usage.Record(rec); err != nil {
		t.Fatal(err)
	}
	gate := testGate(t, usage, DefaultCloudBudget())
	candidates := []ProviderScore{{Provider: ProviderSQD, Tier: TierNormal}}
	d := gate.CanUseSQDCloud(
		testRequirement(DatasetTokenTransfer), nil, candidates,
		testStates(ProviderCircuitOpen), testRuntime(),
	)
	if d.Allowed {
		t.Fatal("budget exceeded must reject cloud")
	}
	if d.Reason != "CLOUD_BUDGET_EXCEEDED：当日 Cloud 用量已达上限" {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
	_ = os.RemoveAll(dir)
}

func TestAdmissionNotEligible(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	req := testRequirement(DatasetTokenTransfer)
	no := false
	req.CloudEligible = &no
	candidates := []ProviderScore{{Provider: ProviderSQD, Tier: TierNormal}}
	d := gate.CanUseSQDCloud(req, nil, candidates, testStates(ProviderCircuitOpen), testRuntime())
	if d.Allowed {
		t.Fatal("cloud_eligible=false must reject")
	}
	if d.Reason != "CLOUD_NOT_ELIGIBLE：后台/非关键任务不允许触发 Cloud" {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
}

func TestAdmissionRuntimeUnavailable(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	candidates := []ProviderScore{{Provider: ProviderSQD, Tier: TierNormal}}
	rt := CloudRuntimeStatus{State: "FAILED", Available: false, Reason: "部署失败"}
	d := gate.CanUseSQDCloud(testRequirement(DatasetTokenTransfer), nil, candidates, testStates(ProviderCircuitOpen), rt)
	if d.Allowed {
		t.Fatal("runtime unavailable must reject")
	}
}

func TestAdmissionDoesNotTrustAvailableFlagForFailedState(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	candidates := []ProviderScore{{Provider: ProviderSQD, Tier: TierNormal}}
	rt := CloudRuntimeStatus{State: "FAILED", Available: true, Reason: "stale available flag"}
	d := gate.CanUseSQDCloud(testRequirement(DatasetTokenTransfer), nil, candidates, testStates(ProviderCircuitOpen), rt)
	if d.Allowed || d.RuntimeAvailable {
		t.Fatalf("failed runtime must be rejected even if available flag is stale: %+v", d)
	}
}

func TestCloudUsageRejectsNegativeMinutes(t *testing.T) {
	usage := NewCloudUsageStore(filepath.Join(t.TempDir(), "cloud_usage.json"))
	if err := usage.Record(CloudUsageRecord{JobID: "bad", DurationMinutes: -10}); err == nil {
		t.Fatal("negative usage must be rejected")
	}
	if got := usage.TodayUsedMinutes(); got != 0 {
		t.Fatalf("used minutes=%d, want 0", got)
	}
}

func TestAdmissionAbsentRuntimeIsDeployableButNotAvailable(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	candidates := []ProviderScore{{Provider: ProviderSQD, Tier: TierNormal}}
	rt := CloudRuntimeStatus{
		State: "ABSENT", Mode: "cloud", Available: false,
		DeploymentKeyConfigured: true, R2Configured: true,
		Reason: "Reconcile：未检测到托管 Worker",
	}
	d := gate.CanUseSQDCloud(testRequirement(DatasetTokenTransfer), nil, candidates, testStates(ProviderCircuitOpen), rt)
	if !d.Allowed || d.RuntimeAvailable || !d.RuntimeDeployable {
		t.Fatalf("absent deployable decision = %+v", d)
	}
	rt.State = "DEPLOYING"
	d = gate.CanUseSQDCloud(testRequirement(DatasetTokenTransfer), nil, candidates, testStates(ProviderCircuitOpen), rt)
	if !d.Allowed || d.RuntimeAvailable || !d.RuntimeDeployable {
		t.Fatalf("deploying runtime must be waitable without being available: %+v", d)
	}
}

func TestAdmissionAbsentRuntimeMissingR2Rejected(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	candidates := []ProviderScore{{Provider: ProviderSQD, Tier: TierNormal}}
	rt := CloudRuntimeStatus{
		State: "ABSENT", Mode: "cloud", Available: false,
		DeploymentKeyConfigured: true, R2Configured: false,
		Reason: "R2/S3 Job Queue 未配置",
	}
	d := gate.CanUseSQDCloud(testRequirement(DatasetTokenTransfer), nil, candidates, testStates(ProviderCircuitOpen), rt)
	if d.Allowed || d.RuntimeDeployable {
		t.Fatalf("absent runtime without R2 must reject: %+v", d)
	}
}

func TestAdmissionCredentialsNotConfigured(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	candidates := []ProviderScore{{Provider: ProviderSQD, Tier: TierNormal}}
	rt := CloudRuntimeStatus{
		State: "NOT_CONFIGURED", Available: false,
		Reason: "SQD Cloud 模式缺少 SQD_DEPLOY_KEY（密钥仅允许来自环境变量）",
	}
	d := gate.CanUseSQDCloud(testRequirement(DatasetTokenTransfer), nil, candidates, testStates(ProviderCircuitOpen), rt)
	if d.Allowed {
		t.Fatal("credentials missing must reject")
	}
	if len(d.Reason) < len("CREDENTIALS_NOT_CONFIGURED") || d.Reason[:len("CREDENTIALS_NOT_CONFIGURED")] != "CREDENTIALS_NOT_CONFIGURED" {
		t.Fatalf("reason = %s, want CREDENTIALS_NOT_CONFIGURED prefix", d.Reason)
	}
}

func TestAdmissionUnsupportedDataset(t *testing.T) {
	gate := testGate(t, NewCloudUsageStore(""), DefaultCloudBudget())
	d := gate.CanUseSQDCloud(testRequirement(DatasetBalance), nil, nil, testStates(), testRuntime())
	if d.Allowed {
		t.Fatal("balance dataset must reject cloud")
	}
}
