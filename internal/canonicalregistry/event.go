package canonicalregistry

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (r *Repository) UpsertParsedEvent(ctx context.Context, event ParsedEvent) error {
	address, err := normalizeAddress(event.ChainID, event.ContractAddress)
	if err != nil {
		return err
	}
	event.ContractAddress = address
	event.TransactionHash = strings.ToLower(strings.TrimSpace(event.TransactionHash))
	if !txHashRE.MatchString(event.TransactionHash) {
		return fmt.Errorf("%w: invalid tx_hash", ErrInvalidInput)
	}
	event.Topic0 = strings.ToLower(strings.TrimSpace(event.Topic0))
	if !topicRE.MatchString(event.Topic0) {
		return fmt.Errorf("%w: invalid topic0", ErrInvalidInput)
	}
	if event.EventName, err = requiredText("event_name", event.EventName, 256); err != nil {
		return err
	}
	if event.EventSignature, err = requiredText("event_signature", event.EventSignature, 2048); err != nil {
		return err
	}
	if event.DecodedFields, err = requireJSON("decoded_fields", event.DecodedFields); err != nil {
		return err
	}
	if event.DecoderSource, err = requiredText("decoder_source", event.DecoderSource, 128); err != nil {
		return err
	}
	if event.DecoderConfidence, err = normalizeConfidence(event.DecoderConfidence); err != nil {
		return err
	}
	if event.ParserVersion, err = requiredText("parser_version", event.ParserVersion, 64); err != nil || !versionRE.MatchString(event.ParserVersion) {
		return fmt.Errorf("%w: invalid parser_version", ErrInvalidInput)
	}
	if event.SchemaVersion == 0 {
		return fmt.Errorf("%w: schema_version must be positive", ErrInvalidInput)
	}
	if event.BlockTime, err = requireTime("block_time", event.BlockTime); err != nil {
		return err
	}
	if event.IngestedAt.IsZero() {
		event.IngestedAt = time.Now().UTC()
	} else if event.IngestedAt, err = requireTime("ingested_at", event.IngestedAt); err != nil {
		return err
	}
	return r.insert(ctx, "onchain.parsed_events", []string{"chain_id", "block_number", "block_time", "tx_hash", "log_index", "contract_address", "topic0", "event_name", "event_signature", "decoded_fields", "decoder_source", "decoder_confidence", "parser_version", "schema_version", "ingested_at"},
		[]string{strconv.FormatUint(uint64(event.ChainID), 10), strconv.FormatUint(event.BlockNumber, 10), formatTime(event.BlockTime), event.TransactionHash, strconv.FormatUint(uint64(event.LogIndex), 10), event.ContractAddress, event.Topic0, event.EventName, event.EventSignature, event.DecodedFields, event.DecoderSource, event.DecoderConfidence, event.ParserVersion, strconv.FormatUint(uint64(event.SchemaVersion), 10), formatTime(event.IngestedAt)})
}

func (r *Repository) ListParsedEvents(ctx context.Context, chainID uint32, txHash string) ([]ParsedEvent, error) {
	if chainID == 0 {
		return nil, fmt.Errorf("%w: chain_id must be positive", ErrInvalidInput)
	}
	txHash = strings.ToLower(strings.TrimSpace(txHash))
	if !txHashRE.MatchString(txHash) {
		return nil, fmt.Errorf("%w: invalid tx_hash", ErrInvalidInput)
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT chain_id,block_number,block_time,tx_hash,log_index,contract_address,topic0,event_name,event_signature,decoded_fields,decoder_source,decoder_confidence,parser_version,schema_version,ingested_at FROM onchain.parsed_events FINAL WHERE chain_id = %d AND tx_hash = '%s' ORDER BY log_index ASC`, chainID, txHash))
	if err != nil {
		return nil, err
	}
	result := make([]ParsedEvent, 0, len(rows))
	for _, row := range rows {
		item, decodeErr := decodeEvent(row)
		if decodeErr != nil || item.ChainID != chainID || strings.ToLower(item.TransactionHash) != txHash {
			return nil, fmt.Errorf("%w: malformed event row", ErrQueryFailed)
		}
		result = append(result, item)
	}
	return result, nil
}

func decodeEvent(row map[string]any) (ParsedEvent, error) {
	var out ParsedEvent
	chain, err := uintValue(row["chain_id"], 32)
	if err != nil {
		return out, err
	}
	out.ChainID = uint32(chain)
	out.BlockNumber, err = uintValue(row["block_number"], 64)
	if err != nil {
		return out, err
	}
	out.BlockTime, err = timeValue(row["block_time"])
	if err != nil {
		return out, err
	}
	out.TransactionHash, _ = stringValue(row["tx_hash"])
	index, err := uintValue(row["log_index"], 32)
	if err != nil {
		return out, err
	}
	out.LogIndex = uint32(index)
	out.ContractAddress, _ = stringValue(row["contract_address"])
	out.Topic0, _ = stringValue(row["topic0"])
	out.EventName, _ = stringValue(row["event_name"])
	out.EventSignature, _ = stringValue(row["event_signature"])
	out.DecodedFields, _ = stringValue(row["decoded_fields"])
	out.DecoderSource, _ = stringValue(row["decoder_source"])
	out.DecoderConfidence, _ = stringValue(row["decoder_confidence"])
	out.ParserVersion, _ = stringValue(row["parser_version"])
	version, err := uintValue(row["schema_version"], 16)
	if err != nil {
		return out, err
	}
	out.SchemaVersion = uint16(version)
	out.IngestedAt, err = timeValue(row["ingested_at"])
	return out, err
}
