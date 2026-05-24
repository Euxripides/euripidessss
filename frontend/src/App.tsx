import { FlowPanel } from "./features/flow/FlowPanel";
import {
  ApartmentOutlined,
  DownloadOutlined,
  PlusOutlined,
  RightOutlined,
  SettingOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import {
  Button,
  Collapse,
  ConfigProvider,
  Dropdown,
  Drawer,
  Form,
  Layout,
  Menu,
  Space,
  Table,
  Upload,
  message,
  theme,
} from "antd";
import type { MenuProps, UploadFile } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Controls,
  MiniMap,
  ReactFlow,
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  type Edge,
  MarkerType,
  type Node,
  type ReactFlowInstance,
  type OnConnect,
  type OnEdgesChange,
  type OnNodesChange,
} from "@xyflow/react";
import type { ProcessResponse, RuleAnalysis } from "./types";
import { CleanPanel } from "./features/clean/CleanPanel";
import { RuleExpansionDrawer } from "./features/clean/RuleExpansionDrawer";
import { EdgeDetailModal } from "./features/flow/EdgeDetailModal";
import { EdgeStylePanel } from "./features/flow/EdgeStylePanel";
import { FlowAnalysisPanel } from "./features/flow/FlowAnalysisPanel";
import { FlowBuildControls } from "./features/flow/FlowBuildControls";
import { FlowAddNodeModal } from "./features/flow/FlowAddNodeModal";
import { FlowDirectionRuleModal } from "./features/flow/FlowDirectionRuleModal";
import { FlowFieldFilters } from "./features/flow/FlowFieldFilters";
import { FlowGraphFilters } from "./features/flow/FlowGraphFilters";
import { FlowImportSummary } from "./features/flow/FlowImportSummary";
import { FlowLabelFilters } from "./features/flow/FlowLabelFilters";
import { FlowLayerPanel } from "./features/flow/FlowLayerPanel";
import { FlowMappingModal } from "./features/flow/FlowMappingModal";
import { DirectionalFlowEdge, FlowEntityNode } from "./features/flow/FlowGraphPrimitives";
import { FlowSourceModal } from "./features/flow/FlowSourceModal";
import { FlowStyleToolbar } from "./features/flow/FlowStyleToolbar";
import { SubjectDetailDrawer } from "./features/flow/SubjectDetailDrawer";
import { aggregateRowsByDate } from "./features/flow/flowAggregation";
import { buildInsights, findShortestPath, normalizeDirectionFilterValues } from "./features/flow/flowAnalysis";
import { buildFlowElements, nextGraphOffset } from "./features/flow/flowElements";
import {
  detectEntityKind,
  miniMapNodeColor,
  miniMapNodeStrokeColor,
  renderFlowNodeLabel,
} from "./features/flow/flowNodes";
import {
  buildEdgeLabel,
  findReciprocalPairKeys,
  getEdgeAmount,
  getEdgeCount,
  getEdgeLineColor,
  getEdgeLinePattern,
  getEdgeLineWidth,
  getEdgeTime,
  getTimeCutoff,
  markerEndForDirectionalEdge,
  markerEndForEdge,
  markerStartForEdge,
  reciprocalEdgeOffset,
  unorderedEdgePairKey,
} from "./features/flow/flowEdges";
import {
  buildOptimizedHandleMap,
  chooseEdgeHandles,
  chooseOptimizedEdgeHandles,
  getNodeGeometry,
} from "./features/flow/flowGeometry";
import { attachManualEdgeLayer, createManualEdge } from "./features/flow/flowManual";
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
} from "./features/flow/flowExport";
import {
  buildSubjectDetailStats,
  nodeSelectOptions,
  normalizeManualLinks,
  uniqueDisplayLabel,
} from "./features/flow/flowSubject";
import {
  autoFlowMapping,
  flowTemplateMatches,
  pickColumn,
  requiredFlowMappingMissing,
  resolveEffectiveFlowMapping,
  resolveSourceFilterRawColumn,
  resolveTargetFilterRawColumn,
  sanitizeFlowMapping,
} from "./features/flow/flowMapping";
import {
  buildFlowGraph as requestBuildFlowGraph,
  isUnknownDirectionPayload,
  loadEdgeDetail as requestEdgeDetail,
  loadFlowValues,
  loadHistoryGraph as requestHistoryGraph,
  loadHistoryItems,
  loadUnknownDirectionValues,
  runFlowAnalysis,
  saveDirectionRules,
  saveMappingRule,
} from "./features/flow/flowApi";
import {
  buildUploadForm,
  detectNetworkMode,
  downloadWithProgress,
  failTransfer,
  requestJsonWithProgress,
  uploadFlowImport,
} from "./features/upload/uploadApi";
import { TransferPanel } from "./features/upload/TransferPanel";
import {
  ENTITY_KIND_OPTIONS,
  SOURCE_FILTER_FIELDS,
  TARGET_FILTER_FIELDS,
  type ArrowMode,
  type CanvasImageExportFormat,
  type DirectionRulePending,
  type EdgeDetailPayload,
  type EdgeLabelMode,
  type EdgeLinePattern,
  type EdgePatch,
  type EntityKind,
  type FlowFieldMapping,
  type FlowBuildStatus,
  type FlowEdgeRow,
  type FlowImportProgress,
  type GraphDetailContext,
  type GraphExportFormat,
  type GraphExportPayload,
  type GraphLayer,
  type HistoryItem,
  type ImportedDataset,
  type LineType,
  type ManualNodeFormValues,
  type ManualNodeLink,
  type NetworkMode,
  type NodeConnectionFormValues,
  type SourceFilterField,
  type SourceFilterPayload,
  type SourceFilterState,
  type SubjectDetailStats,
  type SubjectStat,
  type TargetFilterField,
  type TargetFilterPayload,
  type TargetFilterState,
  type TimeWindow,
  type TransferStatus,
} from "./features/flow/flowTypes";
import { useFlowOperations } from "./hooks/useFlowOperations";
import { useFlowModals } from "./hooks/useFlowModals";

