export type ExplorerChain = "ethereum" | "eth" | "bsc" | "base" | "arbitrum" | `${number}`;

export type ClickHouseActivityKind =
  | "activity"
  | "transactions"
  | "token-transfers"
  | "internal-transactions"
  | "contract-creations";

export interface ClickHouseAddressSummary {
  chain_id: number;
  address: string;
  address_type: string;
  first_seen_time: string;
  last_seen_time: string;
  transaction_count: number;
  incoming_transaction_count: number;
  outgoing_transaction_count: number;
  token_transfer_count: number;
  internal_transaction_count: number;
  nft_transfer_count: number;
  contract_created_count: number;
  unique_counterparty_count: number;
  native_received: string;
  native_sent: string;
  native_netflow: string;
  usd_received: string;
  usd_sent: string;
  usd_netflow: string;
  active_days: number;
  max_single_in_usd: string;
  max_single_out_usd: string;
  top_counterparty: string;
  cex_interaction_count: number;
  dex_interaction_count: number;
  bridge_interaction_count: number;
  risk_score: number;
  updated_at: string;
}

export interface ClickHouseActivity {
  chain_id: number;
  address: string;
  counterparty_address: string;
  direction: "IN" | "OUT" | "SELF";
  activity_type: string;
  block_number: number;
  block_time: string;
  transaction_hash: string;
  event_index: string;
  token_address: string;
  token_name?: string;
  token_symbol: string;
  token_logo_uri?: string;
  token_logo_source?: string;
  token_verified?: boolean;
  token_spam?: boolean;
  amount: string;
  usd_value?: string;
  price_usd?: string;
  price_time?: string;
  price_source?: string;
  price_confidence?: number;
  historical_price_usdt?: string;
  historical_value_usdt?: string;
  price_timestamp?: string;
  price_route?: string;
  price_type?: "TRADED" | "LAST_KNOWN" | "PEG" | "UNKNOWN";
  price_age_seconds?: number;
  valuation_status?: "VALUED" | "BACKFILLING" | "NO_LIQUIDITY" | "NO_POOL" | "NO_PRICE" | "LOW_CONFIDENCE";
  method_id: string;
  method_name: string;
  status: string;
  counterparty_entity_type: string;
  counterparty_label: string;
  source_provider: string;
}

export interface ClickHouseActivityPage {
  items: ClickHouseActivity[];
  next_cursor?: string;
  has_more: boolean;
}

export interface ClickHouseActivityQuery {
  chain?: ExplorerChain;
  pageSize?: number;
  cursor?: string;
}

export interface ClickHouseCounterpartyStat {
  chain_id: number;
  address: string;
  counterparty_address: string;
  direction: string;
  activity_count: number;
  transaction_count: number;
  native_amount: string;
  usd_value: string;
  first_seen_time: string;
  last_seen_time: string;
}

export interface ClickHouseDailyStat {
  chain_id: number;
  address: string;
  date: string;
  incoming_count: number;
  outgoing_count: number;
  incoming_native_amount: string;
  outgoing_native_amount: string;
  native_netflow: string;
  incoming_usd_value: string;
  outgoing_usd_value: string;
  usd_netflow: string;
  unique_counterparties: number;
}

export interface ClickHouseDailyStatsQuery {
  chain?: ExplorerChain;
  from?: string;
  to?: string;
  limit?: number;
}

export interface ClickHouseTokenMetadata {
  chain_id: number;
  contract_address: string;
  name: string;
  symbol: string;
  decimals: number;
  token_standard: string;
  logo_uri: string;
  logo_source: string;
  official_website: string;
  verified: boolean;
  spam: boolean;
  first_seen_block: number;
  first_seen_time: string;
  last_metadata_refresh_at: string;
}

export interface ClickHouseTransactionDetail {
  chain_id: number;
  block_number: number;
  block_hash: string;
  block_time: string;
  transaction_index: number;
  transaction_hash: string;
  from_address: string;
  to_address: string;
  nonce: number;
  value_raw: string;
  value_decimal: string;
  native_symbol: string;
  input: string;
  method_id: string;
  method_name: string;
  transaction_type: string;
  gas_limit: number;
  gas_used: number;
  transaction_fee_native: string;
  transaction_fee_usd?: string;
  status: string;
  is_contract_creation: boolean;
  created_contract_address: string;
  error_message: string;
  source_provider: string;
}

export interface ClickHouseContractDetail {
  chain_id: number;
  contract_address: string;
  creator_address: string;
  creation_tx_hash: string;
  creation_block: number;
  creation_time: string;
  bytecode_hash: string;
  runtime_bytecode_hash: string;
  contract_name: string;
  is_verified: boolean;
  is_proxy: boolean;
  proxy_type: string;
  implementation_address: string;
  abi_json: string;
  token_standard: string;
  first_seen: string;
  last_seen: string;
  risk_flags: string[];
}
