import { getJson, postForm, postJson } from '../../api/client';

const BASE = '/api/crypto/parquet';

export type ParquetSettings = {
  readonly data_root: string;
  readonly download_concurrency: number;
  readonly duckdb_threads: number;
  readonly memory_limit: string;
  readonly minimum_free_gb: number;
  readonly keep_source_files: boolean;
  readonly export_csv: boolean;
};

export type AddressSummary = {
  readonly input: number;
  readonly valid: number;
  readonly invalid: number;
  readonly duplicates: number;
  readonly addresses?: readonly string[];
  readonly invalid_items?: readonly string[];
};

export type ParquetSourceObject = {
  readonly key: string;
  readonly uri: string;
  readonly data_type: string;
  readonly source_date: string;
  readonly size_bytes: number;
  readonly etag: string;
  readonly last_modified?: string;
};

export type ParquetPreview = {
  readonly chain_key: string;
  readonly chain_id: number;
  readonly native_symbol: string;
  readonly addresses: AddressSummary;
  readonly files: readonly ParquetSourceObject[];
  readonly total_bytes: number;
  readonly free_bytes: number;
  readonly warnings: readonly string[];
};

export type ParquetStage = {
  readonly key: string;
  readonly label: string;
  readonly status: string;
  readonly progress: number;
  readonly detail?: string;
};

export type ParquetFileTask = ParquetSourceObject & {
  readonly local_path?: string;
  readonly output_path?: string;
  readonly csv_path?: string;
  readonly status: string;
  readonly progress: number;
  readonly downloaded_bytes: number;
  readonly source_rows: number;
  readonly matched_rows: number;
  readonly retry_count: number;
  readonly error?: string;
};

export type ParquetJob = {
  readonly id: string;
  readonly chain_key: string;
  readonly chain_id: number;
  readonly native_symbol: string;
  readonly status: string;
  readonly stage: string;
  readonly progress: number;
  readonly addresses: AddressSummary;
  readonly start_date: string;
  readonly end_date: string;
  readonly total_bytes: number;
  readonly downloaded_bytes: number;
  readonly download_speed_bps: number;
  readonly eta_seconds: number;
  readonly source_rows: number;
  readonly matched_rows: number;
  readonly failed_files: number;
  readonly files: readonly ParquetFileTask[];
  readonly stages: readonly ParquetStage[];
  readonly outputs: readonly string[];
  readonly warnings: readonly string[];
  readonly error?: string;
  readonly keep_source_files: boolean;
  readonly export_csv: boolean;
  readonly created_at: string;
  readonly updated_at: string;
  readonly finished_at?: string;
};

export type ParquetStartPayload = {
  readonly chain_key: 'bsc';
  readonly addresses: string;
  readonly start_date: string;
  readonly end_date: string;
  readonly keep_source_files?: boolean;
  readonly export_csv?: boolean;
};

export async function loadParquetSettings() {
  const { response, payload } = await getJson<ParquetSettings>(`${BASE}/settings`, '读取 Parquet 设置失败');
  if (!response.ok) throw apiError(payload, response.status, '读取 Parquet 设置失败');
  return payload;
}

export async function saveParquetSettings(settings: ParquetSettings) {
  const { response, payload } = await postJson<ParquetSettings>(`${BASE}/settings`, settings, '保存 Parquet 设置失败');
  if (!response.ok) throw apiError(payload, response.status, '保存 Parquet 设置失败');
  return payload;
}

export async function previewParquetTask(payload: ParquetStartPayload) {
  const { response, payload: result } = await postJson<ParquetPreview>(`${BASE}/preview`, payload, '预检 Parquet 分区失败');
  if (!response.ok) throw apiError(result, response.status, '预检 Parquet 分区失败');
  return result;
}

export async function startParquetTask(payload: ParquetStartPayload) {
  const { response, payload: result } = await postJson<ParquetJob>(`${BASE}/start`, payload, '启动 Parquet 任务失败');
  if (!response.ok) throw apiError(result, response.status, '启动 Parquet 任务失败');
  return result;
}

export async function loadParquetJob(id: string) {
  const { response, payload } = await getJson<ParquetJob>(`${BASE}/job?id=${encodeURIComponent(id)}`, '读取 Parquet 任务失败');
  if (!response.ok) throw apiError(payload, response.status, '读取 Parquet 任务失败');
  return payload;
}

export async function loadParquetJobs() {
  const { response, payload } = await getJson<readonly ParquetJob[]>(`${BASE}/jobs`, '读取 Parquet 任务列表失败');
  if (!response.ok) throw apiError(payload, response.status, '读取 Parquet 任务列表失败');
  return payload;
}

export async function cancelParquetTask(id: string) {
  const { response, payload } = await postJson<ParquetJob>(`${BASE}/cancel?id=${encodeURIComponent(id)}`, {}, '取消 Parquet 任务失败');
  if (!response.ok) throw apiError(payload, response.status, '取消 Parquet 任务失败');
  return payload;
}

export async function retryParquetTask(id: string) {
  const { response, payload } = await postJson<ParquetJob>(`${BASE}/retry?id=${encodeURIComponent(id)}`, {}, '重试 Parquet 任务失败');
  if (!response.ok) throw apiError(payload, response.status, '重试 Parquet 任务失败');
  return payload;
}

export async function uploadParquetAddresses(file: File) {
  const body = new FormData();
  body.append('file', file);
  const { response, payload } = await postForm<{ raw: string; summary: AddressSummary }>(
    `${BASE}/addresses/upload`,
    body,
    '读取地址文件失败',
  );
  if (!response.ok) throw apiError(payload, response.status, '读取地址文件失败');
  return payload;
}

export function parquetOutputURL(jobId: string, path: string) {
  return `${BASE}/file?id=${encodeURIComponent(jobId)}&path=${encodeURIComponent(path)}`;
}

function apiError(payload: unknown, status: number, fallback: string) {
  const detail = typeof payload === 'object' && payload && 'detail' in payload
    ? String((payload as { detail?: unknown }).detail ?? '')
    : '';
  return new Error(detail || `${fallback}（HTTP ${status}）`);
}
