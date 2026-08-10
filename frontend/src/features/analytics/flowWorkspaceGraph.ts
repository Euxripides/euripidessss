// 地址关系图 V2.0 UI 整改 — 渲染层布局与视觉辅助（纯函数）
//
// 只负责把 buildFocusGraph / 分层算法 的输出转换为 ReactFlow 元素：
// - 关系语义（selected/upstream/downstream/exchange/global）只在这里计算；
// - 布局坐标只在这里计算（聚焦 ±500 每层，全局 460 每层）；
// - 不修改 graphUpgrade.ts 的 BFS/方向/深度逻辑。

import { MarkerType, type Edge, type Node } from "@xyflow/react";

import type { GraphData } from "./analyticsApi";
import type { EnhancedEdgeData, EnhancedNodeData, FocusDepth, FocusDirection } from "./graphUpgrade";
import { buildFocusGraph, FOCUS_MAX_EDGES, GLOBAL_EDGE_LIMIT, parseAmount } from "./graphUpgrade";

export type GraphKind = "all" | "TRANSFER" | "INTERACTION" | "COMMON_COUNTERPARTY";

/** 节点/边关系语义（设计 §5.3/§11.3） */
export type WorkspaceRelation = "selected" | "upstream" | "downstream" | "exchange" | "global" | "cross";

export type WorkspaceMode =
  | { kind: "global"; kindFilter: GraphKind }
  | { kind: "focus"; kindFilter: GraphKind; center: string; direction: FocusDirection; depth: FocusDepth };

export interface WorkspaceGraph {
  nodes: Array<{ id: string; type: string; data: EnhancedNodeData }>;
  edges: Array<{ id: string; source: string; target: string; data: EnhancedEdgeData }>;
  center: string;
  truncated: boolean;
  totalNodes: number;
  totalEdges: number;
}

/** 语义色（设计 §5.3 表格色值，禁止散落其他色值） */
export const DANGER_COLOR = "#ef5b67"; // 危险/异常（图例风险地址使用）

export const RELATION_COLORS: Record<WorkspaceRelation, string> = {
  selected: "#26d9e8", // 选中地址
  upstream: "#3f97ff", // 上游来源
  downstream: "#f59e32", // 下游去向
  exchange: "#d7b34d", // 交易所公开标记
  global: "#3a536c", // 非聚焦关系
  cross: "#3a536c", // 非聚焦关系（焦点间交叉边）
};

const DARK_COLORS: Record<WorkspaceRelation, string> = {
  selected: "#26d9e8",
  upstream: "#3f97ff",
  downstream: "#f59e32",
  exchange: "#d7b34d",
  global: "#3a536c",
  cross: "#3a536c",
};

const LIGHT_COLORS: Record<WorkspaceRelation, string> = {
  selected: "#2563eb",
  upstream: "#0ea5e9",
  downstream: "#f59e0b",
  exchange: "#10b981",
  global: "#cbd5e1",
  cross: "#cbd5e1",
};

/** 切换关系边配色（白底工作台用浅色系，深色工作台用原色系）。 */
export function setGraphColorScheme(light: boolean) {
  const palette = light ? LIGHT_COLORS : DARK_COLORS;
  (Object.keys(palette) as WorkspaceRelation[]).forEach((k) => {
    RELATION_COLORS[k] = palette[k];
  });
}

export function riskTone(score: number): "risk" | "warning" | "normal" {
  if (score >= 60) return "risk";
  if (score >= 30) return "warning";
  return "normal";
}

export function directionLabel(direction: FocusDirection): string {
  switch (direction) {
    case "upstream": return "上游";
    case "downstream": return "下游";
    case "both": return "前后";
    default: return "全局视图";
  }
}

