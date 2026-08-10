package eventdecoder

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

const (
	maxTopics    = 16
	maxDataBytes = 1 << 20
)

type Decoder struct {
	registry Registry
}

// EventDecoder is the explicit domain name retained for callers that prefer
// semantic naming over the shorter Decoder form.
type EventDecoder = Decoder

func New(registry Registry) *Decoder {
	// Built-ins are always the final topic0 fallback. Injected definitions keep
	// their declared source priority and therefore override them deterministically.
	return &Decoder{registry: NewMultiRegistry(registry, BuiltinRegistry())}
}

func NewEventDecoder(registry Registry) *EventDecoder {
	return New(registry)
}

func (d *Decoder) Decode(ctx context.Context, log Log) (Result, error) {
	raw := RawEvent{Topics: append([]string(nil), log.Topics...), Data: log.Data}
	if len(log.Topics) == 0 {
		return Result{EventName: "raw", DecoderSource: SourceRaw, DecoderConfidence: ConfidenceUnknown, DecodedFields: map[string]any{}, Raw: raw}, nil
	}
	if len(log.Topics) > maxTopics {
		return Result{}, fmt.Errorf("event topics exceed limit: %d", len(log.Topics))
	}
	topics := make([]string, len(log.Topics))
	for i, topic := range log.Topics {
		value, err := fixedHex(topic, 32)
		if err != nil {
			return Result{}, fmt.Errorf("topic[%d]: %w", i, err)
		}
		topics[i] = value
	}
	data, err := decodeHex(log.Data, maxDataBytes)
	if err != nil {
		return Result{}, fmt.Errorf("event data: %w", err)
	}
	definitions, err := d.registry.LookupEvent(ctx, Query{ChainID: log.ChainID, Contract: strings.ToLower(log.Contract), Topic0: topics[0]})
	if err != nil {
		return Result{}, fmt.Errorf("event registry lookup: %w", err)
	}
	selected, candidates, ambiguous := selectDefinition(definitions)
	if len(candidates) == 0 {
		return Result{EventName: "raw", DecoderSource: SourceRaw, DecoderConfidence: ConfidenceUnknown, DecodedFields: map[string]any{}, Raw: raw}, nil
	}
	if ambiguous {
		return Result{EventName: "ambiguous", DecoderSource: selected.Source, DecoderConfidence: ConfidenceLow, Ambiguous: true, CandidateSignatures: candidates, DecodedFields: map[string]any{}, Raw: raw}, nil
	}
	result := Result{EventName: selected.Name, EventSignature: selected.Signature, DecoderSource: selected.Source, DecoderConfidence: selected.Confidence, CandidateSignatures: candidates, DecodedFields: map[string]any{}, Raw: raw}
	fields, decodeErr := decodeFields(selected.Inputs, topics[1:], data)
	if decodeErr != nil {
		result.DecodeError = decodeErr.Error()
		return result, nil
	}
	result.DecodedFields = fields
	return result, nil
}

func selectDefinition(definitions []EventDefinition) (EventDefinition, []string, bool) {
	if len(definitions) == 0 {
		return EventDefinition{}, nil, false
	}
	best := 100
	byFingerprint := make(map[string]EventDefinition)
	for _, definition := range definitions {
		priority := sourcePriority(definition.Source)
		if priority < best {
			best = priority
			byFingerprint = map[string]EventDefinition{}
		}
		if priority == best && definition.Signature != "" {
			byFingerprint[definitionFingerprint(definition)] = definition
		}
	}
	fingerprints := make([]string, 0, len(byFingerprint))
	signatureSet := make(map[string]struct{})
	for fingerprint, definition := range byFingerprint {
		fingerprints = append(fingerprints, fingerprint)
		signatureSet[definition.Signature] = struct{}{}
	}
	sort.Strings(fingerprints)
	signatures := make([]string, 0, len(signatureSet))
	for signature := range signatureSet {
		signatures = append(signatures, signature)
	}
	sort.Strings(signatures)
	if len(signatures) == 0 {
		return EventDefinition{}, nil, false
	}
	return byFingerprint[fingerprints[0]], signatures, len(fingerprints) > 1
}

