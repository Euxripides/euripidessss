import type { Edge, Node } from '@xyflow/react';
import type { ManualNodeLink, SubjectDetailStats } from './flowTypes';

export function normalizeManualLinks(links: ManualNodeLink[] | undefined): Array<Required<Pick<ManualNodeLink, 'nodeId'>> & ManualNodeLink> {
  return (links ?? [])
    .filter((link) => link?.nodeId)
    .map((link) => ({
      nodeId: String(link.nodeId),
      amount: Number(link.amount || 0),
      count: Number(link.count || 0),
    }));
}

export function buildSubjectDetailStats(node: Node, edges: Edge[]): SubjectDetailStats {
  let amountIn = 0;
  let amountOut = 0;
  let inCount = 0;
  let outCount = 0;
  const inPeers = new Set<string>();
  const outPeers = new Set<string>();
  const allTimes: number[] = [];
  const outTimes: number[] = [];
  for (const edge of edges) {
    if (edge.target === node.id) {
      amountIn += getEdgeAmount(edge);
      inCount += getEdgeCount(edge);
      inPeers.add(edge.source);
      const last = getEdgeTime(edge, 'last');
      const first = getEdgeTime(edge, 'first');
      if (first) allTimes.push(first);
      if (last) allTimes.push(last);
    }
    if (edge.source === node.id) {
      amountOut += getEdgeAmount(edge);
      outCount += getEdgeCount(edge);
      outPeers.add(edge.target);
      const last = getEdgeTime(edge, 'last');
      const first = getEdgeTime(edge, 'first');
      if (first) allTimes.push(first);
      if (last) {
        allTimes.push(last);
        outTimes.push(last);
      }
    }
  }
  const features: string[] = [];
  const totalCount = inCount + outCount;
  const totalAmount = amountIn + amountOut;
  if (amountIn && amountOut) features.push('双向往来');
  if (totalCount >= 10) features.push('高频交易');
  if (Math.max(amountIn, amountOut) >= 1000000) features.push('大额主体');
  if (totalAmount && Math.max(amountIn, amountOut) / totalAmount >= 0.85) features.push(amountOut > amountIn ? '以流出为主' : '以流入为主');
  if (outPeers.size >= 8) features.push('多对象流出');
  if (inPeers.size >= 8) features.push('多对象流入');
  if (totalCount && totalAmount / totalCount >= 50000) features.push('单笔均额高');
  const manualFeatures = (node.data.manualFeatures as string[] | undefined) ?? [];
  for (const feature of manualFeatures) {
    if (feature && !features.includes(feature)) features.push(feature);
  }
  if (!features.length) features.push('普通主体');
  return {
    amountIn,
    amountOut,
    inCount,
    outCount,
    inPeers: inPeers.size,
    outPeers: outPeers.size,
    firstTime: allTimes.length ? formatDateTime(Math.min(...allTimes)) : '',
    lastTime: allTimes.length ? formatDateTime(Math.max(...allTimes)) : '',
    lastOutTime: outTimes.length ? formatDateTime(Math.max(...outTimes)) : '',
    features,
    manualFeatures,
  };
}

export function nodeSelectOptions(nodes: Node[], options: { layerId?: unknown; excludeId?: string } = {}) {
  const hasMultipleLayers = new Set(nodes.map((node) => node.data?.graphLayerId).filter(Boolean)).size > 1;
  const filtered = nodes.filter((node) => {
    if (options.excludeId && node.id === options.excludeId) return false;
    if (!Object.prototype.hasOwnProperty.call(options, 'layerId')) return true;
    return String(node.data?.graphLayerId ?? '') === String(options.layerId ?? '');
  });
  return filtered.map((node) => ({
    value: node.id,
    label: hasMultipleLayers && node.data?.graphLayerLabel
      ? `${String(node.data.graphLayerLabel)} / ${String(node.data.entityLabel ?? node.id)}`
      : String(node.data.entityLabel ?? node.id),
  }));
}

export function uniqueDisplayLabel(label: string, nodes: Node[]) {
  const normalized = label.trim();
  const existingCount = nodes.filter((node) => String((node.data as Record<string, unknown>).rawEntityLabel ?? node.data.entityLabel ?? node.id).trim() === normalized).length;
  return existingCount ? `${normalized}（${existingCount + 1}）` : normalized;
}

function getEdgeAmount(edge: Edge) {
  return Number(edge.data?.amount ?? 0);
}

function getEdgeCount(edge: Edge) {
  return Number(edge.data?.tx_count ?? 0);
}

function getEdgeTime(edge: Edge, field: 'first' | 'last') {
  const value = field === 'first' ? edge.data?.first_time : edge.data?.last_time;
  const time = value ? new Date(String(value)).getTime() : 0;
  return Number.isFinite(time) ? time : 0;
}

function formatDateTime(time: number) {
  const date = new Date(time);
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}
