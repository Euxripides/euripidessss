// 调查请求输入组件（V2 设计 §11：InvestigationInput + ObjectiveEditor + ExpectedResultSelector）
// 组合：调查模式选择 + 调查目的编辑（含常用模板）+ 期望结果多选。
import { Checkbox, Input, Select, Space, Tag, Typography } from "antd";
import {
  EXPECTED_RESULT_OPTIONS,
  INVESTIGATION_MODES,
  OBJECTIVE_TEMPLATES,
  type InvestigationMode,
} from "./intelligenceApi";

const { Text } = Typography;

export interface InvestigationRequestInputProps {
  mode: InvestigationMode;
  onModeChange: (m: InvestigationMode) => void;
  objective: string;
  onObjectiveChange: (s: string) => void;
  expectedResult: string[];
  onExpectedResultChange: (items: string[]) => void;
}

export function InvestigationRequestInput({
  mode,
  onModeChange,
  objective,
  onObjectiveChange,
  expectedResult,
  onExpectedResultChange,
}: InvestigationRequestInputProps) {
  return (
    <div className="investigation-request-input">
      <Space direction="vertical" style={{ width: "100%" }} size={8}>
        {/* 调查模式 */}
        <Space wrap>
          <Text strong>调查模式</Text>
          <Select
            value={mode}
            onChange={onModeChange}
            style={{ width: 150 }}
            options={INVESTIGATION_MODES.map((m) => ({ value: m.value, label: m.label }))}
          />
          <Text type="secondary" style={{ fontSize: 12 }}>
            模式决定规划方向；自动模式下由意图分析推断
          </Text>
        </Space>

        {/* 调查目的（ObjectiveEditor） */}
        <div>
          <Text strong>调查目的</Text>
          <Input.TextArea
            rows={2}
            value={objective}
            onChange={(e) => onObjectiveChange(e.target.value)}
            placeholder="描述调查目的，如：这是一个大额获利地址，寻找最终资金沉淀"
            maxLength={500}
            showCount
          />
          <Space wrap size={4} style={{ marginTop: 4 }}>
            <Text type="secondary" style={{ fontSize: 12 }}>
              常用模板：
            </Text>
            {OBJECTIVE_TEMPLATES.map((t) => (
              <Tag
                key={t}
                style={{ cursor: "pointer" }}
                color={objective === t ? "blue" : undefined}
                onClick={() => onObjectiveChange(objective === t ? "" : t)}
              >
                {t.length > 26 ? `${t.slice(0, 26)}…` : t}
              </Tag>
            ))}
          </Space>
        </div>

        {/* 期望结果（ExpectedResultSelector） */}
        <div>
          <Text strong>期望结果</Text>
          <div style={{ marginTop: 4 }}>
            <Checkbox.Group
              options={EXPECTED_RESULT_OPTIONS.map((o) => ({ label: o, value: o }))}
              value={expectedResult}
              onChange={(v) => onExpectedResultChange(v as string[])}
            />
          </div>
        </div>
      </Space>
    </div>
  );
}
