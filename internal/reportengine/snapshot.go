package reportengine

import (
	"time"

	"github.com/google/uuid"
)

// BuildSnapshot 构建可重现快照（设计 §27-§30）。
func BuildSnapshot(invID string, evidence []EvidenceRef, coverage []DatasetCertification) *ReportSnapshot {
	now := time.Now().UTC()
	coverageMap := map[string]string{}
	var datasetIDs []string
	for _, c := range coverage {
		datasetIDs = append(datasetIDs, c.Dataset)
		coverageMap[c.Dataset] = c.Certification
	}
	return &ReportSnapshot{
		ID: uuid.NewString(), InvestigationID: invID,
		DatasetIDs: datasetIDs, DatasetManifestHash: ManifestHash(evidence),
		Coverage: coverageMap,
		EntityResolverVersion: "v1.0", FundFlowVersion: "v2",
		PathScoringVersion: "v1", ProfitAttributionVersion: "v1",
		ReportTemplateVersion: "v1", EngineVersion: "v2",
		CreatedAt: now,
	}
}

