package financialflow

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DefaultMaxRows = 200_000

var (
	ErrInvalidQuery = errors.New("invalid financial flow query")
	ErrRowLimit     = errors.New("financial flow query exceeded row limit")
	addressRE       = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
)

type CSVQueryClient interface {
	QueryCSV(ctx context.Context, query string) (io.ReadCloser, error)
}

type Repository struct{ client CSVQueryClient }

func NewRepository(client CSVQueryClient) *Repository { return &Repository{client: client} }

// Load streams a deterministic, time-bounded event set. It reads at most
// MaxRows+1 records so a caller never receives a silently truncated ledger.
func (r *Repository) Load(ctx context.Context, input Query) (LoadedBatch, error) {
	query, maxRows, err := BuildQuery(input)
	if err != nil {
		return LoadedBatch{}, err
	}
	if r == nil || r.client == nil {
		return LoadedBatch{}, errors.New("financial flow ClickHouse client is unavailable")
	}
	stream, err := r.client.QueryCSV(ctx, query)
	if err != nil {
		return LoadedBatch{}, fmt.Errorf("query financial flow events: %w", err)
	}
	defer stream.Close()
	reader := csv.NewReader(stream)
	reader.FieldsPerRecord = 17
	batch := LoadedBatch{Events: make([]Event, 0, min(maxRows, 4096))}
	for {
		if err := ctx.Err(); err != nil {
			return LoadedBatch{}, err
		}
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return LoadedBatch{}, fmt.Errorf("decode financial flow CSV: %w", readErr)
		}
		batch.RowsRead++
		if batch.RowsRead > maxRows {
			batch.InputTruncated = true
			return batch, ErrRowLimit
		}
		event, err := decodeRecord(record)
		if err != nil {
			return LoadedBatch{}, fmt.Errorf("decode financial flow row %d: %w", batch.RowsRead, err)
		}
		batch.Events = append(batch.Events, event)
	}
	return batch, nil
}

func BuildQuery(input Query) (string, int, error) {
	address := strings.ToLower(strings.TrimSpace(input.Address))
	token := strings.ToLower(strings.TrimSpace(input.Token))
	if input.ChainID == 0 || !addressRE.MatchString(address) || input.From.IsZero() || input.To.IsZero() || !input.From.Before(input.To) {
		return "", 0, ErrInvalidQuery
	}
	if token != "" && token != NativeAssetID && !addressRE.MatchString(token) {
		return "", 0, ErrInvalidQuery
	}
	maxRows := input.MaxRows
	if maxRows == 0 {
		maxRows = DefaultMaxRows
	}
	if maxRows < 1 || maxRows > DefaultMaxRows {
		return "", 0, ErrInvalidQuery
	}
	tokenFilter := ""
	if token == NativeAssetID {
		tokenFilter = " AND aa.token_address=''"
	} else if token != "" {
		tokenFilter = " AND aa.token_address='" + token + "'"
	}
	fromText := input.From.UTC().Format(time.RFC3339Nano)
	toText := input.To.UTC().Format(time.RFC3339Nano)
	activityRows := fmt.Sprintf(`SELECT aa.address,if(aa.token_address='', 'native', aa.token_address) AS token,
	 aa.direction,if(aa.activity_type='GAS_FEE','GAS_FEE','TRANSFER') AS event_kind,
	 toString(aa.block_time) AS block_time_text,aa.block_number,ifNull(tx.transaction_index,0) AS transaction_index,
	 aa.tx_hash,aa.event_index,toString(aa.amount) AS amount_text,
	 ifNull(toString(aa.usd_value),'') AS usd_value,aa.price_source,ifNull(toString(aa.price_time),'') AS price_time,
	 aa.schema_version,aa.activity_type,aa.status,aa.source_provider,toUInt64OrZero(aa.event_index) AS event_ordinal
FROM onchain.address_activity AS aa FINAL
ANY LEFT JOIN (SELECT chain_id,tx_hash,transaction_index FROM onchain.chain_transactions FINAL) AS tx
	 ON tx.chain_id=aa.chain_id AND tx.tx_hash=aa.tx_hash
WHERE aa.chain_id=%d AND aa.address='%s'
	 AND aa.block_time>=parseDateTime64BestEffort('%s',3,'UTC')
	 AND aa.block_time<parseDateTime64BestEffort('%s',3,'UTC')
	 AND aa.direction IN ('IN','OUT')
	 AND aa.activity_type IN ('TOKEN_TRANSFER','ERC20_TRANSFER','ERC721_TRANSFER','ERC1155_TRANSFER','NATIVE_TRANSFER','INTERNAL_TRANSFER','TRANSFER')%s`, input.ChainID, address, fromText, toText, tokenFilter)
	gasRows := ""
	if token == "" || token == NativeAssetID {
		gasRows = fmt.Sprintf(` UNION ALL SELECT tx.from_address AS address,'native' AS token,'OUT' AS direction,
	 'GAS_FEE' AS event_kind,toString(tx.block_time) AS block_time_text,tx.block_number,tx.transaction_index,
	 tx.tx_hash,'gas' AS event_index,toString(tx.transaction_fee_native) AS amount_text,
	 ifNull(toString(tx.transaction_fee_usd),'') AS usd_value,'' AS price_source,'' AS price_time,
	 tx.schema_version,'GAS_FEE' AS activity_type,tx.status,tx.source_provider,toUInt64(4294967295) AS event_ordinal
FROM onchain.chain_transactions AS tx FINAL
WHERE tx.chain_id=%d AND tx.from_address='%s' AND tx.transaction_fee_native>0
	 AND tx.block_time>=parseDateTime64BestEffort('%s',3,'UTC')
	 AND tx.block_time<parseDateTime64BestEffort('%s',3,'UTC')`, input.ChainID, address, fromText, toText)
	}
	sql := fmt.Sprintf(`SELECT address,token,direction,event_kind,block_time_text,block_number,transaction_index,
	 tx_hash,event_index,amount_text,usd_value,price_source,price_time,schema_version,activity_type,status,source_provider
FROM (%s%s) AS ledger
ORDER BY block_time_text,block_number,transaction_index,tx_hash,event_ordinal,event_index
LIMIT %d`, activityRows, gasRows, maxRows+1)
	return sql, maxRows, nil
}

func decodeRecord(record []string) (Event, error) {
	if len(record) != 17 {
		return Event{}, ErrInvalidEvent
	}
	blockTime, err := parseClickHouseTime(record[4])
	if err != nil {
		return Event{}, err
	}
	block, err := strconv.ParseUint(record[5], 10, 64)
	if err != nil {
		return Event{}, err
	}
	transactionIndex, err := strconv.ParseUint(record[6], 10, 32)
	if err != nil {
		return Event{}, err
	}
	schemaVersion, err := strconv.ParseUint(record[13], 10, 16)
	if err != nil {
		return Event{}, err
	}
	return Event{
		Address: strings.ToLower(record[0]), Token: strings.ToLower(record[1]), Direction: Direction(record[2]),
		Kind: EventKind(record[3]), Time: blockTime, BlockNumber: block, TransactionIndex: uint32(transactionIndex), TxHash: record[7], EventIndex: record[8],
		Amount: record[9], USDValue: record[10], PriceSource: record[11], PriceTime: record[12], SchemaVersion: uint16(schemaVersion),
	}, nil
}

func parseClickHouseTime(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.999", "2006-01-02 15:04:05", time.RFC3339Nano} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, ErrInvalidEvent
}
