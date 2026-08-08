// 调查报告中心（Investigation Report Engine V2）。
import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Input,
  message,
  Select,
  Space,
  Table,
  Tag,
  Timeline,
  Typography,
} from "antd";
import { DownloadOutlined, FileTextOutlined, ReloadOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import {
  createReport,
  diffReports,
  exportReport,
  getReport,
  listReports,
  reportStatusAction,
  signReport,
  polishReport,
  type Finding,
  type ReportDetail,
  type ReportEvidence,
  type ReportListEntry,
  type TimelineEvent,
} from "./reportApi";

const STATUS_LABELS: Record<string, string> = {
  DRAFT: "草稿",
  GENERATING: "生成中",
  READY: "就绪",
  PARTIAL: "部分数据",
  REVIEWED: "已审阅",
  LOCKED: "已锁定",
  SUPERSEDED: "已取代",
  OUTDATED: "已过期",
};

export default function ReportPage() {
  const [invId, setInvId] = useState("default");
  const [reports, setReports] = useState<ReportListEntry[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<ReportDetail | null>(null);
  const [generating, setGenerating] = useState(false);
  const [diffText, setDiffText] = useState<string | null>(null);
  const [language, setLanguage] = useState("zh");
  const [institution, setInstitution] = useState("");

  const load = async (id: string, keepSelected = false) => {
    const list = await listReports(id);
    setReports(list ?? []);
    if (!keepSelected && list && list.length > 0) {
      setSelected(list[0].id);
      const d = await getReport(id, list[0].id);
      setDetail(d);
    }
  };

  useEffect(() => {
    void load(invId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onGenerate = async () => {
    setGenerating(true);
    try {
      const r = await createReport(invId, 4, language, institution);
      if (r) {
        void message.success(`报告 v${r.version} 已生成${r.cache_hit ? "（资金流缓存命中）" : ""}`);
        await load(invId);
      }
    } finally {
      setGenerating(false);
    }
  };

  const onSelect = async (reportId: string) => {
    setSelected(reportId);
    const d = await getReport(invId, reportId);
    setDetail(d);
    setDiffText(null);
  };

  const onDiff = async () => {
    if (reports.length < 2) {
      void message.info("至少需要两个版本");
      return;
    }
    const d = await diffReports(invId, reports[reports.length - 1].id, reports[0].id);
    if (d) {
      const changed = Object.entries(d.changed_metrics ?? {})
        .map(([k, v]) => `${k}: ${v[0]} → ${v[1]}`)
        .join("；");
      setDiffText(`新增 ${d.new_findings?.length ?? 0} 条结论；${changed || "无指标变化"}`);
    }
  };

  const onStatusAction = async (reportId: string, action: "lock" | "review" | "outdated") => {
    const r = await reportStatusAction(invId, reportId, action);
    if (r) {
      void message.success(r.detail ?? "状态已更新");
      await load(invId, true);
      const d = await getReport(invId, reportId);
      if (d) setDetail(d);
    }
  };

  const onSign = async (reportId: string) => {
    const r = await signReport(invId, reportId);
    if (r?.signature) {
      void message.success(`报告已签名：${r.signature.hash.slice(0, 16)}…`);
      const d = await getReport(invId, reportId);
      if (d) setDetail(d);
    }
  };

  const onPolish = async (reportId: string) => {
    const r = await polishReport(invId, reportId, "summary");
    if (r?.narrative) {
      void message.success("摘要已润色（数字一致性校验通过）");
      const d = await getReport(invId, reportId);
      if (d) setDetail(d);
    }
  };

  const evidenceColumns: ColumnsType<ReportEvidence> = [
    { title: "Evidence ID", dataIndex: "id", width: 220, render: (v) => <code>{v}</code> },
    { title: "类型", dataIndex: "type", width: 140 },
    { title: "地址", dataIndex: "address", width: 220, render: (v) => (v ? <code>{v}</code> : "—") },
    { title: "TxHash", dataIndex: "tx_hash", width: 180, render: (v) => (v ? <code>{v}</code> : "—") },
    { title: "数据集", dataIndex: "dataset_id", width: 120 },
    { title: "认证", dataIndex: "certification", width: 110 },
    { title: "证据哈希", dataIndex: "evidence_hash", width: 160, render: (v) => <code>{v?.slice(0, 16)}…</code> },
  ];

  const findingColumns: ColumnsType<Finding> = [
    { title: "类型", dataIndex: "finding_type", width: 180, render: (v) => <Tag>{v}</Tag> },
    { title: "结论", dataIndex: "statement" },
    {
      title: "置信度",
      width: 90,
      render: (_, r) => `${(r.confidence * 100).toFixed(0)}%`,
    },
    {
      title: "证据",
      width: 120,
      render: (_, r) => <code>{(r.evidence_ids ?? []).join(", ")}</code>,
    },
  ];

  const timelineItems = (detail?.timeline ?? []).slice(0, 120).map((ev: TimelineEvent) => ({
    color: ev.type === "EXCHANGE_DEPOSIT" ? "red" : ev.type === "SETTLEMENT" ? "orange" : "blue",
    children: (
      <span>
        {ev.timestamp ? new Date(ev.timestamp).toISOString().slice(0, 10) : "—"} · {ev.type} · {ev.summary}
        {ev.amount ? ` · ${ev.amount}` : ""}
        {ev.tx_hash ? ` · ${ev.tx_hash}` : ""}
      </span>
    ),
  }));

  return (
    <div style={{ padding: 24 }}>
      <Typography.Title level={4}>
        <FileTextOutlined /> 调查报告
      </Typography.Title>
      <Alert
        style={{ marginBottom: 16 }}
        type="info"
        showIcon
        message="报告边界"
        description="所有结论均来自结构化计算并带 Evidence ID 与哈希；数据不完整时报告标记 PARTIAL 并披露缺口；报告不替代正式司法/调证文书。"
      />
      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          value={invId}
          onChange={(v) => {
            setInvId(v);
            void load(v);
          }}
          options={[{ value: "default", label: "default" }, { value: "smoke-inv-1", label: "smoke-inv-1" }]}
          style={{ width: 180 }}
          placeholder="调查 ID"
          showSearch
        />
        <Button type="primary" icon={<ReloadOutlined />} loading={generating} onClick={() => void onGenerate()}>
          生成综合报告
        </Button>
        <Select value={language} onChange={setLanguage} style={{ width: 110 }} options={[{ value: "zh", label: "中文" }, { value: "en", label: "English" }]} />
        <Input value={institution} onChange={(e) => setInstitution(e.target.value)} placeholder="机构抬头（可选）" style={{ width: 180 }} />
        <Button disabled={reports.length < 2} onClick={() => void onDiff()}>
          版本差异
        </Button>
        {diffText && <Tag color="purple">{diffText}</Tag>}
      </Space>

      <Card title="报告版本" style={{ marginBottom: 16 }}>
        <Table
          rowKey="id"
          size="small"
          dataSource={reports}
          pagination={false}
          onRow={(r) => ({ onClick: () => void onSelect(r.id) })}
          rowClassName={(r) => (r.id === selected ? "ant-table-row-selected" : "")}
          columns={[
            { title: "版本", dataIndex: "version", width: 80 },
            { title: "标题", dataIndex: "title" },
            { title: "状态", dataIndex: "status", width: 120, render: (v) => <Tag color={v === "PARTIAL" ? "orange" : "green"}>{STATUS_LABELS[v] ?? v}</Tag> },
            { title: "路径", dataIndex: ["summary", "key_paths"], width: 80 },
            { title: "兑现", dataIndex: ["summary", "cashouts"], width: 80 },
            { title: "沉淀", dataIndex: ["summary", "settlements"], width: 80 },
            { title: "获利", dataIndex: ["summary", "profit_addresses"], width: 80 },
            { title: "缺口", dataIndex: ["summary", "known_gaps"], width: 80 },
            {
              title: "操作",
              width: 190,
              render: (_, r) => (
                <Space size={4}>
                  <Button size="small" onClick={() => void onStatusAction(r.id, "review")}>
                    审阅
                  </Button>
                  <Button size="small" onClick={() => void onStatusAction(r.id, "lock")}>
                    锁定
                  </Button>
                  <Button size="small" onClick={() => void onStatusAction(r.id, "outdated")}>
                    过期
                  </Button>
                </Space>
              ),
            },
          ]}
        />
      </Card>

      {detail && (
        <>
          <Card
            style={{ marginBottom: 16 }}
            title={`${detail.report.title} · 数据完整性`}
            extra={
              <Space>
                {(["json", "xlsx", "docx", "pdf", "case_package"] as const).map((fmt) => (
                  <Button
                    key={fmt}
                    size="small"
                    icon={<DownloadOutlined />}
                    onClick={() => {
                      void exportReport(invId, detail.report.id, fmt).catch(() => void message.error("导出失败"));
                    }}
                  >
                    {fmt.toUpperCase()}
                  </Button>
                ))}
              </Space>
            }
          >
            <Descriptions size="small" column={3} bordered>
              <Descriptions.Item label="状态">{STATUS_LABELS[detail.report.status] ?? detail.report.status}</Descriptions.Item>
              <Descriptions.Item label="覆盖分">{(detail.report.certification.coverage_score * 100).toFixed(1)}%</Descriptions.Item>
              <Descriptions.Item label="已知缺口">{detail.report.certification.known_gap_count}</Descriptions.Item>
              <Descriptions.Item label="快照">{detail.snapshot.id}</Descriptions.Item>
              <Descriptions.Item label="清单哈希">
                <code>{detail.snapshot.dataset_manifest_hash.slice(0, 16)}…</code>
              </Descriptions.Item>
              <Descriptions.Item label="模板版本">{detail.snapshot.report_template_version}</Descriptions.Item>
              <Descriptions.Item label="语言">{detail.report.language ?? "zh"}</Descriptions.Item>
              <Descriptions.Item label="机构">{detail.report.institution || "—"}</Descriptions.Item>
              <Descriptions.Item label="签名">
                {detail.report.signature ? <code>{detail.report.signature.hash.slice(0, 24)}…</code> : "未签名"}
              </Descriptions.Item>
            </Descriptions>
            <Space style={{ marginTop: 8 }}>
              <Button size="small" onClick={() => void onSign(detail.report.id)}>签名</Button>
              <Button size="small" onClick={() => void onPolish(detail.report.id)}>LLM 润色摘要</Button>
            </Space>
            <Space wrap style={{ marginTop: 8 }}>
              {detail.report.certification.dataset_statuses.map((c) => (
                <Tag key={c.dataset} color={c.coverage >= 1 ? "green" : "orange"}>
                  {c.dataset} {((c.coverage ?? 0) * 100).toFixed(2)}% · {c.certification}
                </Tag>
              ))}
            </Space>
          </Card>

          <Card title="报告章节" style={{ marginBottom: 16 }}>
            {detail.report.sections.map((sec) => (
              <Card key={sec.id} size="small" title={sec.title} style={{ marginBottom: 12 }}>
                <p>{sec.narrative}</p>
                {(sec.findings ?? []).length > 0 && (
                  <Table
                    rowKey="id"
                    size="small"
                    pagination={false}
                    dataSource={sec.findings ?? []}
                    columns={findingColumns}
                  />
                )}
              </Card>
            ))}
          </Card>

          <Card title="证据时间线" style={{ marginBottom: 16 }}>
            <Timeline items={timelineItems} />
          </Card>

          <Card title="证据清单" style={{ marginBottom: 16 }}>
            <Table rowKey="id" size="small" dataSource={detail.evidence} columns={evidenceColumns} pagination={{ pageSize: 20 }} />
          </Card>
        </>
      )}
    </div>
  );
}
