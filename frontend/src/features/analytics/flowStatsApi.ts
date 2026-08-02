// V2.0 统计体系 API（设计 §19）
import { getJson } from "../../api/client";

export interface FlowStatsScope {
  chain: string;
  chain_id: number;
  token: string;
  start_date: string;
  end_date: string;
}

export interface FlowGraphStats {
  node_count: number;
  edge_count: number;
  tx_count: number;
}

export interface FlowFlowStats {
  total_in: string;
  total_out: string;
  net: string;
}

export interface FlowEntityStats {
  exchange: number;
  contract: number;
  eoa: number;
  risk: number;
}

export interface FlowCompleteness {
  truncated: boolean;
  complete: boolean;
}

export interface FlowStats {
  scope: FlowStatsScope;
  graph: FlowGraphStats;
  flow: FlowFlowStats;
  entities: FlowEntityStats;
  completeness: FlowCompleteness;
}

export interface AddressStats {
  address: string;
  tx_count: number;
  in_count: number;
  out_count: number;
  unique_upstream: number;
  unique_downstream: number;
  active_days: number;
  first_seen: string;
  last_seen: string;
  avg_amount: string;
  max_amount: string;
  total_in: string;
  total_out: string;
  net_flow: string;
  dominant_token: string;
  top1_source_ratio: number;
  top5_source_ratio: number;
  top1_target_ratio: number;
  top5_target_ratio: number;
  recent_24h: number;
  recent_7d: number;
  recent_30d: number;
}

export async function fetchFlowStats(chain = "bsc", chainId = 56, token = ""): Promise<FlowStats | null> {
  const q = new URLSearchParams({ chain, chain_id: String(chainId) });
  if (token) q.set("token", token);
  const r = await getJson<FlowStats>(`/api/analytics/flow-stats?${q}`, "图统计加载失败");
  return r.payload ?? null;
}

export async function fetchAddressStats(address: string, token = ""): Promise<AddressStats | null> {
  const q = new URLSearchParams({ address });
  if (token) q.set("token", token);
  const r = await getJson<AddressStats>(`/api/analytics/address-stats?${q}`, "地址统计加载失败");
  return r.payload ?? null;
}
