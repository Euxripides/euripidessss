package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/etl/backend/internal/cloudruntime"
	"github.com/etl/backend/internal/downloadscheduler"
	"github.com/etl/backend/internal/smartdownload"
)

func TestSchedulerDebugHandlerRejectsRemoteAddressAndIgnoresForwardedHeaders(t *testing.T) {
	handler := NewSchedulerDebugHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/debug/sqd/metrics", nil)
	req.RemoteAddr = "203.0.113.10:49152"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Real-IP", "127.0.0.1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCollectCloudQueueAggregatesClaimAndLeaseCounters(t *testing.T) {
	runtime := downloadscheduler.CloudRuntimeStatus{WorkerSlots: []cloudruntime.WorkerSlotStatus{
		{Alive: true, QueueClaimTotal: 10, QueueClaimSuccessTotal: 2, QueueClaimEmptyTotal: 7, QueueClaimFailedTotal: 1, LeaseAcquireTotal: 3, LeaseAcquireSuccessTotal: 2, LeaseAcquireFailedTotal: 1, LeaseWriteMS: 15},
		{Alive: true, QueueClaimTotal: 8, QueueClaimSuccessTotal: 1, QueueClaimEmptyTotal: 7, LeaseAcquireTotal: 1, LeaseAcquireSuccessTotal: 1, LeaseWriteMS: 25},
	}}
	got := collectCloudQueue(runtime, nil)
	if got.QueueClaimTotal != 18 || got.QueueClaimSuccessTotal != 3 || got.QueueClaimEmptyTotal != 14 || got.QueueClaimFailedTotal != 1 {
		t.Fatalf("unexpected claim counters: %+v", got)
	}
	if got.LeaseAcquireTotal != 4 || got.LeaseAcquireSuccessTotal != 3 || got.LeaseAcquireFailedTotal != 1 {
		t.Fatalf("unexpected lease counters: %+v", got)
	}
	if len(got.LeaseWrite) != 2 || got.LeaseWrite[0] != 15 || got.LeaseWrite[1] != 25 {
		t.Fatalf("unexpected lease write samples: %+v", got.LeaseWrite)
	}
}

func TestSchedulerDebugHandlerAcceptsIPv4AndIPv6Loopback(t *testing.T) {
	for _, remoteAddr := range []string{"127.0.0.1:8000", "[::1]:8000", "127.0.0.1"} {
		if !isLoopbackRemoteAddr(remoteAddr) {
			t.Errorf("expected loopback: %q", remoteAddr)
		}
	}
	for _, remoteAddr := range []string{"203.0.113.1:8000", "example.com:8000", ""} {
		if isLoopbackRemoteAddr(remoteAddr) {
			t.Errorf("expected non-loopback: %q", remoteAddr)
		}
	}
}

func TestSQDSafetyGateRequiresValidPhysicalRequestDenominator(t *testing.T) {
	tests := []struct {
		name   string
		metric sqdMetricAggregate
		status string
		reason string
	}{
		{name: "missing", metric: sqdMetricAggregate{}, status: "BLOCKED", reason: "MISSING_REQUEST_TOTAL"},
		{name: "mismatch", metric: sqdMetricAggregate{RequestsTotal: 10, SuccessTotal: 8, FailedTotal: 1}, status: "INVALID", reason: "REQUEST_COUNTER_MISMATCH"},
		{name: "out of range", metric: sqdMetricAggregate{RequestsTotal: 10, SuccessTotal: 9, FailedTotal: 1, Total503: 2}, status: "INVALID", reason: "REQUEST_COUNTER_OUT_OF_RANGE"},
		{name: "pass", metric: sqdMetricAggregate{RequestsTotal: 100, SuccessTotal: 95, FailedTotal: 5, Total503: 1, Total429: 1, TimeoutTotal: 1, CancelledTotal: 2, RetryTotal: 10}, status: "PASS", reason: "THRESHOLDS_SATISFIED"},
		{name: "503 fail", metric: sqdMetricAggregate{RequestsTotal: 100, SuccessTotal: 94, FailedTotal: 6, Total503: 6}, status: "FAIL", reason: "5XX_RATE_TOO_HIGH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, reason := evaluateSQDSafetyGate(tt.metric)
			if status != tt.status || reason != tt.reason {
				t.Fatalf("got %s/%s, want %s/%s", status, reason, tt.status, tt.reason)
			}
		})
	}
}

func TestCollectSQDMetricsUsesStructuredCountersAndKeepsLegacySeparate(t *testing.T) {
	got := collectSQDMetrics([]cloudruntime.Job{{Diagnostics: &cloudruntime.JobDiagnostics{
		SQDRequestsTotal: 4, SQDRequestsSuccessTotal: 3, SQDRequestsFailedTotal: 1,
		SQDRequests503Total: 1, SQDRequestsRetryTotal: 1,
		SQDLogicalQueriesTotal: 1, SQDLogicalQueriesSuccessTotal: 1,
		SQDRequestDurationMSSamples: []int64{10, 20, 30, 40},
		HTTP503Count:                99, RetryCount: 98,
	}}})
	if got.RequestsTotal != 4 || got.Total503 != 1 || got.RetryTotal != 1 {
		t.Fatalf("structured counters were not aggregated: %+v", got)
	}
	if got.Legacy503Events != 99 || got.LegacyRetryEvents != 98 {
		t.Fatalf("legacy counters were not kept separate: %+v", got)
	}
	percentiles := percentileSummary(got.RequestDurationSamples)
	if percentiles["p95_ms"] != int64(40) || percentiles["samples"] != 4 {
		t.Fatalf("unexpected percentiles: %+v", percentiles)
	}
}

