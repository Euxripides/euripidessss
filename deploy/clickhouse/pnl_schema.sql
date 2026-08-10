CREATE TABLE IF NOT EXISTS onchain.financial_position_events
(
    chain_id UInt32,
    address FixedString(42),
    token_address String,
    event_time DateTime64(3, 'UTC'),
    block_number UInt64,
    tx_hash FixedString(66),
    event_index UInt32,
    event_type LowCardinality(String),
    amount_decimal Decimal(76, 36),
    usd_value Nullable(Decimal(38, 18)),
    gas_usd Nullable(Decimal(38, 18)),
    semantic_source LowCardinality(String),
    semantic_confidence LowCardinality(String),
    algorithm_input_version LowCardinality(String),
    price_version String,
    data_snapshot_version String,
    ingest_job_id String,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (chain_id, toYYYYMM(event_time))
ORDER BY (chain_id, address, token_address, event_time, block_number, tx_hash, event_index);

CREATE TABLE IF NOT EXISTS onchain.financial_pnl_snapshots
(
    snapshot_id UUID,
    chain_id UInt32,
    address FixedString(42),
    token_address String,
    as_of DateTime64(3, 'UTC'),
    realized_pnl_usd Decimal(38, 18),
    realized_proceeds_covered_usd Decimal(38, 18),
    realized_cost_basis_usd Decimal(38, 18),
    realized_gas_usd Decimal(38, 18),
    sold_amount Decimal(76, 36),
    known_sold_amount Decimal(76, 36),
    known_cost_basis_ratio Decimal(18, 12),
    realized_pnl_status LowCardinality(String),
    realized_pnl_scope LowCardinality(String),
    financial_confidence LowCardinality(String),
    position_amount Decimal(76, 36),
    known_position_amount Decimal(76, 36),
    remaining_known_cost_usd Decimal(38, 18),
    position_market_value_usd Nullable(Decimal(38, 18)),
    known_unrealized_pnl_usd Nullable(Decimal(38, 18)),
    unrealized_coverage Decimal(18, 12),
    current_price_usd Nullable(Decimal(38, 18)),
    current_price_time Nullable(DateTime64(3, 'UTC')),
    current_price_source LowCardinality(String),
    current_price_status LowCardinality(String),
    algorithm_version LowCardinality(String),
    price_version String,
    data_snapshot_version String,
    snapshot_version LowCardinality(String),
    computed_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(computed_at)
PARTITION BY (chain_id, toYYYYMM(as_of))
ORDER BY (chain_id, address, token_address, as_of, snapshot_id);

ALTER TABLE onchain.financial_pnl_snapshots ADD COLUMN IF NOT EXISTS realized_pnl_status LowCardinality(String) DEFAULT 'UNKNOWN_COST_BASIS';
ALTER TABLE onchain.financial_pnl_snapshots ADD COLUMN IF NOT EXISTS realized_pnl_scope LowCardinality(String) DEFAULT 'KNOWN_COST_BASIS_ONLY';
ALTER TABLE onchain.financial_pnl_snapshots ADD COLUMN IF NOT EXISTS financial_confidence LowCardinality(String) DEFAULT 'MISSING';

CREATE TABLE IF NOT EXISTS onchain.token_position_lots
(
    snapshot_id UUID,
    lot_index UInt32,
    chain_id UInt32,
    address FixedString(42),
    token_address String,
    acquired_time DateTime64(3, 'UTC'),
    acquired_amount Decimal(76, 36),
    remaining_amount Decimal(76, 36),
    cost_usd Nullable(Decimal(38, 18)),
    remaining_cost_usd Nullable(Decimal(38, 18)),
    source_tx FixedString(66),
    source_type LowCardinality(String),
    cost_basis_status LowCardinality(String),
    algorithm_version LowCardinality(String),
    price_version String,
    data_snapshot_version String,
    computed_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(computed_at)
PARTITION BY (chain_id, toYYYYMM(acquired_time))
ORDER BY (snapshot_id, lot_index);
