// 资金流向图 V3 统一左侧调查侧边栏（设计 §9、§26-§27、§32-§35）：
// 按「调查 / 分析 / 工作区」归类，全部可折叠。
import { useState } from "react";
import {
  Button,
  Checkbox,
  Collapse,
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
  ThunderboltOutlined,
  UserOutlined,
} from "@ant-design/icons";
import type { FundFlowAnalysis, FlowPath } from "../fundflow/fundFlowApi";
import type { CoverageQueryResult } from "../smart-download/smartDownloadApi";
import type { GraphBookmark, GraphFilters, GraphViewMode, PathQuery, ResultRow, TempGroup } from "./FlowLeftPanel";
import type { Hypothesis } from "./FlowV3Extras";

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

interface InvestigationSidebarProps {
  focusAddress: string | null;
  lightMode: boolean;
  viewMode: GraphViewMode;
  lens: string;
  filters: GraphFilters;
  valueCoverage: number;
  entityCollapse: boolean;
  fundFlow: FundFlowAnalysis | null;
  analyzing: boolean;
  bookmarks: GraphBookmark[];
  highlightPathId: string | null;
  selectedNodes: string[];
  groups: TempGroup[];
  coverage: CoverageQueryResult | null;
  resultRows: ResultRow[];
  multiRoots: string[];
  multiRootInput: string;
  showCommonOnly: boolean;
  hypotheses: Hypothesis[];
  copilot: CopilotTip[];
  timeline: TimelineEvent[];
  canUndo: boolean;
  canRedo: boolean;
  queryResults: FlowPath[];
  onLightMode: (light: boolean) => void;
  onViewMode: (m: GraphViewMode) => void;
  onLens: (l: string) => void;
  onFilters: (f: GraphFilters) => void;
  onValueCoverage: (v: number) => void;
  onEntityCollapse: (v: boolean) => void;
  onAnalyze: () => void;
  onHighlightPath: (id: string | null) => void;
  onSaveBookmark: () => void;
  onRestoreBookmark: (b: GraphBookmark) => void;
  onSmartFill: () => void;
  onOpenAddress: () => void;
  onPathQuery: (q: PathQuery) => void;
  onCreateGroup: (name: string) => void;
  onRemoveGroup: (name: string) => void;
  onTestHypothesis: () => void;
  onCommandPalette: () => void;
  canShowGlobal: boolean;
  onShowGlobalExtension: () => void;
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

const MODE_OPTIONS = [
  { label: "普通", value: "normal" },
  { label: "路径", value: "paths" },
  { label: "实体", value: "entity" },
  { label: "沉淀", value: "settlement" },
  { label: "获利", value: "profit" },
  { label: "落点", value: "cashout" },
];

const LENS_OPTIONS = ["资金主干", "大额流向", "快速转移", "沉淀", "获利", "交易所落点", "风险暴露", "跨链"];
const HYP_STATUS = ["DRAFT", "TESTING", "SUPPORTED", "WEAK", "CONTRADICTED", "UNRESOLVED"];

const PATH_TYPE_LABELS: Record<string, string> = {
  DIRECT_CASHOUT: "直接兑现",
  MULTI_HOP_CASHOUT: "多跳兑现",
  COLLECT_AND_SETTLE: "归集沉淀",
  BRIDGE_EXIT: "跨链出口",
  UNKNOWN: "未知",
};

export default function InvestigationSidebar(props: InvestigationSidebarProps) {
  const [query, setQuery] = useState<PathQuery>({ terminal: "ANY", minAmount: 0, maxHops: 5, mustPass: "" });
  const [groupName, setGroupName] = useState("");
  const [hypothesisOpen, setHypothesisOpen] = useState(false);
  const {
    focusAddress, lightMode, viewMode, lens, filters, valueCoverage, entityCollapse, fundFlow,
    analyzing, bookmarks, highlightPathId, selectedNodes, groups, coverage, resultRows,
    multiRoots, multiRootInput, showCommonOnly, hypotheses, copilot, timeline,
    canUndo, canRedo, queryResults,
    onLightMode, onViewMode, onLens, onFilters, onValueCoverage, onEntityCollapse, onAnalyze,
    onHighlightPath, onSaveBookmark, onRestoreBookmark, onSmartFill, onOpenAddress, onPathQuery,
    onCreateGroup, onRemoveGroup, onTestHypothesis, onCommandPalette, canShowGlobal, onShowGlobalExtension,
    onMultiRootInput, onAddMultiRoot, onRemoveMultiRoot, onShowCommonOnly, onAddHypothesis,
    onHypothesisStatus, onCopilotAction, onTimelineClick, onSaveBaseline, onCompareDiff, onUndo, onRedo,
  } = props;

  const pathList = (queryResults.length > 0 ? queryResults : fundFlow?.paths ?? []).slice(0, 12);

  return (
    <aside className="flow-left-panel">
      <div className="flow-left-panel-inner">
        <Collapse
          ghost
          size="small"
          defaultActiveKey={["investigation"]}
          items={[
            {
              key: "investigation",
              label: "调查",
              children: (
                <div className="flow-left-section">
                  <Space style={{ width: "100%", justifyContent: "space-between" }}>
                    <Typography.Text strong>当前焦点</Typography.Text>
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
                    <Button size="small" disabled={!canShowGlobal} onClick={onShowGlobalExtension}>
                      全局延伸
                    </Button>
                    <Tooltip title="Ctrl+K 命令面板">
                      <Button size="small" icon={<AimOutlined />} onClick={onCommandPalette}>命令</Button>
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
                  <Typography.Text strong style={{ marginTop: 8 }}>调查透镜</Typography.Text>
                  <Space wrap size={4}>
                    {LENS_OPTIONS.map((l) => (
                      <Tag key={l} color={lens === l ? "blue" : "default"} onClick={() => onLens(lens === l ? "" : l)} style={{ cursor: "pointer" }}>
                        {l}
                      </Tag>
                    ))}
                  </Space>
                  <Segmented block size="small" value={viewMode} options={MODE_OPTIONS} onChange={(v) => onViewMode(v as GraphViewMode)} />
                  <Typography.Text strong style={{ marginTop: 8 }}>路径查询器</Typography.Text>
                  <Select size="small" value={query.terminal} onChange={(v) => setQuery({ ...query, terminal: v })}
                    options={[{ value: "ANY", label: "任意终点" }, { value: "EXCHANGE", label: "交易所" }, { value: "SETTLEMENT", label: "沉淀" }, { value: "BRIDGE", label: "跨链" }]} />
                  <Space size={8}>
                    <span className="flow-left-label">金额≥</span>
                    <InputNumber size="small" min={0} value={query.minAmount} onChange={(v) => setQuery({ ...query, minAmount: Number(v ?? 0) })} style={{ width: 100 }} />
                    <span className="flow-left-label">跳数≤</span>
                    <InputNumber size="small" min={1} max={6} value={query.maxHops} onChange={(v) => setQuery({ ...query, maxHops: Number(v ?? 5) })} style={{ width: 70 }} />
                  </Space>
                  <Input size="small" placeholder="必须经过地址（可选）" value={query.mustPass} onChange={(e) => setQuery({ ...query, mustPass: e.target.value })} />
                  <Button size="small" type="primary" ghost onClick={() => onPathQuery(query)}>查询路径</Button>
                </div>
              ),
            },
            {
              key: "analysis",
              label: "分析",
              children: (
                <div className="flow-left-section">
                  <Typography.Text strong>价值覆盖减噪</Typography.Text>
                  <Slider min={50} max={100} step={5} value={valueCoverage} onChange={onValueCoverage}
                    marks={{ 50: "50%", 80: "80%", 95: "95%", 100: "100%" }}
                    tooltip={{ formatter: (v) => `解释 ${v}% 资金` }} />
                  <Checkbox checked={filters.onlyLarge} onChange={(e) => onFilters({ ...filters, onlyLarge: e.target.checked })}>仅显示大额</Checkbox>
                  <Checkbox checked={filters.hideContracts} onChange={(e) => onFilters({ ...filters, hideContracts: e.target.checked })}>隐藏合约</Checkbox>
                  <Checkbox checked={filters.onlyExchange} onChange={(e) => onFilters({ ...filters, onlyExchange: e.target.checked })}>仅显示交易所/服务</Checkbox>
                  <Checkbox checked={filters.hideWeak} onChange={(e) => onFilters({ ...filters, hideWeak: e.target.checked })}>隐藏弱边</Checkbox>
                  <Checkbox checked={entityCollapse} onChange={(e) => onEntityCollapse(e.target.checked)}>实体折叠（实体图）</Checkbox>
                  <Space size={8}>
                    <span className="flow-left-label">最小金额</span>
                    <InputNumber size="small" min={0} value={filters.minAmount} onChange={(v) => onFilters({ ...filters, minAmount: Number(v ?? 0) })} style={{ width: 110 }} />
                  </Space>
                  <Typography.Text strong style={{ marginTop: 8 }}>关键路径（{pathList.length}）</Typography.Text>
                  <div className="flow-left-path-list">
                    {pathList.map((p) => (
                      <div key={p.id} className={`flow-left-path-item${highlightPathId === p.id ? " is-highlight" : ""}`}
                        onClick={() => onHighlightPath(highlightPathId === p.id ? null : p.id)}>
                        <Space size={4}>
                          <Tag color={p.path_type === "UNKNOWN" ? "default" : "red"} style={{ marginRight: 0 }}>{PATH_TYPE_LABELS[p.path_type] ?? p.path_type}</Tag>
                          <span className="flow-left-path-score">{p.score.toFixed(2)}</span>
                        </Space>
                        <div className="flow-left-path-hops">
                          {p.nodes.map((n, i) => (
                            <span key={`${p.id}-${i}`}>{i > 0 ? " → " : ""}{n.address.slice(0, 8)}…</span>
                          ))}
                        </div>
                      </div>
                    ))}
                    {pathList.length === 0 && <div className="flow-left-empty">暂无路径，点击「分析关键路径」或查询</div>}
                  </div>
                  <Typography.Text strong style={{ marginTop: 8 }}>结果数据联动</Typography.Text>
                  <div className="flow-left-bookmarks">
                    {resultRows.slice(0, 10).map((r, i) => (
                      <div key={`${r.type}-${i}`} className="flow-left-bookmark-item"
                        onClick={() => r.pathId && onHighlightPath(highlightPathId === r.pathId ? null : r.pathId)}>
                        <Tag style={{ marginRight: 4 }}>{r.type}</Tag>{r.label} · {r.amount}
                      </div>
                    ))}
                    {resultRows.length === 0 && <div className="flow-left-empty">选中节点后自动过滤结果</div>}
                  </div>
                  <Typography.Text strong style={{ marginTop: 8 }}>Copilot 建议</Typography.Text>
                  <div className="flow-left-bookmarks">
                    {copilot.map((t) => (
                      <div key={t.id} className="flow-left-bookmark-item" onClick={() => onCopilotAction(t.action)}>
                        <ThunderboltOutlined /> {t.text}
                      </div>
                    ))}
                    {copilot.length === 0 && <div className="flow-left-empty">暂无建议</div>}
                  </div>
                  <Typography.Text strong style={{ marginTop: 8 }}>时间线事件</Typography.Text>
                  <div className="flow-left-bookmarks">
                    {timeline.slice(0, 30).map((ev, i) => (
                      <div key={`${ev.address}-${i}`} className="flow-left-bookmark-item" onClick={() => onTimelineClick(ev.address)}>
                        <Tag style={{ marginRight: 4 }}>{ev.type}</Tag>
                        {ev.time ? new Date(ev.time * 1000).toISOString().slice(0, 16) : "—"} · {ev.summary}
                      </div>
                    ))}
                    {timeline.length === 0 && <div className="flow-left-empty">暂无时间线事件</div>}
                  </div>
                </div>
              ),
            },
            {
              key: "workspace",
              label: "工作区",
              children: (
                <div className="flow-left-section">
                  <Typography.Text strong>多根联合调查</Typography.Text>
                  <Space size={6}>
                    <Input size="small" placeholder="根地址" value={multiRootInput} onChange={(e) => onMultiRootInput(e.target.value)} style={{ width: 140 }} />
                    <Button size="small" onClick={onAddMultiRoot}>添加</Button>
                  </Space>
                  <Space wrap size={4}>
                    {multiRoots.map((r) => (
                      <Tag key={r} closable onClose={() => onRemoveMultiRoot(r)}>{r.slice(0, 8)}…</Tag>
                    ))}
                  </Space>
                  <Checkbox checked={showCommonOnly} onChange={(e) => onShowCommonOnly(e.target.checked)} disabled={multiRoots.length < 2}>
                    仅显示共同节点
                  </Checkbox>
                  <Space style={{ width: "100%", justifyContent: "space-between", marginTop: 8 }}>
                    <Typography.Text strong>图谱 Diff / 历史</Typography.Text>
                  </Space>
                  <Space size={6}>
                    <Button size="small" onClick={onSaveBaseline}>保存基线</Button>
                    <Button size="small" onClick={onCompareDiff}>对比当前</Button>
                    <Button size="small" disabled={!canUndo} onClick={onUndo}>后退</Button>
                    <Button size="small" disabled={!canRedo} onClick={onRedo}>前进</Button>
                  </Space>
                  <Space style={{ width: "100%", justifyContent: "space-between", marginTop: 8 }}>
                    <Typography.Text strong>多选 / 临时组</Typography.Text>
                    <Tag>{selectedNodes.length} 已选</Tag>
                  </Space>
                  <Space size={4}>
                    <Input size="small" placeholder="临时组名" value={groupName} onChange={(e) => setGroupName(e.target.value)} style={{ width: 120 }} />
                    <Button size="small" disabled={selectedNodes.length < 2 || !groupName}
                      onClick={() => { onCreateGroup(groupName); setGroupName(""); }}>建立临时组</Button>
                    <Button size="small" icon={<ExperimentOutlined />} disabled={selectedNodes.length < 2} onClick={() => setHypothesisOpen(true)}>建立假设</Button>
                  </Space>
                  <Typography.Text strong style={{ marginTop: 8 }}>假设工作区</Typography.Text>
                  <div className="flow-left-bookmarks">
                    {hypotheses.map((h) => (
                      <div key={h.name} className="flow-left-bookmark-item">
                        <div>{h.name} · {h.addresses.length} 个</div>
                        <Select size="small" value={h.status} onChange={(v) => onHypothesisStatus(h.name, v)}
                          options={HYP_STATUS.map((s) => ({ value: s, label: s }))} style={{ width: 130, marginTop: 4 }} />
                      </div>
                    ))}
                    {hypotheses.length === 0 && <div className="flow-left-empty">暂无假设</div>}
                  </div>
                  <Space style={{ width: "100%", justifyContent: "space-between", marginTop: 8 }}>
                    <Typography.Text strong>书签 / 快照</Typography.Text>
                    <Button size="small" icon={<BookOutlined />} onClick={onSaveBookmark} disabled={!focusAddress}>保存</Button>
                  </Space>
                  <div className="flow-left-bookmarks">
                    {bookmarks.map((b, i) => (
                      <div key={`${b.name}-${i}`} className="flow-left-bookmark-item" onClick={() => onRestoreBookmark(b)}>
                        <HighlightOutlined /> {b.name}
                        <span className="flow-left-bookmark-meta">{b.savedAt}</span>
                      </div>
                    ))}
                    {bookmarks.length === 0 && <div className="flow-left-empty">暂无书签</div>}
                  </div>
                  <Typography.Text strong style={{ marginTop: 8 }}>节点操作</Typography.Text>
                  <Button size="small" icon={<CloudDownloadOutlined />} disabled={!focusAddress} onClick={onSmartFill}>自动补数</Button>
                  <Button size="small" icon={<UserOutlined />} disabled={!focusAddress} onClick={onOpenAddress}>进入地址画像</Button>
                </div>
              ),
            },
          ]}
        />
      </div>
      <Modal open={hypothesisOpen} onCancel={() => setHypothesisOpen(false)}
        onOk={() => { onTestHypothesis(); setHypothesisOpen(false); }}
        title="调查假设：这些地址可能属于同一实体？" width={520}>
        <p>将调用 Entity Intelligence 检查共同实体、共同 Sweep、共同 Funding 与对手重叠。</p>
        <Space wrap>
          {selectedNodes.map((a) => <Tag key={a}>{a.slice(0, 10)}…</Tag>)}
        </Space>
      </Modal>
    </aside>
  );
}
