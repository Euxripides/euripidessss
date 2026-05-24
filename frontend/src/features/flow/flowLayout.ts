import type { ProcessResponse } from '../../types';
import type { LayoutTreeEdge } from './flowTypes';

export function layoutFlowGraph(graph: ProcessResponse['flow_graph']) {
  const nodeIds = graph.nodes.map((node) => node.id);
  const layoutEdges = buildLayoutEdges(graph);
  const outgoing = new Map<string, LayoutTreeEdge[]>();
  const incoming = new Map<string, LayoutTreeEdge[]>();
  const undirected = new Map<string, Set<string>>();
  for (const id of nodeIds) {
    outgoing.set(id, []);
    incoming.set(id, []);
    undirected.set(id, new Set());
  }
  for (const edge of layoutEdges) {
    const item = { source: edge.source, target: edge.target, amount: Number(edge.amount ?? 0) };
    outgoing.get(item.source)?.push(item);
    incoming.get(item.target)?.push(item);
    undirected.get(item.source)?.add(item.target);
    undirected.get(item.target)?.add(item.source);
  }
  for (const edges of outgoing.values()) {
    edges.sort((a, b) => b.amount - a.amount || nodeLayoutWeight(b.target, graph) - nodeLayoutWeight(a.target, graph));
  }

  const components = connectedComponents(nodeIds, undirected)
    .sort((a, b) => componentWeight(b, graph) - componentWeight(a, graph));
  const depth = new Map<string, number>();
  const children = new Map<string, string[]>();
  const roots: string[] = [];
  const assigned = new Set<string>();

  for (const component of components) {
    const componentSet = new Set(component);
    let componentRoots = component
      .filter((id) => !(incoming.get(id) ?? []).some((edge) => componentSet.has(edge.source)))
      .sort((a, b) => nodeLayoutWeight(b, graph) - nodeLayoutWeight(a, graph));
    if (!componentRoots.length) {
      componentRoots = [[...component].sort((a, b) => nodeLayoutBalance(b, graph) - nodeLayoutBalance(a, graph) || nodeLayoutWeight(b, graph) - nodeLayoutWeight(a, graph))[0]];
    }
    for (const root of componentRoots) {
      if (assigned.has(root)) continue;
      roots.push(root);
      assignTreePositions(root, componentSet, outgoing, assigned, depth, children);
    }
    const leftovers = component
      .filter((id) => !assigned.has(id))
      .sort((a, b) => nodeLayoutBalance(b, graph) - nodeLayoutBalance(a, graph) || nodeLayoutWeight(b, graph) - nodeLayoutWeight(a, graph));
    for (const root of leftovers) {
      roots.push(root);
      assignTreePositions(root, componentSet, outgoing, assigned, depth, children);
    }
  }

  const positions = new Map<string, { x: number; y: number }>();
  const xGap = 330;
  const yGap = 120;
  const rootGap = 72;
  const xStart = 72;
  let nextY = 80;
  const sortedRoots = roots.sort((a, b) => nodeLayoutWeight(b, graph) - nodeLayoutWeight(a, graph));
  for (const root of sortedRoots) {
    nextY = placeTree(root, nextY, xStart, xGap, yGap, depth, children, positions, graph);
    nextY += rootGap;
  }
  return positions;
}

function buildLayoutEdges(graph: ProcessResponse['flow_graph']) {
  const byPair = new Map<string, typeof graph.edges>();
  for (const edge of graph.edges) {
    const key = [edge.source, edge.target].sort().join('<->');
    byPair.set(key, [...(byPair.get(key) ?? []), edge]);
  }
  return Array.from(byPair.values()).map((edges) => {
    if (edges.length === 1) return edges[0];
    return [...edges].sort((a, b) => {
      const amountDiff = Number(b.amount ?? 0) - Number(a.amount ?? 0);
      if (amountDiff) return amountDiff;
      return nodeLayoutBalance(b.source, graph) - nodeLayoutBalance(a.source, graph);
    })[0];
  });
}

function connectedComponents(nodeIds: string[], adjacency: Map<string, Set<string>>) {
  const seen = new Set<string>();
  const components: string[][] = [];
  for (const id of nodeIds) {
    if (seen.has(id)) continue;
    const component: string[] = [];
    const stack = [id];
    seen.add(id);
    while (stack.length) {
      const current = stack.pop() as string;
      component.push(current);
      for (const next of adjacency.get(current) ?? []) {
        if (seen.has(next)) continue;
        seen.add(next);
        stack.push(next);
      }
    }
    components.push(component);
  }
  return components;
}

function assignTreePositions(
  root: string,
  component: Set<string>,
  outgoing: Map<string, LayoutTreeEdge[]>,
  assigned: Set<string>,
  depth: Map<string, number>,
  children: Map<string, string[]>,
) {
  const queue = [root];
  assigned.add(root);
  depth.set(root, depth.get(root) ?? 0);
  while (queue.length) {
    const current = queue.shift() as string;
    const nextDepth = (depth.get(current) ?? 0) + 1;
    const nextChildren: string[] = [];
    for (const edge of outgoing.get(current) ?? []) {
      if (!component.has(edge.target) || assigned.has(edge.target)) continue;
      assigned.add(edge.target);
      depth.set(edge.target, nextDepth);
      nextChildren.push(edge.target);
      queue.push(edge.target);
    }
    children.set(current, [...(children.get(current) ?? []), ...nextChildren]);
  }
}

function placeTree(
  nodeId: string,
  nextY: number,
  xStart: number,
  xGap: number,
  yGap: number,
  depth: Map<string, number>,
  children: Map<string, string[]>,
  positions: Map<string, { x: number; y: number }>,
  graph: ProcessResponse['flow_graph'],
) {
  const childIds = [...(children.get(nodeId) ?? [])].sort((a, b) => subtreeWeight(b, children, graph) - subtreeWeight(a, children, graph));
  if (!childIds.length) {
    positions.set(nodeId, { x: xStart + (depth.get(nodeId) ?? 0) * xGap, y: nextY });
    return nextY + yGap;
  }
  let cursor = nextY;
  const childCenters: number[] = [];
  for (const child of childIds) {
    const before = cursor;
    cursor = placeTree(child, cursor, xStart, xGap, yGap, depth, children, positions, graph);
    childCenters.push((before + cursor - yGap) / 2);
  }
  const y = childCenters.reduce((sum, value) => sum + value, 0) / childCenters.length;
  positions.set(nodeId, { x: xStart + (depth.get(nodeId) ?? 0) * xGap, y });
  return cursor;
}

function subtreeWeight(nodeId: string, children: Map<string, string[]>, graph: ProcessResponse['flow_graph']): number {
  return nodeLayoutWeight(nodeId, graph) + (children.get(nodeId) ?? []).reduce((sum, child) => sum + subtreeWeight(child, children, graph), 0);
}

function componentWeight(nodeIds: string[], graph: ProcessResponse['flow_graph']) {
  return nodeIds.reduce((sum, id) => sum + nodeLayoutWeight(id, graph), 0);
}

function nodeLayoutWeight(nodeId: string, graph: ProcessResponse['flow_graph']) {
  const node = graph.nodes.find((item) => item.id === nodeId);
  return Number(node?.amount_in ?? 0) + Number(node?.amount_out ?? 0) + Number(node?.degree ?? 0) * 1000;
}

function nodeLayoutBalance(nodeId: string, graph: ProcessResponse['flow_graph']) {
  const node = graph.nodes.find((item) => item.id === nodeId);
  return Number(node?.amount_out ?? 0) - Number(node?.amount_in ?? 0);
}
