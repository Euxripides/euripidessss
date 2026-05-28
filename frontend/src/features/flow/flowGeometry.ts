import type { Edge, Node } from '@xyflow/react';
import {
  FLOW_NODE_ICON_SIZE,
  FLOW_NODE_LABEL_LINE_HEIGHT,
  FLOW_NODE_LABEL_TOP,
  FLOW_NODE_LABEL_WIDTH,
  type DynamicAnchor,
  type GeometryRect,
  type NodeGeometry,
} from './flowTypes';

function buildNodesMap(nodes: Node[]): Map<string, Node> {
  const map = new Map<string, Node>();
  for (const node of nodes) map.set(node.id, node);
  return map;
}

export function chooseEdgeHandles(source?: { x: number; y: number }, target?: { x: number; y: number }) {
  if (!source || !target) return { sourceHandle: 'right-source', targetHandle: 'left-target' };
  const dx = target.x - source.x;
  const dy = target.y - source.y;
  if (Math.abs(dx) >= Math.abs(dy) * 0.75) {
    return dx >= 0
      ? { sourceHandle: 'right-source', targetHandle: 'left-target' }
      : { sourceHandle: 'left-source', targetHandle: 'right-target' };
  }
  return dy >= 0
    ? { sourceHandle: 'bottom-source', targetHandle: 'top-target' }
    : { sourceHandle: 'top-source', targetHandle: 'bottom-target' };
}

export function chooseOptimizedEdgeHandles(source?: NodeGeometry, target?: NodeGeometry, sourceId = '', targetId = '', edgeId = 'edge') {
  if (!source || !target) return { sourceHandle: 'right-source', targetHandle: 'left-target' };
  const sourceCenter = centerOfGeometry(source);
  const targetCenter = centerOfGeometry(target);
  return {
    sourceHandle: `${dynamicAnchorId(sourceId, edgeId, 'source')}-source`,
    targetHandle: `${dynamicAnchorId(targetId, edgeId, 'target')}-target`,
  };
}

export function buildOptimizedHandleMap(edges: Edge[], nodes: Node[], positions: Map<string, { x: number; y: number }>) {
  const map = new Map<string, DynamicAnchor[]>();
  const nodesMap = buildNodesMap(nodes);
  for (const edge of edges) {
    const source = getNodeGeometry(edge.source, nodesMap, positions);
    const target = getNodeGeometry(edge.target, nodesMap, positions);
    if (!source || !target) continue;
    const sourceAnchor = boundaryAnchorToward(source, centerOfGeometry(target), dynamicAnchorId(edge.source, edge.id, 'source'));
    const targetAnchor = boundaryAnchorToward(target, centerOfGeometry(source), dynamicAnchorId(edge.target, edge.id, 'target'));
    map.set(edge.source, [...(map.get(edge.source) ?? []), sourceAnchor]);
    map.set(edge.target, [...(map.get(edge.target) ?? []), targetAnchor]);
  }
  return map;
}

function dynamicAnchorId(nodeId: string, edgeId: string, endpoint: 'source' | 'target') {
  return `dyn-${endpoint}-${hashHandleId(nodeId)}-${hashHandleId(edgeId)}`;
}

function hashHandleId(value: string) {
  let hash = 0;
  for (const char of String(value)) {
    hash = ((hash << 5) - hash + char.charCodeAt(0)) | 0;
  }
  return Math.abs(hash).toString(36);
}

export function getNodeGeometry(nodeId: string, nodesMap: Map<string, Node>, positions: Map<string, { x: number; y: number }>): NodeGeometry | undefined {
  const node = nodesMap.get(nodeId);
  const position = positions.get(nodeId);
  if (!node || !position) return undefined;
  const label = String(node.data.entityLabel ?? node.id);
  const labelMetrics = estimateFlowLabelMetrics(label);
  const iconLeft = (FLOW_NODE_LABEL_WIDTH - FLOW_NODE_ICON_SIZE) / 2;
  const labelLeft = (FLOW_NODE_LABEL_WIDTH - labelMetrics.width) / 2;
  const rects = [
    { x: iconLeft, y: 0, width: FLOW_NODE_ICON_SIZE, height: FLOW_NODE_ICON_SIZE },
    { x: labelLeft, y: FLOW_NODE_LABEL_TOP, width: labelMetrics.width, height: labelMetrics.height },
  ];
  const tagCount = ((node.data.tags as string[] | undefined) ?? []).length;
  if (tagCount) rects.push({ x: 20, y: FLOW_NODE_LABEL_TOP + labelMetrics.height + 6, width: 180, height: 24 });
  const bounds = boundsOfRects(rects);
  return {
    originX: position.x,
    originY: position.y,
    x: position.x + bounds.x,
    y: position.y + bounds.y,
    width: bounds.width,
    height: bounds.height,
    centerX: position.x + bounds.x + bounds.width / 2,
    centerY: position.y + bounds.y + bounds.height / 2,
    rects: rects.map((rect) => ({ ...rect, x: position.x + rect.x, y: position.y + rect.y })),
  };
}

