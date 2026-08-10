// Package ledgerimport imports Pangu BSC token ledgers and per-address flow
// exports into the onchain ClickHouse warehouse. Rows from all sources are
// staged and then deduplicated on canonical identities before the final
// ReplacingMergeTree inserts, so duplicate data is never imported twice.
package ledgerimport

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

const (
	// ChainIDBSC is the BSC mainnet chain id used across the onchain schema.
	ChainIDBSC uint32 = 56

	// SyntheticLogOffset keeps synthetic log indices far above any real log
	// index so that transfer rows whose source export lacks logIndex never
	// collide with canonical ledger rows in the table's ORDER BY key.
	SyntheticLogOffset int32 = 1_000_000
)

// TransferEventSignature is the ERC-20 Transfer event topic0 signature.
const TransferEventSignature = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// knownTokenDecimals maps the BSC token contracts relevant to this import to
// their decimal places. Unknown contracts keep token_decimals=0 and an empty
// raw_value; value_decimal still stores the delivered token amount.
var knownTokenDecimals = map[string]uint8{
	"0xc9882def23bc42d53895b8361d0b1edc7570bc6a": 6,  // FIST
	"0xd26889f63094ba5a9d32666cdf5ba381acfad6a6": 18, // FNXAI
	"0xb50b7f43d06a002106454bed698d5010382ff9c7": 18, // 1FNXAI
	"0xd8b3ef86afce18edba91fed481abe22f173597c1": 18, // MSN
	"0x6cb626c895381f8af4f580392c3d6cf8dd331a22": 18, // CMSN
	"0x55d398326f99059ff775485246999027b3197955": 18, // USDT
	"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d": 18, // USDC
	"0xe9e7cea3dedca5984780bafc599bd69add087d56": 18, // BUSD
	"0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c": 18, // WBNB
}

// ledgerTokenRanges records the verified full-ledger block coverage for the
// tokens delivered as complete ledgers. Per-address export rows for these
// tokens inside the range are duplicates of the canonical ledger and are
// skipped during import (the ledger is the authoritative full source).
var ledgerTokenRanges = map[string][2]uint64{
	"0xc9882def23bc42d53895b8361d0b1edc7570bc6a": {57_700_000, 114_376_488},  // FIST
	"0xd26889f63094ba5a9d32666cdf5ba381acfad6a6": {44_990_000, 114_486_099},  // FNXAI
	"0xb50b7f43d06a002106454bed698d5010382ff9c7": {94_990_000, 114_486_099},  // 1FNXAI
	"0xd8b3ef86afce18edba91fed481abe22f173597c1": {64_990_000, 114_521_380},  // MSN
	"0x6cb626c895381f8af4f580392c3d6cf8dd331a22": {104_990_000, 114_521_380}, // CMSN
}

// SourceKind enumerates the supported source formats.
type SourceKind string

const (
	KindLedger10   SourceKind = "ledger10"        // 10-col full ledger / SQD shard, has logIndex
	KindTransfer9  SourceKind = "transfer9"       // per-address token transfer CSV without logIndex
	KindTransferOK SourceKind = "transfer_oklink" // OKLink token_20 wallet export without logIndex
	KindTx9        SourceKind = "tx9"             // per-address xlsx transaction export
	KindTx11       SourceKind = "tx11"            // per-address BSC_交易记录 CSV
	KindTxOK       SourceKind = "tx_oklink"       // OKLink transaction wallet export
)

// Source describes one import input file plus provenance metadata.
type Source struct {
	Kind     SourceKind
	Path     string
	Provider string
	RangeID  string // short stable group id, also used for manifest tracking
	Priority uint8
}

// TransferRow is a normalized token transfer row destined for the staging
// table. LogIndex is nil when the source file has no logIndex column.
type TransferRow struct {
	ChainID        uint32
	BlockNumber    uint64
	BlockTime      string
	TxHash         string
	LogIndex       *int32
	TokenAddress   string
	TokenName      string
	TokenSymbol    string
	TokenDecimals  uint8
	TokenStandard  string
	EventSignature string
	FromAddress    string
	ToAddress      string
	RawValue       string
	ValueDecimal   string
	SourcePriority uint8
	SourceProvider string
	SourceRangeID  string
	IngestJobID    string
	IngestedAt     string
}

