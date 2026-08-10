import { getJson } from "../../api/client";
import type {
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

const DEFAULT_CHAIN: ExplorerChain = "bsc";

interface ApiErrorPayload {
  detail?: string;
}

function addressPath(chain: ExplorerChain, address: string): string {
  return `/api/v1/explorer/${encodeURIComponent(chain)}/address/${encodeURIComponent(address)}`;
}

async function explorerGet<T>(url: string, fallbackMessage: string): Promise<T> {
  const result = await getJson<T | ApiErrorPayload>(url, fallbackMessage);
  if (!result.response.ok) {
    const detail = typeof result.payload === "object" && result.payload !== null && "detail" in result.payload
      ? String(result.payload.detail ?? "")
      : undefined;
    throw new Error(detail || fallbackMessage);
  }
  return result.payload as T;
}

export async function fetchClickHouseAddressSummary(
  address: string,
  chain: ExplorerChain = DEFAULT_CHAIN,
): Promise<ClickHouseAddressSummary> {
  return explorerGet<ClickHouseAddressSummary>(
    `${addressPath(chain, address)}/summary`,
    "ClickHouse 画像查询失败",
  );
}

export async function fetchClickHouseActivityPage(
  address: string,
  kind: ClickHouseActivityKind = "activity",
  query: ClickHouseActivityQuery = {},
): Promise<ClickHouseActivityPage> {
  const params = new URLSearchParams();
  if (query.pageSize !== undefined) params.set("page_size", String(query.pageSize));
  if (query.cursor) params.set("cursor", query.cursor);
  const suffix = params.size > 0 ? `?${params.toString()}` : "";
  return explorerGet<ClickHouseActivityPage>(
    `${addressPath(query.chain ?? DEFAULT_CHAIN, address)}/${kind}${suffix}`,
    "ClickHouse 活动查询失败",
  );
}

export function fetchClickHouseTransactions(address: string, query?: ClickHouseActivityQuery) {
  return fetchClickHouseActivityPage(address, "transactions", query);
}

export function fetchClickHouseTokenTransfers(address: string, query?: ClickHouseActivityQuery) {
  return fetchClickHouseActivityPage(address, "token-transfers", query);
}

export function fetchClickHouseInternalTransactions(address: string, query?: ClickHouseActivityQuery) {
  return fetchClickHouseActivityPage(address, "internal-transactions", query);
}

export function fetchClickHouseContractCreations(address: string, query?: ClickHouseActivityQuery) {
  return fetchClickHouseActivityPage(address, "contract-creations", query);
}

export async function fetchClickHouseCounterparties(
  address: string,
  limit = 100,
  chain: ExplorerChain = DEFAULT_CHAIN,
): Promise<ClickHouseCounterpartyStat[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  return explorerGet<ClickHouseCounterpartyStat[]>(
    `${addressPath(chain, address)}/counterparties?${params.toString()}`,
    "ClickHouse 交易对手统计查询失败",
  );
}

export async function fetchClickHouseDailyStats(
  address: string,
  query: ClickHouseDailyStatsQuery = {},
): Promise<ClickHouseDailyStat[]> {
  const params = new URLSearchParams();
  if (query.from) params.set("from", query.from);
  if (query.to) params.set("to", query.to);
  if (query.limit !== undefined) params.set("limit", String(query.limit));
  const suffix = params.size > 0 ? `?${params.toString()}` : "";
  return explorerGet<ClickHouseDailyStat[]>(
    `${addressPath(query.chain ?? DEFAULT_CHAIN, address)}/daily-stats${suffix}`,
    "ClickHouse 每日统计查询失败",
  );
}

export async function fetchClickHouseTokenMetadata(
  contractAddress: string,
  chain: ExplorerChain = DEFAULT_CHAIN,
): Promise<ClickHouseTokenMetadata> {
  return explorerGet<ClickHouseTokenMetadata>(
    `/api/v1/explorer/${encodeURIComponent(chain)}/token/${encodeURIComponent(contractAddress)}`,
    "ClickHouse Token 元数据查询失败",
  );
}

export async function fetchClickHouseTransactionDetail(
  transactionHash: string,
  chain: ExplorerChain = DEFAULT_CHAIN,
): Promise<ClickHouseTransactionDetail> {
  return explorerGet<ClickHouseTransactionDetail>(
    `/api/v1/explorer/${encodeURIComponent(chain)}/tx/${encodeURIComponent(transactionHash)}`,
    "ClickHouse 交易详情查询失败",
  );
}

export async function fetchClickHouseContractDetail(
  contractAddress: string,
  chain: ExplorerChain = DEFAULT_CHAIN,
): Promise<ClickHouseContractDetail> {
  return explorerGet<ClickHouseContractDetail>(
    `/api/v1/explorer/${encodeURIComponent(chain)}/contract/${encodeURIComponent(contractAddress)}`,
    "ClickHouse 合约详情查询失败",
  );
}
