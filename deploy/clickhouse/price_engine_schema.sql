-- BSC Free Historical Token Price Engine V1.0.
-- Additive, idempotent and compatible with the existing onchain.token_prices layer.

CREATE TABLE IF NOT EXISTS onchain.price_anchor_1m
(
    chain_id UInt32,
    symbol LowCardinality(String),
    quote_symbol LowCardinality(String),
    minute DateTime64(3, 'UTC'),
    open Decimal(38, 18),
    high Decimal(38, 18),
    low Decimal(38, 18),
    close Decimal(38, 18),
    volume_base Decimal(38, 18),
    volume_quote Decimal(38, 18),
    trade_count UInt64,
    source LowCardinality(String),
    source_file String,
    source_checksum FixedString(64),
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY toYYYYMM(minute)
ORDER BY (chain_id, symbol, quote_symbol, minute);

CREATE TABLE IF NOT EXISTS onchain.dex_pools
(
    chain_id UInt32,
    dex LowCardinality(String),
    version LowCardinality(String),
    factory_address LowCardinality(String),
    pool_address LowCardinality(String),
    token0_address LowCardinality(String),
    token1_address LowCardinality(String),
    token0_symbol String,
    token1_symbol String,
    token0_decimals UInt8,
    token1_decimals UInt8,
    fee_bps UInt32,
    created_block UInt64,
    created_at DateTime64(3, 'UTC'),
    is_active Bool DEFAULT true,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, pool_address);

CREATE TABLE IF NOT EXISTS onchain.dex_swaps
(
    chain_id UInt32,
    block_number UInt64,
    block_time DateTime64(3, 'UTC'),
    tx_hash LowCardinality(String),
    log_index UInt32,
    dex LowCardinality(String),
    version LowCardinality(String),
    pool_address LowCardinality(String),
    token0_address LowCardinality(String),
    token1_address LowCardinality(String),
    amount0_raw Int256,
    amount1_raw Int256,
    amount0 Decimal(76, 30),
    amount1 Decimal(76, 30),
    token0_per_token1 Nullable(Decimal(76, 30)),
    token1_per_token0 Nullable(Decimal(76, 30)),
    token0_usd Nullable(Decimal(38, 18)),
    token1_usd Nullable(Decimal(38, 18)),
    volume_usd Nullable(Decimal(38, 18)),
    liquidity UInt256 DEFAULT 0,
    sqrt_price_x96 UInt256 DEFAULT 0,
    tick Int32 DEFAULT 0,
    source LowCardinality(String),
    source_job_id String,
    inserted_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(inserted_at)
PARTITION BY toYYYYMM(block_time)
ORDER BY (chain_id, pool_address, block_time, block_number, tx_hash, log_index);

CREATE TABLE IF NOT EXISTS onchain.token_price_1m
(
    chain_id UInt32,
    token_address LowCardinality(String),
    minute DateTime64(3, 'UTC'),
    open Decimal(38, 18),
    high Decimal(38, 18),
    low Decimal(38, 18),
    close Decimal(38, 18),
    vwap Decimal(38, 18),
    volume_token Decimal(76, 30),
    volume_usd Decimal(38, 18),
    trade_count UInt64,
    pool_count UInt32,
    liquidity_usd Nullable(Decimal(38, 18)),
    price_source LowCardinality(String),
    confidence Float32,
    is_interpolated Bool DEFAULT false,
    is_last_known Bool DEFAULT false,
    price_age_seconds UInt64 DEFAULT 0,
    route String,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY toYYYYMM(minute)
ORDER BY (chain_id, token_address, minute);

CREATE TABLE IF NOT EXISTS onchain.token_price_resolution_log
(
    chain_id UInt32,
    token_address LowCardinality(String),
    timestamp DateTime64(3, 'UTC'),
    resolved_price Nullable(Decimal(38, 18)),
    route String,
    source_pool String,
    hop_count UInt8,
    confidence Float32,
    status LowCardinality(String),
    reason String,
    created_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(timestamp)
ORDER BY (chain_id, token_address, timestamp);

CREATE TABLE IF NOT EXISTS onchain.token_price_coverage
(
    chain_id UInt32,
    token_address LowCardinality(String),
    first_price_at DateTime64(3, 'UTC'),
    last_price_at DateTime64(3, 'UTC'),
    first_block UInt64,
    last_block UInt64,
    minute_count UInt64,
    trade_count UInt64,
    coverage_ratio Float64,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, token_address);

CREATE TABLE IF NOT EXISTS onchain.price_engine_checkpoints
(
    job_id UUID,
    job_type LowCardinality(String),
    source LowCardinality(String),
    subject String,
    range_start DateTime64(3, 'UTC'),
    range_end DateTime64(3, 'UTC'),
    last_completed_at Nullable(DateTime64(3, 'UTC')),
    last_completed_block UInt64,
    rows_committed UInt64,
    parts UInt32,
    status LowCardinality(String),
    manifest_path String,
    checksum String,
    error_message String,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (job_id, source, subject);

-- Additive V1.0 audit contract for existing deployments.
ALTER TABLE onchain.dex_pools ADD COLUMN IF NOT EXISTS protocol_id LowCardinality(String) DEFAULT '' AFTER version;
ALTER TABLE onchain.dex_pools ADD COLUMN IF NOT EXISTS verified Bool DEFAULT false AFTER is_active;
ALTER TABLE onchain.dex_pools ADD COLUMN IF NOT EXISTS liquidity_score Float64 DEFAULT 0 AFTER verified;

ALTER TABLE onchain.dex_swaps ADD COLUMN IF NOT EXISTS protocol_id LowCardinality(String) DEFAULT '' AFTER version;
ALTER TABLE onchain.dex_swaps ADD COLUMN IF NOT EXISTS token_in_address LowCardinality(String) DEFAULT '' AFTER token1_per_token0;
ALTER TABLE onchain.dex_swaps ADD COLUMN IF NOT EXISTS amount_in Decimal(76, 30) DEFAULT 0 AFTER token_in_address;
ALTER TABLE onchain.dex_swaps ADD COLUMN IF NOT EXISTS token_out_address LowCardinality(String) DEFAULT '' AFTER amount_in;
ALTER TABLE onchain.dex_swaps ADD COLUMN IF NOT EXISTS amount_out Decimal(76, 30) DEFAULT 0 AFTER token_out_address;
ALTER TABLE onchain.dex_swaps ADD COLUMN IF NOT EXISTS price_token0_token1 Nullable(Decimal(76, 30)) AFTER amount_out;
ALTER TABLE onchain.dex_swaps ADD COLUMN IF NOT EXISTS usd_value Nullable(Decimal(38, 18)) AFTER token1_usd;
ALTER TABLE onchain.dex_swaps ADD COLUMN IF NOT EXISTS dataset LowCardinality(String) DEFAULT 'DEX_SWAP' AFTER source_job_id;
