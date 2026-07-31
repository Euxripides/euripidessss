package downloadengine

import (
	"context"
	"sync"
	"time"
)

// ── Provider Router ──

type Router struct {
	mu              sync.RWMutex
	streaming       []StreamingProvider
	object          []ObjectProvider
	lookup          []LookupProvider
	capabilityCache map[string]ProviderCapabilities
	healthCache     map[string]ProviderHealth
	cacheExpiry     time.Time
}

func NewRouter() *Router {
	return &Router{
		capabilityCache: make(map[string]ProviderCapabilities),
		healthCache:     make(map[string]ProviderHealth),
	}
}

func (r *Router) RegisterStreaming(p StreamingProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streaming = append(r.streaming, p)
	r.capabilityCache[p.Name()] = p.Capabilities()
}

func (r *Router) RegisterObject(p ObjectProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.object = append(r.object, p)
	r.capabilityCache[p.Name()] = p.Capabilities()
}

func (r *Router) RegisterLookup(p LookupProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookup = append(r.lookup, p)
	r.capabilityCache[p.Name()] = p.Capabilities()
}

// ResolveCapabilities 返回所有已注册 Provider 的能力缓存
func (r *Router) ResolveCapabilities() map[string]ProviderCapabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make(map[string]ProviderCapabilities, len(r.capabilityCache))
	for k, v := range r.capabilityCache {
		cp[k] = v
	}
	return cp
}

// ResolveStreaming 找到支持指定数据集类型的 StreamingProvider
func (r *Router) ResolveStreaming(datasetType, chainID string) (StreamingProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.streaming {
		caps := p.Capabilities()
		if r.isHealthy(p.Name()) && r.supportsDataset(caps, datasetType) {
			return p, true
		}
	}
	return nil, false
}

// ResolveObject 找到支持指定数据集类型的 ObjectProvider
func (r *Router) ResolveObject(datasetType, chainID string) (ObjectProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.object {
		caps := p.Capabilities()
		if r.isHealthy(p.Name()) && r.supportsDataset(caps, datasetType) {
			return p, true
		}
	}
	return nil, false
}

// ResolveLookup 找到任意可用的 LookupProvider
func (r *Router) ResolveLookup() (LookupProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.lookup {
		if r.isHealthy(p.Name()) {
			return p, true
		}
	}
	return nil, false
}

// UpdateHealth 批量更新健康状态缓存
func (r *Router) UpdateHealth(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.healthCache = make(map[string]ProviderHealth)
	for _, p := range r.streaming {
		r.healthCache[p.Name()] = p.Health(ctx)
	}
	for _, p := range r.object {
		r.healthCache[p.Name()] = p.Health(ctx)
	}
	for _, p := range r.lookup {
		r.healthCache[p.Name()] = p.Health(ctx)
	}
	r.cacheExpiry = time.Now().Add(30 * time.Second)
}

func (r *Router) isHealthy(name string) bool {
	h, ok := r.healthCache[name]
	if !ok {
		return true // 未知 = 乐观
	}
	return h.Status == ProviderHealthy || h.Status == ProviderDegraded
}

func (r *Router) supportsDataset(caps ProviderCapabilities, datasetType string) bool {
	for _, dt := range caps.DatasetTypes {
		if dt == datasetType {
			return true
		}
	}
	return false
}