/** 紧凑金额（节点内） */
export function fmtAmount(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}k`;
  return value.toFixed(0);
}

/** 边标签金额：千分位 + 最多 6 位小数（如 469,465.987425） */
export function fmtEdgeAmount(value: number): string {
  if (!Number.isFinite(value)) return "0";
  return value.toLocaleString("en-US", { maximumFractionDigits: 6 });
}

/**
 * 构建工作区图：聚焦模式从完整数据 BFS（graphUpgrade），全局模式走分层算法，
 * 然后统一补充关系语义。exchange 关系仅在数据带有公开标签证据时产生
 * （当前数据集无此字段，预留语义位）。
 */
export function buildWorkspaceGraph(graph: GraphData, mode: WorkspaceMode): WorkspaceGraph {
  const safeGraph: GraphData = {
    nodes: Array.isArray(graph?.nodes) ? graph.nodes : [],
    edges: Array.isArray(graph?.edges) ? graph.edges : [],
  };
  const kindEdges = mode.kindFilter === "all" ? safeGraph.edges : safeGraph.edges.filter((e) => e.kind === mode.kindFilter);

  if (mode.kind === "focus") {
    const focused = buildFocusGraph({ ...safeGraph, edges: kindEdges }, {
      address: mode.center,
      direction: mode.direction,
      depth: mode.depth,
    });
    // 方向感知层号（设计 §9.1）：上游节点层号为负（放左侧），下游为正（右侧）。
    // buildFocusGraph 的 BFS 层号恒为正，这里在渲染层按边方向修正符号，不改动 BFS 逻辑。
    const signedLayers = signFocusLayers(focused, mode.direction);
    return {
      nodes: focused.nodes.map((node) => {
        const layer = signedLayers.get(node.id) ?? node.data.layer;
        const data = { ...node.data, layer };
        return {
          id: node.id,
          type: node.type,
          data: { ...data, relation: relationForNode(data, mode) },
        };
      }),
      edges: focused.edges.map((edge) => ({
        id: edge.id,
        source: edge.source,
        target: edge.target,
        data: { ...edge.data, relation: relationForEdge(edge, mode) },
      })),
      center: focused.center,
      truncated: focused.truncated,
      totalNodes: focused.totalNodes,
      totalEdges: focused.totalEdges,
    };
  }

  const layered = buildLayeredGraph(safeGraph, mode.kindFilter, GLOBAL_EDGE_LIMIT);
  return {
    nodes: layered.nodes.map((node) => ({
      id: node.id,
      type: node.type,
      data: { ...node.data, relation: (node.data.isRoot ? "selected" : "global") as WorkspaceRelation },
    })),
    edges: layered.edges.map((edge) => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      data: { ...edge.data, relation: "global" as const },
    })),
    center: layered.center,
    truncated: layered.truncated,
    totalNodes: layered.totalNodes,
    totalEdges: layered.totalEdges,
  };
}

function relationForNode(data: EnhancedNodeData, mode: Extract<WorkspaceMode, { kind: "focus" }>): WorkspaceRelation {
  void mode;
  if (data.isRoot) return "selected";
  if (data.layer < 0) return "upstream";
  if (data.layer > 0) return "downstream";
  // 仅聚焦中心为 0 层；理论不可达的兜底用未聚焦色
  return "global";
}

/**
 * 按边方向为聚焦子图重算带符号层号（设计 §9.1：上游负层 / 下游正层）。
 * 只遍历 buildFocusGraph 已选中的节点与边，不改变节点/边集合。
 */
function signFocusLayers(focused: { center: string; edges: Array<{ source: string; target: string }> }, direction: FocusDirection): Map<string, number> {
  const layers = new Map<string, number>([[focused.center.toLowerCase(), 0]]);
  const queue = [focused.center.toLowerCase()];
  while (queue.length) {
    const current = queue.shift() as string;
    const currentLayer = layers.get(current) ?? 0;
    for (const edge of focused.edges) {
      const s = edge.source.toLowerCase();
      const t = edge.target.toLowerCase();
      if (s === current) {
        // current → t：下游方向
        if (direction === "upstream") continue;
        if (!layers.has(t)) {
          layers.set(t, currentLayer + 1);
          queue.push(t);
        }
      } else if (t === current) {
        // s → current：上游方向
        if (direction === "downstream") continue;
        if (!layers.has(s)) {
          layers.set(s, currentLayer - 1);
          queue.push(s);
        }
      }
    }
  }
  return layers;
}

function relationForEdge(edge: { source: string; target: string }, mode: Extract<WorkspaceMode, { kind: "focus" }>): WorkspaceRelation {
  const center = mode.center.toLowerCase();
  if (edge.source.toLowerCase() === center) return "downstream";
  if (edge.target.toLowerCase() === center) return "upstream";
  return "cross";
}

/**
 * 布局：聚焦模式按设计 §9（每层 ±500，垂直间距按层内节点数自适应）；
 * 全局模式保留分层布局（节点宽度改为 390 后列间距调整为 460）。
 */
export function layoutWorkspaceGraph(graph: WorkspaceGraph, mode: WorkspaceMode): { nodes: Node[]; edges: Edge[] } {
  const grouped = new Map<number, string[]>();
  for (const node of graph.nodes) {
    const layer = node.data.layer ?? 0;
    grouped.set(layer, [...(grouped.get(layer) ?? []), node.id]);
  }
  for (const ids of grouped.values()) {
    ids.sort((a, b) => nodeWeight(b, graph) - nodeWeight(a, graph) || a.localeCompare(b));
  }
  const layerKeys = [...grouped.keys()].sort((a, b) => a - b);
  const minLayer = layerKeys[0] ?? 0;
  const maxCount = Math.max(...[...grouped.values()].map((ids) => ids.length), 1);

  const gap = mode.kind === "focus" ? verticalGap(maxCount) : 96;
  const columnWidth = mode.kind === "focus" ? 500 : 460;

  const nodes: Node[] = graph.nodes.map((node) => {
    const layer = node.data.layer ?? 0;
    const ids = grouped.get(layer) ?? [];
    const index = ids.indexOf(node.id);
    return {
      id: node.id,
      type: "address",
      position: {
        x: mode.kind === "focus" ? layer * columnWidth : (layer - minLayer) * columnWidth,
        y: (index - (ids.length - 1) / 2) * gap,
      },
      data: node.data,
    };
  });

  const edges: Edge[] = graph.edges.map((edge, index) => {
    const relation: WorkspaceRelation = (edge.data.relation as WorkspaceRelation | undefined) ?? "global";
    const color = RELATION_COLORS[relation] ?? RELATION_COLORS.global;
    return {
      id: `${edge.id}-${index}`,
      source: edge.source,
      target: edge.target,
      type: "flow",
      className: mode.kind === "focus" ? "focus-flow-edge" : "global-flow-edge",
      markerEnd: {
        type: MarkerType.ArrowClosed,
        width: 14,
        height: 14,
        color,
      },
      style: {
        stroke: color,
        strokeWidth: edge.data.discovery ? 2.1 : 1.4,
      },
      data: edge.data,
    };
  });

  return { nodes, edges };
}

function verticalGap(count: number): number {
  if (count <= 8) return 116;
  if (count <= 16) return 94;
  return 76;
}

function nodeWeight(id: string, graph: { nodes: Array<{ id: string; data: EnhancedNodeData }> }) {
  const node = graph.nodes.find((n) => n.id === id);
  const data = node?.data;
  return Number(data?.degree ?? 0) * 1000 + Number(data?.inAmount ?? 0) + Number(data?.outAmount ?? 0);
}

// ── 全局图（无聚焦时）：保留原有分层算法，输出与聚焦图同构 ──

type GraphEdge = GraphData["edges"][number];

function buildLayeredGraph(graph: GraphData, kind: GraphKind, nodeLimit: number) {
  const filteredEdges = graph.edges.filter((edge) => kind === "all" || edge.kind === kind);
  if (!filteredEdges.length) {
    return { nodes: [], edges: [], center: "", layerCount: 0, totalNodes: graph.nodes.length, totalEdges: graph.edges.length, truncated: false };
  }

  const nodeMap = new Map(graph.nodes.map((node) => [node.id.toLowerCase(), node]));
  const adjacency = new Map<string, GraphEdge[]>();
  const candidateIds = new Set<string>();
  for (const edge of filteredEdges) {
    const s = edge.source.toLowerCase();
    const t = edge.target.toLowerCase();
    candidateIds.add(s);
    candidateIds.add(t);
    adjacency.set(s, [...(adjacency.get(s) ?? []), edge]);
    adjacency.set(t, [...(adjacency.get(t) ?? []), edge]);
  }

  const rankedIds = [...candidateIds].sort((left, right) => nodeWeightById(right, nodeMap) - nodeWeightById(left, nodeMap));
  const rootId = rankedIds[0] ?? "";
  const layers = new Map<string, number>([[rootId, 0]]);
  const visibleIds = new Set<string>([rootId]);
  const discoveryEdges = new Set<string>();
  const queue = [rootId];

  while (queue.length && visibleIds.size < nodeLimit) {
    const current = queue.shift() as string;
    const currentLayer = layers.get(current) ?? 0;
    const incident = [...(adjacency.get(current) ?? [])].sort((left, right) => edgeWeight(left, right, current));
    let incomingAdded = 0;
    let outgoingAdded = 0;

    for (const edge of incident) {
      const outgoing = edge.source.toLowerCase() === current;
      if (outgoing && outgoingAdded >= 3) continue;
      if (!outgoing && incomingAdded >= 3) continue;
      const next = (outgoing ? edge.target : edge.source).toLowerCase();
      if (visibleIds.has(next)) continue;
      const nextLayer = currentLayer + (outgoing ? 1 : -1);
      if (Math.abs(nextLayer) > 3) continue;
      visibleIds.add(next);
      layers.set(next, nextLayer);
      discoveryEdges.add(edgeKey(edge));
      queue.push(next);
      if (outgoing) outgoingAdded += 1;
      else incomingAdded += 1;
      if (visibleIds.size >= nodeLimit) break;
    }
  }

  const nodes = [...visibleIds].map((id) => {
    const meta = nodeMap.get(id);
    const incident = adjacency.get(id) ?? [];
    let inAmount = 0;
    let outAmount = 0;
    let inCount = 0;
    let outCount = 0;
    const upstreamSet = new Set<string>();
    const downstreamSet = new Set<string>();
    for (const edge of incident) {
      const outgoing = edge.source.toLowerCase() === id;
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
      id,
      type: "address",
      data: {
        address: id,
        kind: meta?.type === "contract" ? "合约" : "地址",
        risk: meta?.risk_score ?? 0,
        degree: meta?.degree ?? incident.length,
        isRoot: id === rootId,
        layer: layers.get(id) ?? 0,
        inAmount,
        outAmount,
        inCount,
        outCount,
        upstream: upstreamSet.size,
        downstream: downstreamSet.size,
      } satisfies EnhancedNodeData,
    };
  });

  const dedupedEdges = dedupeEdges(filteredEdges)
    .filter((edge) => visibleIds.has(edge.source.toLowerCase()) && visibleIds.has(edge.target.toLowerCase()))
    .sort((left, right) => {
      const discoveryDiff = Number(discoveryEdges.has(edgeKey(right))) - Number(discoveryEdges.has(edgeKey(left)));
      return discoveryDiff || edgeImportance(right) - edgeImportance(left);
    })
    .slice(0, Math.max(nodes.length - 1, Math.round(nodes.length * 1.5)));

  const edges = dedupedEdges.map((edge) => ({
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

  return { nodes, edges, center: rootId, layerCount: 0, totalNodes: graph.nodes.length, totalEdges: graph.edges.length, truncated: false };
}

function dedupeEdges(edges: GraphEdge[]) {
  const byRelation = new Map<string, GraphEdge>();
  for (const edge of edges) {
    const key = edgeKey(edge);
    const current = byRelation.get(key);
    if (!current || edgeImportance(edge) > edgeImportance(current)) byRelation.set(key, edge);
  }
  return [...byRelation.values()];
}

function edgeKey(edge: GraphEdge) {
  return `${edge.source.toLowerCase()}->${edge.target.toLowerCase()}:${edge.kind}`;
}

function edgeImportance(edge: GraphEdge) {
  return Number(edge.tx_count ?? 0) * 100 + parseAmount(edge.amount);
}

function edgeWeight(edge: GraphEdge, other: GraphEdge, current: string) {
  void other;
  const next = edge.source.toLowerCase() === current.toLowerCase() ? edge.target : edge.source;
  return edgeImportance(edge) + nodeWeightById(next, new Map());
}

function nodeWeightById(id: string, nodeMap: Map<string, GraphData["nodes"][number]>) {
  const node = nodeMap.get(id);
  return Number(node?.degree ?? 0) * 1000 + Number(node?.pagerank ?? 0) * 100 + Number(node?.risk_score ?? 0);
}

// 供 GraphPage 使用（聚焦边上限常量来自 graphUpgrade）
export { FOCUS_MAX_EDGES };
