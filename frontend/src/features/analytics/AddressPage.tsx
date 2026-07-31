import { useEffect, useState } from "react";
import {
  Card,
  Col,
  Descriptions,
  Row,
  Table,
  Tag,
  Typography,
  Alert,
  Input,
  Button,
  Space,
} from "antd";
import {
  fetchProfile,
  fetchFlows,
  fetchRisk,
  fetchPaths,
  AddressProfile,
  FlowEdge,
  RiskResult,
  PathItem,
} from "./analyticsApi";
import { formatNumber, shortAddr } from "./format";

const { Title, Text } = Typography;

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
  const [error, setError] = useState("");

  const load = (address: string) => {
    if (!address) return;
    setAddr(address);
    setError("");
    fetchProfile(address)
      .then(setProfile)
      .catch((e) => setError(`画像查询失败: ${e}`));
    fetchRisk(address)
      .then(setRisk)
      .catch(() => setRisk(null));
    fetchFlows(address)
      .then(setFlows)
      .catch(() => setFlows([]));
    fetchPaths(address)
      .then(setPaths)
      .catch(() => setPaths([]));
  };

  useEffect(() => {
    if (initialAddress) load(initialAddress);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialAddress]);

  const riskColor = (level: string) =>
    level === "高" ? "red" : level === "中" ? "orange" : "green";

  const flowColumns = [
    { title: "方向", dataIndex: "direction", render: (d: string) => (d === "incoming" ? <Tag color="green">流入</Tag> : <Tag color="blue">流出</Tag>) },
    { title: "Token", dataIndex: "token", render: (t: string) => shortAddr(t) },
    { title: "对手", dataIndex: "counterparty", render: (c: string) => shortAddr(c) },
    { title: "金额", dataIndex: "amount" },
    { title: "Block", dataIndex: "block" },
    { title: "tx_hash", dataIndex: "tx_hash", render: (h: string) => shortAddr(h, 10, 8) },
  ];

  const pathColumns = [
    { title: "路径", key: "path", render: (_: unknown, r: PathItem) => `${shortAddr(r.a)} → ${shortAddr(r.b)} → ${shortAddr(r.c)}` },
    { title: "Token", dataIndex: "token", render: (t: string) => shortAddr(t) },
    { title: "金额", dataIndex: "amount" },
  ];

  return (
    <div style={{ padding: 16 }}>
      <Title level={4}>地址分析</Title>
      <Space style={{ marginBottom: 16 }}>
        <Input
          placeholder="输入 0x 地址"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onPressEnter={() => load(input.trim().toLowerCase())}
          style={{ width: 420 }}
        />
        <Button type="primary" onClick={() => load(input.trim().toLowerCase())}>
          查询
        </Button>
      </Space>
      {error && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}

      {addr && (
        <Row gutter={[16, 16]}>
          <Col span={24}>
            <Card title={`地址画像：${addr}`}>
              {profile && (
                <Descriptions column={4} size="small">
                  <Descriptions.Item label="类型">
                    {profile.contract_count > 0 ? "合约" : profile.transaction_count >= 10 ? "活跃交易方" : "低频"}
                  </Descriptions.Item>
                  <Descriptions.Item label="首次活动">{profile.first_activity_time}</Descriptions.Item>
                  <Descriptions.Item label="最近活动">{profile.last_activity_time}</Descriptions.Item>
                  <Descriptions.Item label="交易数">{formatNumber(profile.transaction_count)}</Descriptions.Item>
                  <Descriptions.Item label="Token 数">{profile.token_count}</Descriptions.Item>
                  <Descriptions.Item label="流入">{formatNumber(profile.total_in)}</Descriptions.Item>
                  <Descriptions.Item label="流出">{formatNumber(profile.total_out)}</Descriptions.Item>
                  <Descriptions.Item label="活跃天数">{profile.active_days}</Descriptions.Item>
                </Descriptions>
              )}
            </Card>
          </Col>

          <Col span={8}>
            <Card title="风险评分">
              {risk && (
                <>
                  <Title level={2} style={{ color: riskColor(risk.risk_level), margin: 0 }}>
                    {risk.risk_score.toFixed(1)}
                  </Title>
                  <Tag color={riskColor(risk.risk_level)}>{risk.risk_level}</Tag>
                  <Text type="secondary">{risk.risk_reason}</Text>
                  <div style={{ marginTop: 8 }}>
                    <Text>交易频率：{formatNumber(risk.transaction_frequency)}</Text>
                    <br />
                    <Text>集中度：{(risk.top_holder_ratio * 100).toFixed(1)}%</Text>
                    <br />
                    <Text>关联度：{risk.shared_counterparty_score.toFixed(3)}</Text>
                  </div>
                </>
              )}
            </Card>
          </Col>

          <Col span={16}>
            <Card title="资金流（近 100 条）">
              <Table
                size="small"
                rowKey={(r) => r.tx_hash + r.direction + r.block}
                columns={flowColumns}
                dataSource={flows.slice(0, 100)}
                pagination={{ pageSize: 10, showSizeChanger: false }}
              />
            </Card>
          </Col>

          <Col span={24}>
            <Card title="两跳资金路径">
              <Table
                size="small"
                rowKey={(r) => r.a + r.b + r.c + r.token}
                columns={pathColumns}
                dataSource={paths}
                pagination={{ pageSize: 10, showSizeChanger: false }}
              />
            </Card>
          </Col>
        </Row>
      )}
    </div>
  );
}
