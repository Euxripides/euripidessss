// 地址关系图升级 V2.0 核心逻辑（Phase 1：全局/聚焦图、方向、深度、子图计算）
//
// 纯函数模块：输入完整图谱数据，输出聚焦子图（节点/边），前端渲染层不包含
// 业务计算逻辑（与 flowElements 同模式）。聚焦图必须从完整数据计算，
// 不能只基于全局 Top 600 边（设计 §2.2）。

import type { GraphData } from "./analyticsApi";

// ── 类型 ──

export type FocusDirection = "all" | "upstream" | "downstream" | "both";
export type FocusDepth = 1 | 2 | 3 | 0; // 0 = 全部

export interface FocusSelection {
  address: string;
  direction: FocusDirection;
  depth: FocusDepth;
}

export interface EnhancedNodeData {
  address: string;
  kind: string; // 地址/合约
  risk: number;
  degree: number;
  isRoot: boolean;
  layer: number; // 相对聚焦地址的层（0 = 中心）
  inAmount: number; // 图边流入（当前数据集）
  outAmount: number; // 图边流出
  inCount: number; // 流入笔数
  outCount: number; // 流出笔数
  upstream: number; // 直接上游数
  downstream: number; // 直接下游数
  [key: string]: unknown; // ReactFlow Node.data 需要索引签名
}

export interface EnhancedEdgeData {
  source: string;
  target: string;
  kind: string;
  token?: string;
  amount: number;
  historicalValueUSDT?: number;
  valuationStatus?: string;
  txCount: number;
  discovery: boolean; // 是否发现路径（聚焦 BFS 主干）
  [key: string]: unknown; // ReactFlow Edge.data 需要索引签名
}

export interface FocusGraph {
  nodes: Array<{ id: string; type: string; position: { x: number; y: number }; data: EnhancedNodeData }>;
  edges: Array<{ id: string; source: string; target: string; data: EnhancedEdgeData }>;
  center: string;
  layerCount: number;
  totalNodes: number; // 完整图节点数（统计用）
  totalEdges: number; // 完整图边数
  truncated: boolean; // 是否截断（深度/上限限制导致）
}

// ── 常量 ──

export const FOCUS_NODE_LIMIT = 200; // 聚焦边默认 ≤200（设计 §25）
export const FOCUS_MAX_EDGES = 500; // 聚焦边最大 500
export const GLOBAL_EDGE_LIMIT = 600; // 全局边默认 ≤600

// ── 计算 ──

export function parseAmount(value?: string): number {
  if (!value) return 0;
  const n = Number.parseFloat(value);
  return Number.isFinite(n) ? n : 0;
}

function isAddressLike(id: string): boolean {
  return /^0x[0-9a-fA-F]{40}$/.test(id.trim());
}

export function normalizeAddress(input: string): string {
  return input.trim().toLowerCase();
}

export function isValidAddress(input: string): boolean {
  return isAddressLike(input.trim());
}

export function isValidTxHash(input: string): boolean {
  return /^0x[0-9a-fA-F]{64}$/.test(input.trim());
}

/**
 * buildFocusGraph 从完整图谱数据计算聚焦子图（设计 §2.2/§5）。
 * - 以 address 为中心，按 direction 过滤上游/下游/双向；
 * - 按 depth 限制层数（BFS）；0 = 全部层；
 * - 节点数据含图边流入/流出/笔数/上下游计数（统计口径与设计 §9 一致）；
 * - truncated 表示因深度/节点上限截断。
 */
