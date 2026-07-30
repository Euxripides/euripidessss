package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/normalize"
)

func (c *Client) BalanceSnapshots(
	ctx context.Context,
	network chain.EVM,
	addresses []string,
	metadata []normalize.TokenMetadata,
) ([]normalize.BalanceSnapshot, error) {
	if err := c.Probe(ctx, network, ""); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	result := make([]normalize.BalanceSnapshot, 0, len(addresses)*(len(metadata)+1))
	for _, address := range addresses {
		address = strings.ToLower(address)
		requests := []rpcRequest{{
			JSONRPC: "2.0", ID: 1, Method: "eth_getBalance", Params: []any{address, "latest"},
		}}
		for index, token := range metadata {
			requests = append(requests, rpcRequest{
				JSONRPC: "2.0", ID: index + 2, Method: "eth_call",
				Params: []any{map[string]string{
					"to": token.TokenAddress, "data": balanceOfCallData(address),
				}, "latest"},
			})
		}
		values, responseErrors, err := c.callPartial(ctx, requests)
		if err != nil {
			return nil, err
		}
		if responseErrors[1] == nil {
			raw, ok := decodeRPCQuantity(values[1])
			if ok {
				result = append(result, normalize.BalanceSnapshot{
					ChainKey: network.Key, ChainID: network.ID, Address: address,
					AssetType: "NATIVE", BalanceRaw: raw.String(), Balance: formatDecimalRPC(raw, 18),
					SnapshotTime: now, Source: "RPC_LATEST",
				})
			}
		}
		for index, token := range metadata {
			if responseErrors[index+2] != nil {
				continue
			}
			raw, ok := decodeRPCQuantity(values[index+2])
			if !ok {
				continue
			}
			balance := ""
			if token.Decimals != nil {
				balance = formatDecimalRPC(raw, int(*token.Decimals))
			}
			result = append(result, normalize.BalanceSnapshot{
				ChainKey: network.Key, ChainID: network.ID, Address: address,
				AssetType: "TOKEN", AssetAddress: token.TokenAddress,
				BalanceRaw: raw.String(), Balance: balance, SnapshotTime: now, Source: "RPC_LATEST",
			})
		}
	}
	return result, nil
}

func (c *Client) AddressTypes(ctx context.Context, network chain.EVM, addresses []string) (map[string]string, error) {
	if err := c.Probe(ctx, network, ""); err != nil {
		return nil, err
	}
	requests := make([]rpcRequest, 0, len(addresses))
	for index, address := range addresses {
		requests = append(requests, rpcRequest{
			JSONRPC: "2.0", ID: index + 1, Method: "eth_getCode",
			Params: []any{strings.ToLower(address), "latest"},
		})
	}
	values, responseErrors, err := c.callPartial(ctx, requests)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(addresses))
	for index, address := range addresses {
		address = strings.ToLower(address)
		if responseErrors[index+1] != nil {
			result[address] = "UNKNOWN"
			continue
		}
		var code string
		if json.Unmarshal(values[index+1], &code) != nil {
			result[address] = "UNKNOWN"
		} else if code == "" || code == "0x" || code == "0x0" {
			result[address] = "EOA"
		} else {
			result[address] = "CONTRACT"
		}
	}
	return result, nil
}

func balanceOfCallData(address string) string {
	address = strings.TrimPrefix(strings.ToLower(address), "0x")
	return "0x70a08231" + strings.Repeat("0", 64-len(address)) + address
}

func decodeRPCQuantity(raw []byte) (*big.Int, bool) {
	var encoded string
	if err := jsonUnmarshalRPC(raw, &encoded); err != nil {
		return nil, false
	}
	encoded = strings.TrimPrefix(strings.TrimSpace(encoded), "0x")
	if encoded == "" {
		return big.NewInt(0), true
	}
	value := new(big.Int)
	_, ok := value.SetString(encoded, 16)
	return value, ok
}

func jsonUnmarshalRPC(raw []byte, target any) error {
	if len(raw) == 0 {
		return fmt.Errorf("空 RPC 响应")
	}
	return json.Unmarshal(raw, target)
}

func formatDecimalRPC(value *big.Int, decimals int) string {
	if decimals <= 0 {
		return value.String()
	}
	text := value.String()
	if len(text) <= decimals {
		text = strings.Repeat("0", decimals-len(text)+1) + text
	}
	split := len(text) - decimals
	result := strings.TrimRight(strings.TrimRight(text[:split]+"."+text[split:], "0"), ".")
	if result == "" {
		return "0"
	}
	return result
}
