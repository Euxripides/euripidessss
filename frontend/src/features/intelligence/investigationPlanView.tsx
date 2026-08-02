// 调查计划预览与执行时间线（V2 设计 §12/§13：PlanPreview + AgentTimeline）
import { type ReactNode } from "react";
import { List, Space, Tag, Timeline, Typography } from "antd";
import { CheckCircleOutlined, ClockCircleOutlined } from "@ant-design/icons";
import { DetailPanel } from "../../design-system/DesignSystem";
import type { Investigation } from "./intelligenceApi";

const { Text } = Typography;

const TASK_TYPE_COLOR: Record<string, string> = {
  ADDRESS_PROFILE: "geekblue",
  BALANCE_ANALYSIS: "cyan",
  TOKEN_ANALYSIS: "cyan",
  PROFIT_DETECTION: "gold",
  FORWARD_TRACE: "green",
  BACKWARD_TRACE: "green",
  FLOW_GRAPH: "blue",
  EXCHANGE_DETECTION: "purple",
  ENTITY_CLUSTER: "purple",
  RISK_ANALYSIS: "red",
  RISK_SCAN: "red",
  IDENTITY_LOOKUP: "magenta",
  REPORT_GENERATE: "default",
  GENERATE_REPORT: "default",
};

const DECISION_COLOR: Record<string, string> = {
  EXPAND: "green",
  STOP: "red",
  DEEP_ANALYSIS: "orange",
};

const OBS_COLOR: Record<string, string> = {
  NEW_ADDRESS: "blue",
  NEW_PATH: "green",
  NEW_TRANSACTION: "cyan",
  RISK_EVENT: "red",
};

function priorityColor(p: number): string {
  if (p <= 1) return "red";
  if (p === 2) return "orange";
  return "blue";
}

// PlanPreview：调查计划（目标 + 任务清单 + 优先级）
export function PlanPreview({ inv }: { inv: Investigation }) {
  const plan = inv.plan;
  if (!plan) {
    return (
      <DetailPanel size="small" title="调查计划">
        <Space>
          <ClockCircleOutlined spin />
          <Text type="secondary">AI 计划生成中，将在规划阶段完成后展示</Text>
        </Space>
      </DetailPanel>
    );
  }
  return (
    <DetailPanel size="small" title={`调查计划（${plan.tasks?.length ?? 0} 项任务）`}>
      <Space direction="vertical" style={{ width: "100%" }} size={8}>
        {(plan.objectives?.length ?? 0) > 0 && (
          <Space wrap>
            <Text strong>目标：</Text>
            {plan.objectives.map((o, i) => (
              <Tag key={i} color="blue">
                {o}
              </Tag>
            ))}
          </Space>
        )}
        <List
          size="small"
          dataSource={plan.tasks ?? []}
          renderItem={(t, i) => (
            <List.Item>
              <Space wrap>
                <Tag color={priorityColor(t.priority)}>P{t.priority}</Tag>
                <Tag color={TASK_TYPE_COLOR[t.type] ?? "default"}>{t.type}</Tag>
                <Text>{t.description}</Text>
              </Space>
            </List.Item>
          )}
          locale={{ emptyText: "暂无任务" }}
        />
      </Space>
    </DetailPanel>
  );
}

// AgentTimeline：执行时间线（轮次决策 + 观察事件，按时间排序）
export function AgentTimeline({ inv }: { inv: Investigation }) {
  interface Ev {
    time: number;
    node: ReactNode;
  }
  const events: Ev[] = [];
  (inv.rounds ?? []).forEach((r) => {
    events.push({
      time: new Date(r.started_at).getTime(),
      node: (
        <Space wrap>
          <Tag color="geekblue">第 {r.round} 轮</Tag>
          <Tag color={DECISION_COLOR[r.decision] ?? "default"}>{r.decision || "执行中"}</Tag>
          {r.note && <Text type="secondary">{r.note}</Text>}
        </Space>
      ),
    });
  });
  (inv.observations ?? []).forEach((o) => {
    events.push({
      time: (o.timestamp ?? 0) * 1000,
      node: (
        <Space wrap>
          <Tag color={OBS_COLOR[o.type] ?? "default"}>{o.type}</Tag>
          <Text code>{o.address ? `${o.address.slice(0, 10)}…` : o.detail.slice(0, 48)}</Text>
          <Text type="secondary">{o.source}</Text>
        </Space>
      ),
    });
  });
  events.sort((a, b) => a.time - b.time);

  const total = (inv.rounds?.length ?? 0) + (inv.observations?.length ?? 0);
  return (
    <DetailPanel size="small" title={`执行时间线（${total} 条事件）`}>
      {total === 0 ? (
        <Text type="secondary">暂无事件，等待任务执行…</Text>
      ) : (
        <Timeline
          items={events.map((e, i) => ({
            dot: i === events.length - 1 && inv.status === "COMPLETED" ? <CheckCircleOutlined style={{ color: "#52c41a" }} /> : undefined,
            children: e.node,
          }))}
        />
      )}
    </DetailPanel>
  );
}
