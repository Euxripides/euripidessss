import { getJson, postJson } from '../../api/client';

export type CryptoDownloadSource = 'rpc' | 'csv' | 'browser';

export type CryptoDownloadAddressChain = {
  readonly address: string;
  readonly chain: string;
};

export type CryptoDownloadStartValues = {
  readonly source: CryptoDownloadSource;
  readonly addresses: string;
  readonly addressChains: readonly CryptoDownloadAddressChain[];
  readonly chains: string;
  readonly rpcUrl?: string;
  readonly rpcConfig?: string;
  readonly nativeSymbol?: string;
  readonly csvEmail?: string;
  readonly csvImapHost?: string;
  readonly csvImapPort?: number;
  readonly csvImapUser?: string;
  readonly csvImapPassword?: string;
  readonly csvStartTime?: number;
  readonly csvEndTime?: number;
  readonly csvRequestHar?: string;
  readonly startBlock?: number;
  readonly endBlock?: number;
  readonly cutoffBlock?: number;
  readonly traceMode?: string;
  readonly blockBatch?: number;
  readonly logBatch?: number;
  readonly workers?: number;
  readonly rps?: number;
  readonly timeoutSeconds?: number;
  readonly retries?: number;
  readonly pageSize?: number;
  readonly rawDir?: string;
  readonly outputDir?: string;
  readonly outputPrefix?: string;
  readonly details?: boolean;
  readonly scanNative?: boolean;
  readonly incremental?: boolean;
  readonly riskCooldownSecs?: number;
};

export type CryptoDownloadSettings = Partial<Pick<
  CryptoDownloadStartValues,
  | 'source'
  | 'csvEmail'
  | 'csvImapHost'
  | 'csvImapPort'
  | 'csvImapUser'
  | 'csvImapPassword'
  | 'csvStartTime'
  | 'csvEndTime'
  | 'workers'
  | 'rps'
  | 'timeoutSeconds'
  | 'rawDir'
  | 'outputDir'
  | 'outputPrefix'
  | 'incremental'
  | 'riskCooldownSecs'
>>;

export type CryptoDownloadPart = {
  readonly key?: string;
  readonly chain?: string;
  readonly kind?: string;
  readonly downloaded?: number;
  readonly total?: number;
  readonly directDownloaded?: number;
  readonly emailDownloaded?: number;
  readonly status?: string;
};

export type CryptoDownloadAddressProgress = {
  readonly index: number;
  readonly address: string;
  readonly chain: string;
  readonly status: string;
  readonly message?: string;
  readonly progress?: number;
  readonly downloaded?: number;
  readonly total?: number;
  readonly startedAt?: string;
  readonly updatedAt?: string;
  readonly finishedAt?: string;
  readonly result?: string;
  readonly errors?: readonly string[];
  readonly parts?: readonly CryptoDownloadPart[];
  readonly cancelRequested?: boolean;
};

export type CryptoDownloadJob = {
  readonly id: string;
  readonly status: string;
  readonly message?: string;
  readonly progress?: number;
  readonly done?: number;
  readonly total?: number;
  readonly running?: boolean;
  readonly needsCredentials?: boolean;
  readonly startedAt?: string;
  readonly finishedAt?: string;
  readonly logs?: readonly string[];
  readonly results?: readonly string[];
  readonly errors?: readonly string[];
  readonly addresses?: readonly CryptoDownloadAddressProgress[];
  readonly taskDir?: string;
  readonly incremental?: boolean;
  readonly queueActive?: number;
  readonly queueWaiting?: number;
  readonly cooldownUntil?: string;
};

const BASE = '/api/crypto/download';

export async function loadCryptoDownloadSettings() {
  const { response, payload } = await getJson<CryptoDownloadSettings>(`${BASE}/settings`, '读取虚拟币下载设置失败');
  if (!response.ok) throw apiError(payload, response.status, '读取虚拟币下载设置失败');
  return payload;
}

export async function saveCryptoDownloadSettings(values: CryptoDownloadSettings) {
  const { response, payload } = await postJson<CryptoDownloadSettings>(`${BASE}/settings`, values, '保存虚拟币下载设置失败');
  if (!response.ok) throw apiError(payload, response.status, '保存虚拟币下载设置失败');
  return payload;
}

export async function startCryptoDownload(values: CryptoDownloadStartValues) {
  const { response, payload } = await postJson<CryptoDownloadJob>(`${BASE}/start`, values, '启动虚拟币下载失败');
  if (!response.ok) throw apiError(payload, response.status, '启动虚拟币下载失败');
  return payload;
}

export async function loadCryptoDownloadJob(id: string) {
  const { response, payload } = await getJson<CryptoDownloadJob>(`${BASE}/job?id=${encodeURIComponent(id)}`, '读取虚拟币下载任务失败');
  if (!response.ok) throw apiError(payload, response.status, '读取虚拟币下载任务失败');
  return payload;
}

export async function loadCryptoDownloadJobs() {
  const { response, payload } = await getJson<readonly CryptoDownloadJob[]>(`${BASE}/jobs`, '读取虚拟币下载任务列表失败');
  if (!response.ok) throw apiError(payload, response.status, '读取虚拟币下载任务列表失败');
  return payload;
}

export async function cancelCryptoDownload(id: string, index?: number) {
  const suffix = index === undefined ? '' : `&index=${encodeURIComponent(index)}`;
  const { response, payload } = await postJson<CryptoDownloadJob>(`${BASE}/cancel?id=${encodeURIComponent(id)}${suffix}`, {}, '取消虚拟币下载失败');
  if (!response.ok) throw apiError(payload, response.status, '取消虚拟币下载失败');
  return payload;
}

export async function resumeCryptoDownload(id: string) {
  const { response, payload } = await postJson<CryptoDownloadJob>(`${BASE}/resume?id=${encodeURIComponent(id)}`, {}, '继续虚拟币下载失败');
  if (!response.ok) throw apiError(payload, response.status, '继续虚拟币下载失败');
  return payload;
}

function apiError(payload: unknown, status: number, fallback: string) {
  const detail = typeof payload === 'object' && payload && 'detail' in payload
    ? String((payload as { detail?: unknown }).detail ?? '')
    : '';
  return new Error(detail || `${fallback}（HTTP ${status}）`);
}
