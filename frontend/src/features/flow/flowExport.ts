import type { Edge, Node } from '@xyflow/react';
import { toCanvas, toSvg } from 'html-to-image';
import JSZip from 'jszip';
import type { CanvasImageExportFormat, GraphExportFormat, GraphExportPayload, GraphLayer } from './flowTypes';

export function saveBlob(blob: Blob, filename: string) {
  const blobUrl = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = blobUrl;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(blobUrl), 250);
}

export function isCanvasImageExportFormat(format: GraphExportFormat): format is CanvasImageExportFormat {
  return format === 'png' || format === 'jpeg' || format === 'webp' || format === 'svg';
}

export async function exportCanvasImage(format: CanvasImageExportFormat, container: HTMLElement | null, filename: string) {
  if (format === 'svg') {
    saveBlob(await captureCanvasSvg(container), `${filename}.svg`);
    return;
  }
  const blob = await captureCanvasRaster(container, format);
  saveBlob(blob, `${filename}.${format === 'jpeg' ? 'jpg' : format}`);
}

async function captureCanvasRaster(container: HTMLElement | null, format: Exclude<CanvasImageExportFormat, 'svg'>) {
  const target = findReactFlowExportTarget(container);
  const { restore, bounds } = expandForFullCapture(target);
  try {
    const canvas = await toCanvas(target, {
      backgroundColor: '#fbfcfe',
      cacheBust: true,
      filter: shouldExportDomNode,
      pixelRatio: 2,
      width: bounds.width,
      height: bounds.height,
    });
    const mimeType = format === 'jpeg' ? 'image/jpeg' : format === 'webp' ? 'image/webp' : 'image/png';
    return canvasToBlob(canvas, mimeType, format === 'jpeg' ? 0.92 : 0.96);
  } finally {
    restore();
  }
}

async function captureCanvasSvg(container: HTMLElement | null) {
  const target = findReactFlowExportTarget(container);
  const { restore, bounds } = expandForFullCapture(target);
  try {
    const dataUrl = await toSvg(target, {
      backgroundColor: '#fbfcfe',
      cacheBust: true,
      filter: shouldExportDomNode,
      width: bounds.width,
      height: bounds.height,
    });
    const response = await fetch(dataUrl);
    return response.blob();
  } finally {
    restore();
  }
}

function findReactFlowExportTarget(container: HTMLElement | null) {
  const target = container?.querySelector('.react-flow') as HTMLElement | null;
  if (!target) throw new Error('没有找到可导出的画布。');
  const rect = target.getBoundingClientRect();
  if (!rect.width || !rect.height) throw new Error('画布尺寸为空，无法导出图片。');
  return target;
}

function shouldExportDomNode(node: HTMLElement) {
  const classList = node.classList;
  return !(
    classList?.contains('react-flow__controls') ||
    classList?.contains('react-flow__minimap') ||
    classList?.contains('flow-minimap') ||
    classList?.contains('minimap-toggle') ||
    classList?.contains('edge-floating-panel')
  );
}

/**
 * Temporarily expands the ReactFlow container to encompass all graph nodes,
 * so html-to-image captures the full graph instead of only the visible viewport.
 * Returns a restore function and the expanded bounds.
 */
function expandForFullCapture(target: HTMLElement) {
  const viewport = target.querySelector('.react-flow__viewport') as HTMLElement | null;
  const nodes = target.querySelectorAll('.react-flow__node');

  const empty = { restore: () => {}, bounds: { width: 0, height: 0 } };
  if (!viewport || nodes.length === 0) return empty;

  // Compute bounding box of all nodes relative to the container
  const targetRect = target.getBoundingClientRect();
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;

  nodes.forEach((node) => {
    const rect = node.getBoundingClientRect();
    minX = Math.min(minX, rect.left - targetRect.left);
    minY = Math.min(minY, rect.top - targetRect.top);
    maxX = Math.max(maxX, rect.right - targetRect.left);
    maxY = Math.max(maxY, rect.bottom - targetRect.top);
  });

  if (!isFinite(minX)) return empty;

  // Save original styles
  const origOverflow = target.style.overflow;
  const origWidth = target.style.width;
  const origHeight = target.style.height;
  const origViewportTransform = viewport.style.transform;

  const padding = 40;
  const fullWidth = Math.ceil(maxX - minX + padding * 2);
  const fullHeight = Math.ceil(maxY - minY + padding * 2);

  // Parse current viewport scale
  let scale = 1;
  const scaleMatch = origViewportTransform.match(/scale\(([\d.]+)\)/);
  if (scaleMatch) scale = parseFloat(scaleMatch[1]);

  // Expand container so nothing is clipped
  target.style.overflow = 'visible';
  target.style.width = `${fullWidth}px`;
  target.style.height = `${fullHeight}px`;

  // Re-center the content so the leftmost/topmost node is at (padding, padding)
  const offsetX = padding - minX;
  const offsetY = padding - minY;
  viewport.style.transform = `translate(${offsetX}px, ${offsetY}px) scale(${scale})`;

  return {
    restore: () => {
      target.style.overflow = origOverflow;
      target.style.width = origWidth;
      target.style.height = origHeight;
      viewport.style.transform = origViewportTransform;
    },
    bounds: { width: fullWidth, height: fullHeight },
  };
}

