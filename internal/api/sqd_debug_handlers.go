package api

import (
	"encoding/json"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/etl/backend/internal/cloudruntime"
	"github.com/etl/backend/internal/downloadscheduler"
	"github.com/etl/backend/internal/smartdownload"
	"github.com/gin-gonic/gin"
)

// HandleSchedulerDebugAPI is the Gin bridge for local-only /api/debug/*
// observability. RemoteAddr is the authority; forwarding headers are ignored.
func HandleSchedulerDebugAPI(c *gin.Context) {
	if schedulerDebugAPI == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]any{"detail": "SQD Debug 服务不可用"})
		return
	}
	schedulerDebugAPI.ServeHTTP(c.Writer, c.Request)
}

type SchedulerDebugHandler struct {
	scheduler *downloadscheduler.Scheduler
}

func NewSchedulerDebugHandler(scheduler *downloadscheduler.Scheduler) *SchedulerDebugHandler {
	return &SchedulerDebugHandler{scheduler: scheduler}
}

func (h *SchedulerDebugHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemoteAddr(r.RemoteAddr) {
		writeJSON(w, http.StatusForbidden, map[string]any{"detail": "debug endpoint is local-only"})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"detail": "method not allowed"})
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/debug"), "/")
	switch path {
	case "sqd/metrics":
		h.sqdMetrics(w)
	case "cloud/workers":
		h.cloudWorkers(w)
	case "cloud/queue":
		h.cloudQueue(w)
	case "cloud/timing":
		h.cloudTiming(w)
	case "benchmark/current":
		h.benchmarkCurrent(w)
	case "backpressure":
		h.backpressure(w)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "unknown debug endpoint: " + path})
	}
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(remoteAddr), "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type sqdMetricAggregate struct {
	RequestsTotal, SuccessTotal, FailedTotal                                 int64
	Total503, Total429, TimeoutTotal, NetworkErrorTotal                      int64
	Total3XX, Total4XXOther, Total5XXOther, TotalHTTPOther                   int64
	DNSErrorTotal, CancelledTotal, RetryTotal                                int64
	RetrySuccessTotal, RetryExhaustedTotal, RetryBackoffMSTotal              int64
	LogicalTotal, LogicalSuccessTotal, LogicalFailedTotal                    int64
	Legacy503Events, Legacy429Events, LegacyTimeoutEvents, LegacyRetryEvents int64
	RequestDurationSamples, QueryDurationSamples, TTFASamples                []int64
	EvidenceJobs                                                             int
}

func collectSQDMetrics(jobs []cloudruntime.Job) sqdMetricAggregate {
	var out sqdMetricAggregate
	for _, job := range jobs {
		if d := job.Diagnostics; d != nil {
			out.EvidenceJobs++
			out.RequestsTotal += d.SQDRequestsTotal
			out.SuccessTotal += d.SQDRequestsSuccessTotal
			out.FailedTotal += d.SQDRequestsFailedTotal
			out.Total503 += d.SQDRequests503Total
			out.Total429 += d.SQDRequests429Total
			out.Total3XX += d.SQDRequests3XXTotal
			out.Total4XXOther += d.SQDRequests4XXOtherTotal
			out.Total5XXOther += d.SQDRequests5XXOtherTotal
			out.TotalHTTPOther += d.SQDRequestsHTTPOtherTotal
			out.TimeoutTotal += d.SQDRequestsTimeoutTotal
			out.NetworkErrorTotal += d.SQDRequestsNetworkErrorTotal
			out.DNSErrorTotal += d.SQDRequestsDNSErrorTotal
			out.CancelledTotal += d.SQDRequestsCancelledTotal
			out.RetryTotal += d.SQDRequestsRetryTotal
			out.RetrySuccessTotal += d.SQDRequestsRetrySuccessTotal
			out.RetryExhaustedTotal += d.SQDRequestsRetryExhaustedTotal
			out.RetryBackoffMSTotal += d.SQDRetryBackoffMSTotal
			out.LogicalTotal += d.SQDLogicalQueriesTotal
			out.LogicalSuccessTotal += d.SQDLogicalQueriesSuccessTotal
			out.LogicalFailedTotal += d.SQDLogicalQueriesFailedTotal
			out.Legacy503Events += d.HTTP503Count
			out.Legacy429Events += d.HTTP429Count
			out.LegacyTimeoutEvents += d.TimeoutCount
			out.LegacyRetryEvents += d.RetryCount
			for _, sample := range d.SQDRequestDurationMSSamples {
				if sample >= 0 {
					out.RequestDurationSamples = append(out.RequestDurationSamples, sample)
				}
			}
			for _, sample := range d.RequestDurationMSSamplesLegacy {
				if sample >= 0 {
					out.RequestDurationSamples = append(out.RequestDurationSamples, sample)
				}
			}
		}
		if job.Durations != nil {
			appendDuration(&out.QueryDurationSamples, job.Durations.SQDScanMS)
			appendDuration(&out.TTFASamples, job.Durations.SQDTTFAMS)
		}
	}
	return out
}