// TxRow is a normalized transaction row destined for the chain_transactions
// staging table.
type TxRow struct {
	ChainID        uint32
	BlockNumber    uint64
	BlockTime      string
	TxHash         string
	FromAddress    string
	ToAddress      string
	ValueRaw       string
	ValueDecimal   string
	FeeNative      string
	MethodID       string
	MethodName     string
	Status         string
	RawStatus      string
	StatusSource   string
	SourcePriority uint8
	SourceProvider string
	SourceRangeID  string
	IngestJobID    string
	IngestedAt     string
}

// SourceStats reports parse-level counters for one source group.
type SourceStats struct {
	RangeID        string
	SourcePath     string
	RowsRead       int64
	ParsedRows     int64
	Rejected       int64
	SkippedCovered int64
	SHA256         string
}

var (
	methodIDPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{8}$`)
	numberPattern   = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?$`)
)

// normalizeAddress lowercases a hex address.
func normalizeAddress(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// normalizeHash lowercases a transaction hash.
func normalizeHash(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ledgerTimeLayout is the UTC time column layout used by the delivered CSVs.
const ledgerTimeLayout = "2006/01/02 15:04:05"

// parseLedgerTime converts "2025/08/15 15:53:00" (UTC) to ClickHouse text.
func parseLedgerTime(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty time")
	}
	parsed, err := time.ParseInLocation(ledgerTimeLayout, raw, time.UTC)
	if err != nil {
		return "", fmt.Errorf("invalid ledger time %q", raw)
	}
	return parsed.Format("2006-01-02 15:04:05.000"), nil
}

// parseEpochMS converts an epoch-millis string to ClickHouse UTC text.
func parseEpochMS(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		return "", fmt.Errorf("invalid epoch ms %q", raw)
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05.000"), nil
}

// normalizeDecimal converts any accepted numeric form (including scientific
// notation) into a plain decimal string suitable for Decimal(76,38).
func normalizeDecimal(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" || raw == "\\N" || !numberPattern.MatchString(raw) {
		return "", false
	}
	f, _, err := big.ParseFloat(raw, 10, 256, big.ToNearestEven)
	if err != nil {
		return "", false
	}
	return f.Text('f', -1), true
}

