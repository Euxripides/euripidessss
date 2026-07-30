import type {
  EnrichmentJob,
  RpcEndpoint,
  RpcEndpointInput,
  RpcHealthResponse,
  RpcTestResult,
} from './rpcTypes';

const RPC = '/api/crypto/rpc';
const JOBS = '/api/crypto/enrichment/jobs';

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init);
  const text = await response.text();
  const payload = text ? JSON.parse(text) : {};
  if (!response.ok) throw new Error(payload.detail || `请求失败（HTTP ${response.status}）`);
  return payload as T;
}

const json = (method: string, body?: unknown): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: body === undefined ? undefined : JSON.stringify(body),
});

export const rpcApi = {
  health: () => request<RpcHealthResponse>(`${RPC}/health`),
  create: (input: RpcEndpointInput) => request<RpcEndpoint>(`${RPC}/endpoints`, json('POST', input)),
  update: (id: string, input: Partial<RpcEndpointInput>) =>
    request<RpcEndpoint>(`${RPC}/endpoints/${encodeURIComponent(id)}`, json('PUT', input)),
  remove: (id: string) => request<{ success: boolean }>(`${RPC}/endpoints/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  test: (id: string) => request<RpcTestResult>(`${RPC}/endpoints/${encodeURIComponent(id)}/test`, json('POST', {})),
  routing: (chain: string, ids: string[]) => request<{ success: boolean }>(
    `${RPC}/routing/${encodeURIComponent(chain)}`, json('PUT', { endpoint_ids: ids }),
  ),
  jobs: async () => (await request<{ items: EnrichmentJob[] }>(JOBS)).items,
  startJob: (input: { job_type: string; chain_key: string; items: string[] }) =>
    request<EnrichmentJob>(JOBS, json('POST', input)),
  cancelJob: (id: string) => request<EnrichmentJob>(`${JOBS}/${encodeURIComponent(id)}/cancel`, json('POST', {})),
};