func (h *SchedulerDebugHandler) sqdMetrics(w http.ResponseWriter) {
	m := collectSQDMetrics(h.scheduler.CloudJobs())
	gateStatus, gateReason := evaluateSQDSafetyGate(m)
	writeJSON(w, http.StatusOK, map[string]any{
		"window":         "all_available_cloud_jobs",
		"requests_total": m.RequestsTotal, "success_total": m.SuccessTotal, "failed_total": m.FailedTotal,
		"503_total": m.Total503, "429_total": m.Total429, "timeout_total": m.TimeoutTotal,
		"3xx_total": m.Total3XX, "4xx_other_total": m.Total4XXOther, "5xx_other_total": m.Total5XXOther, "http_other_total": m.TotalHTTPOther,
		"network_error_total": m.NetworkErrorTotal, "dns_error_total": m.DNSErrorTotal,
		"cancelled_total": m.CancelledTotal, "retry_total": m.RetryTotal,
		"retry_success_total": m.RetrySuccessTotal, "retry_exhausted_total": m.RetryExhaustedTotal,
		"retry_backoff_ms_total": m.RetryBackoffMSTotal,
		"logical_queries_total":  m.LogicalTotal, "logical_queries_success_total": m.LogicalSuccessTotal,
		"logical_queries_failed_total": m.LogicalFailedTotal,
		"success_rate":                 rateOrNil(m.SuccessTotal, m.RequestsTotal), "rate_503": rateOrNil(m.Total503, m.RequestsTotal),
		"rate_429": rateOrNil(m.Total429, m.RequestsTotal), "timeout_rate": rateOrNil(m.TimeoutTotal, m.RequestsTotal),
		"retry_rate":           rateOrNil(m.RetryTotal, m.RequestsTotal),
		"request_duration_ms":  percentileSummary(m.RequestDurationSamples),
		"query_duration_ms":    percentileSummary(m.QueryDurationSamples),
		"ttfa_ms":              percentileSummary(m.TTFASamples),
		"legacy_stderr_events": map[string]int64{"503": m.Legacy503Events, "429": m.Legacy429Events, "timeout": m.LegacyTimeoutEvents, "retry": m.LegacyRetryEvents},
		"evidence_jobs":        m.EvidenceJobs,
		"safety_gate":          map[string]any{"status": gateStatus, "reason": gateReason},
	})
}

