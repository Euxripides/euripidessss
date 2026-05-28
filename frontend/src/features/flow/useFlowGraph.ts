import { useMemo } from 'react';

import { buildEdgeLabel, getEdgeAmount, getEdgeCount, getEdgeLineColor, getEdgeLinePattern, getEdgeLineWidth, getEdgeTime, getTimeCutoff, markerEndForDirectionalEdge, markerEndForEdge, markerStartForEdge, reciprocalEdgeOffset, unorderedEdgePairKey } from './flowEdges';

import { buildOptimizedHandleMap, chooseEdgeHandles, chooseOptimizedEdgeHandles, getNodeGeometry } from './flowGeometry';

import { buildInsights, findShortestPath } from './flowAnalysis';

import type { ArrowMode, EdgeLabelMode, FlowEdgeRow, LineType, SubjectStat, TimeWindow } from './flowTypes';

import type { Edge as ReactFlowEdge, Node as ReactFlowNode } from '@xyflow/react';

import { findReciprocalPairKeys } from './flowEdges';



interface UseFlowGraphParams {

  edges: ReactFlowEdge[];

  nodes: ReactFlowNode[];

  subjectIds: string[];

  minAmount: number;

  pathSource: string | undefined;

  pathTarget: string | undefined;

  edgeLabelMode: EdgeLabelMode;

  timeWindow: TimeWindow;

  renderLimit: number;

  arrowMode: ArrowMode;

  lineColor: string;

  lineType: LineType;

  lineWidth: number;

  optimizeAnchors: boolean;

  selectedEdgeIds: string[];

  dataPenetrationEnabled: boolean;

  expandedPenetrationNodeIds: string[];

  onExpandDataPenetrationNode?: (nodeId: string) => void;

  onCollapseDataPenetrationNode?: (nodeId: string) => void;

}



