package graphcache

import "context"

// Cache 是图扩展缓存的服务门面：先查缓存，未命中再聚合写入。
type Cache struct {
	store   *Store
	builder *Builder
}

// NewCache 创建缓存门面。
func NewCache(store *Store, builder *Builder) *Cache {
	return &Cache{store: store, builder: builder}
}

// GetOrBuild 命中返回缓存结果（hit=true）；未命中构建并写入。
func (c *Cache) GetOrBuild(ctx context.Context, key Key) (*Result, bool, error) {
	key = key.Normalized()
	if e := c.store.Get(key); e != nil {
		res := e.Result
		res.Source = "cache-hit"
		return &res, true, nil
	}
	res, err := c.builder.Build(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if err := c.store.Put(key, *res, c.store.TTLFor(key)); err != nil {
		return nil, false, err
	}
	return res, false, nil
}

// Store 返回底层存储（失效/统计用）。
func (c *Cache) Store() *Store {
	return c.store
}