function centerOfGeometry(box: NodeGeometry) {
  return { x: box.centerX, y: box.centerY };
}

function boundaryAnchorToward(box: NodeGeometry, point: { x: number; y: number }, id: string): DynamicAnchor {
  const center = centerOfGeometry(box);
  const dx = point.x - center.x;
  const dy = point.y - center.y;
  if (!dx && !dy) return anchorFromPoint(id, 'right', box.x + box.width - box.originX, center.y - box.originY);
  const hit = furthestRayHit(center, { x: dx, y: dy }, box.rects) ?? rectBoundaryToward(box, dx, dy);
  return anchorFromPoint(id, hit.side, hit.x - box.originX, hit.y - box.originY);
}

function estimateFlowLabelMetrics(label: string) {
  const textWidth = Math.max(24, Array.from(label).reduce((sum, char) => sum + (/[\u4e00-\u9fff]/.test(char) ? 12 : 6.5), 0));
  const lineCount = Math.max(1, Math.ceil(textWidth / FLOW_NODE_LABEL_WIDTH));
  return {
    width: clamp(textWidth, 24, FLOW_NODE_LABEL_WIDTH),
    height: lineCount * FLOW_NODE_LABEL_LINE_HEIGHT,
  };
}

function boundsOfRects(rects: GeometryRect[]): GeometryRect {
  const left = Math.min(...rects.map((rect) => rect.x));
  const top = Math.min(...rects.map((rect) => rect.y));
  const right = Math.max(...rects.map((rect) => rect.x + rect.width));
  const bottom = Math.max(...rects.map((rect) => rect.y + rect.height));
  return { x: left, y: top, width: right - left, height: bottom - top };
}

function furthestRayHit(origin: { x: number; y: number }, vector: { x: number; y: number }, rects: GeometryRect[]) {
  let best: { x: number; y: number; t: number; side: DynamicAnchor['side'] } | undefined;
  for (const rect of rects) {
    const hit = rayRectExit(origin, vector, rect);
    if (hit && (!best || hit.t > best.t)) best = hit;
  }
  return best;
}

function rayRectExit(origin: { x: number; y: number }, vector: { x: number; y: number }, rect: GeometryRect) {
  const candidates: Array<{ x: number; y: number; t: number; side: DynamicAnchor['side'] }> = [];
  const left = rect.x;
  const right = rect.x + rect.width;
  const top = rect.y;
  const bottom = rect.y + rect.height;
  if (vector.x) {
    candidates.push({ x: left, y: origin.y + ((left - origin.x) / vector.x) * vector.y, t: (left - origin.x) / vector.x, side: 'left' });
    candidates.push({ x: right, y: origin.y + ((right - origin.x) / vector.x) * vector.y, t: (right - origin.x) / vector.x, side: 'right' });
  }
  if (vector.y) {
    candidates.push({ x: origin.x + ((top - origin.y) / vector.y) * vector.x, y: top, t: (top - origin.y) / vector.y, side: 'top' });
    candidates.push({ x: origin.x + ((bottom - origin.y) / vector.y) * vector.x, y: bottom, t: (bottom - origin.y) / vector.y, side: 'bottom' });
  }
  return candidates
    .filter((hit) => hit.t >= 0 && hit.x >= left - 0.01 && hit.x <= right + 0.01 && hit.y >= top - 0.01 && hit.y <= bottom + 0.01)
    .sort((a, b) => b.t - a.t)[0];
}

function rectBoundaryToward(box: NodeGeometry, dx: number, dy: number) {
  const halfW = box.width / 2;
  const halfH = box.height / 2;
  const scale = Math.max(Math.abs(dx) / halfW, Math.abs(dy) / halfH) || 1;
  const x = box.centerX + dx / scale;
  const y = box.centerY + dy / scale;
  const side: DynamicAnchor['side'] = Math.abs(dx / halfW) >= Math.abs(dy / halfH)
    ? (dx >= 0 ? 'right' : 'left')
    : (dy >= 0 ? 'bottom' : 'top');
  return { x, y, side };
}

function anchorFromPoint(id: string, side: DynamicAnchor['side'], x: number, y: number): DynamicAnchor {
  return { id, side, offset: 50, x, y };
}

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(max, value));
}
