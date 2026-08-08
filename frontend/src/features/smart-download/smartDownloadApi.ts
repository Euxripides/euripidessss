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
}): Promise<{ batch: BatchJob } | null> {
  const { response, payload } = await postJson<{ batch: BatchJob }>(
    "/api/smart-download/batches",
    body,
    "创建下载任务失败",
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

/** 触发导出下载：≤30 万行 XLSX，>30 万行 CSV（后端按行数自动选格式）。 */
export function downloadResultExport(datasetJobId: string) {
  const a = document.createElement("a");
  a.href = `/api/smart-download/results/${datasetJobId}/export`;
  a.click();
}