// rawFromDecimal converts a token amount string to its raw integer for the
// given token decimals, rounding half-up only when the source amount carries
// more fractional digits than the token supports.
func rawFromDecimal(value string, decimals uint8) string {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok || rat.Sign() < 0 {
		return ""
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	num := new(big.Int).Mul(rat.Num(), scale)
	den := rat.Denom()
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if r.Sign() != 0 {
		// Round half-up on the remaining fraction.
		twice := new(big.Int).Lsh(r, 1)
		if twice.Cmp(den) >= 0 {
			q.Add(q, big.NewInt(1))
		}
	}
	return q.String()
}

// statusFromReceipt maps receipt-style status strings to the canonical enum.
func statusFromReceipt(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "0x1", "true", "success", "ok", "0x01":
		return "SUCCESS"
	case "0", "0x0", "false", "failed", "error", "0x00":
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// statusSource returns RECEIPT when a receipt-derived status is available.
func statusSource(status string) string {
	if status == "SUCCESS" || status == "FAILED" {
		return "RECEIPT"
	}
	return "MISSING"
}

// methodParts splits a method cell into a canonical method id or a method name.
func methodParts(raw string) (id, name string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" || strings.EqualFold(raw, "None") {
		return "", ""
	}
	if methodIDPattern.MatchString(raw) {
		return strings.ToLower(raw), ""
	}
	return "", raw
}

// csvHeader builds a header index map with BOM stripping.
func csvHeader(record []string) map[string]int {
	index := make(map[string]int, len(record))
	for i, name := range record {
		name = strings.TrimPrefix(strings.TrimSpace(name), "\ufeff")
		index[strings.TrimSpace(name)] = i
	}
	return index
}

// readSourceRows streams every record of a CSV, invoking fn for each data row.
func readSourceRows(r *csv.Reader, fn func(record []string) error) error {
	for {
		record, err := r.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(record); err != nil {
			return err
		}
	}
}

// TransferParser is a streaming sink for transfer rows.
type TransferParser func(row TransferRow) error

// parseLedger10 parses the 10-column full-ledger / SQD-shard format:
// 交易哈希,区块高度,本地时间(UTC+8),UTC时间,发送方,接收方,数量,代币符号,代币地址,logIndex.
func parseLedger10(path string, jobID string, src Source, now string, sink TransferParser) (SourceStats, error) {
	stats := SourceStats{RangeID: src.RangeID, SourcePath: path}
	f, err := os.Open(path)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	hasher := sha256.New()
	br := bufio.NewReaderSize(io.TeeReader(f, hasher), 4<<20)
	csvr := csv.NewReader(br)
	csvr.FieldsPerRecord = -1
	first := true
	var idx map[string]int
	err = readSourceRows(csvr, func(record []string) error {
		if first {
			first = false
			idx = csvHeader(record)
			if len(record) < 10 {
				return fmt.Errorf("ledger10 header requires 10 columns")
			}
			return nil
		}
		stats.RowsRead++
		if len(record) < 10 {
			stats.Rejected++
			return nil
		}
		block, err1 := strconv.ParseUint(strings.TrimSpace(record[idx["区块高度"]]), 10, 64)
		blockTime, err2 := parseLedgerTime(record[idx["UTC时间"]])
		value, ok := normalizeDecimal(record[idx["数量"]])
		if err1 != nil || err2 != nil || !ok {
			stats.Rejected++
			return nil
		}
		logIdx := int32(0)
		if n, err := strconv.ParseInt(strings.TrimSpace(record[idx["logIndex"]]), 10, 32); err == nil {
			logIdx = int32(n)
		}
		token := normalizeAddress(record[idx["代币地址"]])
		raw := ""
		decimals := knownTokenDecimals[token]
		if decimals != 0 {
			raw = rawFromDecimal(value, decimals)
		}
		stats.ParsedRows++
		return sink(TransferRow{
			ChainID:        ChainIDBSC,
			BlockNumber:    block,
			BlockTime:      blockTime,
			TxHash:         normalizeHash(record[idx["交易哈希"]]),
			LogIndex:       &logIdx,
			TokenAddress:   token,
			TokenSymbol:    strings.TrimSpace(record[idx["代币符号"]]),
			TokenDecimals:  decimals,
			TokenStandard:  "ERC20",
			EventSignature: TransferEventSignature,
			FromAddress:    normalizeAddress(record[idx["发送方"]]),
			ToAddress:      normalizeAddress(record[idx["接收方"]]),
			RawValue:       raw,
			ValueDecimal:   value,
			SourcePriority: src.Priority,
			SourceProvider: src.Provider,
			SourceRangeID:  src.RangeID,
			IngestJobID:    jobID,
			IngestedAt:     now,
		})
	})
	if err != nil {
		return stats, err
	}
	stats.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return stats, nil
}

// parseTransfer9 parses the per-address token transfer CSV format without
// logIndex: 交易哈希,区块高度,本地时间(UTC+8),UTC时间,发送方,接收方,数量,代币符号,代币地址,_extra_1.
func parseTransfer9(path string, jobID string, src Source, now string, sink TransferParser) (SourceStats, error) {
	stats := SourceStats{RangeID: src.RangeID, SourcePath: path}
	f, err := os.Open(path)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	hasher := sha256.New()
	br := bufio.NewReaderSize(io.TeeReader(f, hasher), 4<<20)
	csvr := csv.NewReader(br)
	csvr.FieldsPerRecord = -1
	first := true
	var idx map[string]int
	err = readSourceRows(csvr, func(record []string) error {
		if first {
			first = false
			idx = csvHeader(record)
			if len(record) < 9 {
				return fmt.Errorf("transfer9 header requires 9 columns")
			}
			return nil
		}
		stats.RowsRead++
		if len(record) < 9 {
			stats.Rejected++
			return nil
		}
		block, err1 := strconv.ParseUint(strings.TrimSpace(record[idx["区块高度"]]), 10, 64)
		blockTime, err2 := parseLedgerTime(record[idx["UTC时间"]])
		value, ok := normalizeDecimal(record[idx["数量"]])
		if err1 != nil || err2 != nil || !ok {
			stats.Rejected++
			return nil
		}
		token := normalizeAddress(record[idx["代币地址"]])
		if rng, ok := ledgerTokenRanges[token]; ok && block >= rng[0] && block <= rng[1] {
			stats.SkippedCovered++
			return nil
		}
		decimals := knownTokenDecimals[token]
		raw := ""
		if decimals != 0 {
			raw = rawFromDecimal(value, decimals)
		}
		stats.ParsedRows++
		return sink(TransferRow{
			ChainID:        ChainIDBSC,
			BlockNumber:    block,
			BlockTime:      blockTime,
			TxHash:         normalizeHash(record[idx["交易哈希"]]),
			TokenAddress:   token,
			TokenSymbol:    strings.TrimSpace(record[idx["代币符号"]]),
			TokenDecimals:  decimals,
			TokenStandard:  "ERC20",
			EventSignature: TransferEventSignature,
			FromAddress:    normalizeAddress(record[idx["发送方"]]),
			ToAddress:      normalizeAddress(record[idx["接收方"]]),
			RawValue:       raw,
			ValueDecimal:   value,
			SourcePriority: src.Priority,
			SourceProvider: src.Provider,
			SourceRangeID:  src.RangeID,
			IngestJobID:    jobID,
			IngestedAt:     now,
		})
	})
	if err != nil {
		return stats, err
	}
	stats.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return stats, nil
}

// parseTransferOKLink parses the OKLink token_20 wallet export (31 columns).
// The export lists every transfer once per direction; rows are deduplicated on
// canonical identity later, so both direction rows may be emitted safely.
func parseTransferOKLink(path string, jobID string, src Source, now string, sink TransferParser) (SourceStats, error) {
	stats := SourceStats{RangeID: src.RangeID, SourcePath: path}
	f, err := os.Open(path)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	hasher := sha256.New()
	br := bufio.NewReaderSize(io.TeeReader(f, hasher), 4<<20)
	csvr := csv.NewReader(br)
	csvr.FieldsPerRecord = -1
	first := true
	var idx map[string]int
	err = readSourceRows(csvr, func(record []string) error {
		if first {
			first = false
			idx = csvHeader(record)
			for _, need := range []string{"交易哈希", "区块高度", "交易时间(ms)", "From", "To", "金额", "币种", "代币合约"} {
				if _, ok := idx[need]; !ok {
					return fmt.Errorf("transfer_oklink missing column %q", need)
				}
			}
			return nil
		}
		stats.RowsRead++
		if len(record) < 29 {
			stats.Rejected++
			return nil
		}
		block, err1 := strconv.ParseUint(strings.TrimSpace(record[idx["区块高度"]]), 10, 64)
		blockTime, err2 := parseEpochMS(record[idx["交易时间(ms)"]])
		value, ok := normalizeDecimal(record[idx["金额"]])
		if err1 != nil || err2 != nil || !ok {
			stats.Rejected++
			return nil
		}
		token := normalizeAddress(record[idx["代币合约"]])
		if rng, ok := ledgerTokenRanges[token]; ok && block >= rng[0] && block <= rng[1] {
			stats.SkippedCovered++
			return nil
		}
		decimals := knownTokenDecimals[token]
		raw := ""
		if decimals != 0 {
			raw = rawFromDecimal(value, decimals)
		}
		stats.ParsedRows++
		return sink(TransferRow{
			ChainID:        ChainIDBSC,
			BlockNumber:    block,
			BlockTime:      blockTime,
			TxHash:         normalizeHash(record[idx["交易哈希"]]),
			TokenAddress:   token,
			TokenSymbol:    strings.TrimSpace(record[idx["币种"]]),
			TokenDecimals:  decimals,
			TokenStandard:  "ERC20",
			EventSignature: TransferEventSignature,
			FromAddress:    normalizeAddress(record[idx["From"]]),
			ToAddress:      normalizeAddress(record[idx["To"]]),
			RawValue:       raw,
			ValueDecimal:   value,
			SourcePriority: src.Priority,
			SourceProvider: src.Provider,
			SourceRangeID:  src.RangeID,
			IngestJobID:    jobID,
			IngestedAt:     now,
		})
	})
	if err != nil {
		return stats, err
	}
	stats.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return stats, nil
}

// TxParser is a streaming sink for transaction rows.
type TxParser func(row TxRow) error

// parseTx9 parses the per-address xlsx transaction export (9 columns).
func parseTx9(path string, jobID string, src Source, now string, sink TxParser) (SourceStats, error) {
	stats := SourceStats{RangeID: src.RangeID, SourcePath: path}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return stats, fmt.Errorf("xlsx has no sheets")
	}
	rows, err := f.Rows(sheets[0])
	if err != nil {
		return stats, err
	}
	first := true
	var idx map[string]int
	for rows.Next() {
		record, err := rows.Columns()
		if err != nil {
			return stats, err
		}
		if first {
			first = false
			idx = csvHeader(record)
			for _, need := range []string{"交易哈希", "区块高度", "UTC时间", "发送方", "接收方", "数量", "手续费", "交易状态"} {
				if _, ok := idx[need]; !ok {
					return stats, fmt.Errorf("tx9 missing column %q", need)
				}
			}
			continue
		}
		stats.RowsRead++
		if len(record) < 9 {
			stats.Rejected++
			continue
		}
		block, err1 := strconv.ParseUint(strings.TrimSpace(record[idx["区块高度"]]), 10, 64)
		blockTime, err2 := parseLedgerTime(record[idx["UTC时间"]])
		value, ok := normalizeDecimal(record[idx["数量"]])
		fee, feeOK := normalizeDecimal(record[idx["手续费"]])
		if err1 != nil || err2 != nil || !ok {
			stats.Rejected++
			continue
		}
		if !feeOK {
			fee = "0"
		}
		methodID, methodName := methodParts(record[idx["交易状态"]])
		stats.ParsedRows++
		if err := sink(TxRow{
			ChainID:        ChainIDBSC,
			BlockNumber:    block,
			BlockTime:      blockTime,
			TxHash:         normalizeHash(record[idx["交易哈希"]]),
			FromAddress:    normalizeAddress(record[idx["发送方"]]),
			ToAddress:      normalizeAddress(record[idx["接收方"]]),
			ValueRaw:       rawFromDecimal(value, 18),
			ValueDecimal:   value,
			FeeNative:      fee,
			MethodID:       methodID,
			MethodName:     methodName,
			Status:         "UNKNOWN",
			RawStatus:      "",
			StatusSource:   "MISSING",
			SourcePriority: src.Priority,
			SourceProvider: src.Provider,
			SourceRangeID:  src.RangeID,
			IngestJobID:    jobID,
			IngestedAt:     now,
		}); err != nil {
			return stats, err
		}
	}
	if err := rows.Close(); err != nil {
		return stats, err
	}
	return stats, nil
}

