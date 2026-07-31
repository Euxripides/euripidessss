package downloadengine

import (
	"hash/fnv"
	"sync"
	"time"
)

// ── V2.1 RC2 地址覆盖索引 ──

type AddressStatus string

const (
	AddrNew        AddressStatus = "NEW"
	AddrScheduled  AddressStatus = "SCHEDULED"
	AddrDownloading AddressStatus = "DOWNLOADING"
	AddrReady      AddressStatus = "READY"
	AddrFailed     AddressStatus = "FAILED"
)

type AddressCoverageRecord struct {
	Address     string        `json:"address"`
	Chain       string        `json:"chain"`
	DatasetType DatasetType   `json:"dataset_type"`
	StartBlock  uint64        `json:"start_block"`
	EndBlock    uint64        `json:"end_block"`
	Status      AddressStatus `json:"status"`
	DatasetID   string        `json:"dataset_id"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// ── Bloom Filter ──

type BloomFilter struct {
	bits []uint64
	k    int // hash functions
}

func NewBloomFilter(expectedItems int, falsePositiveRate float64) *BloomFilter {
	// 简单实现: 1M bit ≈ 125KB, 3 hash functions
	bits := 1_000_000 / 64
	if bits < 1024 {
		bits = 1024
	}
	return &BloomFilter{bits: make([]uint64, bits), k: 3}
}

func (bf *BloomFilter) Add(s string) {
	for i := 0; i < bf.k; i++ {
		h := bf.hash(s, i)
		idx := h % uint64(len(bf.bits)*64)
		bf.bits[idx/64] |= 1 << (idx % 64)
	}
}

func (bf *BloomFilter) MightContain(s string) bool {
	for i := 0; i < bf.k; i++ {
		h := bf.hash(s, i)
		idx := h % uint64(len(bf.bits)*64)
		if bf.bits[idx/64]&(1<<(idx%64)) == 0 {
			return false
		}
	}
	return true
}

func (bf *BloomFilter) hash(s string, seed int) uint64 {
	h := fnv.New64a()
	h.Write([]byte{byte(seed)})
	h.Write([]byte(s))
	return h.Sum64()
}

// ── Address Coverage Index ──

type AddressCoverageIndex struct {
	mu      sync.RWMutex
	records map[string]*AddressCoverageRecord // key: chain:address:type
	bloom   *BloomFilter

	// Metrics
	Total     int64
	CacheHit  int64
	CacheMiss int64
	ReadyTotal int64
}

func NewAddressCoverageIndex() *AddressCoverageIndex {
	return &AddressCoverageIndex{
		records: make(map[string]*AddressCoverageRecord),
		bloom:   NewBloomFilter(500_000, 0.01),
	}
}

func (aci *AddressCoverageIndex) key(chain, address string, dsType DatasetType) string {
	return chain + ":" + address + ":" + string(dsType)
}

func (aci *AddressCoverageIndex) Mark(rec *AddressCoverageRecord) {
	aci.mu.Lock()
	defer aci.mu.Unlock()
	key := aci.key(rec.Chain, rec.Address, rec.DatasetType)
	if _, exists := aci.records[key]; !exists {
		aci.Total++
	}
	rec.UpdatedAt = time.Now().UTC()
	aci.records[key] = rec
	aci.bloom.Add(key)
	if rec.Status == AddrReady {
		aci.ReadyTotal++
	}
}

func (aci *AddressCoverageIndex) Check(chain, address string, dsType DatasetType) (AddressStatus, bool) {
	key := aci.key(chain, address, dsType)

	// Bloom 快速预查
	if !aci.bloom.MightContain(key) {
		aci.mu.Lock()
		aci.CacheMiss++
		aci.mu.Unlock()
		return AddrNew, false
	}

	aci.mu.RLock()
	defer aci.mu.RUnlock()
	if rec, ok := aci.records[key]; ok {
		aci.CacheHit++
		return rec.Status, true
	}
	aci.CacheMiss++
	return AddrNew, false
}

// BatchCheck 批量查询覆盖状态 — 500K地址高效处理
func (aci *AddressCoverageIndex) BatchCheck(chain string, addresses []string, dsType DatasetType) (ready, missing []string) {
	for _, addr := range addresses {
		status, found := aci.Check(chain, addr, dsType)
		if found && status == AddrReady {
			ready = append(ready, addr)
		} else {
			missing = append(missing, addr)
		}
	}
	return
}

// IncrementalTask 增量任务生成 — 输入地址列表 → 去掉已覆盖 → 返回缺失地址
func (aci *AddressCoverageIndex) IncrementalTask(chain string, addresses []string, dsType DatasetType) struct {
	Total    int
	Ready    int
	Missing  int
	Addrs    []string
} {
	_, missing := aci.BatchCheck(chain, addresses, dsType)
	return struct {
		Total   int
		Ready   int
		Missing int
		Addrs   []string
	}{
		Total:   len(addresses),
		Ready:   len(addresses) - len(missing),
		Missing: len(missing),
		Addrs:   missing,
	}
}

// FailAndRequeue 失败地址重新进入队列
func (aci *AddressCoverageIndex) FailAndRequeue(chain string, addresses []string, dsType DatasetType) int {
	aci.mu.Lock()
	defer aci.mu.Unlock()
	count := 0
	for _, addr := range addresses {
		key := aci.key(chain, addr, dsType)
		if rec, ok := aci.records[key]; ok && rec.Status == AddrDownloading {
			rec.Status = AddrFailed
			count++
		}
	}
	return count
}

// DuckDB 持久化 DDL
func AddressCoverageDDL() string {
	return `CREATE TABLE IF NOT EXISTS address_coverage (
		address VARCHAR NOT NULL,
		chain VARCHAR NOT NULL,
		dataset_type VARCHAR NOT NULL,
		start_block BIGINT,
		end_block BIGINT,
		status VARCHAR NOT NULL DEFAULT 'NEW',
		dataset_id VARCHAR,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chain, address, dataset_type)
	)`
}

// Metrics
func (aci *AddressCoverageIndex) Snapshot() map[string]any {
	aci.mu.RLock()
	defer aci.mu.RUnlock()
	return map[string]any{
		"address_coverage_total":   aci.Total,
		"address_cache_hit":        aci.CacheHit,
		"address_cache_miss":       aci.CacheMiss,
		"address_download_required": aci.Total - aci.ReadyTotal,
		"address_ready_total":      aci.ReadyTotal,
	}
}

// ── 多数据类型覆盖检查 ──

type MultiTypeCoverage struct {
	Address      string
	Transactions AddressStatus
	Logs         AddressStatus
	Transfers    AddressStatus
	Traces       AddressStatus
}

func (aci *AddressCoverageIndex) MultiTypeCheck(chain, address string) MultiTypeCoverage {
	return MultiTypeCoverage{
		Address:      address,
		Transactions: aci.statusOr(chain, address, DSTransactions),
		Logs:         aci.statusOr(chain, address, DSLogs),
		Transfers:    aci.statusOr(chain, address, DSTransfers),
		Traces:       aci.statusOr(chain, address, DSTraces),
	}
}

func (aci *AddressCoverageIndex) statusOr(chain, address string, dsType DatasetType) AddressStatus {
	s, ok := aci.Check(chain, address, dsType)
	if !ok {
		return AddrNew
	}
	return s
}
