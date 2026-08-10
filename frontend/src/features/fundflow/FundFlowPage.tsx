// 资金流智能（Fund Flow Intelligence V2）：路径评分 / 获利归因 / 沉淀识别 / 兑现候选 / 回流 / 守恒。
import { useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Form,
  Input,
  InputNumber,
  message,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import { ExperimentOutlined, NodeIndexOutlined, ThunderboltOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import {
  analyzeFundFlow,
  type CashoutResult,
  type FlowPath,
  type FundFlowAnalysis,
  type ProfitAttribution,
  type SettlementResult,
} from "./fundFlowApi";
import { useAnalysisContext } from "../explorer-intelligence/analysisContext";

const chainOptions = [
  { value: "bsc", label: "BNB Smart Chain" },
  { value: "eth", label: "Ethereum" },
  { value: "base", label: "Base" },
  { value: "arbitrum", label: "Arbitrum One" },
];

const goalOptions = [
  { value: "cashout", label: "交易所落点" },
  { value: "settlement", label: "资金沉淀" },
  { value: "profit", label: "获利地址" },
  { value: "collector", label: "归集地址" },
];

const PATH_TYPE_LABELS: Record<string, string> = {
  DIRECT_CASHOUT: "直接兑现",
  MULTI_HOP_CASHOUT: "多跳兑现",
  COLLECT_AND_SETTLE: "归集沉淀",
  BRIDGE_EXIT: "跨链出口",
  UNKNOWN: "未知",
};

const TERMINAL_LABELS: Record<string, string> = {
  EXCHANGE: "交易所",
  CEX_DEPOSIT: "交易所入金",
  CEX_HOT_WALLET: "交易所热钱包",
  PAYMENT_SERVICE: "支付服务",
  CUSTODIAN: "托管服务",
  BRIDGE: "跨链桥",
  SETTLEMENT: "沉淀",
  DORMANT_WALLET: "沉睡钱包",
};

export default function FundFlowPage() {
  const { state: analysisState, update: updateAnalysis } = useAnalysisContext();
  const [form] = Form.useForm();
  const [analysis, setAnalysis] = useState<FundFlowAnalysis | null>(null);
  const [loading, setLoading] = useState(false);
  const [selectedPath, setSelectedPath] = useState<FlowPath | null>(null);

  const onAnalyze = async () => {
    const values = await form.validateFields();
    updateAnalysis({
      chain: values.chain_key,
      rootAddress: values.root_address.trim().toLowerCase(),
      tokens: values.token ? [values.token.trim().toLowerCase()] : [],
      caseID: values.investigation_id || undefined,
    });
    setLoading(true);
    try {
      const r = await analyzeFundFlow({
        chain_key: values.chain_key,
        root_address: values.root_address,
        token: values.token || undefined,
        goal: values.goal,
        max_depth: values.max_depth ?? 4,
        investigation_id: values.investigation_id || undefined,
      });
      if (r) {
        setAnalysis(r);
        void message.success(r.cache_hit ? "已命中资金流缓存" : "资金流智能分析完成");
      }
    } finally {
      setLoading(false);
    }
  };

  const profitColumns: ColumnsType<ProfitAttribution> = [
    { title: "地址", dataIndex: "address", width: 220, render: (v) => <code>{v}</code> },
    { title: "级别", dataIndex: "level", width: 70 },
    { title: "累计流入", dataIndex: "gross_inflow", width: 140 },
    { title: "累计流出", dataIndex: "gross_outflow", width: 140 },
    { title: "净获利", dataIndex: "net_profit", width: 150 },
    {
      title: "置信度",
      width: 90,
      render: (_, r) => `${(r.confidence * 100).toFixed(0)}%`,
    },
  ];

  const settleColumns: ColumnsType<SettlementResult> = [
    { title: "地址", dataIndex: "address", width: 220, render: (v) => <code>{v}</code> },
    { title: "沉淀类型", dataIndex: "settlement_type", width: 150 },
    { title: "留存金额", dataIndex: "retained_value", width: 140 },
    {
      title: "沉淀分",
      width: 90,
      render: (_, r) => `${(r.settlement_score * 100).toFixed(0)}`,
    },
    {
      title: "持有时长",
      width: 100,
      render: (_, r) => `${Math.round(r.holding_duration_seconds / 86400)} 天`,
    },
    {
      title: "置信度",
      width: 90,
      render: (_, r) => `${(r.confidence * 100).toFixed(0)}%`,
    },
  ];

  const cashoutColumns: ColumnsType<CashoutResult> = [
    { title: "来源", dataIndex: "source_address", width: 200, render: (v) => <code>{v}</code> },
    { title: "落点", dataIndex: "destination_address", width: 200, render: (v) => <code>{v}</code> },
    { title: "实体", dataIndex: "entity_name", width: 180 },
    { title: "路径类型", dataIndex: "path_type", width: 110, render: (v) => PATH_TYPE_LABELS[v] ?? v },
    { title: "Token", dataIndex: "token", width: 90 },
    { title: "金额", dataIndex: "amount", width: 140 },
    {
      title: "置信度",
      width: 90,
      render: (_, r) => `${(r.confidence * 100).toFixed(0)}%`,
    },
  ];

  return (
    <div style={{ padding: 24 }}>
      <Typography.Title level={4}>
        <NodeIndexOutlined /> 资金流智能
      </Typography.Title>
      {analysisState.rootAddress ? <Space wrap style={{ marginBottom: 12 }}><Typography.Text type="secondary">继承 Explorer 上下文：</Typography.Text><Tag>{analysisState.window}</Tag>{analysisState.direction !== "all" ? <Tag>{analysisState.direction.toUpperCase()}</Tag> : null}{analysisState.tokenSymbol ? <Tag>{analysisState.tokenSymbol}</Tag> : null}{analysisState.minUSD ? <Tag>USD ≥ {analysisState.minUSD}</Tag> : null}{analysisState.maxUSD ? <Tag>USD ≤ {analysisState.maxUSD}</Tag> : null}{analysisState.entityFilters.map((entity) => <Tag key={entity}>{entity}</Tag>)}{analysisState.protocolFilters.map((protocol) => <Tag key={protocol}>{protocol}</Tag>)}</Space> : null}
      <Card style={{ marginBottom: 16 }}>
        <Form form={form} layout="inline" initialValues={{ chain_key: analysisState.chain, root_address: analysisState.rootAddress || undefined, token: analysisState.tokens[0], investigation_id: analysisState.caseID, goal: "cashout", max_depth: 4 }}>
          <Form.Item label="链" name="chain_key">
            <Select options={chainOptions} style={{ width: 150 }} />
          </Form.Item>
          <Form.Item label="根地址" name="root_address" rules={[{ required: true, message: "请输入 EVM 地址" }]}>
            <Input placeholder="0x…" style={{ width: 320 }} />
          </Form.Item>
          <Form.Item label="Token" name="token">
            <Input placeholder="合约地址（可选）" style={{ width: 200 }} />
          </Form.Item>
          <Form.Item label="调查目标" name="goal">
            <Select options={goalOptions} style={{ width: 140 }} />
          </Form.Item>
          <Form.Item label="最大深度" name="max_depth">
            <InputNumber min={1} max={6} style={{ width: 90 }} />
          </Form.Item>
          <Form.Item label="调查 ID" name="investigation_id">
            <Input placeholder="default" style={{ width: 130 }} />
          </Form.Item>
          <Button type="primary" icon={<ExperimentOutlined />} loading={loading} onClick={() => void onAnalyze()}>
            分析
          </Button>
        </Form>
      </Card>

      {analysis && (
        <>
          <Space wrap style={{ marginBottom: 16 }}>
            <Card size="small" style={{ minWidth: 130 }}>
              <div>关键路径 {analysis.summary?.high_value_paths ?? 0}</div>
            </Card>
            <Card size="small" style={{ minWidth: 130 }}>
              <div>兑现候选 {analysis.summary?.cashout_candidates ?? 0}</div>
            </Card>
            <Card size="small" style={{ minWidth: 130 }}>
              <div>沉淀候选 {analysis.summary?.settlement_candidates ?? 0}</div>
            </Card>
            <Card size="small" style={{ minWidth: 130 }}>
              <div>获利地址 {analysis.summary?.profit_addresses ?? 0}</div>
            </Card>
            <Card size="small" style={{ minWidth: 130 }}>
              <div>回流 {analysis.summary?.round_trips ?? 0}</div>
            </Card>
            <Card size="small" style={{ minWidth: 160 }}>
              <div>
                守恒通过率 {(((analysis.summary?.conservation_pass_rate ?? 0) as number) * 100).toFixed(0)}%
              </div>
            </Card>
            <Tag color={analysis.cache_hit ? "green" : "blue"}>{analysis.cache_hit ? "缓存命中" : "实时计算"}</Tag>
          </Space>

          <Card title={`关键资金路径（${analysis.paths?.length ?? 0}）`} style={{ marginBottom: 16 }}>
            <Space direction="vertical" style={{ width: "100%" }}>
              {(analysis.paths ?? []).slice(0, 20).map((p) => (
                <Card
                  key={p.id}
                  size="small"
                  hoverable
                  onClick={() => setSelectedPath(p)}
                  title={
                    <Space>
                      <Tag color={p.path_type === "UNKNOWN" ? "default" : "red"}>
                        {PATH_TYPE_LABELS[p.path_type] ?? p.path_type}
                      </Tag>
                      <span>评分 {p.score.toFixed(2)}</span>
                      <span>置信度 {(p.confidence * 100).toFixed(0)}%</span>
                      <span>{p.hops} 跳</span>
                      {p.terminal_type ? (
                        <Tag>{TERMINAL_LABELS[p.terminal_type] ?? p.terminal_type}</Tag>
                      ) : null}
                    </Space>
                  }
                >
                  <div>
                    {p.nodes.map((n, i) => (
                      <span key={`${p.id}-${i}`}>
                        {i > 0 && <span style={{ margin: "0 6px" }}>↓</span>}
                        <code title={n.address}>{n.address.slice(0, 10)}…{n.address.slice(-6)}</code>
                        {n.entity_name ? ` (${n.entity_name})` : ""}
                        {n.in_amount ? ` · ${n.in_amount}` : ""}
                      </span>
                    ))}
                  </div>
                </Card>
              ))}
            </Space>
            {selectedPath && (
              <Descriptions style={{ marginTop: 12 }} size="small" column={2} bordered>
                <Descriptions.Item label="路径金额">{selectedPath.total_amount}</Descriptions.Item>
                <Descriptions.Item label="路径类型">{PATH_TYPE_LABELS[selectedPath.path_type] ?? selectedPath.path_type}</Descriptions.Item>
                <Descriptions.Item label="评分">{selectedPath.score}</Descriptions.Item>
                <Descriptions.Item label="置信度">{(selectedPath.confidence * 100).toFixed(0)}%</Descriptions.Item>
                <Descriptions.Item label="证据" span={2}>
                  {(selectedPath.evidence ?? []).map((ev, i) => (
                    <div key={i}>
                      [{ev.source_name}] {ev.observation} · {(ev.confidence * 100).toFixed(0)}%
                    </div>
                  ))}
                </Descriptions.Item>
              </Descriptions>
            )}
          </Card>

          <Card title="获利归因（L0/L1 筛查）" style={{ marginBottom: 16 }}>
            <Table rowKey={(r) => r.address + r.level} size="small" dataSource={analysis.profit ?? []} columns={profitColumns} pagination={{ pageSize: 20 }} />
          </Card>

          <Card title="资金沉淀候选" style={{ marginBottom: 16 }}>
            <Table rowKey="address" size="small" dataSource={analysis.settlements ?? []} columns={settleColumns} pagination={{ pageSize: 20 }} />
          </Card>

          <Card title="交易所兑现候选" style={{ marginBottom: 16 }}>
            <Table rowKey={(r) => r.source_address + r.destination_address} size="small" dataSource={analysis.cashouts ?? []} columns={cashoutColumns} pagination={{ pageSize: 20 }} />
          </Card>

          {(analysis.round_trips?.length ?? 0) > 0 && (
            <Card title="回流检测" style={{ marginBottom: 16 }}>
              {(analysis.round_trips ?? []).map((rt, i) => (
                <div key={i}>
                  <ThunderboltOutlined /> 回流：{rt.cycle.join(" → ")} · 回流比 {(rt.return_ratio * 100).toFixed(0)}%
                </div>
              ))}
            </Card>
          )}

          {(analysis.conservation ?? []).length > 0 && (
            <Card title="资金守恒检查">
              {(analysis.conservation ?? []).slice(0, 10).map((c) => (
                <Alert
                  key={c.address}
                  style={{ marginBottom: 8 }}
                  type={c.pass ? "success" : "warning"}
                  showIcon
                  message={`${c.address} 偏差 ${(c.deviation * 100).toFixed(0)}%`}
                  description={c.reason || "流入/流出基本一致"}
                />
              ))}
            </Card>
          )}
        </>
      )}
    </div>
  );
}