// parseTx11 parses the per-address BSC_交易记录 CSV (10-11 columns) where the
// status cell holds a method id/name and _extra_1 carries the receipt status.
func parseTx11(path string, jobID string, src Source, now string, sink TxParser) (SourceStats, error) {
	stats := SourceStats{RangeID: src.RangeID, SourcePath: path}
	f, err := os.Open(path)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	hasher := sha256.New()
	br := bufio.NewReaderSize(io.TeeReader(f, hasher), 4<<20)
	csvr := csv.NewReader(br)
	csvr.FieldsPerRecord = -1
	first := true
	var idx map[string]int
	err = readSourceRows(csvr, func(record []string) error {
		if first {
			first = false
			idx = csvHeader(record)
			for _, need := range []string{"交易哈希", "区块高度", "UTC时间", "发送方", "接收方", "数量", "手续费", "交易状态"} {
				if _, ok := idx[need]; !ok {
					return fmt.Errorf("tx11 missing column %q", need)
				}
			}
			return nil
		}
		stats.RowsRead++
		if len(record) < 10 {
			stats.Rejected++
			return nil
		}
		block, err1 := strconv.ParseUint(strings.TrimSpace(record[idx["区块高度"]]), 10, 64)
		blockTime, err2 := parseLedgerTime(record[idx["UTC时间"]])
		value, ok := normalizeDecimal(record[idx["数量"]])
		fee, feeOK := normalizeDecimal(record[idx["手续费"]])
		if err1 != nil || err2 != nil || !ok {
			stats.Rejected++
			return nil
		}
		if !feeOK {
			fee = "0"
		}
		methodID, methodName := methodParts(record[idx["交易状态"]])
		receiptStatus := strings.TrimSpace(record[idx["_extra_1"]])
		status := statusFromReceipt(receiptStatus)
		rawStatus := receiptStatus
		srcStatus := statusSource(status)
		stats.ParsedRows++
		return sink(TxRow{
			ChainID:        ChainIDBSC,
			BlockNumber:    block,
			BlockTime:      blockTime,
			TxHash:         normalizeHash(record[idx["交易哈希"]]),
			FromAddress:    normalizeAddress(record[idx["发送方"]]),
			ToAddress:      normalizeAddress(record[idx["接收方"]]),
			ValueRaw:       rawFromDecimal(value, 18),
			ValueDecimal:   value,
			FeeNative:      fee,
			MethodID:       methodID,
			MethodName:     methodName,
			Status:         status,
			RawStatus:      rawStatus,
			StatusSource:   srcStatus,
			SourcePriority: src.Priority,
			SourceProvider: src.Provider,
			SourceRangeID:  src.RangeID,
			IngestJobID:    jobID,
			IngestedAt:     now,
		})
	})
	if err != nil {
		return stats, err
	}
	stats.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return stats, nil
}