func evaluateSQDSafetyGate(m sqdMetricAggregate) (string, string) {
	if m.RequestsTotal <= 0 {
		return "BLOCKED", "MISSING_REQUEST_TOTAL"
	}
	if m.SuccessTotal < 0 || m.FailedTotal < 0 || m.SuccessTotal+m.FailedTotal != m.RequestsTotal {
		return "INVALID", "REQUEST_COUNTER_MISMATCH"
	}
	if m.Total503 < 0 || m.Total429 < 0 || m.TimeoutTotal < 0 || m.RetryTotal < 0 ||
		m.Total503 > m.FailedTotal || m.Total429 > m.FailedTotal || m.TimeoutTotal > m.FailedTotal ||
		m.RetryTotal > m.RequestsTotal {
		return "INVALID", "REQUEST_COUNTER_OUT_OF_RANGE"
	}
	classifiedFailed := m.Total503 + m.Total429 + m.TimeoutTotal + m.NetworkErrorTotal + m.DNSErrorTotal + m.CancelledTotal + m.Total3XX + m.Total4XXOther + m.Total5XXOther + m.TotalHTTPOther
	if classifiedFailed != m.FailedTotal {
		return "INVALID", "UNCLASSIFIED_REQUEST_FAILURES"
	}
	if m.Total3XX+m.Total4XXOther+m.TotalHTTPOther > 0 {
		return "INVALID", "UNEXPECTED_HTTP_STATUS"
	}
	if float64(m.Total503+m.Total5XXOther)/float64(m.RequestsTotal) > 0.05 {
		return "FAIL", "5XX_RATE_TOO_HIGH"
	}
	if float64(m.Total503)/float64(m.RequestsTotal) > 0.05 {
		return "FAIL", "503_RATE_TOO_HIGH"
	}
	if float64(m.Total429)/float64(m.RequestsTotal) > 0.02 {
		return "FAIL", "429_RATE_TOO_HIGH"
	}
	if float64(m.TimeoutTotal)/float64(m.RequestsTotal) > 0.02 {
		return "FAIL", "TIMEOUT_RATE_TOO_HIGH"
	}
	if float64(m.RetryTotal)/float64(m.RequestsTotal) > 0.10 {
		return "FAIL", "RETRY_RATE_TOO_HIGH"
	}
	return "PASS", "THRESHOLDS_SATISFIED"
}

func rateOrNil(numerator, denominator int64) any {
	if denominator <= 0 {
		return nil
	}
	return float64(numerator) / float64(denominator)
}

func appendDuration(target *[]int64, value *int64) {
	if value != nil && *value >= 0 {
		*target = append(*target, *value)
	}
}

