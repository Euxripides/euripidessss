import { useEffect, useRef, useState } from "react";
import {
  Button,
  Collapse,
  Descriptions,
  Input,
  List,
  Progress,
  Select,
  Space,
  Steps,
  Table,
  Tag,
  Tabs,
  Typography,
  message,
} from "antd";
import {
  RobotOutlined,
  SearchOutlined,
  ThunderboltOutlined,
  FileTextOutlined,
  DownloadOutlined,
} from "@ant-design/icons";
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  type Edge as FlowEdge,
  type Node as FlowNode,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import {
  listInvestigations,
  getInvestigation,
  getReport,
  subscribeInvestigation,
  createInvestigation,
  type Investigation,
  type InvestigationMode,
  type RankedPath,
} from "./intelligenceApi";
import { InvestigationRequestInput } from "./investigationRequestInput";
import { AgentTimeline, PlanPreview } from "./investigationPlanView";
import { useAnalysisContext } from "../explorer-intelligence/analysisContext";
import { InvestigationRequestSummary, InvestigationScorePanel, ProfitReportPanel } from "./investigationResultSummary";
import { EvidenceViewer } from "./investigationEvidenceViewer";
import { DetailPanel, PageHeader } from "../../design-system/DesignSystem";
import "./intelligence.css";

const { Text, Paragraph } = Typography;

const SEVERITY_COLOR: Record<string, string> = {
  high: "red",
  medium: "orange",
  low: "blue",
};

const STATUS_TAG: Record<string, string> = {
  CREATED: "default",
  PLANNING: "processing",
  RUNNING: "processing",
  TRACING: "processing",
  EXPANDING: "processing",
  ANALYZING: "processing",
  VERIFYING: "processing",
  REPORTING: "processing",
  COMPLETED: "success",
  FAILED: "error",
};

// 调查闭环流程阶段（§17：规划 → 执行 → 发现 → 决策 → 完成）
const FLOW_STEPS = ["规划", "执行", "发现", "决策", "完成"];

const DECISION_TAG: Record<string, string> = {
  EXPAND: "green",
  STOP: "red",
  DEEP_ANALYSIS: "orange",
};

const OBS_TAG: Record<string, string> = {
  NEW_ADDRESS: "blue",
  NEW_PATH: "green",
  NEW_TRANSACTION: "cyan",
  RISK_EVENT: "red",
};

const TASK_TAG: Record<string, string> = {
  pending: "default",
  running: "processing",
  done: "success",
  skipped: "warning",
  failed: "error",
};

// flowStepIndex 由调查状态映射流程阶段下标。
function flowStepIndex(status: string): number {
  switch (status) {
    case "CREATED":
    case "PLANNING":
      return 0;
    case "RUNNING":
    case "TRACING":
      return 1;
    case "ANALYZING":
      return 2;
    case "EXPANDING":
    case "VERIFYING":
    case "REPORTING":
      return 3;
    default:
      return 4; // COMPLETED / FAILED
  }
}

