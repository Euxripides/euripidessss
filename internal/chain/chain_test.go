package chain

import "testing"

func TestResolveUsesChainScopedIdentity(t *testing.T) {
	bsc, err := Resolve("bsc")
	if err != nil {
		t.Fatal(err)
	}
	eth, err := Resolve("eth")
	if err != nil {
		t.Fatal(err)
	}
	if bsc.ID != 56 || bsc.NativeSymbol != "BNB" || bsc.RPCEnv != "BSC_RPC" {
		t.Fatalf("unexpected BSC adapter: %+v", bsc)
	}
	if eth.ID != 1 || eth.NativeSymbol != "ETH" || eth.RPCEnv != "ETH_RPC" {
		t.Fatalf("unexpected ETH adapter: %+v", eth)
	}
	base, err := Resolve("base")
	if err != nil || base.ID != 8453 || base.SQDDataset != "base-mainnet" {
		t.Fatalf("unexpected Base adapter: %+v err=%v", base, err)
	}
	arbitrum, err := Resolve("arbitrum")
	if err != nil || arbitrum.ID != 42161 || arbitrum.SQDDataset != "arbitrum-one" {
		t.Fatalf("unexpected Arbitrum adapter: %+v err=%v", arbitrum, err)
	}
}
