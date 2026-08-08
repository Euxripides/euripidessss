package entityintel

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

// Store 是 Entity Intelligence 文件存储（设计 §31-§32）：
//   entity-intelligence/
//   ├── entities/{type}/{id}.json
//   ├── addresses/{chain}/{shard}/{address}.json
//   ├── evidence/{id}.json
//   ├── clusters/{id}.json
//   ├── leads/{investigation_id}/{id}.json
//   ├── manual/{investigation_id}.json
//   ├── conflicts/{id}.json
//   └── events.ndjson
type Store struct {
	root string
	mu   sync.Mutex
}

// NewStore 创建存储。
func NewStore(root string) *Store {
	return &Store{root: root}
}

// ── Entity ──

func (s *Store) SaveEntity(e *Entity) error {
	if e == nil || e.ID == "" {
		return fmt.Errorf("entity 缺少 ID")
	}
	e.UpdatedAt = time.Now().UTC()
	return s.writeJSON(filepath.Join("entities", sanitizeType(string(e.EntityType)), e.ID+".json"), e)
}

func (s *Store) GetEntity(id string) *Entity {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.listEntitiesLocked()
	for _, e := range entries {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (s *Store) ListEntities() []*Entity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listEntitiesLocked()
}

func (s *Store) listEntitiesLocked() []*Entity {
	var out []*Entity
	_ = filepath.WalkDir(filepath.Join(s.root, "entities"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		payload, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var e Entity
		if json.Unmarshal(payload, &e) == nil {
			out = append(out, &e)
		}
		return nil
	})
	return out
}

// ── Address Intelligence Entry ──

func (s *Store) SaveAddressEntry(chainID int64, chainKey, address string, entry *AddressIntelligenceEntry) error {
	if entry == nil {
		entry = &AddressIntelligenceEntry{}
	}
	entry.ChainID = chainID
	entry.ChainKey = chainKey
	entry.Address = strings.ToLower(address)
	entry.UpdatedAt = time.Now().UTC()
	path := filepath.Join("addresses", itoa(chainID), shard(address), entry.Address+".json")
	// 历史版本：仅记录新增/变化的标签（设计 §49-§50、P2 历史实体版本重放）
	if old := s.readAddressEntryLocked(chainID, entry.Address); old != nil {
		oldByKey := map[string]AddressLabel{}
		for _, l := range old.Labels {
			oldByKey[l.Label+"|"+l.EntityID] = l
		}
		for _, l := range entry.Labels {
			prev, ok := oldByKey[l.Label+"|"+l.EntityID]
			if !ok || prev.Confidence != l.Confidence || prev.EntityID != l.EntityID || prev.ResolverVersion != l.ResolverVersion {
				s.appendLabelHistoryLocked(entry.ChainID, entry.Address, l)
			}
		}
	} else {
		for _, l := range entry.Labels {
			s.appendLabelHistoryLocked(entry.ChainID, entry.Address, l)
		}
	}
	if err := s.writeJSON(path, entry); err != nil {
		return err
	}
	return s.appendEvent(map[string]any{
		"type": "address_updated", "chain_id": chainID, "address": entry.Address,
		"labels": len(entry.Labels), "clusters": len(entry.ClusterIDs), "at": time.Now().UTC(),
	})
}

func (s *Store) GetAddressEntry(chainID int64, address string) *AddressIntelligenceEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAddressEntryLocked(chainID, address)
}

func (s *Store) readAddressEntryLocked(chainID int64, address string) *AddressIntelligenceEntry {
	path := filepath.Join(s.root, "addresses", itoa(chainID), shard(address), strings.ToLower(address)+".json")
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var e AddressIntelligenceEntry
	if json.Unmarshal(payload, &e) != nil {
		return nil
	}
	return &e
}

