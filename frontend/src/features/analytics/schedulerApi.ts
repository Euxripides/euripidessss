// Smart Download Orchestrator 前端 API 封装（V2.2 智能下载调度）。
// 对应后端 /api/scheduler/* 端点。
import { getJson, postJson } from "../../api/client";

export type Dataset = "balance" | "transactions" | "token_transfer" | "labels";
export type ProviderKind = "rpc" | "sqd" | "aws" | "browser" | "sqd_cloud";
export type ProviderState =
  | "HEALTHY"
  | "DEGRADED"
  | "RATE_LIMITED"
  | "RISK_CONTROLLED"
  | "CIRCUIT_OPEN"
  | "AUTH_BLOCKED"
  | "UNAVAILABLE"
  | "UNSUPPORTED"
  | "NOT_CONFIGURED";

export interface CoverageItem {
  dataset: Dataset;
  have: boolean;
  tx_count: number;
  note: string;
}

export interface CoverageResult {
  chain_key: string;
  addresses: string[];
  items: CoverageItem[];
}

export interface ProviderScore {
  provider: ProviderKind;
  name: string;
  tier?: number;
  state?: ProviderState;
  coverage: number;
  accuracy: number;
  speed: number;
  cost: number;
  reliability: number;
  total: number;
  available: boolean;
  manual_only: boolean;
  reasons: string[];
}

export interface TaskResult {
  job_id?: string;
  output?: string;
  summary?: string;
  rows?: number;
  new_data?: boolean;
}

export interface ProviderAttempt {
  provider: ProviderKind;
  tier: number;
  started_at: string;
  finished_at: string;
  success: boolean;
  state?: ProviderState;
  error?: string;
  rows?: number;
  latency_ms?: number;
}

export interface CloudAdmissionDecision {
  allowed: boolean;
  reason?: string;
  missing_coverage: boolean;
  dataset_supported: boolean;
  normal_providers_exhausted: boolean;
  cloud_eligible: boolean;
  budget_allowed: boolean;
  runtime_available: boolean;
  runtime_state?: string;
  provider_states?: Record<string, string>;
}

export interface CloudTaskInfo {
  job_id?: string;
  decision: CloudAdmissionDecision;
  mode?: string;
  output?: string;
}

export interface SchedulerTask {
  id: string;
  requirement: {
    id: string;
    dataset: Dataset;
    chain_key: string;
    addresses: string[];
    start_date?: string;
    end_date?: string;
    from_block?: number;
    to_block?: number;
    direction?: string;
    depth?: number;
    note?: string;
  };
  candidates: ProviderScore[];
  provider: ProviderKind;
  status: "pending" | "running" | "done" | "failed" | "skipped";
  retries: number;
  job_id?: string;
  progress: number;
  error?: string;
  result?: TaskResult;
  started_at?: string;
  finished_at?: string;
  attempts?: ProviderAttempt[];
  cloud?: CloudTaskInfo;
}

export type PlanStatus =
  | "ANALYZING_REQUIREMENT"
  | "SELECTING_PROVIDER"
  | "BUILDING_PLAN"
  | "EXECUTING"
  | "RETRYING"
  | "FALLBACK"
  | "VALIDATING"
  | "MERGING"
  | "CLOUD_ADMISSION"
  | "CLOUD_QUEUED"
  | "CLOUD_RUNNING"
  | "WAITING_RETRY"
  | "READY_FOR_GRAPH"
  | "FAILED";

export interface SchedulerPlan {
  id: string;
  status: PlanStatus;
  stage_detail?: string;
  tasks: SchedulerTask[];
  budget: {
    max_addresses_per_task: number;
    max_tasks_per_plan: number;
    max_concurrent_plans: number;
    max_retries_per_task: number;
    cloud: CloudBudget;
  };
  created_at: string;
  started_at?: string;
  finished_at?: string;
  error?: string;
}

export interface CloudBudget {
  enabled: boolean;
  daily_limit_minutes: number;
  max_concurrent_workers: number;
  idle_remove_after_minutes: number;
  deploy_timeout_minutes: number;
}

export interface CloudRuntimeStatus {
  state: string;
  mode?: string;
  available: boolean;
  reason?: string;
  queued_jobs: number;
  leased_jobs: number;
  running_job?: string;
  current_chunk?: string;
  rows_exported?: number;
  failure_cooldown_until?: string;
  deployment_key_configured?: boolean;
  r2_configured?: boolean;
}

