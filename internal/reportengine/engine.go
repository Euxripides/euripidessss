package reportengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/entityintel"
	"github.com/etl/backend/internal/fundflow"
	invcache "github.com/etl/backend/internal/investigation/cache"
	"github.com/google/uuid"
)

// CoverageQuerier 查询数据集覆盖（API 层适配 smartdownload Coverage Index）。
type CoverageQuerier func(chainKey, address, dataset string, from, to uint64) (ratio float64, full bool, cert string)

// EntityResolver 解析地址实体（entityintel.Resolver 满足）。
type EntityResolver interface {
	Resolve(ctx context.Context, chainKey, address, investigationID string) (*entityintel.Resolution, error)
}

// Engine 是报告引擎。
type Engine struct {
	store     *Store
	fundFlow  *fundflow.Engine
	entities  EntityResolver
	coverage  CoverageQuerier
	invCache  *invcache.Store
	engineVer string
	polisher  NarrativePolisher
}

// NewEngine 创建报告引擎。
func NewEngine(store *Store, fundFlow *fundflow.Engine, entities EntityResolver, coverage CoverageQuerier, invCache *invcache.Store) *Engine {
	return &Engine{store: store, fundFlow: fundFlow, entities: entities, coverage: coverage, invCache: invCache, engineVer: "v2", polisher: NewDeepSeekPolisher()}
}

// SetPolisher 覆盖叙事润色器（测试用）。
func (e *Engine) SetPolisher(p NarrativePolisher) {
	e.polisher = p
}

// Generate 生成或重新生成综合调查报告（设计 §65、Case A）。
func (e *Engine) Generate(ctx context.Context, investigationID string, depth int) (*GenerateResult, error) {
	return e.GenerateWithOptions(ctx, investigationID, depth, "zh", "")
}

// GenerateWithOptions 生成报告（支持语言与机构模板，P2）。
func (e *Engine) GenerateWithOptions(ctx context.Context, investigationID string, depth int, language, institution string) (*GenerateResult, error) {
	if e.store == nil || e.fundFlow == nil {
		return nil, fmt.Errorf("reportengine: 报告引擎未完整装配")
	}
	chainKey := "bsc"
	root := ""
	goal := "cashout"
	var from, to uint64
	token := ""
	if e.invCache != nil {
		if inv := e.invCache.Get(investigationID); inv != nil {
			if inv.Context.ChainKey != "" {
				chainKey = inv.Context.ChainKey
			}
			root = inv.Context.FocusAddress
			goal = inv.Context.Goal
			from = inv.Context.FromBlock
			to = inv.Context.ToBlock
			if len(inv.Context.Tokens) > 0 {
				token = inv.Context.Tokens[0]
			}
		}
	}
	if root == "" {
		return nil, fmt.Errorf("reportengine: 调查 %s 未设置焦点地址", investigationID)
	}
	if goal == "" {
		goal = "cashout"
	}
	ff, err := e.fundFlow.Analyze(ctx, chainKey, root, token, from, to, goal, depth, investigationID)
	if err != nil {
		return nil, fmt.Errorf("reportengine: 资金流分析失败: %w", err)
	}
	// 实体解析（焦点 + 路径终点 + 沉淀 + 落点）
	entities := map[string]*entityResolution{}
	keyAddresses := []string{root}
	for _, p := range ff.Paths {
		if len(p.Nodes) > 0 {
			keyAddresses = append(keyAddresses, p.Nodes[len(p.Nodes)-1].Address)
		}
	}
	for _, s := range ff.Settlements {
		keyAddresses = append(keyAddresses, s.Address)
	}
	for _, c := range ff.Cashouts {
		keyAddresses = append(keyAddresses, c.DestinationAddress)
	}
	for _, addr := range keyAddresses {
		if _, ok := entities[addr]; ok {
			continue
		}
		if e.entities == nil {
			entities[addr] = &entityResolution{}
			continue
		}
		if res, err := e.entities.Resolve(ctx, chainKey, addr, investigationID); err == nil && res != nil {
			ent := &entityResolution{}
			if res.Entity != nil {
				ent.EntityID = res.Entity.ID
				ent.EntityName = res.Entity.Name
				ent.EntityType = string(res.Entity.EntityType)
				ent.Confidence = res.Entity.Confidence
			}
			for _, l := range res.Labels {
				ent.Labels = append(ent.Labels, l.Label)
			}
			entities[addr] = ent
		} else {
			entities[addr] = &entityResolution{}
		}
	}
	// 数据认证
	coverage := e.datasetCertification(chainKey, root, from, to)
	inputs := &AnalysisInputs{
		ChainKey: chainKey, RootAddress: root, Goal: goal,
		FundFlow: ff, Entities: entities, Coverage: coverage,
	}
	evidence := NewEvidenceIndex()
	findings := BuildFindings(inputs, evidence)
	evidenceRefs(inputs, findings, evidence)
	timeline := BuildTimeline(ff, findings)
	narrative := renderNarrativeLang(inputs, findings, timeline, evidence, language)
	evidenceList := evidence.List()
	snapshot := BuildSnapshot(investigationID, evidenceList, coverage)
	cert := reportCertification(coverage)
	version := e.store.NextVersion(investigationID)
	if version > 1 {
		prev, _, _, _, _ := e.store.Get(investigationID, "report_v"+fmt.Sprintf("%d", version-1))
		if prev != nil && prev.Status != StatusLocked {
			_ = e.store.UpdateStatus(investigationID, version-1, StatusSuperseded)
		}
	}
	now := time.Now().UTC()
	report := &InvestigationReport{
		ID: uuid.NewString(), InvestigationID: investigationID, Version: version,
		Title: fmt.Sprintf("综合调查报告 v%d", version), Goal: goal,
		RootAddress: root, ChainKey: chainKey, TimeRange: TimeRange{},
		Summary: ReportSummary{
			KeyPaths: len(ff.Paths), Cashouts: len(ff.Cashouts),
			Settlements: len(ff.Settlements), ProfitAddresses: countL1(ff.Profit),
			KeyEntities: len(entities), CoverageScore: cert.CoverageScore, KnownGaps: cert.KnownGapCount,
		},
		Sections: narrative.Sections, EvidenceIndex: evidenceList,
		Certification: cert, SnapshotID: snapshot.ID,
		EngineVersion: e.engineVer, TemplateVersion: "v1",
		Language: language, Institution: institution,
		Status: reportStatus(cert), CreatedAt: now, UpdatedAt: now,
	}
	if err := e.store.Save(investigationID, report, snapshot, timeline, findings, evidenceList); err != nil {
		return nil, err
	}
	return &GenerateResult{Report: report, Snapshot: snapshot, Timeline: timeline, Version: version, CacheHit: ff.CacheHit}, nil
}

