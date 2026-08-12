// 智能下载统一入口前端 API 封装（Phase 4）。
import { getJson, postJson, postForm } from "../../api/client";

type ApiErrorPayload = {
  detail?: unknown;
  message?: unknown;
  error?: unknown;
};

function stringifyApiDetail(value: unknown): string | undefined {
  if (typeof value === "string") return value.trim() || undefined;
  if (value instanceof Error) return value.message;
  if (Array.isArray(value)) {
    const parts = value.map(stringifyApiDetail).filter((part): part is string => Boolean(part));
    return parts.length > 0 ? parts.join("；") : undefined;
  }
  if (value && typeof value === "object") {
    const payload = value as ApiErrorPayload;
    return stringifyApiDetail(payload.detail)
      ?? stringifyApiDetail(payload.message)
      ?? stringifyApiDetail(payload.error);
  }
  return value == null ? undefined : String(value);
}

export class SmartDownloadApiError extends Error {
  readonly status: number;

  constructor(message: string, status = 0) {
    super(message);
    this.name = "SmartDownloadApiError";
    this.status = status;
  }
}

export function smartDownloadErrorMessage(error: unknown, fallback: string): string {
  return stringifyApiDetail(error) ?? fallback;
}

function apiFailure(response: Response, payload: unknown, fallback: string): SmartDownloadApiError {
  const detail = stringifyApiDetail(payload);
  return new SmartDownloadApiError(detail ? `${fallback}：${detail}` : fallback, response.status);
}

export type Dataset =
  | "transactions"
  | "internal_transactions"
  | "token_transfers"
  | "logs"
  | "balances"
  | "token_metadata"
  | "nft_transfers";

export type DownloadMode = "AUTO" | "TURBO" | "EMERGENCY";
export type DownloadPriority = "URGENT" | "HIGH" | "NORMAL" | "BACKGROUND";
export type BurstLevel = "L1" | "L2" | "L3";
export type ResourceProfile = "STANDARD" | "PERFORMANCE" | "EXTREME";

export interface SmartDownloadCapabilities {
  available_datasets: string[];
}

export interface RangeSpec {
  mode: "FULL" | "TIME" | "BLOCK";
  from_block?: number;
  to_block?: number;
  start_time?: string;
  end_time?: string;
}