// LabelHistoryItem 是标签历史版本。
type LabelHistoryItem struct {
	Label      string    `json:"label"`
	EntityID   string    `json:"entity_id,omitempty"`
	Confidence float64   `json:"confidence"`
	Version    string    `json:"resolver_version"`
	RecordedAt time.Time `json:"recorded_at"`
}

func (s *Store) appendLabelHistoryLocked(chainID int64, address string, l AddressLabel) {
	path := filepath.Join(s.root, "labels", "history", itoa(chainID), shard(address), strings.ToLower(address)+".ndjson")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	item := LabelHistoryItem{
		Label: l.Label, EntityID: l.EntityID, Confidence: l.Confidence,
		Version: l.ResolverVersion, RecordedAt: time.Now().UTC(),
	}
	payload, _ := json.Marshal(item)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(payload, '\n'))
}

// LabelHistory 返回标签历史（新→旧）。
func (s *Store) LabelHistory(chainID int64, address string) []LabelHistoryItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.root, "labels", "history", itoa(chainID), shard(address), strings.ToLower(address)+".ndjson")
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []LabelHistoryItem
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item LabelHistoryItem
		if json.Unmarshal([]byte(line), &item) == nil {
			out = append(out, item)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ── Evidence ──

func (s *Store) SaveEvidence(ev *EvidenceRef) error {
	if ev == nil || ev.EvidenceID == "" {
		return fmt.Errorf("evidence 缺少 ID")
	}
	return s.writeJSON(filepath.Join("evidence", ev.EvidenceID+".json"), ev)
}

func (s *Store) GetEvidence(id string) *EvidenceRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, err := os.ReadFile(filepath.Join(s.root, "evidence", id+".json"))
	if err != nil {
		return nil
	}
	var ev EvidenceRef
	if json.Unmarshal(payload, &ev) != nil {
		return nil
	}
	return &ev
}

func (s *Store) GetEvidences(ids []string) []EvidenceRef {
	var out []EvidenceRef
	for _, id := range ids {
		if ev := s.GetEvidence(id); ev != nil {
			out = append(out, *ev)
		}
	}
	return out
}

// ── Cluster ──

func (s *Store) SaveCluster(c *AddressCluster) error {
	if c == nil || c.ID == "" {
		return fmt.Errorf("cluster 缺少 ID")
	}
	c.UpdatedAt = time.Now().UTC()
	sort.Strings(c.Addresses)
	return s.writeJSON(filepath.Join("clusters", c.ID+".json"), c)
}

func (s *Store) GetCluster(id string) *AddressCluster {
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, err := os.ReadFile(filepath.Join(s.root, "clusters", id+".json"))
	if err != nil {
		return nil
	}
	var c AddressCluster
	if json.Unmarshal(payload, &c) != nil {
		return nil
	}
	return &c
}

func (s *Store) ListClusters() []*AddressCluster {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*AddressCluster
	_ = filepath.WalkDir(filepath.Join(s.root, "clusters"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		payload, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var c AddressCluster
		if json.Unmarshal(payload, &c) == nil {
			out = append(out, &c)
		}
		return nil
	})
	return out
}

// ── Investigation Lead ──

func (s *Store) SaveLead(l *InvestigationLead) error {
	if l == nil || l.ID == "" || l.InvestigationID == "" {
		return fmt.Errorf("lead 缺少 ID/调查 ID")
	}
	l.CreatedAt = time.Now().UTC()
	return s.writeJSON(filepath.Join("leads", sanitizeID(l.InvestigationID), l.ID+".json"), l)
}

func (s *Store) ListLeads(investigationID string) []*InvestigationLead {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*InvestigationLead
	dir := filepath.Join(s.root, "leads", sanitizeID(investigationID))
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		payload, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var l InvestigationLead
		if json.Unmarshal(payload, &l) == nil {
			out = append(out, &l)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// ── Manual Label（案件作用域）──

func (s *Store) SaveManualLabel(m *ManualLabel) error {
	if m == nil || m.ID == "" || m.InvestigationID == "" {
		return fmt.Errorf("manual label 缺少 ID/调查 ID")
	}
	m.UpdatedAt = time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.root, "manual", sanitizeID(m.InvestigationID)+".json")
	var labels []*ManualLabel
	if payload, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(payload, &labels)
	}
	replaced := false
	for i, old := range labels {
		if old.Address == m.Address && old.Label == m.Label {
			labels[i] = m
			replaced = true
			break
		}
	}
	if !replaced {
		labels = append(labels, m)
	}
	return writeJSONFile(path, labels)
}