func TestEvaluateDownstreamPressureUsesWorstRealQueue(t *testing.T) {
	tests := []struct {
		name       string
		snapshot   smartdownload.PipelineSnapshot
		state      string
		bottleneck string
		reason     string
	}{
		{
			name: "normal",
			snapshot: smartdownload.PipelineSnapshot{
				LocalSync:  smartdownload.PipelineStageSnapshot{Depth: 7},
				Validation: smartdownload.PipelineStageSnapshot{Depth: 2},
				ClickHouse: smartdownload.PipelineStageSnapshot{Depth: 1},
			},
			state: "NORMAL", bottleneck: "NONE", reason: "DOWNSTREAM_PRESSURE_BELOW_SOFT_THRESHOLDS",
		},
		{
			name: "validation soft pressure",
			snapshot: smartdownload.PipelineSnapshot{
				LocalSync:  smartdownload.PipelineStageSnapshot{Depth: 3},
				Validation: smartdownload.PipelineStageSnapshot{Depth: 8},
				ClickHouse: smartdownload.PipelineStageSnapshot{Depth: 4},
			},
			state: "SOFT_PRESSURE", bottleneck: "VALIDATION", reason: "VALIDATION_QUEUE_DEPTH_SOFT_THRESHOLD",
		},
		{
			name: "clickhouse hard pressure",
			snapshot: smartdownload.PipelineSnapshot{
				LocalSync:  smartdownload.PipelineStageSnapshot{Depth: 12},
				Validation: smartdownload.PipelineStageSnapshot{Depth: 15},
				ClickHouse: smartdownload.PipelineStageSnapshot{Depth: 32},
			},
			state: "HARD_PRESSURE", bottleneck: "CLICKHOUSE", reason: "CLICKHOUSE_QUEUE_DEPTH_HARD_THRESHOLD",
		},
		{
			name: "local sync wait soft pressure",
			snapshot: smartdownload.PipelineSnapshot{
				LocalSync:  smartdownload.PipelineStageSnapshot{Depth: 2, QueueWaitP95MS: 30_000},
				Validation: smartdownload.PipelineStageSnapshot{Depth: 2, QueueWaitP95MS: 10_000},
				ClickHouse: smartdownload.PipelineStageSnapshot{Depth: 2, QueueWaitP95MS: 20_000},
			},
			state: "SOFT_PRESSURE", bottleneck: "LOCAL_SYNC", reason: "LOCAL_SYNC_QUEUE_WAIT_P95_SOFT_THRESHOLD",
		},
		{
			name: "clickhouse wait hard pressure",
			snapshot: smartdownload.PipelineSnapshot{
				LocalSync:  smartdownload.PipelineStageSnapshot{Depth: 4, QueueWaitP95MS: 40_000},
				Validation: smartdownload.PipelineStageSnapshot{Depth: 4, QueueWaitP95MS: 50_000},
				ClickHouse: smartdownload.PipelineStageSnapshot{Depth: 4, QueueWaitP95MS: 120_000},
			},
			state: "HARD_PRESSURE", bottleneck: "CLICKHOUSE", reason: "CLICKHOUSE_QUEUE_WAIT_P95_HARD_THRESHOLD",
		},
		{
			name: "equal depth and wait uses lower nonzero throughput",
			snapshot: smartdownload.PipelineSnapshot{
				LocalSync:  smartdownload.PipelineStageSnapshot{Depth: 8, QueueWaitP95MS: 30_000, RowsPerSecond: 200},
				Validation: smartdownload.PipelineStageSnapshot{Depth: 8, QueueWaitP95MS: 30_000, RowsPerSecond: 100},
				ClickHouse: smartdownload.PipelineStageSnapshot{Depth: 8, QueueWaitP95MS: 30_000, RowsPerSecond: 150},
			},
			state: "SOFT_PRESSURE", bottleneck: "VALIDATION", reason: "VALIDATION_QUEUE_DEPTH_SOFT_THRESHOLD",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, bottleneck, reason := evaluateDownstreamPressure(tt.snapshot, true)
			if state != tt.state || bottleneck != tt.bottleneck || reason != tt.reason {
				t.Fatalf("got %s/%s/%s, want %s/%s/%s", state, bottleneck, reason, tt.state, tt.bottleneck, tt.reason)
			}
		})
	}
}

func TestEvaluateDownstreamPressureDoesNotReportNormalWithoutPipelineMetrics(t *testing.T) {
	state, bottleneck, reason := evaluateDownstreamPressure(smartdownload.PipelineSnapshot{}, false)
	if state != "UNKNOWN" || bottleneck != "UNKNOWN" || reason != "PIPELINE_METRICS_UNAVAILABLE" {
		t.Fatalf("got %s/%s/%s", state, bottleneck, reason)
	}
}

func TestStageDebugPayloadDoesNotClaimMetricsValidWithoutCompletionEvidence(t *testing.T) {
	payload := stageDebugPayload(smartdownload.PipelineStageSnapshot{
		Depth: 4, Capacity: 16, QueuedTotal: 4, RowsPerSecond: 12.5,
	})
	if payload["queue_depth"] != 4 || payload["queue_capacity"] != 16 {
		t.Fatalf("unexpected queue payload: %+v", payload)
	}
	if payload["metrics_valid"] != false {
		t.Fatalf("metrics must remain unverified without completed jobs: %+v", payload)
	}

	payload = stageDebugPayload(smartdownload.PipelineStageSnapshot{CompletedTotal: 1})
	if payload["metrics_valid"] != true {
		t.Fatalf("completed evidence should mark metrics valid: %+v", payload)
	}
}
