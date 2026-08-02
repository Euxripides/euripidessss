// 调查结果摘要（V2 设计 §11 ResultSummary）：请求摘要 + 六维调查价值评分
import { Descriptions, Progress, Space, Tag, Typography } from "antd";
import { DetailPanel } from "../../design-system/DesignSystem";
import type { Investigation } from "./intelligenceApi";

const { Text } = Typography;

const MODE_LABEL: Record<string, string> = {
  auto: "自动推断",
  fund_trace: "资金追踪",
  profit_analyze: "获利分析",
  exchange_entry: "交易所入口",
  identity_lookup: "身份线索",
  risk_scan: "风险扫描",
};

// 六维评分条目（设计 §9-11）
const SCORE_DIMS: { key: "fund" | "behavior" | "risk" | "entity" | "graph" | "identity"; label: string; color: string }[] = [
  { key: "fund", label: "资金价值", color: "#f59f00" },
  { key: "behavior", label: "行为价值", color: "#1677ff" },
  { key: "risk", label: "风险价值", color: "#ff4d4f" },
  { key: "entity", label: "实体价值", color: "#722ed1" },
  { key: "graph", label: "图价值", color: "#13c2c2" },
  { key: "identity", label: "身份价值", color: "#eb2f96" },
];

// InvestigationRequestSummary：调查请求摘要
export function InvestigationRequestSummary({ inv }: { inv: Investigation }) {
  const req = inv.request;
  if (!req) return null;
  return (
    <DetailPanel size="small" title="调查请求">
      <Descriptions size="small" column={{ xs: 1, md: 2 }} bordered>
        <Descriptions.Item label="调查目的" span={2}>
          {req.objective || "-"}
        </Descriptions.Item>
        <Descriptions.Item label="调查模式">
          <Tag color="blue">{MODE_LABEL[req.mode] ?? req.mode}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="期望结果">
          {(req.expected_result ?? []).map((e) => (
            <Tag key={e}>{e}</Tag>
          ))}
        </Descriptions.Item>
        {req.intent && (
          <Descriptions.Item label="意图分析" span={2}>
            <Space wrap>
              <Tag>{req.intent.direction}</Tag>
              {(req.intent.goals ?? []).map((g) => (
                <Tag key={g} color="geekblue">
                  {g}
                </Tag>
              ))}
              <Text type="secondary">{req.intent.summary}</Text>
            </Space>
          </Descriptions.Item>
        )}
      </Descriptions>
    </DetailPanel>
  );
}

// InvestigationScorePanel：六维调查价值评分
export function InvestigationScorePanel({ inv }: { inv: Investigation }) {  const score = inv.investigation_score;
  if (!score) return null;
  return (
    <DetailPanel
      size="small"
      title={
        <Space>
          <span>调查价值评分</span>
          <Tag color={score.total >= 60 ? "green" : score.total >= 30 ? "orange" : "default"}>{score.total.toFixed(1)}</Tag>
        </Space>
      }
    >
      <Space direction="vertical" style={{ width: "100%" }} size={6}>
        {SCORE_DIMS.map((d) => (
          <div key={d.key} className="investigation-score-row">
            <Text style={{ width: 72, display: "inline-block" }}>{d.label}</Text>
            <Progress
              percent={Math.round((score[d.key] ?? 0) * 10) / 10}
              size="small"
              strokeColor={d.color}
              style={{ flex: 1, margin: 0 }}
            />
          </div>
        ))}
        {score.fund_detail && (
          <Text type="secondary" style={{ fontSize: 12 }}>
            资金分项：余额 {score.fund_detail.balance_points} + 获利 {score.fund_detail.profit_points} + 沉淀{" "}
            {score.fund_detail.holding_points} = {score.fund_detail.total}
          </Text>
        )}
      </Space>
    </DetailPanel>
  );
}

// ProfitReportPanel：获利/沉淀检测（V2.1 设计 §2：估算金额 + 可信度 + 依据明细）
export function ProfitReportPanel({ inv }: { inv: Investigation }) {
  const profit = inv.profit_report;
  if (!profit) return null;
  const marks: Record<string, string> = { "✓": "green", "✗": "red", "?": "default" };
  return (
    <DetailPanel
      size="small"
      title={
        <Space>
          <span>获利与沉淀检测</span>
          {profit.confidence > 0 && <Tag color={profit.confidence >= 0.7 ? "green" : "orange"}>可信度 {Math.round(profit.confidence * 100)}%</Tag>}
        </Space>
      }
    >
      <Space direction="vertical" style={{ width: "100%" }} size={6}>
        <Text>{profit.summary}</Text>
        {profit.estimate_usd ? (
          <Text strong>估算金额：{formatAmount(profit.estimate_usd)}（稳定币净额估算）</Text>
        ) : null}
        {(profit.checklist ?? []).length > 0 && (
          <Space wrap>
            {(profit.checklist ?? []).map((c, i) => {
              const mark = !c.present ? "?" : c.ok ? "✓" : "✗";
              return (
                <Tag key={i} color={marks[mark]}>
                  {mark} {c.label}
                </Tag>
              );
            })}
          </Space>
        )}
        <Text type="secondary" style={{ fontSize: 12 }}>
          {profit.estimate_note}
        </Text>
      </Space>
    </DetailPanel>
  );
}

function formatAmount(v: number): string {
  if (v >= 1e12) return (v / 1e12).toFixed(2) + "T";
  if (v >= 1e9) return (v / 1e9).toFixed(2) + "B";
  if (v >= 1e6) return (v / 1e6).toFixed(2) + "M";
  return v.toFixed(0);
}
