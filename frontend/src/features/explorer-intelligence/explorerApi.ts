import { getJson } from "../../api/client";
import type { AnalysisWindow, ExplorerChainKey } from "./analysisContext";
import type { ClickHouseActivityPage, ClickHouseActivityStats, ClickHouseAddressSummary } from "../analytics/ClickHouseExplorerTypes";

export interface ActivityFilterParams {
  transactionHash?: string;
  direction?: string;
  counterparty?: string;
  fromAddress?: string;
  toAddress?: string;
  tokens?: string[];
  tokenSymbol?: string;
  minUSD?: string;
  maxUSD?: string;
  methods?: string[];
  statuses?: string[];
  entityTypes?: string[];
  entity?: string;
  protocols?: string[];
  valuationStatuses?: string[];
  from?: string;
  to?: string;
}

export interface SearchItem {
  kind: "ADDRESS" | "TRANSACTION" | "BLOCK" | "TOKEN" | "CONTRACT" | "ENTITY" | "LABEL";
  title: string;
  subtitle: string;
  value: string;
  chain_id: number;
  verified?: boolean;
  logo_uri?: string;
}

export interface ExplorerHome {
  chain_id: number;
  coverage_ranges: number;
  latest_block: number;
  transaction_count: number;
  token_transfer_count: number;
  latest_transactions: Array<Record<string, unknown>>;
  large_transfers: Array<Record<string, unknown>>;
}

export interface ExplorerHeader {
  identity: { chain_id: number; address: string; address_type: string };
  labels: Array<Record<string, unknown>>;
  balances: { available: boolean; estimated_portfolio_usd: string | null; items: unknown[] };
  coverage: { status: "COMPLETE" | "PARTIAL" | "NO_DATA"; detail?: Record<string, unknown> };
  summary: ExplorerAddressSummary;
  financial: { available: boolean; data: FinancialSummary | null };
}

export type ExplorerAddressSummary = Omit<ClickHouseAddressSummary, "first_seen_time" | "last_seen_time" | "updated_at"> & {
  first_seen_time: string | null;
  last_seen_time: string | null;
  updated_at: string | null;
};

export interface FinancialSummary {
  window: string;
  flow: {
    total_in_usd: string | null;
    total_out_usd: string | null;
    netflow_usd: string | null;
    stablecoin_in_usd: string | null;
    stablecoin_out_usd: string | null;
  };
  largest_in_usd: string | null;
  largest_out_usd: string | null;
  first_funding: string;
  latest_funding: string;
  price_coverage: { activity_count: number; priced_activity_count: number; missing_price_count: number; coverage_ratio: string };
  price_basis: string;
}

export interface CounterpartyFinancial {
  counterparty: string;
  entity_name?: string;
  entity_type?: string;
  entity_role?: string;
  in_usd: string | null;
  out_usd: string | null;
  netflow_usd: string | null;
  in_count: number;
  out_count: number;
  activity_count: number;
  last_interaction: string;
}

async function request<T>(url: string): Promise<T> {
  const result = await getJson<T | { detail?: string }>(url, "Explorer 请求失败");
  if (!result.response.ok) {
    const payload = result.payload as { detail?: string };
    throw new Error(payload?.detail || `Explorer 请求失败 (${result.response.status})`);
  }
  return result.payload as T;
}

export function searchExplorer(chain: ExplorerChainKey, query: string) {
  return request<{ query: string; chain_id: number; items: SearchItem[] }>(
    `/api/v2/explorer/search?chain=${encodeURIComponent(chain)}&q=${encodeURIComponent(query)}`,
  );
}

export function loadExplorerHome(chain: ExplorerChainKey) {
  return request<ExplorerHome>(`/api/v2/explorer/${chain}/home`);
}

export function loadExplorerHeader(chain: ExplorerChainKey, address: string) {
  return request<ExplorerHeader>(`/api/v2/explorer/${chain}/address/${address}/header`);
}

