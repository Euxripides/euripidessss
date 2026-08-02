// V2.0 实时资产 API（设计 §15/§16）
import { getJson, postJson } from "../../api/client";

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

export async function fetchAddressAssets(req: AddressAssetsRequest): Promise<AddressAssets | null> {
  const r = await postJson<AddressAssets>("/api/flow/address-assets", req, "实时资产查询失败");
  return r.payload ?? null;
}

export async function fetchAddressAssetsBatch(
  chain: string,
  chainId: number,
  addresses: string[],
  tokens?: string[],
): Promise<{ total: number; assets: AddressAssets[] } | null> {
  const r = await postJson<{ total: number; assets: AddressAssets[] }>(
    "/api/flow/address-assets/batch",
    { chain, chain_id: chainId, addresses, tokens },
    "批量资产查询失败",
  );
  return r.payload ?? null;
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
  const r = await postJson<SaveSnapshotResult>(
    "/api/flow/balance-snapshot",
    { chain, chain_id: chainId, address, tokens },
    "保存余额快照失败",
  );
  return r.payload ?? null;
}

export async function fetchBalanceSnapshots(chain: string, address: string): Promise<BalanceSnapshot[] | null> {
  const q = new URLSearchParams({ chain, address });
  const r = await getJson<{ snapshots: BalanceSnapshot[] }>(
    `/api/flow/balance-snapshots?${q}`,
    "快照历史加载失败",
  );
  return r.payload?.snapshots ?? null;
}
