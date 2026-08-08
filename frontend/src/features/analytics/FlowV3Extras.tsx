// V3 扩展调查面板：多根联合、图谱 Diff、假设工作区、Copilot、时间线、历史（§22-§23、§27、§32-§35、§48）。
import { useState } from "react";
import { Button, Checkbox, Divider, Input, Select, Space, Tag, Typography } from "antd";
import { ExperimentOutlined, HistoryOutlined, NodeIndexOutlined, ThunderboltOutlined } from "@ant-design/icons";

export interface Hypothesis {
  name: string;
  addresses: string[];
  status: string;
  createdAt: string;
}

interface TimelineEvent {
  time: number;
  type: string;
  summary: string;
  amount: string;
  address: string;
}

interface CopilotTip {
  id: string;
  text: string;
  action: string;
}

interface FlowV3ExtrasProps {
  multiRoots: string[];
  multiRootInput: string;
  showCommonOnly: boolean;
  hypotheses: Hypothesis[];
  copilot: CopilotTip[];
  timeline: TimelineEvent[];
  canUndo: boolean;
  canRedo: boolean;
  onMultiRootInput: (v: string) => void;
  onAddMultiRoot: () => void;
  onRemoveMultiRoot: (addr: string) => void;
  onShowCommonOnly: (v: boolean) => void;
  onAddHypothesis: () => void;
  onHypothesisStatus: (name: string, status: string) => void;
  onCopilotAction: (action: string) => void;
  onTimelineClick: (address: string) => void;
  onSaveBaseline: () => void;
  onCompareDiff: () => void;
  onUndo: () => void;
  onRedo: () => void;
}

const HYP_STATUS = ["DRAFT", "TESTING", "SUPPORTED", "WEAK", "CONTRADICTED", "UNRESOLVED"];

export default function FlowV3Extras({
  multiRoots,
  multiRootInput,
  showCommonOnly,
  hypotheses,
  copilot,
  timeline,
  canUndo,
  canRedo,
  onMultiRootInput,
  onAddMultiRoot,
  onRemoveMultiRoot,
  onShowCommonOnly,
  onAddHypothesis,
  onHypothesisStatus,
  onCopilotAction,
  onTimelineClick,
  onSaveBaseline,
  onCompareDiff,
  onUndo,
  onRedo,
}: FlowV3ExtrasProps) {
  return (
    <aside className="flow-v3-extras">
      <div className="flow-left-panel-inner">
        <section className="flow-left-section">
          <Space style={{ width: "100%", justifyContent: "space-between" }}>
            <Typography.Text strong>多根联合调查</Typography.Text>
            <NodeIndexOutlined />
          </Space>
          <Space size={6} style={{ marginTop: 6 }}>
            <Input
              size="small"
              placeholder="根地址"
              value={multiRootInput}
              onChange={(e) => onMultiRootInput(e.target.value)}
              style={{ width: 140 }}
            />
            <Button size="small" onClick={onAddMultiRoot}>
              添加
            </Button>
          </Space>
          <Space wrap size={4}>
            {multiRoots.map((r) => (
              <Tag key={r} closable onClose={() => onRemoveMultiRoot(r)}>
                {r.slice(0, 8)}…
              </Tag>
            ))}
          </Space>
          <Checkbox checked={showCommonOnly} onChange={(e) => onShowCommonOnly(e.target.checked)} disabled={multiRoots.length < 2}>
            仅显示共同节点
          </Checkbox>
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Space style={{ width: "100%", justifyContent: "space-between" }}>
            <Typography.Text strong>图谱 Diff</Typography.Text>
            <HistoryOutlined />
          </Space>
          <Space size={6} style={{ marginTop: 6 }}>
            <Button size="small" onClick={onSaveBaseline}>
              保存基线
            </Button>
            <Button size="small" onClick={onCompareDiff}>
              对比当前
            </Button>
          </Space>
          <Space size={6}>
            <Button size="small" disabled={!canUndo} onClick={onUndo}>
              后退
            </Button>
            <Button size="small" disabled={!canRedo} onClick={onRedo}>
              前进
            </Button>
          </Space>
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Space style={{ width: "100%", justifyContent: "space-between" }}>
            <Typography.Text strong>假设工作区</Typography.Text>
            <Button size="small" icon={<ExperimentOutlined />} onClick={onAddHypothesis}>
              从多选建立
            </Button>
          </Space>
          <div className="flow-left-bookmarks">
            {hypotheses.map((h) => (
              <div key={h.name} className="flow-left-bookmark-item">
                <div>{h.name} · {h.addresses.length} 个</div>
                <Select
                  size="small"
                  value={h.status}
                  onChange={(v) => onHypothesisStatus(h.name, v)}
                  options={HYP_STATUS.map((s) => ({ value: s, label: s }))}
                  style={{ width: 130, marginTop: 4 }}
                />
              </div>
            ))}
            {hypotheses.length === 0 && <div className="flow-left-empty">暂无假设</div>}
          </div>
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Typography.Text strong>调查 Copilot 建议</Typography.Text>
          <div className="flow-left-bookmarks">
            {copilot.map((t) => (
              <div key={t.id} className="flow-left-bookmark-item" onClick={() => onCopilotAction(t.action)}>
                <ThunderboltOutlined /> {t.text}
              </div>
            ))}
            {copilot.length === 0 && <div className="flow-left-empty">暂无建议</div>}
          </div>
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Typography.Text strong>时间线事件</Typography.Text>
          <div className="flow-left-bookmarks">
            {timeline.slice(0, 30).map((ev, i) => (
              <div key={`${ev.address}-${i}`} className="flow-left-bookmark-item" onClick={() => onTimelineClick(ev.address)}>
                <Tag style={{ marginRight: 4 }}>{ev.type}</Tag>
                {ev.time ? new Date(ev.time * 1000).toISOString().slice(0, 16) : "—"} · {ev.summary}
              </div>
            ))}
            {timeline.length === 0 && <div className="flow-left-empty">暂无时间线事件</div>}
          </div>
        </section>
      </div>
    </aside>
  );
}

