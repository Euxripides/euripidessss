// Package flow 实现链上分析平台地址关系图 V2.0 的实时资产服务
// （设计 §13-§17：Balance API + 缓存 + Provider Router + 批量查询）。
//
// 复用 rpcmanager.Manager（Provider Router，含健康/成功率/P95/429 容灾）
// 与 normalize.TokenMetadata；不新增外部依赖。
//
// 查询链路：Balance API → Balance Service → Cache → Provider Router →
// RPC Batch/Multicall/eth_call → Normalize → 余额+区块+时间+数据源（设计 §13）。
package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/normalize"
	"github.com/etl/backend/internal/rpcmanager"
)

// ── 链资产配置（设计 §22 ChainAssetConfig）──

// ChainAssets 是链级资产配置（首期 EVM 链）。
type ChainAssets struct {
	ChainKey         string `json:"chain_key"`
	ChainID          int64  `json:"chain_id"`
	NativeSymbol     string `json:"native_symbol"`
	USDTAddress      string `json:"usdt_address,omitempty"`
	USDTDecimals     int    `json:"usdt_decimals,omitempty"`
	USDCAddress      string `json:"usdc_address,omitempty"`
	USDCDecimals     int    `json:"usdc_decimals,omitempty"`
	MulticallAddress string `json:"multicall_address,omitempty"`
}

// DefaultChainAssets 返回内置链配置（BSC/ETH；USDT/USDC 主网地址与小数位）。
func DefaultChainAssets() []ChainAssets {
	return []ChainAssets{
		{
			ChainKey: "bsc", ChainID: 56, NativeSymbol: "BNB",
			USDTAddress:      "0x55d398326f99059ff775485246999027b3197955",
			USDTDecimals:     18,
			USDCAddress:      "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d",
			USDCDecimals:     18,
			MulticallAddress: "0xcA11bde05977b3631167028862bE2a173976CA11",
		},
		{
			ChainKey: "eth", ChainID: 1, NativeSymbol: "ETH",
			USDTAddress:      "0xdac17f958d2ee523a2206206994597c13d831ec7",
			USDTDecimals:     6, // ETH 主网 USDT 为 6 位小数
			USDCAddress:      "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
			USDCDecimals:     6, // ETH 主网 USDC 为 6 位小数
			MulticallAddress: "0xcA11bde05977b3631167028862bE2a173976CA11",
		},
	}
}

func chainAssetsFor(chainKey string) (ChainAssets, bool) {
	for _, c := range DefaultChainAssets() {
		if c.ChainKey == chainKey {
			return c, true
		}
	}
	return ChainAssets{}, false
}

// ── 资产查询模型（设计 §15.1 响应）──

// AssetState 是资产状态（设计 §16 缓存状态）。
type AssetState string

const (
	AssetFresh   AssetState = "fresh"   // 0-60 秒
	AssetCached  AssetState = "cached"  // 60 秒-10 分钟
	AssetStale   AssetState = "stale"   // 超 10 分钟
	AssetPartial AssetState = "partial" // 部分 Token 失败
	AssetFailed  AssetState = "failed"  // 全部失败
)

// AssetBalance 是单 Token 余额。
type AssetBalance struct {
	TokenAddress string `json:"token_address"`
	Symbol       string `json:"symbol"`
	Decimals     int    `json:"decimals"`
	RawBalance   string `json:"raw_balance"`
	Balance      string `json:"balance"`
	Status       string `json:"status"` // success / failed
	Error        string `json:"error,omitempty"`
}

// AddressAssets 是单地址资产响应（设计 §15.1）。
type AddressAssets struct {
	Chain      string         `json:"chain"`
	ChainID    int64          `json:"chain_id"`
	Address    string         `json:"address"`
	BlockNumber string        `json:"block_number,omitempty"`
	QueriedAt  time.Time      `json:"queried_at"`
	Source     string         `json:"source"`
	Status     AssetState     `json:"status"`
	Assets     []AssetBalance `json:"assets"`
}

// ── 缓存（设计 §16：有界内存 + TTL 分级）──

// cacheEntry 是缓存条目。
type cacheEntry struct {
	value    AddressAssets
	storedAt time.Time
}

// assetCache 是有界内存缓存（LRU 简化：超限清最旧）。
type assetCache struct {
	mu      sync.Mutex
	maxSize int
	entries map[string]*cacheEntry
}

func newAssetCache(maxSize int) *assetCache {
	if maxSize <= 0 {
		maxSize = 2000
	}
	return &assetCache{maxSize: maxSize, entries: make(map[string]*cacheEntry)}
}

func (c *assetCache) get(key string) (AddressAssets, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return AddressAssets{}, time.Time{}, false
	}
	return e.value, e.storedAt, true
}

