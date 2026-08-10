package financialpnl

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrUnconfirmedTrade = errors.New("unconfirmed trade semantic")
	txHashPattern       = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
)

type CanonicalSwap struct {
	ChainID             uint32
	Trader              string
	TokenIn             string
	AmountIn            string
	USDIn               string
	TokenOut            string
	AmountOut           string
	USDOut              string
	GasUSD              *string
	Time                time.Time
	BlockNumber         uint64
	TransactionHash     string
	EventIndex          uint32
	SemanticConfidence  string
	PriceVersion        string
	DataSnapshotVersion string
	IngestJobID         string
}

type KnownTrade struct {
	Query
	Side                EventType
	Amount              string
	USDValue            string
	GasUSD              *string
	Time                time.Time
	BlockNumber         uint64
	TransactionHash     string
	EventIndex          uint32
	SemanticSource      string
	SemanticConfidence  string
	PriceVersion        string
	DataSnapshotVersion string
	IngestJobID         string
}

type Producer struct{ client Client }

func NewProducer(client Client) *Producer { return &Producer{client: client} }

// MaterializeSwaps converts each confirmed canonical swap into exactly one sell
// event for token_in and one buy event for token_out. The gas is attached only
// to the sell leg so a swap transaction cannot deduct it twice.
func (p *Producer) MaterializeSwaps(ctx context.Context, swaps []CanonicalSwap) error {
	events := make([]producerEvent, 0, len(swaps)*2)
	for _, swap := range swaps {
		if err := validateSwap(swap); err != nil {
			return err
		}
		base := swap.EventIndex * 2
		events = append(events,
			producerEvent{chainID: swap.ChainID, address: swap.Trader, token: swap.TokenIn, event: PositionEvent{Time: swap.Time, BlockNumber: swap.BlockNumber, TransactionHash: swap.TransactionHash, EventIndex: base, Type: EventDEXSell, Amount: swap.AmountIn, USDValue: &swap.USDOut, GasUSD: swap.GasUSD, SemanticSource: "DEX_SWAP", SemanticConfidence: swap.SemanticConfidence, PriceVersion: swap.PriceVersion, DataSnapshotVersion: swap.DataSnapshotVersion}, inputVersion: "canonical_swap_v1", ingestJobID: swap.IngestJobID},
			producerEvent{chainID: swap.ChainID, address: swap.Trader, token: swap.TokenOut, event: PositionEvent{Time: swap.Time, BlockNumber: swap.BlockNumber, TransactionHash: swap.TransactionHash, EventIndex: base + 1, Type: EventDEXBuy, Amount: swap.AmountOut, USDValue: &swap.USDIn, SemanticSource: "DEX_SWAP", SemanticConfidence: swap.SemanticConfidence, PriceVersion: swap.PriceVersion, DataSnapshotVersion: swap.DataSnapshotVersion}, inputVersion: "canonical_swap_v1", ingestJobID: swap.IngestJobID})
	}
	return p.insert(ctx, events)
}

func (p *Producer) MaterializeKnownTrades(ctx context.Context, trades []KnownTrade) error {
	events := make([]producerEvent, 0, len(trades))
	for _, trade := range trades {
		if trade.Side != EventKnownBuy && trade.Side != EventKnownSell {
			return ErrUnconfirmedTrade
		}
		if trade.SemanticSource != "KNOWN_TRADE" && trade.SemanticSource != "VERIFIED_TRADE" {
			return ErrUnconfirmedTrade
		}
		if !confirmed(trade.SemanticConfidence) {
			return ErrUnconfirmedTrade
		}
		if err := validateIdentity(trade.ChainID, trade.Address, trade.Token, trade.Time, trade.TransactionHash, trade.Amount, trade.USDValue, trade.PriceVersion, trade.DataSnapshotVersion); err != nil {
			return err
		}
		usd := trade.USDValue
		events = append(events, producerEvent{chainID: trade.ChainID, address: trade.Address, token: trade.Token, event: PositionEvent{Time: trade.Time, BlockNumber: trade.BlockNumber, TransactionHash: trade.TransactionHash, EventIndex: trade.EventIndex, Type: trade.Side, Amount: trade.Amount, USDValue: &usd, GasUSD: trade.GasUSD, SemanticSource: trade.SemanticSource, SemanticConfidence: trade.SemanticConfidence, PriceVersion: trade.PriceVersion, DataSnapshotVersion: trade.DataSnapshotVersion}, inputVersion: "known_trade_v1", ingestJobID: trade.IngestJobID})
	}
	return p.insert(ctx, events)
}