export interface BatchJob {
  id: string;
  chain_key: string;
  chain_id: number;
  status: string;
  address_count: number;
  dataset_types: string[];
  mode?: DownloadMode;
  priority?: DownloadPriority;
  relevant_range?: RangeSpec;
  relevant_ranges?: RangeSpec[];
  emergency_burst?: boolean;
  burst_level?: BurstLevel;
  resource_profile?: ResourceProfile;
  rows?: number;
  duration_seconds?: number;
  ttfa_seconds?: number;
  average_throughput_rows_per_sec?: number;
  result?: string;
  mode_switched_at?: string;
  prefetch?: boolean;
  prefetch_priority?: number;
  error?: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface AddressJob {
  id: string;
  batch_id: string;
  address: string;
  chain_key: string;
  status: string;
  range: RangeSpec;
  progress: ProgressSnapshot;
  current_dataset?: string;
  current_provider?: string;
  cloud_tier?: string;
  error?: string;
  created_at: string;
}

export interface DatasetJob {
  id: string;
  batch_id: string;
  address_job_id: string;
  address: string;
  dataset: Dataset;
  status: string;
  current_provider?: string;
  preferred_provider?: string;
  estimated_rows?: number;
  downloaded_rows: number;
  requested_range: RangeSpec;
  progress: ProgressSnapshot;
  validation?: ValidationReport;
  repair_rounds?: number;
  cloud_tier?: string;
  cloud_score?: number;
  cloud_reasons?: string[];
  cloud_estimated_cost?: number;
  cloud_estimated_runtime_seconds?: number;
  discovery_confidence?: number;
  suggested_range_span?: number;
  activity_segments?: Array<{
    from_block: number;
    to_block: number;
    estimated_rows: number;
    density: number;
    confidence: number;
  }>;
  error?: string;
}

export interface RangeJob {
  id: string;
  dataset_job_id: string;
  from_block: number;
  to_block: number;
  provider?: string;
  failed_providers?: string[];
  status: string;
  rows_committed: number;
  bytes: number;
  attempts: number;
  error?: string;
}

export interface ProgressSnapshot {
  percent: number;
  rows_current?: number;
  rows_total?: number;
  blocks_current?: number;
  blocks_total?: number;
  bytes_current?: number;
  bytes_total?: number;
  speed_rows_per_sec?: number;
  eta_seconds?: number;
  eta_confidence?: number;
}

export interface ValidationReport {
  dataset_job_id: string;
  status: "VALIDATED" | "PARTIAL" | "FAILED";
  score: number;
  coverage: number;
  rows: number;
  unique_key_count: number;
  duplicate_count: number;
  expected_count: number;
  actual_count: number;
  unknown_ranges?: Array<{ from: number; to: number }>;
  errors?: string[];
  validated_at: string;
}

export interface DetectedColumn {
  name: string;
  confidence: number;
  valid: number;
  non_empty: number;
}

export interface ImportResult {
  rows: number;
  detected_columns: DetectedColumn[];
  selected_column: string;
  valid: number;
  duplicates: number;
  invalid: number;
  final_addresses?: string[];
}

export interface LedgerEntry {
  ts: string;
  event: string;
  range_id?: string;
  from_block?: number;
  to_block?: number;
  provider?: string;
  part?: string;
  rows?: number;
  error?: string;
}

export interface IndexedResult {
  chunk_key: string;
  dataset_job_id: string;
  chain_key: string;
  dataset: string;
  address: string;
  from_block: number;
  to_block: number;
  row_count: number;
  merged_parquet?: string;
  validation: string;
  certification?: string;
  indexed_at: string;
}

export interface SdEvent {
  type: string;
  batch_id?: string;
  address_job_id?: string;
  dataset_job_id?: string;
  range_id?: string;
  provider?: string;
  status?: string;
  message?: string;
  ts: string;
  payload?: Record<string, unknown>;
}

export interface BatchSnapshot {
  entity_type: string;
  entity_id: string;
  status: string;
  progress_percent: number;
  ranges_current?: number;
  ranges_total?: number;
  rows_current?: number;
  eta: {
    seconds: number;
    lower_bound_seconds: number;
    upper_bound_seconds: number;
    confidence: string;
    recalculating: boolean;
    based_on?: string;
  };
  updated_at: string;
}

export interface ExecutionPlan {
  batch_id: string;
  datasets: Array<{
    dataset: string;
    address: string;
    chain_key: string;
    estimated_rows: number;
    estimated_bytes: number;
    size_class: string;
    preferred_provider?: string;
  }>;
}

export interface BatchSummary {
  snapshot: BatchSnapshot;
  counts: Record<string, number>;
  total: number;
  running: number;
  queued: number;
  attention: number;
  throughput_rows_per_sec: number;
}

export interface TurboStatus {
  batch_id?: string;
  mode?: DownloadMode;
  priority?: DownloadPriority;
  cloud_ranges?: number;
  rpc_ranges?: number;
  pending_ranges?: number;
  running_ranges?: number;
  completed_ranges?: number;
  failed_ranges?: number;
  covered_blocks?: number;
  total_blocks?: number;
  coverage_percent?: number;
  relevant_range_coverage_percent?: number;
  relevant_range_certification?: string;
  rows_per_second?: number;
  cloud_jobs?: number;
  cloud_rows_per_second?: number;
  rpc_workers?: number;
  rpc_rows_per_second?: number;
  parser_rows_per_second?: number;
  clickhouse_rows_per_second?: number;
  time_to_first_data_seconds?: number;
  time_to_first_relevant_range_seconds?: number;
  eta_seconds?: number;
  cloud_available?: boolean;
  rpc_available?: boolean;
  burst_active?: boolean;
  burst_level?: BurstLevel;
  backpressure_active?: boolean;
  preemption_active?: boolean;
  work_stealing_active?: boolean;
  reshard_active?: boolean;
  hedge_active?: boolean;
}

export interface CreateBatchRequest {
  chain_key: string;
  mode: DownloadMode;
  addresses: string[];
  datasets: Dataset[];
  priority?: DownloadPriority;
  relevant_range?: RangeSpec;
  relevant_ranges?: RangeSpec[];
  emergency_burst?: boolean;
  burst_level?: BurstLevel;
  resource_profile?: ResourceProfile;
  default_range?: RangeSpec;
  skip_covered?: boolean;
  address_chain_overrides?: Record<string, string>;
  prefetch?: boolean;
  prefetch_priority?: number;
}

export type GuardLevel = "OK" | "WARNING" | "BLOCKED" | "CRITICAL" | "UNKNOWN";

export interface GuardStatus {
  status: GuardLevel;
  message?: string;
  current?: number;
  limit?: number;
  remaining?: number;
  unit?: string;
}

export interface PreflightEstimate {
  estimated_blocks: number;
  address_count: number;
  dataset_count: number;
  estimated_rows: number;
  estimated_bytes: number;
  estimated_cloud_jobs: number;
  estimated_rpc_calls: number;
  estimated_eta_seconds: number;
  estimated_disk_growth_bytes: number;
  confidence: number;
  basis: string[];
  resource_profile: ResourceProfile;
  profile: {
    workers: number;
    cloud_jobs: number;
    rpc_workers: number;
  };
  blocked: boolean;
  block_reasons: string[];
  guards: {
    storage: GuardStatus;
    rpc: GuardStatus;
    cloud: GuardStatus;
  };
}

export interface PipelineStageStatus {
  rows_per_second: number;
  status?: string;
  latency_ms?: number;
  queue_depth?: number;
}

export interface FailureSummary {
  stage?: string;
  dataset?: string;
  range?: string;
  provider?: string;
  error_type?: string;
  completed_percent?: number;
  checkpoint?: string;
  recommended_action?: string;
}

export interface HardeningStatus {
  batch_id?: string;
  eta_seconds: number;
  eta_lower_seconds: number;
  eta_upper_seconds: number;
  eta_confidence: number;
  eta_basis: string[];
  bottleneck: string;
  pipeline: {
    download: PipelineStageStatus;
    parse: PipelineStageStatus;
    clickhouse: PipelineStageStatus;
  };
  slowest_stage?: string;
  stalled: boolean;
  stall_stage?: string;
  stall_seconds: number;
  self_recovery: boolean;
  recovery_status?: string;
  recovery_actions: string[];
  failure?: FailureSummary;
  guards: {
    storage: GuardStatus;
    rpc: GuardStatus;
    cloud: GuardStatus;
    clickhouse: GuardStatus;
  };
  cloud_jobs: unknown[];
  rpc_workers: unknown[];
  range_ledger: unknown[];
  retries: unknown[];
  errors: unknown[];
  checkpoints: unknown[];
  gap_repairs: unknown[];
}

export interface TaskTemplate {
  id: string;
  name: string;
  description?: string;
  resource_profile: ResourceProfile;
  chain_key?: string;
  datasets: Dataset[];
  configuration: Partial<CreateBatchRequest> & Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export interface JobReport {
  batch_id: string;
  mode?: DownloadMode;
  resource_profile?: ResourceProfile;
  provider?: string;
  rows: number;
  coverage: number;
  duplicates: number;
  ttfa_seconds: number;
  total_duration_seconds: number;
  peak_throughput_rows_per_sec: number;
  average_throughput_rows_per_sec: number;
  retry_count: number;
  gap_repair_count: number;
  certification?: string;
  result?: string;
}

export interface CompareRun {
  batch_id: string;
  label?: string;
  resource_profile?: ResourceProfile;
  ttfa_seconds: number;
  total_duration_seconds: number;
  average_throughput_rows_per_sec: number;
  failure_rate: number;
  rows: number;
}

export interface CompareRunsResult {
  runs: CompareRun[];
  deltas: Record<string, number>;
}

export interface PlannerV2Preview {
  address_count?: number;
  input_jobs?: number;
  merged_workloads?: number;
  address_groups?: number;
  merged_ranges?: number;
  dataset_bundles?: number;
  coverage_hits?: number;
  coverage_reuse_ratio?: number;
  provider_requests_saved?: number;
  provider_request_reduction_ratio?: number;
  download_amplification?: number;
  duplicate_work_avoided?: number;
  duplicate_work_ratio?: number;
  bundle_savings?: number;
  heavy_address_count?: number;
  split_count?: number;
}

export interface AcceleratorSharedWorkload {
  id: string;
  status: string;
  provider?: string;
  datasets: string[];
  addresses: string[];
  from_block?: number;
  to_block?: number;
  address_count: number;
  ref_count?: number;
  attempts?: number;
  join_existing: boolean;
  coverage_hit: boolean;
  heavy_address: boolean;
  poison_address: boolean;
  split: boolean;
  fingerprint?: string;
  error?: string;
  group_refs: string[];
}

export interface AcceleratorAddressGroup {
  id: string;
  status: string;
  address_count: number;
  priority?: string;
  provider?: string;
  heavy_address: boolean;
  poison_address: boolean;
  split: boolean;
  parent_group_id?: string;
  datasets: string[];
  addresses: string[];
  workload_ids: string[];
  workloads: AcceleratorSharedWorkload[];
}

export interface AcceleratorDatasetGroup {
  dataset: string;
  status: string;
  address_count: number;
  group_count: number;
  workload_count: number;
  groups: AcceleratorAddressGroup[];
}

export interface BatchAcceleratorStatus {
  batch_id: string;
  status: string;
  summary: PlannerV2Preview;
  datasets: AcceleratorDatasetGroup[];
  shared_workloads: AcceleratorSharedWorkload[];
  updated_at?: string;
}

export const DATASET_LABELS: Record<string, string> = {
  transactions: "交易信息",
  internal_transactions: "内部交易",
  token_transfers: "代币转账",
  logs: "日志",
  balances: "当前余额",
  token_metadata: "Token Metadata",
  nft_transfers: "NFT",
};

export async function importAddressFile(file: File): Promise<ImportResult | null> {
  const form = new FormData();
  form.append("file", file);
  const { response, payload } = await postForm<ImportResult>(
    "/api/smart-download/import",
    form,
    "地址导入失败",
  );
  if (!response.ok) throw apiFailure(response, payload, "地址导入失败");
  return payload;
}

export async function createBatch(body: CreateBatchRequest): Promise<{
  batch: BatchJob;
  local_full_hits?: number;
  local_partial_hits?: number;
  local_misses?: number;
  reused_ranges?: number;
} | null> {
  const { response, payload } = await postJson<{
    batch: BatchJob;
    local_full_hits?: number;
    local_partial_hits?: number;
    local_misses?: number;
    reused_ranges?: number;
  }>(
    "/api/smart-download/batches",
    body,
    "创建下载任务失败",
  );
  if (!response.ok || !payload?.batch) return null;
  return { ...payload, batch: normalizeBatchJob(payload.batch) };
}

export async function preflightBatch(body: CreateBatchRequest): Promise<PreflightEstimate | null> {
  try {
    const { response, payload } = await postJson<unknown>(
      "/api/smart-download/preflight",
      body,
      "生产预检失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    const estimate = asRecord(envelope.estimate ?? payload);
    const eta = asRecord(estimate.eta ?? envelope.eta);
    const guards = asRecord(envelope.guards ?? estimate.guards);
    const profile = asRecord(estimate.profile ?? envelope.profile);
    const storage = normalizeGuard(guards.storage ?? envelope.storage_guard);
    const rpc = normalizeGuard(guards.rpc ?? envelope.rpc_guard);
    const cloud = normalizeGuard(guards.cloud ?? envelope.cloud_guard);
    const allowed = typeof guards.allowed === "boolean" ? guards.allowed : undefined;
    const blockReasons = [storage, rpc, cloud]
      .filter((guard) => guard.status === "BLOCKED" || guard.status === "CRITICAL")
      .map((guard) => guard.message)
      .filter((reason): reason is string => Boolean(reason));
    return {
      estimated_blocks: asNumber(estimate.blocks ?? estimate.estimated_blocks),
      address_count: asNumber(estimate.addresses ?? estimate.address_count),
      dataset_count: asNumber(estimate.datasets ?? estimate.dataset_count),
      estimated_rows: asNumber(estimate.rows ?? estimate.estimated_rows),
      estimated_bytes: asNumber(estimate.bytes ?? estimate.estimated_bytes),
      estimated_cloud_jobs: asNumber(estimate.cloud_jobs ?? estimate.estimated_cloud_jobs),
      estimated_rpc_calls: asNumber(estimate.rpc_calls ?? estimate.estimated_rpc_calls),
      estimated_eta_seconds: asNumber(eta.seconds ?? estimate.eta_seconds),
      estimated_disk_growth_bytes: asNumber(estimate.disk_growth_bytes ?? estimate.estimated_disk_growth_bytes),
      confidence: normalizeConfidence(envelope.confidence ?? eta.confidence ?? estimate.confidence),
      basis: asStrings(envelope.basis ?? eta.basis ?? estimate.basis),
      resource_profile: asResourceProfile(estimate.resource_profile ?? envelope.resource_profile),
      profile: {
        workers: asNumber(profile.workers),
        cloud_jobs: asNumber(profile.cloud_jobs),
        rpc_workers: asNumber(profile.rpc_workers),
      },
      blocked: allowed === false || asBoolean(envelope.blocked ?? estimate.blocked) || blockReasons.length > 0,
      block_reasons: [...asStrings(envelope.block_reasons ?? estimate.block_reasons), ...blockReasons],
      guards: { storage, rpc, cloud },
    };
  } catch {
    return null;
  }
}

export async function previewPlannerV2(body: CreateBatchRequest): Promise<PlannerV2Preview | null> {
  try {
    const { response, payload } = await postJson<unknown>(
      "/api/smart-download/planner-v2",
      body,
      "Batch Planner V2 预览失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    const raw = envelope.preview ?? envelope.plan ?? envelope.planner ?? envelope.planner_v2 ?? payload;
    if (!hasPlannerV2Signal(raw)) return null;
    return normalizePlannerV2(
      raw,
      body.addresses.length,
      body.datasets.length,
    );
  } catch {
    return null;
  }
}

// ── Investigation Cache V2 + Smart Prefetch（设计 V1.0 §62-§64）──

export interface PrefetchCandidateView {
  id: string;
  address: string;
  parent_address?: string;
  score: number;
  priority: string;
  status: string;
  batch_id?: string;
  batch_status?: string;
  reasons?: string[];
  required_datasets?: string[];
  from_block: number;
  to_block: number;
  upgrade_count?: number;
  created_at?: string;
  updated_at?: string;
}

export interface PrefetchStats {
  total_jobs: number;
  active_jobs: number;
  ready_jobs: number;
  interactive_upgrades: number;
  budget: {
    max_active_prefetch_jobs: number;
    max_prefetch_addresses: number;
  };
  counters: {
    day: string;
    addresses: number;
    active_jobs: number;
  };
  feedback: {
    total: number;
    used: number;
    unused: number;
    hit_rate: number;
    saved_latency_avg: number;
    wasted_bytes: number;
  };
  disk_used_pct: number;
  disk_action: string;
  last_run?: string;
}

export interface PrefetchStatus {
  investigation_id: string;
  candidates: PrefetchCandidateView[];
  stats: PrefetchStats;
}

export async function getPrefetchStatus(investigationId: string): Promise<PrefetchStatus | null> {
  const { response, payload } = await getJson<PrefetchStatus>(
    `/api/investigations/${encodeURIComponent(investigationId)}/prefetch`,
    "预取状态加载失败",
  );
  return response.ok ? payload : null;
}

export async function pinPrefetch(
  investigationId: string,
  input: { chain_key: string; address: string; reason?: string; from_block: number; to_block: number },
): Promise<{ candidate?: unknown; detail?: string } | null> {
  const { response, payload } = await postJson<{ candidate?: unknown; detail?: string }>(
    `/api/investigations/${encodeURIComponent(investigationId)}/prefetch/pin`,
    input,
    "固定预取地址失败",
  );
  return response.ok ? payload : null;
}

export async function upgradePrefetch(
  investigationId: string,
  chainKey: string,
  address: string,
): Promise<{ detail?: string } | null> {
  const { response, payload } = await postJson<{ detail?: string }>(
    `/api/investigations/${encodeURIComponent(investigationId)}/prefetch/upgrade`,
    { chain_key: chainKey, address },
    "升级预取任务失败",
  );
  return response.ok ? payload : null;
}

export async function getPrefetchStats(): Promise<PrefetchStats | null> {
  const { response, payload } = await getJson<{ prefetch: PrefetchStats }>("/api/prefetch/stats", "预取统计加载失败");
  return response.ok ? payload.prefetch ?? null : null;
}

export async function expandGraphCache(input: {
  investigation_id?: string;
  chain_key: string;
  address: string;
  direction?: string;
  token?: string;
  from_block?: number;
  to_block?: number;
  depth?: number;
}): Promise<{ result?: unknown; cache_hit?: boolean; prefetch_scheduled?: boolean; candidates?: unknown[] } | null> {
  const { response, payload } = await postJson<{
    result?: unknown;
    cache_hit?: boolean;
    prefetch_scheduled?: boolean;
    candidates?: unknown[];
  }>("/api/graph/expand", input, "图扩展缓存预热失败");
  return response.ok ? payload : null;
}

export interface CoverageRange {
  from: number;
  to: number;
}

export interface CoverageQueryResult {
  coverage_ratio: number;
  full_hit: boolean;
  covered?: CoverageRange[];
  missing?: CoverageRange[];
  certification?: string;
  compatible?: boolean;
}

export async function queryCoverageIndex(input: {
  chain_key: string;
  address: string;
  dataset: string;
  from_block?: number;
  to_block?: number;
}): Promise<CoverageQueryResult | null> {
  const { response, payload } = await postJson<CoverageQueryResult>(
    "/api/smart-download/coverage/query",
    input,
    "覆盖查询失败",
  );
  return response.ok ? payload : null;
}

export async function batchAction(batchId: string, action: string): Promise<BatchJob | null> {
  const { response, payload } = await postJson<BatchJob>(
    `/api/smart-download/batches/${batchId}/${action}`,
    {},
    `${action} 失败`,
  );
  return response.ok ? payload : null;
}

export async function switchBatchMode(batchId: string, mode: DownloadMode): Promise<BatchJob | null> {
  const { response, payload } = await postJson<unknown>(
    `/api/smart-download/batches/${encodeURIComponent(batchId)}/mode`,
    { mode },
    "切换下载模式失败",
  );
  if (!response.ok) return null;
  const envelope = asRecord(payload);
  const raw = asRecord(envelope.batch ?? payload);
  return asString(raw.id) ? normalizeBatchJob(raw) : null;
}

export async function getTurboStatus(batchId: string): Promise<TurboStatus | null> {
  try {
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/batches/${encodeURIComponent(batchId)}/turbo-status`,
      "查询 Turbo 状态失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    const raw = asRecord(envelope.turbo_status ?? payload);
    return normalizeTurboStatus(raw);
  } catch {
    return null;
  }
}

export async function getBatchAccelerator(batchId: string): Promise<BatchAcceleratorStatus | null> {
  try {
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/batches/${encodeURIComponent(batchId)}/accelerator`,
      "查询 Batch Accelerator 状态失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    const raw = asRecord(
      envelope.accelerator ?? envelope.accelerator_plan ?? envelope.batch_accelerator ?? envelope.accelerator_status ?? envelope.plan ?? envelope.data ?? payload,
    );
    if (!hasAcceleratorSignal(raw)) return null;
    const hierarchy = asRecord(raw.hierarchy ?? raw.tree ?? raw.execution_tree);
    let datasets = asArray(
      raw.datasets ?? raw.dataset_groups ?? hierarchy.datasets ?? hierarchy.dataset_groups,
    ).map(normalizeDatasetGroup);
    const sharedWorkloads = asArray(
      raw.shared_workloads ?? raw.shared_works ?? raw.workloads ?? hierarchy.shared_workloads,
    ).map(normalizeSharedWorkload);
    if (datasets.length === 0) {
      datasets = buildDatasetHierarchy(
        asArray(raw.groups ?? raw.address_groups ?? hierarchy.groups ?? hierarchy.address_groups).map(normalizeAddressGroup),
        sharedWorkloads,
      );
    }
    return {
      batch_id: asString(raw.batch_id ?? envelope.batch_id) ?? batchId,
      status: (asString(raw.status ?? raw.state) ?? "UNKNOWN").toUpperCase(),
      summary: normalizePlannerV2(raw),
      datasets,
      shared_workloads: sharedWorkloads,
      updated_at: asString(raw.updated_at ?? raw.generated_at),
    };
  } catch {
    return null;
  }
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" ? (value as Record<string, unknown>) : {};
}

function asNumber(value: unknown, fallback = 0): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function asBoolean(value: unknown): boolean {
  return typeof value === "boolean" ? value : false;
}

function asString(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value : undefined;
}

function asFlexibleNumber(value: unknown, fallback = 0): number {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim()) {
    const parsed = Number(value.replace(/[% ,]/g, ""));
    if (Number.isFinite(parsed)) return parsed;
  }
  return fallback;
}

function asFlexibleBoolean(value: unknown): boolean {
  if (typeof value === "boolean") return value;
  if (typeof value === "number") return value !== 0;
  const normalized = asString(value)?.toUpperCase();
  return normalized === "TRUE" || normalized === "YES" || normalized === "Y" || normalized === "1" || normalized === "HIT";
}

function asRatio(value: unknown): number {
  const raw = asFlexibleNumber(value);
  if (raw <= 0) return 0;
  return Math.max(0, Math.min(1, raw > 1 ? raw / 100 : raw));
}

function hasPlannerV2Signal(value: unknown): boolean {
  const raw = asRecord(value);
  const metrics = asRecord(raw.metrics ?? raw.summary ?? raw.efficiency ?? raw.planner_efficiency);
  const keys = [
    "input_jobs", "original_jobs", "original_task_units", "merged_workloads", "shared_workloads",
    "address_groups", "address_group_count", "coverage_reuse_ratio", "coverage_hits",
    "provider_requests_saved", "request_reduction_ratio", "download_amplification",
    "bundle_savings", "heavy_address_count", "split_count",
  ];
  return keys.some((key) => raw[key] !== undefined || metrics[key] !== undefined) || Array.isArray(raw.groups);
}

function hasAcceleratorSignal(raw: Record<string, unknown>): boolean {
  const hierarchy = asRecord(raw.hierarchy ?? raw.tree ?? raw.execution_tree);
  return hasPlannerV2Signal(raw) || [
    raw.datasets,
    raw.dataset_groups,
    raw.shared_workloads,
    raw.shared_works,
    raw.workloads,
    hierarchy.datasets,
    hierarchy.dataset_groups,
    hierarchy.shared_workloads,
  ].some((value) => Array.isArray(value));
}

function normalizeConfidence(value: unknown): number {
  if (typeof value === "number" && Number.isFinite(value)) return Math.max(0, Math.min(1, value > 1 ? value / 100 : value));
  const label = asString(value)?.toUpperCase();
  if (label === "HIGH") return 0.9;
  if (label === "MEDIUM") return 0.7;
  if (label === "LOW") return 0.4;
  return 0;
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function asStrings(value: unknown): string[] {
  return asArray(value).map((item) => asString(item)).filter((item): item is string => Boolean(item));
}

function normalizePlannerV2(value: unknown, fallbackAddresses = 0, fallbackDatasets = 0): PlannerV2Preview {
  const raw = asRecord(value);
  const metrics = asRecord(raw.metrics ?? raw.summary ?? raw.efficiency ?? raw.planner_efficiency);
  const read = (...keys: string[]): unknown => {
    for (const key of keys) {
      if (raw[key] !== undefined) return raw[key];
      if (metrics[key] !== undefined) return metrics[key];
    }
    return undefined;
  };
  const optionalNumber = (...keys: string[]): number | undefined => {
    const value = read(...keys);
    if (value === undefined || value === null || value === "") return undefined;
    return Array.isArray(value) ? value.length : asFlexibleNumber(value);
  };
  const optionalRatio = (...keys: string[]): number | undefined => {
    const value = read(...keys);
    return value === undefined || value === null || value === "" ? undefined : asRatio(value);
  };
  const addressCount = optionalNumber("address_count", "addresses", "total_addresses") ?? (fallbackAddresses > 0 ? fallbackAddresses : undefined);
  const inputJobs = optionalNumber("input_jobs", "original_jobs", "original_task_units", "raw_workloads", "dataset_jobs")
    ?? (addressCount !== undefined && fallbackDatasets > 0 ? addressCount * fallbackDatasets : undefined);
  const mergedWorkloads = optionalNumber("merged_workloads", "workloads", "shared_workloads", "shared_work_count", "output_jobs");
  const explicitSaved = optionalNumber("provider_requests_saved", "requests_saved", "saved_provider_requests");
  const saved = explicitSaved ?? (inputJobs !== undefined && mergedWorkloads !== undefined
    ? Math.max(0, inputJobs - mergedWorkloads)
    : undefined);
  const reduction = optionalRatio(
    "provider_request_reduction_ratio", "provider_requests_reduction", "request_reduction_ratio", "reduction_ratio",
  ) ?? (inputJobs !== undefined && inputJobs > 0 && saved !== undefined
    ? Math.max(0, Math.min(1, saved / inputJobs))
    : undefined);
  return {
    address_count: addressCount,
    input_jobs: inputJobs,
    merged_workloads: mergedWorkloads,
    address_groups: optionalNumber("address_groups", "address_group_count", "groups"),
    merged_ranges: optionalNumber("merged_ranges", "range_count", "coalesced_ranges"),
    dataset_bundles: optionalNumber("dataset_bundles", "dataset_bundle_count", "bundles"),
    coverage_hits: optionalNumber("coverage_hits", "coverage_hit_count", "reused_coverage"),
    coverage_reuse_ratio: optionalRatio("coverage_reuse_ratio", "coverage_reuse", "coverage_hit_ratio", "coverage_ratio"),
    provider_requests_saved: saved,
    provider_request_reduction_ratio: reduction,
    download_amplification: optionalNumber("download_amplification", "amplification"),
    duplicate_work_avoided: optionalNumber("duplicate_work_avoided", "deduplicated_work", "duplicate_ranges_avoided"),
    duplicate_work_ratio: optionalRatio("duplicate_work_ratio", "dedup_ratio", "duplicate_ratio"),
    bundle_savings: optionalNumber("bundle_savings", "dataset_bundle_savings"),
    heavy_address_count: optionalNumber("heavy_address_count", "heavy_addresses"),
    split_count: optionalNumber("split_count", "group_split_count"),
  };
}

function normalizeSharedWorkload(value: unknown): AcceleratorSharedWorkload {
  const raw = asRecord(value);
  const range = asRecord(raw.range ?? raw.block_range);
  const state = (asString(raw.status ?? raw.state ?? raw.disposition) ?? "UNKNOWN").toUpperCase();
  const flags = asStrings(raw.flags ?? raw.tags).map((flag) => flag.toUpperCase());
  const has = (name: string) => flags.includes(name) || state === name;
  const refs = asArray(raw.refs ?? raw.references);
  const groupRefs = refs.flatMap((ref) => {
    if (typeof ref === "string") return [ref];
    const item = asRecord(ref);
    return [asString(item.group_id ?? item.address_group_id)].filter((id): id is string => Boolean(id));
  });
  return {
    id: asString(raw.id ?? raw.shared_work_id ?? raw.workload_id ?? raw.work_id) ?? "shared-work",
    status: state,
    provider: asString(raw.provider ?? raw.current_provider ?? raw.source),
    datasets: asStrings(raw.datasets ?? raw.dataset_types ?? (raw.dataset ? [raw.dataset] : [])),
    addresses: asStrings(raw.addresses ?? raw.address_filters).map((address) => address.toLowerCase()),
    from_block: asFlexibleNumber(raw.from_block ?? range.from_block ?? range.from, Number.NaN),
    to_block: asFlexibleNumber(raw.to_block ?? range.to_block ?? range.to, Number.NaN),
    address_count: asFlexibleNumber(raw.address_count ?? raw.addresses_count ?? raw.filter_count ?? asArray(raw.addresses).length),
    ref_count: raw.ref_count !== undefined || raw.reference_count !== undefined
      ? asFlexibleNumber(raw.ref_count ?? raw.reference_count)
      : refs.length > 0
        ? refs.length
        : undefined,
    attempts: raw.attempts !== undefined || raw.retry_count !== undefined
      ? asFlexibleNumber(raw.attempts ?? raw.retry_count)
      : undefined,
    join_existing: asFlexibleBoolean(raw.join_existing ?? raw.joined_existing ?? raw.reused) || has("JOIN_EXISTING_WORK"),
    coverage_hit: asFlexibleBoolean(raw.coverage_hit ?? raw.coverage_reused ?? raw.local_hit) || has("COVERAGE_HIT"),
    heavy_address: asFlexibleBoolean(raw.heavy_address ?? raw.is_heavy) || has("HEAVY_ADDRESS"),
    poison_address: asFlexibleBoolean(raw.poison_address ?? raw.is_poison) || has("POISON_ADDRESS"),
    split: asFlexibleBoolean(raw.split ?? raw.was_split ?? raw.split_from_failure) || has("SPLIT") || has("GROUP_SPLIT"),
    fingerprint: asString(raw.fingerprint ?? raw.work_fingerprint ?? raw.filter_hash),
    error: asString(raw.error ?? raw.last_error),
    group_refs: groupRefs,
  };
}

function normalizeAddressGroup(value: unknown): AcceleratorAddressGroup {
  const raw = asRecord(value);
  const state = (asString(raw.status ?? raw.state) ?? "UNKNOWN").toUpperCase();
  const flags = asStrings(raw.flags ?? raw.tags).map((flag) => flag.toUpperCase());
  const has = (name: string) => flags.includes(name) || state === name;
  const workloads = asArray(raw.workloads ?? raw.shared_workloads ?? raw.shared_works ?? raw.work_items)
    .map(normalizeSharedWorkload);
  const classification = (asString(raw.classification) ?? "").toUpperCase();
  return {
    id: asString(raw.id ?? raw.group_id ?? raw.address_group_id) ?? "address-group",
    status: state,
    address_count: asFlexibleNumber(raw.address_count ?? raw.addresses_count ?? asArray(raw.addresses).length),
    priority: asString(raw.priority),
    provider: asString(raw.provider ?? raw.preferred_provider),
    heavy_address: asFlexibleBoolean(raw.heavy_address ?? raw.heavy ?? raw.is_heavy) || classification === "HEAVY" || classification === "HEAVY_ADDRESS" || has("HEAVY_ADDRESS"),
    poison_address: asFlexibleBoolean(raw.poison_address ?? raw.is_poison) || classification === "POISON" || classification === "POISON_ADDRESS" || has("POISON_ADDRESS"),
    split: asFlexibleBoolean(raw.split ?? raw.was_split ?? raw.split_from_failure) || classification === "SPLIT" || has("SPLIT") || has("GROUP_SPLIT"),
    parent_group_id: asString(raw.parent_group_id ?? raw.split_from_group_id),
    datasets: asStrings(raw.datasets ?? raw.dataset_types ?? (raw.dataset ? [raw.dataset] : [])),
    addresses: asStrings(raw.addresses ?? raw.address_filters).map((address) => address.toLowerCase()),
    workload_ids: asStrings(raw.workload_ids ?? raw.shared_workload_ids ?? raw.shared_work_ids),
    workloads,
  };
}

function normalizeDatasetGroup(value: unknown): AcceleratorDatasetGroup {
  const raw = asRecord(value);
  const groups = asArray(raw.groups ?? raw.address_groups ?? raw.addressGroups).map(normalizeAddressGroup);
  return {
    dataset: asString(raw.dataset ?? raw.name ?? raw.dataset_type) ?? "unknown",
    status: (asString(raw.status ?? raw.state) ?? "UNKNOWN").toUpperCase(),
    address_count: asFlexibleNumber(raw.address_count ?? raw.addresses_count),
    group_count: asFlexibleNumber(raw.group_count ?? raw.address_group_count, groups.length),
    workload_count: asFlexibleNumber(
      raw.workload_count ?? raw.shared_workload_count ?? raw.shared_work_count,
      groups.reduce((total, group) => total + group.workloads.length, 0),
    ),
    groups,
  };
}

function buildDatasetHierarchy(
  groups: AcceleratorAddressGroup[],
  sharedWorkloads: AcceleratorSharedWorkload[],
): AcceleratorDatasetGroup[] {
  const byDataset = new Map<string, AcceleratorAddressGroup[]>();
  const workloadsByID = new Map(sharedWorkloads.map((workload) => [workload.id, workload]));
  const workloadsByGroup = new Map<string, AcceleratorSharedWorkload[]>();
  const workloadsByAddress = new Map<string, AcceleratorSharedWorkload[]>();
  for (const workload of sharedWorkloads) {
    for (const groupID of workload.group_refs) {
      workloadsByGroup.set(groupID, [...(workloadsByGroup.get(groupID) ?? []), workload]);
    }
    if (workload.group_refs.length === 0) {
      for (const address of workload.addresses) {
        workloadsByAddress.set(address, [...(workloadsByAddress.get(address) ?? []), workload]);
      }
    }
  }
  for (const group of groups) {
    const datasets = group.datasets.length > 0 ? group.datasets : ["unknown"];
    const linkedByID = new Map<string, AcceleratorSharedWorkload>();
    for (const workloadID of group.workload_ids) {
      const workload = workloadsByID.get(workloadID);
      if (workload) linkedByID.set(workload.id, workload);
    }
    for (const workload of workloadsByGroup.get(group.id) ?? []) linkedByID.set(workload.id, workload);
    for (const address of group.addresses) {
      for (const workload of workloadsByAddress.get(address) ?? []) {
        if (workload.datasets.some((dataset) => datasets.includes(dataset))) linkedByID.set(workload.id, workload);
      }
    }
    const groupWithWorkloads = {
      ...group,
      workloads: group.workloads.length > 0 ? group.workloads : [...linkedByID.values()],
    };
    for (const dataset of datasets) {
      byDataset.set(dataset, [...(byDataset.get(dataset) ?? []), groupWithWorkloads]);
    }
  }
  return [...byDataset.entries()].map(([dataset, datasetGroups]) => {
    const states = datasetGroups.flatMap((group) =>
      group.status !== "UNKNOWN" ? [group.status] : group.workloads.map((workload) => workload.status),
    ).filter((state) => state !== "UNKNOWN");
    const status = states.length === 0
      ? "UNKNOWN"
      : states.every((state) => state === "COMPLETED")
        ? "COMPLETED"
        : states.every((state) => state === "CANCELED")
          ? "CANCELED"
          : states.some((state) => ["FAILED", "POISON", "PARTIAL"].includes(state))
          ? "PARTIAL"
          : states.some((state) => state === "RUNNING")
            ? "RUNNING"
            : "PENDING";
    return {
      dataset,
      status,
      address_count: datasetGroups.reduce((total, group) => total + group.address_count, 0),
      group_count: datasetGroups.length,
      workload_count: new Set(datasetGroups.flatMap((group) => group.workloads.map((workload) => workload.id))).size,
      groups: datasetGroups,
    };
  });
}

function asResourceProfile(value: unknown): ResourceProfile {
  const profile = asString(value)?.toUpperCase();
  return profile === "PERFORMANCE" || profile === "EXTREME" ? profile : "STANDARD";
}

function asDownloadMode(value: unknown): DownloadMode {
  const mode = asString(value)?.toUpperCase();
  return mode === "TURBO" || mode === "EMERGENCY" ? mode : "AUTO";
}

function normalizeGuard(value: unknown): GuardStatus {
  const raw = asRecord(value);
  const rawStatus = asString(raw.status ?? raw.level)?.toUpperCase();
  const status: GuardLevel = rawStatus === "PASS" || rawStatus === "ALLOWED"
    ? "OK"
    : rawStatus === "WARN"
      ? "WARNING"
      : rawStatus === "BLOCK"
        ? "BLOCKED"
        : rawStatus === "OK" || rawStatus === "WARNING" || rawStatus === "BLOCKED" || rawStatus === "CRITICAL"
          ? rawStatus
          : "UNKNOWN";
  return {
    status,
    message: asString(raw.reason ?? raw.message ?? raw.detail),
    current: asNumber(raw.current ?? raw.estimated_bytes ?? raw.estimated_calls ?? raw.estimated_cost),
    limit: asNumber(raw.limit ?? raw.hard_limit ?? raw.hard_limit_calls ?? raw.reserve_bytes),
    remaining: asNumber(raw.remaining ?? raw.available_bytes ?? raw.remaining_calls ?? raw.remaining_budget),
    unit: asString(raw.unit),
  };
}

function normalizeStage(value: unknown): PipelineStageStatus {
  const raw = asRecord(value);
  return {
    rows_per_second: asNumber(raw.rows_per_second ?? raw.rows_per_sec ?? raw.throughput),
    status: asString(raw.status),
    latency_ms: asNumber(raw.latency_ms ?? raw.p95_latency_ms),
    queue_depth: asNumber(raw.queue_depth ?? raw.queue),
  };
}

function normalizeBatchJob(value: unknown): BatchJob {
  const raw = asRecord(value);
  const rawPriority = asString(raw.priority)?.toUpperCase();
  const priority: DownloadPriority = rawPriority === "URGENT" || rawPriority === "HIGH" || rawPriority === "BACKGROUND"
    ? rawPriority
    : "NORMAL";
  const rawBurst = asString(raw.burst_level)?.toUpperCase();
  const burstLevel: BurstLevel = rawBurst === "L2" || rawBurst === "L3" ? rawBurst : "L1";
  return {
    ...(raw as unknown as BatchJob),
    id: asString(raw.id) ?? "",
    chain_key: asString(raw.chain_key) ?? "unknown",
    chain_id: asNumber(raw.chain_id),
    status: asString(raw.status) ?? "UNKNOWN",
    address_count: asNumber(raw.address_count),
    dataset_types: asStrings(raw.dataset_types ?? raw.datasets),
    mode: asDownloadMode(raw.mode),
    priority,
    burst_level: burstLevel,
    resource_profile: asResourceProfile(raw.resource_profile ?? raw.profile),
    rows: asNumber(raw.rows ?? raw.total_rows ?? raw.rows_committed),
    duration_seconds: asNumber(raw.duration_seconds ?? raw.total_duration_seconds),
    ttfa_seconds: asNumber(raw.ttfa_seconds ?? raw.time_to_first_data_seconds),
    average_throughput_rows_per_sec: asNumber(raw.average_throughput_rows_per_sec ?? raw.avg_rows_per_second),
    result: asString(raw.result ?? raw.outcome),
    created_at: asString(raw.created_at) ?? "",
    updated_at: asString(raw.updated_at) ?? "",
  };
}

function normalizeTurboStatus(raw: Record<string, unknown>): TurboStatus {
  const mode = asString(raw.mode);
  const priority = asString(raw.priority);
  const rawBurstLevel = asString(raw.burst_level ?? raw.cloud_burst_level) ?? "";
  const burstLevel: BurstLevel = rawBurstLevel.startsWith("L3")
    ? "L3"
    : rawBurstLevel.startsWith("L2")
      ? "L2"
      : "L1";
  const relevantRanges = asNumber(raw.relevant_ranges);
  const relevantCertifiedRanges = asNumber(raw.relevant_certified_ranges);
  const relevantCoverage = relevantRanges > 0
    ? (relevantCertifiedRanges * 100) / relevantRanges
    : asNumber(raw.relevant_range_coverage_percent ?? raw.relevant_coverage_percent);
  const cloudJobs = asNumber(raw.cloud_jobs ?? raw.cloud_active_jobs ?? raw.cloud_running ?? raw.cloud_burst_jobs);
  return {
    batch_id: asString(raw.batch_id),
    mode: mode === "TURBO" || mode === "EMERGENCY" ? mode : "AUTO",
    priority:
      priority === "URGENT" || priority === "HIGH" || priority === "BACKGROUND"
        ? priority
        : "NORMAL",
    cloud_ranges: asNumber(raw.cloud_ranges),
    rpc_ranges: asNumber(raw.rpc_ranges),
    pending_ranges: asNumber(raw.pending_ranges),
    running_ranges: asNumber(raw.running_ranges),
    completed_ranges: asNumber(raw.completed_ranges),
    failed_ranges: asNumber(raw.failed_ranges),
    covered_blocks: asNumber(raw.covered_blocks),
    total_blocks: asNumber(raw.total_blocks),
    coverage_percent: asNumber(raw.coverage_percent ?? raw.available_coverage_percent),
    relevant_range_coverage_percent: relevantCoverage,
    relevant_range_certification: asString(
      raw.relevant_range_certification ?? raw.relevant_certification,
    ),
    rows_per_second: asNumber(raw.rows_per_second),
    cloud_jobs: cloudJobs,
    cloud_rows_per_second: asNumber(raw.cloud_rows_per_second),
    rpc_workers: asNumber(raw.rpc_workers ?? raw.rpc_active_workers ?? raw.rpc_running),
    rpc_rows_per_second: asNumber(raw.rpc_rows_per_second),
    parser_rows_per_second: asNumber(raw.parser_rows_per_second ?? raw.parsed_rows_per_second),
    clickhouse_rows_per_second: asNumber(
      raw.clickhouse_rows_per_second ?? raw.inserted_rows_per_second,
    ),
    time_to_first_data_seconds: asNumber(raw.time_to_first_data_seconds ?? raw.ttfa_seconds),
    time_to_first_relevant_range_seconds: asNumber(
      raw.time_to_first_relevant_range_seconds ?? raw.time_to_first_relevant_seconds ?? raw.ttfr_seconds,
    ),
    eta_seconds: asNumber(raw.eta_seconds),
    cloud_available: asBoolean(raw.cloud_available),
    rpc_available: asBoolean(raw.rpc_available),
    burst_active:
      asBoolean(raw.burst_active ?? raw.emergency_burst_active) ||
      burstLevel === "L3" ||
      asNumber(raw.cloud_burst_jobs) > 1,
    burst_level: burstLevel,
    backpressure_active: asBoolean(raw.backpressure_active ?? raw.cloud_paused_by_governor),
    preemption_active: asBoolean(raw.preemption_active),
    work_stealing_active: asBoolean(raw.work_stealing_active),
    reshard_active: asBoolean(
      raw.reshard_active ?? raw.re_shard_active ?? raw.dynamic_reshard_active,
    ),
    hedge_active: asBoolean(raw.hedge_active),
  };
}

export async function addressAction(addressId: string, action: string): Promise<AddressJob | null> {
  const { response, payload } = await postJson<AddressJob>(
    `/api/smart-download/addresses/${addressId}/${action}`,
    {},
    `${action} 失败`,
  );
  return response.ok ? payload : null;
}

export async function getSmartDownloadCapabilities(
  chainKey: string,
  mode: DownloadMode,
): Promise<SmartDownloadCapabilities | null> {
  try {
    const params = new URLSearchParams({ chain_key: chainKey, mode, capabilities_only: "true" });
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/status?${params.toString()}`,
      "查询智能下载能力失败",
    );
    if (!response.ok) return null;
    const raw = asRecord(payload);
    if (!Array.isArray(raw.available_datasets)) return null;
    return { available_datasets: asStrings(raw.available_datasets) };
  } catch {
    return null;
  }
}

export async function listBatches(): Promise<BatchJob[]> {
  try {
    const { response, payload } = await getJson<unknown>(
      "/api/smart-download/batches",
      "查询任务列表失败",
    );
    if (!response.ok) return [];
    const envelope = asRecord(payload);
    return asArray(envelope.batches ?? payload).map(normalizeBatchJob).filter((batch) => batch.id);
  } catch {
    return [];
  }
}

export async function batchDetail(batchId: string): Promise<{
  batch: BatchJob;
  addresses: AddressDetail[];
} | null> {
  try {
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/batches/${batchId}`,
      "查询任务详情失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    return {
      batch: normalizeBatchJob(envelope.batch),
      addresses: asArray(envelope.addresses).map(normalizeAddressDetail),
    };
  } catch {
    return null;
  }
}

export interface AddressDetail {
  address: AddressJob;
  datasets: Array<{ dataset: DatasetJob; ranges: RangeJob[] }>;
}

function normalizeProgress(value: unknown): ProgressSnapshot {
  const raw = asRecord(value);
  return {
    percent: asNumber(raw.percent),
    rows_current: asNumber(raw.rows_current),
    rows_total: asNumber(raw.rows_total),
    blocks_current: asNumber(raw.blocks_current),
    blocks_total: asNumber(raw.blocks_total),
    bytes_current: asNumber(raw.bytes_current),
    bytes_total: asNumber(raw.bytes_total),
    speed_rows_per_sec: asNumber(raw.speed_rows_per_sec),
    eta_seconds: asNumber(raw.eta_seconds),
    eta_confidence: asNumber(raw.eta_confidence),
  };
}

function normalizeAddressJob(value: unknown): AddressJob {
  const raw = asRecord(value);
  return {
    ...(raw as unknown as AddressJob),
    id: asString(raw.id) ?? "",
    batch_id: asString(raw.batch_id) ?? "",
    address: asString(raw.address) ?? "",
    chain_key: asString(raw.chain_key) ?? "unknown",
    status: asString(raw.status) ?? "UNKNOWN",
    range: asRecord(raw.range) as unknown as RangeSpec,
    progress: normalizeProgress(raw.progress),
    created_at: asString(raw.created_at) ?? "",
  };
}

function normalizeAddressDetail(value: unknown): AddressDetail {
  const raw = asRecord(value);
  return {
    address: normalizeAddressJob(raw.address),
    datasets: asArray(raw.datasets).map((item) => {
      const entry = asRecord(item);
      const dataset = asRecord(entry.dataset);
      return {
        dataset: {
          ...(dataset as unknown as DatasetJob),
          id: asString(dataset.id) ?? "",
          status: asString(dataset.status) ?? "UNKNOWN",
          downloaded_rows: asNumber(dataset.downloaded_rows),
          progress: normalizeProgress(dataset.progress),
          cloud_reasons: asStrings(dataset.cloud_reasons),
          activity_segments: asArray(dataset.activity_segments) as DatasetJob["activity_segments"],
        },
        ranges: asArray(entry.ranges).map((rangeValue) => {
          const range = asRecord(rangeValue);
          return {
            ...(range as unknown as RangeJob),
            id: asString(range.id) ?? "",
            status: asString(range.status) ?? "UNKNOWN",
            failed_providers: asStrings(range.failed_providers),
            rows_committed: asNumber(range.rows_committed),
            bytes: asNumber(range.bytes),
            attempts: asNumber(range.attempts),
          };
        }),
      };
    }),
  };
}

export async function listBatchAddresses(
  batchId: string,
  page: number,
  pageSize: number,
  status?: string,
): Promise<{ addresses: AddressJob[]; total: number } | null> {
  try {
    const query = new URLSearchParams({
      page: String(page),
      page_size: String(pageSize),
    });
    if (status) query.set("status", status);
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/batches/${batchId}/addresses?${query.toString()}`,
      "查询地址任务失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    const addresses = asArray(envelope.addresses).map(normalizeAddressJob).filter((item) => item.id);
    return { addresses, total: asNumber(envelope.total, addresses.length) };
  } catch {
    return null;
  }
}

export async function addressDetail(addressId: string): Promise<AddressDetail | null> {
  try {
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/addresses/${addressId}`,
      "查询地址详情失败",
    );
    return response.ok ? normalizeAddressDetail(payload) : null;
  } catch {
    return null;
  }
}

export async function datasetLedger(datasetJobId: string): Promise<LedgerEntry[]> {
  try {
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/datasets/${datasetJobId}/ledger`,
      "查询账本失败",
    );
    if (!response.ok) return [];
    const envelope = asRecord(payload);
    return asArray(envelope.ledger ?? payload) as LedgerEntry[];
  } catch {
    return [];
  }
}

export async function listRegistry(): Promise<IndexedResult[]> {
  try {
    const { response, payload } = await getJson<unknown>(
      "/api/smart-download/registry",
      "查询结果注册表失败",
    );
    if (!response.ok) return [];
    const envelope = asRecord(payload);
    return asArray(envelope.results ?? payload) as IndexedResult[];
  } catch {
    return [];
  }
}

export async function queryResults(
  datasetJobId: string,
  page: number,
  pageSize: number,
  sort?: string,
  filter?: string,
): Promise<{ rows: Array<Record<string, unknown>>; total: number } | null> {
  try {
    const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
    if (sort) query.set("sort", sort);
    if (filter) query.set("filter", filter);
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/results/${datasetJobId}?${query.toString()}`,
      "查询结果失败",
    );
    // 过滤字段不适用于当前数据集，或没有可查询的匹配分片时，后端目前会返回 404。
    // 对用户而言这是一个合法的空结果，不应升级为全局错误。
    if (!response.ok && response.status === 404 && Boolean(filter)) return { rows: [], total: 0 };
    if (!response.ok) throw apiFailure(response, payload, "查询结果失败");
    const envelope = asRecord(payload);
    const rows = asArray(envelope.rows) as Array<Record<string, unknown>>;
    return { rows, total: asNumber(envelope.total, rows.length) };
  } catch (error) {
    if (error instanceof SmartDownloadApiError) throw error;
    throw new SmartDownloadApiError(smartDownloadErrorMessage(error, "查询结果失败"));
  }
}