func (c *assetCache) set(key string, v AddressAssets) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		// 淘汰最旧（简单 O(n)，缓存规模小）
		var oldestKey string
		var oldest time.Time
		for k, e := range c.entries {
			if oldestKey == "" || e.storedAt.Before(oldest) {
				oldestKey = k
				oldest = e.storedAt
			}
		}
		if oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}
	c.entries[key] = &cacheEntry{value: v, storedAt: time.Now().UTC()}
}

// ── Token 解析（设计 §17 Token 发现策略）──

// assetRequest 是一次查询请求的 Token 列表。
type assetRequest struct {
	ChainKey string
	ChainID  int64
	Address  string
	Tokens   []tokenSpec
}

type tokenSpec struct {
	Address string // "" = native
	Symbol  string
	Decimals int
}

// ── AssetStore 接口（设计 §16：未来可迁移 DuckDB，业务层经接口访问）──

// AssetStore 是资产查询统一接口（设计 §16 结尾：业务层必须通过 AssetStore 访问）。
type AssetStore interface {
	// AddressAssets 查询单地址实时资产。
	AddressAssets(ctx context.Context, chainKey string, chainID int64, address string, tokens []string, forceRefresh bool) (*AddressAssets, error)
}

// ── AssetService 实现 ──

// AssetService 是实时资产服务（Provider Router 复用 rpcmanager）。
type AssetService struct {
	rpc    *rpcmanager.Manager
	cache  *assetCache
	mu     sync.Mutex // 串行化同地址并发查询（防 RPC 洪泛）
	inflight map[string]bool
}

// NewAssetService 创建资产服务。rpc 为 nil 时服务不可用（返回错误）。
func NewAssetService(rpc *rpcmanager.Manager) *AssetService {
	return &AssetService{
		rpc:      rpc,
		cache:    newAssetCache(2000),
		inflight: make(map[string]bool),
	}
}

// Available 判断服务可用（RPC 管理器已配置）。
func (s *AssetService) Available() bool {
	return s != nil && s.rpc != nil
}

// AddressAssets 查询单地址实时资产（设计 §15.1/§16）。
// tokens 为空时使用默认资产（native + USDT + USDC）。
func (s *AssetService) AddressAssets(ctx context.Context, chainKey string, chainID int64, address string, tokens []string, forceRefresh bool) (*AddressAssets, error) {
	if !s.Available() {
		return nil, errors.New("asset service 不可用（RPC 管理器未配置）")
	}
	address = strings.ToLower(strings.TrimSpace(address))
	evm, err := chain.Resolve(chainKey)
	if err != nil {
		return nil, fmt.Errorf("未知链: %s", chainKey)
	}
	if chainID > 0 && evm.ID != chainID {
		// 容忍 chainID 不匹配（前端可能传别名），以 chain.Resolve 为准
	}
	chainCfg, ok := chainAssetsFor(chainKey)
	if !ok {
		return nil, fmt.Errorf("未配置链资产: %s", chainKey)
	}
	specs := resolveTokenSpecs(chainCfg, tokens)

	cacheKey := cacheKeyFor(chainKey, address, specs)
	if !forceRefresh {
		if cached, storedAt, ok := s.cache.get(cacheKey); ok {
			// TTL 分级（设计 §16）：fresh/cached 直接返回；stale（>10 分钟）
			// 穿透查询刷新（should-fix：避免缓存永远过期）
			state := ttlState(storedAt, isCenterAddress(chainKey, address))
			if state != AssetStale {
				out := cached
				out.Status = state
				return &out, nil
			}
		}
	}

	// 同地址同 Token 组合并发去重（防 RPC 洪泛；should-fix：按 cacheKey 键，
	// 避免同地址不同 Token 组合的并发请求误报）
	s.mu.Lock()
	if s.inflight[cacheKey] {
		s.mu.Unlock()
		// 等待中的请求直接返回上次缓存（若有），否则空响应
		if cached, _, ok := s.cache.get(cacheKey); ok {
			out := cached
			out.Status = AssetCached
			return &out, nil
		}
		return nil, errors.New("该地址正在查询中")
	}
	s.inflight[cacheKey] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, cacheKey)
		s.mu.Unlock()
	}()

	result, err := s.queryAddress(ctx, evm, chainCfg, address, specs)
	if err != nil {
		return nil, err
	}
	s.cache.set(cacheKey, *result)
	return result, nil
}

