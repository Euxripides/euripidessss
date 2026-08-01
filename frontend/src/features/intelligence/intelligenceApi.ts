// 全自动链上调查平台 API 封装
import { getJson, postJson } from "../../api/client";

export interface PlannedTask {
  id: string;
  type: string;
  description: string;
  priority: number;
}

export interface InvestigationPlan {
  target: string;
  objectives: string[];
  tasks: PlannedTask[];
  max_hops: number;
  beam_width: number;
  top_paths: number;
  min_amount: string;
  generated_at: string;
}

export interface FundEdge {
  from: string;
  to: string;
  token: string;
  amount: string;
  tx_hash: string;
  block: number;
  timestamp: number;
  log_index: string;
}

export interface FundPath {
  nodes: string[];
  edges: FundEdge[];
  hops: number;
}

export interface PathScore {
  amount: number;
  time_continuity: number;
  risk: number;
  relation: number;
  entity_penalty: number;
  total: number;
}

export interface RankedPath {
  path: FundPath;
  score: PathScore;
  summary: string;
}

export interface EntityInfo {
  address: string;
  entity: string;
  label?: string;
  risk: number;
  tx_count: number;
}

export interface RiskPattern {
  type: string;
  address: string;
  severity: string;
  detail: string;
  detected_at: string;
}

export interface AIAnalysis {
  summary: string;
  insights: string[];
  suggestions: string[];
  risk_comment: string;
  model: string;
  duration_ms: number;
}

export interface InvestigationMemory {
  investigation_id: string;
  target: string;
  discovered_at: Record<string, string>;
  analyzed_paths: string[];
  ignored_entities: string[];
  completed_tasks: string[];
  conclusions: string[];
  updated_at: string;
}

// ── 调查闭环（V2.1 RC2 智能调查闭环与自主决策引擎）──

export interface InvestigationTask {
  id: string;
  type: string;
  description: string;
  priority: number;
  target?: string;
  status: string;
  result?: string;
  error?: string;
  round: number;
}

export interface Observation {
  id: string;
  type: string;
  address?: string;
  detail: string;
  source: string;
  value: number;
  timestamp: number;
}

export interface DecisionScores {
  path_score: number;
  risk_score: number;
  entity_score: number;
  expansion_score: number;
}

export interface Decision {
  action: string;
  round: number;
  scores: DecisionScores;
  reasons: string[];
  next_targets?: string[];
  made_at: string;
}

export interface RoundRecord {
  round: number;
  decision: string;
  note: string;
  started_at: string;
  finished_at: string;
}

// ── AI 驱动调查（V2.1 RC2 DeepSeek 驱动自主调查 Agent）──

export interface AITask {
  type: string;
  priority: number;
  target?: string;
  reason?: string;
}

export interface AIStrategy {
  strategy: string;
  tasks: AITask[];
  rationale: string;
  confidence: number;
}

export interface AIFinding {
  type: string;
  address?: string;
  detail: string;
  confidence: number;
  evidence: string[];
}

export interface VerifiedFinding {
  finding: AIFinding;
  status: string; // VERIFIED / REJECTED / UNVERIFIED
  reason: string;
  verified_at: string;
}

export interface AIHypothesis {
  id: string;
  title: string;
  description: string;
  confidence: number;
  source: string; // rule / ai
  status: string; // proposed / verifying / evaluated
  tasks: AITask[];
  note?: string;
  created_at: string;
}

export interface AISuggestion {
  action: string; // EXPAND / STOP / DEEP_ANALYSIS / VERIFY
  target?: string;
  reasons: string[];
  confidence: number;
  source: string;
}

export interface Investigation {
  id: string;
  target: string;
  chain_id: string;
  status: string;
  created_at: string;
  updated_at: string;
  plan?: InvestigationPlan;
  paths?: RankedPath[];
  entities?: EntityInfo[];
  patterns?: RiskPattern[];
  ai_analysis?: AIAnalysis;
  memory?: InvestigationMemory;
  report?: { format: string; content: string; filename: string };
  progress: number;
  stage_detail: string;
  error?: string;
  round: number;
  rounds?: RoundRecord[];
  tasks?: InvestigationTask[];
  observations?: Observation[];
  decision?: Decision;
  stop_reason?: string;
  completed_at?: string;
  strategy?: AIStrategy;
  hypotheses?: AIHypothesis[];
  findings?: VerifiedFinding[];
  ai_suggestion?: AISuggestion;
}

export interface IntelligenceConfig {
  max_hops: number;
  beam_width: number;
  top_paths: number;
  min_amount: string;
  use_ai: boolean;
  ai_model: string;
  ai_timeout_ms: number;
  max_expansion: number;
  max_rounds: number;
  max_runtime_ms: number;
  max_addresses: number;
  expansion_threshold: number;
  max_tokens: number;
  max_ai_calls: number;
}

export async function startInvestigation(target: string, chainId?: string, config?: Partial<IntelligenceConfig>): Promise<Investigation | null> {
  const r = await postJson<Investigation>("/api/intelligence/investigations", { target, chain_id: chainId, config }, "启动调查失败");
  return r.payload ?? null;
}

export async function listInvestigations(): Promise<{ total: number; items: Investigation[] }> {
  const r = await getJson<{ total: number; items: Investigation[] }>("/api/intelligence/investigations", "调查列表加载失败");
  return r.payload ?? { total: 0, items: [] };
}

export async function getInvestigation(id: string): Promise<Investigation | null> {
  const r = await getJson<Investigation>(`/api/intelligence/investigations/${id}`, "调查详情加载失败");
  return r.payload ?? null;
}

export async function getReport(id: string, format: "markdown" | "html" | "json"): Promise<string> {
  const resp = await fetch(`/api/intelligence/investigations/${id}/report?format=${format}`);
  if (!resp.ok) throw new Error(`报告获取失败: ${resp.status}`);
  return resp.text();
}

export async function getMemory(id: string): Promise<InvestigationMemory | null> {
  const r = await getJson<InvestigationMemory>(`/api/intelligence/investigations/${id}/memory`, "调查记忆加载失败");
  return r.payload ?? null;
}

export async function getIntelligenceConfig(): Promise<IntelligenceConfig | null> {
  const r = await getJson<IntelligenceConfig>("/api/intelligence/config", "配置加载失败");
  return r.payload ?? null;
}

export async function updateIntelligenceConfig(config: Partial<IntelligenceConfig>): Promise<IntelligenceConfig | null> {
  const r = await postJson<IntelligenceConfig>("/api/intelligence/config", config, "配置更新失败");
  return r.payload ?? null;
}