// parseTxOKLink parses the OKLink transaction wallet export (31 columns).
func parseTxOKLink(path string, jobID string, src Source, now string, sink TxParser) (SourceStats, error) {
	stats := SourceStats{RangeID: src.RangeID, SourcePath: path}
	f, err := os.Open(path)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	hasher := sha256.New()
	br := bufio.NewReaderSize(io.TeeReader(f, hasher), 4<<20)
	csvr := csv.NewReader(br)
	csvr.FieldsPerRecord = -1
	first := true
	var idx map[string]int
	err = readSourceRows(csvr, func(record []string) error {
		if first {
			first = false
			idx = csvHeader(record)
			for _, need := range []string{"交易哈希", "区块高度", "交易时间(ms)", "From", "To", "金额", "手续费", "状态", "方法ID"} {
				if _, ok := idx[need]; !ok {
					return fmt.Errorf("tx_oklink missing column %q", need)
				}
			}
			return nil
		}
		stats.RowsRead++
		if len(record) < 27 {
			stats.Rejected++
			return nil
		}
		block, err1 := strconv.ParseUint(strings.TrimSpace(record[idx["区块高度"]]), 10, 64)
		blockTime, err2 := parseEpochMS(record[idx["交易时间(ms)"]])
		value, ok := normalizeDecimal(record[idx["金额"]])
		fee, feeOK := normalizeDecimal(record[idx["手续费"]])
		if err1 != nil || err2 != nil || !ok {
			stats.Rejected++
			return nil
		}
		if !feeOK {
			fee = "0"
		}
		methodID := strings.ToLower(strings.TrimSpace(record[idx["方法ID"]]))
		if !methodIDPattern.MatchString(methodID) {
			methodID = ""
		}
		rawStatus := strings.TrimSpace(record[idx["状态"]])
		status := statusFromReceipt(rawStatus)
		stats.ParsedRows++
		return sink(TxRow{
			ChainID:        ChainIDBSC,
			BlockNumber:    block,
			BlockTime:      blockTime,
			TxHash:         normalizeHash(record[idx["交易哈希"]]),
			FromAddress:    normalizeAddress(record[idx["From"]]),
			ToAddress:      normalizeAddress(record[idx["To"]]),
			ValueRaw:       rawFromDecimal(value, 18),
			ValueDecimal:   value,
			FeeNative:      fee,
			MethodID:       methodID,
			MethodName:     "",
			Status:         status,
			RawStatus:      rawStatus,
			StatusSource:   statusSource(status),
			SourcePriority: src.Priority,
			SourceProvider: src.Provider,
			SourceRangeID:  src.RangeID,
			IngestJobID:    jobID,
			IngestedAt:     now,
		})
	})
	if err != nil {
		return stats, err
	}
	stats.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return stats, nil
}