// List 列出调查报告版本。
func (e *Engine) List(investigationID string) []ReportListEntry {
	return e.store.List(investigationID)
}

// Get 读取报告版本。
func (e *Engine) Get(investigationID, reportID string) (*InvestigationReport, *ReportSnapshot, []TimelineEvent, []Finding, []EvidenceRef) {
	return e.store.Get(investigationID, reportID)
}

// Diff 比较两个版本（设计 §32、§67）。
func (e *Engine) Diff(investigationID, a, b string) *DiffResult {
	ra, _, _, fa, _ := e.store.Get(investigationID, a)
	rb, _, _, fb, _ := e.store.Get(investigationID, b)
	if ra == nil || rb == nil {
		return &DiffResult{ReportA: a, ReportB: b}
	}
	d := &DiffResult{ReportA: a, ReportB: b, ChangedMetrics: map[string][2]string{}}
	byID := map[string]Finding{}
	for _, f := range fa {
		byID[f.ID] = f
	}
	newSet := map[string]bool{}
	for _, f := range fb {
		if old, ok := byID[f.ID]; ok {
			for k, v := range f.Metrics {
				if ov, ok2 := old.Metrics[k]; ok2 && ov != v {
					d.ChangedMetrics[f.ID+"."+k] = [2]string{ov, v}
				}
			}
		} else {
			d.NewFindings = append(d.NewFindings, f.ID)
			newSet[f.ID] = true
		}
	}
	for id := range byID {
		if !newSet[id] {
			d.RemovedFindings = append(d.RemovedFindings, id)
		}
	}
	d.SummaryDiff = map[string]any{
		"paths":     rb.Summary.KeyPaths - ra.Summary.KeyPaths,
		"cashouts":  rb.Summary.Cashouts - ra.Summary.Cashouts,
		"settlements": rb.Summary.Settlements - ra.Summary.Settlements,
		"coverage":  rb.Summary.CoverageScore - ra.Summary.CoverageScore,
	}
	return d
}

