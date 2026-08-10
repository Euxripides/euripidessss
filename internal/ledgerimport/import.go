package ledgerimport

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/google/uuid"
)

// Config configures a ledger import run.
type Config struct {
	LedgerRoot       string
	FlowsRoot        string
	JobID            string
	CredentialFile   string
	MaxBatchRows     int
	DropStagingAfter bool
	SkipCompleted    bool
	RequestTimeout   time.Duration
	Logger           *log.Logger
}

// Result carries import and verification counters.
type Result struct {
	Stats             []SourceStats
	StagedTransfers   uint64
	UniqueTransfers   uint64
	InsertedTransfers uint64
	StagedTxs         uint64
	UniqueTxs         uint64
	InsertedTxs       uint64
	InsertedTokens    uint64
	ActivityRows      uint64
	ManifestRows      uint64
	CoverageRows      uint64
	StartedAt         time.Time
	CompletedAt       time.Time
}

var (
	safeJobIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	safeRangePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

const (
	transferStageTable = "onchain._ledger_transfer_stage"
	txStageTable       = "onchain._ledger_tx_stage"
	parserVersion      = "ledger-import-v1"
	normalizerVersion  = "pangu-ledger-v1"
)

// DiscoverSources enumerates the import inputs under the configured roots.
func DiscoverSources(cfg Config) ([]Source, error) {
	var sources []Source
	add := func(kind SourceKind, path, provider, rangeID string, priority uint8) {
		sources = append(sources, Source{Kind: kind, Path: path, Provider: provider, RangeID: rangeID, Priority: priority})
	}

	fistDir := filepath.Join(cfg.LedgerRoot, "交付_FIST全量账本_20260806")
	fistLedger := filepath.Join(fistDir, "原始全量账本_20260806.csv")
	if info, err := os.Stat(fistLedger); err == nil && !info.IsDir() {
		add(KindLedger10, fistLedger, "SQD_FINALIZED", "fist-ledger", 3)
	}
	fnxaiDir := filepath.Join(cfg.LedgerRoot, "交付_FNXAI_1FNXAI全量账本_20260807", "原始下载分片")
	if shards, err := listFiles(fnxaiDir, "fnxai_sub_*.csv"); err == nil {
		for _, p := range shards {
			add(KindLedger10, p, "SQD_FINALIZED", "fnxai-shards", 3)
		}
	}
	if shards, err := listFiles(fnxaiDir, "1fnxai_sub_*.csv"); err == nil {
		for _, p := range shards {
			add(KindLedger10, p, "SQD_FINALIZED", "1fnxai-shards", 3)
		}
	}
	msnDir := filepath.Join(cfg.LedgerRoot, "交付_MSN_CMSN全量账本_20260807", "原始下载分片")
	if shards, err := listFiles(msnDir, "msn_sub_*.csv"); err == nil {
		for _, p := range shards {
			add(KindLedger10, p, "SQD_FINALIZED", "msn-shards", 3)
		}
	}
	if shards, err := listFiles(msnDir, "cmsn_sub_*.csv"); err == nil {
		for _, p := range shards {
			add(KindLedger10, p, "SQD_FINALIZED", "cmsn-shards", 3)
		}
	}

	// Per-address token transfer exports (no logIndex). The OKLink
	// wallet_export files are handled separately below with their own parser.
	if shards, err := listFiles(cfg.FlowsRoot, "BSC_代币转账_*.csv"); err == nil {
		for _, p := range shards {
			if strings.Contains(filepath.ToSlash(p), "/wallet_export_") {
				continue
			}
			add(KindTransfer9, p, "ADDRESS_CSV_EXPORT", "address-transfer-csv", 2)
		}
	}
	if shards, err := listFiles(cfg.FlowsRoot, "BSC_交易记录_*.csv"); err == nil {
		for _, p := range shards {
			if strings.Contains(filepath.ToSlash(p), "/wallet_export_") {
				continue
			}
			add(KindTx11, p, "ADDRESS_CSV_EXPORT", "address-tx-csv", 2)
		}
	}
	if shards, err := listFiles(cfg.FlowsRoot, "*.xlsx"); err == nil {
		for _, p := range shards {
			if strings.HasPrefix(strings.ToLower(filepath.Base(p)), "下载情况") {
				continue
			}
			if filepath.Dir(p) != cfg.FlowsRoot {
				continue
			}
			add(KindTx9, p, "WALLET_XLSX_EXPORT", "address-tx-xlsx", 1)
		}
	}
	// OKLink wallet exports (richest transaction records).
	if shards, err := listFiles(cfg.FlowsRoot, "BSC_代币转账_*.csv"); err == nil {
		// wallet_export dirs only
		for _, p := range shards {
			if strings.Contains(filepath.ToSlash(p), "/wallet_export_") {
				add(KindTransferOK, p, "OKLINK_WALLET_EXPORT", "wallet-transfer-oklink", 2)
			}
		}
	}
	if shards, err := listFiles(cfg.FlowsRoot, "BSC_交易记录_*.csv"); err == nil {
		for _, p := range shards {
			if strings.Contains(filepath.ToSlash(p), "/wallet_export_") {
				add(KindTxOK, p, "OKLINK_WALLET_EXPORT", "wallet-tx-oklink", 3)
			}
		}
	}
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].Kind != sources[j].Kind {
			return sources[i].Kind < sources[j].Kind
		}
		return sources[i].Path < sources[j].Path
	})
	return sources, nil
}

