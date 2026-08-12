export type RpcStatus = 'HEALTHY' | 'DEGRADED' | 'RATE_LIMITED' | 'UNAVAILABLE' | 'MISCONFIGURED' | 'DISABLED';

export interface RpcHealth {
  endpoint_id: string;
  status: RpcStatus;
  health_score: number;
  latest_block: number;
  block_lag: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
  success_rate_5m: number;
  consecutive_failures: number;
  circuit_state: string;
  last_error_code?: string;
  last_error_message_redacted?: string;
  checked_at?: string;
}

export interface RpcEndpoint {
  endpoint_id: string;
  provider: string;
  chain_key: string;
  chain_id: number;
  display_name: string;
  endpoint_host: string;
  endpoint_masked: string;
  secret_configured: boolean;
  test_endpoint_masked?: string;
  test_endpoint_configured: boolean;
  priority: number;
  enabled: boolean;
  max_rps: number;
  current_rps: number;
  max_concurrency: number;
  request_timeout_ms: number;
  health: RpcHealth;
}

export interface RpcOverview {
  configured_endpoints: number;
  healthy_endpoints: number;
  degraded_endpoints: number;
  today_requests: number;
  cache_hit_rate: number;
  rate_limited_count: number;
}

export interface RpcHealthResponse {
  overview: RpcOverview;
  endpoints: RpcEndpoint[];
  routing: Record<string, RpcEndpoint[]>;
}

export interface RpcEndpointInput {
  provider: string;
  chain_key: string;
  display_name: string;
  endpoint_url: string;
  test_endpoint_url?: string;
  priority: number;
  enabled: boolean;
  max_rps: number;
  max_concurrency: number;
  request_timeout_ms: number;
}

export interface RpcBatchFailure {
  index: number;
  display_name: string;
  detail: string;
}

export interface RpcBatchCreateResponse {
  total: number;
  created_count: number;
  failure_count: number;
  created: RpcEndpoint[] | null;
  failures: RpcBatchFailure[] | null;
}

export interface RpcTestResult {
  success: boolean;
  latest_block: number;
  latency_ms: number;
  status: RpcStatus;
  error_class?: string;
  error_message?: string;
  suggestion?: string;
  endpoint_role: 'PRIMARY' | 'TEST';
}

export interface EnrichmentJob {
  job_id: string;
  job_type: string;
  chain_key: string;
  status: string;
  total_items: number;
  completed_items: number;
  succeeded_items: number;
  failed_items: number;
  cache_hits: number;
  updated_at: string;
}
