import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import message from "antd/es/message";
import type { UploadFile } from "antd";
import type { MenuProps } from "antd";
import { type Edge, type Node, type ReactFlowInstance } from "@xyflow/react";
import type {
  ArrowMode,
  EdgeDetailPayload,
  EdgeLabelMode,
  EdgePatch,
  GraphDetailContext,
  GraphExportFormat,
  GraphLayer,
  HistoryItem,
  LineType,
  TimeWindow,
} from "./flowTypes";
import {
  buildDot,
  buildDrawio,
  buildEdgesCsv,
  buildExportZip,
  buildGraphExportPayload,
  buildGraphMl,
  buildMermaid,
  buildXMind,
  exportCanvasImage,
  graphExportFilename,
  isCanvasImageExportFormat,
  saveBlob,
} from "./flowExport";
import { aggregateRowsByDate } from "./flowAggregation";
import { buildSubjectDetailStats } from "./flowSubject";
import { loadHistoryItems, loadEdgeDetail as requestEdgeDetail } from "./flowApi";
import { useFlowGraph } from "./useFlowGraph";
import { findReciprocalPairKeys } from "./flowEdges";

export interface UseFlowPanelStateProps {
  nodes: Node[];
  edges: Edge[];
  meta: Record<string, unknown>;
  graphLayers: GraphLayer[];
  onNodeClick: (event: React.MouseEvent, node: Node) => void;
  onUpdateEdges: (edgeIds: string[], patch: EdgePatch) => void;
  onDeleteEdges: (edgeIds: string[]) => void;
  onMoveLayer: (layerId: string, deltaX: number, deltaY: number, excludeNodeId?: string) => void;
  onDeleteLayer: (layerId: string) => void;
  onUpdateEdgeText?: (edgeId: string, text: string) => void;
}

