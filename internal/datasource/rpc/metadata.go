package rpc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/normalize"
)

const (
	nameSelector        = "0x06fdde03"
	symbolSelector      = "0x95d89b41"
	decimalsSelector    = "0x313ce567"
	totalSupplySelector = "0x18160ddd"
)

func (c *Client) TokenMetadata(
	ctx context.Context,
	network chain.EVM,
	tokens map[string]string,
) ([]normalize.TokenMetadata, error) {
	if err := c.Probe(ctx, network, ""); err != nil {
		return nil, err
	}
	result := make([]normalize.TokenMetadata, 0, len(tokens))
	for address, standard := range tokens {
		address = strings.ToLower(address)
		requests := []rpcRequest{
			{JSONRPC: "2.0", ID: 1, Method: "eth_call", Params: []any{map[string]string{"to": address, "data": nameSelector}, "latest"}},
			{JSONRPC: "2.0", ID: 2, Method: "eth_call", Params: []any{map[string]string{"to": address, "data": symbolSelector}, "latest"}},
			{JSONRPC: "2.0", ID: 3, Method: "eth_call", Params: []any{map[string]string{"to": address, "data": decimalsSelector}, "latest"}},
			{JSONRPC: "2.0", ID: 4, Method: "eth_call", Params: []any{map[string]string{"to": address, "data": totalSupplySelector}, "latest"}},
		}
		values, responseErrors, err := c.callPartial(ctx, requests)
		if err != nil {
			return nil, err
		}
		item := normalize.TokenMetadata{
			ChainKey: network.Key, ChainID: network.ID, TokenAddress: address,
			Name: "UNKNOWN", Symbol: "UNKNOWN", Standard: standard,
			UpdatedAt: time.Now().UTC(), Source: "RPC",
		}
		if responseErrors[1] == nil {
			if value := decodeABIText(values[1]); value != "" {
				item.Name = value
			}
		}
		if responseErrors[2] == nil {
			if value := decodeABIText(values[2]); value != "" {
				item.Symbol = value
			}
		}
		if responseErrors[3] == nil {
			if value, ok := decodeABIUint(values[3]); ok && value.IsUint64() && value.Uint64() <= 255 {
				decimals := uint8(value.Uint64())
				item.Decimals = &decimals
			}
		}
		if responseErrors[4] == nil {
			if value, ok := decodeABIUint(values[4]); ok {
				item.TotalSupply = value.String()
			}
		}
		if item.Name == "UNKNOWN" || item.Symbol == "UNKNOWN" || item.Decimals == nil {
			item.Source = "RPC_PARTIAL"
		}
		result = append(result, item)
	}
	return result, nil
}

func decodeABIText(raw json.RawMessage) string {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return ""
	}
	data, err := hex.DecodeString(strings.TrimPrefix(encoded, "0x"))
	if err != nil || len(data) == 0 {
		return ""
	}
	var text []byte
	if len(data) >= 64 {
		offset := new(big.Int).SetBytes(data[:32])
		if offset.IsUint64() {
			start := int(offset.Uint64())
			if start >= 0 && start+32 <= len(data) {
				length := new(big.Int).SetBytes(data[start : start+32])
				if length.IsUint64() {
					end := start + 32 + int(length.Uint64())
					if end <= len(data) {
						text = data[start+32 : end]
					}
				}
			}
		}
	}
	if len(text) == 0 {
		text = bytes.TrimRight(data[:minIntRPC(32, len(data))], "\x00")
	}
	if !utf8.Valid(text) {
		return ""
	}
	return strings.TrimSpace(string(text))
}

func decodeABIUint(raw json.RawMessage) (*big.Int, bool) {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return nil, false
	}
	data, err := hex.DecodeString(strings.TrimPrefix(encoded, "0x"))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return new(big.Int).SetBytes(data), true
}

func minIntRPC(a, b int) int {
	if a < b {
		return a
	}
	return b
}
