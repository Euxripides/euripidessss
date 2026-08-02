import {
  ApartmentOutlined,
  CalendarOutlined,
  DatabaseOutlined,
  SearchOutlined,
  SwapOutlined,
  WalletOutlined,
} from "@ant-design/icons";
import { Alert, Button, Descriptions, Empty, Input, Progress, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useCallback, useEffect, useState } from "react";
import { DetailPanel, MetricCard, PageHeader } from "../../design-system/DesignSystem";
import {
  fetchFlows,
  fetchPaths,
  fetchProfile,
  fetchRisk,
  type AddressProfile,
  type FlowEdge,
  type PathItem,
  type RiskResult,
} from "./analyticsApi";
import { formatNumber, shortAddr } from "./format";
import "./address-detail.css";

interface Props {
  initialAddress?: string;
}

export default function AddressPage({ initialAddress }: Props) {
  const [addr, setAddr] = useState(initialAddress ?? "");
  const [input, setInput] = useState(initialAddress ?? "");
  const [profile, setProfile] = useState<AddressProfile | null>(null);
  const [risk, setRisk] = useState<RiskResult | null>(null);
  const [flows, setFlows] = useState<FlowEdge[]>([]);
  const [paths, setPaths] = useState<PathItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async (value: string) => {
    const address = value.trim().toLowerCase();
    if (!address) return;
    setAddr(address);
    setError("");
    setLoading(true);
    const [nextProfile, nextRisk, nextFlows, nextPaths] = await Promise.allSettled([
      fetchProfile(address),
      fetchRisk(address),
      fetchFlows(address),
      fetchPaths(address),
    ]);
    setProfile(nextProfile.status === "fulfilled" ? nextProfile.value : null);
    setRisk(nextRisk.status === "fulfilled" ? nextRisk.value : null);
    setFlows(nextFlows.status === "fulfilled" ? nextFlows.value : []);
    setPaths(nextPaths.status === "fulfilled" ? nextPaths.value : []);
    if (nextProfile.status === "rejected") {
      setError(`画像查询失败：${errorMessage(nextProfile.reason)}`);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    if (initialAddress) void load(initialAddress);
  }, [initialAddress, load]);

  const flowColumns: ColumnsType<FlowEdge> = [
    {
      title: "方向",
      dataIndex: "direction",
      width: 72,
      render: (direction: string) => (
        <Tag color={direction === "incoming" ? "green" : "blue"}>
          {direction === "incoming" ? "流入" : "流出"}
        </Tag>
      ),
    },
    { title: "Token", dataIndex: "token", render: (token: string) => shortAddr(token) },
    { title: "对手地址", dataIndex: "counterparty", render: (counterparty: string) => <code>{shortAddr(counterparty)}</code> },
    { title: "金额", dataIndex: "amount" },
    { title: "区块", dataIndex: "block" },
    { title: "交易哈希", dataIndex: "tx_hash", render: (hash: string) => <code>{shortAddr(hash, 10, 8)}</code> },
  ];

  const pathColumns: ColumnsType<PathItem> = [
    {
      title: "路径",
      key: "path",
      render: (_, row) => <code>{`${shortAddr(row.a)} → ${shortAddr(row.b)} → ${shortAddr(row.c)}`}</code>,
    },
    { title: "Token", dataIndex: "token", render: (token: string) => shortAddr(token) },
    { title: "金额", dataIndex: "amount" },
  ];

  const addressType = profile
    ? profile.contract_count > 0
      ? "合约"
      : profile.transaction_count >= 10
        ? "活跃交易方"
        : "低频地址"
    : "待识别";

  return (
    <div className="ds-page analytics-page address-detail-page">
      <PageHeader
        title="地址画像"
        description="汇总地址身份、活动、资产关系与两跳资金路径，所有结果基于当前数据资产窗口。"
      />

      <DetailPanel className="analytics-query-panel">
        <div className="analytics-query-row">
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder="输入 0x 地址"
            value={input}
            onChange={(event) => setInput(event.target.value)}
            onPressEnter={() => void load(input)}
          />
          <Button type="primary" loading={loading} onClick={() => void load(input)}>查询地址</Button>
        </div>
      </DetailPanel>

      {error ? <Alert type="error" message={error} showIcon className="analytics-page-alert" /> : null}

      {addr ? (
        <>
          <DetailPanel
            className="address-identity-panel"
            title={(
              <span className="address-panel-title">
                <span>地址身份</span>
                <Tag color={riskTagColor(risk?.risk_level)}>{risk ? `${risk.risk_level}风险` : "未评分"}</Tag>
              </span>
            )}
            description="当前查询地址"
            extra={<code className="address-full-value">{addr}</code>}
          >
            <div className="address-metrics">
              <MetricCard title="地址类型" value={addressType} detail="基于合约与活动特征" icon={<WalletOutlined />} />
              <MetricCard title="链上事件" value={formatNumber(profile?.event_count ?? 0)} detail={`交易 ${formatNumber(profile?.transaction_count ?? 0)} 条`} icon={<SwapOutlined />} />
              <MetricCard title="Token" value={formatNumber(profile?.token_count ?? 0)} detail={`合约关联 ${formatNumber(profile?.contract_count ?? 0)}`} icon={<DatabaseOutlined />} tone="green" />
              <MetricCard title="活跃天数" value={formatNumber(profile?.active_days ?? 0)} detail="当前数据窗口" icon={<CalendarOutlined />} tone="neutral" />
            </div>
            {profile ? (
              <Descriptions className="address-activity" column={{ xs: 1, sm: 2, lg: 4 }} size="small">
                <Descriptions.Item label="首次活动">{profile.first_activity_time || "—"}</Descriptions.Item>
                <Descriptions.Item label="最近活动">{profile.last_activity_time || "—"}</Descriptions.Item>
                <Descriptions.Item label="累计流入">{formatNumber(profile.total_in)}</Descriptions.Item>
                <Descriptions.Item label="累计流出">{formatNumber(profile.total_out)}</Descriptions.Item>
              </Descriptions>
            ) : null}
          </DetailPanel>

          <div className="address-detail-grid">
            <DetailPanel title="风险评分" description="当前地址的规则评分与关键驱动">
              {risk ? (
                <div className="address-risk-content">
                  <div className="address-risk-score">
                    <strong className={`risk-text-${riskTone(risk.risk_level)}`}>{risk.risk_score.toFixed(1)}</strong>
                    <span>/ 100</span>
                  </div>
                  <Progress
                    percent={risk.risk_score}
                    showInfo={false}
                    strokeColor={riskStroke(risk.risk_level)}
                    trailColor="#edf1f6"
                  />
                  <p>{risk.risk_reason || "当前规则未返回风险说明"}</p>
                  <div className="address-risk-signals">
                    <span><small>交易频率</small><strong>{formatNumber(risk.transaction_frequency)}</strong></span>
                    <span><small>集中度</small><strong>{(risk.top_holder_ratio * 100).toFixed(1)}%</strong></span>
                    <span><small>关联度</small><strong>{risk.shared_counterparty_score.toFixed(3)}</strong></span>
                  </div>
                </div>
              ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无风险评分" />}
            </DetailPanel>

            <DetailPanel
              title="资金流"
              description="最近 100 条与当前地址直接相关的链上资金记录"
              extra={<span className="detail-count">{flows.length} 条</span>}
            >
              <Table
                size="small"
                rowKey={(row) => row.tx_hash + row.direction + row.block}
                columns={flowColumns}
                dataSource={flows.slice(0, 100)}
                pagination={{ pageSize: 8, showSizeChanger: false }}
                scroll={{ x: 760 }}
              />
            </DetailPanel>
          </div>

          <DetailPanel
            title="两跳资金路径"
            description="从当前地址出发的两层可追踪资金关系"
            extra={<span className="detail-count">{paths.length} 条</span>}
          >
            <Table
              size="small"
              rowKey={(row) => row.a + row.b + row.c + row.token}
              columns={pathColumns}
              dataSource={paths}
              pagination={{ pageSize: 8, showSizeChanger: false }}
              scroll={{ x: 640 }}
            />
          </DetailPanel>
        </>
      ) : (
        <DetailPanel className="analytics-empty-panel">
          <Empty image={<ApartmentOutlined />} description="输入地址后查看画像、风险、资金流与路径" />
        </DetailPanel>
      )}
    </div>
  );
}

function riskTagColor(level?: string) {
  return level === "高" ? "red" : level === "中" ? "orange" : level === "低" ? "green" : "default";
}

function riskTone(level: string) {
  return level === "高" ? "red" : level === "中" ? "amber" : "green";
}

function riskStroke(level: string) {
  return level === "高" ? "#e5484d" : level === "中" ? "#d97706" : "#0f9f6e";
}

function errorMessage(value: unknown) {
  return value instanceof Error ? value.message : String(value);
}