export async function resultSummary(
  datasetJobId: string,
): Promise<Record<string, unknown> | null> {
  try {
    const { response, payload } = await getJson<Record<string, unknown>>(
      `/api/smart-download/results/${datasetJobId}/summary`,
      "查询结果摘要失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

export async function batchSnapshot(batchId: string): Promise<BatchSnapshot | null> {
  try {
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/jobs/${batchId}/snapshot`,
      "查询批次快照失败",
    );
    return response.ok ? normalizeBatchSnapshot(payload) : null;
  } catch {
    return null;
  }
}

export async function batchSummary(batchId: string): Promise<BatchSummary | null> {
  try {
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/jobs/${batchId}/summary`,
      "查询批次摘要失败",
    );
    if (!response.ok) return null;
    const raw = asRecord(payload);
    return {
      snapshot: normalizeBatchSnapshot(raw.snapshot),
      counts: asRecord(raw.counts) as Record<string, number>,
      total: asNumber(raw.total),
      running: asNumber(raw.running),
      queued: asNumber(raw.queued),
      attention: asNumber(raw.attention),
      throughput_rows_per_sec: asNumber(raw.throughput_rows_per_sec),
    };
  } catch {
    return null;
  }
}

export async function planBatch(batchId: string): Promise<ExecutionPlan | null> {
  try {
    const { response, payload } = await postJson<unknown>(
      `/api/smart-download/batches/${batchId}/plan`,
      {},
      "分析数据规模失败",
    );
    if (!response.ok) return null;
    const raw = asRecord(payload);
    return {
      batch_id: asString(raw.batch_id) ?? batchId,
      datasets: asArray(raw.datasets) as ExecutionPlan["datasets"],
    };
  } catch {
    return null;
  }
}

function normalizeBatchSnapshot(value: unknown): BatchSnapshot {
  const raw = asRecord(value);
  const eta = asRecord(raw.eta);
  return {
    entity_type: asString(raw.entity_type) ?? "batch",
    entity_id: asString(raw.entity_id) ?? "",
    status: asString(raw.status) ?? "UNKNOWN",
    progress_percent: asNumber(raw.progress_percent),
    ranges_current: asNumber(raw.ranges_current),
    ranges_total: asNumber(raw.ranges_total),
    rows_current: asNumber(raw.rows_current),
    eta: {
      seconds: asNumber(eta.seconds),
      lower_bound_seconds: asNumber(eta.lower_bound_seconds),
      upper_bound_seconds: asNumber(eta.upper_bound_seconds),
      confidence: asString(eta.confidence) ?? "LOW",
      recalculating: asBoolean(eta.recalculating),
      based_on: asString(eta.based_on),
    },
    updated_at: asString(raw.updated_at) ?? "",
  };
}

export async function getBatchHardening(batchId: string): Promise<HardeningStatus | null> {
  try {
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/batches/${encodeURIComponent(batchId)}/hardening`,
      "生产加固状态加载失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    const raw = asRecord(envelope.hardening ?? envelope.status ?? payload);
    const eta = asRecord(raw.eta_v2 ?? raw.eta);
    const pipeline = asRecord(raw.pipeline ?? raw.throughput_pipeline);
    const stall = asRecord(raw.stall ?? raw.stall_detector);
    const recoveryItems = asArray(raw.self_recovery ?? raw.recovery_actions);
    const recovery = asRecord(raw.recovery);
    const guards = asRecord(raw.guards);
    const failure = asRecord(raw.failure_summary ?? raw.failure);
    const advanced = asRecord(raw.advanced);
    const selfRecoveryValue = raw.self_recovery ?? raw.recovery_active;
    return {
      batch_id: asString(raw.batch_id) ?? batchId,
      eta_seconds: asNumber(eta.seconds ?? raw.eta_seconds),
      eta_lower_seconds: asNumber(eta.lower_bound_seconds ?? eta.lower_seconds),
      eta_upper_seconds: asNumber(eta.upper_bound_seconds ?? eta.upper_seconds),
      eta_confidence: normalizeConfidence(eta.confidence ?? raw.eta_confidence),
      eta_basis: asStrings(eta.basis ?? raw.eta_basis),
      bottleneck: asString(asRecord(raw.bottleneck).code ?? raw.bottleneck ?? raw.bottleneck_stage) ?? "UNKNOWN",
      pipeline: {
        download: normalizeStage(pipeline.download ?? pipeline.source ?? { rows_per_second: pipeline.download_rows_per_second ?? asRecord(raw.download).rows_per_second }),
        parse: normalizeStage(pipeline.parse ?? pipeline.parser ?? { rows_per_second: pipeline.parse_rows_per_second ?? asRecord(raw.parse).rows_per_second }),
        clickhouse: normalizeStage(pipeline.clickhouse ?? pipeline.db ?? pipeline.writer ?? { rows_per_second: pipeline.clickhouse_rows_per_second ?? asRecord(raw.clickhouse).rows_per_second }),
      },
      slowest_stage: asString(raw.slowest_stage ?? pipeline.slowest_stage),
      stalled: asBoolean(stall.detected ?? stall.stalled ?? raw.stalled),
      stall_stage: asString(stall.stage ?? raw.stall_stage),
      stall_seconds: asNumber(stall.seconds ?? stall.duration_seconds ?? raw.stall_seconds),
      self_recovery: asBoolean(stall.recovering) || (typeof selfRecoveryValue === "object"
        ? asBoolean(recovery.active ?? recovery.running)
        : asBoolean(selfRecoveryValue)),
      recovery_status: asString(recovery.status ?? raw.recovery_status),
      recovery_actions: recoveryItems.map((item) => {
        const action = asRecord(item);
        return [asString(action.stage), asString(action.action), asString(action.result)].filter(Boolean).join(" · ");
      }).filter(Boolean).concat(asStrings(recovery.actions)),
      failure: Object.keys(failure).length > 0 ? {
        stage: asString(failure.stage),
        dataset: asString(failure.dataset),
        range: asString(failure.range ?? failure.range_id),
        provider: asString(failure.provider),
        error_type: asString(failure.error_type ?? failure.type),
        completed_percent: asNumber(failure.completed_percent ?? failure.progress_percent),
        checkpoint: asString(failure.checkpoint ?? failure.resume_from ?? failure.resume_point),
        recommended_action: asString(failure.recommended_action ?? failure.action),
      } : undefined,
      guards: {
        storage: normalizeGuard(guards.storage ?? raw.storage_guard),
        rpc: normalizeGuard(guards.rpc ?? raw.rpc_guard),
        cloud: normalizeGuard(guards.cloud ?? raw.cloud_guard),
        clickhouse: normalizeGuard(guards.clickhouse ?? raw.clickhouse_guard),
      },
      cloud_jobs: asArray(advanced.cloud_jobs ?? raw.cloud_jobs),
      rpc_workers: asArray(advanced.rpc_workers ?? raw.rpc_workers),
      range_ledger: asArray(advanced.range_ledger ?? raw.range_ledger),
      retries: asArray(advanced.retries ?? raw.retries),
      errors: asArray(advanced.errors ?? raw.errors),
      checkpoints: asArray(advanced.checkpoints ?? raw.checkpoints),
      gap_repairs: asArray(advanced.gap_repair ?? advanced.gap_repairs ?? raw.gap_repair ?? raw.gap_repairs),
    };
  } catch {
    return null;
  }
}

function normalizeTemplate(value: unknown): TaskTemplate {
  const raw = asRecord(value);
  const config = asRecord(raw.request ?? raw.configuration ?? raw.config ?? raw.template);
  const rawDatasets = raw.datasets ?? config.datasets;
  return {
    id: asString(raw.id) ?? "",
    name: asString(raw.name) ?? "未命名模板",
    description: asString(raw.description),
    resource_profile: asResourceProfile(raw.resource_profile ?? config.resource_profile),
    chain_key: asString(raw.chain_key ?? config.chain_key),
    datasets: asStrings(rawDatasets) as Dataset[],
    configuration: config as TaskTemplate["configuration"],
    created_at: asString(raw.created_at),
    updated_at: asString(raw.updated_at),
  };
}

export async function listTaskTemplates(): Promise<TaskTemplate[]> {
  try {
    const { response, payload } = await getJson<unknown>(
      "/api/smart-download/templates",
      "任务模板加载失败",
    );
    if (!response.ok) return [];
    const envelope = asRecord(payload);
    return asArray(envelope.templates ?? payload).map(normalizeTemplate).filter((item) => item.id);
  } catch {
    return [];
  }
}

export async function saveTaskTemplate(input: {
  name: string;
  description?: string;
  resource_profile: ResourceProfile;
  configuration: CreateBatchRequest;
}): Promise<TaskTemplate | null> {
  try {
    const { response, payload } = await postJson<unknown>(
      "/api/smart-download/templates",
      {
        name: input.name,
        description: input.description,
        request: { ...input.configuration, resource_profile: input.resource_profile },
      },
      "保存任务模板失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    return normalizeTemplate(envelope.template ?? payload);
  } catch {
    return null;
  }
}

export async function deleteTaskTemplate(templateId: string): Promise<boolean> {
  try {
    const response = await fetch(`/api/smart-download/templates/${encodeURIComponent(templateId)}`, {
      method: "DELETE",
    });
    return response.ok;
  } catch {
    return false;
  }
}

export async function instantiateTaskTemplate(
  templateId: string,
  overrides: Record<string, unknown> = {},
): Promise<BatchJob | null> {
  try {
    const { response, payload } = await postJson<unknown>(
      `/api/smart-download/templates/${encodeURIComponent(templateId)}/instantiate`,
      overrides,
      "模板创建任务失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    const raw = envelope.batch ?? payload;
    return asString(asRecord(raw).id) ? normalizeBatchJob(raw) : null;
  } catch {
    return null;
  }
}

function normalizeJobReport(value: unknown, batchId = ""): JobReport {
  const raw = asRecord(value);
  return {
    batch_id: asString(raw.batch_id ?? raw.id) ?? batchId,
    mode: asDownloadMode(raw.mode),
    resource_profile: asResourceProfile(raw.resource_profile ?? raw.profile),
    provider: asString(raw.provider) ?? (asStrings(raw.providers).join(" / ") || undefined),
    rows: asNumber(raw.rows ?? raw.total_rows),
    coverage: asNumber(raw.coverage ?? raw.coverage_percent),
    duplicates: asNumber(raw.duplicates ?? raw.duplicate_count),
    ttfa_seconds: asNumber(raw.ttfa_seconds ?? raw.time_to_first_data_seconds),
    total_duration_seconds: asNumber(raw.total_duration_seconds ?? raw.total_time_seconds ?? raw.duration_seconds),
    peak_throughput_rows_per_sec: asNumber(raw.peak_throughput_rows_per_sec ?? raw.peak_rows_per_second),
    average_throughput_rows_per_sec: asNumber(raw.average_throughput_rows_per_sec ?? raw.avg_rows_per_second),
    retry_count: asNumber(raw.retry_count ?? raw.retries),
    gap_repair_count: asNumber(raw.gap_repair_count ?? raw.gap_repairs),
    certification: asString(raw.certification),
    result: asString(raw.result ?? raw.status),
  };
}

export async function getBatchReport(batchId: string): Promise<JobReport | null> {
  try {
    const { response, payload } = await getJson<unknown>(
      `/api/smart-download/batches/${encodeURIComponent(batchId)}/report`,
      "任务报告加载失败",
    );
    if (!response.ok) return null;
    const envelope = asRecord(payload);
    return normalizeJobReport(envelope.report ?? payload, batchId);
  } catch {
    return null;
  }
}

export async function compareBatchRuns(batchA: string, batchB: string): Promise<CompareRunsResult | null> {
  try {
    const { response, payload } = await postJson<unknown>(
      "/api/smart-download/compare",
      { batch_a: batchA, batch_b: batchB },
      "任务对比失败",
    );
    if (!response.ok) throw apiFailure(response, payload, "任务对比失败");
    const envelope = asRecord(payload);
    const rawRuns = asArray(envelope.runs ?? envelope.comparison ?? [envelope.run_a, envelope.run_b]);
    const runs = rawRuns
      .filter((item) => Object.keys(asRecord(item)).length > 0)
      .map((item): CompareRun => {
        const raw = asRecord(item);
        return {
          batch_id: asString(raw.batch_id ?? raw.id) ?? "",
          label: asString(raw.label ?? raw.name),
          resource_profile: asResourceProfile(raw.resource_profile ?? raw.profile),
          ttfa_seconds: asNumber(raw.ttfa_seconds),
          total_duration_seconds: asNumber(raw.total_duration_seconds ?? raw.total_time_seconds ?? raw.duration_seconds),
          average_throughput_rows_per_sec: asNumber(raw.average_throughput_rows_per_sec ?? raw.avg_rows_per_second),
          failure_rate: asNumber(raw.failure_rate),
          rows: asNumber(raw.rows ?? raw.total_rows),
        };
      });
    return { runs, deltas: asRecord(envelope.deltas ?? envelope.delta) as Record<string, number> };
  } catch (error) {
    if (error instanceof SmartDownloadApiError) throw error;
    throw new SmartDownloadApiError(smartDownloadErrorMessage(error, "任务对比失败"));
  }
}

/** 触发导出下载：≤30 万行 XLSX，>30 万行 CSV（后端按行数自动选格式）。 */
export function downloadResultExport(datasetJobId: string) {
  const a = document.createElement("a");
  a.href = `/api/smart-download/results/${datasetJobId}/export`;
  a.click();
}