func percentileSummary(values []int64) map[string]any {
	if len(values) == 0 {
		return map[string]any{"samples": 0, "p50_ms": nil, "p90_ms": nil, "p95_ms": nil, "p99_ms": nil}
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	percentile := func(p float64) int64 {
		index := int(math.Ceil(p*float64(len(copyValues)))) - 1
		if index < 0 {
			index = 0
		}
		return copyValues[index]
	}
	return map[string]any{
		"samples": len(copyValues), "p50_ms": percentile(0.50), "p90_ms": percentile(0.90),
		"p95_ms": percentile(0.95), "p99_ms": percentile(0.99),
	}
}

func (h *SchedulerDebugHandler) cloudWorkers(w http.ResponseWriter) {
	runtime := h.scheduler.CloudRuntimeStatus()
	workers := make([]map[string]any, 0, len(runtime.WorkerSlots))
	for _, slot := range runtime.WorkerSlots {
		idleReason := ""
		if slot.State == "IDLE" {
			idleReason = conservativeIdleReason(slot, runtime)
		}
		workers = append(workers, map[string]any{
			"id": slot.WorkerID, "slot": slot.Slot, "state": slot.State, "alive": slot.Alive,
			"idle_reason": idleReason, "current_job_id": slot.JobID, "updated_at": slot.UpdatedAt,
			"completed_jobs": slot.CompletedJobs, "failed_jobs": slot.FailedJobs,
			"busy_millis_total": slot.BusyMillisTotal, "worker_idle_between_jobs_ms": slot.WorkerIdleBetweenJobsMS,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": runtime.ConfiguredConcurrency, "total": runtime.WorkersTotal,
		"alive": runtime.WorkersAlive, "busy": runtime.WorkersBusy, "idle": runtime.WorkersIdle,
		"max_sqd_inflight_requests": runtime.MaxSQDInflightRequests, "workers": workers,
	})
}

func conservativeIdleReason(slot cloudruntime.WorkerSlotStatus, runtime downloadscheduler.CloudRuntimeStatus) string {
	if !slot.Alive {
		return "UNKNOWN"
	}
	if runtime.QueuedJobs == 0 && runtime.LeasedJobs == 0 && runtime.RunningJob == "" {
		return "NO_PENDING_JOB"
	}
	reason := strings.ToUpper(strings.TrimSpace(slot.IdleReason))
	switch reason {
	case "CONCURRENCY_LIMIT", "CIRCUIT_OPEN", "BACKPRESSURE", "RATE_LIMITED", "WAITING_RETRY",
		"LEASE_ACQUIRE_FAILED", "QUEUE_POLL_WAIT", "LOCAL_CAPACITY_FULL", "SHUTTING_DOWN":
		return reason
	default:
		return "UNKNOWN"
	}
}

type cloudQueueSnapshot struct {
	Pending, Claimable, Leased, Running, RemoteCompleted, LocalSyncPending int
	LocalSyncRunning, ValidationPending                                    int
	QueuePoll, PayloadLoad, LeaseAcquire, LeaseWrite, Claim                []int64
	UploadActive, UploadOutstanding, UploadCapacity                        int
	QueueClaimTotal, QueueClaimSuccessTotal, QueueClaimEmptyTotal          int64
	QueueClaimFailedTotal, LeaseAcquireTotal, LeaseAcquireSuccessTotal     int64
	LeaseAcquireFailedTotal                                                int64
}

func collectCloudQueue(runtime downloadscheduler.CloudRuntimeStatus, jobs []cloudruntime.Job) cloudQueueSnapshot {
	out := cloudQueueSnapshot{Pending: runtime.QueuedJobs, Claimable: runtime.QueuedJobs, Leased: runtime.LeasedJobs}
	for _, job := range jobs {
		state := strings.ToLower(strings.TrimSpace(job.State))
		stage := strings.ToLower(strings.TrimSpace(job.Stage))
		if state == "running" || stage == "running" || stage == "uploading" || stage == "manifesting" {
			out.Running++
		}
		if state == "done" && job.LocalSyncFinishedAt == nil {
			out.RemoteCompleted++
			out.LocalSyncPending++
		}
		if job.LocalSyncStartedAt != nil && job.LocalSyncFinishedAt == nil {
			out.LocalSyncRunning++
		}
		if job.LocalSyncFinishedAt != nil && job.ValidationStartedAt == nil {
			out.ValidationPending++
		}
	}
	for _, slot := range runtime.WorkerSlots {
		if !slot.Alive {
			continue
		}
		out.UploadActive = max(out.UploadActive, slot.R2UploadActive)
		out.UploadOutstanding = max(out.UploadOutstanding, slot.R2UploadOutstanding)
		out.UploadCapacity = max(out.UploadCapacity, slot.R2UploadQueueCapacity)
		appendNonNegative(&out.QueuePoll, slot.QueuePollMS)
		appendNonNegative(&out.PayloadLoad, slot.JobPayloadLoadMS)
		appendNonNegative(&out.LeaseAcquire, slot.LeaseAcquireMS)
		appendNonNegative(&out.LeaseWrite, slot.LeaseWriteMS)
		appendNonNegative(&out.Claim, slot.ClaimMS)
		out.QueueClaimTotal += slot.QueueClaimTotal
		out.QueueClaimSuccessTotal += slot.QueueClaimSuccessTotal
		out.QueueClaimEmptyTotal += slot.QueueClaimEmptyTotal
		out.QueueClaimFailedTotal += slot.QueueClaimFailedTotal
		out.LeaseAcquireTotal += slot.LeaseAcquireTotal
		out.LeaseAcquireSuccessTotal += slot.LeaseAcquireSuccessTotal
		out.LeaseAcquireFailedTotal += slot.LeaseAcquireFailedTotal
	}
	return out
}

func appendNonNegative(target *[]int64, value int64) {
	if value >= 0 {
		*target = append(*target, value)
	}
}

func (h *SchedulerDebugHandler) cloudQueue(w http.ResponseWriter) {
	runtime := h.scheduler.CloudRuntimeStatus()
	q := collectCloudQueue(runtime, h.scheduler.CloudJobs())
	writeJSON(w, http.StatusOK, map[string]any{
		"queue_pending_jobs": q.Pending, "queue_claimable_jobs": q.Claimable, "queue_leased_jobs": q.Leased,
		"queue_running_jobs": q.Running, "queue_remote_completed_jobs": q.RemoteCompleted,
		"queue_local_sync_pending_jobs": q.LocalSyncPending,
		"r2_upload_active":              q.UploadActive, "r2_upload_outstanding": q.UploadOutstanding,
		"r2_upload_queue_capacity": q.UploadCapacity,
		"r2_queue_list_ms":         percentileSummary(q.QueuePoll), "r2_queue_read_ms": percentileSummary(q.PayloadLoad),
		"job_payload_load_ms": percentileSummary(q.PayloadLoad), "r2_queue_lease_write_ms": percentileSummary(q.LeaseWrite),
		"lease_acquire_ms": percentileSummary(q.LeaseAcquire), "r2_queue_total_claim_ms": percentileSummary(q.Claim),
		"claim_counters": map[string]any{"total": q.QueueClaimTotal, "success_total": q.QueueClaimSuccessTotal, "empty_total": q.QueueClaimEmptyTotal, "failed_total": q.QueueClaimFailedTotal},
		"lease_counters": map[string]any{"total": q.LeaseAcquireTotal, "success_total": q.LeaseAcquireSuccessTotal, "failed_total": q.LeaseAcquireFailedTotal, "renew_total": nil, "expired_total": nil},
	})
}

func (h *SchedulerDebugHandler) cloudTiming(w http.ResponseWriter) {
	jobs := h.scheduler.CloudJobs()
	metrics := map[string][]int64{}
	for _, job := range jobs {
		if job.Durations == nil {
			continue
		}
		appendDurationMap(metrics, "queue_wait_ms", job.Durations.QueueWaitMS)
		appendDurationMap(metrics, "claim_ms", job.Durations.ClaimMS)
		appendDurationMap(metrics, "lease_acquire_ms", job.Durations.LeaseAcquireMS)
		appendDurationMap(metrics, "claim_to_fetch_ms", job.Durations.ClaimToFetchMS)
		appendDurationMap(metrics, "pre_fetch_prepare_ms", job.Durations.PreFetchPrepareMS)
		appendDurationMap(metrics, "lease_to_start_ms", job.Durations.LeaseToStartMS)
		appendDurationMap(metrics, "sqd_ttfa_ms", job.Durations.SQDTTFAMS)
		appendDurationMap(metrics, "sqd_fetch_ms", job.Durations.SQDScanMS)
		appendDurationMap(metrics, "parquet_ms", job.Durations.ParquetWriteMS)
		appendDurationMap(metrics, "r2_upload_ms", job.Durations.R2UploadMS)
		appendDurationMap(metrics, "manifest_ms", job.Durations.ManifestMS)
		appendDurationMap(metrics, "local_sync_ms", job.Durations.LocalSyncMS)
		appendDurationMap(metrics, "validation_ms", job.Durations.ValidationMS)
		appendDurationMap(metrics, "coverage_ms", job.Durations.CoverageCommitMS)
		appendDurationMap(metrics, "total_ms", job.Durations.TotalJobMS)
	}
	response := map[string]any{"jobs_total": len(jobs), "worker_idle_between_jobs_ms": percentileSummary(workerIdleSamples(h.scheduler.CloudRuntimeStatus()))}
	for _, name := range []string{"queue_wait_ms", "claim_ms", "lease_acquire_ms", "claim_to_fetch_ms", "pre_fetch_prepare_ms", "lease_to_start_ms", "sqd_ttfa_ms", "sqd_fetch_ms", "parquet_ms", "r2_upload_ms", "manifest_ms", "local_sync_ms", "validation_ms", "coverage_ms", "total_ms"} {
		response[name] = percentileSummary(metrics[name])
	}
	writeJSON(w, http.StatusOK, response)
}

func appendDurationMap(values map[string][]int64, key string, value *int64) {
	if value != nil && *value >= 0 {
		values[key] = append(values[key], *value)
	}
}

func workerIdleSamples(runtime downloadscheduler.CloudRuntimeStatus) []int64 {
	values := make([]int64, 0, len(runtime.WorkerSlots))
	for _, slot := range runtime.WorkerSlots {
		if slot.Alive && slot.WorkerIdleBetweenJobsMS >= 0 {
			values = append(values, slot.WorkerIdleBetweenJobsMS)
		}
	}
	return values
}

func (h *SchedulerDebugHandler) benchmarkCurrent(w http.ResponseWriter) {
	if result := loadBenchmarkResult(); result != nil {
		status, _ := result["status"].(string)
		gate, _ := result["safety_gate"].(map[string]any)
		gateStatus, _ := gate["status"].(string)
		writeJSON(w, http.StatusOK, map[string]any{
			"current": result, "running": strings.EqualFold(status, "RUNNING"),
			"safety_gate": gate, "scale_up_allowed": strings.EqualFold(status, "TERMINAL") && gateStatus == "PASS",
		})
		return
	}
	m := collectSQDMetrics(h.scheduler.CloudJobs())
	gateStatus, gateReason := evaluateSQDSafetyGate(m)
	var current any
	for _, plan := range h.scheduler.Plans() {
		if plan == nil || plan.Status.Terminal() || !isBenchmarkPlan(plan) {
			continue
		}
		current = map[string]any{"plan_id": plan.ID, "status": plan.Status, "created_at": plan.CreatedAt, "started_at": plan.StartedAt}
		break
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current": current, "running": current != nil,
		"safety_gate":      map[string]any{"status": gateStatus, "reason": gateReason, "requests_total": m.RequestsTotal},
		"scale_up_allowed": gateStatus == "PASS",
	})
}

func loadBenchmarkResult() map[string]any {
	names := []string{"BENCH_C4_50K.json", "BENCH_C2_50K.json", "BASELINE_C1_50K.json"}
	candidates := make([]string, 0, len(names)*2)
	for _, name := range names {
		candidates = append(candidates, filepath.Join("benchmark_results", name))
	}
	if executable, err := os.Executable(); err == nil {
		for _, name := range names {
			candidates = append(candidates, filepath.Join(filepath.Dir(filepath.Dir(executable)), "benchmark_results", name))
		}
	}
	for _, candidate := range candidates {
		payload, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var result map[string]any
		if json.Unmarshal(payload, &result) == nil {
			name, _ := result["name"].(string)
			if name == "BASELINE_C1_50K" || name == "BENCH_C2_50K" || name == "BENCH_C4_50K" {
				return result
			}
		}
	}
	return nil
}

func isBenchmarkPlan(plan *downloadscheduler.Plan) bool {
	values := []string{plan.ID, plan.StageDetail}
	for _, task := range plan.Tasks {
		if task != nil {
			values = append(values, task.Requirement.Note, task.Requirement.ObjectiveDescription)
		}
	}
	for _, value := range values {
		upper := strings.ToUpper(value)
		if strings.Contains(upper, "BENCH") || strings.Contains(upper, "BASELINE_") {
			return true
		}
	}
	return false
}

func (h *SchedulerDebugHandler) backpressure(w http.ResponseWriter) {
	runtime := h.scheduler.CloudRuntimeStatus()
	q := collectCloudQueue(runtime, h.scheduler.CloudJobs())
	pipeline := smartdownload.PipelineSnapshot{}
	pipelineAvailable := smartDownloadService != nil
	if pipelineAvailable {
		pipeline = smartDownloadService.PipelineSnapshot()
	}
	state, bottleneck, reason := evaluateDownstreamPressure(pipeline, pipelineAvailable)
	recommendedAction := "NONE"
	if state == "SOFT_PRESSURE" {
		recommendedAction = "REDUCE_CLOUD_CONCURRENCY"
	} else if state == "HARD_PRESSURE" {
		recommendedAction = "PAUSE_NEW_CLOUD_CLAIMS"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"state": state, "observational_only": true, "active_control_verified": false,
		"bottleneck": bottleneck, "reason": reason, "recommended_action": recommendedAction,
		"configured_cloud_concurrency": runtime.ConfiguredConcurrency,
		"effective_cloud_concurrency":  runtime.ConfiguredConcurrency,
		"pipeline_metrics_available":   pipelineAvailable,
		"captured_at":                  nullableCapturedAt(pipeline, pipelineAvailable),
		"queues": map[string]int{
			"remote_completed": q.RemoteCompleted,
			"local_sync":       pipeline.LocalSync.Depth,
			"validation":       pipeline.Validation.Depth,
			"clickhouse":       pipeline.ClickHouse.Depth,
		},
		"stages": map[string]any{
			"local_sync": stageDebugPayload(pipeline.LocalSync),
			"validation": stageDebugPayload(pipeline.Validation),
			"clickhouse": stageDebugPayload(pipeline.ClickHouse),
		},
		"remote_completed_queue_depth": q.RemoteCompleted, "r2_upload_outstanding": q.UploadOutstanding,
		"local_sync_pending": pipeline.LocalSync.Depth, "local_sync_running": pipeline.LocalSync.Running,
		"validation_pending": pipeline.Validation.Depth, "clickhouse_ingest_pending": pipeline.ClickHouse.Depth,
		"local_sync_queue_depth": pipeline.LocalSync.Depth, "validation_queue_depth": pipeline.Validation.Depth,
		"clickhouse_queue_depth": pipeline.ClickHouse.Depth, "clickhouse_ingest_queue_depth": pipeline.ClickHouse.Depth,
		"local_sync_queue_wait_p95_ms":       pipeline.LocalSync.QueueWaitP95MS,
		"validation_queue_wait_p95_ms":       pipeline.Validation.QueueWaitP95MS,
		"clickhouse_queue_wait_p95_ms":       pipeline.ClickHouse.QueueWaitP95MS,
		"local_sync_jobs_per_second":         pipeline.LocalSync.JobsPerSecond,
		"local_sync_rows_per_second":         pipeline.LocalSync.RowsPerSecond,
		"local_sync_bytes_per_second":        pipeline.LocalSync.BytesPerSecond,
		"local_sync_mb_per_second":           pipeline.LocalSync.BytesPerSecond / (1024 * 1024),
		"validation_jobs_per_second":         pipeline.Validation.JobsPerSecond,
		"validation_rows_per_second":         pipeline.Validation.RowsPerSecond,
		"clickhouse_jobs_per_second":         pipeline.ClickHouse.JobsPerSecond,
		"clickhouse_insert_rows_per_second":  pipeline.ClickHouse.RowsPerSecond,
		"clickhouse_insert_bytes_per_second": pipeline.ClickHouse.BytesPerSecond,
		"clickhouse_mb_per_second":           pipeline.ClickHouse.BytesPerSecond / (1024 * 1024),
		"thresholds": map[string]int{
			"soft": 8, "hard": 32,
			"queue_depth_soft": 8, "queue_depth_hard": 32,
			"queue_wait_p95_soft_ms": 30_000, "queue_wait_p95_hard_ms": 120_000,
		},
	})
}