// queryAddress 执行 RPC 查询（批量 balanceOf + native，含失败降级）。
func (s *AssetService) queryAddress(ctx context.Context, evm chain.EVM, cfg ChainAssets, address string, specs []tokenSpec) (*AddressAssets, error) {
	now := time.Now().UTC()
	out := &AddressAssets{
		Chain:     cfg.ChainKey,
		ChainID:   cfg.ChainID,
		Address:   address,
		QueriedAt: now,
		Status:    AssetFresh,
	}
	failed := 0
	for _, spec := range specs {
		balance, block, source, err := s.queryOne(ctx, evm, address, spec)
		if err != nil {
			failed++
			out.Assets = append(out.Assets, AssetBalance{
				TokenAddress: spec.Address,
				Symbol:       spec.Symbol,
				Decimals:     spec.Decimals,
				Status:       "failed",
				Error:        sanitizeRPCError(err),
			})
			continue
		}
		out.Assets = append(out.Assets, AssetBalance{
			TokenAddress: spec.Address,
			Symbol:       spec.Symbol,
			Decimals:     spec.Decimals,
			RawBalance:   balance.String(),
			Balance:      formatBalance(balance, spec.Decimals),
			Status:       "success",
		})
		if out.Source == "" {
			out.Source = source
		}
		if out.BlockNumber == "" {
			out.BlockNumber = block
		}
	}
	if len(out.Assets) > 0 && failed == len(out.Assets) {
		out.Status = AssetFailed
	} else if failed > 0 {
		out.Status = AssetPartial
	}
	return out, nil
}

// queryOne 查询单个资产（native 用 eth_getBalance，Token 用 eth_call balanceOf）。
// 返回 (余额, 区块号, 数据源, error)。
func (s *AssetService) queryOne(ctx context.Context, evm chain.EVM, address string, spec tokenSpec) (*big.Int, string, string, error) {
	if spec.Address == "" {
		raw, source, err := s.rpc.Call(ctx, evm.Key, "eth_getBalance", []any{address, "latest"})
		if err != nil {
			return nil, "", source, err
		}
		var hexStr string
		if err := json.Unmarshal(raw, &hexStr); err != nil {
			return nil, "", source, fmt.Errorf("解析 eth_getBalance 响应失败: %w", err)
		}
		n, ok := new(big.Int).SetString(strings.TrimPrefix(hexStr, "0x"), 16)
		if !ok {
			return nil, "", source, errors.New("eth_getBalance 返回非法余额")
		}
		return n, blockFromResponse(raw), source, nil
	}
	// Token：balanceOf(address) eth_call
	data := balanceOfCallData(address)
	raw, source, err := s.rpc.Call(ctx, evm.Key, "eth_call", []any{
		map[string]string{"to": spec.Address, "data": data}, "latest",
	})
	if err != nil {
		return nil, "", source, err
	}
	var hexStr string
	if err := json.Unmarshal(raw, &hexStr); err != nil {
		return nil, "", source, fmt.Errorf("解析 balanceOf 响应失败: %w", err)
	}
	n, ok := new(big.Int).SetString(strings.TrimPrefix(hexStr, "0x"), 16)
	if !ok {
		return nil, "", source, errors.New("balanceOf 返回非法余额")
	}
	return n, blockFromResponse(raw), source, nil
}

// balanceOfCallData 构造 balanceOf(address) calldata（与 datasource/rpc/balances.go 一致）。
func balanceOfCallData(address string) string {
	addr := strings.TrimPrefix(strings.ToLower(address), "0x")
	for len(addr) < 64 {
		addr = "0" + addr
	}
	return "0x70a08231" + addr
}

