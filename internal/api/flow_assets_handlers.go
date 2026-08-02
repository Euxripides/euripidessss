package api

import (
	"strings"
	"time"

	"github.com/etl/backend/internal/flow"
	"github.com/gin-gonic/gin"
)

// ── 实时资产 API（V2.0 设计 §15）──
//
// POST /api/flow/address-assets          单地址（含 force_refresh）
// POST /api/flow/address-assets/batch    批量（≤50 地址，≤20 Token/地址）
// POST /api/flow/address-assets/refresh  刷新（force_refresh=true 单地址语义）
//
// 限流/安全（设计 §26）：地址与 Token 格式校验、批量上限、RPC 错误脱敏、
// 旧请求 context cancel、全局并发上限。

// assetQuerySemaphore 是全局资产查询并发信号量（设计 §14：全局余额并发 8，
// 防多并发 batch 请求叠加 RPC 洪泛耗尽共享 provider 配额）。
var assetQuerySemaphore = make(chan struct{}, 8)

// acquireAssetSlot 获取并发槽（信号量满时立即返回 429，context 取消也返回）。
func acquireAssetSlot(c *gin.Context) bool {
	select {
	case assetQuerySemaphore <- struct{}{}:
		return true
	default:
		c.JSON(429, gin.H{"detail": "资产查询并发已满，请稍后重试", "retry_after": "1"})
		return false
	case <-c.Request.Context().Done():
		c.JSON(429, gin.H{"detail": "资产查询已取消", "retry_after": "1"})
		return false
	}
}

// releaseAssetSlot 释放并发槽。
func releaseAssetSlot() {
	<-assetQuerySemaphore
}

type addressAssetsRequest struct {
	Chain        string   `json:"chain"`
	ChainID      int64    `json:"chain_id"`
	Address      string   `json:"address"`
	Tokens       []string `json:"tokens,omitempty"`
	ForceRefresh bool     `json:"force_refresh,omitempty"`
}

// validChainKey 校验链 key 白名单（LOW 修复：防快照目录嵌套子目录噪音）。
func validChainKey(chain string) bool {
	switch strings.ToLower(chain) {
	case "bsc", "eth":
		return true
	}
	return false
}