func stageDebugPayload(stage smartdownload.PipelineStageSnapshot) map[string]any {
	return map[string]any{
		"queue_depth": stage.Depth, "queue_capacity": stage.Capacity,
		"queued_total": stage.QueuedTotal, "started_total": stage.StartedTotal,
		"completed_total": stage.CompletedTotal, "failed_total": stage.FailedTotal,
		"running": stage.Running, "workers": stage.Workers,
		"rows_total": stage.RowsTotal, "bytes_total": stage.BytesTotal,
		"jobs_per_second": stage.JobsPerSecond, "rows_per_second": stage.RowsPerSecond,
		"bytes_per_second":  stage.BytesPerSecond,
		"queue_wait_p95_ms": stage.QueueWaitP95MS, "duration_p95_ms": stage.DurationP95MS,
		"metrics_valid": stage.CompletedTotal > 0,
	}
}

func nullableCapturedAt(snapshot smartdownload.PipelineSnapshot, available bool) any {
	if !available {
		return nil
	}
	return snapshot.CapturedAt
}

type downstreamPressureCandidate struct {
	name       string
	depth      int
	waitP95MS  float64
	throughput float64
}

func evaluateDownstreamPressure(snapshot smartdownload.PipelineSnapshot, available bool) (state, bottleneck, reason string) {
	if !available {
		return "UNKNOWN", "UNKNOWN", "PIPELINE_METRICS_UNAVAILABLE"
	}
	candidates := []downstreamPressureCandidate{
		pipelinePressureCandidate("LOCAL_SYNC", snapshot.LocalSync),
		pipelinePressureCandidate("VALIDATION", snapshot.Validation),
		pipelinePressureCandidate("CLICKHOUSE", snapshot.ClickHouse),
	}
	worst := candidates[0]
	for _, item := range candidates[1:] {
		if downstreamPressureRank(item) > downstreamPressureRank(worst) ||
			(downstreamPressureRank(item) == downstreamPressureRank(worst) && pressureCandidateWorse(item, worst)) {
			worst = item
		}
	}
	rank := downstreamPressureRank(worst)
	if rank == 2 {
		if worst.depth >= 32 {
			return "HARD_PRESSURE", worst.name, worst.name + "_QUEUE_DEPTH_HARD_THRESHOLD"
		}
		return "HARD_PRESSURE", worst.name, worst.name + "_QUEUE_WAIT_P95_HARD_THRESHOLD"
	}
	if rank == 1 {
		if worst.depth >= 8 {
			return "SOFT_PRESSURE", worst.name, worst.name + "_QUEUE_DEPTH_SOFT_THRESHOLD"
		}
		return "SOFT_PRESSURE", worst.name, worst.name + "_QUEUE_WAIT_P95_SOFT_THRESHOLD"
	}
	return "NORMAL", "NONE", "DOWNSTREAM_PRESSURE_BELOW_SOFT_THRESHOLDS"
}

func pipelinePressureCandidate(name string, stage smartdownload.PipelineStageSnapshot) downstreamPressureCandidate {
	throughput := stage.RowsPerSecond
	if throughput <= 0 {
		throughput = stage.JobsPerSecond
	}
	return downstreamPressureCandidate{name: name, depth: stage.Depth, waitP95MS: stage.QueueWaitP95MS, throughput: throughput}
}

func downstreamPressureRank(candidate downstreamPressureCandidate) int {
	if candidate.depth >= 32 || candidate.waitP95MS >= 120_000 {
		return 2
	}
	if candidate.depth >= 8 || candidate.waitP95MS >= 30_000 {
		return 1
	}
	return 0
}

func pressureCandidateWorse(candidate, current downstreamPressureCandidate) bool {
	if candidate.depth != current.depth {
		return candidate.depth > current.depth
	}
	if candidate.waitP95MS != current.waitP95MS {
		return candidate.waitP95MS > current.waitP95MS
	}
	return candidate.throughput > 0 && current.throughput > 0 && candidate.throughput < current.throughput
}