function canvasToBlob(canvas: HTMLCanvasElement, type: string, quality: number) {
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error('浏览器没有生成图片文件。'));
    }, type, quality);
  });
}

export function buildGraphExportPayload(nodes: Node[], edges: Edge[], meta: Record<string, unknown>, layers: GraphLayer[]): GraphExportPayload {
  const exportNodes = nodes.map((node) => {
    const data = (node.data ?? {}) as Record<string, unknown>;
    return {
      id: node.id,
      label: stringifyExportValue(data.entityLabel ?? data.label ?? node.id),
      kind: stringifyExportValue(data.entityKind ?? 'unknown'),
      layer: stringifyExportValue(data.graphLayerLabel ?? data.graphLayerId ?? ''),
      x: Math.round(Number(node.position?.x ?? 0)),
      y: Math.round(Number(node.position?.y ?? 0)),
      tags: Array.isArray(data.tags) ? data.tags.map((tag) => stringifyExportValue(tag)).filter(Boolean) : [],
    };
  });
  const labelMap = new Map(exportNodes.map((node) => [node.id, node.label]));
  const exportEdges = edges.map((edge) => {
    const data = (edge.data ?? {}) as Record<string, unknown>;
    return {
      id: edge.id,
      source: edge.source,
      sourceLabel: labelMap.get(edge.source) ?? edge.source,
      target: edge.target,
      targetLabel: labelMap.get(edge.target) ?? edge.target,
      label: stringifyExportValue(data.customLabel ?? data.displayLabel ?? edge.label ?? ''),
      amount: toExportNumber(data.amount),
      tx_count: toExportNumber(data.tx_count),
      avg_amount: toExportNumber(data.avg_amount),
      max_amount: toExportNumber(data.max_amount),
      first_time: stringifyExportValue(data.first_time ?? ''),
      last_time: stringifyExportValue(data.last_time ?? ''),
      layer: stringifyExportValue(data.graphLayerLabel ?? data.graphLayerId ?? ''),
    };
  });
  return {
    exported_at: new Date().toISOString(),
    meta,
    layers,
    nodes: exportNodes,
    edges: exportEdges,
  };
}

export function graphExportFilename(payload: GraphExportPayload) {
  const label = payload.layers[0]?.label || 'flow_canvas';
  const stamp = new Date().toISOString().replace(/[-:]/g, '').replace(/\.\d+Z$/, '');
  return `${safeFilenamePart(label)}_${stamp}`;
}

