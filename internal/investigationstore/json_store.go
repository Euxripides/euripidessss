package investigationstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ── JSONStore：通用 JSON 文件存储（V1 设计 §4/§11/§14/§15）──
//
// 通用实现，业务类型通过泛型复用同一套原子写/加锁/加载/校验机制。
// 文件格式为 envelope：{"schema_version":N,"data":{...}}，加载时校验版本。
// 每个 key 对应一个文件（key 作为文件名，目录由 storeDir 决定），
// 支持 "/" 嵌套（evidence/{inv}/{ev}.json 用 key="inv-1/ev-1"）。
//
// 并发控制：单文件锁（per-key mutex）+ 全局 RWMutex 保护内存 map。
// 锁顺序固定为 key lock → store mu，避免锁嵌套/顺序反转（设计 §14）。

// ErrInvalidKey 表示 key 不合法（路径穿越防护）。
var ErrInvalidKey = errors.New("invalid storage key")

// CurrentSchemaVersion 是当前数据 schema 版本。
const CurrentSchemaVersion = 1

// validateFunc 在加载时校验记录（ID 合法性/关联完整性，设计 §15）。
type validateFunc[T any] func(key string, v *T) bool

// Option 配置 JSONStore。
type Option[T any] func(*options[T])

type options[T any] struct {
	validate validateFunc[T]
}

// WithValidate 注册加载时校验函数：返回 false 的记录被跳过（不载入内存）。
func WithValidate[T any](fn func(key string, v *T) bool) Option[T] {
	return func(o *options[T]) { o.validate = fn }
}

// JSONStore 是通用的原子写 JSON 文件存储。
type JSONStore[T any] struct {
	mu    sync.RWMutex           // 保护 items 与 locks
	locks map[string]*sync.Mutex // 单文件锁（per-key）
	dir   string                 // 存储目录（空 = 仅内存，测试用）
	opts  options[T]
	items map[string]*T
}

// NewJSONStore 创建存储。dir 为空则仅内存（测试用）。
// 非空目录启动时加载全部文件（跳过 archive/ 与 .tmp 残留）。
func NewJSONStore[T any](dir string, opts ...Option[T]) *JSONStore[T] {
	s := &JSONStore[T]{
		locks: make(map[string]*sync.Mutex),
		dir:   dir,
		items: make(map[string]*T),
	}
	for _, o := range opts {
		o(&s.opts)
	}
	if dir != "" {
		s.loadAll()
	}
	return s
}

// keyLock 获取单文件锁（per-key），必须在无锁状态下调用。
func (s *JSONStore[T]) keyLock(key string) *sync.Mutex {
	s.mu.Lock()
	lock, ok := s.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[key] = lock
	}
	s.mu.Unlock()
	return lock
}

// ValidKey 校验 key 合法性：只允许 [A-Za-z0-9._-] 段，"/" 仅作目录分隔，
// 禁止空段、"."、".."（路径穿越防护）。
func ValidKey(key string) bool {
	if key == "" || len(key) > 256 {
		return false
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
		for _, c := range seg {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
				c == '.' || c == '_' || c == '-') {
				return false
			}
		}
	}
	return true
}

// pathForKey 将 key 映射为文件路径（dir/key.json）。
func (s *JSONStore[T]) pathForKey(key string) (string, error) {
	if !ValidKey(key) {
		return "", fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	return filepath.Join(s.dir, filepath.FromSlash(key)+".json"), nil
}

// Save 保存记录（原子写：temp + fsync + rename）。key 同时作为文件名。
func (s *JSONStore[T]) Save(key string, v T) error {
	if s.dir == "" {
		// 仅内存模式：拷贝后入 map
		cp := v
		s.mu.Lock()
		s.items[key] = &cp
		s.mu.Unlock()
		return nil
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return err
	}
	lock := s.keyLock(key)
	lock.Lock()
	defer lock.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	env := envelope[T]{SchemaVersion: CurrentSchemaVersion, Data: v}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, data); err != nil {
		return err
	}
	cp := v
	s.mu.Lock()
	s.items[key] = &cp
	s.mu.Unlock()
	return nil
}

// Get 读取记录（防御性值拷贝）。
func (s *JSONStore[T]) Get(key string) (T, bool) {
	lock := s.keyLock(key)
	lock.Lock()
	defer lock.Unlock()
	s.mu.RLock()
	item, ok := s.items[key]
	s.mu.RUnlock()
	if !ok {
		var zero T
		return zero, false
	}
	cp := *item
	return cp, true
}