// handleAddressAssets 单地址资产查询。
func handleAddressAssets(c *gin.Context) {
	svc := flowService()
	if svc == nil || !svc.Available() {
		c.JSON(503, gin.H{"detail": "实时资产服务不可用（RPC 未配置）"})
		return
	}
	var req addressAssetsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"detail": "请求格式错误: " + err.Error()})
		return
	}
	if !validEVMAddressStrict(req.Address) {
		c.JSON(400, gin.H{"detail": "address 不是合法的 EVM 地址"})
		return
	}
	if req.Chain == "" {
		req.Chain = "bsc"
	}
	if !validChainKey(req.Chain) {
		c.JSON(400, gin.H{"detail": "chain 不受支持（仅 bsc/eth）"})
		return
	}
	for _, t := range req.Tokens {
		if t != "" && t != "native" && !strings.EqualFold(t, "usdt") && !strings.EqualFold(t, "usdc") &&
			!validEVMAddressStrict(t) {
			c.JSON(400, gin.H{"detail": "token 不是合法地址或已知符号"})
			return
		}
	}
	// MEDIUM 修复：单地址/refresh 端点 tokens 数量上限（与批量一致 ≤20，防 RPC 放大）
	if len(req.Tokens) > 20 {
		c.JSON(400, gin.H{"detail": "Token 数超过上限 20"})
		return
	}
	// MEDIUM 修复：全局并发上限（设计 §14，防多请求叠加 RPC 洪泛）
	if !acquireAssetSlot(c) {
		return
	}
	defer releaseAssetSlot()
	assets, err := svc.AddressAssets(c.Request.Context(), req.Chain, req.ChainID, req.Address, req.Tokens, req.ForceRefresh)
	if err != nil {
		c.JSON(503, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(200, assets)
}

// handleAddressAssetsRefresh 刷新余额（设计 §15.3：仅当前中心/可见节点，
// nit 修复：强制 force_refresh=true，不复用单地址 handler 的默认 false）。
func handleAddressAssetsRefresh(c *gin.Context) {
	svc := flowService()
	if svc == nil || !svc.Available() {
		c.JSON(503, gin.H{"detail": "实时资产服务不可用（RPC 未配置）"})
		return
	}
	var req addressAssetsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"detail": "请求格式错误: " + err.Error()})
		return
	}
	if !validEVMAddressStrict(req.Address) {
		c.JSON(400, gin.H{"detail": "address 不是合法的 EVM 地址"})
		return
	}
	if req.Chain == "" {
		req.Chain = "bsc"
	}
	if !validChainKey(req.Chain) {
		c.JSON(400, gin.H{"detail": "chain 不受支持（仅 bsc/eth）"})
		return
	}
	// 低危修复：refresh 端点同样校验 tokens（与单地址/批量端点一致）
	for _, t := range req.Tokens {
		if t != "" && t != "native" && !strings.EqualFold(t, "usdt") && !strings.EqualFold(t, "usdc") &&
			!validEVMAddressStrict(t) {
			c.JSON(400, gin.H{"detail": "token 不是合法地址或已知符号"})
			return
		}
	}
	// MEDIUM 修复：refresh 端点 tokens 数量上限（与单地址/批量一致，防 RPC 放大）
	if len(req.Tokens) > 20 {
		c.JSON(400, gin.H{"detail": "Token 数超过上限 20"})
		return
	}
	// MEDIUM 修复：全局并发上限（设计 §14）
	if !acquireAssetSlot(c) {
		return
	}
	defer releaseAssetSlot()
	assets, err := svc.AddressAssets(c.Request.Context(), req.Chain, req.ChainID, req.Address, req.Tokens, true)
	if err != nil {
		c.JSON(503, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(200, assets)
}

// handleAddressAssetsBatch 批量资产查询（设计 §15.2）。
func handleAddressAssetsBatch(c *gin.Context) {
	svc := flowService()
	if svc == nil || !svc.Available() {
		c.JSON(503, gin.H{"detail": "实时资产服务不可用（RPC 未配置）"})
		return
	}
	var req struct {
		Chain     string   `json:"chain"`
		ChainID   int64    `json:"chain_id"`
		Addresses []string `json:"addresses"`
		Tokens    []string `json:"tokens,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"detail": "请求格式错误: " + err.Error()})
		return
	}
	const maxAddresses = 50
	if len(req.Addresses) == 0 {
		c.JSON(400, gin.H{"detail": "addresses 不能为空"})
		return
	}
	if len(req.Addresses) > maxAddresses {
		c.JSON(400, gin.H{"detail": "批量地址数超过上限 50"})
		return
	}
	if req.Chain == "" {
		req.Chain = "bsc"
	}
	if !validChainKey(req.Chain) {
		c.JSON(400, gin.H{"detail": "chain 不受支持（仅 bsc/eth）"})
		return
	}
	for _, addr := range req.Addresses {
		if !validEVMAddressStrict(addr) {
			c.JSON(400, gin.H{"detail": "含非法 EVM 地址: " + addr})
			return
		}
	}
	// nit 修复：批量端点同样校验 tokens（与单地址一致，非法值拒绝而非静默忽略）
	for _, t := range req.Tokens {
		if t != "" && t != "native" && !strings.EqualFold(t, "usdt") && !strings.EqualFold(t, "usdc") &&
			!validEVMAddressStrict(t) {
			c.JSON(400, gin.H{"detail": "token 不是合法地址或已知符号"})
			return
		}
	}
	// LOW 修复：batch handler 层补齐 tokens ≤20（服务层兜底已生效，此处保持纵深一致）
	if len(req.Tokens) > 20 {
		c.JSON(400, gin.H{"detail": "Token 数超过上限 20"})
		return
	}
	// MEDIUM 修复：全局并发上限（设计 §14，batch 最坏 50×23=1150 次 RPC，必须限流）
	if !acquireAssetSlot(c) {
		return
	}
	defer releaseAssetSlot()
	assets, err := svc.BatchAssets(c.Request.Context(), req.Chain, req.ChainID, req.Addresses, req.Tokens)
	if err != nil {
		c.JSON(400, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(200, gin.H{
		"chain":    req.Chain,
		"chain_id": req.ChainID,
		"total":    len(assets),
		"assets":   assets,
	})
}

// flowAssetsService 是全局实时资产服务（装配时初始化，handlers.go 赋值）。
var flowAssetsService *flow.AssetService

// balanceSnapshotStore 是全局余额快照存储（装配时初始化）。
var balanceSnapshotStore *flow.BalanceSnapshotStore

// handleBalanceSnapshotSave 保存余额快照（设计 §8）。
func handleBalanceSnapshotSave(c *gin.Context) {
	svc := flowService()
	if svc == nil || !svc.Available() {
		c.JSON(503, gin.H{"detail": "实时资产服务不可用（RPC 未配置）"})
		return
	}
	if balanceSnapshotStore == nil {
		c.JSON(503, gin.H{"detail": "快照存储未初始化"})
		return
	}
	var req addressAssetsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"detail": "请求格式错误: " + err.Error()})
		return
	}
	if !validEVMAddressStrict(req.Address) {
		c.JSON(400, gin.H{"detail": "address 不是合法的 EVM 地址"})
		return
	}
	if req.Chain == "" {
		req.Chain = "bsc"
	}
	if !validChainKey(req.Chain) {
		c.JSON(400, gin.H{"detail": "chain 不受支持（仅 bsc/eth）"})
		return
	}
	// LOW 修复：snapshot 端点校验 tokens（与 refresh 一致，非法值拒绝而非静默忽略）
	for _, t := range req.Tokens {
		if t != "" && t != "native" && !strings.EqualFold(t, "usdt") && !strings.EqualFold(t, "usdc") &&
			!validEVMAddressStrict(t) {
			c.JSON(400, gin.H{"detail": "token 不是合法地址或已知符号"})
			return
		}
	}
	if len(req.Tokens) > 20 {
		c.JSON(400, gin.H{"detail": "Token 数超过上限 20"})
		return
	}
	// MEDIUM 修复：snapshot 端点同样接入全局并发信号量（其调 AddressAssets
	// force_refresh=true 发起 RPC，必须与单地址/batch/refresh 一致限流防绕过）
	if !acquireAssetSlot(c) {
		return
	}
	defer releaseAssetSlot()
	// 查询实时资产（force_refresh 保证快照为最新）
	assets, err := svc.AddressAssets(c.Request.Context(), req.Chain, req.ChainID, req.Address, req.Tokens, true)
	if err != nil {
		c.JSON(503, gin.H{"detail": err.Error()})
		return
	}
	snap := flow.BalanceSnapshot{
		Chain:       assets.Chain,
		ChainID:     assets.ChainID,
		Address:     assets.Address,
		BlockNumber: assets.BlockNumber,
		CapturedAt:  time.Now().UTC(),
		Source:      assets.Source,
		Assets:      assets.Assets,
	}
	// 历史对比在保存前执行（review blocking 修复：先取旧快照对比，
	// 再保存新快照——若先 Save 则 Latest 返回刚保存的快照，diff 恒为 0）
	diffs := balanceSnapshotStore.Compare(assets.Chain, assets.Address, assets)
	key, err := balanceSnapshotStore.Save(snap)
	if err != nil {
		c.JSON(500, gin.H{"detail": "保存快照失败: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{
		"snapshot_key": key,
		"snapshot":     snap,
		"diff":         diffs,
	})
}

// handleBalanceSnapshotCompare 返回地址快照历史（设计 §8）。
func handleBalanceSnapshotCompare(c *gin.Context) {
	if balanceSnapshotStore == nil {
		c.JSON(503, gin.H{"detail": "快照存储未初始化"})
		return
	}
	address := strings.ToLower(c.Query("address"))
	if !validEVMAddressStrict(address) {
		c.JSON(400, gin.H{"detail": "address 不是合法的 EVM 地址"})
		return
	}
	chain := c.Query("chain")
	if chain == "" {
		chain = "bsc"
	}
	// INFO 修复：compare 端点 chain 过白名单（与写路径对齐，防无意义 key 前缀枚举）
	if !validChainKey(chain) {
		c.JSON(400, gin.H{"detail": "chain 不受支持（仅 bsc/eth）"})
		return
	}
	snaps := balanceSnapshotStore.List(chain, address)
	if snaps == nil {
		snaps = []flow.BalanceSnapshot{}
	}
	c.JSON(200, gin.H{"address": address, "chain": chain, "total": len(snaps), "snapshots": snaps})
}

// flowService 返回全局实时资产服务（装配时初始化）。
func flowService() *flow.AssetService {
	return flowAssetsService
}

// validEVMAddressStrict 校验 EVM 地址（0x + 40 hex）。
func validEVMAddressStrict(addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if len(addr) != 42 || !strings.HasPrefix(addr, "0x") {
		return false
	}
	for _, c := range addr[2:] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
