import {
  ApartmentOutlined,
  DatabaseOutlined,
  FileSearchOutlined,
  FundProjectionScreenOutlined,
  SafetyCertificateOutlined,
  WarningOutlined,
  WalletOutlined,
} from "@ant-design/icons";
import { Button, Empty, Progress, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Panel,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
  useEdgesState,
  useNodesState,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useEffect, useMemo, useState } from "react";
import { MetricCard, PageHeader, Section, StatusDot } from "../../design-system/DesignSystem";
import { loadParquetJobs, type ParquetJob } from "../crypto/cryptoParquetApi";
import { listInvestigations, type Investigation } from "../intelligence/intelligenceApi";
import { fetchDashboard, fetchGraph, type DashboardOverview, type GraphData } from "./analyticsApi";
import { formatNumber } from "./format";
import "./dashboard.css";

interface Props {
  onNavigate: (page: string, address?: string) => void;
}

type DashboardState = {
  overview: DashboardOverview | null;
  graph: GraphData | null;
  parquetJobs: readonly ParquetJob[];
  investigations: Investigation[];
};

type WorkTask = {
  key: string;
  name: string;
  type: string;
  progress: number;
  status: string;
  updatedAt: string;
};

const EMPTY_STATE: DashboardState = {
  overview: null,
  graph: null,
  parquetJobs: [],
  investigations: [],
};

const ACTIVE_STATUSES = new Set(["running", "pending", "planning", "analyzing", "expanding", "verifying", "reporting"]);
const DASHBOARD_GRAPH_LIMIT = 12;
const DASHBOARD_MOBILE_GRAPH_LIMIT = 8;

type DashboardGraphNodeData = {
  address: string;
  degree: number;
  riskScore: number;
  kind: string;
};

function DashboardAddressNode({ data, selected }: NodeProps) {
  const node = data as DashboardGraphNodeData;
  const tone = riskTone(node.riskScore);
  return (
    <div className={`dashboard-flow-node dashboard-flow-node-${tone}${selected ? " is-selected" : ""}`}>
      <Handle type="target" position={Position.Top} />
      <span className="dashboard-flow-node-icon">
        {node.kind === "contract" ? <DatabaseOutlined /> : <WalletOutlined />}
      </span>
      <span className="dashboard-flow-node-copy">
        <strong>{shortAddress(node.address)}</strong>
        <small>{node.kind === "contract" ? "合约" : "地址"} · {node.degree} 关联</small>
      </span>
      <Handle type="source" position={Position.Bottom} />
    </div>
  );
}

const DASHBOARD_NODE_TYPES = { address: DashboardAddressNode };