// stageWriter buffers normalized rows and inserts them into a ClickHouse
// staging table in bounded batches.
type stageWriter struct {
	client *clickhouse.Client
	table  string
	cols   []string
	rows   int
	buf    bytes.Buffer
	csvw   *csv.Writer
	max    int
	total  uint64
}

func newStageWriter(client *clickhouse.Client, table string, cols []string, max int) *stageWriter {
	w := &stageWriter{client: client, table: table, cols: cols, max: max}
	w.buf.Grow(8 << 20)
	w.csvw = csv.NewWriter(&w.buf)
	return w
}

func (w *stageWriter) Write(ctx context.Context, row []string) error {
	if err := w.csvw.Write(row); err != nil {
		return err
	}
	w.rows++
	if w.rows >= w.max {
		return w.Flush(ctx)
	}
	return nil
}

func (w *stageWriter) Flush(ctx context.Context) error {
	if w.rows == 0 {
		return nil
	}
	w.csvw.Flush()
	if err := w.csvw.Error(); err != nil {
		return err
	}
	if err := w.client.InsertCSV(ctx, w.table, w.cols, bytes.NewReader(w.buf.Bytes())); err != nil {
		return err
	}
	w.total += uint64(w.rows)
	w.rows = 0
	w.buf.Reset()
	w.csvw = csv.NewWriter(&w.buf)
	return nil
}

