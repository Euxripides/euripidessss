// Fund Flow Intelligence V2 前端 API。
import { postJson } from "../../api/client";

export interface FlowPathNode {
  address: string;
  entity_id?: string;
  entity_name?: string;
  entity_type?: string;
  in_amount?: string;
  block_number?: number;
  edge_type?: string;
  edge_tx_hash?: string;
  token?: string;
}

export interface FlowPath {
  id: string;
  root_address: string;
  chain_key: string;
  goal?: string;
  nodes: FlowPathNode[];
  total_amount?: string;
  hops: number;
  path_type: string;
  score: number;
  confidence: number;
  terminal_type?: string;
  evidence?: Array<{ source_type: string; source_name: string; observation: string; confidence: number }>;
}

export interface ProfitAttribution {
  address: string;
  entity_id?: string;
  entity_name?: string;
  gross_inflow: string;
  gross_outflow: string;
  net_profit: string;
  level: string;
  confidence: number;
}

export interface SettlementResult {
  address: string;
  entity_id?: string;
  entity_name?: string;
  retained_value: string;
  holding_duration_seconds: number;
  settlement_score: number;
  settlement_type: string;
  confidence: number;
}

export interface CashoutResult {
  source_address: string;
  destination_address: string;
  entity_id?: string;
  entity_name?: string;
  tx_hash?: string;
  token?: string;
  amount?: string;
  path_type: string;
  confidence: number;
}

export interface RoundTripResult {
  path_id?: string;
  cycle: string[];
  return_ratio: number;
  score: number;
}

export interface ConservationResult {
  address: string;
  inflow: string;
  outflow: string;
  deviation: number;
  pass: boolean;
  reason?: string;
}

export interface EntityAwareFlowGraph {
  root: string;
  nodes: Array<{
    address: string;
    entity_id?: string;
    entity_name?: string;
    entity_type?: string;
    gross_inflow?: string;
    gross_outflow?: string;
    net_flow?: string;
  }>;
  edges: Array<{ from: string; to: string; token?: string; amount?: string; block_number?: number; edge_type: string }>;
  collapsed_entities: number;
}

export interface FundFlowAnalysis {
  root_address: string;
  chain_key: string;
  goal?: string;
  cache_hit: boolean;
  generated_at: string;
  paths?: FlowPath[];
  profit?: ProfitAttribution[];
  settlements?: SettlementResult[];
  cashouts?: CashoutResult[];
  round_trips?: RoundTripResult[];
  conservation?: ConservationResult[];
  graph?: EntityAwareFlowGraph;
  summary?: Record<string, number>;
}

export async function analyzeFundFlow(input: {
  chain_key: string;
  root_address: string;
  token?: string;
  from_block?: number;
  to_block?: number;
  goal?: string;
  max_depth?: number;
  investigation_id?: string;
}): Promise<FundFlowAnalysis | null> {
  const { response, payload } = await postJson<FundFlowAnalysis>("/api/fund-flow/analyze", input, "资金流智能分析失败");
  return response.ok ? payload : null;
}