export default function DashboardPage({ onNavigate }: Props) {
  const [state, setState] = useState<DashboardState>(EMPTY_STATE);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      const [overview, graph, jobs, investigations] = await Promise.allSettled([
        fetchDashboard(),
        fetchGraph(80),
        loadParquetJobs(),
        listInvestigations(),
      ]);
      if (!alive) return;
      setState({
        overview: overview.status === "fulfilled" ? overview.value : null,
        graph: graph.status === "fulfilled" ? graph.value : null,
        parquetJobs: jobs.status === "fulfilled" ? jobs.value : [],
        investigations: investigations.status === "fulfilled" ? investigations.value.items : [],
      });
      setLoading(false);
    };
    void load();
    return () => {
      alive = false;
    };
  }, []);

  const tasks = useMemo<WorkTask[]>(() => {
    const parquet = state.parquetJobs.slice(0, 3).map((job) => ({
      key: `parquet-${job.id}`,
      name: `${job.chain_key.toUpperCase()} 数据采集`,
      type: job.stage || "链数据采集",
      progress: Math.round(job.progress || 0),
      status: job.status,
      updatedAt: job.updated_at,
    }));
    const investigations = state.investigations.slice(0, 3).map((item) => ({
      key: `investigation-${item.id}`,
      name: shortAddress(item.target),
      type: "调查工作台",
      progress: Math.round(item.progress || 0),
      status: item.status,
      updatedAt: item.updated_at,
    }));
    return [...parquet, ...investigations]
      .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))
      .slice(0, 4);
  }, [state.investigations, state.parquetJobs]);

  const activeInvestigations = useMemo(
    () => state.investigations.filter((item) => ACTIVE_STATUSES.has(item.status.toLowerCase())).length,
    [state.investigations],
  );

  const overview = state.overview;
  const columns: ColumnsType<Investigation> = [
    {
      title: "调查目标",
      dataIndex: "target",
      ellipsis: true,
      render: (value: string) => <code className="dashboard-address">{shortAddress(value)}</code>,
    },
    {
      title: "状态",
      dataIndex: "status",
      width: 104,
      render: (value: string) => <StatusDot tone={statusTone(value)} label={statusLabel(value)} />,
    },
    {
      title: "进度",
      dataIndex: "progress",
      width: 132,
      render: (value: number) => <Progress percent={Math.round(value || 0)} size="small" showInfo={false} />,
    },
    {
      title: "更新时间",
      dataIndex: "updated_at",
      width: 158,
      render: (value: string) => formatDate(value),
    },
  ];

  return (
    <div className="ds-page dashboard-page">
      <PageHeader
        title="链上分析工作台"
        description="聚合数据资产、采集任务、调查进度与风险信号，所有指标均来自当前分析数据。"
        actions={(
          <>
            <Button icon={<ApartmentOutlined />} onClick={() => onNavigate("analytics-graph")}>打开图谱</Button>
            <Button type="primary" icon={<FileSearchOutlined />} onClick={() => onNavigate("intelligence")}>创建调查</Button>
          </>
        )}
      />

      <div className="dashboard-metrics">
        <MetricCard
          title="地址"
          value={formatNumber(overview?.address_count ?? 0)}
          detail="当前数据资产"
          icon={<WalletOutlined />}
          loading={loading}
        />
        <MetricCard
          title="链上事件"
          value={formatNumber(overview?.transaction_count ?? 0)}
          detail={`转账 ${formatNumber(overview?.transfer_count ?? 0)} 条`}
          icon={<FundProjectionScreenOutlined />}
          loading={loading}
        />
        <MetricCard
          title="Token"
          value={formatNumber(overview?.token_count ?? 0)}
          detail={overview?.transaction_count ? "分析数据已就绪" : "等待数据采集"}
          icon={<DatabaseOutlined />}
          tone="green"
          loading={loading}
        />
        <MetricCard
          title="高风险地址"
          value={formatNumber(overview?.risk_addresses ?? 0)}
          detail="当前规则扫描结果"
          icon={<SafetyCertificateOutlined />}
          tone="red"
          loading={loading}
        />
      </div>

      <div className="dashboard-status-grid">
        <Section title="实时任务" description="最近的数据采集与调查任务" className="dashboard-tasks">
          {tasks.length ? (
            <div className="dashboard-task-list">
              {tasks.map((task) => (
                <button
                  className="dashboard-task-row"
                  key={task.key}
                  onClick={() => onNavigate(task.key.startsWith("parquet-") ? "crypto-parquet" : "intelligence")}
                >
                  <span className="dashboard-task-name">
                    <strong>{task.name}</strong>
                    <small>{task.type}</small>
                  </span>
                  <Progress percent={task.progress} size="small" strokeColor="#1769e0" />
                  <StatusDot tone={statusTone(task.status)} label={statusLabel(task.status)} />
                  <time>{formatDate(task.updatedAt)}</time>
                </button>
              ))}
            </div>
          ) : (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无采集或调查任务" />
          )}
        </Section>

        <Section title="风险概览" description="调查与规则引擎的当前信号" className="dashboard-risk">
          <button onClick={() => onNavigate("risk")}>
            <span className="dashboard-risk-icon dashboard-risk-red"><WarningOutlined /></span>
            <span><small>高风险地址</small><strong>{formatNumber(overview?.risk_addresses ?? 0)}</strong></span>
            <em>查看风险分析</em>
          </button>
          <button onClick={() => onNavigate("intelligence")}>
            <span className="dashboard-risk-icon dashboard-risk-amber"><FileSearchOutlined /></span>
            <span><small>进行中的调查</small><strong>{formatNumber(activeInvestigations)}</strong></span>
            <em>进入调查工作台</em>
          </button>
        </Section>
      </div>

      <div className="dashboard-workbench-grid">
        <Section
          title="地址关系图谱"
          description="拖拽节点或缩放查看当前高关联地址"
          extra={<Button type="link" onClick={() => onNavigate("analytics-graph")}>查看完整图谱</Button>}
          className="dashboard-graph"
        >
          {state.graph?.nodes.length
            ? <DashboardRelationGraph graph={state.graph} />
            : <Empty className="dashboard-empty" description="当前没有可展示的关系数据" />}
        </Section>
        <Section
          title="最近调查任务"
          description="按最近更新时间排序"
          extra={<Button type="link" onClick={() => onNavigate("intelligence")}>查看全部</Button>}
          className="dashboard-investigations"
        >
          <Table
            columns={columns}
            dataSource={state.investigations.slice(0, 6)}
            rowKey="id"
            pagination={false}
            size="small"
            loading={loading}
            locale={{ emptyText: "暂无调查任务" }}
          />
        </Section>
      </div>
    </div>
  );
}

