package investigationstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ── Score Profile Storage（V1 设计 §10）──
//
// 不同调查模式的评分权重，持久化于 score-profile/profiles.json。
// 结构：{"fund_trace": {"fund":0.4,"graph":0.3,...}, ...}
// 维度键与六维评分一致（fund/behavior/risk/entity/graph/identity）。

// ScoreProfileStore 管理评分权重配置（单文件原子写）。
type ScoreProfileStore struct {
	mu       sync.Mutex
	path     string                        // profiles.json 路径（空 = 仅内存，测试用）
	profiles map[string]map[string]float64 // mode → 维度 → 权重
}

// NewScoreProfileStore 创建评分配置存储。path 非空时启动加载。
func NewScoreProfileStore(path string) *ScoreProfileStore {
	s := &ScoreProfileStore{
		path:     path,
		profiles: make(map[string]map[string]float64),
	}
	if path != "" {
		s.load()
	}
	return s
}

// Get 返回指定模式的权重（nil 表示未配置，由调用方回退默认）。
func (s *ScoreProfileStore) Get(mode string) map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.profiles[mode]
	if !ok {
		return nil
	}
	out := make(map[string]float64, len(w))
	for k, v := range w {
		out[k] = v
	}
	return out
}

// Set 设置模式权重并原子持久化。
func (s *ScoreProfileStore) Set(mode string, weights map[string]float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[mode] = cloneWeights(weights)
	return s.saveLocked()
}

// All 返回全部模式权重（防御性拷贝）。
func (s *ScoreProfileStore) All() map[string]map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]map[string]float64, len(s.profiles))
	for m, w := range s.profiles {
		out[m] = cloneWeights(w)
	}
	return out
}

// saveLocked 原子写 profiles.json，必须在持锁状态调用。
func (s *ScoreProfileStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	env := envelope[map[string]map[string]float64]{
		SchemaVersion: CurrentSchemaVersion,
		Data:          s.profiles,
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data)
}

// load 启动时加载 profiles.json。
func (s *ScoreProfileStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var env envelope[map[string]map[string]float64]
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}
	if env.SchemaVersion != CurrentSchemaVersion {
		return
	}
	if env.Data != nil {
		s.profiles = env.Data
	}
}

// cloneWeights 拷贝权重 map。
func cloneWeights(w map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(w))
	for k, v := range w {
		out[k] = v
	}
	return out
}
