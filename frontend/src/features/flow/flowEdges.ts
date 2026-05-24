import { MarkerType, type Edge } from '@xyflow/react';
import type { ArrowMode, EdgeLabelMode, EdgeLinePattern, TimeWindow } from './flowTypes';

export function buildEdgeLabel(edge: Edge, mode: EdgeLabelMode) {
  const amount = getEdgeAmount(edge);
  const txCount = getEdgeCount(edge);
  if (mode === 'amount') return formatMoney(amount);
  if (mode === 'count') return `${txCount} 笔`;
  if (amount || txCount) return `${formatMoney(amount)} / ${txCount} 笔`;
  return edge.label;
}

export function getEdgeLineWidth(edge: Edge, fallback: number) {
  return Number(edge.data?.lineWidth ?? fallback);
}

export function getEdgeLineColor(edge: Edge, fallback: string) {
  return String(edge.data?.lineColor ?? fallback);
}

export function getEdgeArrowMode(edge: Edge, fallback: ArrowMode) {
  const value = edge.data?.arrowMode;
  return value === 'forward' || value === 'reverse' || value === 'both' || value === 'none' ? value : fallback;
}

export function getEdgeLinePattern(edge: Edge): EdgeLinePattern {
  return edge.data?.linePattern === 'dashed' ? 'dashed' : 'solid';
}

export function markerStartForEdge(edge: Edge, fallbackMode: ArrowMode, fallbackColor: string) {
  const mode = getEdgeArrowMode(edge, fallbackMode);
  const color = getEdgeLineColor(edge, fallbackColor);
  if (mode === 'reverse') return { type: MarkerType.ArrowClosed, color, width: 14, height: 14 };
  if (mode === 'both') return { type: MarkerType.Arrow, color, width: 14, height: 14 };
  return undefined;
}

export function markerEndForEdge(edge: Edge, fallbackMode: ArrowMode, fallbackColor: string) {
  const mode = getEdgeArrowMode(edge, fallbackMode);
  const color = getEdgeLineColor(edge, fallbackColor);
  if (mode === 'forward') return { type: MarkerType.ArrowClosed, color, width: 14, height: 14 };
  if (mode === 'both') return { type: MarkerType.Arrow, color, width: 14, height: 14 };
  return undefined;
}

export function markerEndForDirectionalEdge(edge: Edge, fallbackMode: ArrowMode, fallbackColor: string) {
  const mode = getEdgeArrowMode(edge, fallbackMode);
  if (mode === 'none') return undefined;
  return { type: MarkerType.ArrowClosed, color: getEdgeLineColor(edge, fallbackColor), width: 14, height: 14 };
}

export function findReciprocalPairKeys(edges: Edge[]) {
  const directed = new Set(edges.map((edge) => directedEdgeKey(edge.source, edge.target)));
  const reciprocal = new Set<string>();
  for (const edge of edges) {
    if (directed.has(directedEdgeKey(edge.target, edge.source))) {
      reciprocal.add(unorderedEdgePairKey(edge));
    }
  }
  return reciprocal;
}

function directedEdgeKey(source: string, target: string) {
  return `${source}->${target}`;
}

export function unorderedEdgePairKey(edge: Edge) {
  return [edge.source, edge.target].sort().join('<->');
}

export function reciprocalEdgeOffset() {
  return -8;
}

export function getEdgeAmount(edge: Edge) {
  return Number(edge.data?.amount ?? 0);
}

export function getEdgeCount(edge: Edge) {
  return Number(edge.data?.tx_count ?? 0);
}

export function getEdgeTime(edge: Edge, field: 'first' | 'last') {
  const value = field === 'first' ? edge.data?.first_time : edge.data?.last_time;
  const time = value ? new Date(String(value)).getTime() : 0;
  return Number.isFinite(time) ? time : 0;
}

export function getTimeCutoff(latestTime: number, window: TimeWindow) {
  if (!latestTime || window === 'all') return 0;
  return latestTime - Number(window) * 24 * 60 * 60 * 1000;
}
function formatMoney(value: number) {
  return Number(value || 0).toLocaleString('zh-CN', { maximumFractionDigits: 0 });
}
