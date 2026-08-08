// 资金流向图 V3 左侧调查面板（设计 §6-§14、§24-§27、§32-§33、§47）。
import { useState } from "react";
import {
  Button,
  Card,
  Checkbox,
  Divider,
  Input,
  InputNumber,
  Modal,
  Segmented,
  Select,
  Slider,
  Space,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import {
  AimOutlined,
  BookOutlined,
  CloudDownloadOutlined,
  ExperimentOutlined,
  HighlightOutlined,
  ReloadOutlined,
  UserOutlined,
} from "@ant-design/icons";
import type { FundFlowAnalysis, FlowPath } from "../fundflow/fundFlowApi";
import type { CoverageQueryResult } from "../smart-download/smartDownloadApi";

export type GraphViewMode = "normal" | "paths" | "entity" | "settlement" | "profit" | "cashout";

export interface GraphFilters {
  onlyLarge: boolean;
  hideContracts: boolean;
  onlyExchange: boolean;
  hideWeak: boolean;
  minAmount: number;
}

export interface GraphBookmark {
  name: string;
  address: string;
  direction: string;
  depth: number;
  mode: GraphViewMode;
  filters: GraphFilters;
  savedAt: string;
  lens?: string;
  valueCoverage?: number;
  highlightPathId?: string | null;
  replayTime?: number;
  nodes?: Array<{ id: string; position: { x: number; y: number } }>;
}

export interface PathQuery {
  terminal: string;
  minAmount: number;
  maxHops: number;
  mustPass: string;
}

export interface TempGroup {
  name: string;
  addresses: string[];
}

export interface ResultRow {
  type: string;
  label: string;
  amount: string;
  pathId?: string;
}

interface FlowLeftPanelProps {
  focusAddress: string | null;
  lightMode: boolean;
  viewMode: GraphViewMode;
  lens: string;
  filters: GraphFilters;
  valueCoverage: number;
  fundFlow: FundFlowAnalysis | null;
  analyzing: boolean;
  bookmarks: GraphBookmark[];
  highlightPathId: string | null;
  selectedNodes: string[];
  groups: TempGroup[];
  coverage: CoverageQueryResult | null;
  resultRows: ResultRow[];
  onLightMode: (light: boolean) => void;
  onViewMode: (m: GraphViewMode) => void;
  onLens: (l: string) => void;
  onFilters: (f: GraphFilters) => void;
  onValueCoverage: (v: number) => void;
  entityCollapse: boolean;
  onEntityCollapse: (v: boolean) => void;
  onAnalyze: () => void;
  onHighlightPath: (id: string | null) => void;
  onSaveBookmark: () => void;
  onRestoreBookmark: (b: GraphBookmark) => void;
  onSmartFill: () => void;
  onOpenAddress: () => void;
  onPathQuery: (q: PathQuery) => void;
  queryResults: FlowPath[];
  onCreateGroup: (name: string) => void;
  onRemoveGroup: (name: string) => void;
  onTestHypothesis: () => void;
  onCommandPalette: () => void;
}

const MODE_OPTIONS = [
  { label: "普通", value: "normal" },
  { label: "路径", value: "paths" },
  { label: "实体", value: "entity" },
  { label: "沉淀", value: "settlement" },
  { label: "获利", value: "profit" },
  { label: "落点", value: "cashout" },
];

const LENS_OPTIONS = ["资金主干", "大额流向", "快速转移", "沉淀", "获利", "交易所落点", "风险暴露", "跨链"];

const PATH_TYPE_LABELS: Record<string, string> = {
  DIRECT_CASHOUT: "直接兑现",
  MULTI_HOP_CASHOUT: "多跳兑现",
  COLLECT_AND_SETTLE: "归集沉淀",
  BRIDGE_EXIT: "跨链出口",
  UNKNOWN: "未知",
};

export default function FlowLeftPanel({
  focusAddress,
  lightMode,
  viewMode,
  lens,
  filters,
  valueCoverage,
  fundFlow,
  analyzing,
  bookmarks,
  highlightPathId,
  selectedNodes,
  groups,
  coverage,
  resultRows,
  onLightMode,
  onViewMode,
  onLens,
  onFilters,
  onValueCoverage,
  entityCollapse,
  onEntityCollapse,
  onAnalyze,
  onHighlightPath,
  onSaveBookmark,
  onRestoreBookmark,
  onSmartFill,
  onOpenAddress,
  onPathQuery,
  queryResults,
  onCreateGroup,
  onRemoveGroup,
  onTestHypothesis,
  onCommandPalette,
}: FlowLeftPanelProps) {
  const [query, setQuery] = useState<PathQuery>({ terminal: "ANY", minAmount: 0, maxHops: 5, mustPass: "" });
  const [groupName, setGroupName] = useState("");
  const [hypothesisOpen, setHypothesisOpen] = useState(false);

  return (
    <aside className="flow-left-panel">
      <div className="flow-left-panel-inner">
        <section className="flow-left-section">
          <Space style={{ width: "100%", justifyContent: "space-between" }}>
            <Typography.Text strong>调查上下文</Typography.Text>
            <Tag color={lightMode ? "gold" : "purple"} onClick={() => onLightMode(!lightMode)} style={{ cursor: "pointer" }}>
              {lightMode ? "白底" : "深色"}
            </Tag>
          </Space>
          <div className="flow-left-meta">
            {focusAddress ? <code title={focusAddress}>{focusAddress.slice(0, 12)}…{focusAddress.slice(-6)}</code> : "未聚焦"}
            <Tag color="blue">BSC</Tag>
          </div>
          <Space size={4} wrap>
            <Button size="small" icon={<ReloadOutlined />} onClick={onAnalyze} loading={analyzing} disabled={!focusAddress}>
              分析关键路径
            </Button>
            <Tooltip title="Ctrl+K 命令面板">
              <Button size="small" icon={<AimOutlined />} onClick={onCommandPalette}>
                命令
              </Button>
            </Tooltip>
          </Space>
          {coverage && (
            <div className="flow-left-coverage">
              本地覆盖：{(coverage.coverage_ratio * 100).toFixed(1)}%
              <Tag color={coverage.full_hit ? "green" : "orange"} style={{ marginLeft: 6 }}>
                {coverage.full_hit ? "完整" : coverage.certification ?? "缺失"}
              </Tag>
            </div>
          )}
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Typography.Text strong>调查透镜（V3）</Typography.Text>
          <Space wrap size={4} style={{ marginTop: 6 }}>
            {LENS_OPTIONS.map((l) => (
              <Tag
                key={l}
                color={lens === l ? "blue" : "default"}
                onClick={() => onLens(lens === l ? "" : l)}
                style={{ cursor: "pointer" }}
              >
                {l}
              </Tag>
            ))}
          </Space>
          <Segmented
            block
            size="small"
            value={viewMode}
            options={MODE_OPTIONS}
            onChange={(v) => onViewMode(v as GraphViewMode)}
            style={{ marginTop: 8 }}
          />
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Typography.Text strong>价值覆盖减噪（§25）</Typography.Text>
          <Slider
            min={50}
            max={100}
            step={5}
            value={valueCoverage}
            onChange={onValueCoverage}
            marks={{ 50: "50%", 80: "80%", 95: "95%", 100: "100%" }}
            tooltip={{ formatter: (v) => `解释 ${v}% 资金` }}
          />
          <Space direction="vertical" size={6} style={{ width: "100%" }}>
            <Checkbox checked={filters.onlyLarge} onChange={(e) => onFilters({ ...filters, onlyLarge: e.target.checked })}>
              仅显示大额
            </Checkbox>
            <Checkbox checked={filters.hideContracts} onChange={(e) => onFilters({ ...filters, hideContracts: e.target.checked })}>
              隐藏合约
            </Checkbox>
            <Checkbox checked={filters.onlyExchange} onChange={(e) => onFilters({ ...filters, onlyExchange: e.target.checked })}>
              仅显示交易所/服务
            </Checkbox>
            <Checkbox checked={filters.hideWeak} onChange={(e) => onFilters({ ...filters, hideWeak: e.target.checked })}>
              隐藏弱边
            </Checkbox>
            <Checkbox checked={entityCollapse} onChange={(e) => onEntityCollapse(e.target.checked)}>
              实体折叠（实体图）
            </Checkbox>
            <Space size={8}>
              <span className="flow-left-label">最小金额</span>
              <InputNumber
                size="small"
                min={0}
                value={filters.minAmount}
                onChange={(v) => onFilters({ ...filters, minAmount: Number(v ?? 0) })}
                style={{ width: 110 }}
              />
            </Space>
          </Space>
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Typography.Text strong>路径查询器（§13-§14）</Typography.Text>
          <Space direction="vertical" size={6} style={{ marginTop: 6, width: "100%" }}>
            <Select
              size="small"
              value={query.terminal}
              onChange={(v) => setQuery({ ...query, terminal: v })}
              options={[
                { value: "ANY", label: "任意终点" },
                { value: "EXCHANGE", label: "交易所" },
                { value: "SETTLEMENT", label: "沉淀" },
                { value: "BRIDGE", label: "跨链" },
              ]}
            />
            <Space size={8}>
              <span className="flow-left-label">金额≥</span>
              <InputNumber size="small" min={0} value={query.minAmount} onChange={(v) => setQuery({ ...query, minAmount: Number(v ?? 0) })} style={{ width: 100 }} />
              <span className="flow-left-label">跳数≤</span>
              <InputNumber size="small" min={1} max={6} value={query.maxHops} onChange={(v) => setQuery({ ...query, maxHops: Number(v ?? 5) })} style={{ width: 70 }} />
            </Space>
            <Input size="small" placeholder="必须经过地址（可选）" value={query.mustPass} onChange={(e) => setQuery({ ...query, mustPass: e.target.value })} />
            <Button size="small" type="primary" ghost onClick={() => onPathQuery(query)}>
              查询路径
            </Button>
          </Space>
          <div className="flow-left-path-list">
            {(queryResults.length > 0 ? queryResults : fundFlow?.paths ?? []).slice(0, 12).map((p: FlowPath) => (
              <div
                key={p.id}
                className={`flow-left-path-item${highlightPathId === p.id ? " is-highlight" : ""}`}
                onClick={() => onHighlightPath(highlightPathId === p.id ? null : p.id)}
              >
                <Space size={4}>
                  <Tag color={p.path_type === "UNKNOWN" ? "default" : "red"} style={{ marginRight: 0 }}>
                    {PATH_TYPE_LABELS[p.path_type] ?? p.path_type}
                  </Tag>
                  <span className="flow-left-path-score">{p.score.toFixed(2)}</span>
                </Space>
                <div className="flow-left-path-hops">
                  {p.nodes.map((n, i) => (
                    <span key={`${p.id}-${i}`}>
                      {i > 0 ? " → " : ""}
                      {n.address.slice(0, 8)}…
                    </span>
                  ))}
                </div>
              </div>
            ))}
            {(queryResults.length === 0 && (fundFlow?.paths?.length ?? 0) === 0) && (
              <div className="flow-left-empty">点击「分析关键路径」或查询</div>
            )}
          </div>
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Space style={{ width: "100%", justifyContent: "space-between" }}>
            <Typography.Text strong>多选工作区（§32-§33）</Typography.Text>
            <Tag>{selectedNodes.length} 已选</Tag>
          </Space>
          <Space wrap size={4} style={{ marginTop: 6 }}>
            <Input
              size="small"
              placeholder="临时组名"
              value={groupName}
              onChange={(e) => setGroupName(e.target.value)}
              style={{ width: 120 }}
            />
            <Button
              size="small"
              disabled={selectedNodes.length < 2 || !groupName}
              onClick={() => {
                onCreateGroup(groupName);
                setGroupName("");
              }}
            >
              建立临时组
            </Button>
            <Button size="small" icon={<ExperimentOutlined />} disabled={selectedNodes.length < 2} onClick={() => setHypothesisOpen(true)}>
              建立假设
            </Button>
          </Space>
          <div className="flow-left-bookmarks">
            {groups.map((g) => (
              <div key={g.name} className="flow-left-bookmark-item">
                <HighlightOutlined /> {g.name} · {g.addresses.length} 个
                <Button size="small" type="text" onClick={() => onRemoveGroup(g.name)}>
                  删
                </Button>
              </div>
            ))}
            {groups.length === 0 && <div className="flow-left-empty">多选节点后建立临时组</div>}
          </div>
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Typography.Text strong>结果数据联动（§39）</Typography.Text>
          <div className="flow-left-bookmarks">
            {resultRows.slice(0, 10).map((r, i) => (
              <div
                key={`${r.type}-${i}`}
                className="flow-left-bookmark-item"
                onClick={() => r.pathId && onHighlightPath(highlightPathId === r.pathId ? null : r.pathId)}
              >
                <Tag style={{ marginRight: 4 }}>{r.type}</Tag>
                {r.label} · {r.amount}
              </div>
            ))}
            {resultRows.length === 0 && <div className="flow-left-empty">选中节点后自动过滤结果</div>}
          </div>
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Typography.Text strong>书签 / 快照</Typography.Text>
          <Button size="small" icon={<BookOutlined />} onClick={onSaveBookmark} disabled={!focusAddress}>
            保存
          </Button>
          <div className="flow-left-bookmarks">
            {bookmarks.length === 0 && <div className="flow-left-empty">暂无书签</div>}
            {bookmarks.map((b, i) => (
              <div key={`${b.name}-${i}`} className="flow-left-bookmark-item" onClick={() => onRestoreBookmark(b)}>
                <HighlightOutlined /> {b.name}
                <span className="flow-left-bookmark-meta">{b.savedAt}</span>
              </div>
            ))}
          </div>
        </section>

        <Divider style={{ margin: "8px 0" }} />

        <section className="flow-left-section">
          <Typography.Text strong>节点操作</Typography.Text>
          <Space direction="vertical" size={6} style={{ marginTop: 8, width: "100%" }}>
            <Button size="small" icon={<CloudDownloadOutlined />} disabled={!focusAddress} onClick={onSmartFill}>
              自动补数（Smart Download）
            </Button>
            <Button size="small" icon={<UserOutlined />} disabled={!focusAddress} onClick={onOpenAddress}>
              进入地址画像
            </Button>
          </Space>
        </section>
      </div>

      <Modal
        open={hypothesisOpen}
        onCancel={() => setHypothesisOpen(false)}
        onOk={() => {
          onTestHypothesis();
          setHypothesisOpen(false);
        }}
        title="调查假设：这些地址可能属于同一实体？"
        width={520}
      >
        <p>将调用 Entity Intelligence 检查共同实体、共同 Sweep、共同 Funding 与对手重叠。</p>
        <Space wrap>
          {selectedNodes.map((a) => (
            <Tag key={a}>{a.slice(0, 10)}…</Tag>
          ))}
        </Space>
      </Modal>
    </aside>
  );
}
