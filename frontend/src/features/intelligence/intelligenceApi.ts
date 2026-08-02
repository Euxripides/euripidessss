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
  // Runtime V2（设计 §5 任务模型扩展，向后兼容可选）
  dependencies?: string[];
  max_retries?: number;
  retry_count?: number;
  timeout_sec?: number;
  started_at?: number;
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
  // V2 六维评分（设计 §9）
  behavior_score?: number;
  graph_score?: number;
  identity_score?: number;
  fund_score?: number;
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
  // ── V2 调查请求与六维评分（设计 §4/§9）──
  request?: InvestigationRequest;
  investigation_score?: InvestigationScore;
  // ── V2.1 Evidence Layer（设计 §1）──
  evidence?: Evidence[];
  profit_report?: ProfitReport;
}

// ── V2 调查请求（Investigation Agent Planner V2 §3/§4）──

export type InvestigationMode =
  | "auto"
  | "fund_trace"
  | "profit_analyze"
  | "exchange_entry"
  | "identity_lookup"
  | "risk_scan";

export const INVESTIGATION_MODES: { value: InvestigationMode; label: string }[] = [
  { value: "auto", label: "自动推断" },
  { value: "fund_trace", label: "资金追踪" },
  { value: "profit_analyze", label: "获利分析" },
  { value: "exchange_entry", label: "交易所入口" },
  { value: "identity_lookup", label: "身份线索" },
  { value: "risk_scan", label: "风险扫描" },
];

// 期望结果选项（设计 §4）
export const EXPECTED_RESULT_OPTIONS = [
  "资金流图",
  "资金去向",
  "资金来源",
  "交易所入口",
  "关联钱包",
  "获利检测",
  "身份线索",
  "风险扫描",
];

// 调查目的模板（快速填充）
export const OBJECTIVE_TEMPLATES = [
  "这是一个大额获利地址，寻找最终资金沉淀",
  "识别该地址的交易所入口与提现路径",
  "追踪资金来源与上游关联钱包",
  "检测是否存在洗钱/拆分风险模式",
  "查找地址身份线索与实体归属",
];

export interface InvestigationIntent {
  direction: string; // in / out / both / unknown
  goals: string[];
  mode: InvestigationMode;
  summary: string;
}

export interface InvestigationRequest {
  id: string;
  investigation_id?: string;
  address: string;
  chain_id: string;
  objective: string;
  expected_result: string[];
  mode: InvestigationMode;
  intent?: InvestigationIntent;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface FundScoreDetail {
  balance_points: number;
  profit_points: number;
  holding_points: number;
  total: number;
}

// ── V2.1 Profit Detection V2（设计 §2：估算 + 可信度 + 依据）──

export interface ProfitChecklistItem {
  ok: boolean;
  label: string;
  present: boolean;
}

export interface ProfitReport {
  detected: boolean;
  kind: string;
  tokens?: string[];
  estimate_usd?: number;
  confidence: number;
  checklist?: ProfitChecklistItem[];
  summary: string;
  estimate_note: string;
}

export interface InvestigationScore {
  total: number;
  fund: number;
  behavior: number;
  risk: number;
  entity: number;
  graph: number;
  identity: number;
  fund_detail?: FundScoreDetail;
}

// ── V2.1 Evidence Layer（设计 §1）──

export type EvidenceType = "TRANSACTION" | "ADDRESS" | "TIME" | "PATH" | "RISK" | "PROFIT";

export interface Evidence {
  id: string;
  investigation_id: string;
  task_id?: string;
  evidence_type: EvidenceType;
  address?: string;
  tx_hash?: string;
  block_number?: number;
  token?: string;
  amount?: string;
  detail: string;
  confidence: number;
  created_at: string;
}

export const EVIDENCE_TYPE_LABEL: Record<EvidenceType, string> = {
  TRANSACTION: "交易证据",
  ADDRESS: "地址证据",
  TIME: "时间证据",
  PATH: "路径证据",
  RISK: "风险证据",
  PROFIT: "获利证据",
};

// getInvestigationEvidence 查询调查证据链（V2.1）。
export async function getInvestigationEvidence(id: string): Promise<{ total: number; evidence: Evidence[] } | null> {
  const r = await getJson<{ total: number; evidence: Evidence[] }>(`/api/investigation/${id}/evidence`, "调查证据加载失败");
  return r.payload ?? null;
}

export interface CreateInvestigationInput {
  address: string;
  chain?: string;
  objective?: string;
  expected_result?: string[];
  mode?: InvestigationMode;
}

export interface CreateInvestigationResult {
  request: InvestigationRequest;
  investigation: Investigation;
}

// createInvestigation 通过 V2 入口创建调查请求并启动调查。
export async function createInvestigation(input: CreateInvestigationInput): Promise<CreateInvestigationResult | null> {
  const r = await postJson<CreateInvestigationResult>("/api/investigation/create", input, "创建调查失败");
  return r.payload ?? null;
}

// getInvestigationPlan 查询调查计划（V2 端点）。
export async function getInvestigationPlan(id: string): Promise<{ status: string; plan?: InvestigationPlan } | null> {
  const r = await getJson<{ status: string; plan?: InvestigationPlan }>(`/api/investigation/${id}/plan`, "调查计划加载失败");
  return r.payload ?? null;
}

// getInvestigationTasks 查询调查任务（V2 端点）。
export async function getInvestigationTasks(id: string): Promise<{ status: string; tasks: InvestigationTask[] } | null> {
  const r = await getJson<{ status: string; tasks: InvestigationTask[] }>(`/api/investigation/${id}/tasks`, "调查任务加载失败");
  return r.payload ?? null;
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

// subscribeInvestigation 通过 SSE 订阅调查进度（#7 优化：替代轮询）。
// 返回取消函数；调查终态或连接断开时自动结束。
export function subscribeInvestigation(id: string, onUpdate: (inv: Investigation) => void): () => void {
  const source = new EventSource(`/api/intelligence/events?id=${encodeURIComponent(id)}`);
  source.addEventListener("investigation", (ev) => {
    try {
      const inv = JSON.parse((ev as MessageEvent).data) as Investigation;
      onUpdate(inv);
      if (inv.status === "COMPLETED" || inv.status === "FAILED") {
        source.close();
      }
    } catch {
      // 忽略畸形事件
    }
  });
  source.onerror = () => source.close();
  return () => source.close();
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
