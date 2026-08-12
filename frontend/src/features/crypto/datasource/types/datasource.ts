export type DataSourceType = 'STREAM' | 'DATASET' | 'RPC';
export type DataSourceStatus = 'HEALTHY' | 'DEGRADED' | 'RATE_LIMITED' | 'UNAVAILABLE' | 'DISABLED' | 'UNKNOWN' | 'MISCONFIGURED';

export interface DataSourceConfig {
  source_id?: string;
  type: DataSourceType;
  name: string;
  endpoint: string;
  api_key?: string;
  bucket?: string;
  region?: string;
  prefix?: string;
  cache_directory?: string;
  timeout_ms: number;
  max_concurrency: number;
  retry_count: number;
  enabled: boolean;
}

export interface PublicSourceConfig {
  bucket?: string;
  region?: string;
  prefix?: string;
  cache_directory?: string;
  timeout_ms: number;
  max_concurrency: number;
  retry_count: number;
}

export interface DataSourceItem {
  source_id: string;
  type: DataSourceType;
  provider: string;
  name: string;
  description: string;
  endpoint_masked: string;
  secret_configured: boolean;
  chain_keys: string[];
  enabled: boolean;
  status: DataSourceStatus;
  health_score: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
  success_rate: number;
  today_requests: number;
  success_count: number;
  failure_count: number;
  rate_limited_count: number;
  timeout_count: number;
  average_speed_bps: number;
  last_success_at?: string;
  last_failure_at?: string;
  last_error?: string;
  checked_at?: string;
  account_count?: number;
  enabled_accounts?: number;
  healthy_accounts?: number;
  abnormal_accounts?: number;
  config: PublicSourceConfig;
}

export interface DataSourceEvent {
  source_id: string;
  source_name: string;
  status: DataSourceStatus;
  message: string;
  occurred_at: string;
}

export interface DataSourceSnapshot {
  overview: {
    source_count: number;
    healthy_count: number;
    abnormal_count: number;
    today_requests: number;
    cache_hit_rate: number;
  };
  sources: DataSourceItem[];
  events: DataSourceEvent[];
}

export interface DataSourceTestResult {
  success: boolean;
  source_id: string;
  status: DataSourceStatus;
  latency_ms: number;
  dataset?: string;
  latest_block?: number;
  checked_at: string;
  message: string;
}
