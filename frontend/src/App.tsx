import { FlowPanel } from "./features/flow/FlowPanel";
import {
  ApartmentOutlined,
  CloudDownloadOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  FileZipOutlined,
  FundProjectionScreenOutlined,
  MenuOutlined,
  PlusOutlined,
  RightOutlined,
  SettingOutlined,
  UploadOutlined,
  WalletOutlined,
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
import zhCN from "antd/locale/zh_CN";
import dayjs from "dayjs";
import "dayjs/locale/zh-cn";
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
import type { ProcessArtifact, ProcessProgress, ProcessResponse, RuleAnalysis } from "./types";
import { CleanPanel } from "./features/clean/CleanPanel";
import { DuneDownloadPanel } from "./features/download/DuneDownloadPanel";
import { CryptoAddressPanel } from "./features/crypto/CryptoAddressPanel";
import { CryptoDownloadPanel } from "./features/crypto/CryptoDownloadPanel";
import { CryptoParquetPanel } from "./features/crypto/CryptoParquetPanel";
import { AddressAnalyticsPanel } from "./features/crypto/AddressAnalyticsPanel";
import { RpcSettingsPage } from "./features/rpc/RpcSettingsPage";
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

dayjs.locale("zh-cn");

const menuItems = [
  { key: "clean", icon: <UploadOutlined />, label: "数据清洗" },
  { key: "graph", icon: <ApartmentOutlined />, label: "资金流向图" },
  {
    key: "download",
    icon: <DownloadOutlined />,
    label: "下载",
    children: [
      { key: "download-dune", icon: <DatabaseOutlined />, label: "dune" },
    ],
  },
  {
    key: "crypto",
    icon: <WalletOutlined />,
    label: "虚拟币",
    children: [
      { key: "crypto-download", icon: <CloudDownloadOutlined />, label: "数据下载" },
      { key: "crypto-parquet", icon: <FileZipOutlined />, label: "Parquet下载" },
      { key: "crypto-rpc", icon: <CloudServerOutlined />, label: "RPC节点管理" },
      { key: "crypto-analytics", icon: <FundProjectionScreenOutlined />, label: "链上地址分析" },
      { key: "crypto-address", icon: <WalletOutlined />, label: "地址区分" },
    ],
  },
];

export function App() {
  const [active, setActive] = useState("clean");
  const [sideCollapsed, setSideCollapsed] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<ProcessResponse | null>(null);
  const [processProgress, setProcessProgress] = useState<ProcessProgress | null>(null);

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
    unify_sources?: boolean;
    include_alipay_balance?: boolean;
  }) {
    setLoading(true);
    setResult(null);
    setProcessProgress(null);
    const requestedJobId = `etl-${Date.now()}-${crypto.randomUUID().slice(0, 8)}`;
    let progressTimer: number | undefined;
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
      form.append("unify_sources", String(values.unify_sources !== false));
      form.append("include_alipay_balance", String(values.include_alipay_balance === true));
      form.append("job_id", requestedJobId);
      const refreshProgress = async () => {
        try {
          const response = await fetch(`/api/process/progress/${encodeURIComponent(requestedJobId)}`, { cache: "no-store" });
          if (response.ok) {
            setProcessProgress((await response.json()) as ProcessProgress);
          }
        } catch {
          // Upload and processing continue even if one progress poll is missed.
        }
      };
      progressTimer = window.setInterval(refreshProgress, 750);
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
      await refreshProgress();
      flowOps.resetFlowGraph();
      message.success("数据清洗完成，可在资金流向图中继续生成或导入数据分析");
    } catch (error) {
      failTransfer(
        networkMode,
        error instanceof Error ? error.message : "处理失败",
        updateTransferStatus,
      );
      message.error(error instanceof Error ? error.message : "处理失败");
    } finally {
      if (progressTimer !== undefined) window.clearInterval(progressTimer);
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

  async function downloadArtifact(artifact: ProcessArtifact) {
    try {
      await downloadWithProgress(
        artifact.download_url,
        networkMode,
        artifact.name,
        updateTransferStatus,
      );
    } catch (error) {
      failTransfer(
        networkMode,
        error instanceof Error ? error.message : "下载阶段产物失败",
        updateTransferStatus,
      );
      message.error(error instanceof Error ? error.message : "下载阶段产物失败");
    }
  }

  const handleMenuClick: MenuProps["onClick"] = (item) => {
    const nextActive = String(item.key);
    setActive(nextActive);
    setSideCollapsed(nextActive === "graph");
    setMobileNavOpen(false);
  };

  return (
    <ConfigProvider
      locale={zhCN}
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
      <Button
        className="mobile-nav-trigger"
        type="primary"
        icon={<MenuOutlined />}
        aria-label="打开导航"
        onClick={() => setMobileNavOpen(true)}
      />
      <Drawer
        className="mobile-nav-drawer"
        placement="left"
        width={270}
        title="资金数据智能分析平台"
        open={mobileNavOpen}
        onClose={() => setMobileNavOpen(false)}
      >
        <Menu
          mode="inline"
          selectedKeys={[active]}
          defaultOpenKeys={["download", "crypto"]}
          items={menuItems}
          onClick={handleMenuClick}
        />
      </Drawer>
      <Layout className="app-shell">
        <Sider
          width={248}
          collapsedWidth={0}
          collapsible
          collapsed={sideCollapsed}
          onCollapse={setSideCollapsed}
          className="side"
        >
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
            defaultOpenKeys={["download", "crypto"]}
            items={menuItems}
            onClick={handleMenuClick}
          />
        </Sider>
        <Layout>
          <Content className={`content ${active === "graph" ? "content-graph" : ""}`}>
            {active === "clean" && (
              <section className="topbar">
                <div>
                  <div className="topbar-title-row">
                    <h1>{titleFor(active)}</h1>
                  </div>
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
            )}
            <TransferPanel status={transferStatus} />

            {active === "clean" && (
              <CleanPanel
                loading={loading}
                onFinish={submit}
                result={result}
                onOpenRules={() => modals.setRuleOpen(true)}
                onDownload={downloadResult}
                progress={processProgress}
                onDownloadArtifact={downloadArtifact}
              />
            )}
            {active === "graph" && (
              <FlowPanel
                nodes={flowOps.nodes}
                edges={flowOps.edges}
                meta={flowOps.flowMeta}
                graphLayers={flowOps.graphLayers}
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
                onUploadGraph={flowOps.uploadFlowGraph}
                onImportData={flowOps.handleImportData}
                onImportPaths={flowOps.importFlowByPaths}
                onDatabaseImported={flowOps.acceptDatabaseImportedDataset}
                onOpenMapping={() => modals.setMappingModalOpen(true)}
                onBuildFilteredGraph={flowOps.buildFilteredGraph}
                onSmartAnalyze={flowOps.runSmartAnalysis}
                onLoadHistory={flowOps.loadHistoryGraph}
              />
            )}
            {active === "download-dune" && <DuneDownloadPanel />}
            {active === "crypto-download" && <CryptoDownloadPanel />}
            {active === "crypto-parquet" && <CryptoParquetPanel />}
            {active === "crypto-rpc" && <RpcSettingsPage />}
            {active === "crypto-analytics" && <AddressAnalyticsPanel />}
            {active === "crypto-address" && <CryptoAddressPanel />}
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
      "download-dune": "Dune 下载",
      "crypto-download": "虚拟币数据下载",
      "crypto-parquet": "EVM Parquet 批量资金分析",
      "crypto-rpc": "EVM RPC 节点管理",
      "crypto-analytics": "链上地址分析",
      "crypto-address": "地址区分",
    }[key];
  }
}

