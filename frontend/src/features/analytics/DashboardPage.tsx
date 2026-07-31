import { useEffect, useRef, useState } from "react";
import { Card, Col, Row, Statistic, Input, Button, Typography, Space } from "antd";
import {
  ApartmentOutlined,
  FundProjectionScreenOutlined,
  WalletOutlined,
  SafetyOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import * as echarts from "echarts";
import { fetchDashboard, fetchProfile, DashboardOverview } from "./analyticsApi";
import { formatNumber } from "./format";

const { Title, Text } = Typography;

interface Props {
  onNavigate: (page: string, address?: string) => void;
}

export default function DashboardPage({ onNavigate }: Props) {
  const [data, setData] = useState<DashboardOverview | null>(null);
  const [addr, setAddr] = useState("");
  const trendRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    fetchDashboard()
      .then(setData)
      .catch(() => setData(null));
  }, []);

  useEffect(() => {
    if (!data || !trendRef.current) return;
    const chart = echarts.init(trendRef.current);
    chart.setOption({
      title: { text: "事件趋势（按区块分桶）", left: "center", textStyle: { fontSize: 13 } },
      tooltip: { trigger: "axis" },
      grid: { left: 50, right: 20, top: 40, bottom: 30 },
      xAxis: { type: "category", data: data.trend.map((t) => t.block) },
      yAxis: { type: "value" },
      series: [
        {
          name: "事件数",
          type: "line",
          smooth: true,
          areaStyle: { opacity: 0.15 },
          data: data.trend.map((t) => t.events),
        },
      ],
    });
    const onResize = () => chart.resize();
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("resize", onResize);
      chart.dispose();
    };
  }, [data]);

  const stats = [
    { title: "地址数量", value: data?.address_count ?? 0, icon: <WalletOutlined />, key: "addresses" },
    { title: "Token 数量", value: data?.token_count ?? 0, icon: <FundProjectionScreenOutlined />, key: "tokens" },
    { title: "事件数量", value: data?.transaction_count ?? 0, icon: <ApartmentOutlined />, key: "events" },
    { title: "风险地址", value: data?.risk_addresses ?? 0, icon: <SafetyOutlined />, key: "risk" },
  ];

  const handleSearch = () => {
    const a = addr.trim().toLowerCase();
    if (a) onNavigate("analytics-address", a);
  };

  return (
    <div style={{ padding: 16 }}>
      <Title level={4}>链上分析工作台</Title>
      <Space style={{ marginBottom: 16 }}>
        <Input
          placeholder="输入地址查询画像 / 资金流 / 风险"
          value={addr}
          onChange={(e) => setAddr(e.target.value)}
          onPressEnter={handleSearch}
          style={{ width: 420 }}
          prefix={<SearchOutlined />}
        />
        <Button type="primary" onClick={handleSearch}>
          查询
        </Button>
        <Button onClick={() => onNavigate("analytics-graph")}>打开图谱</Button>
        <Button onClick={() => onNavigate("analytics-report")}>报告中心</Button>
      </Space>

      <Row gutter={[16, 16]}>
        {stats.map((s) => (
          <Col span={6} key={s.key}>
            <Card>
              <Statistic title={s.title} value={s.value} precision={0} prefix={s.icon} />
            </Card>
          </Col>
        ))}
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col span={16}>
          <Card>
            <div ref={trendRef} style={{ height: 320 }} />
          </Card>
        </Col>
        <Col span={8}>
          <Card title="快速入口">
            <Space direction="vertical" style={{ width: "100%" }}>
              <Button block onClick={() => onNavigate("analytics-address")}>
                地址分析（画像 / 资产 / 风险）
              </Button>
              <Button block onClick={() => onNavigate("analytics-graph")}>
                地址关系图谱
              </Button>
              <Button block onClick={() => onNavigate("analytics-report")}>
                报告中心
              </Button>
              {data && (
                <Text type="secondary">
                  数据版本：sqd-200k-v2 · 事件 {formatNumber(data.transaction_count)} 条 · 转账{" "}
                  {formatNumber(data.transfer_count)} 条
                </Text>
              )}
            </Space>
          </Card>
        </Col>
      </Row>
      {data && data.address_count === 0 && (
        <Text type="warning">提示：数据为空，请先运行 SQD 下载生成 Parquet 数据资产。</Text>
      )}
    </div>
  );
}
