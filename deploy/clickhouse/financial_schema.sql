-- Historical Price & Financial Analytics V1.0 - P0 price data plane.
-- This migration is intentionally additive and idempotent.

ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS price_time DateTime64(3, 'UTC') DEFAULT timestamp_bucket;
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS time_bucket DateTime64(3, 'UTC') DEFAULT timestamp_bucket;
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS resolution LowCardinality(String) DEFAULT '1h';
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS source_priority UInt8 DEFAULT 100;
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS liquidity_usd Nullable(Decimal(38, 18));
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS volume_usd Nullable(Decimal(38, 18));
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS is_fallback Bool DEFAULT false;
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS is_verified Bool DEFAULT false;
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS price_version LowCardinality(String) DEFAULT 'v1';
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS source_version LowCardinality(String) DEFAULT 'unknown';
ALTER TABLE onchain.token_prices ADD COLUMN IF NOT EXISTS ingested_at DateTime64(3, 'UTC') DEFAULT now64(3);

CREATE TABLE IF NOT EXISTS onchain.price_gaps
(
    gap_id UUID,
    chain_id UInt32,
    token_address LowCardinality(String),
    resolution LowCardinality(String),
    gap_start DateTime64(3, 'UTC'),
    gap_end DateTime64(3, 'UTC'),
    missing_buckets UInt64,
    status LowCardinality(String),
    repair_job_id Nullable(UUID),
    detected_at DateTime64(3, 'UTC'),
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (chain_id, toYYYYMM(gap_start))
ORDER BY (chain_id, token_address, resolution, gap_start, gap_id);

CREATE TABLE IF NOT EXISTS onchain.price_backfill_jobs
(
    job_id UUID,
    job_type LowCardinality(String),
    chain_id UInt32,
    token_address LowCardinality(String),
    range_start DateTime64(3, 'UTC'),
    range_end DateTime64(3, 'UTC'),
    resolution LowCardinality(String),
    source_priority Array(String),
    status LowCardinality(String),
    fetched_rows UInt64,
    written_rows UInt64,
    error_message String,
    created_at DateTime64(3, 'UTC'),
    started_at Nullable(DateTime64(3, 'UTC')),
    finished_at Nullable(DateTime64(3, 'UTC')),
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (chain_id, toYYYYMM(range_start))
ORDER BY (chain_id, token_address, created_at, job_id);
