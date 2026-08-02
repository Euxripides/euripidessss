// 调查证据查看器（V2.1 设计 §8：EvidenceViewer）
// 展示调查证据链：发现（交易/地址/风险/获利）、交易哈希、路径、可信度。
import { useEffect, useState } from "react";
import { List, Progress, Space, Table, Tag, Typography } from "antd";
import { DetailPanel } from "../../design-system/DesignSystem";
import { EVIDENCE_TYPE_LABEL, getInvestigationEvidence, type Evidence } from "./intelligenceApi";

const { Text } = Typography;

const EVIDENCE_COLOR: Record<string, string> = {
  TRANSACTION: "green",
  ADDRESS: "blue",
  TIME: "cyan",
  PATH: "purple",
  RISK: "red",
  PROFIT: "gold",
};

function confidenceColor(c: number): string {
  if (c >= 0.8) return "#52c41a";
  if (c >= 0.6) return "#faad14";
  return "#999";
}

// EvidenceViewer 展示调查证据链（独立请求，轮询刷新直到调查完成）。
export function EvidenceViewer({ investigationId, done }: { investigationId: string; done: boolean }) {
  const [evidence, setEvidence] = useState<Evidence[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      setLoading(true);
      try {
        const data = await getInvestigationEvidence(investigationId);
        if (!cancelled && data) setEvidence(data.evidence ?? []);
      } catch {
        // 静默：调查详情页已提示
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    load();
    if (!done) {
      const timer = setInterval(load, 4000);
      return () => {
        cancelled = true;
        clearInterval(timer);
      };
    }
    return () => {
      cancelled = true;
    };
  }, [investigationId, done]);

  const columns = [
    {
      title: "类型",
      dataIndex: "evidence_type",
      key: "evidence_type",
      width: 110,
      render: (v: string) => <Tag color={EVIDENCE_COLOR[v] ?? "default"}>{EVIDENCE_TYPE_LABEL[v as keyof typeof EVIDENCE_TYPE_LABEL] ?? v}</Tag>,
    },
    {
      title: "发现",
      dataIndex: "detail",
      key: "detail",
      ellipsis: true,
      render: (v: string) => <Text>{v}</Text>,
    },
    {
      title: "交易",
      dataIndex: "tx_hash",
      key: "tx_hash",
      ellipsis: true,
      render: (v?: string) => (v ? <Text code>{v.slice(0, 12)}…</Text> : "-"),
    },
    {
      title: "地址",
      dataIndex: "address",
      key: "address",
      ellipsis: true,
      render: (v?: string) => (v ? `${v.slice(0, 10)}…` : "-"),
    },
    {
      title: "Token/金额",
      key: "token",
      width: 150,
      render: (_: unknown, ev: Evidence) => (
        <Text>{ev.token ? `${ev.token} ${ev.amount ?? ""}` : ev.amount ?? "-"}</Text>
      ),
    },
    {
      title: "可信度",
      dataIndex: "confidence",
      key: "confidence",
      width: 140,
      render: (c: number) => (
        <Progress
          percent={Math.round((c ?? 0) * 100)}
          size="small"
          strokeColor={confidenceColor(c ?? 0)}
          format={(p) => `${p}%`}
        />
      ),
    },
  ];

  return (
    <DetailPanel size="small" title={`证据链（${evidence.length} 条）`} description="每条发现均附带交易/地址/时间证据与可信度">
      <Table
        size="small"
        rowKey={(ev) => ev.id ?? `${ev.evidence_type}-${ev.tx_hash}-${ev.detail}`}
        columns={columns}
        dataSource={evidence}
        loading={loading}
        pagination={{ pageSize: 8, size: "small" }}
        scroll={{ x: 760 }}
        locale={{ emptyText: <Text type="secondary">暂无证据（调查完成后展示完整证据链）</Text> }}
      />
      {evidence.length > 0 && (
        <Space wrap style={{ marginTop: 8 }}>
          <Text type="secondary" style={{ fontSize: 12 }}>
            证据来源：资金路径、风险模式、观察结果与获利检测；链上交易证据可信度 ≥85%
          </Text>
        </Space>
      )}
    </DetailPanel>
  );
}

// EvidenceList 紧凑模式：直接从调查对象读取证据（SSE 实时，无独立请求）。
export function EvidenceList({ evidence }: { evidence?: Evidence[] }) {
  if (!evidence || evidence.length === 0) return null;
  return (
    <List
      size="small"
      dataSource={evidence.slice(0, 8)}
      renderItem={(ev) => (
        <List.Item>
          <Space wrap>
            <Tag color={EVIDENCE_COLOR[ev.evidence_type] ?? "default"}>
              {EVIDENCE_TYPE_LABEL[ev.evidence_type as keyof typeof EVIDENCE_TYPE_LABEL] ?? ev.evidence_type}
            </Tag>
            <Text code>{ev.tx_hash ? ev.tx_hash.slice(0, 12) + "…" : ev.detail.slice(0, 40)}</Text>
            <Text type="secondary">可信度 {(ev.confidence ?? 0) * 100}%</Text>
          </Space>
        </List.Item>
      )}
    />
  );
}
