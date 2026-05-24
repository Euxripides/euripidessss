import { useMemo, useRef, useState, useEffect } from "react";







import { Form } from "antd";







import message from "antd/es/message";







import type { UploadFile } from "antd";







import {







  addEdge,







  applyEdgeChanges,







  applyNodeChanges,







  type Edge,







  MarkerType,







  type Node,







  type OnConnect,







  type OnEdgesChange,







  type OnNodesChange,







} from "@xyflow/react";







import type { ProcessResponse } from "../types";

type FlowGraphPayload = ProcessResponse["flow_graph"];

type BuildFlowPayload = Partial<ProcessResponse> & Partial<FlowGraphPayload>;

function normalizeFlowGraphPayload(payload: BuildFlowPayload): FlowGraphPayload {
  const graph = payload.flow_graph ?? {
    nodes: payload.nodes,
    edges: payload.edges,
    meta: payload.meta,
  };

  return {
    nodes: Array.isArray(graph?.nodes) ? graph.nodes : [],
    edges: Array.isArray(graph?.edges) ? graph.edges : [],
    meta: graph?.meta ?? {},
  };
}







import {







  type DirectionRulePending,



  type NetworkMode,







  type EdgePatch,







  type EntityKind,







  type FlowBuildStatus,







  type FlowFieldMapping,







  type FlowImportProgress,







  type GraphDetailContext,







  type GraphLayer,







  type ImportedDataset,







  type ManualNodeFormValues,







  type NodeConnectionFormValues,







  type SourceFilterPayload,







  type TargetFilterPayload,







  type TransferStatus,







} from "../features/flow/flowTypes";







import {







  buildFlowElements,







  nextGraphOffset,







} from "../features/flow/flowElements";







import { renderFlowNodeLabel } from "../features/flow/flowNodes";







import { attachManualEdgeLayer, createManualEdge } from "../features/flow/flowManual";







import {







  buildSubjectDetailStats,







  nodeSelectOptions,







  normalizeManualLinks,







  uniqueDisplayLabel,







} from "../features/flow/flowSubject";







import {







  autoFlowMapping,







  flowTemplateMatches,







  requiredFlowMappingMissing,







  sanitizeFlowMapping,







} from "../features/flow/flowMapping";







import {







  buildFlowGraph as requestBuildFlowGraph,







  isUnknownDirectionPayload,







  loadHistoryGraph as requestHistoryGraph,







  loadUnknownDirectionValues,







  runFlowAnalysis,







  saveDirectionRules,







  saveMappingRule,







} from "../features/flow/flowApi";







import {







  buildUploadForm,







  downloadWithProgress,







  failTransfer,







  requestJsonWithProgress,







  uploadFlowImport,







} from "../features/upload/uploadApi";















export interface UseFlowOperationsOptions {







  networkMode: NetworkMode;







  updateTransferStatus: (status: TransferStatus) => void;







  setMappingModalOpen: (open: boolean) => void;







}















