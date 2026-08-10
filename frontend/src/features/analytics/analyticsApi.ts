import { getJson } from "../../api/client";
import {
  fetchClickHouseActivityPage,
  fetchClickHouseAddressSummary,
} from "./ClickHouseExplorerApi";

export {
  fetchClickHouseActivityPage,
  fetchClickHouseAddressSummary,
  fetchClickHouseContractDetail,
  fetchClickHouseContractCreations,
  fetchClickHouseCounterparties,
  fetchClickHouseDailyStats,
  fetchClickHouseInternalTransactions,
  fetchClickHouseTokenMetadata,
  fetchClickHouseTokenTransfers,
  fetchClickHouseTransactionDetail,
  fetchClickHouseTransactions,
} from "./ClickHouseExplorerApi";
export type {
  ClickHouseActivity,
  ClickHouseActivityKind,
  ClickHouseActivityPage,
  ClickHouseActivityQuery,
  ClickHouseAddressSummary,
  ClickHouseContractDetail,
  ClickHouseCounterpartyStat,
  ClickHouseDailyStat,
  ClickHouseDailyStatsQuery,
  ClickHouseTokenMetadata,
  ClickHouseTransactionDetail,
  ExplorerChain,
} from "./ClickHouseExplorerTypes";

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
  edges: { source: string; target: string; kind: string; token?: string; amount?: string; historical_value_usdt?: string; valuation_status?: string; tx_count?: number }[];
}

export async function fetchDashboard(): Promise<DashboardOverview | null> {
  const r = await getJson<DashboardOverview>("/api/analytics/dashboard", "Dashboard 加载失败");
  return r.payload ?? null;
}

export async function fetchProfile(address: string): Promise<AddressProfile | null> {
  const summary = await fetchClickHouseAddressSummary(address);
  return {
    address: summary.address,
    first_activity_time: summary.first_seen_time,
    last_activity_time: summary.last_seen_time,
    event_count: summary.transaction_count + summary.token_transfer_count + summary.internal_transaction_count + summary.contract_created_count,
    transaction_count: summary.transaction_count,
    contract_count: summary.contract_created_count,
    token_count: summary.token_transfer_count,
    total_in: Number(summary.native_received || 0),
    total_out: Number(summary.native_sent || 0),
    active_days: summary.active_days,
    risk_score: summary.risk_score,
  };
}

export async function fetchFlows(address: string, token?: string): Promise<FlowEdge[]> {
  const page = await fetchClickHouseActivityPage(address, "activity", { pageSize: 200 });
  const items = page.items ?? [];
  const normalizedToken = token?.trim().toLowerCase();
  return items
    .filter((item) => !normalizedToken || item.token_address.toLowerCase() === normalizedToken)
    .map((item) => ({
      direction: item.direction === "IN" ? "incoming" : item.direction === "SELF" ? "self" : "outgoing",
      token: item.token_address || "native",
      counterparty: item.counterparty_address,
      amount: item.amount,
      block: String(item.block_number),
      tx_hash: item.transaction_hash,
    }));
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
  const r = await getJson<Partial<GraphData>>(`/api/analytics/graph?limit=${limit}`, "图谱加载失败");
  if (!r.payload || typeof r.payload !== "object") return null;
  return {
    nodes: Array.isArray(r.payload.nodes) ? r.payload.nodes : [],
    edges: Array.isArray(r.payload.edges) ? r.payload.edges : [],
  };
}