// SetStatus 更新报告状态（Lock / Review / Outdated）。
func (e *Engine) SetStatus(investigationID, reportID string, status ReportStatus) error {
	v := reportVersion(reportID)
	if v <= 0 {
		return fmt.Errorf("reportengine: 非法报告版本 %s", reportID)
	}
	return e.store.UpdateStatus(investigationID, v, status)
}

func reportVersion(id string) int {
	id = strings.TrimPrefix(strings.TrimSpace(id), "report_v")
	v, err := strconv.Atoi(id)
	if err != nil {
		return 0
	}
	return v
}

// SignReport 计算报告哈希并写入签名（P2 电子签名/Hash 归档）。
func (e *Engine) SignReport(investigationID, reportID string) (*ReportSignature, error) {
	report, _, _, _, _ := e.store.Get(investigationID, reportID)
	if report == nil {
		return nil, fmt.Errorf("reportengine: 报告不存在")
	}
	clone := *report
	clone.Signature = nil
	payload, err := json.Marshal(clone)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	sig := &ReportSignature{
		Hash: hex.EncodeToString(sum[:]), Method: "SHA256_LOCAL",
		SignedAt: time.Now().UTC(),
	}
	if err := e.store.UpdateSignature(investigationID, report.Version, sig); err != nil {
		return nil, err
	}
	return sig, nil
}

// PolishSection 对指定章节执行 LLM 叙事润色；一致性检查失败则拒绝。
func (e *Engine) PolishSection(ctx context.Context, investigationID, reportID, sectionID string) (string, bool, error) {
	if e.polisher == nil {
		return "", false, fmt.Errorf("reportengine: 未配置 LLM 叙事润色（DEEPSEEK_API_KEY）")
	}
	report, _, _, _, _ := e.store.Get(investigationID, reportID)
	if report == nil {
		return "", false, fmt.Errorf("reportengine: 报告不存在")
	}
	var section *ReportSection
	for i := range report.Sections {
		if report.Sections[i].ID == sectionID {
			section = &report.Sections[i]
			break
		}
	}
	if section == nil {
		return "", false, fmt.Errorf("reportengine: 章节不存在")
	}
	polished, err := e.polisher.Polish(ctx, section.Narrative, section.Findings)
	if err != nil {
		return "", false, err
	}
	if !metricsInNarrative(polished, section.FindingsMetrics()) {
		return "", false, fmt.Errorf("reportengine: 润色结果数字一致性检查失败，已拒绝")
	}
	if err := e.store.UpdateSectionNarrative(investigationID, report.Version, sectionID, polished); err != nil {
		return "", false, err
	}
	return polished, true, nil
}

func (e *Engine) datasetCertification(chainKey, address string, from, to uint64) []DatasetCertification {
	datasets := []string{"transactions", "token_transfers", "internal_transactions", "balances"}
	var out []DatasetCertification
	if e.coverage == nil {
		for _, ds := range datasets {
			out = append(out, DatasetCertification{Dataset: ds, Coverage: 0, Certification: "UNKNOWN", Status: "UNKNOWN"})
		}
		return out
	}
	for _, ds := range datasets {
		ratio, _, cert := e.coverage(chainKey, address, ds, from, to)
		status := "CERTIFIED"
		gaps := 0
		if ratio < 1 {
			status = "PARTIAL"
			gaps = 1
		}
		if cert == "" {
			cert = status
		}
		out = append(out, DatasetCertification{
			Dataset: ds, Coverage: ratio, Certification: cert,
			Provider: "SMART_DOWNLOAD", Status: status, GapCount: gaps,
		})
	}
	return out
}

func reportCertification(coverage []DatasetCertification) ReportCertification {
	cert := ReportCertification{OverallStatus: "READY", DatasetStatuses: coverage}
	total := 0.0
	for _, c := range coverage {
		total += c.Coverage
		if c.Coverage < 1 {
			cert.HasKnownGaps = true
			cert.KnownGapCount += c.GapCount
			cert.OverallStatus = "PARTIAL"
		}
	}
	if len(coverage) > 0 {
		cert.CoverageScore = total / float64(len(coverage))
	}
	return cert
}

func reportStatus(cert ReportCertification) ReportStatus {
	if cert.HasKnownGaps {
		return StatusPartial
	}
	return StatusReady
}
