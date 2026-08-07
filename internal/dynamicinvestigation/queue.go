package dynamicinvestigation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── 地址发现队列 ──

// Queue 是地址发现队列：管理状态迁移与持久化。
type Queue struct {
	mu       sync.RWMutex
	items    map[string]*DiscoveredAddress // address → item
	order    []string                      // 按发现顺序
	storeDir string                        // JSON 持久化目录（可选）
	dirty    bool
}

// NewQueue 创建队列。storeDir 为空则仅内存。
func NewQueue(storeDir string) *Queue {
	q := &Queue{
		items:    make(map[string]*DiscoveredAddress),
		storeDir: storeDir,
	}
	if storeDir != "" {
		q.load()
	}
	return q
}

// Add 添加新发现的地址（幂等：已存在则更新关联信息，不覆盖状态）。
func (q *Queue) Add(address, source, amount, token string, depth int) (*DiscoveredAddress, bool) {
	address = normalizeAddress(address)
	q.mu.Lock()
	defer q.mu.Unlock()
	if existing, ok := q.items[address]; ok {
		// 已存在：保留状态，仅补充来源信息
		if existing.Amount == "" && amount != "" {
			existing.Amount = amount
		}
		if existing.Token == "" && token != "" {
			existing.Token = token
		}
		if depth < existing.Depth {
			existing.Depth = depth
		}
		existing.UpdatedAt = time.Now().UTC()
		q.dirty = true
		return existing, false
	}
	now := time.Now().UTC()
	item := &DiscoveredAddress{
		Address:      address,
		Source:       source,
		Amount:       amount,
		Token:        token,
		Depth:        depth,
		Status:       StatusDiscovered,
		DataLevel:    LevelDiscover,
		DiscoveredAt: now,
		UpdatedAt:    now,
	}
	q.items[address] = item
	q.order = append(q.order, address)
	q.dirty = true
	return item, true
}

// Transition 迁移地址状态，非法迁移返回错误。
func (q *Queue) Transition(address string, target DiscoveryStatus) error {
	address = normalizeAddress(address)
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[address]
	if !ok {
		return fmt.Errorf("地址 %s 不在队列中", address)
	}
	if item.Status == target {
		return nil // 幂等
	}
	allowed, ok := ValidTransitions[item.Status]
	if !ok {
		return fmt.Errorf("地址 %s 状态 %s 无迁移规则", address, item.Status)
	}
	for _, a := range allowed {
		if a == target {
			item.Status = target
			item.UpdatedAt = time.Now().UTC()
			q.dirty = true
			return nil
		}
	}
	return fmt.Errorf("非法状态迁移 %s → %s (地址 %s)", item.Status, target, address)
}

// SetStatus 直接设置状态（引擎内部受控使用，跳过状态机校验）。
func (q *Queue) SetStatus(address string, status DiscoveryStatus) {
	address = normalizeAddress(address)
	q.mu.Lock()
	defer q.mu.Unlock()
	if item, ok := q.items[address]; ok {
		item.Status = status
		item.UpdatedAt = time.Now().UTC()
		q.dirty = true
	}
}

// SetScore 记录评分结果。
func (q *Queue) SetScore(address string, score float64, breakdown map[string]float64) {
	address = normalizeAddress(address)
	q.mu.Lock()
	defer q.mu.Unlock()
	if item, ok := q.items[address]; ok {
		item.Score = score
		item.ScoreBreakdown = breakdown
		item.UpdatedAt = time.Now().UTC()
		q.dirty = true
	}
}

// SetEntity 记录实体识别结果。
func (q *Queue) SetEntity(address string, entity EntityType, label string) {
	address = normalizeAddress(address)
	q.mu.Lock()
	defer q.mu.Unlock()
	if item, ok := q.items[address]; ok {
		item.Entity = entity
		if label != "" {
			item.Label = label
		}
		item.UpdatedAt = time.Now().UTC()
		q.dirty = true
	}
}

// SetAcquisition 记录采集方式与目标等级（不提升当前等级）。
func (q *Queue) SetAcquisition(address string, mode AcquisitionMode, target DataLevel) {
	address = normalizeAddress(address)
	q.mu.Lock()
	defer q.mu.Unlock()
	if item, ok := q.items[address]; ok {
		item.Acquisition = mode
		item.TargetLevel = target
		item.UpdatedAt = time.Now().UTC()
		q.dirty = true
	}
}

// SetDataLevel 提升地址的当前数据等级（采集成功后调用）。
func (q *Queue) SetDataLevel(address string, level DataLevel) {
	address = normalizeAddress(address)
	q.mu.Lock()
	defer q.mu.Unlock()
	if item, ok := q.items[address]; ok {
		if level > item.DataLevel {
			item.DataLevel = level
		}
		item.UpdatedAt = time.Now().UTC()
		q.dirty = true
	}
}