// List 返回全部记录（防御性值拷贝，按 key 排序保证稳定输出）。
func (s *JSONStore[T]) List() []T {
	s.mu.RLock()
	out := make([]T, 0, len(s.items))
	for _, item := range s.items {
		out = append(out, *item)
	}
	s.mu.RUnlock()
	return out
}

// Keys 返回全部 key（排序稳定）。
func (s *JSONStore[T]) Keys() []string {
	s.mu.RLock()
	keys := make([]string, 0, len(s.items))
	for k := range s.items {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	sortStrings(keys)
	return keys
}

// Delete 删除记录（文件 + 内存）。
func (s *JSONStore[T]) Delete(key string) error {
	if s.dir != "" {
		path, err := s.pathForKey(key)
		if err != nil {
			return err
		}
		lock := s.keyLock(key)
		lock.Lock()
		defer lock.Unlock()
		_ = os.Remove(path)
	}
	s.mu.Lock()
	delete(s.items, key)
	s.mu.Unlock()
	return nil
}

// Exists 判断记录是否存在。
func (s *JSONStore[T]) Exists(key string) bool {
	s.mu.RLock()
	_, ok := s.items[key]
	s.mu.RUnlock()
	return ok
}

// MoveToArchive 将记录移入 dir/archive/ 子目录（生命周期管理，设计 §13）。
// 归档文件不再被 loadAll 载入。仅内存模式为 no-op。
// 嵌套 key（如 inv-1/ev-1）归档时保留子路径（archive/inv-1/ev-1.json），
// 避免不同前缀的同名文件互相覆盖。
func (s *JSONStore[T]) MoveToArchive(key string) error {
	if s.dir == "" {
		return nil
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return err
	}
	lock := s.keyLock(key)
	lock.Lock()
	defer lock.Unlock()
	dst := filepath.Join(s.dir, "archive", filepath.FromSlash(key)+".json")
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	if err := os.Rename(path, dst); err != nil {
		if os.IsNotExist(err) {
			s.mu.Lock()
			delete(s.items, key)
			s.mu.Unlock()
			return nil
		}
		return err
	}
	s.mu.Lock()
	delete(s.items, key)
	s.mu.Unlock()
	return nil
}

// Count 返回记录数。
func (s *JSONStore[T]) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// Dir 返回存储目录（测试/恢复用；仅内存模式返回空）。
func (s *JSONStore[T]) Dir() string {
	return s.dir
}

// loadAll 启动时加载全部记录：递归扫描 storeDir 下 *.json，
// 跳过 archive/ 目录与 .tmp 残留；解析 envelope 并校验 schema_version；
// 校验失败的记录跳过（防磁盘篡改/版本不兼容）。
func (s *JSONStore[T]) loadAll() {
	_ = filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "archive" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			return nil
		}
		rel, err := filepath.Rel(s.dir, path)
		if err != nil {
			return nil
		}
		key := strings.TrimSuffix(filepath.ToSlash(rel), ".json")
		if !ValidKey(key) {
			return nil // 越界/非法文件名不载入（路径穿越防护）
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var env envelope[T]
		if err := json.Unmarshal(data, &env); err != nil {
			return nil // 损坏文件跳过（崩溃恢复：旧 .tmp 或半写文件）
		}
		if env.SchemaVersion != CurrentSchemaVersion {
			return nil // 版本不兼容跳过（设计 §15）
		}
		if s.opts.validate != nil && !s.opts.validate(key, &env.Data) {
			return nil // ID/关联校验失败跳过（设计 §15）
		}
		s.mu.Lock()
		s.items[key] = &env.Data
		s.mu.Unlock()
		return nil
	})
}

// envelope 是磁盘文件包装：{"schema_version":N,"data":{...}}。
type envelope[T any] struct {
	SchemaVersion int `json:"schema_version"`
	Data          T   `json:"data"`
}

// writeFileAtomic 原子写：temp + fsync + rename + 目录 fsync（设计 §11）。
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil { // fsync：崩溃后不残留半写
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	// 目录 fsync：确保 rename 的目录项持久化（断电后新文件名不丢失）。
	// Windows 目录句柄不支持 Sync，忽略错误（NTFS 元数据语义由系统保证）。
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// sortStrings 简单排序（避免引入额外依赖）。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
