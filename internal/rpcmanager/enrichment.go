package rpcmanager

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
)

const (
	cacheAddress = "ADDRESS_STATE"
	cacheToken   = "TOKEN_METADATA"
)

func (m *Manager) Address(ctx context.Context, chainKey, address string, force bool) (AddressEnrichment, error) {
	network, err := chain.Resolve(chainKey)
	if err != nil {
		return AddressEnrichment{}, err
	}
	address, err = normalizeAddress(address)
	if err != nil {
		return AddressEnrichment{}, err
	}
	var cached AddressEnrichment
	if !force {
		hit, cacheErr := m.store.cacheGet(network.Key, cacheAddress, address, &cached)
		if cacheErr != nil {
			return AddressEnrichment{}, cacheErr
		}
		if hit {
			m.cacheHits.Add(1)
			cached.Cached = true
			return cached, nil
		}
	}
	m.cacheMisses.Add(1)
	codeRaw, _, err := m.Call(ctx, network.Key, "eth_getCode", []any{address, "latest"})
	if err != nil {
		return AddressEnrichment{}, err
	}
	balanceRaw, _, err := m.Call(ctx, network.Key, "eth_getBalance", []any{address, "latest"})
	if err != nil {
		return AddressEnrichment{}, err
	}
	var code, balanceHex string
	if json.Unmarshal(codeRaw, &code) != nil || json.Unmarshal(balanceRaw, &balanceHex) != nil {
		return AddressEnrichment{}, errors.New("RPC 地址富化返回值无法解析")
	}
	balance, ok := new(big.Int).SetString(strings.TrimPrefix(balanceHex, "0x"), 16)
	if !ok {
		return AddressEnrichment{}, errors.New("RPC 余额返回值无法解析")
	}
	addressType, reason := "EOA", "eth_getCode 返回空字节码"
	if code != "" && code != "0x" && code != "0x0" {
		addressType, reason = "CONTRACT", "eth_getCode 返回合约字节码"
	}
	result := AddressEnrichment{
		ChainKey: network.Key, ChainID: network.ID, Address: address,
		AddressType: addressType, NativeBalanceRaw: balance.String(),
		NativeBalance: formatUnits(balance, 18), NativeSymbol: network.NativeSymbol,
		Status: "DETECTED", Reason: reason, CheckedAt: time.Now().UTC(),
	}
	_ = m.store.cachePut(network.Key, network.ID, cacheAddress, address, result.Status, result, 2*time.Minute)
	return result, nil
}

func (m *Manager) Token(ctx context.Context, chainKey, tokenAddress string, force bool) (TokenMetadata, error) {
	network, err := chain.Resolve(chainKey)
	if err != nil {
		return TokenMetadata{}, err
	}
	tokenAddress, err = normalizeAddress(tokenAddress)
	if err != nil {
		return TokenMetadata{}, err
	}
	var cached TokenMetadata
	if !force {
		hit, cacheErr := m.store.cacheGet(network.Key, cacheToken, tokenAddress, &cached)
		if cacheErr != nil {
			return TokenMetadata{}, cacheErr
		}
		if hit {
			m.cacheHits.Add(1)
			cached.Cached = true
			return cached, nil
		}
	}
	m.cacheMisses.Add(1)
	result := TokenMetadata{
		ChainKey: network.Key, ChainID: network.ID, TokenAddress: tokenAddress,
		Status: "PARTIAL", UpdatedAt: time.Now().UTC(),
	}
	var successes int
	for selector, apply := range map[string]func(json.RawMessage) bool{
		"0x06fdde03": func(raw json.RawMessage) bool { result.Name = decodeABIString(raw); return result.Name != "" },
		"0x95d89b41": func(raw json.RawMessage) bool { result.Symbol = decodeABIString(raw); return result.Symbol != "" },
		"0x313ce567": func(raw json.RawMessage) bool {
			value, ok := decodeABIUint(raw)
			if !ok || !value.IsUint64() || value.Uint64() > 255 {
				return false
			}
			decimals := uint8(value.Uint64())
			result.Decimals = &decimals
			return true
		},
		"0x18160ddd": func(raw json.RawMessage) bool {
			value, ok := decodeABIUint(raw)
			if ok {
				result.TotalSupply = value.String()
			}
			return ok
		},
	} {
		raw, _, callErr := m.Call(ctx, network.Key, "eth_call", []any{map[string]string{"to": tokenAddress, "data": selector}, "latest"})
		if callErr == nil && apply(raw) {
			successes++
		}
	}
	if successes == 0 {
		result.Status = "UNKNOWN"
	} else if successes == 4 {
		result.Status = "COMPLETE"
	}
	_ = m.store.cachePut(network.Key, network.ID, cacheToken, tokenAddress, result.Status, result, 24*time.Hour)
	return result, nil
}

