import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Input,
  Progress,
  Row,
  Space,
  Statistic,
  Tag,
  Typography,
} from "antd";
import {
  fetchDashboard,
  fetchProfile,
  fetchRisk,
  AddressProfile,
  RiskResult,
} from "./analyticsApi";
import { formatNumber } from "./format";

const { Title, Text } = Typography;

function riskColor(level: string): string {
  return level === "高" ? "red" : level === "中" ? "orange" : "green";
}

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
      .then((d) => setRiskAddresses(d?.risk_addresses ?? null))
      .catch(() => setRiskAddresses(null));
  }, []);

  const load = (value: string) => {
    const addr = value.trim().toLowerCase();
    if (!addr) return;
    setAddress(addr);
    setError("");
    setLoading(true);
    fetchRisk(addr)
      .then(setRisk)
      .catch((e) => {
        setRisk(null);
        setError(`风险查询失败: ${e}`);
      });
    fetchProfile(addr)
      .then(setProfile)
      .catch(() => setProfile(null))
      .finally(() => setLoading(false));
  };

  return (
    <div style={{ padding: 16 }}>
      <Title level={4}>风险分析</Title>

      <Row gutter={[16, 16]}>
        <Col span={8}>
          <Card title="风险地址总数">
            <Statistic
              title="事件数 ≥ 100 的高频地址（数据资产概览）"
              value={riskAddresses === null ? "—" : riskAddresses}
            />
          </Card>
        </Col>
        <Col span={16}>
          <Card title="地址风险评分">
            <Space style={{ marginBottom: 16 }}>
              <Input
                placeholder="输入 0x 地址"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onPressEnter={() => load(input)}
                style={{ width: 420 }}
              />
              <Button type="primary" loading={loading} onClick={() => load(input)}>
                查询
              </Button>
            </Space>
            {error && <Alert type="error" message={error} showIcon />}
          </Card>
        </Col>
      </Row>

      {address && risk && (
        <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
          <Col span={8}>
            <Card title={`风险评分：${address}`}>
              <Title level={1} style={{ color: riskColor(risk.risk_level), margin: 0 }}>
                {risk.risk_score.toFixed(1)}
              </Title>
              <Space style={{ marginTop: 4 }}>
                <Tag color={riskColor(risk.risk_level)}>{risk.risk_level}风险</Tag>
                <Text type="secondary">{risk.risk_reason}</Text>
              </Space>
              <Progress
                percent={risk.risk_score}
                status={risk.risk_level === "低" ? "success" : risk.risk_level === "高" ? "exception" : "active"}
                showInfo={false}
                strokeColor={riskColor(risk.risk_level)}
                style={{ marginTop: 16 }}
              />
              <Descriptions column={1} size="small" style={{ marginTop: 12 }}>
                <Descriptions.Item label="交易频率（笔/活跃天）">
                  {formatNumber(risk.transaction_frequency)}
                </Descriptions.Item>
                <Descriptions.Item label="Top10 接收占比">
                  {(risk.top_holder_ratio * 100).toFixed(1)}%
                </Descriptions.Item>
                <Descriptions.Item label="共同对手关联度">
                  {risk.shared_counterparty_score.toFixed(3)}
                </Descriptions.Item>
              </Descriptions>
            </Card>
          </Col>
          <Col span={16}>
            <Card title="地址画像">
              {profile ? (
                <Descriptions column={4} size="small">
                  <Descriptions.Item label="事件数">{formatNumber(profile.event_count)}</Descriptions.Item>
                  <Descriptions.Item label="交易数">{formatNumber(profile.transaction_count)}</Descriptions.Item>
                  <Descriptions.Item label="Token 数">{profile.token_count}</Descriptions.Item>
                  <Descriptions.Item label="合约关联">{profile.contract_count}</Descriptions.Item>
                  <Descriptions.Item label="首次活动">{profile.first_activity_time}</Descriptions.Item>
                  <Descriptions.Item label="最近活动">{profile.last_activity_time}</Descriptions.Item>
                  <Descriptions.Item label="流入">{formatNumber(profile.total_in)}</Descriptions.Item>
                  <Descriptions.Item label="流出">{formatNumber(profile.total_out)}</Descriptions.Item>
                </Descriptions>
              ) : (
                <Text type="secondary">画像数据不可用</Text>
              )}
            </Card>
          </Col>
        </Row>
      )}

      {address && !risk && !loading && !error && (
        <Alert
          style={{ marginTop: 16 }}
          type="info"
          showIcon
          message="该地址无风险评分数据"
          description="地址可能不在当前数据集内，或尚未生成风险指标。"
        />
      )}

      <Card title="评分说明" style={{ marginTop: 16 }}>
        <Descriptions column={1} size="small">
          <Descriptions.Item label="评分构成">
            交易频率 60% + Top10 接收占比 40%，共同对手关联度额外加分（最高 +10），总分封顶 100。
          </Descriptions.Item>
          <Descriptions.Item label="风险等级">
            ≥ 60 为高风险，≥ 30 为中风险，其余为低风险。
          </Descriptions.Item>
          <Descriptions.Item label="数据口径">
            风险地址统计采用事件数 ≥ 100 的高频地址作为代理指标；单地址评分基于当前数据仓库窗口。
          </Descriptions.Item>
        </Descriptions>
      </Card>
    </div>
  );
}
