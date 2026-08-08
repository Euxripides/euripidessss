// Entity Intelligence Layer V1 前端 API（设计 §53-§56）。
import { getJson, postJson } from "../../api/client";

export interface EvidenceRef {
  evidence_id: string;
  source_type: string;
  source_name: string;
  source_uri?: string;
  observation: string;
  collected_at: string;
  confidence: number;
}

export interface AddressLabel {
  chain_id: number;
  address: string;
  label: string;
  entity_id?: string;
  scope: string;
  confidence: number;
  evidence_ids?: string[];
  resolver_version: string;
}

export interface Entity {
  id: string;
  name: string;
  entity_type: string;
  chain_ids?: number[];
  addresses?: string[];
  confidence: number;
  evidence_ids?: string[];
  source: string;
  version?: number;
}

export interface ConflictEntry {
  id: string;
  address: string;
  source_a: string;
  source_b: string;
  entity_a: string;
  entity_b: string;
  resolved?: boolean;
}

export interface AddressFeature {
  tx_count: number;
  counterparty_count: number;
  inflow?: string;
  outflow?: string;
  net_retained?: string;
  sweep_ratio: number;
  dormancy_score: number;
  is_contract: boolean;
  risk_score: number;
  recent_24h: number;
  recent_7d: number;
  recent_30d: number;
}

export interface EntityResolution {
  address: string;
  chain_key: string;
  chain_id: number;
  entity?: Entity;
  labels: AddressLabel[];
  cluster_ids?: string[];
  confidence: number;
  confidence_tier: string;
  evidence: EvidenceRef[];
  conflicts?: ConflictEntry[];
  feature?: AddressFeature;
  cache_hit: boolean;
  resolved_at: string;
}

export interface InvestigationLead {
  id: string;
  investigation_id: string;
  address: string;
  entity_id?: string;
  entity_name?: string;
  lead_type: string;
  transaction_hash?: string;
  block_number?: number;
  token?: string;
  amount?: string;
  evidence_ids?: string[];
  confidence: number;
  created_at: string;
}

export interface ManualLabel {
  id: string;
  investigation_id: string;
  chain_key: string;
  address: string;
  label: string;
  reason?: string;
  confidence: number;
}

export interface EntityStats {
  entities: number;
  clusters: number;
  addresses: number;
  evidence: number;
  leads: number;
  cache_hits: number;
  cache_misses: number;
  cache_hit_rate: number;
  known_label_hits: number;
}

export async function resolveEntity(
  chainKey: string,
  address: string,
  investigationId?: string,
): Promise<EntityResolution | null> {
  const q = new URLSearchParams({ chain: chainKey, address });
  if (investigationId) q.set("investigation_id", investigationId);
  const { response, payload } = await getJson<EntityResolution>(
    `/api/entity/resolve?${q.toString()}`,
    "实体解析失败",
  );
  return response.ok ? payload : null;
}

export async function resolveEntityBatch(input: {
  chain_key: string;
  investigation_id?: string;
  addresses: string[];
}): Promise<{ total: number; results: EntityResolution[] } | null> {
  const { response, payload } = await postJson<{ total: number; results: EntityResolution[] }>(
    "/api/entity/resolve/batch",
    input,
    "批量实体解析失败",
  );
  return response.ok ? payload : null;
}

export async function getEntityGraph(entityId: string, chainKey: string): Promise<unknown | null> {
  const { response, payload } = await getJson<unknown>(
    `/api/entity/${encodeURIComponent(entityId)}/graph?chain=${encodeURIComponent(chainKey)}`,
    "实体图加载失败",
  );
  return response.ok ? payload : null;
}

export async function addManualLabel(input: {
  investigation_id: string;
  chain_key: string;
  address: string;
  label: string;
  reason?: string;
}): Promise<{ manual_label: ManualLabel } | null> {
  const { response, payload } = await postJson<{ manual_label: ManualLabel }>(
    "/api/entity/labels",
    input,
    "添加案件标签失败",
  );
  return response.ok ? payload : null;
}

export async function getEntityLeads(investigationId: string): Promise<{
  investigation_id: string;
  total: number;
  leads: InvestigationLead[];
} | null> {
  const { response, payload } = await getJson<{
    investigation_id: string;
    total: number;
    leads: InvestigationLead[];
  }>(`/api/investigations/${encodeURIComponent(investigationId)}/entity-leads`, "实体线索加载失败");
  return response.ok ? payload : null;
}

export async function getEntityStats(): Promise<EntityStats | null> {
  const { response, payload } = await getJson<EntityStats>("/api/entity/stats", "实体统计加载失败");
  return response.ok ? payload : null;
}

export async function searchEntities(query: string): Promise<{ total: number; items: Entity[] } | null> {
  const { response, payload } = await getJson<{ total: number; items: Entity[] }>(
    `/api/entity/search?q=${encodeURIComponent(query)}`,
    "实体搜索失败",
  );
  return response.ok ? payload : null;
}

export const ENTITY_TYPE_LABELS: Record<string, string> = {
  EXCHANGE: "交易所",
  DEX: "DEX",
  BRIDGE: "跨链桥",
  CEX_DEPOSIT: "交易所入金",
  CEX_HOT_WALLET: "交易所热钱包",
  CEX_COLD_WALLET: "交易所冷钱包",
  PAYMENT_SERVICE: "支付服务",
  CUSTODIAN: "托管服务",
  MARKET_MAKER: "做市商",
  PROJECT_TREASURY: "项目金库",
  PROJECT_DEPLOYER: "项目部署者",
  CONTRACT: "合约",
  TOKEN_CONTRACT: "Token 合约",
  ROUTER: "路由器",
  MULTISIG: "多签",
  RELAYER: "Relayer",
  BOT: "Bot",
  MEV: "MEV",
  MINER_VALIDATOR: "矿工/验证者",
  MIXER: "混币器",
  SCAM: "诈骗",
  PHISHING: "钓鱼",
  EXPLOIT: "攻击",
  UNKNOWN_SERVICE: "未知服务",
  UNKNOWN_ENTITY: "未知实体",
  INDIVIDUAL_UNKNOWN: "独立未知地址",
};

export const CONFIDENCE_TIER_LABELS: Record<string, string> = {
  CONFIRMED: "已确认",
  HIGH: "高可信",
  MEDIUM: "中等可信",
  LOW: "低可信",
  UNVERIFIED: "未验证",
};
