// 地址关系图 V2.0 UI 整改 — 画布外壳（设计 §5.3/§10/§11/§12）
//
// 职责：ReactFlow 画布 + 聚焦标签 + 计数徽章 + 左下控制器 + 底部图例 + 退出聚焦。
// - NODE_TYPES / EDGE_TYPES 模块级常量（设计 §12.2 禁止 render 内创建）；
// - 节点/边组件 React.memo，key 稳定（设计 §12.2）；
// - 视口交互期间给容器加 flow-workspace-interacting 类，暂停流向动画（§12.4）。

import { AimOutlined, CloseOutlined } from "@ant-design/icons";
import {
  Background,
  BaseEdge,
  Controls,
  EdgeLabelRenderer,
  Handle,
  Position,
  ReactFlow,
  getBezierPath,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
  type OnEdgesChange,
  type OnNodesChange,
  type ReactFlowInstance,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Button, Empty, Spin } from "antd";
import { memo, useCallback, useRef, useState } from "react";
import type { GraphData } from "./analyticsApi";
import { DANGER_COLOR, RELATION_COLORS, fmtEdgeAmount, fmtAmount, riskTone, type WorkspaceRelation } from "./flowWorkspaceGraph";
import type { EnhancedEdgeData, EnhancedNodeData, FocusSelection } from "./graphUpgrade";

// 模块级常量（设计 §12.2/§12.5）：组件声明之后引用（const 无提升）
interface FlowCanvasShellProps {
  loading: boolean;
  graph: GraphData | null;
  nodes: Node[];
  edges: Edge[];
  focus: FocusSelection | null;
  truncated: boolean;
  onNodesChange: OnNodesChange;
  onEdgesChange: OnEdgesChange;
  onInit: (instance: ReactFlowInstance) => void;
  onNodeClick: (event: React.MouseEvent, node: Node) => void;
  onExitFocus: () => void;
  onViewportChange: (zoom: number) => void;
  zoomLevel: "far" | "medium" | "near";
  onSelectionChange: (nodeIds: string[]) => void;
}

function roleLabel(meta: EnhancedNodeData, relation: WorkspaceRelation): string {
  const kind = meta.kind === "合约" ? "合约" : "地址";
  if (relation === "selected") return `核心节点 · ${kind}`;
  if (relation === "upstream") return `上游来源 · ${kind} · L${-meta.layer}`;
  if (relation === "downstream") return `下游去向 · ${kind} · L${meta.layer}`;
  return `${kind} · 全局`;
}

/**
 * 地址节点（设计 §10）：390×64、圆角 9px、完整地址（overflow-wrap:anywhere）、
 * 角色/标签、选中态。只改 class 不重建节点。
 */
const AddressNode = memo(function AddressNode({ data, selected }: NodeProps) {
  const meta = data as unknown as EnhancedNodeData;
  const relation: WorkspaceRelation = (meta.relation as WorkspaceRelation | undefined) ?? "global";
  const selectedClass = selected || relation === "selected" ? " is-selected" : "";
  return (
    <div
      className={`focus-address-node relation-${relation}${selectedClass}`}
      title={meta.address}
    >
      <span className="focus-address-dot" />
      <span className="focus-address-main">
        <span className="focus-address-text">{meta.address}</span>
        <span className="focus-address-role">{roleLabel(meta, relation)}</span>
      </span>
      <span className="focus-address-side">
        <span className={`focus-address-risk ${riskTone(meta.risk)}`}>{meta.risk.toFixed(0)}</span>
        <span className="focus-address-flow" title={`图边流入 ${fmtAmount(meta.inAmount)} / 流出 ${fmtAmount(meta.outAmount)}`}>
          入 {fmtAmount(meta.inAmount)} · 出 {fmtAmount(meta.outAmount)}
        </span>
      </span>
      {meta.isRoot ? <span className="focus-selected-label">已选择</span> : null}
      <Handle type="target" position={Position.Left} />
      <Handle type="source" position={Position.Right} />
    </div>
  );
});

/**
 * 边（设计 §11）：三次贝塞尔 + 箭头指向真实接收方 + 金额/笔数标签。
 * vector-effect 由 CSS 统一设置（non-scaling-stroke）。
 */
