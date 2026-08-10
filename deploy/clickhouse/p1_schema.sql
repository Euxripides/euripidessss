-- ClickHouse Data Plane V1.0 - P1 Explorer aggregates.
-- Apply after schema.sql. Tables use versioned replacement so a scoped refresh
-- can be retried without double-counting logical results.

CREATE TABLE IF NOT EXISTS onchain.address_counterparty_stats
(
    chain_id UInt32,
    address LowCardinality(String),
    counterparty_address LowCardinality(String),
    direction LowCardinality(String),
    activity_count UInt64,
    tx_count UInt64,
    native_amount Decimal256(38),
    usd_value Decimal(38, 18),
    first_seen_time DateTime64(3, 'UTC'),
    last_seen_time DateTime64(3, 'UTC'),
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (chain_id, cityHash64(address) % 64)
ORDER BY (chain_id, address, counterparty_address, direction);

CREATE TABLE IF NOT EXISTS onchain.address_daily_stats
(
    chain_id UInt32,
    address LowCardinality(String),
    activity_date Date,
    in_count UInt64,
    out_count UInt64,
    in_native_amount Decimal256(38),
    out_native_amount Decimal256(38),
    native_netflow Decimal256(38),
    in_usd_value Decimal(38, 18),
    out_usd_value Decimal(38, 18),
    usd_netflow Decimal(38, 18),
    unique_counterparty_count UInt64,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (chain_id, toYYYYMM(activity_date))
ORDER BY (chain_id, address, activity_date);

-- The canonical tokens/contracts/address_summary tables are declared by
-- schema.sql. They are refreshed by the Go repository after a certified writer
-- batch, rather than by an insert-triggered materialized view, so replayed
-- ReplacingMergeTree source versions cannot inflate aggregates.