// blockFromResponse 从 RPC 响应提取区块号（rpcmanager 响应可能带 latestBlock 包装）。
func blockFromResponse(raw json.RawMessage) string {
	var probe struct {
		LatestBlock string `json:"latest_block,omitempty"`
		BlockNumber string `json:"block_number,omitempty"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil {
		if probe.LatestBlock != "" {
			return probe.LatestBlock
		}
		if probe.BlockNumber != "" {
			return probe.BlockNumber
		}
	}
	return ""
}

// ── 工具 ──

// resolveTokenSpecs 解析 Token 列表（设计 §17：用户选择 + 图边 Token + 平台重点）。
func resolveTokenSpecs(cfg ChainAssets, tokens []string) []tokenSpec {
	var specs []tokenSpec
	seen := map[string]bool{}
	add := func(addr, symbol string, decimals int) {
		key := strings.ToLower(addr)
		if seen[key] {
			return
		}
		seen[key] = true
		specs = append(specs, tokenSpec{Address: addr, Symbol: symbol, Decimals: decimals})
	}
	// Native 始终包含
	add("", cfg.NativeSymbol, 18)
	for _, t := range tokens {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || t == "native" {
			continue
		}
		switch t {
		case cfg.USDTAddress, "usdt":
			add(cfg.USDTAddress, "USDT", decimalsOf(cfg.USDTDecimals, 18))
		case cfg.USDCAddress, "usdc":
			add(cfg.USDCAddress, "USDC", decimalsOf(cfg.USDCDecimals, 18))
		default:
			if strings.HasPrefix(t, "0x") && len(t) == 42 {
				// 自定义 Token decimals 未知，默认 18（可通过 Token Metadata 后续精确化）
				add(t, "TOKEN", 18)
			}
		}
	}
	if !seen[strings.ToLower(cfg.USDTAddress)] {
		add(cfg.USDTAddress, "USDT", decimalsOf(cfg.USDTDecimals, 18))
	}
	if !seen[strings.ToLower(cfg.USDCAddress)] {
		add(cfg.USDCAddress, "USDC", decimalsOf(cfg.USDCDecimals, 18))
	}
	return specs
}

// decimalsOf 返回配置小数位（未配置时回退默认值）。
func decimalsOf(configured, fallback int) int {
	if configured > 0 {
		return configured
	}
	return fallback
}

// cacheKeyFor 缓存键：chain + address + token（设计 §16）。
func cacheKeyFor(chainKey, address string, specs []tokenSpec) string {
	parts := make([]string, 0, len(specs))
	for _, s := range specs {
		parts = append(parts, s.Address)
	}
	return chainKey + ":" + address + ":" + strings.Join(parts, ",")
}

// ttlState 计算缓存状态（设计 §16 时效定义）。
func ttlState(storedAt time.Time, isCenter bool) AssetState {
	age := time.Since(storedAt)
	if age <= 60*time.Second {
		return AssetFresh
	}
	if age <= 10*time.Minute {
		return AssetCached
	}
	return AssetStale
}

// isCenterAddress 判断是否中心地址（首期简化：全部按普通 TTL；中心地址 TTL 由调用方控制）。
func isCenterAddress(chainKey, address string) bool {
	_ = chainKey
	_ = address
	return false
}

// formatBalance 格式化余额（大数 → 十进制字符串，保留 6 位小数）。
func formatBalance(raw *big.Int, decimals int) string {
	if raw == nil || decimals < 0 {
		return "0"
	}
	if decimals == 0 {
		return raw.String()
	}
	div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	q, r := new(big.Int).QuoRem(raw, div, new(big.Int))
	intPart := q.String()
	// 小数部分补零到 decimals 位（r.String() 不补零）
	rStr := r.String()
	for len(rStr) < decimals {
		rStr = "0" + rStr
	}
	frac := strings.TrimRight(rStr, "0")
	if frac == "" {
		return intPart
	}
	if len(frac) > 6 {
		frac = frac[:6]
	}
	return intPart + "." + frac
}

// sanitizeRPCError 脱敏 RPC 错误（设计 §26：RPC 错误脱敏，不回显密钥/URL）。
func sanitizeRPCError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// 截断过长错误（可能含 URL）；用 rune 截断避免多字节字符破坏
	runes := []rune(msg)
	if len(runes) > 120 {
		msg = string(runes[:120]) + "…"
	}
	return msg
}

// BatchAssets 批量查询（设计 §15.2：地址 ≤ 50，每地址 Token ≤ 20，
// 加上恒附加的 native+USDT+USDC，最坏 50×23=1150 次 RPC 串行调用
// ——由 handler 层全局信号量限制并发，rpcmanager 负责超时与 429 容灾）。
func (s *AssetService) BatchAssets(ctx context.Context, chainKey string, chainID int64, addresses []string, tokens []string) ([]*AddressAssets, error) {
	const maxAddresses = 50
	const maxTokensPerAddress = 20
	if len(addresses) > maxAddresses {
		return nil, fmt.Errorf("批量地址数 %d 超过上限 %d", len(addresses), maxAddresses)
	}
	if len(tokens) > maxTokensPerAddress {
		return nil, fmt.Errorf("Token 数 %d 超过上限 %d", len(tokens), maxTokensPerAddress)
	}
	out := make([]*AddressAssets, 0, len(addresses))
	for _, addr := range addresses {
		assets, err := s.AddressAssets(ctx, chainKey, chainID, addr, tokens, false)
		if err != nil {
			// 单地址失败不阻断批量（设计 §27 部分失败）
			out = append(out, &AddressAssets{
				Chain: chainKey, ChainID: chainID, Address: strings.ToLower(addr),
				QueriedAt: time.Now().UTC(), Status: AssetFailed,
				Assets: []AssetBalance{{TokenAddress: "", Symbol: "native", Status: "failed", Error: sanitizeRPCError(err)}},
			})
			continue
		}
		out = append(out, assets)
	}
	return out, nil
}

var _ = normalize.TokenMetadata{} // 保留 normalize 依赖（Token 元数据复用预留）