const FlowEdgeView = memo(function FlowEdgeView({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  markerEnd,
  style,
}: EdgeProps) {
  const meta = data as unknown as EnhancedEdgeData;
  const [preview, setPreview] = useState(false);
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    curvature: 0.42,
  });
  const timeline = (meta.timeline ?? []) as Array<{ time: number; amount: number }>;
  const first = timeline.length > 0 ? new Date(timeline[0].time * 1000).toISOString().slice(0, 10) : "—";
  const last = timeline.length > 0 ? new Date(timeline[timeline.length - 1].time * 1000).toISOString().slice(0, 10) : "—";
  const maxAmt = Math.max(1, ...timeline.map((t) => t.amount));
  const bars = timeline
    .slice(-24)
    .map((t) => {
      const level = Math.max(1, Math.round((t.amount / maxAmt) * 7));
      return "▁▂▃▄▅▆▇"[level - 1] ?? "▁";
    })
    .join("");
  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} style={style} />
      <EdgeLabelRenderer>
        <div
          className="analytics-flow-edge-label"
          onMouseEnter={() => setPreview(true)}
          onMouseLeave={() => setPreview(false)}
          style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
        >
          <strong>{fmtEdgeAmount(meta.amount)}</strong>
          <span>{meta.token ?? "—"} · {meta.txCount} 笔</span>
        </div>
        {preview && timeline.length > 0 && (
          <div
            className="analytics-edge-preview"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY - 34}px)` }}
          >
            <div>首次 {first} · 最后 {last}</div>
            <div className="analytics-edge-bars">{bars}</div>
          </div>
        )}
      </EdgeLabelRenderer>
    </>
  );
});

function LegendItem({ color, label, shape = "line" }: { color: string; label: string; shape?: "line" | "dot" }) {
  return (
    <span className="flow-legend-item">
      <i className={shape} style={{ background: color }} />
      {label}
    </span>
  );
}

// 模块级常量（设计 §12.2/§12.5）：组件声明之后引用（const 无提升）
const NODE_TYPES = { address: AddressNode };
const EDGE_TYPES = { flow: FlowEdgeView };

export default function FlowCanvasShell({
  loading,
  graph,
  nodes,
  edges,
  focus,
  truncated,
  onNodesChange,
  onEdgesChange,
  onInit,
  onNodeClick,
  onExitFocus,
  onViewportChange,
  zoomLevel,
  onSelectionChange,
}: FlowCanvasShellProps) {
  const shellRef = useRef<HTMLDivElement | null>(null);
  const idleTimer = useRef<number>(0);

  // 视口交互控制器（设计 §12.4）：交互中暂停动画/滤镜，空闲 140ms 后恢复
  const beginViewportInteraction = useCallback(() => {
    window.clearTimeout(idleTimer.current);
    shellRef.current?.classList.add("flow-workspace-interacting");
  }, []);
  const endViewportInteraction = useCallback(() => {
    window.clearTimeout(idleTimer.current);
    idleTimer.current = window.setTimeout(() => {
      shellRef.current?.classList.remove("flow-workspace-interacting");
    }, 140);
  }, []);

  return (
    <div className={`flow-canvas-shell zoom-${zoomLevel}`} ref={shellRef}>
      {loading ? (
        <div className="flow-canvas-center-state">
          <Spin size="large" />
        </div>
      ) : !graph ? (
        <div className="flow-canvas-center-state">
          <Empty description="图谱数据尚未生成，请先完成图谱分析" image={Empty.PRESENTED_IMAGE_SIMPLE} />
        </div>
      ) : nodes.length === 0 ? (
        <div className="flow-canvas-center-state">
          <Empty description="当前筛选条件下没有可展示的关系" image={Empty.PRESENTED_IMAGE_SIMPLE} />
        </div>
      ) : (
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={NODE_TYPES}
          edgeTypes={EDGE_TYPES}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onInit={onInit}
          onNodeClick={onNodeClick}
          onMoveStart={beginViewportInteraction}
          onMoveEnd={endViewportInteraction}
          onMove={(_, viewport) => onViewportChange(viewport.zoom)}
          onSelectionChange={(params) => onSelectionChange(params.nodes.map((n) => String(n.id)))}
          fitView
          fitViewOptions={{ padding: 0.16, maxZoom: 1 }}
          minZoom={0.12}
          maxZoom={1.6}
          nodesConnectable={false}
          proOptions={{ hideAttribution: true }}
        >
          <Background color="#17324a" gap={22} size={1} />
          <Controls showInteractive={false} />
        </ReactFlow>
      )}

      {/* 画布左上：模式标签（设计 §5.3/§7.2） */}
      <div className="flow-canvas-mode-label">
        {focus ? (
          <>
            <span className="flow-canvas-mode-title"><AimOutlined /> 聚焦模式</span>
            <span className="flow-canvas-mode-hint">点击任一相邻地址可继续追踪</span>
          </>
        ) : (
          <>
            <span className="flow-canvas-mode-title">全局视图</span>
            <span className="flow-canvas-mode-hint">搜索地址或点击任一地址开始聚焦追踪</span>
          </>
        )}
      </div>

      {/* 画布右上：计数徽章（设计 §5.1/§7.2） */}
      <div className="flow-canvas-count-badge">
        当前显示 <strong>{nodes.length}</strong> 个地址 · <strong>{edges.length}</strong> 组关系
        {truncated ? <span className="flow-canvas-truncated">已截断</span> : null}
      </div>

      {/* 底部图例：常驻（设计 §5.1） */}
      <div className="flow-legend">
        <LegendItem color={RELATION_COLORS.selected} label="选中地址" shape="dot" />
        <LegendItem color={RELATION_COLORS.upstream} label="上游来源" />
        <LegendItem color={RELATION_COLORS.downstream} label="下游去向" />
        <LegendItem color={RELATION_COLORS.exchange} label="交易所公开标记" />
        <LegendItem color={RELATION_COLORS.global} label="未聚焦节点" />
        <LegendItem color={DANGER_COLOR} label="风险地址" shape="dot" />
      </div>

      {/* 画布右下：退出聚焦（设计 §5.1/§7.4） */}
      {focus ? (
        <Button
          className="flow-exit-focus"
          size="small"
          icon={<CloseOutlined />}
          onClick={onExitFocus}
          aria-label="退出聚焦"
        >
          退出聚焦
        </Button>
      ) : null}

    </div>
  );
}
