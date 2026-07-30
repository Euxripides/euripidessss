import type {
  DataSourceConfig,
  DataSourceItem,
  DataSourceSnapshot,
  DataSourceTestResult,
} from '../types/datasource';

const BASE = '/api/crypto/datasource';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${BASE}${path}`, init);
  const text = await response.text();
  const payload = text ? JSON.parse(text) : {};
  if (!response.ok) {
    throw new Error(payload.detail || payload.message || `请求失败（HTTP ${response.status}）`);
  }
  return payload as T;
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
});

export const dataSourceApi = {
  snapshot: () => request<DataSourceSnapshot>('/list'),
  metrics: () => request<DataSourceSnapshot>('/metrics'),
  config: (id: string) => request<DataSourceConfig>(`/config?id=${encodeURIComponent(id)}`),
  save: (input: DataSourceConfig) => request<DataSourceItem>('/save', json('POST', input)),
  test: (id: string) => request<DataSourceTestResult>('/test', json('POST', { source_id: id })),
  remove: (id: string) => request<{ success: boolean }>(`/delete?id=${encodeURIComponent(id)}`, { method: 'DELETE' }),
};
