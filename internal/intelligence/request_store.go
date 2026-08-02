package intelligence

import (
	"strconv"
	"sync"
	"time"

	"github.com/etl/backend/internal/investigationstore"
)

// ── Investigation Request Store（V1 设计 §4/§5）──
//
// 调查请求的 JSON 文件持久化（backend/data/investigation/requests/）。
// 迁移至 Investigation Storage Layer V1：复用 investigationstore.JSONStore，
// 原子写（tmp + fsync + rename）、schema_version 校验、单文件锁、
// 加载时 ID 校验（validRequestID，路径穿越防护）。
//
// 存储接口（Store[T]）：Save/Get/List/Delete/Exists 由 JSONStore 提供，
// RequestStore 在其上提供业务 API（Create/Link 等）。

// RequestStore 管理调查请求的读写。
type RequestStore struct {
	mu     sync.Mutex // 保护 nextID（Create 自增）
	store  *investigationstore.JSONStore[InvestigationRequest]
	nextID int
}

// NewRequestStore 创建请求存储。storeDir 为空则仅内存（测试用）。
func NewRequestStore(storeDir string) *RequestStore {
	s := &RequestStore{
		store: investigationstore.NewJSONStore(
			storeDir,
			investigationstore.WithValidate(func(key string, v *InvestigationRequest) bool {
				// LOW-3：仅接受自生成 ID 格式（req-N），且 ID 与文件名一致，
				// 磁盘被篡改的越界 ID 不入 map（防止文件路径越界写）
				return v != nil && v.ID == key && validRequestID(v.ID) && v.Address != ""
			}),
		),
		nextID: 1,
	}
	// 推进自增计数器（req-N 解析）
	for _, req := range s.store.List() {
		if n, err := strconv.Atoi(req.ID[4:]); err == nil && n >= s.nextID {
			s.nextID = n + 1
		}
	}
	return s
}

// Create 创建新请求并持久化，返回带 ID 的深拷贝。
// 顺序：先持久化成功再入内存 map（避免磁盘失败时内存/磁盘不一致）；
// 入 map 与返回均使用深拷贝（store 不持有调用方对象，防止外部改写）。
func (s *RequestStore) Create(req *InvestigationRequest) (*InvestigationRequest, error) {
	s.mu.Lock()
	id := "req-" + itoa(s.nextID)
	s.nextID++
	s.mu.Unlock()

	now := time.Now().UTC()
	stored := cloneRequest(req)
	stored.ID = id
	stored.CreatedAt = now
	stored.UpdatedAt = now
	if stored.Status == "" {
		stored.Status = RequestCreated
	}
	if err := s.store.Save(id, *stored); err != nil {
		return nil, err
	}
	return cloneRequest(stored), nil
}

// cloneRequest 深拷贝请求（含切片与意图）。
func cloneRequest(req *InvestigationRequest) *InvestigationRequest {
	copy := *req
	copy.ExpectedResult = append([]string(nil), req.ExpectedResult...)
	if req.Intent != nil {
		intent := *req.Intent
		intent.Goals = append([]string(nil), req.Intent.Goals...)
		copy.Intent = &intent
	}
	return &copy
}

// Get 读取请求（防御性深拷贝）。
func (s *RequestStore) Get(id string) (*InvestigationRequest, bool) {
	req, ok := s.store.Get(id)
	if !ok {
		return nil, false
	}
	return cloneRequest(&req), true
}

// List 返回全部请求（按创建时间倒序，防御性深拷贝）。
func (s *RequestStore) List() []InvestigationRequest {
	list := s.store.List()
	out := make([]InvestigationRequest, 0, len(list))
	for i := range list {
		out = append(out, *cloneRequest(&list[i]))
	}
	// 稳定排序：新的在前
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].CreatedAt.After(out[j-1].CreatedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Link 回填调查 ID 并更新状态（调查启动/终态同步后调用）。
// 终态保护：finished/failed 不可被回退为 started/created
// （防止启动回填的 Link(started) 覆盖调查已极快完成/失败落盘的终态）。
func (s *RequestStore) Link(id, investigationID, status string) error {
	req, ok := s.store.Get(id)
	if !ok {
		return nil
	}
	if isTerminalStatus(req.Status) && !isTerminalStatus(status) {
		return nil // 终态不回退
	}
	req.InvestigationID = investigationID
	if status != "" {
		req.Status = status
	}
	req.UpdatedAt = time.Now().UTC()
	return s.store.Save(id, req)
}

// isTerminalStatus 判断请求状态是否为终态。
func isTerminalStatus(s string) bool {
	return s == RequestFinished || s == RequestFailed
}

// Save 持久化单个请求（原子写）。
func (s *RequestStore) Save(id string) error {
	req, ok := s.store.Get(id)
	if !ok {
		return nil
	}
	return s.store.Save(id, req)
}

// Delete 删除请求（存储接口 Store[T]）。
func (s *RequestStore) Delete(id string) error {
	return s.store.Delete(id)
}

// Exists 判断请求是否存在（存储接口 Store[T]）。
func (s *RequestStore) Exists(id string) bool {
	return s.store.Exists(id)
}

// Archive 生命周期归档（V1 设计 §13）：active ≤ 5 / history ≤ 200，
// 超出上限的最旧请求移入 requests/archive/。
func (s *RequestStore) Archive(maxActive, maxHistory int) (int, error) {
	return investigationstore.Archive(s.store, func(v *InvestigationRequest) bool {
		return isTerminalStatus(v.Status)
	}, investigationstore.Lifecycle{MaxActive: maxActive, MaxHistory: maxHistory})
}

// validRequestID 校验请求 ID 为自生成格式（req-<数字>）。
func validRequestID(id string) bool {
	if len(id) <= 4 || id[:4] != "req-" {
		return false
	}
	for _, c := range id[4:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
