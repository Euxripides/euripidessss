// V2.0 实时资产 API（设计 §15/§16）
import { getJson, parseJsonResponse } from "../../api/client";

export type AssetState = "fresh" | "cached" | "stale" | "partial" | "failed";

export interface AssetBalance {
  token_address: string;
  symbol: string;
  decimals: number;
  raw_balance: string;
  balance: string;
  status: string; // success / failed
  error?: string;
}

export interface AddressAssets {
  chain: string;
  chain_id: number;
  address: string;
  block_number?: string;
  queried_at: string;
  source: string;
  status: AssetState;
  assets: AssetBalance[];
}

export interface AddressAssetsRequest {
  chain: string;
  chain_id: number;
  address: string;
  tokens?: string[];
  force_refresh?: boolean;
}

function apiDetail(payload: unknown, fallback: string): string {
  if (payload && typeof payload === "object" && "detail" in payload) {
    const detail = String((payload as { detail?: unknown }).detail ?? "").trim();
    if (detail) return detail;
  }
  return fallback;
}

async function postAssetJson<T>(url: string, body: unknown, fallback: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal,
  });
  const payload = await parseJsonResponse<T>(response, fallback);
  if (!response.ok) throw new Error(apiDetail(payload, fallback));
  return payload;
}

export async function fetchAddressAssets(req: AddressAssetsRequest, signal?: AbortSignal): Promise<AddressAssets | null> {
  return await postAssetJson<AddressAssets>("/api/flow/address-assets", req, "实时资产查询失败", signal) ?? null;
}

export async function fetchAddressAssetsBatch(
  chain: string,
  chainId: number,
  addresses: string[],
  tokens?: string[],
): Promise<{ total: number; assets: AddressAssets[] } | null> {
  return await postAssetJson<{ total: number; assets: AddressAssets[] }>(
    "/api/flow/address-assets/batch",
    { chain, chain_id: chainId, addresses, tokens },
    "批量资产查询失败",
  );
}

// ── 余额快照（设计 §8）──

export interface BalanceSnapshot {
  chain: string;
  chain_id: number;
  address: string;
  block_number?: string;
  captured_at: string;
  source: string;
  assets: AssetBalance[];
}

export interface SnapshotDiff {
  address: string;
  chain: string;
  symbol: string;
  current: string;
  snapshot: string;
  snapshot_at: string;
  change: number;
  change_pct: number;
}

export interface SaveSnapshotResult {
  snapshot_key: string;
  snapshot: BalanceSnapshot;
  diff: SnapshotDiff[];
}

export async function saveBalanceSnapshot(
  chain: string,
  chainId: number,
  address: string,
  tokens?: string[],
): Promise<SaveSnapshotResult | null> {
  return await postAssetJson<SaveSnapshotResult>(
    "/api/flow/balance-snapshot",
    { chain, chain_id: chainId, address, tokens },
    "保存余额快照失败",
  );
}

export async function fetchBalanceSnapshots(chain: string, address: string): Promise<BalanceSnapshot[] | null> {
  const q = new URLSearchParams({ chain, address });
  const r = await getJson<{ snapshots: BalanceSnapshot[] }>(
    `/api/flow/balance-snapshots?${q}`,
    "快照历史加载失败",
  );
  return r.payload?.snapshots ?? null;
}
