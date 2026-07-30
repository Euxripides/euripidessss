import { getJson } from '../../api/client';

export type EVMChainKey = 'bsc' | 'eth' | 'base' | 'arbitrum';

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

export async function loadAddressSummary(chainKey: EVMChainKey, address: string) {
  return load<AddressSummary>(addressURL(chainKey, address, 'summary'), '读取地址概览失败');
}

export async function loadAddressActivity(chainKey: EVMChainKey, address: string, limit = 50, offset = 0) {
  return load<PageResult<AddressActivity>>(
    `${addressURL(chainKey, address, 'activity')}&limit=${limit}&offset=${offset}`,
    '读取地址流水失败',
  );
}

export async function loadAddressTokens(chainKey: EVMChainKey, address: string) {
  return load<PageResult<AddressAsset>>(addressURL(chainKey, address, 'tokens'), '读取 Token 资产失败');
}

export async function loadAddressNFTs(chainKey: EVMChainKey, address: string) {
  return load<PageResult<AddressAsset>>(addressURL(chainKey, address, 'nfts'), '读取 NFT 资产失败');
}

export async function loadAddressCounterparties(chainKey: EVMChainKey, address: string) {
  return load<PageResult<AddressCounterparty>>(
    addressURL(chainKey, address, 'counterparties'),
    '读取交易对手失败',
  );
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
