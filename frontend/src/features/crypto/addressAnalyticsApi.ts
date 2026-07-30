import { getJson } from '../../api/client';

export type EVMChainKey = 'bsc' | 'eth' | 'base' | 'arbitrum';

export type FirstSeenStatus = 'loading' | 'found' | 'partial' | 'not_found' | 'temporarily_unavailable' | 'failed';

export type FirstSeen = {
  readonly status: FirstSeenStatus;
  readonly first_seen_time?: string;
  readonly first_seen_block?: number;
  readonly first_seen_source?: string;
  readonly chain_id?: number;
  readonly address_type?: string;
  readonly updated_at?: string;
};

export type AddressSummary = {
  readonly chain_key: EVMChainKey;
  readonly chain_id: number;
  readonly address: string;
  readonly address_type: string;
  readonly address_type_reason: string;
  readonly rpc_configured: boolean;
  readonly rpc_env: string;
  readonly tx_count: number;
  readonly token_count: number;
  readonly nft_count: number;
  readonly contract_count: number;
  readonly first_active_time?: string;
  readonly last_active_time?: string;
  readonly total_native_in: string;
  readonly total_native_out: string;
  readonly unique_counterparty_count: number;
  readonly native_balance_raw?: string;
  readonly native_balance?: string;
  readonly native_symbol?: string;
  readonly rpc_cached?: boolean;
  readonly rpc_checked_at?: string;
  readonly data_complete?: boolean;
  readonly data_status_message?: string;
  readonly dataset_coverage?: {
    readonly job_id: string;
    readonly chain_id: number;
    readonly transactions_status: string;
    readonly logs_status: string;
    readonly trace_status: string;
    readonly coverage_percent: number;
    readonly updated_at: string;
  };
};

export type AddressActivity = {
  readonly chain_key: string;
  readonly chain_id: number;
  readonly address: string;
  readonly counterparty?: string;
  readonly direction: string;
  readonly activity_type: string;
  readonly asset_type: string;
  readonly asset_address?: string;
  readonly symbol?: string;
  readonly amount_raw: string;
  readonly amount?: string;
  readonly tx_hash: string;
  readonly block_time: string;
  readonly method_id?: string;
  readonly trace_depth: number;
  readonly status?: string;
  readonly source: string;
};

export type AddressAsset = {
  readonly asset_address: string;
  readonly symbol?: string;
  readonly name?: string;
  readonly asset_type: string;
  readonly standard?: string;
  readonly decimals?: number;
  readonly balance_raw?: string;
  readonly balance?: string;
  readonly activity_count: number;
  readonly last_active_time: string;
  readonly source?: string;
};

export type AddressCounterparty = {
  readonly counterparty: string;
  readonly activity_count: number;
  readonly tx_count: number;
  readonly direction: 'IN' | 'OUT' | 'BOTH';
  readonly native_in_count: number;
  readonly native_out_count: number;
  readonly token_in_count: number;
  readonly token_out_count: number;
  readonly first_active_time: string;
  readonly last_active_time: string;
};

export type PageResult<T> = {
  readonly rows: readonly T[];
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
};

export type AddressQueryParams = {
  chain_key: EVMChainKey;
  address: string;
  use_first_seen?: boolean;
  start_time?: string | null;
  end_time?: string | null;
};

export async function loadFirstSeen(chainKey: EVMChainKey, address: string) {
  return load<FirstSeen>(
    `/api/crypto/addresses/${encodeURIComponent(chainKey)}/${encodeURIComponent(address)}/first-seen`,
    '读取首次出现时间失败',
  );
}

export async function loadAddressSummary(params: AddressQueryParams) {
  const qs = buildQuery(params);
  return load<AddressSummary>(`${addressURL(params.chain_key, params.address, 'summary')}${qs}`, '读取地址概览失败');
}

export async function loadAddressActivity(params: AddressQueryParams, limit = 50, offset = 0) {
  const qs = buildQuery(params);
  return load<PageResult<AddressActivity>>(
    `${addressURL(params.chain_key, params.address, 'activity')}${qs}&limit=${limit}&offset=${offset}`,
    '读取地址流水失败',
  );
}

export async function loadAddressTokens(params: AddressQueryParams) {
  const qs = buildQuery(params);
  return load<PageResult<AddressAsset>>(`${addressURL(params.chain_key, params.address, 'tokens')}${qs}`, '读取 Token 资产失败');
}

export async function loadAddressNFTs(params: AddressQueryParams) {
  const qs = buildQuery(params);
  return load<PageResult<AddressAsset>>(`${addressURL(params.chain_key, params.address, 'nfts')}${qs}`, '读取 NFT 资产失败');
}

export async function loadAddressCounterparties(params: AddressQueryParams) {
  const qs = buildQuery(params);
  return load<PageResult<AddressCounterparty>>(
    `${addressURL(params.chain_key, params.address, 'counterparties')}${qs}`,
    '读取交易对手失败',
  );
}

function buildQuery(params: AddressQueryParams) {
  const parts: string[] = [];
  if (params.use_first_seen !== undefined) parts.push(`use_first_seen=${params.use_first_seen}`);
  if (params.start_time) parts.push(`start_time=${encodeURIComponent(params.start_time)}`);
  if (params.end_time) parts.push(`end_time=${encodeURIComponent(params.end_time)}`);
  return parts.length ? `&${parts.join('&')}` : '';
}

async function load<T>(url: string, fallback: string) {
  const { response, payload } = await getJson<T>(url, fallback);
  if (!response.ok) {
    const detail = typeof payload === 'object' && payload && 'detail' in payload
      ? String((payload as { detail?: unknown }).detail ?? '')
      : '';
    throw new Error(detail || fallback);
  }
  return payload;
}

function addressURL(chainKey: EVMChainKey, address: string, section: string) {
  return `/api/address/${encodeURIComponent(address)}/${section}?chain_key=${encodeURIComponent(chainKey)}`;
}
