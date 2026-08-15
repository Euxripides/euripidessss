import { FlowPanel } from "./features/flow/FlowPanel";

// 路由级代码分割（#6 优化）：重页面懒加载，减小首屏 bundle
const AnalyticsDashboardPage = lazy(() => import("./features/analytics/DashboardPage"));
const AnalyticsAddressPage = lazy(() => import("./features/analytics/AddressPage"));
const AnalyticsGraphPage = lazy(() => import("./features/analytics/GraphPage"));
const AnalyticsReportPage = lazy(() => import("./features/analytics/ReportPage"));
const AnalyticsRiskPage = lazy(() => import("./features/analytics/RiskAnalysisPage"));
const DataQualityPage = lazy(() => import("./features/quality/DataQualityPage"));
const FinancialQualityPage = lazy(() => import("./features/financial-quality/FinancialQualityPage"));
const PriceEnginePage = lazy(() => import("./features/price-engine/PriceEnginePage"));
const SystemSettingsPage = lazy(() => import("./features/system/SystemSettingsPage"));
const IntelligencePage = lazy(() => import("./features/intelligence/IntelligencePage"));
const ReportPage = lazy(() => import("./features/reports/ReportPage"));
const ExplorerPage = lazy(() => import("./features/explorer-intelligence/ExplorerPage"));
import {
  ApartmentOutlined,
  CloudDownloadOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  DollarOutlined,
  FileTextOutlined,
  FileZipOutlined,
  FundProjectionScreenOutlined,
  MenuOutlined,
  PlusOutlined,
  RightOutlined,
  RobotOutlined,
  SearchOutlined,
  SettingOutlined,
  ThunderboltOutlined,
  UploadOutlined,
  WalletOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import {
  App as AntdApp,
  Button,
  Collapse,
  ConfigProvider,
  Dropdown,
  Drawer,
  Form,
  Input,
  Layout,
  Menu,
  Space,
  Tag,
  Table,
  Upload,
  message,
  theme,
} from "antd";
import zhCN from "antd/locale/zh_CN";
import dayjs from "dayjs";
import "dayjs/locale/zh-cn";
import type { MenuProps, UploadFile } from "antd";
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
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
import { DataSourcePage } from "./features/crypto/datasource/DataSourcePage";
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
import SmartDownloadPage from "./features/smart-download/SmartDownloadPage";
import AddressLibraryInput from "./features/address-library/AddressLibraryInput";
import { loadSystemNavigationPreference } from "./features/system/systemSettingsStore";
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
import { useAnalysisContext } from "./features/explorer-intelligence/analysisContext";

const { Sider, Content } = Layout;

dayjs.locale("zh-cn");

const menuItems: MenuProps["items"] = [
  { key: "explorer", icon: <SearchOutlined />, label: "链上查询" },
  { key: "analytics-graph", icon: <ApartmentOutlined />, label: "资金追踪" },
  { key: "intelligence", icon: <RobotOutlined />, label: "调查工作台" },
  {
    key: "analysis-center",
    icon: <DashboardOutlined />,
    label: "更多分析",
    children: [
      { key: "analytics-dashboard", label: "分析总览" },
      { key: "analytics-address", label: "地址画像" },
      { key: "risk", label: "风险分析" },
      { key: "analytics-report", label: "分析报告" },
      { key: "graph", label: "导入资金路径" },
      { key: "crypto-address", label: "地址区分" },
    ],
  },
  { key: "clean", icon: <DatabaseOutlined />, label: "数据资产" },
  { key: "reports", icon: <FileTextOutlined />, label: "案件" },
];

const systemMenuItems: MenuProps["items"] = [
  {
    key: "download-center",
    icon: <DownloadOutlined />,
    label: "下载中心",
    children: [
      { key: "smart-download", label: "下载任务" },
      {
        key: "advanced-download",
        label: "高级",
        children: [
          { key: "crypto-parquet", label: "Parquet 数据" },
          { key: "crypto-download", label: "浏览器下载" },
          { key: "download-dune", label: "Dune 下载" },
        ],
      },
    ],
  },
  { key: "crypto-datasource", icon: <DatabaseOutlined />, label: "数据源" },
  {
    key: "data-quality-center",
    icon: <WarningOutlined />,
    label: "数据质量",
    children: [
      { key: "data-quality", label: "语义质量" },
      { key: "financial-quality", label: "金融质量" },
      { key: "price-engine", label: "历史价格" },
    ],
  },
  { key: "system-settings", icon: <SettingOutlined />, label: "系统设置" },
];

export function App() {
  const { state: analysisState, update: updateAnalysis } = useAnalysisContext();
  const systemPreference = useMemo(() => loadSystemNavigationPreference(), []);
  const [active, setActive] = useState(() => systemPreference.rememberLastPage ? window.localStorage.getItem("etl.app.active.v1") || systemPreference.defaultPage : systemPreference.defaultPage);
  const [activeAddressParam, setActiveAddressParam] = useState<string | undefined>(undefined);
  const [sideCollapsed, setSideCollapsed] = useState(() => systemPreference.compactSidebar);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [globalQuery, setGlobalQuery] = useState("");
  const [serviceHealthy, setServiceHealthy] = useState<boolean | null>(null);
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
    let activeRequest = true;
    const checkHealth = async () => {
      try {
        const response = await fetch("/api/health", { cache: "no-store" });
        if (activeRequest) setServiceHealthy(response.ok);
      } catch {
        if (activeRequest) setServiceHealthy(false);
      }
    };
    void checkHealth();
    const timer = window.setInterval(checkHealth, 30_000);
    return () => {
      activeRequest = false;
      window.clearInterval(timer);
    };
  }, []);

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
    if (systemPreference.rememberLastPage) {
      window.localStorage.setItem("etl.app.active.v1", nextActive);
    }
    // 进入资金追踪自动收起左侧导航（画布全屏）；其他页面恢复展开
    setSideCollapsed(nextActive === "analytics-graph" || systemPreference.compactSidebar);
    setMobileNavOpen(false);
  };

  // 地址关系图全屏：无论从菜单还是其他入口（Dashboard 导航等）进入，
  // 均自动收起左侧导航；离开该页不强制恢复（保留用户手动状态）
  useEffect(() => {
    if (active === "analytics-graph") setSideCollapsed(true);
    if (active !== "analytics-graph" && systemPreference.compactSidebar) setSideCollapsed(true);
  }, [active]);

  // 全局搜索 → 地址详情页（图谱页搜索框合并复用同一入口）
  const openAddressDetail = (address: string, chainKey = analysisState.chain) => {
    const normalized = address.trim().toLowerCase();
    // 与图谱页搜索框（graphUpgrade.isValidAddress）一致的 EVM 地址校验，
    // 防止脏输入进入后端请求路径
    if (!/^0x[0-9a-f]{40}$/.test(normalized)) {
      void message.warning("请输入有效的 EVM 地址（0x + 40 位十六进制）");
      return;
    }
    setActiveAddressParam(normalized);
    updateAnalysis({ chain: chainKey, rootAddress: normalized, pendingQuery: "", tab: "overview" });
    setActive("explorer");
    setMobileNavOpen(false);
  };

  const runGlobalSearch = () => {
    const normalized = globalQuery.trim().toLowerCase();
    if (/^0x[0-9a-f]{40}$/.test(normalized)) {
      openAddressDetail(normalized);
      return;
    }
    updateAnalysis({ pendingQuery: globalQuery.trim() });
    setActive("explorer");
  };

  return (
    <ConfigProvider
      locale={zhCN}
      theme={{
        algorithm: theme.defaultAlgorithm,
        token: {
          colorPrimary: "#1769e0",
          colorInfo: "#1769e0",
          colorSuccess: "#0f9f6e",
          colorWarning: "#d97706",
          colorError: "#e5484d",
          colorBgLayout: "#f4f7fc",
          colorBorder: "#e3eaf5",
          borderRadius: 10,
          fontFamily: '"Microsoft YaHei", "PingFang SC", "Segoe UI", system-ui, sans-serif',
        },
      }}
    >
      <AntdApp>
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
        width={286}
        title="链上分析"
        open={mobileNavOpen}
        onClose={() => setMobileNavOpen(false)}
      >
        <Menu
          mode="inline"
          selectedKeys={[active]}
          items={menuItems}
          onClick={handleMenuClick}
        />
        <div className="mobile-system-menu">
          <div className="nav-section-label">系统</div>
          <Menu mode="inline" selectedKeys={[active]} items={systemMenuItems} onClick={handleMenuClick} />
        </div>
      </Drawer>
      <Layout className="app-shell">
        <Sider
          width={208}
          collapsedWidth={64}
          collapsed={sideCollapsed}
          className="side"
        >
          <div className="brand">
            <div className="brand-mark">链</div>
            <div>
              <strong>链上分析</strong>
              <span>Investigation OS</span>
            </div>
          </div>
          <Menu
            mode="inline"
            selectedKeys={[active]}
            items={menuItems}
            onClick={handleMenuClick}
          />
          <div className="side-system">
            {!sideCollapsed ? <div className="nav-section-label">系统</div> : null}
            <Menu mode="inline" selectedKeys={[active]} items={systemMenuItems} onClick={handleMenuClick} />
          </div>
        </Sider>
        <Layout className="app-main">
          {/* 地址关系图为沉浸式全屏工作台：隐藏顶部横条（搜索功能已并入图谱页搜索框） */}
          {active !== "analytics-graph" && active !== "explorer" ? (
            <header className="app-header">
              <div className="app-header-search">
                <AddressLibraryInput
                  placeholder="搜索地址 / 交易哈希"
                  value={globalQuery}
                  onChange={setGlobalQuery}
                  onSelect={(address, item) => openAddressDetail(address, item?.chain_key ?? analysisState.chain)}
                  onPressEnter={runGlobalSearch}
                  ariaLabel="全局搜索地址或交易哈希"
                />
              </div>
              <div className="app-header-context">
                <span className="app-header-page">{titleFor(active)}</span>
                <span className="app-network">EVM 多链</span>
                <span className={`app-health ${serviceHealthy === false ? "app-health-down" : ""}`}>
                  <i />
                  {serviceHealthy === null ? "检测中" : serviceHealthy ? "数据服务正常" : "数据服务异常"}
                </span>
              </div>
            </header>
          ) : null}
          <Content className={`content ${active === "graph" ? "content-graph" : ""} ${active === "explorer" ? "content-explorer" : ""}`}>
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
            {active === "clean" && analysisState.rootAddress ? <div className="xi-inherited-context"><span>继承 Explorer 上下文</span><Tag>{analysisState.chain.toUpperCase()}</Tag><Tag>{analysisState.window}</Tag>{analysisState.direction !== "all" ? <Tag>{analysisState.direction.toUpperCase()}</Tag> : null}{analysisState.tokenSymbol ? <Tag>{analysisState.tokenSymbol}</Tag> : null}{analysisState.minUSD ? <Tag>USD ≥ {analysisState.minUSD}</Tag> : null}{analysisState.entityFilters.map((entity) => <Tag key={entity}>{entity}</Tag>)}</div> : null}
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
            {active === "smart-download" && (
              <SmartDownloadPage
                onOpenAddress={openAddressDetail}
                onNavigate={(page) => setActive(page)}
              />
            )}
            {active === "crypto-rpc" && <RpcSettingsPage />}
            {active === "crypto-datasource" && <DataSourcePage onOpenRpc={() => setActive("crypto-rpc")} />}
            {active === "crypto-address" && <CryptoAddressPanel />}
            <Suspense fallback={null}>
              {active === "explorer" && <ExplorerPage onNavigate={(page, address) => { if (address) updateAnalysis({ rootAddress: address }); setActive(page === "fund-flow" ? "analytics-graph" : page); }} />}
              {active === "analytics-dashboard" && <AnalyticsDashboardPage onNavigate={(p, a) => { if (a) setActiveAddressParam(a); setActive(p); }} />}
              {active === "analytics-address" && <AnalyticsAddressPage initialAddress={activeAddressParam} />}
              {active === "analytics-graph" && <AnalyticsGraphPage onOpenAddress={openAddressDetail} />}
              {active === "analytics-report" && <AnalyticsReportPage />}
              {active === "risk" && <AnalyticsRiskPage />}
              {active === "data-quality" && <DataQualityPage />}
              {active === "financial-quality" && <FinancialQualityPage />}
              {active === "price-engine" && <PriceEnginePage />}
              {active === "intelligence" && <IntelligencePage />}
              {active === "reports" && <ReportPage />}
              {active === "system-settings" && <SystemSettingsPage />}
            </Suspense>
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
      </AntdApp>
    </ConfigProvider>
  );

  function titleFor(key: string) {
    return {
      explorer: "链上查询",
      clean: "数据集管理",
      graph: "导入资金路径",
      "download-dune": "Dune 下载",
      "crypto-download": "浏览器数据下载",
      "crypto-parquet": "链数据采集",
      "smart-download": "智能下载",
      "crypto-rpc": "EVM RPC 节点管理",
      "crypto-datasource": "数据源管理中心",
      "crypto-address": "地址区分",
      "analytics-dashboard": "仪表盘",
      "analytics-address": "地址画像",
      "analytics-graph": "资金追踪",
      "analytics-report": "案件报告",
      risk: "风险分析",
      intelligence: "调查工作台",
      reports: "调查报告",
      "system-settings": "系统设置",
    }[key];
  }
}

