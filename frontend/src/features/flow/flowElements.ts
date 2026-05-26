import { MarkerType, type Edge, type Node } from '@xyflow/react';
import type { ProcessResponse } from '../../types';
import { layoutFlowGraph } from './flowLayout';
import { detectEntityKind, renderFlowNodeLabel } from './flowNodes';
import type { GraphDetailContext } from './flowTypes';

export function buildFlowElements(
  graph: ProcessResponse['flow_graph'],
  options: {
    layerId?: string;
    layerLabel?: string;
    detailContext?: GraphDetailContext;
    offset?: { x: number; y: number };
  } = {},
): { nodes: Node[]; edges: Edge[] } {
  const layerId = options.layerId ?? 'graph';
  const offset = options.offset ?? { x: 0, y: 0 };
  const positions = layoutFlowGraph(graph);
  const nodes = graph.nodes.map((item) => {
    const entityKind = detectEntityKind(item.role, item.label);
    const position = positions.get(item.id) ?? { x: 120, y: 120 };
    return {
      id: scopedGraphId(layerId, item.id),
      position: { x: position.x + offset.x, y: position.y + offset.y },
      type: 'flowEntity',
      data: {
        entityLabel: item.label,
        rawEntityId: item.id,
        accountNo: item.account_no,
        accountName: item.account_name,
        idNumber: item.id_number,
        graphLayerId: layerId,
        graphLayerLabel: options.layerLabel,
        entityKind,
        label: renderFlowNodeLabel(item.label, item.tags, entityKind),
        tags: item.tags,
      },
      className: `flow-node ${item.role}`,
    } satisfies Node;
  });
  const edges = graph.edges.map((item) => ({
    id: scopedGraphId(layerId, item.id),
    source: scopedGraphId(layerId, item.source),
    target: scopedGraphId(layerId, item.target),
    label: item.label,
    animated: false,
    type: 'straight',
    markerEnd: { type: MarkerType.ArrowClosed, color: '#111827', width: 14, height: 14 },
    data: {
      amount: item.amount,
      tx_count: item.tx_count,
      avg_amount: item.avg_amount,
      max_amount: item.max_amount,
      first_time: item.first_time,
      last_time: item.last_time,
      rawSource: item.source,
      rawTarget: item.target,
      graphLayerId: layerId,
      graphLayerLabel: options.layerLabel,
      detailContext: options.detailContext ?? { kind: 'none' },
    },
    style: {
      stroke: '#111827',
      strokeWidth: Math.min(2.2, Math.max(1, Math.log10(item.amount + 10) / 5)),
    },
    labelBgPadding: [4, 2] as [number, number],
    labelBgBorderRadius: 2,
    labelBgStyle: { fill: '#ffffff', fillOpacity: 0.96 },
  }));
  return { nodes, edges };
}

function scopedGraphId(layerId: string, id: string) {
  return `${layerId}::${id}`;
}

export function nextGraphOffset(nodes: Node[]) {
  if (!nodes.length) return { x: 0, y: 0 };
  const maxX = Math.max(...nodes.map((node) => node.position.x));
  const minY = Math.min(...nodes.map((node) => node.position.y));
  return { x: maxX + 420, y: minY };
}