function activityFilterSearchParams(filters?: ActivityFilterParams) {
  const params = new URLSearchParams();
  const set = (key: string, value?: string) => { if (value) params.set(key, value); };
  const list = (key: string, values?: string[]) => { if (values?.length) params.set(key, values.join(",")); };
  set("transaction_hash", filters?.transactionHash); set("direction", filters?.direction); set("counterparty", filters?.counterparty);
  set("from_address", filters?.fromAddress); set("to_address", filters?.toAddress); list("token", filters?.tokens);
  set("token_symbol", filters?.tokenSymbol); set("min_usd", filters?.minUSD); set("max_usd", filters?.maxUSD);
  list("method", filters?.methods); list("status", filters?.statuses); list("entity_type", filters?.entityTypes);
  set("entity", filters?.entity); list("protocol", filters?.protocols); list("valuation_status", filters?.valuationStatuses);
  set("from", filters?.from); set("to", filters?.to);
  return params;
}

export function loadActivity(chain: ExplorerChainKey, address: string, kind: string, pageSize: number, cursor?: string, filters?: ActivityFilterParams) {
  const params = activityFilterSearchParams(filters);
	params.set("page_size", String(pageSize));
  if (cursor) params.set("cursor", cursor);
  return request<ClickHouseActivityPage>(
    `/api/v1/explorer/${chain}/address/${address}/${kind}?${params.toString()}`,
  );
}

export function loadActivityStats(chain: ExplorerChainKey, address: string, kind: string, filters?: ActivityFilterParams) {
  const params = activityFilterSearchParams(filters);
  return request<ClickHouseActivityStats>(`/api/v1/explorer/${chain}/address/${address}/${kind}/stats?${params.toString()}`);
}

export function loadDailyStats(chain: ExplorerChainKey, address: string, from?: string, to?: string) {
  const params = new URLSearchParams({ limit: "366" });
  if (from) params.set("from", from);
  if (to) params.set("to", to);
  return request<Array<Record<string, unknown>>>(
    `/api/v1/explorer/${chain}/address/${address}/daily-stats?${params.toString()}`,
  );
}

export function loadFinancialSummary(chain: ExplorerChainKey, address: string, window: AnalysisWindow, from?: string, to?: string) {
  const params = new URLSearchParams({ window });
  if (from) params.set("from", from);
  if (to) params.set("to", to);
  return request<FinancialSummary>(`/api/v2/analytics/${chain}/address/${address}/financial-summary?${params.toString()}`);
}

export function loadCounterparties(chain: ExplorerChainKey, address: string, window: AnalysisWindow) {
  return request<CounterpartyFinancial[]>(
    `/api/v2/analytics/${chain}/address/${address}/financial-counterparties?window=${window}&limit=50`,
  );
}

export function loadBlock(chain: ExplorerChainKey, block: string) {
  return request<Record<string, unknown>>(`/api/v2/explorer/${chain}/block/${block}`);
}

export function loadTransaction(chain: ExplorerChainKey, hash: string) {
  return request<Record<string, unknown>>(`/api/v2/explorer/${chain}/tx/${hash}`);
}

export function loadToken(chain: ExplorerChainKey, address: string) {
  return request<Record<string, unknown>>(`/api/v2/explorer/${chain}/token/${address}`);
}

export function loadContract(chain: ExplorerChainKey, address: string) {
  return request<Record<string, unknown>>(`/api/v1/explorer/${chain}/contract/${address}`);
}

export function loadStrictFinancialAnalysis(chain: ExplorerChainKey, address: string, mode: "retention" | "pass-through" | "pnl") {
  const endpoint = mode === "retention" ? "fifo-retention" : mode === "pass-through" ? "fifo-pass-through" : "pnl";
  return request<Record<string, unknown>>(`/api/v2/analytics/${chain}/address/${address}/${endpoint}`);
}
