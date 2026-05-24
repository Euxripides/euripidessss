import type { Edge } from '@xyflow/react';
import { DIRECTION_FILTER_MAP } from './flowTypes';
import { getEdgeAmount, getEdgeCount } from './flowEdges';

export function normalizeDirectionFilterValues(values: string[]) {
  const normalized: string[] = [];
  for (const value of values) {
    const item = DIRECTION_FILTER_MAP[value.trim()] ?? value.trim();
    if (item && !normalized.includes(item)) normalized.push(item);
  }
  return normalized;
}

export function findShortestPath(edges: Edge[], source?: string, target?: string) {
  if (!source || !target || source === target) return { nodes: [] as string[], edges: [] as string[] };
  const adjacency = new Map<string, Array<{ node: string; edgeId: string }>>();
  for (const edge of edges) {
    if (!adjacency.has(edge.source)) adjacency.set(edge.source, []);
    adjacency.get(edge.source)?.push({ node: edge.target, edgeId: edge.id });
  }
  const queue = [source];
  const seen = new Set([source]);
  const previous = new Map<string, { node: string; edgeId: string }>();
  while (queue.length) {
    const current = queue.shift() as string;
    if (current === target) break;
    for (const next of adjacency.get(current) ?? []) {
      if (seen.has(next.node)) continue;
      seen.add(next.node);
      previous.set(next.node, { node: current, edgeId: next.edgeId });
      queue.push(next.node);
    }
  }
  if (!previous.has(target)) return { nodes: [] as string[], edges: [] as string[] };
  const nodes = [target];
  const pathEdges: string[] = [];
  let current = target;
  while (current !== source) {
    const item = previous.get(current);
    if (!item) break;
    pathEdges.unshift(item.edgeId);
    current = item.node;
    nodes.unshift(current);
  }
  return { nodes, edges: pathEdges };
}

export function buildInsights(edges: Edge[], labels: Map<string, string>) {
  const byPair = new Map<string, Edge>();
  const reciprocal: Array<{ a: Edge; b: Edge }> = [];
  for (const edge of edges) {
    const reverse = byPair.get(`${edge.target}->${edge.source}`);
    if (reverse) reciprocal.push({ a: reverse, b: edge });
    byPair.set(`${edge.source}->${edge.target}`, edge);
  }
  const items = reciprocal.slice(0, 2).map(({ a, b }, index) => ({
    key: `reciprocal-${index}-${a.id}`,
    title: '双向资金往来',
    detail: `${labels.get(a.source) ?? a.source} ↔ ${labels.get(a.target) ?? a.target}，合计 ${formatMoney(getEdgeAmount(a) + getEdgeAmount(b))}`,
    subjects: [a.source, a.target],
  }));
  const highFrequency = [...edges].sort((a, b) => getEdgeCount(b) - getEdgeCount(a))[0];
  if (highFrequency && getEdgeCount(highFrequency) >= 5) {
    items.push({
      key: `freq-${highFrequency.id}`,
      title: '高频往来关系',
      detail: `${labels.get(highFrequency.source) ?? highFrequency.source} → ${labels.get(highFrequency.target) ?? highFrequency.target}，${getEdgeCount(highFrequency)} 笔`,
      subjects: [highFrequency.source, highFrequency.target],
    });
  }
  const largeSingle = [...edges].sort((a, b) => Number(b.data?.max_amount ?? 0) - Number(a.data?.max_amount ?? 0))[0];
  if (largeSingle && Number(largeSingle.data?.max_amount ?? 0) > 0) {
    items.push({
      key: `max-${largeSingle.id}`,
      title: '最大单笔交易',
      detail: `${labels.get(largeSingle.source) ?? largeSingle.source} → ${labels.get(largeSingle.target) ?? largeSingle.target}，单笔 ${formatMoney(Number(largeSingle.data?.max_amount ?? 0))}`,
      subjects: [largeSingle.source, largeSingle.target],
    });
  }
  return items.slice(0, 4);
}

function formatMoney(value: number) {
  return Number(value || 0).toLocaleString('zh-CN', { maximumFractionDigits: 0 });
}
