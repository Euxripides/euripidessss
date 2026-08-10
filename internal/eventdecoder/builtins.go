package eventdecoder

import (
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/sha3"
)

func Topic0(signature string) string {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write([]byte(signature))
	return "0x" + hex.EncodeToString(h.Sum(nil))
}

func BuiltinRegistry() *MemoryRegistry {
	return NewMemoryRegistry(
		builtin("Transfer", "Transfer(address,address,uint256)", []Input{{"from", "address", true}, {"to", "address", true}, {"value", "uint256", false}}),
		builtin("Approval", "Approval(address,address,uint256)", []Input{{"owner", "address", true}, {"spender", "address", true}, {"value", "uint256", false}}),
		builtin("OwnershipTransferred", "OwnershipTransferred(address,address)", []Input{{"previousOwner", "address", true}, {"newOwner", "address", true}}),
		builtin("PairCreated", "PairCreated(address,address,address,uint256)", []Input{{"token0", "address", true}, {"token1", "address", true}, {"pair", "address", false}, {"pairCount", "uint256", false}}),
		builtin("Swap", "Swap(address,uint256,uint256,uint256,uint256,address)", []Input{{"sender", "address", true}, {"amount0In", "uint256", false}, {"amount1In", "uint256", false}, {"amount0Out", "uint256", false}, {"amount1Out", "uint256", false}, {"to", "address", true}}),
		builtin("SwapV3", "Swap(address,address,int256,int256,uint160,uint128,int24)", []Input{{"sender", "address", true}, {"recipient", "address", true}, {"amount0", "int256", false}, {"amount1", "int256", false}, {"sqrtPriceX96", "uint160", false}, {"liquidity", "uint128", false}, {"tick", "int24", false}}),
		builtin("PoolCreatedV3", "PoolCreated(address,address,uint24,int24,address)", []Input{{"token0", "address", true}, {"token1", "address", true}, {"fee", "uint24", true}, {"tickSpacing", "int24", false}, {"pool", "address", false}}),
		builtin("Mint", "Mint(address,uint256,uint256)", []Input{{"sender", "address", true}, {"amount0", "uint256", false}, {"amount1", "uint256", false}}),
		builtin("Burn", "Burn(address,uint256,uint256,address)", []Input{{"sender", "address", true}, {"amount0", "uint256", false}, {"amount1", "uint256", false}, {"to", "address", true}}),
		builtin("Deposit", "Deposit(address,uint256)", []Input{{"dst", "address", true}, {"wad", "uint256", false}}),
		builtin("Withdrawal", "Withdrawal(address,uint256)", []Input{{"src", "address", true}, {"wad", "uint256", false}}),
		builtin("Withdraw", "Withdraw(address,uint256)", []Input{{"account", "address", true}, {"amount", "uint256", false}}),
	)
}

func builtin(name, signature string, inputs []Input) EventDefinition {
	return EventDefinition{Name: name, Signature: signature, Topic0: Topic0(signature), Inputs: inputs, Source: SourceTopic0, Confidence: ConfidenceLow}
}

func normalizeHex(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "0x") {
		value = "0x" + value
	}
	return value
}