const { Sider, Content } = Layout;

const menuItems = [
  { key: "clean", icon: <UploadOutlined />, label: "数据清洗" },
  { key: "graph", icon: <ApartmentOutlined />, label: "资金流向图" },
];

export function App() {
  const [active, setActive] = useState("clean");
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<ProcessResponse | null>(null);

  const networkMode = useMemo(() => detectNetworkMode(), []);
  const [transferStatus, setTransferStatus] = useState<TransferStatus>({
    visible: false,
    phase: "idle",
    mode: networkMode,
    label: "",
    percent: 0,
    speed: 0,
    loaded: 0,
    total: 0,
  });

  function updateTransferStatus(status: TransferStatus) {
    setTransferStatus(status);
    if (status.phase === "done") {
      window.setTimeout(() => {
        setTransferStatus((current) =>
          current.phase === "done" ? { ...current, visible: false } : current,
        );
      }, 2500);
    }
  }

  const modals = useFlowModals();
  const flowOps = useFlowOperations({ networkMode, updateTransferStatus, setMappingModalOpen: modals.setMappingModalOpen });

  useEffect(() => {
    flowOps.nodeConnectionForm.resetFields();
    flowOps.nodeConnectionForm.setFieldsValue({
      lineStyle: "solid",
      lineWidth: 1.2,
      outgoingEnabled: false,
      incomingEnabled: false,
      outgoingLinks: [{}],
      incomingLinks: [{}],
    });
  }, [flowOps.nodeConnectionForm, flowOps.selectedNode?.id]);

  async function submit(values: {
    transaction_files?: UploadFile[];
    account_files?: UploadFile[];
    label_file?: UploadFile[];
  }) {
    setLoading(true);
    try {
      const form = await buildUploadForm(
        [
          { field: "transaction_files", files: values.transaction_files ?? [], archiveName: "transaction_files.zip" },
          { field: "account_files", files: values.account_files ?? [], archiveName: "account_files.zip" },
          { field: "label_file", files: values.label_file ?? [], archiveName: "label_file.zip", single: true },
        ],
        networkMode,
        updateTransferStatus,
      );
      const payload = (await requestJsonWithProgress(
        "/api/process",
        form,
        networkMode,
        "上传并清洗数据",
        updateTransferStatus,
      )) as ProcessResponse | { detail?: string | { message?: string; analysis?: RuleAnalysis }; status?: number };
      if ("status" in payload && payload.status && payload.status >= 400) {
        const detail = payload.detail;
        if (payload.status === 409 && typeof detail === "object" && detail?.analysis) {
          modals.setPendingRuleAnalysis(detail.analysis);
          modals.setRuleOpen(true);
          message.warning(detail.message || "发现未覆盖的表头，请先确认候选规则");
          return;
        }
        throw new Error(typeof detail === "string" ? detail : "处理失败");
      }
      setResult(payload as ProcessResponse);
      flowOps.resetFlowGraph();
      message.success("数据清洗完成，可在资金流向图中选择'清洗的文件'进行分析");
    } catch (error) {
      failTransfer(
        networkMode,
        error instanceof Error ? error.message : "处理失败",
        updateTransferStatus,
      );
      message.error(error instanceof Error ? error.message : "处理失败");
    } finally {
      setLoading(false);
    }
  }

  async function downloadResult(resultItem: ProcessResponse) {
    try {
      await downloadWithProgress(
        resultItem.download_url,
        networkMode,
        `清洗结果_${resultItem.job_id}`,
        updateTransferStatus,
      );
    } catch (error) {
      failTransfer(
        networkMode,
        error instanceof Error ? error.message : "下载失败",
        updateTransferStatus,
      );
      message.error(error instanceof Error ? error.message : "下载失败");
    }
  }

  return (
    <ConfigProvider
      theme={{
        algorithm: theme.defaultAlgorithm,
        token: {
          colorPrimary: "#3b5bdb",
          colorInfo: "#3b5bdb",
          borderRadius: 8,
          fontFamily: '"Microsoft YaHei", "PingFang SC", system-ui, sans-serif',
        },
      }}
    >
      <Layout className="app-shell">
        <Sider width={248} className="side">
          <div className="brand">
            <div className="brand-mark">资</div>
            <div>
              <strong>资金数据智能分析平台</strong>
              <span>ETL &middot; Flow Intelligence</span>
            </div>
          </div>
          <Menu
            mode="inline"
            selectedKeys={[active]}
            items={menuItems}
            onClick={(item) => setActive(item.key)}
          />
        </Sider>
        <Layout>
          <Content className="content">
            <section className="topbar">
              <div>
                <h1>{titleFor(active)}</h1>
                <p>清洗、合并、标注和分析支付宝、微信、银行卡流水。</p>
              </div>
              <Space>
                {result && (
                  <Button
                    icon={<DownloadOutlined />}
                    onClick={() => downloadResult(result)}
                    type="primary"
                  >
                    下载结果
                  </Button>
                )}
              </Space>
            </section>
            <TransferPanel status={transferStatus} />

            {active === "clean" && (
              <CleanPanel
                loading={loading}
                onFinish={submit}
                result={result}
                onOpenRules={() => modals.setRuleOpen(true)}
                onDownload={downloadResult}
              />
            )}
            {active === "graph" && (
              <FlowPanel
                nodes={flowOps.nodes}
                edges={flowOps.edges}
                meta={flowOps.flowMeta}
                graphLayers={flowOps.graphLayers}
                currentResult={result}
                importedDataset={flowOps.importedDataset}
                fieldMapping={flowOps.fieldMapping}
                buildStatus={flowOps.flowBuildStatus}
                analysisReport={flowOps.analysisReport}
                loading={flowOps.flowLoading}
                onNodesChange={flowOps.onNodesChange}
                onEdgesChange={flowOps.onEdgesChange}
                onUpdateEdgeText={flowOps.updateEdgeText}
                onUpdateEdges={flowOps.updateEdges}
                onDeleteEdges={flowOps.deleteEdges}
                onDeleteLayer={flowOps.deleteGraphLayer}
                onMoveLayer={flowOps.moveGraphLayer}
                onConnect={flowOps.onConnect}
                onNodeClick={(_, node) => flowOps.setSelectedNode(node)}
                onAddNode={flowOps.addManualNode}
                onOpenImport={() => setActive("clean")}
                onUseCurrent={() => {
                  const result_ = flowOps.useCurrentCleanedGraph(result);
                  if (result_ === "clean") setActive("clean");
                }}
                onUploadGraph={flowOps.uploadFlowGraph}
                onImportData={flowOps.handleImportData}
                onOpenMapping={() => modals.setMappingModalOpen(true)}
                onBuildFilteredGraph={flowOps.buildFilteredGraph}
                onSmartAnalyze={flowOps.runSmartAnalysis}
                onLoadHistory={flowOps.loadHistoryGraph}
              />
            )}
          </Content>
        </Layout>
      </Layout>
      <Drawer
        title="主体详情"
        open={!!flowOps.selectedNode}
        onClose={() => flowOps.setSelectedNode(null)}
        width={420}
      >
        {flowOps.selectedNode && (
          <SubjectDetailDrawer
            node={flowOps.selectedNode}
            stats={
              (flowOps.selectedNode.data.visibleSubjectStats as
                | SubjectDetailStats
                | undefined) ??
              buildSubjectDetailStats(flowOps.selectedNode, flowOps.edges)
            }
            connectionOptions={nodeSelectOptions(flowOps.nodes, {
              excludeId: flowOps.selectedNode.id,
            })}
            tagInput={flowOps.tagInput}
            setTagInput={flowOps.setTagInput}
            onSaveTag={flowOps.saveTag}
            featureInput={flowOps.featureInput}
            setFeatureInput={flowOps.setFeatureInput}
            onSaveFeature={flowOps.saveFeature}
            onDeleteFeature={flowOps.deleteFeature}
            onDelete={flowOps.deleteSelectedNode}
            onKindChange={flowOps.changeSelectedNodeKind}
            connectionForm={flowOps.nodeConnectionForm}
            outgoingEnabled={flowOps.nodeOutgoingEnabled}
            incomingEnabled={flowOps.nodeIncomingEnabled}
            onCreateConnections={flowOps.createSelectedNodeConnections}
          />
        )}
      </Drawer>
      <FlowAddNodeModal
        open={flowOps.addNodeOpen}
        nodes={flowOps.nodes}
        onClose={() => flowOps.setAddNodeOpen(false)}
        onFinish={flowOps.createManualNode}
      />
      <FlowMappingModal
        open={modals.mappingModalOpen}
        columns={flowOps.importedDataset?.columns ?? []}
        mapping={flowOps.fieldMapping}
        onChange={flowOps.setFieldMapping}
        onSave={flowOps.handleSaveFlowMappingRule}
        onClose={() => modals.setMappingModalOpen(false)}
      />
      <RuleExpansionDrawer
        open={modals.ruleOpen}
        initialAnalysis={modals.pendingRuleAnalysis}
        onClose={() => {
          modals.setRuleOpen(false);
          modals.setPendingRuleAnalysis(null);
        }}
      />
      <FlowDirectionRuleModal
        pending={flowOps.directionRulePending}
        values={flowOps.directionRuleValues}
        loading={flowOps.flowLoading}
        onChange={flowOps.setDirectionRuleValues}
        onConfirm={flowOps.handleConfirmDirectionRules}
        onCancel={() => {
          flowOps.setDirectionRulePending(null);
          flowOps.setDirectionRuleValues({});
        }}
      />
    </ConfigProvider>
  );

  function titleFor(key: string) {
    return {
      clean: "数据清洗",
      graph: "资金流向图",
    }[key];
  }
}

