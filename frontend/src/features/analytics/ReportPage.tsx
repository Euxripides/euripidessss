import { Card, List, Tag, Typography, Empty } from "antd";
import {
  FileTextOutlined,
  FilePdfOutlined,
  FileZipOutlined,
  FileImageOutlined,
  FundProjectionScreenOutlined,
} from "@ant-design/icons";

const { Title, Text } = Typography;

interface ReportItem {
  name: string;
  description: string;
  url?: string;
  icon: React.ReactNode;
  color: string;
}

const reports: ReportItem[] = [
  {
    name: "案件分析报告（HTML）",
    description: "7 部分完整案件报告：摘要/画像/资产/资金流/路径/图谱/风险",
    url: "/api/analytics/report/case-full/case-report.html",
    icon: <FileTextOutlined />,
    color: "#1677ff",
  },
  {
    name: "案件分析报告（DOCX）",
    description: "仿宋小四标准化 Word 案件报告",
    url: "/api/analytics/report/case-demo/case-report.docx",
    icon: <FilePdfOutlined />,
    color: "#52c41a",
  },
  {
    name: "证据链（evidence_bundle.json）",
    description: "286 条可追溯证据：dataset + block + tx_hash + log_index",
    url: "/api/analytics/report/case-full/evidence_bundle.json",
    icon: <FileZipOutlined />,
    color: "#fa8c16",
  },
  {
    name: "资产快照（asset_summary.json）",
    description: "Token 余额 / 历史最高 / 时间线 / 清仓信号",
    url: "/api/analytics/report/asset_summary.json",
    icon: <FundProjectionScreenOutlined />,
    color: "#722ed1",
  },
  {
    name: "关系图谱（graph.json）",
    description: "15,595 节点 / 21,693 边，含 PageRank / 簇 / 风险网络",
    url: "/api/analytics/report/graph.json",
    icon: <FileImageOutlined />,
    color: "#13c2c2",
  },
  {
    name: "调查证据（evidence.json）",
    description: "地址/交易/关系/路径四类证据",
    url: "/api/analytics/report/evidence.json",
    icon: <FileZipOutlined />,
    color: "#eb2f96",
  },
];

export default function ReportPage() {
  return (
    <div className="ds-page analytics-page">
      <Title level={4}>报告中心</Title>
      <Text type="secondary">
        已生成的调查产物（基于 sqd-200k-v2 数据资产）。报告文件由后端分析管线生成，可直接下载归档。
      </Text>
      <Card style={{ marginTop: 16 }}>
        {reports.length === 0 ? (
          <Empty description="暂无报告" />
        ) : (
          <List
            itemLayout="horizontal"
            dataSource={reports}
            renderItem={(item) => (
              <List.Item
                actions={[
                  <a key="open" href={item.url} target="_blank" rel="noreferrer">
                    打开
                  </a>,
                ]}
              >
                <List.Item.Meta
                  avatar={<span style={{ fontSize: 22, color: item.color }}>{item.icon}</span>}
                  title={item.name}
                  description={item.description}
                />
                <Tag color={item.color}>V2.1 RC2</Tag>
              </List.Item>
            )}
          />
        )}
      </Card>
    </div>
  );
}