// assignSyntheticLogIndices assigns synthetic log indices to rows that lack a
// real logIndex. Rows sharing the exact event identity (tx, token, from, to,
// raw value) receive the same index so they collapse in the staging
// ReplacingMergeTree, while distinct identities in the same transaction
// receive distinct indices.
func assignSyntheticLogIndices(rows []TransferRow) {
	type txTokenKey struct{ tx, token string }
	type identityKey struct{ tx, token, from, to, value string }
	order := make(map[txTokenKey][]int)
	for i, row := range rows {
		key := txTokenKey{tx: row.TxHash, token: row.TokenAddress}
		order[key] = append(order[key], i)
	}
	keys := make([]txTokenKey, 0, len(order))
	for key := range order {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].tx != keys[b].tx {
			return keys[a].tx < keys[b].tx
		}
		return keys[a].token < keys[b].token
	})
	seenIndex := map[identityKey]int32{}
	counters := map[txTokenKey]int32{}
	for _, key := range keys {
		idx := order[key]
		sort.SliceStable(idx, func(a, b int) bool {
			ra, rb := rows[idx[a]], rows[idx[b]]
			if ra.BlockNumber != rb.BlockNumber {
				return ra.BlockNumber < rb.BlockNumber
			}
			if ra.FromAddress != rb.FromAddress {
				return ra.FromAddress < rb.FromAddress
			}
			if ra.ToAddress != rb.ToAddress {
				return ra.ToAddress < rb.ToAddress
			}
			return ra.RawValue < rb.RawValue
		})
		for _, pos := range idx {
			if rows[pos].LogIndex != nil {
				continue
			}
			ident := identityKey{tx: rows[pos].TxHash, token: rows[pos].TokenAddress, from: rows[pos].FromAddress, to: rows[pos].ToAddress, value: rows[pos].ValueDecimal}
			if assigned, ok := seenIndex[ident]; ok {
				rows[pos].LogIndex = &assigned
				continue
			}
			counter := counters[key]
			logIdx := SyntheticLogOffset + counter
			counters[key] = counter + 1
			seenIndex[ident] = logIdx
			rows[pos].LogIndex = &logIdx
		}
	}
}

