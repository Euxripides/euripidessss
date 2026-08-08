// Investigation Report Engine V2 前端 API（设计 §65-§68）。
import { getJson, postJson } from "../../api/client";

export interface ReportListEntry {
  id: string;
  version: number;
  title: string;
  goal?: string;
  status: string;
  summary: {
    key_paths: number;
    cashouts: number;
    settlements: number;
    profit_addresses: number;
    key_entities: number;
    coverage_score: number;
    known_gaps: number;
  };
  created_at: string;
}

export interface Finding {
  id: string;
  finding_type: string;
  subject_ids?: string[];
  statement: string;
  metrics?: Record<string, string>;
  confidence: number;
  evidence_ids?: string[];
}

export interface ReportSection {
  id: string;
  type: string;
  title: string;
  findings: Finding[];
  narrative: string;
  evidence_ids?: string[];
  confidence: number;
}

export interface ReportEvidence {
  id: string;
  type: string;
  chain_id?: number;
  address?: string;
  tx_hash?: string;
  dataset_id?: string;
  block_number?: number;
  source_provider?: string;
  certification?: string;
  evidence_hash: string;
}

export interface TimelineEvent {
  id: string;
  timestamp: string;
  type: string;
  subject_ids?: string[];
  summary: string;
  amount?: string;
  token?: string;
  tx_hash?: string;
  importance_score: number;
}

export interface ReportSnapshot {
  id: string;
  investigation_id: string;
  dataset_manifest_hash: string;
  coverage?: Record<string, string>;
  entity_resolver_version: string;
  fund_flow_version: string;
  report_template_version: string;
  created_at: string;
}

export interface InvestigationReport {
  id: string;
  investigation_id: string;
  version: number;
  title: string;
  goal?: string;
  root_address?: string;
  chain_key?: string;
  summary: ReportListEntry["summary"];
  sections: ReportSection[];
  evidence_index: ReportEvidence[];
  certification: {
    overall_status: string;
    dataset_statuses: Array<{
      dataset: string;
      coverage: number;
      certification: string;
      status: string;
      gap_count: number;
    }>;
    coverage_score: number;
    has_known_gaps: boolean;
    known_gap_count: number;
  };
  snapshot_id: string;
  status: string;
  language?: string;
  institution?: string;
  signature?: { hash: string; method: string; signed_at: string };
  created_at: string;
}

export interface ReportDetail {
  report: InvestigationReport;
  snapshot: ReportSnapshot;
  timeline: TimelineEvent[];
  findings: Finding[];
  evidence: ReportEvidence[];
}

export interface GenerateResult {
  report: InvestigationReport;
  snapshot: ReportSnapshot;
  timeline: TimelineEvent[];
  version: number;
  cache_hit: boolean;
}

export async function listReports(investigationId: string): Promise<ReportListEntry[] | null> {
  const { response, payload } = await getJson<{ reports: ReportListEntry[] }>(
    `/api/investigations/${encodeURIComponent(investigationId)}/reports`,
    "报告列表加载失败",
  );
  return response.ok ? payload.reports : null;
}

export async function createReport(
  investigationId: string,
  maxDepth = 4,
  language = "zh",
  institution = "",
): Promise<GenerateResult | null> {
  const { response, payload } = await postJson<GenerateResult>(
    `/api/investigations/${encodeURIComponent(investigationId)}/reports?max_depth=${maxDepth}`,
    { language, institution },
    "报告生成失败",
  );
  return response.ok ? payload : null;
}

export async function getReport(investigationId: string, reportId: string): Promise<ReportDetail | null> {
  const { response, payload } = await getJson<ReportDetail>(
    `/api/investigations/${encodeURIComponent(investigationId)}/reports/${encodeURIComponent(reportId)}`,
    "报告加载失败",
  );
  return response.ok ? payload : null;
}

export async function exportReport(
  investigationId: string,
  reportId: string,
  format: "json" | "xlsx" | "docx" | "pdf" | "case_package",
): Promise<void> {
  const res = await fetch(`/api/investigations/${encodeURIComponent(investigationId)}/reports/${encodeURIComponent(reportId)}/export`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ format }),
  });
  if (!res.ok) {
    throw new Error("导出失败");
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `report_${reportId}.${format === "case_package" ? "zip" : format}`;
  a.click();
  URL.revokeObjectURL(url);
}

export async function diffReports(
  investigationId: string,
  a: string,
  b: string,
): Promise<{ report_a: string; report_b: string; new_findings?: string[]; changed_metrics?: Record<string, [string, string]> } | null> {
  const { response, payload } = await getJson<{
    report_a: string;
    report_b: string;
    new_findings?: string[];
    changed_metrics?: Record<string, [string, string]>;
  }>(`/api/investigations/${encodeURIComponent(investigationId)}/reports/diff/${encodeURIComponent(a)}/${encodeURIComponent(b)}`, "报告差异加载失败");
  return response.ok ? payload : null;
}

export async function reportStatusAction(
  investigationId: string,
  reportId: string,
  action: "lock" | "review" | "outdated",
): Promise<{ detail?: string } | null> {
  const { response, payload } = await postJson<{ detail?: string }>(
    `/api/investigations/${encodeURIComponent(investigationId)}/reports/${encodeURIComponent(reportId)}/${action}`,
    {},
    "报告状态操作失败",
  );
  return response.ok ? payload : null;
}

export async function signReport(investigationId: string, reportId: string): Promise<{ signature?: { hash: string; method: string } } | null> {
  const { response, payload } = await postJson<{ signature?: { hash: string; method: string } }>(
    `/api/investigations/${encodeURIComponent(investigationId)}/reports/${encodeURIComponent(reportId)}/sign`,
    {},
    "报告签名失败",
  );
  return response.ok ? payload : null;
}

export async function polishReport(
  investigationId: string,
  reportId: string,
  sectionId: string,
): Promise<{ narrative?: string; consistency?: boolean } | null> {
  const { response, payload } = await postJson<{ narrative?: string; consistency?: boolean }>(
    `/api/investigations/${encodeURIComponent(investigationId)}/reports/${encodeURIComponent(reportId)}/polish`,
    { section_id: sectionId },
    "报告润色失败",
  );
  return response.ok ? payload : null;
}