export interface CloudJob {
  id: string;
  chunk_id?: string;
  plan_id?: string;
  task_id?: string;
  chain_key: string;
  addresses: string[];
  token_contract?: string;
  from_block: number;
  to_block: number;
  priority?: number;
  attempt?: number;
  mode?: string;
  state: string;
  output_dir?: string;
  rows?: number;
  error?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface RegistryStats {
  entries: number;
  rows: number;
  files: number;
  bytes: number;
}

export interface CloudJobsResult {
  jobs: CloudJob[];
  registry: RegistryStats;
}

export interface CloudUsageResult {
  usage: CloudUsage;
  registry: RegistryStats;
  deployment_key_configured: boolean;
}

export interface CloudUsageRecord {
  job_id: string;
  plan_id?: string;
  task_id?: string;
  mode?: string;
  started_at: string;
  finished_at: string;
  duration_minutes: number;
  rows?: number;
  output?: string;
  success: boolean;
}

export interface CloudUsage {
  date: string;
  used_minutes: number;
  records: CloudUsageRecord[];
}

export interface ProviderHealthView {
  kind: ProviderKind;
  name: string;
  tier: number;
  state: ProviderState;
  available: boolean;
  manual_only: boolean;
  reasons?: string[];
  consecutive_failures?: number;
}

export interface ExpandResult {
  plan: SchedulerPlan;
  plan_id: string;
  status: PlanStatus;
  poll: string;
  message: string;
}

export interface StatusResult {
  plan: SchedulerPlan | null;
  running: boolean;
}

export interface PlanRequest {
  requirements: Array<{
    dataset: Dataset;
    chain_key: string;
    addresses: string[];
    start_date?: string;
    end_date?: string;
    from_block?: number;
    to_block?: number;
    direction?: string;
    depth?: number;
    cloud_eligible?: boolean;
  }>;
}

const BASE = "/api/scheduler";

/** 覆盖检查（Coverage Resolver）：判断本地数据集是否已有该地址数据。 */
export async function fetchCoverage(
  chainKey: string,
  addresses: string[],
  datasets?: Dataset[],
): Promise<CoverageResult | null> {
  try {
    const { response, payload } = await postJson<CoverageResult>(
      `${BASE}/coverage`,
      { chain_key: chainKey, addresses, datasets },
      "覆盖检查失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

/** 生成下载计划（分析需求 → Provider 选择）。 */
export async function createSchedulerPlan(body: PlanRequest): Promise<SchedulerPlan | null> {
  const { response, payload } = await postJson<SchedulerPlan>(`${BASE}/plan`, body, "生成下载计划失败");
  return response.ok ? payload : null;
}

/** 执行计划（异步，返回计划初始状态）。 */
export async function runSchedulerPlan(planId: string): Promise<SchedulerPlan | null> {
  const { response, payload } = await postJson<SchedulerPlan>(`${BASE}/run`, { plan_id: planId }, "启动执行失败");
  return response.ok ? payload : null;
}

/** 图联动一站式：覆盖 → 计划 → 执行。 */
export async function expandSmartFill(
  address: string,
  chainKey: string,
  datasets?: Dataset[],
  direction?: string,
  cloudEligible?: boolean,
): Promise<ExpandResult | null> {
  const { response, payload } = await postJson<ExpandResult>(
    `${BASE}/expand`,
    { address, chain_key: chainKey, datasets, direction, cloud_eligible: cloudEligible },
    "启动智能数据补充失败",
  );
  return response.ok ? payload : null;
}

/** 轮询计划状态。 */
export async function fetchSchedulerStatus(planId: string): Promise<StatusResult | null> {
  try {
    const { response, payload } = await getJson<StatusResult>(
      `${BASE}/status?plan_id=${encodeURIComponent(planId)}`,
      "查询计划状态失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

/** 历史计划列表。 */
export async function fetchSchedulerPlans(): Promise<SchedulerPlan[]> {
  try {
    const { response, payload } = await getJson<{ plans: SchedulerPlan[] }>(`${BASE}/plans`, "查询计划列表失败");
    return response.ok ? payload.plans ?? [] : [];
  } catch {
    return [];
  }
}

/** Provider 健康/Tier 快照（V3：/api/scheduler/providers/health）。 */
export async function fetchSchedulerProvidersHealth(): Promise<ProviderHealthView[] | null> {
  try {
    const { response, payload } = await getJson<{ providers: ProviderHealthView[] }>(
      `${BASE}/providers/health`,
      "查询 Provider 健康失败",
    );
    return response.ok ? payload.providers ?? [] : null;
  } catch {
    return null;
  }
}

/** Cloud 运行时/预算/用量（V3：/api/scheduler/cloud/runtime）。 */
export async function fetchCloudRuntime(): Promise<{
  runtime: CloudRuntimeStatus;
  budget: CloudBudget;
  usage: CloudUsage;
} | null> {
  try {
    const { response, payload } = await getJson<{
      runtime: CloudRuntimeStatus;
      budget: CloudBudget;
      usage: CloudUsage;
    }>(`${BASE}/cloud/runtime`, "查询 Cloud 运行时失败");
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

/** Cloud 任务列表 + Registry 汇总（Phase 4）。 */
export async function fetchCloudJobs(): Promise<CloudJobsResult | null> {
  try {
    const { response, payload } = await getJson<CloudJobsResult>(
      `${BASE}/cloud/jobs`,
      "查询 Cloud 任务失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

/** Cloud 用量 + Registry 汇总（Phase 4）。 */
export async function fetchCloudUsage(): Promise<CloudUsageResult | null> {
  try {
    const { response, payload } = await getJson<CloudUsageResult>(
      `${BASE}/cloud/usage`,
      "查询 Cloud 用量失败",
    );
    return response.ok ? payload : null;
  } catch {
    return null;
  }
}

/** 手动触发 Local Sync（Phase 4）。 */
export async function syncCloudData(): Promise<Array<{ chunk_key: string; rows: number; skipped: boolean }> | null> {
  try {
    const { response, payload } = await postJson<{ results: Array<{ chunk_key: string; rows: number; skipped: boolean }> }>(
      `${BASE}/cloud/sync`,
      {},
      "Cloud 数据同步失败",
    );
    return response.ok ? payload.results ?? [] : null;
  } catch {
    return null;
  }
}

/** 数据集中文名。 */
export const DATASET_LABELS: Record<Dataset, string> = {
  transactions: "历史交易",
  token_transfer: "Token 转账",
  balance: "实时余额",
  labels: "标签信息",
};

/** Provider 中文名。 */
export const PROVIDER_LABELS: Record<ProviderKind, string> = {
  sqd: "SQD",
  rpc: "RPC",
  aws: "AWS",
  browser: "Browser",
  sqd_cloud: "应急 Cloud",
};
