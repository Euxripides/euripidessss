// Package reportengine 实现 Investigation Report Engine V2（设计 V1.0）：
// Evidence Timeline + Case Narrative + Exportable Case Package + Reproducible Investigation。
// 核心原则：每一个结论都必须能追溯到具体地址、交易、Dataset、时间范围、算法版本和证据。
package reportengine

import "time"

// ReportStatus 报告状态（设计 §26）。
type ReportStatus string

const (
	StatusDraft      ReportStatus = "DRAFT"
	StatusGenerating ReportStatus = "GENERATING"
	StatusReady      ReportStatus = "READY"
	StatusPartial    ReportStatus = "PARTIAL"
	StatusReviewed   ReportStatus = "REVIEWED"
	StatusLocked     ReportStatus = "LOCKED"
	StatusSuperseded ReportStatus = "SUPERSEDED"
	StatusOutdated   ReportStatus = "OUTDATED"
)

// TimeRange 时间范围。
type TimeRange struct {
	From time.Time `json:"from,omitempty"`
	To   time.Time `json:"to,omitempty"`
}

// ReportSummary 报告摘要。
type ReportSummary struct {
	KeyPaths        int     `json:"key_paths"`
	Cashouts        int     `json:"cashouts"`
	Settlements     int     `json:"settlements"`
	ProfitAddresses int     `json:"profit_addresses"`
	KeyEntities     int     `json:"key_entities"`
	CoverageScore   float64 `json:"coverage_score"`
	KnownGaps       int     `json:"known_gaps"`
}

// DatasetCertification 数据集认证（设计 §23-§25）。
type DatasetCertification struct {
	Dataset       string  `json:"dataset"`
	Coverage      float64 `json:"coverage"`
	Certification string  `json:"certification"`
	Provider      string  `json:"provider,omitempty"`
	Status        string  `json:"status"`
	GapCount      int     `json:"gap_count"`
}

// ReportCertification 报告认证（设计 §25）。
type ReportCertification struct {
	OverallStatus   string                 `json:"overall_status"`
	DatasetStatuses []DatasetCertification `json:"dataset_statuses,omitempty"`
	CoverageScore   float64                `json:"coverage_score"`
	HasKnownGaps    bool                   `json:"has_known_gaps"`
	KnownGapCount   int                    `json:"known_gap_count"`
}

// Finding 结构化结论（设计 §6-§7）。
type Finding struct {
	ID           string            `json:"id"`
	FindingType  string            `json:"finding_type"`
	SubjectIDs   []string          `json:"subject_ids,omitempty"`
	Statement    string            `json:"statement"`
	Metrics      map[string]string `json:"metrics,omitempty"`
	Confidence   float64           `json:"confidence"`
	EvidenceIDs  []string          `json:"evidence_ids,omitempty"`
}

// ReportSection 报告章节（设计 §5）。
type ReportSection struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	Findings    []Finding `json:"findings,omitempty"`
	Narrative   string    `json:"narrative"`
	EvidenceIDs []string  `json:"evidence_ids,omitempty"`
	Confidence  float64   `json:"confidence"`
}

// FindingsMetrics 返回章节全部 Finding 的指标合并（叙事一致性检查用）。
func (s ReportSection) FindingsMetrics() map[string]string {
	out := map[string]string{}
	for _, f := range s.Findings {
		for k, v := range f.Metrics {
			out[k+"|"+f.ID] = v
		}
	}
	return out
}

// EvidenceRef 证据引用（设计 §10-§11）。
type EvidenceRef struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	ChainID        int64      `json:"chain_id,omitempty"`
	Address        string     `json:"address,omitempty"`
	TxHash         string     `json:"tx_hash,omitempty"`
	DatasetID      string     `json:"dataset_id,omitempty"`
	BlockNumber    uint64     `json:"block_number,omitempty"`
	Timestamp      *time.Time `json:"timestamp,omitempty"`
	SourcePath     string     `json:"source_path,omitempty"`
	SourceProvider string     `json:"source_provider,omitempty"`
	Certification  string     `json:"certification,omitempty"`
	EvidenceHash   string     `json:"evidence_hash"`
}

