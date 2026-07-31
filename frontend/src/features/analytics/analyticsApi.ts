import { getJson } from "../../api/client";

export interface DashboardOverview {
  address_count: number;
  token_count: number;
  transaction_count: number;
  transfer_count: number;
  risk_addresses: number;
  trend: { block: string; events: number }[];
}

export interface AddressProfile {
  address: string;
  first_activity_time: string;
  last_activity_time: string;
  event_count: number;
  transaction_count: number;
  contract_count: number;
  token_count: number;
  total_in: number;
  total_out: number;
  active_days: number;
  risk_score: number;
}

export interface FlowEdge {
  direction: string;
  token: string;
  counterparty: string;
  amount: string;
  block: string;
  tx_hash: string;
}

export interface RiskResult {
  risk_score: number;
  risk_level: string;
  risk_reason: string;
  transaction_frequency: number;
  top_holder_ratio: number;
  shared_counterparty_score: number;
}

export interface PathItem {
  a: string;
  b: string;
  c: string;
  token: string;
  amount: string;
}

export interface GraphData {
  nodes: { id: string; type: string; risk_score: number; degree: number; pagerank: number }[];
  edges: { source: string; target: string; kind: string; token?: string; amount?: string; tx_count?: number }[];
}

export async function fetchDashboard(): Promise<DashboardOverview | null> {
  const r = await getJson<DashboardOverview>("/api/analytics/dashboard", "Dashboard 加载失败");
  return r.payload ?? null;
}

export async function fetchProfile(address: string): Promise<AddressProfile | null> {
  const r = await getJson<AddressProfile>(`/api/analytics/address/${address}/profile`, "画像查询失败");
  return r.payload ?? null;
}

export async function fetchFlows(address: string, token?: string): Promise<FlowEdge[]> {
  const q = token ? `?token=${token}` : "";
  const r = await getJson<FlowEdge[]>(`/api/analytics/address/${address}/flows${q}`, "资金流查询失败");
  return r.payload ?? [];
}

export async function fetchRisk(address: string): Promise<RiskResult | null> {
  const r = await getJson<RiskResult>(`/api/analytics/address/${address}/risk`, "风险查询失败");
  return r.payload ?? null;
}

export async function fetchPaths(address: string): Promise<PathItem[]> {
  const r = await getJson<PathItem[]>(`/api/analytics/address/${address}/path`, "路径查询失败");
  return r.payload ?? [];
}

export async function fetchGraph(limit = 500): Promise<GraphData | null> {
  const r = await getJson<GraphData>(`/api/analytics/graph?limit=${limit}`, "图谱加载失败");
  return r.payload ?? null;
}
