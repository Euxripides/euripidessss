package eventdecoder

import (
	"math/big"
	"strings"
)

const (
	ActivityRaw            = "RAW"
	ActivityDEXSwap        = "DEX_SWAP"
	ActivityBridgeDeposit  = "BRIDGE_DEPOSIT"
	ActivityBridgeWithdraw = "BRIDGE_WITHDRAW"
	ActivityBridgeSend     = "BRIDGE_SEND"
	ActivityBridgeReceive  = "BRIDGE_RECEIVE"
)

// Classify upgrades a decoded event only when all canonical fields are known.
// It intentionally returns RAW for partial evidence instead of guessing.
func Classify(log Log, event Result, context SemanticContext) SemanticResult {
	if event.Ambiguous || event.DecodeError != "" {
		return SemanticResult{ActivityType: ActivityRaw}
	}
	if swap, ok := classifyV2Swap(log, event, context); ok {
		return SemanticResult{ActivityType: ActivityDEXSwap, DEXSwap: swap}
	}
	if bridge, ok := classifyBridge(log, event, context); ok {
		return SemanticResult{ActivityType: bridge.Type, Bridge: bridge}
	}
	return SemanticResult{ActivityType: ActivityRaw}
}

func classifyV2Swap(log Log, event Result, context SemanticContext) (*DEXSwap, bool) {
	if event.EventName != "Swap" || context.Protocol == "" || context.Pool == "" || context.Token0 == "" || context.Token1 == "" {
		return nil, false
	}
	trader := firstNonEmpty(context.Trader, fieldString(event.DecodedFields, "sender"))
	if trader == "" {
		return nil, false
	}
	a0in, ok0i := positive(fieldString(event.DecodedFields, "amount0In"))
	a1in, ok1i := positive(fieldString(event.DecodedFields, "amount1In"))
	a0out, ok0o := positive(fieldString(event.DecodedFields, "amount0Out"))
	a1out, ok1o := positive(fieldString(event.DecodedFields, "amount1Out"))
	var tokenIn, amountIn, tokenOut, amountOut string
	switch {
	case ok0i && ok1o && !ok1i && !ok0o:
		tokenIn, amountIn, tokenOut, amountOut = context.Token0, a0in, context.Token1, a1out
	case ok1i && ok0o && !ok0i && !ok1o:
		tokenIn, amountIn, tokenOut, amountOut = context.Token1, a1in, context.Token0, a0out
	default:
		return nil, false
	}
	return &DEXSwap{Type: ActivityDEXSwap, TxHash: log.TransactionHash, Protocol: context.Protocol, Router: context.Router, Pool: context.Pool, Trader: trader, TokenIn: tokenIn, AmountIn: amountIn, TokenOut: tokenOut, AmountOut: amountOut, USDValue: context.USDValue}, true
}

func classifyBridge(log Log, event Result, context SemanticContext) (*BridgeEvent, bool) {
	if context.Bridge == "" || context.SourceChain == "" || context.DestinationChain == "" || context.SourceAddress == "" || context.DestinationAddress == "" || context.Token == "" {
		return nil, false
	}
	amount := firstNonEmpty(fieldString(event.DecodedFields, "amount"), fieldString(event.DecodedFields, "value"), fieldString(event.DecodedFields, "wad"))
	if _, ok := nonNegative(amount); !ok {
		return nil, false
	}
	var typ string
	switch strings.ToLower(event.EventName) {
	case "deposit", "bridgedeposit":
		typ = ActivityBridgeDeposit
	case "withdraw", "withdrawal", "bridgewithdraw":
		typ = ActivityBridgeWithdraw
	case "send", "messagesent", "bridgesend":
		typ = ActivityBridgeSend
	case "receive", "messagereceived", "bridgereceive":
		typ = ActivityBridgeReceive
	default:
		return nil, false
	}
	return &BridgeEvent{Type: typ, TxHash: log.TransactionHash, Bridge: context.Bridge, SourceChain: context.SourceChain, DestinationChain: context.DestinationChain, SourceAddress: context.SourceAddress, DestinationAddress: context.DestinationAddress, Token: context.Token, Amount: amount, USDValue: context.USDValue}, true
}

func PreserveCalls(calls []CanonicalCall) []CanonicalCall {
	result := make([]CanonicalCall, len(calls))
	copy(result, calls)
	return result
}

func fieldString(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

func positive(value string) (string, bool) {
	n, ok := new(big.Int).SetString(value, 10)
	return value, ok && n.Sign() > 0
}

func nonNegative(value string) (string, bool) {
	n, ok := new(big.Int).SetString(value, 10)
	return value, ok && n.Sign() >= 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