// TimelineEvent 时间线事件（设计 §13-§16）。
type TimelineEvent struct {
	ID              string    `json:"id"`
	Timestamp       time.Time `json:"timestamp"`
	Type            string    `json:"type"`
	SubjectIDs      []string  `json:"subject_ids,omitempty"`
	Summary         string    `json:"summary"`
	Amount          string    `json:"amount,omitempty"`
	Token           string    `json:"token,omitempty"`
	TxHash          string    `json:"tx_hash,omitempty"`
	EvidenceIDs     []string  `json:"evidence_ids,omitempty"`
	ImportanceScore float64   `json:"importance_score"`
}

// ReportSnapshot 可重现快照（设计 §27-§30）。
type ReportSnapshot struct {
	ID                     string            `json:"id"`
	InvestigationID        string            `json:"investigation_id"`
	DatasetIDs             []string          `json:"dataset_ids,omitempty"`
	DatasetManifestHash    string            `json:"dataset_manifest_hash"`
	Coverage               map[string]string `json:"coverage,omitempty"`
	EntityResolverVersion  string            `json:"entity_resolver_version"`
	FundFlowVersion        string            `json:"fund_flow_version"`
	PathScoringVersion     string            `json:"path_scoring_version"`
	ProfitAttributionVersion string          `json:"profit_attribution_version"`
	ReportTemplateVersion  string            `json:"report_template_version"`
	EngineVersion          string            `json:"engine_version"`
	CreatedAt              time.Time         `json:"created_at"`
}

// ReportSignature 是报告电子签名（P2：Hash 归档）。
type ReportSignature struct {
	Hash     string    `json:"hash"`
	Method   string    `json:"method"`
	SignedAt time.Time `json:"signed_at"`
}

// InvestigationReport 最终报告（设计 §4）。
type InvestigationReport struct {
	ID                 string               `json:"id"`
	InvestigationID    string               `json:"investigation_id"`
	Version            int                  `json:"version"`
	Title              string               `json:"title"`
	Goal               string               `json:"goal,omitempty"`
	RootAddress        string               `json:"root_address,omitempty"`
	ChainKey           string               `json:"chain_key,omitempty"`
	TimeRange          TimeRange            `json:"time_range,omitempty"`
	Summary            ReportSummary        `json:"summary"`
	Sections           []ReportSection      `json:"sections,omitempty"`
	EvidenceIndex      []EvidenceRef        `json:"evidence_index,omitempty"`
	Certification      ReportCertification  `json:"certification"`
	SnapshotID         string               `json:"snapshot_id"`
	EngineVersion      string               `json:"engine_version"`
	TemplateVersion    string               `json:"template_version"`
	Status             ReportStatus         `json:"status"`
	Language           string               `json:"language,omitempty"`
	Institution        string               `json:"institution,omitempty"`
	Signature          *ReportSignature     `json:"signature,omitempty"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
}

// ReportListEntry 报告列表条目。
type ReportListEntry struct {
	ID              string       `json:"id"`
	Version         int          `json:"version"`
	Title           string       `json:"title"`
	Goal            string       `json:"goal,omitempty"`
	Status          ReportStatus `json:"status"`
	Summary         ReportSummary `json:"summary"`
	CreatedAt       time.Time    `json:"created_at"`
}

// GenerateResult 生成接口返回。
type GenerateResult struct {
	Report      *InvestigationReport `json:"report"`
	Snapshot    *ReportSnapshot      `json:"snapshot"`
	Timeline    []TimelineEvent      `json:"timeline,omitempty"`
	Version     int                  `json:"version"`
	CacheHit    bool                 `json:"cache_hit"`
}

// DiffResult 报告差异（设计 §32）。
type DiffResult struct {
	ReportA          string            `json:"report_a"`
	ReportB          string            `json:"report_b"`
	NewFindings      []string          `json:"new_findings,omitempty"`
	RemovedFindings  []string          `json:"removed_findings,omitempty"`
	ChangedMetrics   map[string][2]string `json:"changed_metrics,omitempty"`
	SummaryDiff      map[string]any    `json:"summary_diff,omitempty"`
}