// transferCSVRow serializes a transfer row for the staging CSV format.
func transferCSVRow(row TransferRow) []string {
	logIndex := ""
	if row.LogIndex != nil {
		logIndex = strconv.FormatInt(int64(*row.LogIndex), 10)
	} else {
		logIndex = "0"
	}
	return []string{
		strconv.FormatUint(uint64(row.ChainID), 10),
		strconv.FormatUint(row.BlockNumber, 10),
		row.BlockTime,
		row.TxHash,
		logIndex,
		row.TokenAddress,
		row.TokenName,
		row.TokenSymbol,
		strconv.FormatUint(uint64(row.TokenDecimals), 10),
		row.TokenStandard,
		row.EventSignature,
		row.FromAddress,
		row.ToAddress,
		row.RawValue,
		row.ValueDecimal,
		strconv.FormatUint(uint64(row.SourcePriority), 10),
		row.SourceProvider,
		row.IngestJobID,
		row.SourceRangeID,
		row.IngestedAt,
	}
}

// txCSVRow serializes a transaction row for the staging CSV format.
func txCSVRow(row TxRow) []string {
	return []string{
		strconv.FormatUint(uint64(row.ChainID), 10),
		strconv.FormatUint(row.BlockNumber, 10),
		row.BlockTime,
		row.TxHash,
		row.FromAddress,
		row.ToAddress,
		row.ValueRaw,
		row.ValueDecimal,
		row.FeeNative,
		row.MethodID,
		row.MethodName,
		row.Status,
		row.RawStatus,
		row.StatusSource,
		strconv.FormatUint(uint64(row.SourcePriority), 10),
		row.SourceProvider,
		row.IngestJobID,
		row.SourceRangeID,
		row.IngestedAt,
	}
}

// stageColumns returns the staging column list used by InsertCSV.
func transferStageColumns() []string {
	return []string{
		"chain_id", "block_number", "block_time", "tx_hash", "log_index",
		"token_address", "token_name", "token_symbol", "token_decimals",
		"token_standard", "event_signature", "from_address", "to_address",
		"raw_value", "value_decimal", "source_priority", "source_provider",
		"ingest_job_id", "source_range_id", "ingested_at",
	}
}

func txStageColumns() []string {
	return []string{
		"chain_id", "block_number", "block_time", "tx_hash", "from_address",
		"to_address", "value_raw", "value_decimal", "transaction_fee_native",
		"method_id", "method_name", "status", "raw_status", "status_source",
		"source_priority", "source_provider", "ingest_job_id", "source_range_id",
		"ingested_at",
	}
}

// transferCSVBuffer is a small helper used by tests to round-trip a row.
func transferCSVBuffer(row TransferRow) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(transferCSVRow(row))
	w.Flush()
	return buf.String()
}

// fileSHA256 computes the sha256 of a file (streaming).
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// listFiles returns sorted absolute paths matching the pattern under root.
func listFiles(root string, pattern string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if ok, _ := filepath.Match(pattern, filepath.Base(path)); ok {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
