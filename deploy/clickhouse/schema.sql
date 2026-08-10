CREATE DATABASE IF NOT EXISTS onchain;

CREATE TABLE IF NOT EXISTS chain_blocks
(
    chain_id UInt32,
    block_number UInt64,
    block_hash String,
    parent_hash String,
    block_time DateTime64(3, 'UTC'),
    miner_address LowCardinality(String),
    gas_limit UInt64,
    gas_used UInt64,
    base_fee_per_gas Nullable(UInt256),
    tx_count UInt32,
    size_bytes UInt64,
    source_provider LowCardinality(String),
    parser_version LowCardinality(String) DEFAULT '',
    schema_version UInt16 DEFAULT 1,
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY (chain_id, toYYYYMM(block_time))
ORDER BY (chain_id, block_number);

CREATE TABLE IF NOT EXISTS chain_transactions
(
    chain_id UInt32,
    block_number UInt64,
    block_hash String,
    block_time DateTime64(3, 'UTC'),
    transaction_index UInt32,
    tx_hash String,
    from_address LowCardinality(String),
    to_address LowCardinality(String),
    nonce UInt64,
    value_raw String,
    value_decimal Decimal256(38),
    native_symbol LowCardinality(String),
    input String,
    method_id LowCardinality(String),
    method_name LowCardinality(String),
    tx_type LowCardinality(String),
    gas_limit UInt64,
    gas_price Nullable(UInt256),
    max_fee_per_gas Nullable(UInt256),
    max_priority_fee_per_gas Nullable(UInt256),
    effective_gas_price Nullable(UInt256),
    gas_used UInt64,
    transaction_fee_native Decimal256(38),
    transaction_fee_usd Nullable(Decimal(38, 18)),
    status LowCardinality(String),
    is_contract_creation Bool,
    created_contract_address LowCardinality(String),
    error_message String,
    source_provider LowCardinality(String),
    ingest_job_id String DEFAULT '',
    source_range_id String DEFAULT '',
    parser_version LowCardinality(String) DEFAULT '',
    normalizer_version LowCardinality(String) DEFAULT '',
    schema_version UInt16 DEFAULT 1,
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY (chain_id, toYYYYMM(block_time))
ORDER BY (chain_id, tx_hash);

CREATE TABLE IF NOT EXISTS token_transfers
(
    chain_id UInt32,
    block_number UInt64,
    block_time DateTime64(3, 'UTC'),
    tx_hash String,
    transaction_index UInt32,
    log_index UInt32,
    token_address LowCardinality(String),
    token_name LowCardinality(String),
    token_symbol LowCardinality(String),
    token_decimals UInt8,
    token_standard LowCardinality(String),
    event_signature LowCardinality(String),
    from_address LowCardinality(String),
    to_address LowCardinality(String),
    raw_value String,
    value_decimal Decimal256(38),
    usd_price Nullable(Decimal(38, 18)),
    usd_value Nullable(Decimal(38, 18)),
    token_id String DEFAULT '',
    batch_index UInt32 DEFAULT 0,
    from_entity_id String DEFAULT '',
    to_entity_id String DEFAULT '',
    source_provider LowCardinality(String),
    ingest_job_id String DEFAULT '',
    source_range_id String DEFAULT '',
    parser_version LowCardinality(String) DEFAULT '',
    normalizer_version LowCardinality(String) DEFAULT '',
    schema_version UInt16 DEFAULT 1,
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY (chain_id, toYYYYMM(block_time))
ORDER BY (chain_id, tx_hash, log_index, token_id, batch_index);

CREATE TABLE IF NOT EXISTS internal_transactions
(
    chain_id UInt32,
    block_number UInt64,
    block_time DateTime64(3, 'UTC'),
    tx_hash String,
    trace_address String,
    trace_index UInt32,
    call_type LowCardinality(String),
    from_address LowCardinality(String),
    to_address LowCardinality(String),
    value_raw String,
    value_decimal Decimal256(38),
    gas UInt64,
    gas_used UInt64,
    input String,
    output String,
    success Bool,
    error String,
    depth UInt16,
    parent_trace_index Nullable(UInt32),
    source_provider LowCardinality(String) DEFAULT '',
    ingest_job_id String DEFAULT '',
    source_range_id String DEFAULT '',
    parser_version LowCardinality(String) DEFAULT '',
    schema_version UInt16 DEFAULT 1,
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY (chain_id, toYYYYMM(block_time))
ORDER BY (chain_id, tx_hash, trace_address);

CREATE TABLE IF NOT EXISTS contract_creations
(
    chain_id UInt32,
    block_number UInt64,
    block_time DateTime64(3, 'UTC'),
    tx_hash String,
    creator_address LowCardinality(String),
    contract_address LowCardinality(String),
    creation_type LowCardinality(String),
    factory_address LowCardinality(String),
    init_code_hash String,
    runtime_code_hash String,
    deployer_nonce UInt64,
    token_detected Bool,
    token_standard LowCardinality(String),
    contract_name String,
    compiler_version LowCardinality(String),
    is_proxy Bool,
    proxy_type LowCardinality(String),
    implementation_address LowCardinality(String),
    source_verified Bool,
    source_provider LowCardinality(String) DEFAULT '',
    ingest_job_id String DEFAULT '',
    source_range_id String DEFAULT '',
    parser_version LowCardinality(String) DEFAULT '',
    schema_version UInt16 DEFAULT 1,
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY (chain_id, toYYYYMM(block_time))
ORDER BY (chain_id, contract_address);

CREATE TABLE IF NOT EXISTS contracts
(
    chain_id UInt32,
    contract_address LowCardinality(String),
    creator_address LowCardinality(String),
    creation_tx_hash String,
    creation_block UInt64,
    creation_time DateTime64(3, 'UTC'),
    bytecode_hash String,
    runtime_bytecode_hash String,
    contract_name String,
    is_verified Bool,
    is_proxy Bool,
    proxy_type LowCardinality(String),
    implementation_address LowCardinality(String),
    abi_json String,
    token_standard LowCardinality(String),
    first_seen DateTime64(3, 'UTC'),
    last_seen DateTime64(3, 'UTC'),
    risk_flags Array(String),
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
ORDER BY (chain_id, contract_address);

CREATE TABLE IF NOT EXISTS tokens
(
    chain_id UInt32,
    contract_address LowCardinality(String),
    name String,
    symbol LowCardinality(String),
    decimals UInt8,
    token_standard LowCardinality(String),
    logo_uri String,
    logo_source LowCardinality(String),
    logo_hash String,
    official_website String,
    is_verified Bool,
    is_spam Bool,
    first_seen_block UInt64,
    first_seen_time DateTime64(3, 'UTC'),
    last_metadata_refresh_at DateTime64(3, 'UTC'),
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
ORDER BY (chain_id, contract_address);

CREATE TABLE IF NOT EXISTS address_activity
(
    chain_id UInt32,
    address LowCardinality(String),
    counterparty_address LowCardinality(String),
    direction LowCardinality(String),
    activity_type LowCardinality(String),
    block_number UInt64,
    block_time DateTime64(3, 'UTC'),
    tx_hash String,
    event_index String,
    token_address LowCardinality(String),
    token_symbol LowCardinality(String),
    amount Decimal256(38),
    usd_value Nullable(Decimal(38, 18)),
    method_id LowCardinality(String),
    method_name LowCardinality(String),
    status LowCardinality(String),
    counterparty_entity_type LowCardinality(String),
    counterparty_label String,
    source_provider LowCardinality(String) DEFAULT '',
    ingest_job_id String DEFAULT '',
    source_range_id String DEFAULT '',
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY (chain_id, toYYYYMM(block_time))
ORDER BY (chain_id, address, block_time, tx_hash, event_index);

CREATE TABLE IF NOT EXISTS address_summary
(
    chain_id UInt32,
    address LowCardinality(String),
    address_type LowCardinality(String),
    first_seen_time DateTime64(3, 'UTC'),
    last_seen_time DateTime64(3, 'UTC'),
    tx_count UInt64,
    in_tx_count UInt64,
    out_tx_count UInt64,
    token_transfer_count UInt64,
    internal_tx_count UInt64,
    nft_transfer_count UInt64,
    contract_created_count UInt64,
    unique_counterparty_count UInt64,
    native_received Decimal256(38),
    native_sent Decimal256(38),
    native_netflow Decimal256(38),
    usd_received Decimal(38, 18),
    usd_sent Decimal(38, 18),
    usd_netflow Decimal(38, 18),
    active_days UInt32,
    max_single_in_usd Decimal(38, 18),
    max_single_out_usd Decimal(38, 18),
    top_counterparty LowCardinality(String),
    cex_interaction_count UInt64,
    dex_interaction_count UInt64,
    bridge_interaction_count UInt64,
    risk_score Float32,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, address);

CREATE TABLE IF NOT EXISTS data_coverage
(
    chain_id UInt32,
    dataset LowCardinality(String),
    subject LowCardinality(String),
    from_block UInt64,
    to_block UInt64,
    from_time Nullable(DateTime64(3, 'UTC')),
    to_time Nullable(DateTime64(3, 'UTC')),
    row_count UInt64,
    status LowCardinality(String),
    source_provider LowCardinality(String),
    manifest_sha256 String,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, dataset, subject, from_block, to_block);

CREATE TABLE IF NOT EXISTS migration_manifest
(
    migration_id UUID,
    source_path String,
    source_sha256 String,
    dataset LowCardinality(String),
    chain_id UInt32,
    source_rows UInt64,
    parsed_rows UInt64,
    unique_rows UInt64,
    inserted_rows UInt64,
    rejected_rows UInt64,
    parser_version LowCardinality(String),
    schema_version UInt16,
    status LowCardinality(String),
    error_message String,
    started_at DateTime64(3, 'UTC'),
    completed_at Nullable(DateTime64(3, 'UTC')),
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY migration_id;
