import { getJson, postJson } from "../../api/client";

export type AddressLibraryState = "IMPORTED" | "DOWNLOADING" | "PARTIAL" | "FAILED" | "CERTIFIED" | "AVAILABLE";

export interface AddressLibraryItem {
  chain_key: "bsc" | "eth" | "base" | "arbitrum";
  chain_id: number;
  address: string;
  label?: string;
  source: string;
  source_name?: string;
  import_count: number;
  first_imported_at: string;
  last_imported_at: string;
  state: AddressLibraryState;
  activity_rows: number;
  download: {
    jobs: number;
    completed_jobs: number;
    partial_jobs: number;
    failed_jobs: number;
    running_jobs: number;
    certified_datasets: number;
    indexed_rows: number;
    db_write_failed: number;
  };
}

export interface AddressLibraryResponse {
  items: AddressLibraryItem[];
  total: number;
  limit: number;
  offset: number;
}

function apiError(payload: unknown, fallback: string): Error {
  const detail = payload && typeof payload === "object" && "detail" in payload ? String((payload as { detail?: unknown }).detail ?? "") : "";
  return new Error(detail || fallback);
}

export async function listAddressLibrary(
  chainKey?: string,
  query = "",
  limit = 50,
  includeStatus = true,
  offset = 0,
): Promise<AddressLibraryResponse> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset), include_status: String(includeStatus) });
  if (chainKey) params.set("chain_key", chainKey);
  if (query.trim()) params.set("q", query.trim());
  const { response, payload } = await getJson<AddressLibraryResponse | { detail?: string }>(
    `/api/address-library?${params.toString()}`,
    "读取地址资产库失败",
  );
  if (!response.ok) throw apiError(payload, "读取地址资产库失败");
  return payload as AddressLibraryResponse;
}

export async function saveAddressLibrary(chainKey: string, addresses: string[], source = "manual", sourceName = "") {
  const { response, payload } = await postJson<{ persisted: number } | { detail?: string }>(
    "/api/address-library/import",
    { chain_key: chainKey, addresses, source, source_name: sourceName },
    "保存地址资产失败",
  );
  if (!response.ok) throw apiError(payload, "保存地址资产失败");
  return payload as { persisted: number };
}
