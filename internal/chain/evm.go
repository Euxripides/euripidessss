package chain

import (
	"fmt"
	"strings"
)

// EVM describes chain-specific constants without coupling data sources to a chain.
type EVM struct {
	Key          string `json:"chain_key"`
	ID           int64  `json:"chain_id"`
	Name         string `json:"name"`
	NativeSymbol string `json:"native_symbol"`
	RPCEnv       string `json:"rpc_env"`
	SQDDataset   string `json:"sqd_dataset"`
}

var supported = map[string]EVM{
	"bsc": {
		Key:          "bsc",
		ID:           56,
		Name:         "BNB Smart Chain",
		NativeSymbol: "BNB",
		RPCEnv:       "BSC_RPC",
		SQDDataset:   "binance-mainnet",
	},
	"eth": {
		Key:          "eth",
		ID:           1,
		Name:         "Ethereum",
		NativeSymbol: "ETH",
		RPCEnv:       "ETH_RPC",
		SQDDataset:   "ethereum-mainnet",
	},
	"base": {
		Key:          "base",
		ID:           8453,
		Name:         "Base",
		NativeSymbol: "ETH",
		RPCEnv:       "BASE_RPC",
		SQDDataset:   "base-mainnet",
	},
	"arbitrum": {
		Key:          "arbitrum",
		ID:           42161,
		Name:         "Arbitrum One",
		NativeSymbol: "ETH",
		RPCEnv:       "ARBITRUM_RPC",
		SQDDataset:   "arbitrum-one",
	},
}

func Resolve(key string) (EVM, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		key = "bsc"
	}
	item, ok := supported[key]
	if !ok {
		return EVM{}, fmt.Errorf("不支持的 EVM 网络: %s", key)
	}
	return item, nil
}

func Supported() []EVM {
	return []EVM{supported["bsc"], supported["eth"], supported["base"], supported["arbitrum"]}
}
