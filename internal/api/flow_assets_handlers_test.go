package api

import "testing"

func TestValidChainKeyMatchesExplorerChains(t *testing.T) {
	for _, chain := range []string{"bsc", "eth", "base", "arbitrum", "BASE", "ARBITRUM"} {
		if !validChainKey(chain) {
			t.Fatalf("Explorer 链 %q 应支持资产查询", chain)
		}
	}
	for _, chain := range []string{"", "polygon", "../bsc", "bsc/../../"} {
		if validChainKey(chain) {
			t.Fatalf("非法或未支持链 %q 不应通过白名单", chain)
		}
	}
}
