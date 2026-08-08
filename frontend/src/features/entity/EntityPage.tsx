// 实体智能（Entity Intelligence Layer V1）：地址标签解析 / 实体映射 / 证据溯源 / 调查线索。
import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Form,
  Input,
  message,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import {
  ApartmentOutlined,
  ExperimentOutlined,
  ReloadOutlined,
  TagsOutlined,
} from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import {
  CONFIDENCE_TIER_LABELS,
  ENTITY_TYPE_LABELS,
  addManualLabel,
  getEntityLeads,
  getEntityStats,
  resolveEntity,
  resolveEntityBatch,
  type EntityResolution,
  type EntityStats,
  type InvestigationLead,
} from "./entityApi";

const chainOptions = [
  { value: "bsc", label: "BNB Smart Chain" },
  { value: "eth", label: "Ethereum" },
  { value: "base", label: "Base" },
  { value: "arbitrum", label: "Arbitrum One" },
];

export default function EntityPage() {
  const [resolveForm] = Form.useForm();
  const [batchForm] = Form.useForm();
  const [labelForm] = Form.useForm();
  const [resolution, setResolution] = useState<EntityResolution | null>(null);
  const [resolving, setResolving] = useState(false);
  const [batchResults, setBatchResults] = useState<EntityResolution[]>([]);
  const [leads, setLeads] = useState<InvestigationLead[]>([]);
  const [stats, setStats] = useState<EntityStats | null>(null);
  const [leadsInv, setLeadsInv] = useState("default");

  const loadStats = () => {
    void getEntityStats().then(setStats);
  };

  useEffect(() => {
    loadStats();
    void getEntityLeads(leadsInv).then((r) => setLeads(r?.leads ?? []));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onResolve = async () => {
    const values = await resolveForm.validateFields();
    setResolving(true);
    try {
      const res = await resolveEntity(values.chain_key, values.address, values.investigation_id || undefined);
      if (res) setResolution(res);
    } finally {
      setResolving(false);
    }
  };

  const onBatch = async () => {
    const values = await batchForm.validateFields();
    const addresses = (values.addresses ?? "")
      .split(/[\n,，;；\s]+/)
      .map((s: string) => s.trim())
      .filter(Boolean);
    if (addresses.length === 0) {
      void message.info("请至少输入一个地址");
      return;
    }
    const r = await resolveEntityBatch({
      chain_key: values.chain_key,
      investigation_id: values.investigation_id || undefined,
      addresses,
    });
    if (r) {
      setBatchResults(r.results);
      void message.success(`批量解析完成：${r.total} 条`);
    }
  };

  const onLabel = async () => {
    const values = await labelForm.validateFields();
    const r = await addManualLabel({
      investigation_id: values.investigation_id || "default",
      chain_key: values.chain_key,
      address: values.address,
      label: values.label,
      reason: values.reason,
    });
    if (r) {
      void message.success("案件标签已添加（不污染全局实体）");
      labelForm.resetFields();
    }
  };

  const onLoadLeads = async () => {
    const r = await getEntityLeads(leadsInv);
    setLeads(r?.leads ?? []);
  };

  const columns: ColumnsType<EntityResolution> = [
    { title: "地址", dataIndex: "address", width: 220, render: (v) => <code>{v}</code> },
    { title: "实体", width: 200, render: (_, r) => r.entity?.name ?? "—" },
    {
      title: "类型",
      width: 140,
      render: (_, r) => ENTITY_TYPE_LABELS[r.entity?.entity_type ?? ""] ?? r.entity?.entity_type ?? "—",
    },
    {
      title: "标签",
      render: (_, r) => (
        <Space wrap size={4}>
          {(r.labels ?? []).map((l) => (
            <Tag key={l.label + l.scope}>{l.label}</Tag>
          ))}
        </Space>
      ),
    },
    {
      title: "可信度",
      width: 100,
      render: (_, r) => (
        <Tag color={r.confidence_tier === "CONFIRMED" ? "green" : r.confidence_tier === "HIGH" ? "cyan" : "default"}>
          {CONFIDENCE_TIER_LABELS[r.confidence_tier] ?? r.confidence_tier} {(r.confidence * 100).toFixed(0)}%
        </Tag>
      ),
    },
  ];

  const leadColumns: ColumnsType<InvestigationLead> = [
    { title: "类型", dataIndex: "lead_type", width: 150 },
    { title: "地址", dataIndex: "address", width: 220, render: (v) => <code>{v}</code> },
    { title: "目标实体", dataIndex: "entity_name", width: 180 },
    { title: "Token", dataIndex: "token", width: 100 },
    { title: "金额", dataIndex: "amount", width: 140 },
    {
      title: "可信度",
      width: 100,
      render: (_, r) => `${(r.confidence * 100).toFixed(0)}%`,
    },
  ];

  return (
    <div style={{ padding: 24 }}>
      <Typography.Title level={4}>
        <TagsOutlined /> 实体智能
      </Typography.Title>
      <Alert
        style={{ marginBottom: 16 }}
        type="info"
        showIcon
        message="证据边界"
        description="地址≠实体，标签≠事实，推断≠证明。所有标签均带来源、证据与置信度；行为模式只能产生候选，不自动生成现实身份结论。"
      />
      <Space wrap style={{ marginBottom: 16 }}>
        <Card size="small" style={{ minWidth: 120 }}>
          <div>实体 {stats?.entities ?? 0}</div>
        </Card>
        <Card size="small" style={{ minWidth: 120 }}>
          <div>聚类 {stats?.clusters ?? 0}</div>
        </Card>
        <Card size="small" style={{ minWidth: 120 }}>
          <div>已解析地址 {stats?.addresses ?? 0}</div>
        </Card>
        <Card size="small" style={{ minWidth: 120 }}>
          <div>证据 {stats?.evidence ?? 0}</div>
        </Card>
        <Card size="small" style={{ minWidth: 140 }}>
          <div>线索 {stats?.leads ?? 0}</div>
        </Card>
        <Card size="small" style={{ minWidth: 150 }}>
          <div>缓存命中率 {((stats?.cache_hit_rate ?? 0) * 100).toFixed(1)}%</div>
        </Card>
        <Button icon={<ReloadOutlined />} onClick={loadStats}>
          刷新统计
        </Button>
      </Space>

      <Card title="地址实体解析" style={{ marginBottom: 16 }}>
        <Form form={resolveForm} layout="inline" initialValues={{ chain_key: "bsc" }} style={{ marginBottom: 12 }}>
          <Form.Item label="链" name="chain_key">
            <Select options={chainOptions} style={{ width: 180 }} />
          </Form.Item>
          <Form.Item label="地址" name="address" rules={[{ required: true, message: "请输入 EVM 地址" }]}>
            <Input placeholder="0x…" style={{ width: 360 }} />
          </Form.Item>
          <Form.Item label="调查 ID" name="investigation_id">
            <Input placeholder="default" style={{ width: 160 }} />
          </Form.Item>
          <Button type="primary" icon={<ApartmentOutlined />} loading={resolving} onClick={() => void onResolve()}>
            解析
          </Button>
        </Form>
        {resolution && (
          <Card size="small">
            <Descriptions column={3} size="small">
              <Descriptions.Item label="地址">
                <code>{resolution.address}</code>
              </Descriptions.Item>
              <Descriptions.Item label="实体">{resolution.entity?.name ?? "未绑定实体"}</Descriptions.Item>
              <Descriptions.Item label="类型">
                {ENTITY_TYPE_LABELS[resolution.entity?.entity_type ?? ""] ?? resolution.entity?.entity_type ?? "—"}
              </Descriptions.Item>
              <Descriptions.Item label="置信度">
                <Tag
                  color={resolution.confidence_tier === "CONFIRMED" ? "green" : resolution.confidence_tier === "HIGH" ? "cyan" : "default"}
                >
                  {CONFIDENCE_TIER_LABELS[resolution.confidence_tier] ?? resolution.confidence_tier} ·{" "}
                  {(resolution.confidence * 100).toFixed(0)}%
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="缓存命中">{resolution.cache_hit ? "是" : "否"}</Descriptions.Item>
              <Descriptions.Item label="聚类">{(resolution.cluster_ids ?? []).join(", ") || "—"}</Descriptions.Item>
              <Descriptions.Item label="标签" span={3}>
                <Space wrap>
                  {(resolution.labels ?? []).map((l) => (
                    <Tag key={l.label + l.scope} color={l.scope === "INVESTIGATION" ? "orange" : "blue"}>
                      {l.label} · {l.scope === "INVESTIGATION" ? "案件" : "全局"}
                    </Tag>
                  ))}
                </Space>
              </Descriptions.Item>
            </Descriptions>
            <h4>证据溯源（{resolution.evidence?.length ?? 0}）</h4>
            <ul>
              {(resolution.evidence ?? []).map((ev) => (
                <li key={ev.evidence_id}>
                  [{ev.source_name}] {ev.observation} · 置信度 {(ev.confidence * 100).toFixed(0)}%
                  {ev.source_uri ? ` · ${ev.source_uri}` : ""}
                </li>
              ))}
            </ul>
            {(resolution.conflicts?.length ?? 0) > 0 && (
              <Alert
                type="warning"
                showIcon
                message="标签冲突（未静默覆盖）"
                description={resolution.conflicts?.map((c) => `${c.entity_a} vs ${c.entity_b}`).join("；")}
              />
            )}
          </Card>
        )}
      </Card>

      <Card title="批量解析（单次 ≤ 10,000 地址）" style={{ marginBottom: 16 }}>
        <Form form={batchForm} layout="vertical" initialValues={{ chain_key: "bsc" }}>
          <Space size={12}>
            <Form.Item label="链" name="chain_key">
              <Select options={chainOptions} style={{ width: 180 }} />
            </Form.Item>
            <Form.Item label="调查 ID" name="investigation_id">
              <Input placeholder="default" style={{ width: 160 }} />
            </Form.Item>
          </Space>
          <Form.Item label="地址列表（每行一个 / 逗号分隔）" name="addresses" rules={[{ required: true }]}>
            <Input.TextArea rows={5} placeholder={"0x…\n0x…"} />
          </Form.Item>
          <Button type="primary" icon={<ExperimentOutlined />} onClick={() => void onBatch()}>
            批量解析
          </Button>
        </Form>
        {batchResults.length > 0 && (
          <Table
            style={{ marginTop: 12 }}
            rowKey="address"
            size="small"
            dataSource={batchResults}
            columns={columns}
            pagination={{ pageSize: 20 }}
          />
        )}
      </Card>

      <Card
        title="调查实体线索"
        style={{ marginBottom: 16 }}
        extra={
          <Space>
            <Input value={leadsInv} onChange={(e) => setLeadsInv(e.target.value)} placeholder="调查 ID" style={{ width: 160 }} />
            <Button onClick={() => void onLoadLeads()}>加载</Button>
          </Space>
        }
      >
        <Table rowKey="id" size="small" dataSource={leads} columns={leadColumns} pagination={{ pageSize: 20 }} />
      </Card>

      <Card title="案件自定义标签（仅当前调查可见）">
        <Form form={labelForm} layout="inline" initialValues={{ chain_key: "bsc", investigation_id: "default" }}>
          <Form.Item label="调查 ID" name="investigation_id">
            <Input style={{ width: 140 }} />
          </Form.Item>
          <Form.Item label="链" name="chain_key">
            <Select options={chainOptions} style={{ width: 140 }} />
          </Form.Item>
          <Form.Item label="地址" name="address" rules={[{ required: true }]}>
            <Input style={{ width: 300 }} />
          </Form.Item>
          <Form.Item label="标签" name="label" rules={[{ required: true }]}>
            <Input style={{ width: 160 }} />
          </Form.Item>
          <Form.Item label="原因" name="reason">
            <Input style={{ width: 200 }} />
          </Form.Item>
          <Button type="primary" icon={<TagsOutlined />} onClick={() => void onLabel()}>
            添加
          </Button>
        </Form>
      </Card>
    </div>
  );
}

