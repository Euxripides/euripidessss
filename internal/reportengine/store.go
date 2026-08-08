package reportengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store 是报告文件存储（设计 §61）：
// investigations/{inv}/reports/report_v{N}/{report.json,snapshot.json,findings.json,timeline.json,evidence-index.json,exports/}
type Store struct {
	root string
	mu   sync.Mutex
}

// NewStore 创建存储。
func NewStore(root string) *Store {
	return &Store{root: root}
}

// NextVersion 返回下一个版本号。
func (s *Store) NextVersion(invID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.root, sanitizeID(invID), "reports")
	entries, _ := os.ReadDir(dir)
	max := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "report_v") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "report_v")); err == nil && n > max {
			max = n
		}
	}
	return max + 1
}

// Save 保存报告全套文件（原子写）。
func (s *Store) Save(invID string, report *InvestigationReport, snapshot *ReportSnapshot, timeline []TimelineEvent, findings []Finding, evidence []EvidenceRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.reportDir(invID, report.Version)
	if err := os.MkdirAll(filepath.Join(dir, "exports"), 0o755); err != nil {
		return err
	}
	files := map[string]any{
		"report.json":       report,
		"snapshot.json":     snapshot,
		"timeline.json":     map[string]any{"events": timeline},
		"findings.json":     map[string]any{"findings": findings},
		"evidence-index.json": map[string]any{"evidence": evidence},
	}
	for name, v := range files {
		if err := writeJSON(filepath.Join(dir, name), v); err != nil {
			return err
		}
	}
	return nil
}

// List 返回报告列表（按版本倒序）。
func (s *Store) List(invID string) []ReportListEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.root, sanitizeID(invID), "reports")
	entries, _ := os.ReadDir(dir)
	var out []ReportListEntry
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "report_v") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(dir, e.Name(), "report.json"))
		if err != nil {
			continue
		}
		var r InvestigationReport
		if json.Unmarshal(payload, &r) != nil {
			continue
		}
		out = append(out, ReportListEntry{
			ID: "report_v" + strconv.Itoa(r.Version), Version: r.Version, Title: r.Title, Goal: r.Goal,
			Status: r.Status, Summary: r.Summary, CreatedAt: r.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out
}

// Get 读取报告全套。
func (s *Store) Get(invID, versionID string) (*InvestigationReport, *ReportSnapshot, []TimelineEvent, []Finding, []EvidenceRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.reportDir(invID, versionOf(versionID))
	report := loadJSON[InvestigationReport](filepath.Join(dir, "report.json"))
	if report == nil {
		return nil, nil, nil, nil, nil
	}
	snapshot := loadJSON[ReportSnapshot](filepath.Join(dir, "snapshot.json"))
	tl := loadJSON[struct{ Events []TimelineEvent }](filepath.Join(dir, "timeline.json"))
	fd := loadJSON[struct{ Findings []Finding }](filepath.Join(dir, "findings.json"))
	ev := loadJSON[struct{ Evidence []EvidenceRef }](filepath.Join(dir, "evidence-index.json"))
	var timeline []TimelineEvent
	if tl != nil {
		timeline = tl.Events
	}
	var findings []Finding
	if fd != nil {
		findings = fd.Findings
	}
	var evidence []EvidenceRef
	if ev != nil {
		evidence = ev.Evidence
	}
	return report, snapshot, timeline, findings, evidence
}

// UpdateStatus 更新报告状态（Lock / Review / Outdated / Superseded）。
func (s *Store) UpdateStatus(invID string, version int, status ReportStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.reportDir(invID, version), "report.json")
	report := loadJSON[InvestigationReport](path)
	if report == nil {
		return os.ErrNotExist
	}
	report.Status = status
	report.UpdatedAt = time.Now().UTC()
	return writeJSON(path, report)
}

// UpdateSignature 写入报告签名。
func (s *Store) UpdateSignature(invID string, version int, sig *ReportSignature) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.reportDir(invID, version), "report.json")
	report := loadJSON[InvestigationReport](path)
	if report == nil {
		return os.ErrNotExist
	}
	report.Signature = sig
	report.UpdatedAt = time.Now().UTC()
	return writeJSON(path, report)
}

// UpdateSectionNarrative 更新章节叙事（LLM 润色后写回）。
func (s *Store) UpdateSectionNarrative(invID string, version int, sectionID, narrative string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.reportDir(invID, version), "report.json")
	report := loadJSON[InvestigationReport](path)
	if report == nil {
		return os.ErrNotExist
	}
	found := false
	for i := range report.Sections {
		if report.Sections[i].ID == sectionID {
			report.Sections[i].Narrative = narrative
			found = true
			break
		}
	}
	if !found {
		return os.ErrNotExist
	}
	report.UpdatedAt = time.Now().UTC()
	return writeJSON(path, report)
}

// ExportDir 返回导出目录。
func (s *Store) ExportDir(invID string, version int) string {
	return filepath.Join(s.reportDir(invID, version), "exports")
}

func (s *Store) reportDir(invID string, version int) string {
	return filepath.Join(s.root, sanitizeID(invID), "reports", "report_v"+strconv.Itoa(version))
}

func versionOf(versionID string) int {
	v := strings.TrimPrefix(versionID, "report_v")
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

func loadJSON[T any](path string) *T {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var v T
	if json.Unmarshal(payload, &v) != nil {
		return nil
	}
	return &v
}

func writeJSON(path string, v any) error {
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
