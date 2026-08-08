// 智能下载统一入口前端 API 封装（Phase 4）。
import { getJson, postJson, postForm } from "../../api/client";

export type Dataset =
  | "transactions"
  | "internal_transactions"
  | "token_transfers"
  | "logs"
  | "balances"
  | "token_metadata"
  | "nft_transfers";

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
  return response.ok ? payload : null;
}

export async function createBatch(body: {
  chain_key: string;
  addresses: string[];
  datasets: Dataset[];
  default_range?: RangeSpec;
  skip_covered?: boolean;
  address_chain_overrides?: Record<string, string>;
  prefetch?: boolean;
  prefetch_priority?: number;
}): Promise<{
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
  return response.ok ? payload : null;
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

export async function addressAction(addressId: string, action: string): Promise<AddressJob | null> {
  const { response, payload } = await postJson<AddressJob>(
    `/api/smart-download/addresses/${addressId}/${action}`,
    {},
    `${action} 失败`,
  );
  return response.ok ? payload : null;
}

export async function listBatches(): Promise<BatchJob[]> {
  try {
    const { response, payload } = await getJson<{ batches: BatchJob[] }>(
      "/api/smart-download/batches",
      "查询任务列表失败",
    );
    return response.ok ? payload.batches ?? [] : [];
  } catch {
    return [];
  }
}

export async function batchDetail(batchId: string): Promise<{
  batch: BatchJob;
  addresses: AddressDetail[];
} | null> {
  try {
    const { response, payload } = await getJson<{ batch: BatchJob; addresses: AddressDetail[] }>(
      `/api/smart-download/batches/${batchId}`,
      "查询任务详情失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

export interface AddressDetail {
  address: AddressJob;
  datasets: Array<{ dataset: DatasetJob; ranges: RangeJob[] }>;
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
    const { response, payload } = await getJson<{ addresses: AddressJob[]; total: number }>(
      `/api/smart-download/batches/${batchId}/addresses?${query.toString()}`,
      "查询地址任务失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

export async function addressDetail(addressId: string): Promise<AddressDetail | null> {
  try {
    const { response, payload } = await getJson<AddressDetail>(
      `/api/smart-download/addresses/${addressId}`,
      "查询地址详情失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

export async function datasetLedger(datasetJobId: string): Promise<LedgerEntry[]> {
  try {
    const { response, payload } = await getJson<{ ledger: LedgerEntry[] }>(
      `/api/smart-download/datasets/${datasetJobId}/ledger`,
      "查询账本失败",
    );
    return response.ok ? payload.ledger ?? [] : [];
  } catch {
    return [];
  }
}

export async function listRegistry(): Promise<IndexedResult[]> {
  try {
    const { response, payload } = await getJson<{ results: IndexedResult[] }>(
      "/api/smart-download/registry",
      "查询结果注册表失败",
    );
    return response.ok ? payload.results ?? [] : [];
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
    const { response, payload } = await getJson<{ rows: Array<Record<string, unknown>>; total: number }>(
      `/api/smart-download/results/${datasetJobId}?${query.toString()}`,
      "查询结果失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
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
    const { response, payload } = await getJson<BatchSnapshot>(
      `/api/smart-download/jobs/${batchId}/snapshot`,
      "查询批次快照失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

export async function batchSummary(batchId: string): Promise<BatchSummary | null> {
  try {
    const { response, payload } = await getJson<BatchSummary>(
      `/api/smart-download/jobs/${batchId}/summary`,
      "查询批次摘要失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

export async function planBatch(batchId: string): Promise<ExecutionPlan | null> {
  try {
    const { response, payload } = await postJson<ExecutionPlan>(
      `/api/smart-download/batches/${batchId}/plan`,
      {},
      "分析数据规模失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

/** 触发导出下载：≤30 万行 XLSX，>30 万行 CSV（后端按行数自动选格式）。 */
export function downloadResultExport(datasetJobId: string) {
  const a = document.createElement("a");
  a.href = `/api/smart-download/results/${datasetJobId}/export`;
  a.click();
}