export function useFlowOperations(options: UseFlowOperationsOptions) {







  const { networkMode, updateTransferStatus, setMappingModalOpen } = options;















  const [nodes, setNodes] = useState<Node[]>([]);







  const [edges, setEdges] = useState<Edge[]>([]);







  const [flowMeta, setFlowMeta] = useState<Record<string, unknown>>({});







  const [graphLayers, setGraphLayers] = useState<GraphLayer[]>([]);







  const [importedDataset, setImportedDataset] = useState<ImportedDataset | null>(null);







  const [flowImportProgress, setFlowImportProgress] = useState<FlowImportProgress>({







    visible: false, percent: 0, status: "normal", text: "",







  });







  const [flowBuildStatus, setFlowBuildStatus] = useState<FlowBuildStatus>({







    visible: false, status: "normal", text: "",







  });







  const [analysisReport, setAnalysisReport] = useState("");







  const [fieldMapping, setFieldMapping] = useState<FlowFieldMapping>({});







  const [flowLoading, setFlowLoading] = useState(false);







  const [directionRulePending, setDirectionRulePending] = useState<DirectionRulePending | null>(null);







  const [directionRuleValues, setDirectionRuleValues] = useState<Record<string, "进" | "出">>({});







  const [selectedNode, setSelectedNode] = useState<Node | null>(null);







  const [addNodeOpen, setAddNodeOpen] = useState(false);







  const [tagInput, setTagInput] = useState("");







  const [featureInput, setFeatureInput] = useState("");







  const graphLayerSeq = useRef(0);







  const [nodeConnectionForm] = Form.useForm<NodeConnectionFormValues>();







  const nodeOutgoingEnabled = Form.useWatch("outgoingEnabled", nodeConnectionForm);







  const nodeIncomingEnabled = Form.useWatch("incomingEnabled", nodeConnectionForm);















  const onNodesChange: OnNodesChange = (changes) =>







    setNodes((items) => applyNodeChanges(changes, items));







  const onEdgesChange: OnEdgesChange = (changes) =>







    setEdges((items) => applyEdgeChanges(changes, items));















  function updateEdgeText(edgeId: string, text: string) {







    setEdges((items) =>







      items.map((edge) =>







        edge.id === edgeId







          ? { ...edge, data: { ...(edge.data ?? {}), customLabel: text }, label: text || edge.label }







          : edge,







      ),







    );







  }















  function updateEdges(edgeIds: string[], patch: EdgePatch) {







    const chosen = new Set(edgeIds);







    setEdges((items) =>







      items.map((edge) =>







        chosen.has(edge.id)







          ? { ...edge, data: { ...(edge.data ?? {}), ...patch }, label: patch.customLabel !== undefined ? patch.customLabel || edge.label : edge.label }







          : edge,







      ),







    );







  }















  function deleteEdges(edgeIds: string[]) {







    const chosen = new Set(edgeIds);







    setEdges((items) => items.filter((edge) => !chosen.has(edge.id)));







  }















  function deleteGraphLayer(layerId: string) {







    setNodes((items) => items.filter((node) => node.data?.graphLayerId !== layerId));







    setEdges((items) =>







      items.filter(







        (edge) =>







          edge.data?.graphLayerId !== layerId &&







          !String(edge.source).startsWith(layerId + "::") &&







          !String(edge.target).startsWith(layerId + "::"),







      ),







    );







    setGraphLayers((items) => items.filter((layer) => layer.id !== layerId));







  }















  function moveGraphLayer(layerId: string, deltaX: number, deltaY: number, excludeNodeId?: string) {







    if (!deltaX && !deltaY) return;







    setNodes((items) =>







      items.map((node) =>







        node.id !== excludeNodeId && node.data?.graphLayerId === layerId







          ? { ...node, position: { x: node.position.x + deltaX, y: node.position.y + deltaY } }







          : node,







      ),







    );







  }















  const onConnect: OnConnect = (connection) =>







    setEdges((items) =>







      addEdge(







        {







          ...connection,







          animated: true,







          label: "无关联",







          type: "smoothstep",







          sourceHandle: connection.sourceHandle,







          targetHandle: connection.targetHandle,







          markerEnd: { type: MarkerType.ArrowClosed, color: "#7c3aed" },







          style: { stroke: "#7c3aed", strokeWidth: 2.5 },







          labelBgPadding: [8, 4] as [number, number],







          labelBgBorderRadius: 4,







          labelBgStyle: { fill: "#ffffff", fillOpacity: 0.92 },







        },







        items,







      ),







    );















  function nextGraphLayerId() {







    graphLayerSeq.current += 1;







    return "graph-" + graphLayerSeq.current;







  }















  function applyFlowGraph(flowGraph: ProcessResponse["flow_graph"], opts: {







    append?: boolean; label?: string; rows?: number; detailContext?: GraphDetailContext;







  } = {}) {







    const rows = Number(opts.rows ?? flowGraph.meta?.rows ?? 0);







    const totalNodeCount = Number(flowGraph.meta?.total_nodes ?? flowGraph.nodes?.length ?? 0);







    const totalEdgeCount = Number(flowGraph.meta?.total_edges ?? flowGraph.edges?.length ?? 0);







    if (rows <= 0 || (totalNodeCount <= 0 && totalEdgeCount <= 0)) return false;







    const layerId = nextGraphLayerId();







    const layerLabel = opts.label || ("生成图" + graphLayerSeq.current);







    const graph = buildFlowElements(flowGraph, {







      layerId, layerLabel,







      detailContext: opts.detailContext,







      offset: opts.append ? nextGraphOffset(nodes) : { x: 0, y: 0 },







    });







    if (opts.append) {







      setNodes((items) => [...items, ...graph.nodes]);







      setEdges((items) => [...items, ...graph.edges]);







      setGraphLayers((items) => [...items, { id: layerId, label: layerLabel, rows }]);







      setFlowMeta((meta) => ({







        ...meta,







        total_nodes: Number(meta.total_nodes ?? nodes.length) + Number(flowGraph.meta?.total_nodes ?? graph.nodes.length),







        total_edges: Number(meta.total_edges ?? edges.length) + Number(flowGraph.meta?.total_edges ?? graph.edges.length),







      }));







      return;







    }







    setNodes(graph.nodes);







    setEdges(graph.edges);







    setGraphLayers([{ id: layerId, label: layerLabel, rows }]);







    setFlowMeta(flowGraph.meta ?? {});







    return true;







  }















  function useCurrentCleanedGraph(result: ProcessResponse | null) {







    if (!result?.flow_graph) {







      message.info("你还没有清洗数据，请先清洗流水数据。");




      return "clean" as const;







    }







    applyFlowGraph(result.flow_graph, {







      label: "清洗后 " + result.job_id,




      rows: result.rows,







      detailContext: { kind: "cleaned", jobId: result.job_id },







    });







    message.success("已加载本次清洗后的数据。");




    return null;







  }















  async function uploadFlowGraph(files: UploadFile[]) {







    if (!files.length) { message.warning("请选择要导入图的文件"); return; }




    setFlowLoading(true);







    try {







      const data = await buildUploadForm([{ field: "files", files, archiveName: "flow_graph_files.zip" }], networkMode, updateTransferStatus);







      const payload = await requestJsonWithProgress("/api/flow/upload", data, networkMode, "上传图数据", updateTransferStatus);




      if (payload.status >= 400) throw new Error(payload.detail || "图数据上传失败");




      applyFlowGraph(payload.flow_graph, { label: payload.name || "上传图", rows: payload.rows, detailContext: { kind: "none" } });




      message.success("已成功加载资金流向图 " + (payload.rows ?? 0) + " 条数据");




    } catch (error) {







      failTransfer(networkMode, error instanceof Error ? error.message : "图数据上传失败", updateTransferStatus);




      message.error(error instanceof Error ? error.message : "图数据上传失败");




    } finally { setFlowLoading(false); }







  }















  async function importFlowData(files: UploadFile[]): Promise<boolean> {







    if (!files.length) { message.warning("请选择要导入数据的文件"); return false; }




    setFlowLoading(true);







    setFlowImportProgress({ visible: true, percent: 0, status: "active", text: "准备上传数据文件..." });




    try {







      const data = await buildUploadForm([{ field: "files", files, archiveName: "flow_import_files.zip" }], networkMode, updateTransferStatus);







      const payload = await uploadFlowImport(data, setFlowImportProgress, networkMode, updateTransferStatus);







      setImportedDataset(payload as ImportedDataset);







      const savedMapping = payload.mapping_rule?.mapping;







      const nextMapping = savedMapping ? sanitizeFlowMapping(savedMapping, payload.columns ?? []) : autoFlowMapping(payload.columns ?? []);







      setFieldMapping(nextMapping);







      setFlowBuildStatus({ visible: false, status: "normal", text: "" });







      setFlowImportProgress({ visible: true, percent: 100, status: "success", text: "已导入" + (payload.rows ?? 0) + " 条数据" });




      message.success("已导入" + (payload.rows ?? 0) + " 条数据，请在左侧\"字段筛选\"选择字段后生成图");




      return !savedMapping && !flowTemplateMatches(payload.columns ?? []);







    } catch (error) {







      failTransfer(networkMode, error instanceof Error ? error.message : "数据导入失败", updateTransferStatus);




      setFlowImportProgress({ visible: true, percent: 100, status: "exception", text: error instanceof Error ? error.message : "数据导入失败" });




      message.error(error instanceof Error ? error.message : "数据导入失败");




      return false;







    } finally { setFlowLoading(false); }







  }















  function acceptDatabaseImportedDataset(dataset: ImportedDataset) {
    setImportedDataset(dataset);
    setFieldMapping(autoFlowMapping(dataset.columns ?? []));
    setFlowBuildStatus({ visible: false, status: "normal", text: "" });
    setFlowImportProgress({
      visible: true,
      percent: 100,
      status: "success",
      text: "已导入" + (dataset.rows ?? 0) + " 条数据库数据",
    });
  }



  async function confirmDirectionRules(): Promise<"close-mapping" | null> {







    if (!directionRulePending) return null;







    const aliases = directionRulePending.values.reduce<Record<string, "进" | "出">>((result, value) => {







      result[value] = directionRuleValues[value] ?? "进";







      return result;







    }, {});







    setFlowLoading(true);







    try {







      const { response, payload } = await saveDirectionRules(aliases);







      if (!response.ok) throw new Error(payload.detail || "保存映射规则失败");




      const retryPayload = directionRulePending.payload;







      const retryMapping = directionRulePending.mapping;







      const source = directionRulePending.source;







      setDirectionRulePending(null);







      setDirectionRuleValues({});







      if (source === "mapping" && retryMapping) {







        const saved = await saveFlowMappingRule(retryMapping, { skipDirectionCheck: true });







        if (saved) return "close-mapping";







        return null;







      }







      message.success("方向规则已保存，正在重新生成图");




      await buildFilteredGraph(retryPayload);







    } catch (error) {







      message.error(error instanceof Error ? error.message : "保存映射规则失败");




    } finally { setFlowLoading(false); }







    return null;







  }















  async function saveFlowMappingRule(mapping: FlowFieldMapping, options: { skipDirectionCheck?: boolean } = {}): Promise<boolean> {







    if (!importedDataset) return false;







    const missing = requiredFlowMappingMissing(mapping);







    if (missing.length) { message.warning("请选择" + missing.join("、")); return false; }




    if (!options.skipDirectionCheck && mapping.direction_column) {







      const unknownValues = await loadUnknownDirectionValues(importedDataset.session_id, mapping.direction_column);







      if (unknownValues.length) {







        setDirectionRulePending({ source: "mapping", values: unknownValues, payload: {}, mapping });







        setDirectionRuleValues(Object.fromEntries(unknownValues.map((value) => [value, "进"])));







        message.warning("发现新的收付标志，请确认它们是进账还是出账。");




        return false;







      }







    }







    const { response, payload } = await saveMappingRule(importedDataset.columns, mapping);







    if (!response.ok) throw new Error(payload.detail || "保存字段映射规则失败");




    message.success("字段映射规则已保存，下次相同表头自动导入。");




    return true;







  }















  async function buildFilteredGraph(values: Record<string, unknown> & {







    source_column?: string; target_column?: string; amount_column?: string;







    time_column?: string; direction_column?: string; append?: boolean;







  }) {







    if (!importedDataset) { message.warning("请先导入数据文件"); return; }




    const missing = [!values.source_column ? "交易方字段" : "", !values.target_column ? "交易对方字段" : "", !values.amount_column ? "金额字段" : ""].filter(Boolean);




    if (missing.length) { const text = "请选择" + missing.join("、"); setFlowBuildStatus({ visible: true, status: "exception", text }); message.warning(text); return; }




    setFlowLoading(true);







    setFlowBuildStatus({ visible: true, status: "active", text: "正在按当前筛选条件生成资金流向图..." });




    try {







      const { response, payload } = await requestBuildFlowGraph(importedDataset.session_id, values);







      if (!response.ok && isUnknownDirectionPayload(payload)) {







        setDirectionRulePending({ source: "build", values: payload.detail.values, payload: values });







        setDirectionRuleValues(Object.fromEntries(payload.detail.values.map((value: string) => [value, "进"])));







        setFlowBuildStatus({ visible: true, status: "exception", text: payload.detail.message });







        message.warning(payload.detail.message);







        return;







      }







      if (!response.ok) throw new Error(payload.detail || "生成流向图失败");




      const rows = Number(payload.rows ?? 0);







      const append = Boolean(values.append);







      const flowGraph = normalizeFlowGraphPayload(payload);
      const meta = flowGraph.meta ?? {};







      if (rows === 0) {







        const text = "当前筛选条件下无数据，请放宽筛选条件或追加更多数据。";




        setFlowBuildStatus({ visible: true, status: "exception", text }); message.warning(text); return;







      }







      const layerLabel = (importedDataset.files?.[0] ?? "未命名图谱") + (append ? " #" + (graphLayers.length + 1) : "");




      const applied = applyFlowGraph(flowGraph, {







        append, label: layerLabel, rows,







        detailContext: {







          kind: "imported", sessionId: importedDataset.session_id,







          sourceColumn: values.source_column,







          sourceAccountColumn: values.source_account_column as string | undefined,







          sourceNameColumn: values.source_name_column as string | undefined,







          sourceIdColumn: values.source_id_column as string | undefined,







          sourceLabelColumn: values.source_label_column as string | undefined,







          targetColumn: values.target_column,







          targetCardColumn: values.target_card_column as string | undefined,







          targetNameColumn: values.target_name_column as string | undefined,







          targetIdColumn: values.target_id_column as string | undefined,







          targetLabelColumn: values.target_label_column as string | undefined,







          amountColumn: values.amount_column, timeColumn: values.time_column,







          directionColumn: values.direction_column,







          sourceFilters: values.source_filters as SourceFilterPayload[] | undefined,







          sourceValues: values.source_values as string[] | undefined,







          targetFilters: values.target_filters as TargetFilterPayload[] | undefined,







          targetValues: values.target_values as string[] | undefined,







          directionValues: values.directions as string[] | undefined,







          startDate: values.start_date as string | undefined,







          endDate: values.end_date as string | undefined,







        },







      });







      if (!applied) {







        const text = "当前筛选条件下无数据，请放宽筛选条件或追加更多数据。";




        setFlowBuildStatus({ visible: true, status: "exception", text }); message.warning(text); return;







      }







      setFlowBuildStatus({ visible: true, status: "success", text: "已生成，" + rows + " 条数据，" + (meta.total_nodes ?? 0) + " 个节点，" + (meta.total_edges ?? 0) + " 条关系" });




      message.success((append ? "已追加" : "已导入") + "资金流向图 " + rows + " 条数据");




    } catch (error) {







      setFlowBuildStatus({ visible: true, status: "exception", text: error instanceof Error ? error.message : "生成流向图失败" });




      message.error(error instanceof Error ? error.message : "生成流向图失败");




    } finally { setFlowLoading(false); }







  }















  async function runSmartAnalysis(values: Record<string, unknown> & {







    prompt: string; source_column?: string; target_column?: string;







    amount_column?: string; time_column?: string; direction_column?: string;







  }) {







    if (!importedDataset) { message.warning("请先导入数据文件"); return; }




    setFlowLoading(true);







    try {







      const { response, payload } = await runFlowAnalysis(importedDataset.session_id, values);







      if (!response.ok) throw new Error(payload.detail || "智能分析失败");




      if (payload.flow_graph) {
        applyFlowGraph(normalizeFlowGraphPayload(payload), {







        label: "智能分析 " + (importedDataset.files?.[0] ?? importedDataset.session_id),




        rows: payload.rows,







        detailContext: { kind: "imported", sessionId: importedDataset.session_id, sourceColumn: values.source_column, targetColumn: values.target_column, amountColumn: values.amount_column, timeColumn: values.time_column, directionColumn: values.direction_column },







        });
      }







      setAnalysisReport(payload.report ?? "");







      message.success("智能分析完成，共" + (payload.rows ?? 0) + " 条数据");




    } catch (error) {







      message.error(error instanceof Error ? error.message : "智能分析失败");




    } finally { setFlowLoading(false); }







  }















  async function loadHistoryGraph(jobId: string) {







    setFlowLoading(true);







    try {







      const { response, payload } = await requestHistoryGraph(jobId);







      if (!response.ok) throw new Error(payload.detail || "历史图谱加载失败");




      if (payload.flow_graph) {
        applyFlowGraph(normalizeFlowGraphPayload(payload), { label: payload.name, rows: payload.rows, detailContext: { kind: "cleaned", jobId } });
      } else if (payload.session_id) {
        const dataset = payload as ImportedDataset;
        const savedMapping = dataset.mapping_rule?.mapping;
        setImportedDataset(dataset);
        setFieldMapping(savedMapping ? sanitizeFlowMapping(savedMapping, dataset.columns ?? []) : autoFlowMapping(dataset.columns ?? []));
        setFlowBuildStatus({ visible: false, status: "normal", text: "" });
        message.info("已加载历史数据，请确认字段后生成图谱");
      } else {
        throw new Error("历史图谱数据结构不完整");
      }







      message.success("已加载历史数据 " + (payload.name ?? jobId));




    } catch (error) {







      message.error(error instanceof Error ? error.message : "历史图谱加载失败");




    } finally { setFlowLoading(false); }







  }















  function addManualNode() { setAddNodeOpen(true); }















  function createManualNode(values: ManualNodeFormValues) {







    const label = String(values.label || "").trim();







    if (!label) { message.warning("请输入主体名称。"); return; }




    const outgoingLinks = values.outgoingEnabled ? normalizeManualLinks(values.outgoingLinks) : [];







    const incomingLinks = values.incomingEnabled ? normalizeManualLinks(values.incomingLinks) : [];







    const displayLabel = uniqueDisplayLabel(label, nodes);







    const id = "manual-" + Date.now();







    const kind = values.kind ?? "unknown";







    const tags: string[] = [];







    setNodes((items) => [







      ...items,







      { id, position: { x: 160 + items.length * 24, y: 160 + items.length * 18 }, type: "flowEntity", data: { entityLabel: displayLabel, rawEntityLabel: label, entityKind: kind, label: renderFlowNodeLabel(displayLabel, tags, kind), tags }, className: "flow-node manual" },







    ]);







    setEdges((items) => [







      ...items,







      ...outgoingLinks.map((link) => createManualEdge(id, link.nodeId, link, values)),







      ...incomingLinks.map((link) => createManualEdge(link.nodeId, id, link, values)),







    ]);







    message.success("已成功创建手动关系。");




  }















  function saveTag() {







    if (!selectedNode || !tagInput.trim()) return;







    const nextTag = tagInput.trim();







    setNodes((items) =>







      items.map((node) => {







        if (node.id !== selectedNode.id) return node;







        const tags = Array.from(new Set([...(node.data.tags as string[] ?? []), nextTag]));







        const label = String(node.data.entityLabel ?? node.id);







        const kind = (node.data.entityKind as EntityKind | undefined) ?? "unknown";







        return { ...node, data: { ...node.data, tags, label: renderFlowNodeLabel(label, tags, kind) } };







      }),







    );







    setSelectedNode((node) => {







      if (!node) return node;







      const tags = Array.from(new Set([...(node.data.tags as string[] ?? []), nextTag]));







      const label = String(node.data.entityLabel ?? node.id);







      const kind = (node.data.entityKind as EntityKind | undefined) ?? "unknown";







      return { ...node, data: { ...node.data, tags, label: renderFlowNodeLabel(label, tags, kind) } };







    });







    setTagInput("");







  }















  function saveFeature() {







    if (!selectedNode || !featureInput.trim()) return;







    const nextFeature = featureInput.trim();







    updateSelectedNodeFeatures((features) => Array.from(new Set([...features, nextFeature])));







    setFeatureInput("");







  }















  function deleteFeature(feature: string) {







    updateSelectedNodeFeatures((features) => features.filter((item) => item !== feature));







  }















  function updateSelectedNodeFeatures(updater: (features: string[]) => string[]) {







    if (!selectedNode) return;







    const apply = (node: Node) => ({ ...node, data: { ...node.data, manualFeatures: updater((node.data.manualFeatures as string[] | undefined) ?? []) } });







    setNodes((items) => items.map((node) => (node.id === selectedNode.id ? apply(node) : node)));







    setSelectedNode((node) => (node ? apply(node) : node));







  }















  function deleteSelectedNode() {







    if (!selectedNode) return;







    const id = selectedNode.id;







    setNodes((items) => items.filter((node) => node.id !== id));







    setEdges((items) => items.filter((edge) => edge.source !== id && edge.target !== id));







    setSelectedNode(null);







    message.success("已删除主体及其关联关系。");




  }















  function changeSelectedNodeKind(kind: EntityKind) {







    if (!selectedNode) return;







    setNodes((items) =>







      items.map((node) => {







        if (node.id !== selectedNode.id) return node;







        const label = String(node.data.entityLabel ?? node.id);







        return { ...node, data: { ...node.data, entityKind: kind, label: renderFlowNodeLabel(label, (node.data.tags as string[] ?? []), kind) } };







      }),







    );







    setSelectedNode((node) =>







      node ? { ...node, data: { ...node.data, entityKind: kind, label: renderFlowNodeLabel(String(node.data.entityLabel ?? node.id), (node.data.tags as string[] ?? []), kind) } } : node,







    );







  }















  function createSelectedNodeConnections(values: NodeConnectionFormValues) {







    if (!selectedNode) return;







    const outgoingLinks = values.outgoingEnabled ? normalizeManualLinks(values.outgoingLinks) : [];







    const incomingLinks = values.incomingEnabled ? normalizeManualLinks(values.incomingLinks) : [];







    if (!outgoingLinks.length && !incomingLinks.length) { message.warning("请至少创建一个连接对象"); return; }




    const layerId = selectedNode.data?.graphLayerId as string | undefined;







    const layerLabel = selectedNode.data?.graphLayerLabel as string | undefined;







    const edgeValues = { lineStyle: values.lineStyle ?? "solid", lineWidth: values.lineWidth ?? 1.2 };







    const nextEdges = [







      ...outgoingLinks.map((link) => attachManualEdgeLayer(createManualEdge(selectedNode.id, link.nodeId, link, edgeValues), layerId, layerLabel)),







      ...incomingLinks.map((link) => attachManualEdgeLayer(createManualEdge(link.nodeId, selectedNode.id, link, edgeValues), layerId, layerLabel)),







    ];







    setEdges((items) => [...items, ...nextEdges]);







  }















  function resetFlowGraph() {







  }







  async function handleImportData(files: UploadFile[]): Promise<boolean> {



    const needsMapping = await importFlowData(files);



    if (needsMapping) {



      setMappingModalOpen(true);



    }



    return true;



  }







  async function handleConfirmDirectionRules(): Promise<void> {



    const result = await confirmDirectionRules();



    if (result === "close-mapping") {



      setMappingModalOpen(false);



    }



  }







  async function handleSaveFlowMappingRule(mapping: FlowFieldMapping): Promise<boolean> {



    const saved = await saveFlowMappingRule(mapping);



    if (saved) setMappingModalOpen(false);



    return saved;



  }



















  return {







    nodes, edges, flowMeta, graphLayers, importedDataset, flowImportProgress,







    flowBuildStatus, analysisReport, fieldMapping, flowLoading,







    directionRulePending, directionRuleValues, selectedNode, addNodeOpen,







    tagInput, featureInput, graphLayerSeq, nodeConnectionForm,







    nodeOutgoingEnabled, nodeIncomingEnabled,







    setNodes, setEdges, setSelectedNode, setAddNodeOpen,







    setTagInput, setFeatureInput, setDirectionRulePending, setDirectionRuleValues,







    setFieldMapping, setFlowBuildStatus,







    onNodesChange, onEdgesChange, updateEdgeText, updateEdges, deleteEdges,







    deleteGraphLayer, moveGraphLayer, onConnect, nextGraphLayerId, applyFlowGraph,







    useCurrentCleanedGraph, uploadFlowGraph, importFlowData,







    confirmDirectionRules, saveFlowMappingRule, buildFilteredGraph,







    runSmartAnalysis, loadHistoryGraph, addManualNode, createManualNode,







    saveTag, saveFeature, deleteFeature, updateSelectedNodeFeatures,







    deleteSelectedNode, changeSelectedNodeKind, createSelectedNodeConnections,







    resetFlowGraph, acceptDatabaseImportedDataset,







    handleImportData, handleConfirmDirectionRules, handleSaveFlowMappingRule,







  };







}







