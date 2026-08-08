package smartdownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// MockProvider 确定性测试/演示 Provider（Phase 1 验收用；生产不注册）。
// 同一 (dataset, address, range) 永远返回相同记录，便于断言“完成 Range 不重跑、dup=0”。
type MockProvider struct {
	name      string
	failCount atomic.Int32 // >0 时前 N 次 ExecuteRange 返回错误（重试测试）
	failFrom  uint64       // 指定 FromBlock 起的 Range 全部失败（切换测试）
	failAll   bool         // 全部 Range 失败（Case A/C 用）
	delay     time.Duration
}

// NewMockProvider 创建 Mock Provider。
func NewMockProvider() *MockProvider {
	return &MockProvider{name: "mock"}
}

// NewFailingMockProvider 创建前 failN 次调用失败的 Mock Provider。
func NewFailingMockProvider(failN int) *MockProvider {
	p := NewMockProvider()
	p.failCount.Store(int32(failN))
	return p
}

// NewSlowMockProvider 创建每 Range 延迟指定时长的 Mock Provider（暂停/取消竞态测试用）。
func NewSlowMockProvider(delay time.Duration) *MockProvider {
	p := NewMockProvider()
	p.delay = delay
	return p
}

// SetFailFrom 配置从指定区块起的 Range 全部失败（用于 SQD→RPC 切换验收）。
func (p *MockProvider) SetFailFrom(from uint64) *MockProvider {
	p.failFrom = from
	return p
}

// SetFailAll 配置全部 Range 失败（CSV→SQD、RPC→Cloud 验收）。
func (p *MockProvider) SetFailAll() *MockProvider {
	p.failAll = true
	return p
}

func (p *MockProvider) Name() string           { return p.name }
func (p *MockProvider) Available() bool        { return true }
func (p *MockProvider) Supports(d string) bool { return ValidDataset(d) }

// Probe 确定性估算：密度 = (1 + 地址长度 % 3) / 采样块数，外推整段。
func (p *MockProvider) Probe(_ context.Context, req ProbeRequest) (ProbeResult, error) {
	sampleFrom, sampleTo := probeRange(req)
	sampleBlocks := sampleTo - sampleFrom + 1
	density := float64(1+uint64(len(req.Address))%3) / float64(sampleBlocks)
	total := req.ToBlock - req.FromBlock + 1
	rows := uint64(density*float64(total)) + 1
	return ProbeResult{
		EstimatedRows:  rows,
		EstimatedBytes: rows * 128,
		FirstBlock:     req.FromBlock,
		LastBlock:      req.ToBlock,
		Confidence:     0.9,
	}, nil
}

// ExecuteRange 确定性生成记录：每块 0~2 条 Transfer 类记录。
func (p *MockProvider) ExecuteRange(_ context.Context, req RangeRequest) (*ProviderResult, error) {
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	if p.failAll || (p.failFrom > 0 && req.FromBlock >= p.failFrom) {
		return nil, fmt.Errorf("mock provider 注入失败（FromBlock %d >= %d）", req.FromBlock, p.failFrom)
	}
	if n := p.failCount.Load(); n > 0 {
		p.failCount.Add(-1)
		return nil, fmt.Errorf("mock provider 注入失败（剩余 %d）", n-1)
	}
	var records []Record
	span := req.ToBlock - req.FromBlock + 1
	if span > 300 {
		span = 300
	}
	for i := uint64(0); i < span; i++ {
		block := req.FromBlock + i
		n := (block + uint64(len(req.Address))) % 3
		for j := uint64(0); j < n; j++ {
			txHash := mockTxHash(req.Dataset, req.Address, block, j)
			records = append(records, Record{
				ChainID:         req.ChainID,
				BlockNumber:     block,
				TransactionHash: txHash,
				LogIndex:        j,
				Dataset:         req.Dataset,
				Address:         req.Address,
				Payload: map[string]any{
					"from_address": req.Address,
					"to_address":   "0x" + txHash[len(txHash)-40:],
					"value_raw":    strconv.FormatUint(block*1000+j, 10),
				},
			})
		}
	}
	bytes := uint64(0)
	for i := range records {
		bytes += uint64(len(records[i].TransactionHash) + 64)
	}
	return &ProviderResult{Records: records, Bytes: bytes, CompletedTo: req.ToBlock}, nil
}

func mockTxHash(dataset, address string, block, j uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d", dataset, strings.ToLower(address), block, j)))
	return "0x" + hex.EncodeToString(sum[:])[:64]
}