// Run executes the full import pipeline: stage -> deduplicate -> final tables
// -> derived address activity -> provenance records -> verification.
func Run(ctx context.Context, cfg Config) (Result, error) {
	var result Result
	result.StartedAt = time.Now().UTC()
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	if !safeJobIDPattern.MatchString(cfg.JobID) {
		return result, fmt.Errorf("invalid job id")
	}
	if cfg.MaxBatchRows <= 0 {
		cfg.MaxBatchRows = 200_000
	}

	sources, err := DiscoverSources(cfg)
	if err != nil {
		return result, err
	}
	if len(sources) == 0 {
		return result, fmt.Errorf("no sources discovered under %q and %q", cfg.LedgerRoot, cfg.FlowsRoot)
	}
	client, err := newClickHouseClient(cfg)
	if err != nil {
		return result, err
	}
	if err := client.Ping(ctx); err != nil {
		return result, fmt.Errorf("clickhouse ping: %w", err)
	}

	completedPaths := map[string]bool{}
	if cfg.SkipCompleted {
		rows, qErr := client.QueryJSON(ctx, fmt.Sprintf(
			"SELECT source_path FROM onchain.migration_manifest FINAL WHERE status='COMPLETED' AND parser_version='%s' AND chain_id=56", parserVersion))
		if qErr != nil {
			return result, fmt.Errorf("query completed manifests: %w", qErr)
		}
		for _, r := range rows {
			if path, ok := r["source_path"].(string); ok {
				completedPaths[path] = true
			}
		}
	}
	// A completed manifest records one representative path per source group;
	// skip every source that shares that group's range id.
	skipRanges := map[string]bool{}
	for _, src := range sources {
		if completedPaths[src.Path] {
			skipRanges[src.RangeID] = true
		}
	}

	now := time.Now().UTC().Format("2006-01-02 15:04:05.000")
	if err := resetStaging(ctx, client); err != nil {
		return result, err
	}

	// ---- stage token transfers ----
	transferWriter := newStageWriter(client, transferStageTable, transferStageColumns(), cfg.MaxBatchRows)
	var noLogRows []TransferRow
	var transferStats []SourceStats
	for _, src := range sources {
		if skipRanges[src.RangeID] {
			logger.Printf("skip completed group %s (%s)", src.RangeID, src.Path)
			continue
		}
		switch src.Kind {
		case KindLedger10:
			stats, err := parseLedger10(src.Path, cfg.JobID, src, now, func(row TransferRow) error {
				if err := transferWriter.Write(ctx, transferCSVRow(row)); err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				return result, fmt.Errorf("parse %s: %w", src.Path, err)
			}
			transferStats = append(transferStats, stats)
			logger.Printf("staged %s: %d rows (%d rejected)", src.RangeID, stats.ParsedRows, stats.Rejected)
		case KindTransfer9, KindTransferOK:
			stats, err := parseSourceTransfers(src, cfg.JobID, now, func(row TransferRow) error {
				noLogRows = append(noLogRows, row)
				return nil
			})
			if err != nil {
				return result, fmt.Errorf("parse %s: %w", src.Path, err)
			}
			transferStats = append(transferStats, stats)
			logger.Printf("buffered %s: %d rows (%d rejected, %d covered-by-ledger skipped)", src.RangeID, stats.ParsedRows, stats.Rejected, stats.SkippedCovered)
		}
	}
	assignSyntheticLogIndices(noLogRows)
	for _, row := range noLogRows {
		if err := transferWriter.Write(ctx, transferCSVRow(row)); err != nil {
			return result, err
		}
	}
	if err := transferWriter.Flush(ctx); err != nil {
		return result, fmt.Errorf("flush transfer stage: %w", err)
	}
	result.StagedTransfers = transferWriter.total
	logger.Printf("transfer stage total: %d rows", result.StagedTransfers)

	// ---- stage transactions ----
	txWriter := newStageWriter(client, txStageTable, txStageColumns(), cfg.MaxBatchRows)
	var txStats []SourceStats
	for _, src := range sources {
		if skipRanges[src.RangeID] {
			continue
		}
		var err error
		var stats SourceStats
		switch src.Kind {
		case KindTx9:
			stats, err = parseTx9(src.Path, cfg.JobID, src, now, func(row TxRow) error {
				return txWriter.Write(ctx, txCSVRow(row))
			})
		case KindTx11:
			stats, err = parseTx11(src.Path, cfg.JobID, src, now, func(row TxRow) error {
				return txWriter.Write(ctx, txCSVRow(row))
			})
		case KindTxOK:
			stats, err = parseTxOKLink(src.Path, cfg.JobID, src, now, func(row TxRow) error {
				return txWriter.Write(ctx, txCSVRow(row))
			})
		}
		if err != nil {
			return result, fmt.Errorf("parse %s: %w", src.Path, err)
		}
		if stats.SourcePath != "" {
			txStats = append(txStats, stats)
			logger.Printf("staged %s: %d rows (%d rejected)", src.RangeID, stats.ParsedRows, stats.Rejected)
		}
	}
	if err := txWriter.Flush(ctx); err != nil {
		return result, fmt.Errorf("flush tx stage: %w", err)
	}
	result.StagedTxs = txWriter.total
	logger.Printf("tx stage total: %d rows", result.StagedTxs)

	result.Stats = append(transferStats, txStats...)

	// Merge the staging parts before deduplicated final inserts so the FINAL
	// scans stay memory-bounded (ClickHouse server memory is ~10.6 GiB).
	for _, table := range []string{transferStageTable, txStageTable} {
		if err := client.Exec(ctx, "OPTIMIZE TABLE "+table+" FINAL SETTINGS optimize_throw_if_noop=0"); err != nil {
			return result, fmt.Errorf("optimize staging %s: %w", table, err)
		}
	}
	logger.Printf("staging tables merged")

	// ---- deduplicate and insert final token transfers ----
	if result.StagedTransfers > 0 {
		logger.Printf("inserting deduplicated token_transfers...")
		if err := client.Exec(ctx, finalTransferInsertSQL(cfg.JobID)); err != nil {
			return result, fmt.Errorf("insert token_transfers: %w", err)
		}
		if err := client.Exec(ctx, "OPTIMIZE TABLE onchain.token_transfers FINAL SETTINGS optimize_throw_if_noop=0"); err != nil {
			return result, fmt.Errorf("optimize token_transfers: %w", err)
		}
		if err := insertTokenDimension(ctx, client, cfg.JobID, logger); err != nil {
			return result, err
		}
	}

	// ---- deduplicate and insert final chain transactions ----
	if result.StagedTxs > 0 {
		logger.Printf("inserting deduplicated chain_transactions...")
		if err := client.Exec(ctx, finalTxInsertSQL(cfg.JobID)); err != nil {
			return result, fmt.Errorf("insert chain_transactions: %w", err)
		}
		if err := client.Exec(ctx, "OPTIMIZE TABLE onchain.chain_transactions FINAL SETTINGS optimize_throw_if_noop=0"); err != nil {
			return result, fmt.Errorf("optimize chain_transactions: %w", err)
		}
	}

	// ---- derive address activity ----
	if result.StagedTransfers > 0 || result.StagedTxs > 0 {
		logger.Printf("deriving address_activity...")
		if err := client.Exec(ctx, activityInsertSQL(cfg.JobID)); err != nil {
			return result, fmt.Errorf("insert address_activity: %w", err)
		}
		if err := client.Exec(ctx, "OPTIMIZE TABLE onchain.address_activity FINAL SETTINGS optimize_throw_if_noop=0"); err != nil {
			return result, fmt.Errorf("optimize address_activity: %w", err)
		}
	}

	// ---- provenance: data_coverage + migration_manifest ----
	if err := client.Exec(ctx, coverageInsertSQL(cfg.JobID)); err != nil {
		return result, fmt.Errorf("insert data_coverage: %w", err)
	}
	manifestRows, err := insertManifest(ctx, client, cfg.JobID, result.Stats, result.StartedAt)
	if err != nil {
		return result, err
	}
	result.ManifestRows = manifestRows

	// ---- verify ----
	if err := verify(ctx, client, cfg, &result, logger); err != nil {
		return result, err
	}

	if cfg.DropStagingAfter {
		if err := dropStaging(ctx, client); err != nil {
			logger.Printf("warn: drop staging failed: %v", err)
		}
	}
	result.CompletedAt = time.Now().UTC()
	return result, nil
}

func parseSourceTransfers(src Source, jobID, now string, sink TransferParser) (SourceStats, error) {
	if src.Kind == KindTransferOK {
		return parseTransferOKLink(src.Path, jobID, src, now, sink)
	}
	return parseTransfer9(src.Path, jobID, src, now, sink)
}

func newClickHouseClient(cfg Config) (*clickhouse.Client, error) {
	host, port, db, user, password := loadClickHouseCreds(cfg.CredentialFile)
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Minute
	}
	clickhouseClient, err := clickhouse.New(clickhouse.Config{
		Enabled:        true,
		Required:       true,
		Host:           host,
		HTTPPort:       port,
		Database:       db,
		User:           user,
		Password:       password,
		ConnectTimeout: 10 * time.Second,
		RequestTimeout: cfg.RequestTimeout,
		MaxConnections: 8,
	})
	if err != nil {
		return nil, err
	}
	return clickhouseClient, nil
}