// SetJob 关联下载任务。
func (q *Queue) SetJob(address, jobID string) {
	address = normalizeAddress(address)
	q.mu.Lock()
	defer q.mu.Unlock()
	if item, ok := q.items[address]; ok {
		item.JobID = jobID
		item.UpdatedAt = time.Now().UTC()
		q.dirty = true
	}
}

// SetIgnoredReason 记录忽略原因。
func (q *Queue) SetIgnoredReason(address, reason string) {
	address = normalizeAddress(address)
	q.mu.Lock()
	defer q.mu.Unlock()
	if item, ok := q.items[address]; ok {
		item.IgnoredReason = reason
		item.UpdatedAt = time.Now().UTC()
		q.dirty = true
	}
}

// Get 查询地址条目。
func (q *Queue) Get(address string) (*DiscoveredAddress, bool) {
	address = normalizeAddress(address)
	q.mu.RLock()
	defer q.mu.RUnlock()
	item, ok := q.items[address]
	if !ok {
		return nil, false
	}
	copy := *item
	return &copy, true
}

// List 按状态/实体/深度过滤返回条目（副本）。
func (q *Queue) List(filterStatus DiscoveryStatus, entity EntityType, maxDepth int) []DiscoveredAddress {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var out []DiscoveredAddress
	for _, addr := range q.order {
		item := q.items[addr]
		if filterStatus != "" && item.Status != filterStatus {
			continue
		}
		if entity != "" && item.Entity != entity {
			continue
		}
		if maxDepth >= 0 && item.Depth > maxDepth {
			continue
		}
		out = append(out, *item)
	}
	return out
}

// Count 统计各状态数量。
func (q *Queue) Count() map[DiscoveryStatus]int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := map[DiscoveryStatus]int{}
	for _, item := range q.items {
		out[item.Status]++
	}
	return out
}

// Total 返回队列总数。
func (q *Queue) Total() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.items)
}

// CountByEntity 按实体类型统计。
func (q *Queue) CountByEntity() map[EntityType]int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := map[EntityType]int{}
	for _, item := range q.items {
		out[item.Entity]++
	}
	return out
}

// CountByAcquisition 按采集方式统计。
func (q *Queue) CountByAcquisition() map[AcquisitionMode]int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := map[AcquisitionMode]int{}
	for _, item := range q.items {
		out[item.Acquisition]++
	}
	return out
}

// PendingScoring 返回待评分地址（DISCOVERED）。
func (q *Queue) PendingScoring(limit int) []string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var out []string
	for _, addr := range q.order {
		if q.items[addr].Status == StatusDiscovered {
			out = append(out, addr)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out
}

// PendingAcquisition 返回待采集地址（APPROVED），按评分降序。
func (q *Queue) PendingAcquisition(limit int) []string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	type scored struct {
		addr  string
		score float64
	}
	var pending []scored
	for _, addr := range q.order {
		item := q.items[addr]
		if item.Status == StatusApproved {
			pending = append(pending, scored{addr, item.Score})
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].score > pending[j].score })
	var out []string
	for _, p := range pending {
		out = append(out, p.addr)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// ── 持久化 ──

type queueSnapshot struct {
	Items []*DiscoveredAddress `json:"items"`
}

func (q *Queue) load() {
	if q.storeDir == "" {
		return
	}
	path := filepath.Join(q.storeDir, "discovery_queue.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return // 首次运行无文件
	}
	var snap queueSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return
	}
	for _, item := range snap.Items {
		if item == nil || !IsValidEVMAddress(item.Address) {
			continue // 复验地址，防止本地篡改的队列注入非法地址
		}
		item.Address = strings.ToLower(item.Address)
		q.items[item.Address] = item
		q.order = append(q.order, item.Address)
	}
}

// Save 将队列持久化到 JSON（原子写）。
func (q *Queue) Save() error {
	if q.storeDir == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.dirty {
		return nil
	}
	if err := os.MkdirAll(q.storeDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(q.storeDir, "discovery_queue.json")
	items := make([]*DiscoveredAddress, 0, len(q.order))
	for _, addr := range q.order {
		items = append(items, q.items[addr])
	}
	data, err := json.MarshalIndent(queueSnapshot{Items: items}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	q.dirty = false
	return nil
}

// normalizeAddress 统一地址为小写 0x 前缀。
func normalizeAddress(addr string) string {
	addr = strings.TrimSpace(strings.ToLower(addr))
	return addr
}

// IsValidEVMAddress 校验地址是否为合法的 EVM 地址（0x + 40 位十六进制）。
// 作为 API 边界的安全校验，防止恶意地址字符串注入 SQL 查询。
func IsValidEVMAddress(addr string) bool {
	addr = strings.TrimSpace(strings.ToLower(addr))
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