function safeFilenamePart(value: string) {
  return String(value || 'flow_canvas')
    .replace(/[\\/:*?"<>|]+/g, '_')
    .replace(/\s+/g, '_')
    .slice(0, 64) || 'flow_canvas';
}

export function buildEdgesCsv(payload: GraphExportPayload) {
  const rows = [
    ['edge_id', 'source_id', 'source_label', 'target_id', 'target_label', 'label', 'amount', 'tx_count', 'avg_amount', 'max_amount', 'first_time', 'last_time', 'layer'],
    ...payload.edges.map((edge) => [
      edge.id,
      edge.source,
      edge.sourceLabel,
      edge.target,
      edge.targetLabel,
      edge.label,
      edge.amount,
      edge.tx_count,
      edge.avg_amount,
      edge.max_amount,
      edge.first_time,
      edge.last_time,
      edge.layer,
    ]),
  ];
  return `\ufeff${rows.map((row) => row.map(csvCell).join(',')).join('\r\n')}`;
}

function buildNodesCsv(payload: GraphExportPayload) {
  const rows = [
    ['node_id', 'label', 'kind', 'layer', 'x', 'y', 'tags'],
    ...payload.nodes.map((node) => [node.id, node.label, node.kind, node.layer, node.x, node.y, node.tags.join('|')]),
  ];
  return `\ufeff${rows.map((row) => row.map(csvCell).join(',')).join('\r\n')}`;
}

export function buildGraphMl(payload: GraphExportPayload) {
  const nodeIds = new Map(payload.nodes.map((node, index) => [node.id, `n${index}`]));
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<graphml xmlns="http://graphml.graphdrawing.org/xmlns">',
    '  <key id="label" for="all" attr.name="label" attr.type="string"/>',
    '  <key id="kind" for="node" attr.name="kind" attr.type="string"/>',
    '  <key id="amount" for="edge" attr.name="amount" attr.type="double"/>',
    '  <key id="tx_count" for="edge" attr.name="tx_count" attr.type="int"/>',
    '  <graph id="flow_canvas" edgedefault="directed">',
    ...payload.nodes.map((node) => [
      `    <node id="${xmlAttr(nodeIds.get(node.id) ?? node.id)}">`,
      `      <data key="label">${xmlText(node.label)}</data>`,
      `      <data key="kind">${xmlText(node.kind)}</data>`,
      '    </node>',
    ].join('\n')),
    ...payload.edges.map((edge, index) => [
      `    <edge id="e${index}" source="${xmlAttr(nodeIds.get(edge.source) ?? edge.source)}" target="${xmlAttr(nodeIds.get(edge.target) ?? edge.target)}">`,
      `      <data key="label">${xmlText(edge.label)}</data>`,
      `      <data key="amount">${edge.amount}</data>`,
      `      <data key="tx_count">${edge.tx_count}</data>`,
      '    </edge>',
    ].join('\n')),
    '  </graph>',
    '</graphml>',
  ].join('\n');
}

export function buildDot(payload: GraphExportPayload) {
  const nodeIds = new Map(payload.nodes.map((node, index) => [node.id, `n${index}`]));
  return [
    'digraph FlowCanvas {',
    '  graph [rankdir=LR];',
    '  node [shape=box, style="rounded,filled", fillcolor="#f8fafc", color="#94a3b8", fontname="Microsoft YaHei"];',
    '  edge [color="#111827", fontname="Microsoft YaHei"];',
    ...payload.nodes.map((node) => `  ${nodeIds.get(node.id)} [label="${dotText(node.label)}"];`),
    ...payload.edges.map((edge) => `  ${nodeIds.get(edge.source)} -> ${nodeIds.get(edge.target)} [label="${dotText(edge.label || `${edge.amount} / ${edge.tx_count}`)}"];`),
    '}',
  ].join('\n');
}

export function buildMermaid(payload: GraphExportPayload) {
  const nodeIds = new Map(payload.nodes.map((node, index) => [node.id, `N${index}`]));
  return [
    'flowchart LR',
    ...payload.nodes.map((node) => `  ${nodeIds.get(node.id)}["${mermaidText(node.label)}"]`),
    ...payload.edges.map((edge) => `  ${nodeIds.get(edge.source)} -->|"${mermaidText(edge.label || `${edge.amount} / ${edge.tx_count}`)}"| ${nodeIds.get(edge.target)}`),
  ].join('\n');
}

export function buildDrawio(payload: GraphExportPayload) {
  const nodeIds = new Map(payload.nodes.map((node, index) => [node.id, `node-${index}`]));
  const cells = [
    '<mxCell id="0"/>',
    '<mxCell id="1" parent="0"/>',
    ...payload.nodes.map((node) => {
      const id = nodeIds.get(node.id) ?? node.id;
      return `<mxCell id="${xmlAttr(id)}" value="${xmlAttr(node.label)}" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#f8fafc;strokeColor=#94a3b8;" vertex="1" parent="1"><mxGeometry x="${node.x}" y="${node.y}" width="180" height="56" as="geometry"/></mxCell>`;
    }),
    ...payload.edges.map((edge, index) => {
      const label = edge.label || `${formatMoney(edge.amount)} / ${edge.tx_count}`;
      return `<mxCell id="edge-${index}" value="${xmlAttr(label)}" style="endArrow=block;html=1;rounded=0;strokeColor=#111827;" edge="1" parent="1" source="${xmlAttr(nodeIds.get(edge.source) ?? edge.source)}" target="${xmlAttr(nodeIds.get(edge.target) ?? edge.target)}"><mxGeometry relative="1" as="geometry"/></mxCell>`;
    }),
  ];
  return [
    '<mxfile host="app.diagrams.net" type="device">',
    '  <diagram id="flow-canvas" name="资金流向图">',
    `    <mxGraphModel dx="1422" dy="794" grid="1" gridSize="10" guides="1" tooltips="1" connect="1" arrows="1" fold="1" page="1" pageScale="1" pageWidth="1400" pageHeight="900" math="0" shadow="0"><root>${cells.join('')}</root></mxGraphModel>`,
    '  </diagram>',
    '</mxfile>',
  ].join('\n');
}

export async function buildXMind(payload: GraphExportPayload) {
  const zip = new JSZip();
  const topicIds = new Map(payload.nodes.map((node, index) => [node.id, `topic-${index}`]));
  const topics = payload.nodes.map((node) => ({
    id: topicIds.get(node.id),
    class: 'topic',
    title: node.label,
    labels: node.tags,
  }));
  const content = [
    {
      id: 'sheet-1',
      class: 'sheet',
      title: '资金流向图',
      rootTopic: {
        id: 'root-topic',
        class: 'topic',
        title: '资金流向图',
        children: { attached: topics },
      },
      relationships: payload.edges.map((edge, index) => ({
        id: `relationship-${index}`,
        class: 'relationship',
        end1Id: topicIds.get(edge.source),
        end2Id: topicIds.get(edge.target),
        title: edge.label || `${formatMoney(edge.amount)} / ${edge.tx_count}`,
      })),
    },
  ];
  zip.file('content.json', JSON.stringify(content, null, 2));
  zip.file('metadata.json', JSON.stringify({ creator: { name: 'Funds ETL Canvas Exporter' }, activeSheetId: 'sheet-1' }, null, 2));
  zip.file('manifest.json', JSON.stringify({ 'file-entries': { 'content.json': {}, 'metadata.json': {} } }, null, 2));
  return zip.generateAsync({ type: 'blob', mimeType: 'application/vnd.xmind.workbook' });
}

export async function buildExportZip(payload: GraphExportPayload, container: HTMLElement | null, filename: string) {
  const zip = new JSZip();
  zip.file(`${filename}.json`, JSON.stringify(payload, null, 2));
  zip.file(`${filename}_nodes.csv`, buildNodesCsv(payload));
  zip.file(`${filename}_edges.csv`, buildEdgesCsv(payload));
  zip.file(`${filename}.graphml`, buildGraphMl(payload));
  zip.file(`${filename}.dot`, buildDot(payload));
  zip.file(`${filename}.mmd`, buildMermaid(payload));
  zip.file(`${filename}.drawio`, buildDrawio(payload));
  zip.file(`${filename}.xmind`, await buildXMind(payload));
  try {
    zip.file(`${filename}.svg`, await captureCanvasSvg(container));
    zip.file(`${filename}.png`, await captureCanvasRaster(container, 'png'));
  } catch {
    zip.file('image-export-warning.txt', 'Canvas image export failed in this browser, but data formats were generated.');
  }
  return zip.generateAsync({ type: 'blob', mimeType: 'application/zip' });
}

function csvCell(value: unknown) {
  const text = stringifyExportValue(value);
  return /[",\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

function xmlText(value: unknown) {
  return stringifyExportValue(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

function xmlAttr(value: unknown) {
  return xmlText(value)
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

function dotText(value: unknown) {
  return stringifyExportValue(value).replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\r?\n/g, '\\n');
}

function mermaidText(value: unknown) {
  return stringifyExportValue(value).replace(/"/g, '#quot;').replace(/\r?\n/g, '<br/>');
}

function stringifyExportValue(value: unknown) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function toExportNumber(value: unknown) {
  const number = Number(value ?? 0);
  return Number.isFinite(number) ? number : 0;
}
function formatMoney(value: number) {
  return Number(value || 0).toLocaleString('zh-CN', { maximumFractionDigits: 0 });
}