func (s *Store) ListManualLabels(investigationID string) []*ManualLabel {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.root, "manual", sanitizeID(investigationID)+".json")
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var labels []*ManualLabel
	_ = json.Unmarshal(payload, &labels)
	return labels
}

// ── Conflict ──

func (s *Store) SaveConflict(c *ConflictEntry) error {
	if c == nil || c.ID == "" {
		return fmt.Errorf("conflict 缺少 ID")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	return s.writeJSON(filepath.Join("conflicts", c.ID+".json"), c)
}

func (s *Store) ListConflicts(address string) []*ConflictEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*ConflictEntry
	_ = filepath.WalkDir(filepath.Join(s.root, "conflicts"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		payload, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var c ConflictEntry
		if json.Unmarshal(payload, &c) == nil && strings.EqualFold(c.Address, address) {
			out = append(out, &c)
		}
		return nil
	})
	return out
}

// ── Index（Address → Entity / Entity → Addresses / Label → Addresses）──

type Indexes struct {
	AddressEntity map[string]string   `json:"address_entity"`
	EntityAddress map[string][]string `json:"entity_addresses"`
	LabelAddress  map[string][]string `json:"label_addresses"`
}

func (s *Store) RebuildIndexes() (*Indexes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := &Indexes{AddressEntity: map[string]string{}, EntityAddress: map[string][]string{}, LabelAddress: map[string][]string{}}
	_ = filepath.WalkDir(filepath.Join(s.root, "addresses"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		payload, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var e AddressIntelligenceEntry
		if json.Unmarshal(payload, &e) != nil {
			return nil
		}
		key := fmt.Sprintf("%d|%s", e.ChainID, e.Address)
		for _, l := range e.Labels {
			if l.EntityID != "" {
				idx.AddressEntity[key] = l.EntityID
				idx.EntityAddress[l.EntityID] = append(idx.EntityAddress[l.EntityID], e.Address)
			}
			idx.LabelAddress[l.Label] = append(idx.LabelAddress[l.Label], e.Address)
		}
		return nil
	})
	for k := range idx.EntityAddress {
		sort.Strings(idx.EntityAddress[k])
		idx.EntityAddress[k] = uniqueStrings(idx.EntityAddress[k])
	}
	for k := range idx.LabelAddress {
		sort.Strings(idx.LabelAddress[k])
		idx.LabelAddress[k] = uniqueStrings(idx.LabelAddress[k])
	}
	if err := writeJSONFile(filepath.Join(s.root, "indexes", "indexes.json"), idx); err != nil {
		return nil, err
	}
	return idx, nil
}

// ── 内部工具 ──

func (s *Store) writeJSON(rel string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSONFile(filepath.Join(s.root, filepath.FromSlash(rel)), v)
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) appendEvent(ev map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.root, "events.ndjson")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	payload, _ := json.Marshal(ev)
	_, err = f.Write(append(payload, '\n'))
	return err
}

func shard(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if len(addr) > 4 {
		return addr[2:4]
	}
	return "00"
}

func itoa(v int64) string {
	return fmt.Sprintf("%d", v)
}

func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	for _, r := range `/\:*?"<>|` {
		id = strings.ReplaceAll(id, string(r), "_")
	}
	if id == "" || id == "." || id == ".." {
		return "default"
	}
	return id
}

func sanitizeType(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return "unknown"
	}
	return strings.ToLower(strings.ReplaceAll(t, " ", "_"))
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
