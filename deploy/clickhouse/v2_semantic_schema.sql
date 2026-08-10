-- Canonical Data Asset Layer V2.0 - semantic registries.
-- Additive and idempotent: existing fact tables are never rebuilt.

CREATE TABLE IF NOT EXISTS onchain.method_registry
(
    method_id FixedString(10),
    canonical_signature String,
    display_name String,
    source LowCardinality(String),
    confidence LowCardinality(String),
    is_verified Bool DEFAULT false,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (method_id, canonical_signature, source);

CREATE TABLE IF NOT EXISTS onchain.token_metadata_registry
(
    chain_id UInt32,
    contract_address LowCardinality(String),
    name String,
    symbol LowCardinality(String),
    decimals UInt8,
    token_standard LowCardinality(String),
    logo_uri String,
    logo_hash String,
    logo_source LowCardinality(String),
    is_verified Bool,
    is_spam Bool,
    official_website String,
    first_seen_block UInt64,
    first_seen_time DateTime64(3, 'UTC'),
    metadata_source LowCardinality(String),
    metadata_confidence LowCardinality(String),
    metadata_updated_at DateTime64(3, 'UTC'),
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, contract_address);

CREATE TABLE IF NOT EXISTS onchain.token_metadata_history
(
    chain_id UInt32,
    contract_address LowCardinality(String),
    observation_id UUID,
    name String,
    symbol LowCardinality(String),
    decimals UInt8,
    token_standard LowCardinality(String),
    logo_uri String,
    logo_hash String,
    logo_source LowCardinality(String),
    is_verified Bool,
    is_spam Bool,
    official_website String,
    first_seen_block UInt64,
    first_seen_time DateTime64(3, 'UTC'),
    metadata_source LowCardinality(String),
    metadata_confidence LowCardinality(String),
    metadata_updated_at DateTime64(3, 'UTC'),
    observed_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(observed_at)
PARTITION BY (chain_id, toYYYYMM(observed_at))
ORDER BY (chain_id, contract_address, observed_at, observation_id);

ALTER TABLE onchain.tokens ADD COLUMN IF NOT EXISTS metadata_source LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.tokens ADD COLUMN IF NOT EXISTS metadata_confidence LowCardinality(String) DEFAULT '';

ALTER TABLE onchain.chain_transactions ADD COLUMN IF NOT EXISTS raw_status String DEFAULT '';
ALTER TABLE onchain.chain_transactions ADD COLUMN IF NOT EXISTS status_source LowCardinality(String) DEFAULT 'MISSING';
ALTER TABLE onchain.chain_transactions ADD COLUMN IF NOT EXISTS method_confidence LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.chain_transactions ADD COLUMN IF NOT EXISTS candidate_signatures Array(String) DEFAULT [];

ALTER TABLE onchain.token_transfers ADD COLUMN IF NOT EXISTS price_time Nullable(DateTime64(3, 'UTC'));
ALTER TABLE onchain.token_transfers ADD COLUMN IF NOT EXISTS price_source LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.token_transfers ADD COLUMN IF NOT EXISTS price_confidence LowCardinality(String) DEFAULT '';

ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS counterparty_entity_id Nullable(UUID);
ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS counterparty_role LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS method_confidence LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS price_usd Nullable(Decimal(38, 18));
ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS price_time Nullable(DateTime64(3, 'UTC'));
ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS price_source LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS price_confidence LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS parser_version LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS normalizer_version LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS schema_version UInt16 DEFAULT 1;

ALTER TABLE onchain.contracts ADD COLUMN IF NOT EXISTS factory_address LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.contracts ADD COLUMN IF NOT EXISTS abi_source LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.contracts ADD COLUMN IF NOT EXISTS source_provider LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.contracts ADD COLUMN IF NOT EXISTS parser_version LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.contracts ADD COLUMN IF NOT EXISTS schema_version UInt16 DEFAULT 1;
ALTER TABLE onchain.contracts ADD COLUMN IF NOT EXISTS ingest_job_id String DEFAULT '';
ALTER TABLE onchain.contracts ADD COLUMN IF NOT EXISTS source_range_id String DEFAULT '';

CREATE TABLE IF NOT EXISTS onchain.abi_registry
(
    chain_id UInt32,
    contract_address LowCardinality(String),
    abi_hash FixedString(64),
    abi_json String,
    source LowCardinality(String),
    is_verified Bool,
    observed_at DateTime64(3, 'UTC'),
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, contract_address, abi_hash, source);

CREATE TABLE IF NOT EXISTS onchain.entity_registry
(
    entity_id UUID,
    entity_name String,
    entity_type LowCardinality(String),
    website String,
    risk_level LowCardinality(String),
    source LowCardinality(String),
    confidence LowCardinality(String),
    is_verified Bool,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY entity_id;

CREATE TABLE IF NOT EXISTS onchain.address_labels
(
    chain_id UInt32,
    address LowCardinality(String),
    label_name String,
    label_type LowCardinality(String),
    entity_id Nullable(UUID),
    entity_role LowCardinality(String),
    source LowCardinality(String),
    confidence LowCardinality(String),
    evidence String,
    first_seen DateTime64(3, 'UTC'),
    last_verified DateTime64(3, 'UTC'),
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, address, label_type, label_name, source);

CREATE TABLE IF NOT EXISTS onchain.token_prices
(
    chain_id UInt32,
    token_address LowCardinality(String),
    timestamp_bucket DateTime64(3, 'UTC'),
    price_usd Decimal(38, 18),
    source LowCardinality(String),
    confidence LowCardinality(String),
    observed_at DateTime64(3, 'UTC'),
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (chain_id, toYYYYMM(timestamp_bucket))
ORDER BY (chain_id, token_address, timestamp_bucket, source);

CREATE TABLE IF NOT EXISTS onchain.parsed_events
(
    chain_id UInt32,
    block_number UInt64,
    block_time DateTime64(3, 'UTC'),
    tx_hash FixedString(66),
    log_index UInt32,
    contract_address LowCardinality(String),
    topic0 FixedString(66),
    event_name LowCardinality(String),
    event_signature String,
    decoded_fields String,
    decoder_source LowCardinality(String),
    decoder_confidence LowCardinality(String),
    parser_version LowCardinality(String),
    schema_version UInt16,
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY (chain_id, toYYYYMM(block_time))
ORDER BY (chain_id, tx_hash, log_index);

CREATE TABLE IF NOT EXISTS onchain.semantic_jobs
(
    job_id UUID,
    job_type LowCardinality(String),
    chain_id UInt32,
    dataset LowCardinality(String),
    from_block UInt64,
    to_block UInt64,
    target_version LowCardinality(String),
    status LowCardinality(String),
    processed_rows UInt64,
    failed_rows UInt64,
    error_message String,
    created_at DateTime64(3, 'UTC'),
    started_at Nullable(DateTime64(3, 'UTC')),
    completed_at Nullable(DateTime64(3, 'UTC')),
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY job_id;

ALTER TABLE onchain.parsed_events ADD COLUMN IF NOT EXISTS source_provider LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.parsed_events ADD COLUMN IF NOT EXISTS normalizer_version LowCardinality(String) DEFAULT '';
ALTER TABLE onchain.parsed_events ADD COLUMN IF NOT EXISTS ingest_job_id String DEFAULT '';
ALTER TABLE onchain.parsed_events ADD COLUMN IF NOT EXISTS source_range_id String DEFAULT '';