// MaterializePositionEvents accepts non-trade movements for position coverage.
// It deliberately rejects BUY/SELL inputs; only the two confirmed-trade methods
// above are authorized to create PnL-bearing events.
func (p *Producer) MaterializePositionEvents(ctx context.Context, q Query, events []PositionEvent, ingestJobID string) error {
	rows := make([]producerEvent, 0, len(events))
	for _, event := range events {
		switch event.Type {
		case EventTransferIn, EventTransferOut, EventAirdrop, EventMint, EventBurn, EventBridgeIn, EventBridgeOut, EventUnknown:
		default:
			return ErrUnconfirmedTrade
		}
		if !confirmed(event.SemanticConfidence) || event.SemanticSource == "" || event.DataSnapshotVersion == "" {
			return ErrUnconfirmedTrade
		}
		if (event.Type == EventTransferIn || event.Type == EventTransferOut) && event.SemanticSource != "TOKEN_TRANSFER" {
			return ErrUnconfirmedTrade
		}
		if event.PriceVersion == "" {
			event.PriceVersion = "not_applicable"
		}
		if err := validateIdentity(q.ChainID, q.Address, q.Token, event.Time, event.TransactionHash, event.Amount, "0", event.PriceVersion, event.DataSnapshotVersion); err != nil {
			return err
		}
		if _, err := optionalNonNegative(event.GasUSD); err != nil {
			return ErrInvalidEvent
		}
		rows = append(rows, producerEvent{chainID: q.ChainID, address: q.Address, token: q.Token, event: event, inputVersion: "canonical_position_event_v1", ingestJobID: ingestJobID})
	}
	return p.insert(ctx, rows)
}

type producerEvent struct {
	chainID                   uint32
	address, token            string
	event                     PositionEvent
	inputVersion, ingestJobID string
}

func (p *Producer) insert(ctx context.Context, events []producerEvent) error {
	if len(events) == 0 {
		return nil
	}
	var buffer bytes.Buffer
	w := csv.NewWriter(&buffer)
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000")
	for _, item := range events {
		row := []string{strconv.FormatUint(uint64(item.chainID), 10), strings.ToLower(item.address), strings.ToLower(item.token), item.event.Time.UTC().Format("2006-01-02 15:04:05.000"), strconv.FormatUint(item.event.BlockNumber, 10), strings.ToLower(item.event.TransactionHash), strconv.FormatUint(uint64(item.event.EventIndex), 10), string(item.event.Type), item.event.Amount, nullableString(item.event.USDValue), nullableString(item.event.GasUSD), item.event.SemanticSource, item.event.SemanticConfidence, item.inputVersion, item.event.PriceVersion, item.event.DataSnapshotVersion, item.ingestJobID, now}
		if err := w.Write(row); err != nil {
			return ErrQueryFailed
		}
	}
	w.Flush()
	if w.Error() != nil {
		return ErrQueryFailed
	}
	columns := []string{"chain_id", "address", "token_address", "event_time", "block_number", "tx_hash", "event_index", "event_type", "amount_decimal", "usd_value", "gas_usd", "semantic_source", "semantic_confidence", "algorithm_input_version", "price_version", "data_snapshot_version", "ingest_job_id", "updated_at"}
	if err := p.client.InsertCSV(ctx, "onchain.financial_position_events", columns, &buffer); err != nil {
		return ErrQueryFailed
	}
	return nil
}

func validateSwap(swap CanonicalSwap) error {
	if swap.TokenIn == swap.TokenOut || swap.EventIndex > math.MaxUint32/2 || !confirmed(swap.SemanticConfidence) {
		return ErrUnconfirmedTrade
	}
	if err := validateIdentity(swap.ChainID, swap.Trader, swap.TokenIn, swap.Time, swap.TransactionHash, swap.AmountIn, swap.USDOut, swap.PriceVersion, swap.DataSnapshotVersion); err != nil {
		return err
	}
	if err := validateIdentity(swap.ChainID, swap.Trader, swap.TokenOut, swap.Time, swap.TransactionHash, swap.AmountOut, swap.USDIn, swap.PriceVersion, swap.DataSnapshotVersion); err != nil {
		return err
	}
	if _, err := optionalNonNegative(swap.GasUSD); err != nil {
		return ErrInvalidEvent
	}
	return nil
}

func validateIdentity(chainID uint32, address, token string, eventTime time.Time, txHash, amount, usd, priceVersion, dataVersion string) error {
	if err := ValidateQuery(Query{ChainID: chainID, Address: strings.ToLower(address), Token: strings.ToLower(token), AsOf: eventTime}); err != nil {
		return err
	}
	if !txHashPattern.MatchString(strings.ToLower(txHash)) || priceVersion == "" || dataVersion == "" {
		return ErrInvalidEvent
	}
	if _, err := positiveRat(amount); err != nil {
		return ErrInvalidEvent
	}
	if _, err := nonNegative(usd); err != nil {
		return ErrInvalidEvent
	}
	return nil
}

func confirmed(value string) bool { return value == "HIGH" || value == "VERIFIED" }