export function useFlowGraph(params: UseFlowGraphParams) {

  const {

    edges, nodes,

    subjectIds, minAmount, pathSource, pathTarget,

    edgeLabelMode, timeWindow, renderLimit,

    arrowMode, lineColor, lineType, lineWidth,

    optimizeAnchors, selectedEdgeIds,

    dataPenetrationEnabled, expandedPenetrationNodeIds,

    onExpandDataPenetrationNode, onCollapseDataPenetrationNode,

  } = params;



  const maxAmount = useMemo(() => Math.ceil(Math.max(0, ...edges.map((edge) => getEdgeAmount(edge)))), [edges]);
  const effectiveMinAmount = Math.min(minAmount, maxAmount || 0);

  const subjectOptions = useMemo(

    () =>

      nodes.map((node) => ({

        value: node.id,

        label: String((node.data as any).entityLabel ?? node.id),

      })),

    [nodes],

  );

  const nodeLabels = useMemo(

    () => new Map(nodes.map((node) => [node.id, String(node.data.entityLabel ?? node.id)])),

    [nodes],

  );

  const latestEdgeTime = useMemo(() => Math.max(0, ...edges.map((edge) => getEdgeTime(edge, 'last'))), [edges]);

  const pathResult = useMemo(() => findShortestPath(edges, pathSource, pathTarget), [edges, pathSource, pathTarget]);

  const nodePositions = useMemo(() => new Map(nodes.map((node) => [node.id, node.position])), [nodes]);

  const optimizedHandleMap = useMemo(() => buildOptimizedHandleMap(edges, nodes, nodePositions), [nodePositions, edges, nodes]);



  const visibleGraph = useMemo(() => {
    const nodesMap = new Map(nodes.map((n) => [n.id, n]));
    const chosen = new Set(subjectIds);

    const hasSubjectFilter = chosen.size > 0;

    const connectedNodeIds = new Set<string>(subjectIds);

    const pathNodeIds = new Set(pathResult.nodes);

    const pathEdgeIds = new Set(pathResult.edges);

    const timeCutoff = getTimeCutoff(latestEdgeTime, timeWindow);
    const hasEdgeFilter = effectiveMinAmount > 0 || Boolean(timeCutoff);



    const sortedVisibleEdges = edges

      .filter((edge) => {

        if (getEdgeAmount(edge) < effectiveMinAmount) return false;

        if (timeCutoff && getEdgeTime(edge, 'last') < timeCutoff) return false;

        if (!hasSubjectFilter) return true;

        const matched = chosen.has(edge.source) || chosen.has(edge.target);

        if (matched) {

          connectedNodeIds.add(edge.source);

          connectedNodeIds.add(edge.target);

        }

        return matched;

      })

      .sort((a, b) => getEdgeAmount(b) - getEdgeAmount(a));

    const baseVisibleEdges = renderLimit > 0 ? sortedVisibleEdges.slice(0, renderLimit) : sortedVisibleEdges;

    const penetrationState = dataPenetrationEnabled
      ? buildDataPenetrationState(baseVisibleEdges, expandedPenetrationNodeIds)
      : undefined;

    const edgeCandidates = penetrationState
      ? baseVisibleEdges.filter((edge) => penetrationState.visibleEdgeIds.has(edge.id))
      : baseVisibleEdges;

    const reciprocalPairKeys = findReciprocalPairKeys(edgeCandidates);

    const visibleEdges = edgeCandidates

      .map((edge) => {

        const pairKey = unorderedEdgePairKey(edge);

        const hasReciprocal = reciprocalPairKeys.has(pairKey);

        const handles = optimizeAnchors

          ? chooseOptimizedEdgeHandles(

              getNodeGeometry(edge.source, nodesMap, nodePositions),



              getNodeGeometry(edge.target, nodesMap, nodePositions),

              edge.source,

              edge.target,

              edge.id,

            )

          : chooseEdgeHandles(nodePositions.get(edge.source), nodePositions.get(edge.target));

        const labelText = edgeLabelMode === 'none' ? undefined : String(edge.data?.customLabel ?? buildEdgeLabel(edge, edgeLabelMode));

        const selected = selectedEdgeIds.includes(edge.id);

        const color = pathEdgeIds.has(edge.id) ? '#dc2626' : getEdgeLineColor(edge, lineColor);

        const width = selected

          ? Math.max(3.5, getEdgeLineWidth(edge, lineWidth) + 1.8)

          : pathEdgeIds.has(edge.id)

            ? Math.max(3, getEdgeLineWidth(edge, lineWidth) + 1.5)

            : getEdgeLineWidth(edge, lineWidth);

        return {

          ...edge,

          selected,

          sourceHandle: handles.sourceHandle,

          targetHandle: handles.targetHandle,

          type: hasReciprocal ? 'directional' : lineType,

          label: hasReciprocal ? undefined : labelText,

          animated: edge.animated || pathEdgeIds.has(edge.id),

          markerStart: hasReciprocal ? undefined : markerStartForEdge(edge, arrowMode, lineColor),

          markerEnd: hasReciprocal ? markerEndForDirectionalEdge(edge, arrowMode, color) : markerEndForEdge(edge, arrowMode, lineColor),

          style: {

            ...(edge.style ?? {}),

            stroke: color,

            strokeWidth: width,

            strokeDasharray: getEdgeLinePattern(edge) === 'dashed' ? '6 4' : undefined,

          },

          interactionWidth: hasReciprocal ? 18 : 36,

          data: {

            ...(edge.data ?? {}),

            displayLabel: labelText,

            parallelOffset: hasReciprocal ? reciprocalEdgeOffset() : 0,

          },

        };

      });



    for (const edge of visibleEdges) {

      connectedNodeIds.add(edge.source);

      connectedNodeIds.add(edge.target);

    }

    for (const nodeId of pathNodeIds) connectedNodeIds.add(nodeId);

    const enrichNodeData = (node: ReactFlowNode) => ({
      ...node.data,
      dynamicHandles: optimizedHandleMap.get(node.id) ?? [],
      penetrationCanExpand: Boolean(penetrationState?.canExpandNodeIds.has(node.id)),
      penetrationCanCollapse: Boolean(penetrationState?.canCollapseNodeIds.has(node.id)),
      onPenetrationExpand: onExpandDataPenetrationNode,
      onPenetrationCollapse: onCollapseDataPenetrationNode,
    });

    const visibleNodes = (dataPenetrationEnabled || hasSubjectFilter || hasEdgeFilter || (renderLimit > 0 && renderLimit < edges.length) || pathNodeIds.size > 0)

      ? nodes

          .filter((node) => connectedNodeIds.has(node.id))

          .map((node) => ({

            ...node,

            data: enrichNodeData(node),

            className: pathNodeIds.has(node.id) ? node.className + ' path-focus' : node.className,

          }))

      : nodes.map((node) => ({ ...node, data: enrichNodeData(node) }));

    return { nodes: visibleNodes, edges: visibleEdges };

  }, [arrowMode, dataPenetrationEnabled, edgeLabelMode, expandedPenetrationNodeIds, latestEdgeTime, lineColor, lineType, lineWidth, effectiveMinAmount, minAmount, nodePositions, onCollapseDataPenetrationNode, onExpandDataPenetrationNode, optimizeAnchors, optimizedHandleMap, pathResult.edges, pathResult.nodes, edges, nodes, renderLimit, selectedEdgeIds, subjectIds, timeWindow]);



  const relationshipRows = useMemo<FlowEdgeRow[]>(

    () =>

      visibleGraph.edges

        .map((edge) => ({

          id: edge.id,

          source: edge.source,

          target: edge.target,

          sourceLabel: nodeLabels.get(edge.source) ?? edge.source,

          targetLabel: nodeLabels.get(edge.target) ?? edge.target,

          amount: getEdgeAmount(edge),

          tx_count: getEdgeCount(edge),

        }))

        .sort((a, b) => b.amount - a.amount),

    [nodeLabels, visibleGraph.edges],

  );



  const subjectStats = useMemo<SubjectStat[]>(() => {

    const stats = new Map<string, SubjectStat>();

    for (const node of visibleGraph.nodes) {

      stats.set(node.id, {

        id: node.id,

        label: String((node.data as Record<string, unknown>).entityLabel ?? node.id),

        amount: 0,

        tx_count: 0,

        degree: 0,

      });

    }

    for (const edge of visibleGraph.edges) {

      const amount = getEdgeAmount(edge);

      const txCount = getEdgeCount(edge);

      for (const nodeId of [edge.source, edge.target]) {

        const item = stats.get(nodeId);

        if (!item) continue;

        item.amount += amount;

        item.tx_count += txCount;

        item.degree += 1;

      }

    }

    return Array.from(stats.values()).sort((a, b) => b.amount - a.amount).slice(0, 8);

  }, [visibleGraph.edges, visibleGraph.nodes]);



  const visibleTotal = relationshipRows.reduce((sum, row) => sum + row.amount, 0);

  const strongest = relationshipRows[0];

  const insightItems = useMemo(() => buildInsights(visibleGraph.edges, nodeLabels), [nodeLabels, visibleGraph.edges]);



  return {

    maxAmount,

    subjectOptions,

    nodeLabels,

    latestEdgeTime,

    pathResult,

    nodePositions,

    optimizedHandleMap,

    visibleGraph,

    relationshipRows,

    subjectStats,

    visibleTotal,

    strongest,

    insightItems,

  };

}