function DashboardRelationGraph({ graph }: { graph: GraphData }) {
  const compact = window.matchMedia("(max-width: 760px)").matches;
  const elements = useMemo(
    () => buildDashboardGraph(graph, compact ? DASHBOARD_MOBILE_GRAPH_LIMIT : DASHBOARD_GRAPH_LIMIT),
    [compact, graph],
  );
  const [nodes, , onNodesChange] = useNodesState<Node>(elements.nodes);
  const [edges, , onEdgesChange] = useEdgesState<Edge>(elements.edges);

  return (
    <div className="dashboard-graph-canvas" aria-label="地址关系图谱画布">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={DASHBOARD_NODE_TYPES}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        fitView
        fitViewOptions={{ padding: 0.18, maxZoom: 1.05 }}
        minZoom={0.25}
        maxZoom={1.8}
        nodesConnectable={false}
        elementsSelectable
        proOptions={{ hideAttribution: true }}
      >
        <Background color="#d9e3f0" gap={18} size={1} />
        <Controls showInteractive={false} />
        <MiniMap
          pannable
          zoomable
          nodeColor={(node) => riskColor(Number(node.data.riskScore ?? 0))}
          maskColor="rgba(248, 250, 252, .7)"
        />
        <Panel position="top-left" className="dashboard-graph-legend">
          <span><i className="risk" />高风险</span>
          <span><i className="warning" />中风险</span>
          <span><i className="normal" />一般地址</span>
        </Panel>
      </ReactFlow>
    </div>
  );
}

function buildDashboardGraph(graph: GraphData, nodeLimit: number): { nodes: Node[]; edges: Edge[] } {
  const sourceNodes = graph.nodes
    .slice(0, nodeLimit)
    .sort((a, b) => b.degree - a.degree || b.pagerank - a.pagerank);
  const visibleIds = new Set(sourceNodes.map((node) => node.id));

  const nodes: Node[] = sourceNodes.map((node, index) => ({
    id: node.id,
    type: "address",
    position: dashboardNodePosition(index, sourceNodes.length),
    data: {
      address: node.id,
      degree: node.degree,
      riskScore: node.risk_score,
      kind: node.type,
    } satisfies DashboardGraphNodeData,
  }));

  const edges: Edge[] = graph.edges
    .filter((edge) => visibleIds.has(edge.source) && visibleIds.has(edge.target))
    .slice(0, nodeLimit * 3)
    .map((edge, index) => ({
      id: `${edge.source}-${edge.target}-${edge.kind}-${index}`,
      source: edge.source,
      target: edge.target,
      type: "smoothstep",
      animated: edge.kind === "TRANSFER",
      markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14 },
      style: {
        stroke: edge.kind === "TRANSFER" ? "#1769e0" : edge.kind === "INTERACTION" ? "#0f9f6e" : "#94a3b8",
        strokeWidth: edge.kind === "TRANSFER" ? 1.5 : 1,
        opacity: 0.72,
      },
    }));

  return { nodes, edges };
}

function dashboardNodePosition(index: number, total: number) {
  if (index === 0) return { x: 0, y: 0 };
  const firstRingSize = Math.min(11, Math.max(0, total - 1));
  const firstRing = index <= firstRingSize;
  const ringIndex = firstRing ? index - 1 : index - firstRingSize - 1;
  const ringSize = firstRing ? firstRingSize : Math.max(1, total - firstRingSize - 1);
  const angle = (ringIndex / ringSize) * Math.PI * 2 - Math.PI / 2;
  const radiusX = firstRing ? 285 : 455;
  const radiusY = firstRing ? 170 : 285;
  return {
    x: Math.cos(angle) * radiusX,
    y: Math.sin(angle) * radiusY,
  };
}

function shortAddress(value: string) {
  if (value.length <= 18) return value;
  return `${value.slice(0, 8)}…${value.slice(-6)}`;
}

function riskTone(score: number) {
  if (score >= 70) return "risk";
  if (score >= 40) return "warning";
  return "normal";
}

function riskColor(score: number) {
  if (score >= 70) return "#e5484d";
  if (score >= 40) return "#d97706";
  return "#1769e0";
}

function formatDate(value: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

function statusTone(status: string): "success" | "warning" | "risk" | "neutral" {
  const value = status.toLowerCase();
  if (["completed", "complete", "done", "success"].includes(value)) return "success";
  if (["failed", "error", "cancelled", "canceled"].includes(value)) return "risk";
  if (ACTIVE_STATUSES.has(value)) return "warning";
  return "neutral";
}

function statusLabel(status: string) {
  const value = status.toLowerCase();
  if (["completed", "complete", "done", "success"].includes(value)) return "已完成";
  if (["failed", "error"].includes(value)) return "失败";
  if (["cancelled", "canceled"].includes(value)) return "已取消";
  if (ACTIVE_STATUSES.has(value)) return "进行中";
  return status || "等待中";
}