func definitionFingerprint(definition EventDefinition) string {
	var b strings.Builder
	b.WriteString(definition.Name)
	b.WriteByte('|')
	b.WriteString(definition.Signature)
	for _, input := range definition.Inputs {
		b.WriteByte('|')
		b.WriteString(input.Name)
		b.WriteByte(':')
		b.WriteString(input.Type)
		if input.Indexed {
			b.WriteString(":indexed")
		}
	}
	return b.String()
}

func decodeFields(inputs []Input, topics []string, data []byte) (map[string]any, error) {
	indexedCount := 0
	nonIndexedCount := 0
	for _, input := range inputs {
		if input.Indexed {
			indexedCount++
		} else {
			nonIndexedCount++
		}
	}
	if len(topics) != indexedCount {
		return nil, fmt.Errorf("indexed topic count mismatch: got %d want %d", len(topics), indexedCount)
	}
	if len(data) != nonIndexedCount*32 {
		return nil, fmt.Errorf("static ABI data length mismatch: got %d want %d", len(data), nonIndexedCount*32)
	}
	result := make(map[string]any, len(inputs))
	topicIndex, dataIndex := 0, 0
	for index, input := range inputs {
		name := input.Name
		if name == "" {
			name = fmt.Sprintf("field_%d", index)
		}
		var word []byte
		if input.Indexed {
			decoded, _ := hex.DecodeString(strings.TrimPrefix(topics[topicIndex], "0x"))
			word = decoded
			topicIndex++
		} else {
			word = data[dataIndex*32 : (dataIndex+1)*32]
			dataIndex++
		}
		value, err := decodeWord(input.Type, word, input.Indexed)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", name, err)
		}
		result[name] = value
	}
	return result, nil
}

func decodeWord(typ string, word []byte, indexed bool) (any, error) {
	if len(word) != 32 {
		return nil, errors.New("ABI word is not 32 bytes")
	}
	typ = strings.ToLower(strings.TrimSpace(typ))
	switch {
	case typ == "address":
		return "0x" + hex.EncodeToString(word[12:]), nil
	case typ == "bool":
		v := new(big.Int).SetBytes(word)
		if v.Cmp(big.NewInt(1)) > 0 {
			return nil, errors.New("invalid bool")
		}
		return v.Sign() == 1, nil
	case typ == "uint" || strings.HasPrefix(typ, "uint"):
		return new(big.Int).SetBytes(word).String(), nil
	case typ == "int" || strings.HasPrefix(typ, "int"):
		value := new(big.Int).SetBytes(word)
		if word[0]&0x80 != 0 {
			value.Sub(value, new(big.Int).Lsh(big.NewInt(1), 256))
		}
		return value.String(), nil
	case typ == "bytes32":
		return "0x" + hex.EncodeToString(word), nil
	case indexed:
		// Indexed dynamic values are hashes by ABI definition and cannot be
		// reconstructed; retaining the hash is the only lossless result.
		return "0x" + hex.EncodeToString(word), nil
	default:
		return nil, fmt.Errorf("unsupported static ABI type %q", typ)
	}
}

func fixedHex(value string, bytes int) (string, error) {
	decoded, err := decodeHex(value, bytes)
	if err != nil {
		return "", err
	}
	if len(decoded) != bytes {
		return "", fmt.Errorf("hex length is %d bytes, want %d", len(decoded), bytes)
	}
	return "0x" + hex.EncodeToString(decoded), nil
}

func decodeHex(value string, maxBytes int) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if len(value)%2 != 0 {
		return nil, errors.New("hex has odd length")
	}
	if len(value)/2 > maxBytes {
		return nil, errors.New("hex exceeds size limit")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, errors.New("invalid hex")
	}
	return decoded, nil
}
