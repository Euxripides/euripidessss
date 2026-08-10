import {
  ApartmentOutlined,
  DollarOutlined,
  FileUnknownOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SwapOutlined,
} from "@ant-design/icons";
import { Alert, Button, Card, Empty, Select, Skeleton, Statistic, Table, Tag, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { PageHeader, Section } from "../../design-system/DesignSystem";
import {
  fetchFinancialQuality,
  type Coverage,
  type FinancialQualityChain,
  type FinancialQualityReport,
  type FinancialQualityWindow,
} from "./financialQualityApi";
import "./financial-quality.css";

const CHAIN_OPTIONS: ReadonlyArray<{ value: FinancialQualityChain; label: string }> = [
  { value: "bsc", label: "BNB Smart Chain" },
  { value: "eth", label: "Ethereum" },
  { value: "base", label: "Base" },
  { value: "arbitrum", label: "Arbitrum One" },
];

const WINDOW_OPTIONS: ReadonlyArray<{ value: FinancialQualityWindow; label: string }> = [
  { value: "24H", label: "最近 24 小时" },
  { value: "7D", label: "最近 7 天" },
  { value: "30D", label: "最近 30 天" },
  { value: "90D", label: "最近 90 天" },
  { value: "1Y", label: "最近 1 年" },
  { value: "ALL", label: "全部时间" },
];

interface QualityRow {
  key: string;
  metric: string;
  evidence: string;
  coverage: Coverage;
  unknown: number;
}

export interface FinancialQualityPageProps {
  initialChain?: FinancialQualityChain;
  initialWindow?: FinancialQualityWindow;
}

export default function FinancialQualityPage({
  initialChain = "bsc",
  initialWindow = "30D",
}: FinancialQualityPageProps) {
  const [chain, setChain] = useState<FinancialQualityChain>(initialChain);
  const [window, setWindow] = useState<FinancialQualityWindow>(initialWindow);
  const [report, setReport] = useState<FinancialQualityReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [requestVersion, setRequestVersion] = useState(0);

  const refresh = useCallback(() => setRequestVersion((version) => version + 1), []);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);
    void fetchFinancialQuality(chain, window)
      .then((payload) => {
        if (active) setReport(payload);
      })
      .catch((reason: unknown) => {
        if (!active) return;
        setReport(null);
        setError(reason instanceof Error ? reason.message : "金融质量读取失败");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [chain, requestVersion, window]);

  const rows = useMemo(() => (report ? qualityRows(report) : []), [report]);
  const columns = useMemo<ColumnsType<QualityRow>>(() => [
    { title: "指标", dataIndex: "metric", key: "metric" },
    { title: "证据范围", dataIndex: "evidence", key: "evidence", responsive: ["md"] },
    {
      title: "覆盖率",
      key: "coverage",
      render: (_, row) => <CoverageValue coverage={row.coverage} />,
    },
    {
      title: "未知 / 缺失",
      dataIndex: "unknown",
      key: "unknown",
      render: (value: number) => value.toLocaleString("zh-CN"),
    },
  ], []);

  return (
    <div className="ds-page financial-quality-page">
      <PageHeader
        title="Financial Quality 金融数据质量"
        description="核验历史价格、成本基础、DEX/Bridge 语义与实体证据覆盖；未知值保持未知，不按 0 参与金融结论。"
        actions={(
          <div className="financial-quality-actions">
            <Select<FinancialQualityChain>
              aria-label="选择区块链网络"
              value={chain}
              options={[...CHAIN_OPTIONS]}
              onChange={setChain}
              disabled={loading}
              popupMatchSelectWidth={false}
            />
            <Select<FinancialQualityWindow>
              aria-label="选择统计时间窗口"
              value={window}
              options={[...WINDOW_OPTIONS]}
              onChange={setWindow}
              disabled={loading}
              popupMatchSelectWidth={false}
            />
            <Button icon={<ReloadOutlined />} onClick={refresh} loading={loading}>刷新</Button>
          </div>
        )}
      />

      {error ? (
        <Alert
          type="error"
          showIcon
          message="金融质量加载失败"
          description={error}
          action={<Button size="small" onClick={refresh}>重试</Button>}
        />
      ) : null}

      {loading && !report ? <QualitySkeleton /> : null}

      {!loading && !error && !report ? (
        <Section title="暂无金融质量快照">
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="完成 Canonical 金融数据写入后刷新此页面" />
        </Section>
      ) : null}

      {report ? (
        <>
          <div className="financial-quality-snapshot" role="status">
            <SafetyCertificateOutlined />
            <span>Chain ID {report.chain_id} · {report.window.name}</span>
            <span>快照 {formatDateTime(report.generated_at)}</span>
          </div>

          <div className="financial-quality-cards" aria-busy={loading}>
            <MetricCard title="历史价格覆盖" value={report.price.coverage} icon={<DollarOutlined />} />
            <MetricCard title="Stablecoin 回退比例" value={report.price.fallback_ratio} icon={<DollarOutlined />} warning />
            <CountCard title="缺失价格" value={report.price.missing_price} icon={<FileUnknownOutlined />} warning />
            <CountCard title="未知成本基础" value={report.cost_basis.unknown_cost_basis} icon={<FileUnknownOutlined />} warning />
            <MetricCard title="DEX 解码覆盖" value={report.dex_decode.coverage} icon={<SwapOutlined />} />
            <MetricCard title="Bridge 解码覆盖" value={report.bridge_decode.coverage} icon={<SwapOutlined />} />
            <MetricCard title="实体覆盖" value={report.entity.coverage} icon={<ApartmentOutlined />} />
          </div>

          {!report.cost_basis.coverage.available ? (
            <Alert
              className="financial-quality-notice"
              type="warning"
              showIcon
              message="成本基础覆盖当前不可计算"
              description="系统尚无独立的 Canonical 成本批次台账。转账不会被推断为买卖，PnL 也不会显示虚假的 0 成本或 0% 覆盖。"
            />
          ) : null}

          <Section
            title="质量证据明细"
            description="百分比以完整候选集为分母；指标不可用时显示“未知”，不会自动替换成 0%。"
            extra={<Tag>{report.last_updated ? `事实更新 ${formatDateTime(report.last_updated)}` : "事实更新时间未知"}</Tag>}
          >
            <Table<QualityRow>
              rowKey="key"
              size="small"
              columns={columns}
              dataSource={rows}
              pagination={false}
              loading={loading}
              scroll={{ x: 720 }}
            />
          </Section>
        </>
      ) : null}
    </div>
  );
}

function MetricCard({ title, value, icon, warning = false }: { title: string; value: Coverage; icon: ReactNode; warning?: boolean }) {
  const display = formatCoverage(value);
  return (
    <Card size="small" className="financial-quality-card">
      <Statistic
        title={<span>{icon} {title}</span>}
        value={display}
        valueStyle={warning && value.available && value.numerator > 0 ? { color: "#ad6800" } : undefined}
      />
      <div className="financial-quality-card-foot">
        {value.available ? `${value.numerator.toLocaleString("zh-CN")} / ${value.denominator.toLocaleString("zh-CN")}` : "证据不足，指标不可用"}
      </div>
    </Card>
  );
}

function CountCard({ title, value, icon, warning = false }: { title: string; value: number; icon: ReactNode; warning?: boolean }) {
  return (
    <Card size="small" className="financial-quality-card">
      <Statistic
        title={<span>{icon} {title}</span>}
        value={value}
        valueStyle={warning && value > 0 ? { color: "#ad6800" } : undefined}
      />
      <div className="financial-quality-card-foot">记录数</div>
    </Card>
  );
}

function CoverageValue({ coverage }: { coverage: Coverage }) {
  if (!coverage.available || coverage.percentage === null) {
    return <Tooltip title="当前证据不足，不能计算为 0%"><Tag>未知</Tag></Tooltip>;
  }
  const color = coverage.percentage >= 95 ? "green" : coverage.percentage >= 80 ? "gold" : "red";
  return <Tag color={color}>{coverage.percentage.toFixed(1)}%</Tag>;
}

function qualityRows(report: FinancialQualityReport): QualityRow[] {
  return [
    { key: "price", metric: "历史价格覆盖", evidence: report.price.coverage.scope, coverage: report.price.coverage, unknown: report.price.missing_price },
    { key: "fallback", metric: "Stablecoin 回退比例", evidence: report.price.fallback_ratio.scope, coverage: report.price.fallback_ratio, unknown: report.price.fallback_price },
    { key: "cost", metric: "成本基础覆盖", evidence: report.cost_basis.coverage.scope, coverage: report.cost_basis.coverage, unknown: report.cost_basis.unknown_cost_basis },
    { key: "dex", metric: "DEX 解码覆盖", evidence: report.dex_decode.coverage.scope, coverage: report.dex_decode.coverage, unknown: report.dex_decode.missing },
    { key: "bridge", metric: "Bridge 解码覆盖", evidence: report.bridge_decode.coverage.scope, coverage: report.bridge_decode.coverage, unknown: report.bridge_decode.missing },
    { key: "entity", metric: "实体覆盖", evidence: report.entity.coverage.scope, coverage: report.entity.coverage, unknown: report.entity.unknown_entity },
  ];
}

function formatCoverage(coverage: Coverage): string {
  return coverage.available && coverage.percentage !== null ? `${coverage.percentage.toFixed(1)}%` : "未知";
}

function formatDateTime(value?: string | null): string {
  if (!value) return "未知";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false });
}

function QualitySkeleton() {
  return (
    <Section title="正在读取金融质量">
      <Skeleton active paragraph={{ rows: 8 }} />
    </Section>
  );
}