export function buildFocusGraph(graph: GraphData, selection: FocusSelection): FocusGraph {
  const center = normalizeAddress(selection.address);
  const nodeMap = new Map(graph.nodes.map((n) => [n.id.toLowerCase(), n]));
  // 边索引：以归一化地址为 key
  const adjacency = new Map<string, GraphData["edges"][number][]>();
  for (const edge of graph.edges) {
    const s = edge.source.toLowerCase();
    const t = edge.target.toLowerCase();
    adjacency.set(s, [...(adjacency.get(s) ?? []), edge]);
    adjacency.set(t, [...(adjacency.get(t) ?? []), edge]);
  }

  const layers = new Map<string, number>([[center, 0]]);
  const visible = new Set<string>([center]);
  const discoveryEdges = new Set<string>();
  const queue: string[] = [center];
  let truncated = false;

  const maxLayer = selection.depth === 0 ? Number.POSITIVE_INFINITY : selection.depth;

  while (queue.length && visible.size < FOCUS_NODE_LIMIT) {
    const current = queue.shift() as string;
    const currentLayer = layers.get(current) ?? 0;
    if (currentLayer >= maxLayer) continue;
    const incident = adjacency.get(current) ?? [];
    for (const edge of incident) {
      const outgoing = edge.source.toLowerCase() === current;
      const next = (outgoing ? edge.target : edge.source).toLowerCase();
      if (next === current) continue;
      // 方向过滤
      if (selection.direction === "upstream" && outgoing) continue;
      if (selection.direction === "downstream" && !outgoing) continue;
      if (visible.has(next)) continue;
      if (isAddressLike(next) && !nodeMap.has(next)) continue; // 仅含已知节点
      const nextLayer = currentLayer + 1;
      layers.set(next, nextLayer);
      visible.add(next);
      discoveryEdges.add(edgeKey(edge));
      queue.push(next);
      if (visible.size >= FOCUS_NODE_LIMIT) {
        truncated = true;
        break;
      }
    }
    if (truncated) break;
  }

  // 深度截断标记：存在未探索层（BFS 因深度终止）。
  // should-fix：按 direction 过滤未探索邻居——upstream 模式只把上游方向
  // 邻居计入截断，避免把反向邻居误报为 truncated。
  if (selection.depth > 0) {
    for (const addr of visible) {
      const layer = layers.get(addr) ?? 0;
      if (layer >= selection.depth) {
        const incident = adjacency.get(addr) ?? [];
        for (const edge of incident) {
          const outgoing = edge.source.toLowerCase() === addr;
          if (selection.direction === "upstream" && outgoing) continue;
          if (selection.direction === "downstream" && !outgoing) continue;
          const next = (outgoing ? edge.target : edge.source).toLowerCase();
          if (!visible.has(next) && nodeMap.has(next)) {
            truncated = true;
            break;
          }
        }
        if (truncated) break;
      }
    }
  }

  // 节点统计（从完整数据计算，设计 §9）
  const nodes = [...visible].map((addr) => {
    const meta = nodeMap.get(addr);
    const incident = adjacency.get(addr) ?? [];
    let inAmount = 0;
    let outAmount = 0;
    let inCount = 0;
    let outCount = 0;
    const upstreamSet = new Set<string>();
    const downstreamSet = new Set<string>();
    for (const edge of incident) {
      const outgoing = edge.source.toLowerCase() === addr;
      const counterparty = (outgoing ? edge.target : edge.source).toLowerCase();
      if (outgoing) {
        outAmount += parseAmount(edge.amount);
        outCount += edge.tx_count ?? 1;
        downstreamSet.add(counterparty);
      } else {
        inAmount += parseAmount(edge.amount);
        inCount += edge.tx_count ?? 1;
        upstreamSet.add(counterparty);
      }
    }
    return {
      id: addr,
      type: "address",
      position: { x: 0, y: 0 }, // 布局在 render 层计算（Dagre）
      data: {
        address: addr,
        kind: meta?.type === "contract" ? "合约" : "地址",
        risk: meta?.risk_score ?? 0,
        degree: meta?.degree ?? incident.length,
        isRoot: addr === center,
        layer: layers.get(addr) ?? 0,
        inAmount,
        outAmount,
        inCount,
        outCount,
        upstream: upstreamSet.size,
        downstream: downstreamSet.size,
      } satisfies EnhancedNodeData,
    };
  });

  // 边（聚焦子图内的可见边；发现路径优先，截断到 FOCUS_MAX_EDGES）
  const edgeSet = new Map<string, GraphData["edges"][number]>();
  for (const edge of graph.edges) {
    const s = edge.source.toLowerCase();
    const t = edge.target.toLowerCase();
    if (!visible.has(s) || !visible.has(t)) continue;
    const key = edgeKey(edge);
    const current = edgeSet.get(key);
    if (!current || edgeImportance(edge) > edgeImportance(current)) edgeSet.set(key, edge);
  }
  const sortedEdges = [...edgeSet.values()]
    .sort((a, b) => {
      const aDiscovery = Number(discoveryEdges.has(edgeKey(a)));
      const bDiscovery = Number(discoveryEdges.has(edgeKey(b)));
      return bDiscovery - aDiscovery || edgeImportance(b) - edgeImportance(a);
    })
    .slice(0, FOCUS_MAX_EDGES);
  if (edgeSet.size > FOCUS_MAX_EDGES) truncated = true;

  const edges = sortedEdges.map((edge) => ({
    id: `${edgeKey(edge)}`,
    source: edge.source.toLowerCase(),
    target: edge.target.toLowerCase(),
    data: {
      source: edge.source.toLowerCase(),
      target: edge.target.toLowerCase(),
      kind: edge.kind,
      token: edge.token,
      amount: parseAmount(edge.amount),
      historicalValueUSDT: parseAmount(edge.historical_value_usdt),
      valuationStatus: edge.valuation_status,
      txCount: edge.tx_count ?? 1,
      discovery: discoveryEdges.has(edgeKey(edge)),
    } satisfies EnhancedEdgeData,
  }));

  // 层数（最大层 - 最小层 + 1）
  let minLayer = 0;
  let maxLayerSeen = 0;
  for (const addr of visible) {
    const layer = layers.get(addr) ?? 0;
    if (layer < minLayer) minLayer = layer;
    if (layer > maxLayerSeen) maxLayerSeen = layer;
  }

  return {
    nodes,
    edges,
    center,
    layerCount: maxLayerSeen - minLayer + 1,
    totalNodes: graph.nodes.length,
    totalEdges: graph.edges.length,
    truncated,
  };
}

function edgeKey(edge: { source: string; target: string; kind: string }): string {
  return `${edge.source.toLowerCase()}->${edge.target.toLowerCase()}:${edge.kind}`;
}

function edgeImportance(edge: GraphData["edges"][number]): number {
  return Number(edge.tx_count ?? 0) * 100 + parseAmount(edge.amount);
}
