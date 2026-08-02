package flow

import (
	"context"
	"math/big"
	"testing"
	"time"
)

// ── 实时资产服务单元测试（V2.0 设计 §15/§16/§28）──
//
// 纯逻辑测试（不依赖真实 RPC）：Token 解析、余额格式化、缓存 TTL、
// 批量限制、脱敏。RPC 查询路径由 enable-flag 真实链测试覆盖（见 assets_rpc_test.go）。

// ── Token 解析 ──

func TestResolveTokenSpecs(t *testing.T) {
	cfg, _ := chainAssetsFor("bsc")
	specs := resolveTokenSpecs(cfg, nil)
	// native + USDT + USDC 默认
	if len(specs) != 3 {
		t.Fatalf("默认应 3 个资产（native+USDT+USDC）, got %d: %+v", len(specs), specs)
	}
	if specs[0].Symbol != "BNB" || specs[0].Address != "" {
		t.Fatalf("首个应为 native BNB: %+v", specs[0])
	}
	// 去重 + 已知符号
	specs2 := resolveTokenSpecs(cfg, []string{"usdt", "usdc", "0x55d398326f99059ff775485246999027b3197955", "native"})
	if len(specs2) != 3 {
		t.Fatalf("重复 Token 应去重, got %d: %+v", len(specs2), specs2)
	}
	// 自定义 Token
	specs3 := resolveTokenSpecs(cfg, []string{"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d"})
	found := false
	for _, s := range specs3 {
		if s.Address == "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d" {
			found = true
		}
	}
	if !found {
		t.Fatalf("自定义 Token 应被解析: %+v", specs3)
	}
}

// ── review should-fix：ETH 链 USDT/USDC 6 位小数（不应硬编码 18）──

func TestResolveTokenSpecsETHDecimals(t *testing.T) {
	cfg, _ := chainAssetsFor("eth")
	specs := resolveTokenSpecs(cfg, nil)
	for _, s := range specs {
		switch s.Symbol {
		case "USDT", "USDC":
			if s.Decimals != 6 {
				t.Fatalf("ETH 链 %s 应为 6 位小数, got %d", s.Symbol, s.Decimals)
			}
		case "ETH":
			if s.Decimals != 18 {
				t.Fatalf("ETH native 应为 18 位小数, got %d", s.Decimals)
			}
		}
	}
	// ETH 链 USDT raw 1e6 → 1.0（6 位小数格式化）
	raw := toBig("1000000")
	if got := formatBalance(raw, decimalsOf(cfg.USDTDecimals, 18)); got != "1" {
		t.Fatalf("ETH USDT 1e6 raw 应格式化为 1, got %s", got)
	}
	// BSC 链 USDT raw 1e18 → 1.0（18 位小数）
	cfgBsc, _ := chainAssetsFor("bsc")
	if got := formatBalance(toBig("1000000000000000000"), decimalsOf(cfgBsc.USDTDecimals, 18)); got != "1" {
		t.Fatalf("BSC USDT 1e18 raw 应格式化为 1, got %s", got)
	}
}

// ── 余额格式化 ──

func TestFormatBalance(t *testing.T) {
	// 1253120.5 USDT（18 位小数）
	raw := toBig("1253120500000000000000000")
	if got := formatBalance(raw, 18); got != "1253120.5" {
		t.Fatalf("formatBalance = %s, want 1253120.5", got)
	}
	// 0
	if got := formatBalance(toBig("0"), 18); got != "0" {
		t.Fatalf("formatBalance(0) = %s", got)
	}
	// 小数不足 6 位
	if got := formatBalance(toBig("12300000000000000"), 18); got != "0.0123" {
		t.Fatalf("formatBalance(0.0123) = %s", got)
	}
	// 大额（1e24 wei = 1000000 USDT）
	if got := formatBalance(toBig("1000000000000000000000000"), 18); got != "1000000" {
		t.Fatalf("formatBalance(1e6) = %s", got)
	}
}

// ── 缓存 TTL（设计 §16 时效定义）──

func TestTTLState(t *testing.T) {
	now := time.Now().UTC()
	if got := ttlState(now.Add(-30*time.Second), false); got != AssetFresh {
		t.Fatalf("30 秒应为 fresh, got %s", got)
	}
	if got := ttlState(now.Add(-5*time.Minute), false); got != AssetCached {
		t.Fatalf("5 分钟应为 cached, got %s", got)
	}
	if got := ttlState(now.Add(-30*time.Minute), false); got != AssetStale {
		t.Fatalf("30 分钟应为 stale, got %s", got)
	}
}

// ── 缓存读写与淘汰 ──

func TestAssetCache(t *testing.T) {
	c := newAssetCache(2)
	c.set("a", AddressAssets{Address: "0xa", Status: AssetFresh})
	time.Sleep(2 * time.Millisecond) // 保证 a 是"最旧"
	c.set("b", AddressAssets{Address: "0xb", Status: AssetFresh})
	v, _, ok := c.get("a")
	if !ok || v.Address != "0xa" {
		t.Fatalf("get a 失败: %+v", v)
	}
	// 超限淘汰最旧（a）
	c.set("c", AddressAssets{Address: "0xc", Status: AssetFresh})
	if _, _, ok := c.get("a"); ok {
		t.Fatal("超限后最旧条目应被淘汰")
	}
	if _, _, ok := c.get("b"); !ok {
		t.Fatal("b 应保留")
	}
	if _, _, ok := c.get("c"); !ok {
		t.Fatal("c 应保留")
	}
}

// ── 批量限制（设计 §15.2）──

func TestBatchAssetsLimits(t *testing.T) {
	svc := NewAssetService(nil) // rpc nil：AddressAssets 返回错误，但批量限制先于查询
	// 超过 50 地址应报错
	addresses := make([]string, 51)
	for i := range addresses {
		addresses[i] = "0x0000000000000000000000000000000000000001"
	}
	if _, err := svc.BatchAssets(context.Background(), "bsc", 56, addresses, nil); err == nil {
		t.Fatal("超过 50 地址应报错")
	}
	// 超过 20 Token 应报错
	tokens := make([]string, 21)
	for i := range tokens {
		tokens[i] = "0x0000000000000000000000000000000000000002"
	}
	if _, err := svc.BatchAssets(context.Background(), "bsc", 56, []string{"0x0000000000000000000000000000000000000001"}, tokens); err == nil {
		t.Fatal("超过 20 Token 应报错")
	}
}

// ── RPC 错误脱敏（设计 §26）──

func TestSanitizeRPCError(t *testing.T) {
	longErr := "rpc error: https://rpc.example.com/secret-key-abcdef123456 " +
		"connection refused after 5000ms with very long detail message that should be truncated to avoid leaking secrets"
	got := sanitizeRPCError(jsonErr(longErr))
	if len(got) > 160 {
		t.Fatalf("错误应被截断, len=%d", len(got))
	}
	if got == "" {
		t.Fatal("非空错误不应为空")
	}
	if sanitizeRPCError(nil) != "" {
		t.Fatal("nil 错误应返回空")
	}
}

// ── 辅助 ──

type errString string

func (e errString) Error() string { return string(e) }

func jsonErr(s string) error { return errString(s) }

func toBig(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return new(big.Int)
	}
	return n
}