func loadClickHouseCreds(credentialFile string) (host string, port int, db, user, password string) {
	host, port, db, user = "127.0.0.1", 8123, "onchain", "etl_app"
	data, err := os.ReadFile(credentialFile)
	if err != nil {
		return host, port, db, user, ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch key {
		case "CLICKHOUSE_HOST":
			host = value
		case "CLICKHOUSE_HTTP_PORT":
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				port = n
			}
		case "CLICKHOUSE_DATABASE":
			db = value
		case "CLICKHOUSE_USER":
			user = value
		case "CLICKHOUSE_PASSWORD":
			password = value
		}
	}
	return host, port, db, user, password
}

func resetStaging(ctx context.Context, client *clickhouse.Client) error {
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS " + transferStageTable,
		"DROP TABLE IF EXISTS " + txStageTable,
		`CREATE TABLE IF NOT EXISTS ` + transferStageTable + ` (
			chain_id UInt32, block_number UInt64, block_time DateTime64(3,'UTC'), tx_hash String,
			log_index Int32, token_address String, token_name String, token_symbol String,
			token_decimals UInt8, token_standard LowCardinality(String), event_signature String,
			from_address String, to_address String, raw_value String, value_decimal Decimal(76,38),
			source_priority UInt8, source_provider LowCardinality(String), ingest_job_id String,
			source_range_id String, ingested_at DateTime64(3,'UTC')
		) ENGINE = ReplacingMergeTree(source_priority) ORDER BY (chain_id, tx_hash, log_index)`,
		`CREATE TABLE IF NOT EXISTS ` + txStageTable + ` (
			chain_id UInt32, block_number UInt64, block_time DateTime64(3,'UTC'), tx_hash String,
			from_address String, to_address String, value_raw String, value_decimal Decimal(76,38),
			transaction_fee_native Decimal(76,38), method_id String, method_name String,
			status LowCardinality(String), raw_status String, status_source LowCardinality(String),
			source_priority UInt8, source_provider LowCardinality(String), ingest_job_id String,
			source_range_id String, ingested_at DateTime64(3,'UTC')
		) ENGINE = ReplacingMergeTree(source_priority) ORDER BY (chain_id, tx_hash)`,
	} {
		if err := client.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func dropStaging(ctx context.Context, client *clickhouse.Client) error {
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS " + transferStageTable,
		"DROP TABLE IF EXISTS " + txStageTable,
	} {
		if err := client.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func finalTransferInsertSQL(jobID string) string {
	return fmt.Sprintf(`
INSERT INTO onchain.token_transfers
(chain_id, block_number, block_time, tx_hash, transaction_index, log_index,
 token_address, token_name, token_symbol, token_decimals, token_standard,
 event_signature, from_address, to_address, raw_value, value_decimal,
 usd_price, usd_value, token_id, batch_index, from_entity_id, to_entity_id,
 source_provider, parser_version, normalizer_version, schema_version,
 ingested_at, ingest_job_id, source_range_id, price_time, price_source, price_confidence)
SELECT chain_id, block_number, block_time, tx_hash, 0,
 log_index,
 token_address, '', token_symbol, token_decimals, 'ERC20',
 '%[3]s', from_address, to_address, raw_value, value_decimal,
 NULL, NULL, '', 0, '', '',
 source_provider, '%[4]s', '%[5]s', 1,
 now64(3), ingest_job_id, source_range_id, NULL, '', ''
FROM `+transferStageTable+` FINAL
WHERE ingest_job_id = '%[1]s'`,
		jobID, SyntheticLogOffset, TransferEventSignature, parserVersion, normalizerVersion)
}

func finalTxInsertSQL(jobID string) string {
	return fmt.Sprintf(`
INSERT INTO onchain.chain_transactions
(chain_id, block_number, block_hash, block_time, transaction_index, tx_hash,
 from_address, to_address, nonce, value_raw, value_decimal, native_symbol,
 input, method_id, method_name, tx_type, gas_limit, gas_price, max_fee_per_gas,
 max_priority_fee_per_gas, effective_gas_price, gas_used, transaction_fee_native,
 transaction_fee_usd, status, is_contract_creation, created_contract_address,
 error_message, source_provider, parser_version, normalizer_version, schema_version,
 ingested_at, ingest_job_id, source_range_id, raw_status, status_source,
 method_confidence, candidate_signatures)
SELECT chain_id, block_number, '', block_time, 0, tx_hash,
 from_address, to_address, 0, value_raw, value_decimal, 'BNB',
 '', method_id, method_name, '', 0, NULL, NULL,
 NULL, NULL, 0, transaction_fee_native,
 NULL, status, false, '',
 '', source_provider, '%[2]s', '%[3]s', 1,
 now64(3), ingest_job_id, source_range_id, raw_status, status_source,
 '', []::Array(String)
FROM `+txStageTable+` FINAL
WHERE ingest_job_id = '%[1]s'`,
		jobID, parserVersion, normalizerVersion)
}

func insertTokenDimension(ctx context.Context, client *clickhouse.Client, jobID string, logger *log.Logger) error {
	stmt := fmt.Sprintf(`
INSERT INTO onchain.tokens
(chain_id, contract_address, name, symbol, decimals, token_standard, logo_uri,
 logo_source, logo_hash, official_website, is_verified, is_spam,
 first_seen_block, first_seen_time, last_metadata_refresh_at, ingested_at)
SELECT 56, token_address, token_symbol, token_symbol, token_decimals, 'ERC20', '',
 '', '', '', 0, 0, min(block_number), min(block_time), now64(3), now64(3)
FROM `+transferStageTable+`
WHERE ingest_job_id = '%[1]s'
GROUP BY token_address, token_symbol, token_decimals
ORDER BY token_address`,
		jobID)
	if err := client.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("insert tokens: %w", err)
	}
	logger.Printf("token dimension refreshed from staged transfers")
	return nil
}

func activityInsertSQL(jobID string) string {
	base := `
SELECT chain_id, from_address, to_address, 'OUT', 'TOKEN_TRANSFER',
 block_number, block_time, tx_hash, concat('log:', toString(log_index)),
 token_address, token_symbol, value_decimal, NULL, '', '', 'UNKNOWN', '', '',
 source_provider, now64(3), ingest_job_id, source_range_id,
 NULL, '', '', NULL, NULL, '', '', '%[2]s', '%[3]s', 1
FROM onchain.token_transfers FINAL
WHERE ingest_job_id = '%[1]s' AND from_address != to_address`
	baseIn := `
SELECT chain_id, to_address, from_address, 'IN', 'TOKEN_TRANSFER',
 block_number, block_time, tx_hash, concat('log:', toString(log_index)),
 token_address, token_symbol, value_decimal, NULL, '', '', 'UNKNOWN', '', '',
 source_provider, now64(3), ingest_job_id, source_range_id,
 NULL, '', '', NULL, NULL, '', '', '%[2]s', '%[3]s', 1
FROM onchain.token_transfers FINAL
WHERE ingest_job_id = '%[1]s' AND from_address != to_address`
	baseSelf := `
SELECT chain_id, from_address, from_address, 'SELF', 'TOKEN_TRANSFER',
 block_number, block_time, tx_hash, concat('log:', toString(log_index)),
 token_address, token_symbol, value_decimal, NULL, '', '', 'UNKNOWN', '', '',
 source_provider, now64(3), ingest_job_id, source_range_id,
 NULL, '', '', NULL, NULL, '', '', '%[2]s', '%[3]s', 1
FROM onchain.token_transfers FINAL
WHERE ingest_job_id = '%[1]s' AND from_address = to_address`
	txBase := `
SELECT chain_id, from_address, to_address, 'OUT', 'NATIVE_TRANSFER',
 block_number, block_time, tx_hash, 'tx:0',
 '', '', value_decimal, NULL, method_id, method_name, status, '', '',
 source_provider, now64(3), ingest_job_id, source_range_id,
 NULL, '', '', NULL, NULL, '', '', '%[2]s', '%[3]s', 1
FROM onchain.chain_transactions FINAL
WHERE ingest_job_id = '%[1]s' AND from_address != to_address`
	txIn := `
SELECT chain_id, to_address, from_address, 'IN', 'NATIVE_TRANSFER',
 block_number, block_time, tx_hash, 'tx:0',
 '', '', value_decimal, NULL, method_id, method_name, status, '', '',
 source_provider, now64(3), ingest_job_id, source_range_id,
 NULL, '', '', NULL, NULL, '', '', '%[2]s', '%[3]s', 1
FROM onchain.chain_transactions FINAL
WHERE ingest_job_id = '%[1]s' AND from_address != to_address`
	txSelf := `
SELECT chain_id, from_address, from_address, 'SELF', 'NATIVE_TRANSFER',
 block_number, block_time, tx_hash, 'tx:0',
 '', '', value_decimal, NULL, method_id, method_name, status, '', '',
 source_provider, now64(3), ingest_job_id, source_range_id,
 NULL, '', '', NULL, NULL, '', '', '%[2]s', '%[3]s', 1
FROM onchain.chain_transactions FINAL
WHERE ingest_job_id = '%[1]s' AND from_address = to_address`
	render := func(s string) string { return fmt.Sprintf(s, jobID, parserVersion, normalizerVersion) }
	return fmt.Sprintf(`INSERT INTO onchain.address_activity
(chain_id, address, counterparty_address, direction, activity_type, block_number,
 block_time, tx_hash, event_index, token_address, token_symbol, amount, usd_value,
 method_id, method_name, status, counterparty_entity_type, counterparty_label,
 source_provider, ingested_at, ingest_job_id, source_range_id, counterparty_entity_id,
 counterparty_role, method_confidence, price_usd, price_time, price_source,
 price_confidence, parser_version, normalizer_version, schema_version)
%s UNION ALL %s UNION ALL %s UNION ALL %s UNION ALL %s UNION ALL %s`,
		render(base), render(baseIn), render(baseSelf), render(txBase), render(txIn), render(txSelf))
}

func coverageInsertSQL(jobID string) string {
	return fmt.Sprintf(`
INSERT INTO onchain.data_coverage
(chain_id, dataset, subject, from_block, to_block, from_time, to_time,
 row_count, status, source_provider, manifest_sha256, updated_at)
SELECT 56, 'TOKEN_TRANSFER', token_symbol, min(block_number), max(block_number),
 min(block_time), max(block_time), count(), 'COMPLETED', source_provider, '', now64(3)
FROM onchain.token_transfers FINAL
WHERE ingest_job_id = '%[1]s'
GROUP BY token_symbol, source_provider
UNION ALL
SELECT 56, 'TRANSACTION', 'ADDRESS_EXPORT', min(block_number), max(block_number),
 min(block_time), max(block_time), count(), 'COMPLETED', 'MIXED_ADDRESS_EXPORT', '', now64(3)
FROM onchain.chain_transactions FINAL
WHERE ingest_job_id = '%[1]s'`,
		jobID)
}

func insertManifest(ctx context.Context, client *clickhouse.Client, jobID string, stats []SourceStats, started time.Time) (uint64, error) {
	var inserted uint64
	grouped := map[string]*SourceStats{}
	var order []string
	transferRanges := map[string]bool{
		"fist-ledger": true, "fnxai-shards": true, "1fnxai-shards": true,
		"msn-shards": true, "cmsn-shards": true,
		"address-transfer-csv": true, "wallet-transfer-oklink": true,
	}
	for i := range stats {
		s := &stats[i]
		if prev, ok := grouped[s.RangeID]; ok {
			prev.RowsRead += s.RowsRead
			prev.ParsedRows += s.ParsedRows
			prev.Rejected += s.Rejected
			continue
		}
		cp := *s
		grouped[s.RangeID] = &cp
		order = append(order, s.RangeID)
	}
	for _, rangeID := range order {
		g := grouped[rangeID]
		if !safeRangePattern.MatchString(rangeID) {
			continue
		}
		unique := int64(0)
		if g.ParsedRows > 0 {
			table := txStageTable
			identity := "chain_id, tx_hash"
			if transferRanges[rangeID] {
				table = transferStageTable
				// Ledger rows are unique per (chain, tx, log_index); the
				// staging ReplacingMergeTree dedups on exactly that key.
				identity = "chain_id, tx_hash, log_index"
			}
			rows, err := client.QueryJSON(ctx, fmt.Sprintf(
				"SELECT count() AS c FROM (SELECT 1 FROM %s WHERE ingest_job_id='%s' AND source_range_id='%s' GROUP BY %s)",
				table, jobID, rangeID, identity))
			if err != nil {
				return inserted, fmt.Errorf("manifest unique count %s: %w", rangeID, err)
			}
			if len(rows) > 0 {
				unique = int64(toUint(rows[0]["c"]))
			}
		}
		dataset := "TOKEN_TRANSFER"
		if !transferRanges[rangeID] {
			dataset = "TRANSACTION"
		}
		sha := g.SHA256
		if sha == "" {
			if h, err := fileSHA256(g.SourcePath); err == nil {
				sha = h
			}
		}
		stmt := fmt.Sprintf(`INSERT INTO onchain.migration_manifest
(migration_id, source_path, source_sha256, dataset, chain_id, source_rows, parsed_rows,
 unique_rows, inserted_rows, rejected_rows, parser_version, schema_version, status,
 error_message, started_at, completed_at, updated_at)
VALUES ('%s', '%s', '%s', '%s', 56, %d, %d, %d, %d, %d, '%s', 1, 'COMPLETED', '', '%s', now64(3), now64(3))`,
			uuid.NewString(), escapeSQL(g.SourcePath), sha, dataset,
			g.RowsRead, g.ParsedRows, unique, unique, g.Rejected,
			parserVersion, started.UTC().Format("2006-01-02 15:04:05.000"))
		if err := client.Exec(ctx, stmt); err != nil {
			return inserted, fmt.Errorf("insert migration_manifest %s: %w", rangeID, err)
		}
		inserted++
	}
	return inserted, nil
}

func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func verify(ctx context.Context, client *clickhouse.Client, cfg Config, result *Result, logger *log.Logger) error {
	jobID := cfg.JobID
	if result.StagedTransfers > 0 {
		rows, err := client.QueryJSON(ctx, fmt.Sprintf(
			"SELECT count() AS c FROM onchain.token_transfers FINAL WHERE ingest_job_id='%s'", jobID))
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			result.InsertedTransfers = toUint(rows[0]["c"])
		}
		rows, err = client.QueryJSON(ctx, fmt.Sprintf(
			"SELECT count() AS c FROM (SELECT 1 FROM onchain.token_transfers FINAL WHERE ingest_job_id='%s' GROUP BY chain_id, tx_hash, token_address, from_address, to_address, raw_value)", jobID))
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			result.UniqueTransfers = toUint(rows[0]["c"])
		}
		rows, err = client.QueryJSON(ctx, fmt.Sprintf(
			"SELECT count() AS c FROM (SELECT chain_id, tx_hash, log_index, token_id, batch_index FROM onchain.token_transfers FINAL WHERE ingest_job_id='%s' GROUP BY chain_id, tx_hash, log_index, token_id, batch_index HAVING count() > 1)", jobID))
		if err != nil {
			return err
		}
		if len(rows) > 0 && toUint(rows[0]["c"]) > 0 {
			return fmt.Errorf("order-key duplicates remain in token_transfers: %d", toUint(rows[0]["c"]))
		}
		rows, err = client.QueryJSON(ctx, fmt.Sprintf(
			"SELECT count() AS c FROM onchain.address_activity FINAL WHERE ingest_job_id='%s'", jobID))
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			result.ActivityRows = toUint(rows[0]["c"])
		}
	}
	if result.StagedTxs > 0 {
		rows, err := client.QueryJSON(ctx, fmt.Sprintf(
			"SELECT count() AS c FROM onchain.chain_transactions FINAL WHERE ingest_job_id='%s'", jobID))
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			result.InsertedTxs = toUint(rows[0]["c"])
		}
		rows, err = client.QueryJSON(ctx, fmt.Sprintf(
			"SELECT count() AS c FROM (SELECT 1 FROM onchain.chain_transactions FINAL WHERE ingest_job_id='%s' GROUP BY chain_id, tx_hash)", jobID))
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			result.UniqueTxs = toUint(rows[0]["c"])
		}
	}
	rows, err := client.QueryJSON(ctx, fmt.Sprintf(
		"SELECT count() AS c FROM onchain.tokens FINAL WHERE chain_id=56 AND contract_address IN (SELECT DISTINCT token_address FROM onchain.token_transfers FINAL WHERE ingest_job_id='%s')", jobID))
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		result.InsertedTokens = toUint(rows[0]["c"])
	}
	rows, err = client.QueryJSON(ctx, fmt.Sprintf(
		"SELECT count() AS c FROM onchain.data_coverage WHERE chain_id=56 AND updated_at >= '%s'",
		result.StartedAt.UTC().Add(-2*time.Minute).Format("2006-01-02 15:04:05")))
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		result.CoverageRows = toUint(rows[0]["c"])
	}
	logger.Printf("verify: transfers inserted=%d unique=%d txs inserted=%d unique=%d activity=%d tokens=%d",
		result.InsertedTransfers, result.UniqueTransfers, result.InsertedTxs, result.UniqueTxs, result.ActivityRows, result.InsertedTokens)
	return nil
}

func toUint(v any) uint64 {
	switch n := v.(type) {
	case float64:
		return uint64(n)
	case int64:
		return uint64(n)
	case string:
		parsed, _ := strconv.ParseUint(n, 10, 64)
		return parsed
	}
	return 0
}
