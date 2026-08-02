import {
  ApartmentOutlined,
  CalendarOutlined,
  SearchOutlined,
  SafetyCertificateOutlined,
  SwapOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { Alert, Button, Descriptions, Empty, Input, Progress, Tag } from "antd";
import { useEffect, useState } from "react";
import { DetailPanel, MetricCard, PageHeader } from "../../design-system/DesignSystem";
import {
  fetchDashboard,
  fetchProfile,
  fetchRisk,
  type AddressProfile,
  type RiskResult,
} from "./analyticsApi";
import { formatNumber } from "./format";
import "./risk-detail.css";

export default function RiskAnalysisPage() {
  const [input, setInput] = useState("");
  const [address, setAddress] = useState("");
  const [risk, setRisk] = useState<RiskResult | null>(null);
  const [profile, setProfile] = useState<AddressProfile | null>(null);
  const [riskAddresses, setRiskAddresses] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    fetchDashboard()
      .then((data) => setRiskAddresses(data?.risk_addresses ?? null))
      .catch(() => setRiskAddresses(null));
  }, []);

  const load = async (value: string) => {
    const nextAddress = value.trim().toLowerCase();
    if (!nextAddress) return;
    setAddress(nextAddress);
    setError("");
    setLoading(true);
    const [nextRisk, nextProfile] = await Promise.allSettled([
      fetchRisk(nextAddress),
      fetchProfile(nextAddress),
    ]);
    setRisk(nextRisk.status === "fulfilled" ? nextRisk.value : null);
    setProfile(nextProfile.status === "fulfilled" ? nextProfile.value : null);
    if (nextRisk.status === "rejected") {
      setError(`风险查询失败：${nextRisk.reason instanceof Error ? nextRisk.reason.message : String(nextRisk.reason)}`);
    }
    setLoading(false);
  };

  return (
    <div className="ds-page analytics-page risk-detail-page">
      <PageHeader
        title="风险分析"
        description="识别高频风险地址，并拆解单地址评分、行为画像与评分口径。"
      />

      <div className="risk-overview-grid">
        <MetricCard
          title="高频风险地址"
          value={riskAddresses === null ? "—" : formatNumber(riskAddresses)}
          detail="事件数 ≥ 100 的当前代理指标"
          icon={<WarningOutlined />}
          tone="red"
        />
        <DetailPanel className="analytics-query-panel">
          <div className="analytics-query-row">
            <Input
              allowClear
              prefix={<SearchOutlined />}
              placeholder="输入 0x 地址进行风险评分"
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onPressEnter={() => void load(input)}
            />
            <Button type="primary" loading={loading} onClick={() => void load(input)}>查询风险</Button>
          </div>
        </DetailPanel>
      </div>

      {error ? <Alert type="error" message={error} showIcon className="analytics-page-alert" /> : null}

      {address && risk ? (
        <>
          <div className="risk-result-grid">
            <DetailPanel
              title="地址风险评分"
              description="规则引擎综合评分"
              extra={<Tag color={riskTagColor(risk.risk_level)}>{risk.risk_level}风险</Tag>}
            >
              <div className="risk-score-hero">
                <div>
                  <strong className={`risk-score-${riskTone(risk.risk_level)}`}>{risk.risk_score.toFixed(1)}</strong>
                  <span>/ 100</span>
                </div>
                <code>{address}</code>
              </div>
              <Progress
                percent={risk.risk_score}
                showInfo={false}
                strokeColor={riskStroke(risk.risk_level)}
                trailColor="#edf1f6"
              />
              <p className="risk-reason">{risk.risk_reason || "当前规则未返回风险说明"}</p>
              <div className="risk-signal-grid">
                <span><small>交易频率</small><strong>{formatNumber(risk.transaction_frequency)}</strong><em>笔 / 活跃天</em></span>
                <span><small>Top10 接收占比</small><strong>{(risk.top_holder_ratio * 100).toFixed(1)}%</strong><em>资金集中度</em></span>
                <span><small>共同对手关联度</small><strong>{risk.shared_counterparty_score.toFixed(3)}</strong><em>关系网络信号</em></span>
              </div>
            </DetailPanel>

            <DetailPanel title="地址行为画像" description="与风险评分对应的链上活动数据">
              {profile ? (
                <div className="risk-profile-metrics">
                  <MetricCard title="事件" value={formatNumber(profile.event_count)} detail={`交易 ${formatNumber(profile.transaction_count)} 条`} icon={<SwapOutlined />} />
                  <MetricCard title="Token" value={formatNumber(profile.token_count)} detail={`合约关联 ${formatNumber(profile.contract_count)}`} icon={<ApartmentOutlined />} tone="green" />
                  <MetricCard title="活跃天数" value={formatNumber(profile.active_days)} detail="当前数据窗口" icon={<CalendarOutlined />} tone="neutral" />
                  <MetricCard title="资金流量" value={formatNumber(profile.total_in + profile.total_out)} detail={`流入 ${formatNumber(profile.total_in)} / 流出 ${formatNumber(profile.total_out)}`} icon={<SafetyCertificateOutlined />} tone="amber" />
                </div>
              ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="画像数据不可用" />}
              {profile ? (
                <Descriptions className="risk-profile-time" column={{ xs: 1, md: 2 }} size="small">
                  <Descriptions.Item label="首次活动">{profile.first_activity_time || "—"}</Descriptions.Item>
                  <Descriptions.Item label="最近活动">{profile.last_activity_time || "—"}</Descriptions.Item>
                </Descriptions>
              ) : null}
            </DetailPanel>
          </div>
        </>
      ) : null}

      {address && !risk && !loading && !error ? (
        <Alert
          className="analytics-page-alert"
          type="info"
          showIcon
          message="该地址无风险评分数据"
          description="地址可能不在当前数据集内，或尚未生成风险指标。"
        />
      ) : null}

      <DetailPanel title="评分口径" description="用于解释当前规则评分，不代表外部黑名单认定">
        <div className="risk-method-grid">
          <span><strong>评分构成</strong><p>交易频率 60% + Top10 接收占比 40%，共同对手关联度额外加分，最高加 10 分。</p></span>
          <span><strong>风险等级</strong><p>评分 ≥ 60 为高风险，≥ 30 为中风险，其余为低风险，总分封顶 100。</p></span>
          <span><strong>数据边界</strong><p>高频地址采用事件数 ≥ 100 作为代理指标；单地址评分仅覆盖当前数据仓库窗口。</p></span>
        </div>
      </DetailPanel>
    </div>
  );
}

function riskTagColor(level: string) {
  return level === "高" ? "red" : level === "中" ? "orange" : "green";
}

function riskTone(level: string) {
  return level === "高" ? "red" : level === "中" ? "amber" : "green";
}

function riskStroke(level: string) {
  return level === "高" ? "#e5484d" : level === "中" ? "#d97706" : "#0f9f6e";
}
