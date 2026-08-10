import {
  ApiOutlined,
  ClockCircleOutlined,
  CodeOutlined,
  DatabaseOutlined,
  DollarOutlined,
  FileUnknownOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  TagsOutlined,
} from "@ant-design/icons";
import { Alert, Button, Empty, Progress, Select, Skeleton, Table, Tag, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { PageHeader, Section } from "../../design-system/DesignSystem";
import {
  fetchDataQuality,
  type DataQualityChain,
  type DataQualityResponse,
  type QualityItem,
  type QualityMetrics,
  type QualitySection,
  type QualitySectionKey,
} from "./qualityApi";
import "./data-quality.css";

const CHAIN_OPTIONS: ReadonlyArray<{ value: DataQualityChain; label: string }> = [
  { value: "bsc", label: "BNB Smart Chain" },
  { value: "eth", label: "Ethereum" },
  { value: "base", label: "Base" },
  { value: "arbitrum", label: "Arbitrum One" },
];

interface SectionDefinition {
  key: QualitySectionKey;
  title: string;
  description: string;
  icon: ReactNode;
  issueMetric: keyof Pick<QualityMetrics, "invalid" | "unknown" | "unpriced" | "unlabeled" | "decode_failed">;
  issueLabel: string;
}

const SECTION_DEFINITIONS: readonly SectionDefinition[] = [
  {
    key: "datasets",
    title: "Dataset 数据集",
    description: "规范数据集的行数、覆盖率、字段完整度与无效记录。",
    icon: <DatabaseOutlined />,
    issueMetric: "invalid",
    issueLabel: "无效记录",
  },
  {
    key: "tokens",
    title: "Token 资产",
    description: "Token 元数据覆盖、字段完整度与未识别资产。",
    icon: <TagsOutlined />,
    issueMetric: "unknown",
    issueLabel: "未知资产",
  },
  {
    key: "contracts",
    title: "Contract 合约",
    description: "合约识别与标签覆盖情况。",
    icon: <ApiOutlined />,
    issueMetric: "unlabeled",
    issueLabel: "未标注合约",
  },
  {
    key: "decoders",
    title: "Decoder 解码",
    description: "调用和事件解码的覆盖率与失败记录。",
    icon: <CodeOutlined />,
    issueMetric: "decode_failed",
    issueLabel: "解码失败",
  },
  {
    key: "prices",
    title: "Price 价格",
    description: "法币计价覆盖、完整度与未定价记录。",
    icon: <DollarOutlined />,
    issueMetric: "unpriced",
    issueLabel: "未定价记录",
  },
] as const;

export interface DataQualityPageProps {
  initialChain?: DataQualityChain;
}

export default function DataQualityPage({ initialChain = "bsc" }: DataQualityPageProps) {
  const [chain, setChain] = useState<DataQualityChain>(initialChain);
  const [data, setData] = useState<DataQualityResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [requestVersion, setRequestVersion] = useState(0);

  const refresh = useCallback(() => setRequestVersion((version) => version + 1), []);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);

    void fetchDataQuality(chain)
      .then((payload) => {
        if (active) setData(payload);
      })
      .catch((reason: unknown) => {
        if (!active) return;
        setData(null);
        setError(reason instanceof Error ? reason.message : "数据质量读取失败");
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
    };
  }, [chain, requestVersion]);

  const hasRows = useMemo(
    () => data ? SECTION_DEFINITIONS.some(({ key }) => data.sections[key].rows > 0 || data.sections[key].items.length > 0) : false,
    [data],
  );

  return (
    <div className="ds-page data-quality-page">
      <PageHeader
        title="Data Quality 数据质量"
        description="审计 Canonical Data Asset Layer 的覆盖、完整性和未解析数据，质量指标不替代原始证据核验。"
        actions={(
          <div className="data-quality-actions">
            <Select<DataQualityChain>
              aria-label="选择区块链网络"
              value={chain}
              options={[...CHAIN_OPTIONS]}
              onChange={setChain}
              disabled={loading}
              popupMatchSelectWidth={false}
            />
            <Button icon={<ReloadOutlined />} onClick={refresh} loading={loading}>刷新</Button>
          </div>
        )}
      />

      {error ? (
        <Alert
          className="data-quality-alert"
          type="error"
          showIcon
          message="数据质量加载失败"
          description={error}
          action={<Button size="small" onClick={refresh}>重试</Button>}
        />
      ) : null}

      {loading && !data ? <QualityPageSkeleton /> : null}

      {!loading && !error && data && !hasRows ? (
        <Section title="暂无质量数据" description="当前网络尚未生成质量快照。">
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="完成 Canonical 数据写入后刷新此页面" />
        </Section>
      ) : null}

      {data && hasRows ? (
        <>
          <div className="data-quality-snapshot" role="status">
            <SafetyCertificateOutlined />
            <span>{data.chain || chain.toUpperCase()} · Chain ID {data.chain_id || "--"}</span>
            <span className="data-quality-snapshot-time">快照时间 {formatDateTime(data.generated_at)}</span>
          </div>
          <div className="data-quality-sections" aria-busy={loading}>
            {SECTION_DEFINITIONS.map((definition) => (
              <QualitySectionPanel
                key={definition.key}
                definition={definition}
                section={data.sections[definition.key]}
                loading={loading}
              />
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}

function QualitySectionPanel({
  definition,
  section,
  loading,
}: {
  definition: SectionDefinition;
  section: QualitySection;
  loading: boolean;
}) {
  const issueValue = section[definition.issueMetric];
  const columns = useMemo<ColumnsType<QualityItem>>(() => createColumns(definition), [definition]);

  return (
    <Section
      className="data-quality-section"
      title={definition.title}
      description={definition.description}
      extra={<Tag color={qualityTone(section)}>{qualityLabel(section)}</Tag>}
    >
      <div className="data-quality-metrics">
        <QualityCount icon={definition.icon} label="数据行" value={section.rows} />
        <QualityRate label="覆盖率" value={section.coverage} />
        <QualityRate label="完整度" value={section.completeness} />
        <QualityCount icon={<FileUnknownOutlined />} label={definition.issueLabel} value={issueValue} danger={issueValue > 0} />
        <QualityCount icon={<ClockCircleOutlined />} label="最后更新" value={formatDateTime(section.last_updated)} compact />
      </div>
      {section.items.length > 0 ? (
        <Table<QualityItem>
          className="data-quality-table"
          size="small"
          loading={loading}
          rowKey="key"
          columns={columns}
          dataSource={[...section.items]}
          pagination={section.items.length > 8 ? { pageSize: 8, showSizeChanger: false } : false}
          scroll={{ x: 760 }}
        />
      ) : (
        <div className="data-quality-no-breakdown">暂无分项明细</div>
      )}
    </Section>
  );
}

function QualityCount({
  icon,
  label,
  value,
  danger = false,
  compact = false,
}: {
  icon: ReactNode;
  label: string;
  value: number | string;
  danger?: boolean;
  compact?: boolean;
}) {
  return (
    <div className={`data-quality-metric${danger ? " is-danger" : ""}`}>
      <span className="data-quality-metric-icon" aria-hidden="true">{icon}</span>
      <span>
        <small>{label}</small>
        <strong className={compact ? "is-compact" : undefined}>{typeof value === "number" ? formatInteger(value) : value}</strong>
      </span>
    </div>
  );
}

function QualityRate({ label, value }: { label: string; value: number | null }) {
  const percent = normalizePercent(value);
  return (
    <div className="data-quality-rate">
      <span><small>{label}</small><strong>{percent === null ? "--" : `${percent.toFixed(1)}%`}</strong></span>
      <Progress
        percent={percent ?? 0}
        showInfo={false}
        size="small"
        status={percent !== null && percent < 80 ? "exception" : "normal"}
        aria-label={`${label} ${percent === null ? "未知" : `${percent.toFixed(1)}%`}`}
      />
    </div>
  );
}

function QualityPageSkeleton() {
  return (
    <div className="data-quality-sections" aria-label="正在加载数据质量">
      {[0, 1, 2].map((key) => (
        <section className="ds-section data-quality-section data-quality-skeleton" key={key}>
          <Skeleton active paragraph={{ rows: 3 }} />
        </section>
      ))}
    </div>
  );
}

function createColumns(definition: SectionDefinition): ColumnsType<QualityItem> {
  return [
    {
      title: "对象",
      dataIndex: "name",
      key: "name",
      ellipsis: { showTitle: false },
      render: (name: string, row) => (
        <Tooltip title={row.description || name}><span className="data-quality-object">{name || row.key}</span></Tooltip>
      ),
    },
    { title: "Rows", dataIndex: "rows", key: "rows", width: 112, align: "right", render: formatInteger },
    { title: "Coverage", dataIndex: "coverage", key: "coverage", width: 112, align: "right", render: formatPercent },
    { title: "Completeness", dataIndex: "completeness", key: "completeness", width: 128, align: "right", render: formatPercent },
    {
      title: definition.issueLabel,
      dataIndex: definition.issueMetric,
      key: definition.issueMetric,
      width: 116,
      align: "right",
      render: (value: number) => value > 0 ? <span className="data-quality-bad-value">{formatInteger(value)}</span> : "0",
    },
    { title: "Last Updated", dataIndex: "last_updated", key: "last_updated", width: 164, render: formatDateTime },
  ];
}

function normalizePercent(value: number | null): number | null {
  if (value === null || !Number.isFinite(value)) return null;
  const percent = value >= 0 && value <= 1 ? value * 100 : value;
  return Math.min(100, Math.max(0, percent));
}

function formatPercent(value: number | null): string {
  const percent = normalizePercent(value);
  return percent === null ? "--" : `${percent.toFixed(1)}%`;
}

function formatInteger(value: number): string {
  return Number.isFinite(value) ? Math.max(0, value).toLocaleString("zh-CN") : "--";
}

function formatDateTime(value: string): string {
  if (!value) return "--";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false });
}

function qualityTone(section: QualitySection): "success" | "warning" | "error" | "default" {
  const coverage = normalizePercent(section.coverage);
  const completeness = normalizePercent(section.completeness);
  const issues = section.invalid + section.unknown + section.unpriced + section.unlabeled + section.decode_failed;
  if (coverage === null || completeness === null) return "default";
  if (coverage < 80 || completeness < 80) return "error";
  if (issues > 0 || coverage < 95 || completeness < 95) return "warning";
  return "success";
}

function qualityLabel(section: QualitySection): string {
  const tone = qualityTone(section);
  if (tone === "success") return "质量良好";
  if (tone === "warning") return "需要关注";
  if (tone === "error") return "质量异常";
  return "待评估";
}