func (m *Manager) StartJob(request JobRequest) (Job, error) {
	network, err := chain.Resolve(request.ChainKey)
	if err != nil {
		return Job{}, err
	}
	request.JobType = strings.ToUpper(strings.TrimSpace(request.JobType))
	if request.JobType != cacheAddress && request.JobType != cacheToken {
		return Job{}, errors.New("仅支持 ADDRESS_STATE 或 TOKEN_METADATA 富化任务")
	}
	if len(request.Items) == 0 || len(request.Items) > 10000 {
		return Job{}, errors.New("任务条目数必须在 1 到 10000 之间")
	}
	now := time.Now().UTC()
	job := Job{
		ID: newID("enrich"), JobType: request.JobType, ChainKey: network.Key, ChainID: network.ID,
		Status: "QUEUED", TotalItems: int64(len(request.Items)), StartedAt: now, UpdatedAt: now,
	}
	if err := m.store.saveJob(job); err != nil {
		return Job{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.jobMu.Lock()
	m.jobCancel[job.ID] = cancel
	m.jobMu.Unlock()
	m.jobWG.Add(1)
	go m.runJob(ctx, job, request.Items)
	return job, nil
}

func (m *Manager) Job(id string) (Job, error) { return m.store.job(id) }

func (m *Manager) Jobs() ([]Job, error) { return m.store.jobs() }

func (m *Manager) CancelJob(id string) (Job, error) {
	job, err := m.store.job(id)
	if err != nil {
		return Job{}, err
	}
	if job.Status == "COMPLETED" || job.Status == "FAILED" || job.Status == "CANCELED" {
		return job, nil
	}
	job.CancellationRequested = true
	job.UpdatedAt = time.Now().UTC()
	_ = m.store.saveJob(job)
	m.jobMu.Lock()
	cancel := m.jobCancel[id]
	m.jobMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return m.store.job(id)
}

func (m *Manager) runJob(ctx context.Context, job Job, items []string) {
	defer m.jobWG.Done()
	job.Status, job.UpdatedAt = "RUNNING", time.Now().UTC()
	_ = m.store.saveJob(job)
	defer func() {
		m.jobMu.Lock()
		delete(m.jobCancel, job.ID)
		m.jobMu.Unlock()
	}()
	for _, item := range items {
		select {
		case <-ctx.Done():
			now := time.Now().UTC()
			job.Status, job.CancellationRequested, job.FinishedAt = "CANCELED", true, &now
			job.UpdatedAt = now
			_ = m.store.saveJob(job)
			return
		default:
		}
		var cached bool
		var err error
		if job.JobType == cacheAddress {
			var result AddressEnrichment
			result, err = m.Address(ctx, job.ChainKey, item, false)
			cached = result.Cached
		} else {
			var result TokenMetadata
			result, err = m.Token(ctx, job.ChainKey, item, false)
			cached = result.Cached
		}
		job.CompletedItems++
		if err != nil {
			job.FailedItems++
			job.ErrorSummary = appendJobError(job.ErrorSummary, err.Error())
		} else {
			job.SucceededItems++
			if cached {
				job.CacheHits++
			}
		}
		job.UpdatedAt = time.Now().UTC()
		_ = m.store.saveJob(job)
	}
	now := time.Now().UTC()
	job.Status, job.FinishedAt, job.UpdatedAt = "COMPLETED", &now, now
	if job.SucceededItems == 0 && job.FailedItems > 0 {
		job.Status = "FAILED"
	}
	_ = m.store.saveJob(job)
}

func normalizeAddress(address string) (string, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	if len(address) != 42 || !strings.HasPrefix(address, "0x") {
		return "", errors.New("EVM 地址必须是 0x 开头的 40 位十六进制")
	}
	if _, err := hex.DecodeString(address[2:]); err != nil {
		return "", errors.New("EVM 地址包含非法十六进制字符")
	}
	return address, nil
}

func decodeABIUint(raw json.RawMessage) (*big.Int, bool) {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return nil, false
	}
	value, ok := new(big.Int).SetString(strings.TrimPrefix(encoded, "0x"), 16)
	return value, ok
}

func decodeABIString(raw json.RawMessage) string {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return ""
	}
	data, err := hex.DecodeString(strings.TrimPrefix(encoded, "0x"))
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) >= 64 {
		offset := new(big.Int).SetBytes(data[:32])
		if offset.IsUint64() && offset.Uint64()+32 <= uint64(len(data)) {
			start := int(offset.Uint64())
			length := new(big.Int).SetBytes(data[start : start+32])
			if length.IsUint64() && start+32+int(length.Uint64()) <= len(data) {
				return strings.TrimSpace(string(data[start+32 : start+32+int(length.Uint64())]))
			}
		}
	}
	if len(data) >= 32 {
		return strings.TrimSpace(strings.TrimRight(string(data[:32]), "\x00"))
	}
	return ""
}

func formatUnits(value *big.Int, decimals int) string {
	if value == nil {
		return "0"
	}
	digits := value.String()
	if decimals == 0 {
		return digits
	}
	if len(digits) <= decimals {
		digits = strings.Repeat("0", decimals-len(digits)+1) + digits
	}
	point := len(digits) - decimals
	return strings.TrimRight(strings.TrimRight(digits[:point]+"."+digits[point:], "0"), ".")
}

func appendJobError(existing, message string) string {
	message = redactMessage(message)
	if existing == "" {
		return message
	}
	if len(existing) > 800 {
		return existing
	}
	return fmt.Sprintf("%s；%s", existing, message)
}
