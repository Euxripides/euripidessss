import { MarkerType, type Edge } from '@xyflow/react';
import type { ManualNodeFormValues, ManualNodeLink } from './flowTypes';
import { chooseEdgeHandles } from './flowGeometry';

export function createManualEdge(source: string, target: string, link: ManualNodeLink, values: Pick<ManualNodeFormValues, 'lineStyle' | 'lineWidth'>): Edge {
  const value = Number(link.amount || 0);
  const txCount = Number(link.count || 0);
  const lineWidth = Number(values.lineWidth || 1.2);
  const lineStyle = values.lineStyle ?? 'solid';
  const id = `manual-edge-${source}-${target}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const handles = chooseEdgeHandles(undefined, undefined);
  const labelParts = [];
  if (value) labelParts.push(formatMoney(value));
  if (txCount) labelParts.push(`${txCount} 笔`);
  const label = labelParts.length ? labelParts.join(' / ') : '关系';
  return {
    id,
    source,
    target,
    sourceHandle: handles.sourceHandle,
    targetHandle: handles.targetHandle,
    label,
    animated: false,
    type: 'straight',
    markerEnd: { type: MarkerType.ArrowClosed, color: '#111827', width: 14, height: 14 },
    data: {
      amount: value,
      tx_count: txCount,
      avg_amount: txCount ? value / txCount : value,
      max_amount: value,
      customLabel: label,
    },
    style: {
      stroke: '#111827',
      strokeWidth: lineWidth,
      strokeDasharray: lineStyle === 'dashed' ? '6 4' : undefined,
    },
    labelBgPadding: [4, 2] as [number, number],
    labelBgBorderRadius: 2,
    labelBgStyle: { fill: '#ffffff', fillOpacity: 0.96 },
  };
}

export function attachManualEdgeLayer(edge: Edge, layerId?: string, layerLabel?: string): Edge {
  if (!layerId) return edge;
  return {
    ...edge,
    data: {
      ...(edge.data ?? {}),
      graphLayerId: layerId,
      graphLayerLabel: layerLabel,
    },
  };
}

function formatMoney(value: number) {
  return Number(value || 0).toLocaleString('zh-CN', { maximumFractionDigits: 0 });
}