function buildDataPenetrationState(edges: ReactFlowEdge[], expandedNodeIds: string[]) {
  const expanded = new Set(expandedNodeIds);
  const visibleEdgeIds = new Set<string>();
  const canExpandNodeIds = new Set<string>();
  const canCollapseNodeIds = new Set<string>();
  const incomingByNode = new Map<string, ReactFlowEdge[]>();
  const outgoingByNode = new Map<string, ReactFlowEdge[]>();

  for (const edge of edges) {
    const incoming = incomingByNode.get(edge.target) ?? [];
    incoming.push(edge);
    incomingByNode.set(edge.target, incoming);
    const outgoing = outgoingByNode.get(edge.source) ?? [];
    outgoing.push(edge);
    outgoingByNode.set(edge.source, outgoing);
  }

  const rootSources = new Set<string>();
  for (const source of outgoingByNode.keys()) {
    if (!incomingByNode.has(source)) rootSources.add(source);
  }

  if (rootSources.size > 0) {
    for (const edge of edges) {
      if (rootSources.has(edge.source)) visibleEdgeIds.add(edge.id);
    }
  } else {
    const earliest = [...edges].sort((a, b) => edgeFirstTime(a) - edgeFirstTime(b))[0];
    if (earliest) visibleEdgeIds.add(earliest.id);
  }

  let changed = true;
  while (changed) {
    changed = false;
    const visibleIncomingCutoffs = visibleIncomingTimes(edges, visibleEdgeIds);
    for (const edge of edges) {
      if (visibleEdgeIds.has(edge.id) || !expanded.has(edge.source)) continue;
      const cutoff = visibleIncomingCutoffs.get(edge.source) ?? 0;
      if (!edgeIsAfter(edge, cutoff)) continue;
      visibleEdgeIds.add(edge.id);
      changed = true;
    }
  }

  const finalIncomingCutoffs = visibleIncomingTimes(edges, visibleEdgeIds);
  for (const edge of edges) {
    const cutoff = finalIncomingCutoffs.get(edge.source);
    if (!cutoff || !edgeIsAfter(edge, cutoff)) continue;
    if (visibleEdgeIds.has(edge.id)) {
      if (expanded.has(edge.source)) canCollapseNodeIds.add(edge.source);
    } else {
      canExpandNodeIds.add(edge.source);
    }
  }

  return { visibleEdgeIds, canExpandNodeIds, canCollapseNodeIds };
}

function visibleIncomingTimes(edges: ReactFlowEdge[], visibleEdgeIds: Set<string>) {
  const incoming = new Map<string, number>();
  for (const edge of edges) {
    if (!visibleEdgeIds.has(edge.id)) continue;
    const time = edgeLastTime(edge);
    if (!time) continue;
    const current = incoming.get(edge.target) ?? 0;
    if (!current || time < current) incoming.set(edge.target, time);
  }
  return incoming;
}

function edgeIsAfter(edge: ReactFlowEdge, cutoff: number) {
  if (!cutoff) return true;
  const first = edgeFirstTime(edge);
  const last = edgeLastTime(edge);
  return first > cutoff || (!first && last > cutoff);
}

function edgeFirstTime(edge: ReactFlowEdge) {
  return getEdgeTime(edge, 'first') || edgeLastTime(edge);
}

function edgeLastTime(edge: ReactFlowEdge) {
  return getEdgeTime(edge, 'last') || getEdgeTime(edge, 'first');
}