export function useFlowPanelState(props: UseFlowPanelStateProps) {
  const [subjectIds, setSubjectIds] = useState<string[]>([]);
  const [edgeLabelMode, setEdgeLabelMode] = useState<EdgeLabelMode>("amount_count");
  const [timeWindow, setTimeWindow] = useState<TimeWindow>("all");
  const [renderLimit, setRenderLimit] = useState(0);
  const [minAmount, setMinAmount] = useState(0);
  const [pathSource, setPathSource] = useState<string>();
  const [pathTarget, setPathTarget] = useState<string>();
  const [uploadFiles, setUploadFiles] = useState<UploadFile[]>([]);
  const [historyItems, setHistoryItems] = useState<HistoryItem[]>([]);
  const [selectedHistory, setSelectedHistory] = useState<string>();
  const [sourceModalOpen, setSourceModalOpen] = useState(false);
  const [inspectorOpen, setInspectorOpen] = useState(true);
  const [lineType, setLineType] = useState<LineType>("straight");
  const [arrowMode, setArrowMode] = useState<ArrowMode>("forward");
  const [lineColor, setLineColor] = useState("#111827");
  const [lineWidth, setLineWidth] = useState(1.2);
  const [optimizeAnchors, setOptimizeAnchors] = useState(true);
  const [selectedEdgeIds, setSelectedEdgeIds] = useState<string[]>([]);
  const [edgeDetailOpen, setEdgeDetailOpen] = useState(false);
  const [edgeDetailLoading, setEdgeDetailLoading] = useState(false);
  const [edgeDetail, setEdgeDetail] = useState<EdgeDetailPayload | null>(null);
  const [edgeDetailSearch, setEdgeDetailSearch] = useState("");
  const [selectedGraphLayerIds, setSelectedGraphLayerIds] = useState<string[]>([]);
  const [graphLayerPanelCollapsed, setGraphLayerPanelCollapsed] = useState(false);
  const [toolbarCollapsed, setToolbarCollapsed] = useState(true);
  const [miniMapCollapsed, setMiniMapCollapsed] = useState(false);
  const [subjectMultiSelect, setSubjectMultiSelect] = useState(false);
  const [dataPenetrationEnabled, setDataPenetrationEnabled] = useState(false);
  const [expandedPenetrationNodeIds, setExpandedPenetrationNodeIds] = useState<string[]>([]);
  const [reactFlowInstance, setReactFlowInstance] = useState<ReactFlowInstance | null>(null);
  const flowCanvasRef = useRef<HTMLDivElement | null>(null);
  const layerDragRef = useRef<{
    layerId: string;
    nodeId: string;
    x: number;
    y: number;
  } | null>(null);

  const expandDataPenetrationNode = useCallback((nodeId: string) => {
    setExpandedPenetrationNodeIds((items) => (items.includes(nodeId) ? items : [...items, nodeId]));
  }, []);

  const collapseDataPenetrationNode = useCallback((nodeId: string) => {
    setExpandedPenetrationNodeIds((items) => items.filter((id) => id !== nodeId));
  }, []);

  const {
    maxAmount,
    subjectOptions,
    nodeLabels,
    pathResult,
    visibleGraph,
    relationshipRows,
    subjectStats,
    visibleTotal,
    strongest,
    insightItems,
  } = useFlowGraph({
    edges: props.edges,
    nodes: props.nodes,
    subjectIds,
    minAmount,
    pathSource,
    pathTarget,
    edgeLabelMode,
    timeWindow,
    renderLimit,
    arrowMode,
    lineColor,
    lineType,
    lineWidth,
    optimizeAnchors,
    selectedEdgeIds,
    dataPenetrationEnabled,
    expandedPenetrationNodeIds,
    onExpandDataPenetrationNode: expandDataPenetrationNode,
    onCollapseDataPenetrationNode: collapseDataPenetrationNode,
  });
  const graphLayerKey = useMemo(() => props.graphLayers.map((layer) => layer.id).join("|"), [props.graphLayers]);

  useEffect(() => {
    setSubjectIds([]);
    setMinAmount(0);
    setPathSource(undefined);
    setPathTarget(undefined);
    setSelectedEdgeIds([]);
    setExpandedPenetrationNodeIds([]);
  }, [graphLayerKey]);

  useEffect(() => {
    const availableSubjects = new Set(subjectOptions.map((option) => option.value));
    setSubjectIds((items) => items.filter((id) => availableSubjects.has(id)));
    setPathSource((id) => (id && availableSubjects.has(id) ? id : undefined));
    setPathTarget((id) => (id && availableSubjects.has(id) ? id : undefined));
    setMinAmount((value) => Math.min(value, maxAmount || 0));
  }, [maxAmount, subjectOptions]);

  const selectedEdges = useMemo(
    () =>
      selectedEdgeIds
        .map((id) => props.edges.find((edge) => edge.id === id))
        .filter((edge): edge is Edge => Boolean(edge)),
    [props.edges, selectedEdgeIds],
  );
  const totalEdges = Number(props.meta.total_edges ?? props.edges.length);
  const totalNodes = Number(props.meta.total_nodes ?? props.nodes.length);
  const isTruncated = Boolean(props.meta.truncated);

  const exportMenuItems = useMemo<MenuProps["items"]>(
    () => [
      { key: "png", label: "PNG 图片（含透明背景）" },
      { key: "jpeg", label: "JPG 图片（含白色背景）" },
      { key: "webp", label: "WebP 图片（含透明背景）" },
      { key: "svg", label: "SVG 图片（含透明背景）" },
      { type: "divider" },
      { key: "xmind", label: "XMind 思维导图" },
      { key: "drawio", label: "Draw.io / diagrams.net" },
      { key: "graphml", label: "GraphML" },
      { key: "mermaid", label: "Mermaid" },
      { key: "dot", label: "Graphviz DOT" },
      { key: "json", label: "JSON 数据" },
      { key: "csv", label: "CSV 关系表" },
      { type: "divider" },
      { key: "zip", label: "全量格式 ZIP" },
    ],
    [],
  );

  function refreshHistory() {
    loadHistoryItems()
      .then(({ payload }) => setHistoryItems(payload.items ?? []))
      .catch(() => setHistoryItems([]));
  }

  useEffect(() => {
    refreshHistory();
  }, []);

  useEffect(() => {
    setSelectedGraphLayerIds((current) =>
      current.filter((id) => props.graphLayers.some((layer) => layer.id === id)),
    );
  }, [props.graphLayers]);

  async function exportCurrentGraph(format: GraphExportFormat) {
    if (!visibleGraph.nodes.length) {
      message.warning("当前画布没有可导出的节点。");
      return;
    }
    const payload = buildGraphExportPayload(
      visibleGraph.nodes,
      visibleGraph.edges,
      props.meta,
      props.graphLayers,
    );
    const filename = graphExportFilename(payload);
    try {
      if (isCanvasImageExportFormat(format)) {
        await exportCanvasImage(format, flowCanvasRef.current, filename);
      } else if (format === "json") {
        saveBlob(
          new Blob([JSON.stringify(payload, null, 2)], {
            type: "application/json;charset=utf-8",
          }),
          `${filename}.json`,
        );
      } else if (format === "csv") {
        saveBlob(
          new Blob([buildEdgesCsv(payload)], { type: "text/csv;charset=utf-8" }),
          `${filename}_edges.csv`,
        );
      } else if (format === "graphml") {
        saveBlob(
          new Blob([buildGraphMl(payload)], {
            type: "application/graphml+xml;charset=utf-8",
          }),
          `${filename}.graphml`,
        );
      } else if (format === "dot") {
        saveBlob(
          new Blob([buildDot(payload)], { type: "text/vnd.graphviz;charset=utf-8" }),
          `${filename}.dot`,
        );
      } else if (format === "mermaid") {
        saveBlob(
          new Blob([buildMermaid(payload)], { type: "text/markdown;charset=utf-8" }),
          `${filename}.mmd`,
        );
      } else if (format === "drawio") {
        saveBlob(
          new Blob([buildDrawio(payload)], { type: "application/xml;charset=utf-8" }),
          `${filename}.drawio`,
        );
      } else if (format === "xmind") {
        saveBlob(await buildXMind(payload), `${filename}.xmind`);
      } else {
        saveBlob(
          await buildExportZip(payload, flowCanvasRef.current, filename),
          `${filename}_exports.zip`,
        );
      }
      message.success("数据导入完成。");
    } catch (error) {
      message.error(error instanceof Error ? error.message : "数据导入失败");
    }
  }

  function handleEdgeClick(event: React.MouseEvent, edge: Edge) {
    event.stopPropagation();
    setSelectedEdgeIds((current) => {
      if (event.ctrlKey || event.metaKey || event.shiftKey) {
        return current.includes(edge.id)
          ? current.filter((id) => id !== edge.id)
          : [...current, edge.id];
      }
      return [edge.id];
    });
  }

  function handleNodeClick(event: React.MouseEvent, node: Node) {
    props.onNodeClick(event, {
      ...node,
      data: {
        ...node.data,
        visibleSubjectStats: buildSubjectDetailStats(node, visibleGraph.edges),
      },
    });
  }

  function updateSelectedEdges(patch: EdgePatch) {
    if (!selectedEdgeIds.length) return;
    props.onUpdateEdges(selectedEdgeIds, patch);
  }

  function deleteSelectedEdges() {
    if (!selectedEdgeIds.length) return;
    props.onDeleteEdges(selectedEdgeIds);
    setSelectedEdgeIds([]);
    message.success(
      selectedEdgeIds.length > 1 ? "已删除选中的关系" : "已删除该关系",
    );
  }

  function toggleGraphLayerSelection(layerId: string) {
    setSelectedGraphLayerIds((current) =>
      current.includes(layerId)
        ? current.filter((id) => id !== layerId)
        : [...current, layerId],
    );
  }

  function handleNodeDragStart(_event: React.MouseEvent, node: Node) {
    const selectedNodeIds = props.nodes
      .filter((item) => item.selected)
      .map((item) => item.id);
    if (selectedNodeIds.length > 1 && selectedNodeIds.includes(node.id)) {
      layerDragRef.current = null;
      return;
    }
    const layerId = String(node.data?.graphLayerId ?? "");
    layerDragRef.current =
      layerId && selectedGraphLayerIds.includes(layerId)
        ? {
            layerId,
            nodeId: node.id,
            x: node.position.x,
            y: node.position.y,
          }
        : null;
  }

  function handleNodeDrag(_event: React.MouseEvent, node: Node) {
    const current = layerDragRef.current;
    if (!current || current.nodeId !== node.id) return;
    const deltaX = node.position.x - current.x;
    const deltaY = node.position.y - current.y;
    if (!deltaX && !deltaY) return;
    selectedGraphLayerIds.forEach((layerId) =>
      props.onMoveLayer(
        layerId,
        deltaX,
        deltaY,
        layerId === current.layerId ? node.id : undefined,
      ),
    );
    layerDragRef.current = { ...current, x: node.position.x, y: node.position.y };
  }

  function centerGraphLayer(layerId: string) {
    const layerNodes = props.nodes.filter(
      (node) => node.data?.graphLayerId === layerId,
    );
    if (!reactFlowInstance || !layerNodes.length) return;
    reactFlowInstance.fitView({
      nodes: layerNodes.map((node) => ({ id: node.id })),
      padding: 0.25,
      duration: 450,
    });
  }

  function deleteGraphLayerFromPanel(layerId: string) {
    props.onDeleteLayer(layerId);
    setSelectedGraphLayerIds((current) => current.filter((id) => id !== layerId));
  }

  async function openEdgeDetail(edge: Edge) {
    const context = edge.data?.detailContext as GraphDetailContext | undefined;
    if (!context || context.kind === "none") {
      message.warning("请使用上传/导入功能加载数据流图，再查看节点详情。");
      return;
    }
    setEdgeDetailOpen(true);
    setEdgeDetailLoading(true);
    setEdgeDetailSearch("");
    try {
      const { response, payload } = await requestEdgeDetail(
        context,
        String(edge.data?.rawSource ?? edge.source),
        String(edge.data?.rawTarget ?? edge.target),
      );
      if (!response.ok) throw new Error(payload.detail || "获取节点详情失败");
      setEdgeDetail(payload as EdgeDetailPayload);
    } catch (error) {
      message.error(error instanceof Error ? error.message : "获取节点详情失败");
      setEdgeDetail(null);
    } finally {
      setEdgeDetailLoading(false);
    }
  }

  async function recalculateEdgeByDate(edge: Edge, range: any) {
    const context = edge.data?.detailContext as GraphDetailContext | undefined;
    if (!context || context.kind === "none") {
      message.warning("请使用上传/导入功能加载数据流图，再按时间聚合。");
      return;
    }
    const start = range?.[0]?.startOf?.("day")?.valueOf?.();
    const end = range?.[1]?.endOf?.("day")?.valueOf?.();
    try {
      const { response, payload } = await requestEdgeDetail(
        context,
        String(edge.data?.rawSource ?? edge.source),
        String(edge.data?.rawTarget ?? edge.target),
      );
      if (!response.ok) throw new Error(payload.detail || "获取节点详情失败");
      const stats = aggregateRowsByDate(
        (payload.rows ?? []) as Record<string, unknown>[],
        start,
        end,
      );
      props.onUpdateEdges([edge.id], stats);
      message.success(
        `流水汇总：${stats.tx_count ?? 0} 笔，${formatMoney(stats.amount ?? 0)}`,
      );
    } catch (error) {
      message.error(error instanceof Error ? error.message : "获取流水统计失败");
    }
  }

  return {
    // state
    subjectIds,
    setSubjectIds,
    edgeLabelMode,
    setEdgeLabelMode,
    timeWindow,
    setTimeWindow,
    renderLimit,
    setRenderLimit,
    minAmount,
    setMinAmount,
    pathSource,
    setPathSource,
    pathTarget,
    setPathTarget,
    uploadFiles,
    setUploadFiles,
    historyItems,
    setHistoryItems,
    selectedHistory,
    setSelectedHistory,
    sourceModalOpen,
    setSourceModalOpen,
    inspectorOpen,
    setInspectorOpen,
    lineType,
    setLineType,
    arrowMode,
    setArrowMode,
    lineColor,
    setLineColor,
    lineWidth,
    setLineWidth,
    optimizeAnchors,
    setOptimizeAnchors,
    selectedEdgeIds,
    setSelectedEdgeIds,
    edgeDetailOpen,
    setEdgeDetailOpen,
    edgeDetailLoading,
    edgeDetail,
    edgeDetailSearch,
    setEdgeDetailSearch,
    selectedGraphLayerIds,
    setSelectedGraphLayerIds,
    graphLayerPanelCollapsed,
    setGraphLayerPanelCollapsed,
    toolbarCollapsed,
    setToolbarCollapsed,
    miniMapCollapsed,
    setMiniMapCollapsed,
    subjectMultiSelect,
    setSubjectMultiSelect,
    dataPenetrationEnabled,
    setDataPenetrationEnabled,
    expandedPenetrationNodeIds,
    setExpandedPenetrationNodeIds,
    reactFlowInstance,
    setReactFlowInstance,
    flowCanvasRef,
    layerDragRef,
    // computed from useFlowGraph
    maxAmount,
    subjectOptions,
    nodeLabels,
    pathResult,
    visibleGraph,
    relationshipRows,
    subjectStats,
    visibleTotal,
    strongest,
    insightItems,
    // computed locally
    selectedEdges,
    totalEdges,
    totalNodes,
    isTruncated,
    exportMenuItems,
    // handlers
    refreshHistory,
    exportCurrentGraph,
    handleEdgeClick,
    handleNodeClick,
    updateSelectedEdges,
    deleteSelectedEdges,
    toggleGraphLayerSelection,
    handleNodeDragStart,
    handleNodeDrag,
    centerGraphLayer,
    deleteGraphLayerFromPanel,
    openEdgeDetail,
    recalculateEdgeByDate,
  };
}

function formatMoney(value: number) {
  return Number(value || 0).toLocaleString("zh-CN", { maximumFractionDigits: 0 });
}

