import { Card, Empty, Typography } from "antd";

const { Title, Text } = Typography;

export default function SystemSettingsPage() {
  return (
    <div style={{ padding: 16 }}>
      <Title level={4}>系统设置</Title>
      <Card>
        <Empty description="建设中" />
        <div style={{ textAlign: "center" }}>
          <Text type="secondary">
            计划提供：服务状态、日志、配置、系统信息。
          </Text>
        </div>
      </Card>
    </div>
  );
}