export default function IntelligencePage() {
  const { state: analysisState, update: updateAnalysis } = useAnalysisContext();
  const [target, setTarget] = useState(analysisState.rootAddress);
  const [chainId, setChainId] = useState(analysisState.chain);
  // ── V2 调查请求输入（设计 §3：目的/期望结果/模式）──
  const [objective, setObjective] = useState("");
  const [expectedResult, setExpectedResult] = useState<string[]>([]);
  const [mode, setMode] = useState<InvestigationMode>("auto");
  const [investigations, setInvestigations] = useState<Investigation[]>([]);
  const [current, setCurrent] = useState<Investigation | null>(null);
  const [loading, setLoading] = useState(false);
  const [polling, setPolling] = useState(false);
  const sseRef = useRef<(() => void) | null>(null);

  const refreshList = async () => {
    const data = await listInvestigations();
    setInvestigations(data.items ?? []);
  };

  const loadDetail = async (id: string) => {
    const inv = await getInvestigation(id);
    if (inv) {
      setCurrent(inv);
      // 未完成则 SSE 订阅实时进度（#7 优化：替代 3s 轮询）
      if (inv.status !== "COMPLETED" && inv.status !== "FAILED") {
        startSSE(id);
      } else {
        stopSSE();
      }
    }
  };

  // SSE 实时订阅（#7 优化）
  const startSSE = (id: string) => {
    stopSSE();
    setPolling(true);
    sseRef.current = subscribeInvestigation(id, (inv) => {
      setCurrent(inv);
      if (inv.status === "COMPLETED" || inv.status === "FAILED") {
        stopSSE();
        refreshList();
      }
    });
  };

  const stopSSE = () => {
    if (sseRef.current) {
      sseRef.current();
      sseRef.current = null;
    }
    setPolling(false);
  };

  useEffect(() => {
    refreshList();
    return () => stopSSE();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (analysisState.rootAddress) setTarget(analysisState.rootAddress);
    setChainId(analysisState.chain);
  }, [analysisState.chain, analysisState.rootAddress]);

  const handleStart = async () => {
    const addr = target.trim().toLowerCase();
    if (!addr) {
      message.warning("请输入目标地址");
      return;
    }
    if (!objective.trim() && expectedResult.length === 0) {
      message.warning("请填写调查目的或至少选择一个期望结果");
      return;
    }
    setLoading(true);
    try {
      updateAnalysis({ rootAddress: addr, chain: chainId as typeof analysisState.chain });
      const result = await createInvestigation({
        address: addr,
        chain: chainId,
        objective: objective.trim(),
        expected_result: expectedResult,
        mode,
      });
      const inv = result?.investigation;
      if (!inv) throw new Error("启动失败");
      message.success(`调查已启动: ${inv.id}`);
      setTarget("");
      await refreshList();
      await loadDetail(inv.id);
    } catch (e) {
      message.error(`启动失败: ${(e as Error).message}`);
    } finally {
      setLoading(false);
    }
  };

  const handleDownloadReport = async (format: "markdown" | "html" | "json") => {
    if (!current) return;
    try {
      const content = await getReport(current.id, format);
      const blob = new Blob([content], { type: "text/plain;charset=utf-8" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${current.id}-report.${format === "json" ? "json" : format === "html" ? "html" : "md"}`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      message.error(`报告下载失败: ${(e as Error).message}`);
    }
  };

  // ── 图谱数据（路径 → ReactFlow 节点/边）──
  const graphNodes: FlowNode[] = [];
  const graphEdges: FlowEdge[] = [];
  const nodeSet = new Map<string, FlowNode>();
  const addNode = (id: string, label?: string) => {
    if (!nodeSet.has(id)) {
      const short = `${id.slice(0, 6)}…${id.slice(-4)}`;
      const node: FlowNode = {
        id,
        position: { x: 0, y: 0 },
        data: { label: label ?? short },
      };
      nodeSet.set(id, node);
      graphNodes.push(node);
    }
    return nodeSet.get(id)!;
  };
  (current?.paths ?? []).slice(0, 5).forEach((p: RankedPath, pi: number) => {
    const nodes = p.path.nodes ?? [];
    nodes.forEach((n, ni) => {
      const node = addNode(n);
      node.position = { x: pi * 320 + ni * 140, y: pi * 220 };
    });
    (p.path.edges ?? []).forEach((e, ei) => {
      graphEdges.push({
        id: `${p.path.nodes?.[0]}-${ei}-${e.tx_hash?.slice(0, 8) ?? ei}`,
        source: e.from,
        target: e.to,
        label: `${e.token} ${e.amount}`,
        animated: true,
        style: { stroke: "#1677ff" },
      });
    });
  });

  const pathColumns = [
    { title: "路径", dataIndex: "summary", key: "summary", ellipsis: true },
    {
      title: "评分",
      dataIndex: "score",
      key: "score",
      width: 90,
      render: (s: RankedPath["score"]) => <Text strong>{s?.total ?? 0}</Text>,
    },
    {
      title: "金额",
      key: "amount",
      width: 100,
      render: (_: unknown, p: RankedPath) => <Text>{p.score?.amount ?? 0}</Text>,
    },
    {
      title: "时间连续性",
      key: "time",
      width: 100,
      render: (_: unknown, p: RankedPath) => <Text>{p.score?.time_continuity ?? 0}</Text>,
    },
  ];

  const entityColumns = [
    { title: "地址", dataIndex: "address", key: "address", ellipsis: true },
    {
      title: "实体",
      dataIndex: "entity",
      key: "entity",
      render: (e: string) => <Tag color="geekblue">{e}</Tag>,
    },
    { title: "标签", dataIndex: "label", key: "label" },
    {
      title: "风险",
      dataIndex: "risk",
      key: "risk",
      render: (r: number) => <Tag color={r >= 60 ? "red" : r >= 30 ? "orange" : "green"}>{r?.toFixed(1)}</Tag>,
    },
    { title: "交易数", dataIndex: "tx_count", key: "tx_count" },
  ];

  return (
    <div className="ds-page analytics-page intelligence-page">
      <PageHeader
        title="调查工作台"
        description="启动多轮链上调查，持续接收实时进度，并在同一工作区核验证据、路径、实体、风险与 AI 结论。"
      />

      {analysisState.rootAddress ? <div className="xi-inherited-context"><Typography.Text type="secondary">继承 Explorer 上下文：</Typography.Text><Tag>{analysisState.chain.toUpperCase()}</Tag><Tag>{analysisState.window}</Tag>{analysisState.direction !== "all" ? <Tag>{analysisState.direction.toUpperCase()}</Tag> : null}{analysisState.tokenSymbol ? <Tag>{analysisState.tokenSymbol}</Tag> : null}{analysisState.tokens.map((token) => <Tag key={token}>{token.slice(0, 10)}…</Tag>)}{analysisState.minUSD ? <Tag>USD ≥ {analysisState.minUSD}</Tag> : null}{analysisState.entityFilters.map((entity) => <Tag key={entity}>{entity}</Tag>)}</div> : null}

      {/* 启动调查 */}
      <DetailPanel
        size="small"
        className="intelligence-start-panel"
        title="启动调查"
        description="输入目标地址、调查目的与期望结果，系统将按意图规划、执行、验证并生成报告。"
      >
        <div className="intelligence-start-row">
          <Input
            placeholder="输入目标地址（0x...）"
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            onPressEnter={handleStart}
            prefix={<SearchOutlined />}
            allowClear
          />
          <Select
            value={chainId}
            onChange={setChainId}
            style={{ width: 120 }}
            options={[
              { value: "bsc", label: "BSC" },
              { value: "eth", label: "ETH" },
              { value: "base", label: "Base" },
              { value: "arbitrum", label: "Arbitrum" },
            ]}
          />
          <Button type="primary" icon={<ThunderboltOutlined />} loading={loading} onClick={handleStart}>
            启动调查
          </Button>
        </div>
        <InvestigationRequestInput
          mode={mode}
          onModeChange={setMode}
          objective={objective}
          onObjectiveChange={setObjective}
          expectedResult={expectedResult}
          onExpectedResultChange={setExpectedResult}
        />
      </DetailPanel>

      <div className="intelligence-layout">
        {/* 左：调查列表 */}
        <DetailPanel
          size="small"
          title="调查记录"
          description={`${investigations.length} 个历史任务`}
          className="intelligence-list-panel"
        >
            <List
              size="small"
              dataSource={investigations}
              pagination={{ pageSize: 8, size: "small" }}
              renderItem={(inv) => (
                <List.Item
                  onClick={() => loadDetail(inv.id)}
                  className={current?.id === inv.id ? "intelligence-list-item intelligence-list-item-active" : "intelligence-list-item"}
                >
                  <List.Item.Meta
                    title={
                      <Space>
                        <Text code>{inv.id}</Text>
                        <Tag color={STATUS_TAG[inv.status] ?? "default"}>{inv.status}</Tag>
                      </Space>
                    }
                    description={
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        {inv.target.slice(0, 10)}… · {inv.progress}%
                      </Text>
                    }
                  />
                </List.Item>
              )}
            />
        </DetailPanel>

        {/* 右：调查详情 */}
        <div className="intelligence-detail">
          {current ? (
            <DetailPanel
              size="small"
              className="intelligence-detail-panel"
              title={
                <Space>
                  <Text strong>{current.id}</Text>
                  <Tag color={STATUS_TAG[current.status] ?? "default"}>{current.status}</Tag>
                  {polling && <Tag color="processing">追踪中</Tag>}
                </Space>
              }
              extra={
                <Space>
                  <Button size="small" icon={<DownloadOutlined />} onClick={() => handleDownloadReport("markdown")}>
                    MD
                  </Button>
                  <Button size="small" icon={<DownloadOutlined />} onClick={() => handleDownloadReport("html")}>
                    HTML
                  </Button>
                  <Button size="small" icon={<DownloadOutlined />} onClick={() => handleDownloadReport("json")}>
                    JSON
                  </Button>
                </Space>
              }
            >
              <Progress percent={Math.round(current.progress ?? 0)} size="small" status={current.status === "FAILED" ? "exception" : undefined} />
              <Text type="secondary">
                阶段：{current.stage_detail || current.status}
              </Text>
              {current.error && <Text type="danger">错误：{current.error}</Text>}

              {/* ── V2 调查请求摘要 + 六维价值评分（设计 §11/§9）── */}
              <InvestigationRequestSummary inv={current} />
              <InvestigationScorePanel inv={current} />
              {/* ── V2.1 获利与沉淀检测（估算/可信度/依据）── */}
              <ProfitReportPanel inv={current} />

              <Tabs
                size="small"
                items={[
                  {
                    key: "flow",
                    label: `调查流程 (${current.round ?? 0}轮)`,
                    children: (
                      <Space direction="vertical" style={{ width: "100%" }}>
                        <Steps
                          size="small"
                          current={flowStepIndex(current.status)}
                          items={FLOW_STEPS.map((s) => ({ title: s }))}
                        />
                        {/* ── V2 计划预览 + 执行时间线（设计 §12/§13）── */}
                        <PlanPreview inv={current} />
                        <AgentTimeline inv={current} />
                        <Descriptions size="small" column={{ xs: 1, md: 2 }} bordered>
                          <Descriptions.Item label="当前轮次">{current.round ?? 0}</Descriptions.Item>
                          <Descriptions.Item label="完成时间">
                            {current.completed_at ? new Date(current.completed_at).toLocaleString() : "-"}
                          </Descriptions.Item>
                          <Descriptions.Item label="停止原因" span={2}>
                            {current.stop_reason || "-"}
                          </Descriptions.Item>
                        </Descriptions>
                        {current.decision && (
                          <DetailPanel size="small" title="本轮决策">
                            <Space direction="vertical" style={{ width: "100%" }}>
                              <Space wrap>
                                <Tag color={DECISION_TAG[current.decision.action] ?? "default"}>
                                  {current.decision.action}
                                </Tag>
                                <Text type="secondary">第 {current.decision.round} 轮</Text>
                              </Space>
                              <Descriptions size="small" column={{ xs: 2, md: 4 }} bordered>
                                <Descriptions.Item label="路径分">
                                  {current.decision.scores?.path_score?.toFixed(1)}
                                </Descriptions.Item>
                                <Descriptions.Item label="风险分">
                                  {current.decision.scores?.risk_score?.toFixed(1)}
                                </Descriptions.Item>
                                <Descriptions.Item label="实体分">
                                  {current.decision.scores?.entity_score?.toFixed(1)}
                                </Descriptions.Item>
                                <Descriptions.Item label="扩展分">
                                  {current.decision.scores?.expansion_score?.toFixed(1)}
                                </Descriptions.Item>
                              </Descriptions>
                              <List
                                size="small"
                                dataSource={current.decision.reasons ?? []}
                                renderItem={(r) => (
                                  <List.Item>
                                    <Text type="secondary">• {r}</Text>
                                  </List.Item>
                                )}
                              />
                              {(current.decision.next_targets?.length ?? 0) > 0 && (
                                <Space wrap>
                                  <Text strong>下一轮目标：</Text>
                                  {current.decision.next_targets!.map((a) => (
                                    <Tag key={a} color="blue">
                                      {a.slice(0, 8)}…
                                    </Tag>
                                  ))}
                                </Space>
                              )}
                            </Space>
                          </DetailPanel>
                        )}
                        {(current.rounds?.length ?? 0) > 0 && (
                          <DetailPanel size="small" title={`轮次记录 (${current.rounds!.length})`}>
                            <List
                              size="small"
                              dataSource={current.rounds}
                              renderItem={(r) => (
                                <List.Item>
                                  <Space wrap>
                                    <Tag>第 {r.round} 轮</Tag>
                                    <Tag color={DECISION_TAG[r.decision] ?? "default"}>{r.decision || "-"}</Tag>
                                    <Text type="secondary">{r.note}</Text>
                                  </Space>
                                </List.Item>
                              )}
                            />
                          </DetailPanel>
                        )}
                        {(current.tasks?.length ?? 0) > 0 && (
                          <DetailPanel size="small" title={`任务队列 (${current.tasks!.length})`}>
                            <Table
                              size="small"
                              rowKey={(t) => t.id}
                              pagination={{ pageSize: 8, size: "small" }}
                              dataSource={current.tasks}
                              scroll={{ x: 720 }}
                              columns={[
                                {
                                  title: "类型",
                                  dataIndex: "type",
                                  key: "type",
                                  render: (v: string) => <Tag color="geekblue">{v}</Tag>,
                                },
                                {
                                  title: "状态",
                                  dataIndex: "status",
                                  key: "status",
                                  width: 90,
                                  render: (v: string) => <Tag color={TASK_TAG[v] ?? "default"}>{v}</Tag>,
                                },
                                {
                                  title: "目标",
                                  dataIndex: "target",
                                  key: "target",
                                  ellipsis: true,
                                  render: (v?: string) => (v ? `${v.slice(0, 10)}…` : "-"),
                                },
                                {
                                  title: "结果",
                                  dataIndex: "result",
                                  key: "result",
                                  ellipsis: true,
                                  render: (v?: string) => v || "-",
                                },
                                { title: "轮次", dataIndex: "round", key: "round", width: 60 },
                              ]}
                            />
                          </DetailPanel>
                        )}
                        {(current.observations?.length ?? 0) > 0 && (
                          <DetailPanel size="small" title={`调查观察 (${current.observations!.length})`}>
                            <List
                              size="small"
                              dataSource={current.observations}
                              pagination={{ pageSize: 8, size: "small" }}
                              renderItem={(o) => (
                                <List.Item>
                                  <Space wrap>
                                    <Tag color={OBS_TAG[o.type] ?? "default"}>{o.type}</Tag>
                                    <Text code>{o.address ? `${o.address.slice(0, 10)}…` : o.detail.slice(0, 60)}</Text>
                                    <Text type="secondary">{o.source}</Text>
                                  </Space>
                                </List.Item>
                              )}
                            />
                          </DetailPanel>
                        )}
                      </Space>
                    ),
                  },
                  {
                    key: "paths",
                    label: `资金路径 (${current.paths?.length ?? 0})`,
                    children: (
                      <Table
                        size="small"
                        rowKey={(p) => p.summary ?? p.path.nodes?.join("→") ?? Math.random().toString()}
                        columns={pathColumns}
                        dataSource={current.paths ?? []}
                        pagination={{ pageSize: 5, size: "small" }}
                        scroll={{ x: 540 }}
                      />
                    ),
                  },
                  {
                    key: "graph",
                    label: "资金图谱",
                    children: graphNodes.length > 0 ? (
                      <div style={{ height: 360 }}>
                        <ReactFlow nodes={graphNodes} edges={graphEdges} fitView>
                          <Background />
                          <Controls />
                          <MiniMap />
                        </ReactFlow>
                      </div>
                    ) : (
                      <Text type="secondary">暂无图谱数据</Text>
                    ),
                  },
                  {
                    key: "evidence",
                    label: `证据链 (${current.evidence?.length ?? 0})`,
                    children: (
                      <EvidenceViewer
                        investigationId={current.id}
                        done={current.status === "COMPLETED" || current.status === "FAILED"}
                      />
                    ),
                  },
                  {
                    key: "entities",
                    label: `实体 (${current.entities?.length ?? 0})`,
                    children: (
                      <Table
                        size="small"
                        rowKey={(e) => e.address}
                        columns={entityColumns}
                        dataSource={current.entities ?? []}
                        pagination={{ pageSize: 8, size: "small" }}
                        scroll={{ x: 680 }}
                      />
                    ),
                  },
                  {
                    key: "risk",
                    label: `风险 (${current.patterns?.length ?? 0})`,
                    children: (
                      <List
                        size="small"
                        dataSource={current.patterns ?? []}
                        renderItem={(p) => (
                          <List.Item>
                            <Space wrap>
                              <Tag color={SEVERITY_COLOR[p.severity] ?? "blue"}>{p.severity}</Tag>
                              <Text code>{p.type}</Text>
                              <Text>{p.detail}</Text>
                            </Space>
                          </List.Item>
                        )}
                      />
                    ),
                  },
                  {
                    key: "ai",
                    label: "AI 助手",
                    children: current.ai_analysis || current.hypotheses?.length || current.findings?.length || current.ai_suggestion ? (
                      <Collapse
                        size="small"
                        items={[
                          {
                            key: "suggestion",
                            label: "AI 下一步建议",
                            children: current.ai_suggestion ? (
                              <Space direction="vertical" style={{ width: "100%" }}>
                                <Space wrap>
                                  <Tag color={DECISION_TAG[current.ai_suggestion.action] ?? "default"}>
                                    {current.ai_suggestion.action}
                                  </Tag>
                                  <Text type="secondary">置信度 {(current.ai_suggestion.confidence ?? 0).toFixed(2)}</Text>
                                  <Text type="secondary">来源 {current.ai_suggestion.source}</Text>
                                </Space>
                                <List
                                  size="small"
                                  dataSource={current.ai_suggestion.reasons ?? []}
                                  renderItem={(r) => (
                                    <List.Item>
                                      <Text type="secondary">• {r}</Text>
                                    </List.Item>
                                  )}
                                />
                              </Space>
                            ) : (
                              <Text type="secondary">暂无 AI 建议</Text>
                            ),
                          },
                          {
                            key: "hypotheses",
                            label: `调查假设 (${current.hypotheses?.length ?? 0})`,
                            children:
                              (current.hypotheses?.length ?? 0) > 0 ? (
                                <List
                                  size="small"
                                  dataSource={current.hypotheses}
                                  renderItem={(h) => (
                                    <List.Item>
                                      <Space direction="vertical" style={{ width: "100%" }}>
                                        <Space wrap>
                                          <Tag color={h.status === "evaluated" ? "green" : h.status === "verifying" ? "processing" : "default"}>
                                            {h.status}
                                          </Tag>
                                          <Text strong>{h.title}</Text>
                                          <Tag color={h.source === "ai" ? "purple" : "blue"}>{h.source}</Tag>
                                          <Text type="secondary">置信度 {(h.confidence ?? 0).toFixed(2)}</Text>
                                        </Space>
                                        <Text type="secondary">{h.description}</Text>
                                        {(h.tasks?.length ?? 0) > 0 && (
                                          <Space wrap size={4}>
                                            {h.tasks.map((t, i) => (
                                              <Tag key={i} color="geekblue">
                                                {t.type}
                                              </Tag>
                                            ))}
                                          </Space>
                                        )}
                                        {h.note && <Text type="secondary">状态说明：{h.note}</Text>}
                                      </Space>
                                    </List.Item>
                                  )}
                                />
                              ) : (
                                <Text type="secondary">暂无调查假设</Text>
                              ),
                          },
                          {
                            key: "findings",
                            label: `已验证发现 (${current.findings?.length ?? 0})`,
                            children:
                              (current.findings?.length ?? 0) > 0 ? (
                                <List
                                  size="small"
                                  dataSource={current.findings}
                                  renderItem={(vf) => (
                                    <List.Item>
                                      <Space direction="vertical" style={{ width: "100%" }}>
                                        <Space wrap>
                                          <Tag color={vf.status === "VERIFIED" ? "green" : vf.status === "REJECTED" ? "red" : "orange"}>
                                            {vf.status}
                                          </Tag>
                                          <Text code>{vf.finding.type}</Text>
                                          <Text type="secondary">置信度 {(vf.finding.confidence ?? 0).toFixed(2)}</Text>
                                        </Space>
                                        <Text>{vf.finding.detail}</Text>
                                        {(vf.finding.evidence?.length ?? 0) > 0 && (
                                          <Text type="secondary" style={{ fontSize: 12 }}>
                                            证据：{vf.finding.evidence.join(", ")}
                                          </Text>
                                        )}
                                        <Text type="secondary" style={{ fontSize: 12 }}>
                                          {vf.reason}
                                        </Text>
                                      </Space>
                                    </List.Item>
                                  )}
                                />
                              ) : (
                                <Text type="secondary">暂无已验证发现</Text>
                              ),
                          },
                          {
                            key: "summary",
                            label: "资金行为总结",
                            children: <Paragraph>{current.ai_analysis?.summary}</Paragraph>,
                          },
                          {
                            key: "insights",
                            label: `洞察 (${current.ai_analysis?.insights?.length ?? 0})`,
                            children: (
                              <List
                                size="small"
                                dataSource={current.ai_analysis?.insights ?? []}
                                renderItem={(i) => <List.Item>{i}</List.Item>}
                              />
                            ),
                          },
                          {
                            key: "suggestions",
                            label: "下一步建议",
                            children: (
                              <List
                                size="small"
                                dataSource={current.ai_analysis?.suggestions ?? []}
                                renderItem={(s) => <List.Item>{s}</List.Item>}
                              />
                            ),
                          },
                        ]}
                      />
                    ) : (
                      <Text type="secondary">
                        {current.status === "COMPLETED" ? "未启用 AI 分析（需配置 DEEPSEEK_API_KEY）" : "AI 分析进行中..."}
                      </Text>
                    ),
                  },
                  {
                    key: "memory",
                    label: "调查记忆",
                    children: current.memory ? (
                      <Descriptions size="small" column={1} bordered>
                        <Descriptions.Item label="已发现地址">
                          {Object.keys(current.memory.discovered_at ?? {}).length} 个
                        </Descriptions.Item>
                        <Descriptions.Item label="已分析路径">
                          {current.memory.analyzed_paths?.length ?? 0} 条
                        </Descriptions.Item>
                        <Descriptions.Item label="已忽略实体">
                          {current.memory.ignored_entities?.length ?? 0} 个
                        </Descriptions.Item>
                        <Descriptions.Item label="已完成任务">
                          {(current.memory.completed_tasks?.length ?? 0) > 0 ? (
                            <Space wrap>
                              {current.memory.completed_tasks.map((t) => (
                                <Tag key={t}>{t}</Tag>
                              ))}
                            </Space>
                          ) : (
                            "-"
                          )}
                        </Descriptions.Item>
                        <Descriptions.Item label="结论">
                          <List
                            size="small"
                            dataSource={current.memory.conclusions ?? []}
                            renderItem={(c) => <List.Item>{c}</List.Item>}
                          />
                        </Descriptions.Item>
                      </Descriptions>
                    ) : (
                      <Text type="secondary">暂无记忆</Text>
                    ),
                  },
                  {
                    key: "plan",
                    label: "调查计划",
                    children: current.plan ? (
                      <Space direction="vertical" style={{ width: "100%" }}>
                        {current.strategy && (
                          <DetailPanel size="small" title="AI 调查策略">
                            <Space direction="vertical" style={{ width: "100%" }}>
                              <Space wrap>
                                <Tag color="purple">{current.strategy.strategy}</Tag>
                                <Text type="secondary">置信度 {(current.strategy.confidence ?? 0).toFixed(2)}</Text>
                              </Space>
                              {current.strategy.rationale && <Text>{current.strategy.rationale}</Text>}
                              {(current.strategy.tasks?.length ?? 0) > 0 && (
                                <Space wrap>
                                  {current.strategy.tasks.map((t, i) => (
                                    <Tag key={i} color="geekblue">
                                      {t.type}
                                      {t.target ? ` (${t.target.slice(0, 8)}…)` : ""}
                                    </Tag>
                                  ))}
                                </Space>
                              )}
                            </Space>
                          </DetailPanel>
                        )}
                        <List
                          size="small"
                          dataSource={current.plan.tasks ?? []}
                          renderItem={(t) => (
                            <List.Item>
                              <Space>
                                <Tag color="blue">{t.type}</Tag>
                                <Text>{t.description}</Text>
                              </Space>
                            </List.Item>
                          )}
                        />
                      </Space>
                    ) : (
                      <Text type="secondary">暂无计划</Text>
                    ),
                  },
                ]}
              />
            </DetailPanel>
          ) : (
            <DetailPanel size="small" className="intelligence-empty-detail">
              <RobotOutlined />
              <Text type="secondary">选择调查记录查看详情，或输入地址启动新调查。</Text>
            </DetailPanel>
          )}
        </div>
      </div>
    </div>
  );
}
