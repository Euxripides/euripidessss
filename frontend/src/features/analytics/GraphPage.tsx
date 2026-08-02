// 链上地址关系图（V2.0 UI 整改版）— 全屏暗色调查工作台
//
// 结构（设计 §5）：
//   FlowWorkspaceHeader（品牌/搜索/方向/深度/操作）
//   └─ FlowWorkspaceBody
//      ├─ FlowCanvasShell（聚焦标签/计数徽章/画布/图例/退出聚焦）
//      └─ FlowInspector（地址详情/图边统计/实时资产/Tabs/证据边界/操作）
//   └─ FlowWorkspaceStatusBar（图谱统计）
//
// 保留能力：BFS 聚焦/方向/深度、DuckDB 统计、实时资产、快照、Investigation 联动。
// 只重构 UI Layer；graphUpgrade.ts / 后端逻辑零改动。

import { DoubleRightOutlined } from "@ant-design/icons";
import { Drawer, message } from "antd";
import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import { useEdgesState, useNodesState, type Edge, type Node, type ReactFlowInstance } from "@xyflow/react";
import { fetchFlows, fetchGraph, type FlowEdge, type GraphData } from "./analyticsApi";
import FlowCanvasShell from "./flowCanvasShell";
import FlowGraphStatsBar from "./FlowGraphStatsBar";
import FlowInspector, { type NeighborInfo } from "./flowInspector";
import { saveBalanceSnapshot, type SnapshotDiff } from "./flowAssetApi";
import { createInvestigation } from "../intelligence/intelligenceApi";
import { fetchAddressStats, type AddressStats } from "./flowStatsApi";
import {
  buildWorkspaceGraph,
  layoutWorkspaceGraph,
  type GraphKind,
  type WorkspaceMode,
} from "./flowWorkspaceGraph";
import {
  isValidAddress,
  isValidTxHash,
  normalizeAddress,
  type EnhancedNodeData,
  type FocusDepth,
  type FocusDirection,
  type FocusSelection,
} from "./graphUpgrade";
import { useAddressAssets } from "./useAddressAssets";
import FlowWorkspaceHeader from "./flowWorkspaceHeader";
import "./graph-page.css";

const DEFAULT_DIRECTION: FocusDirection = "all";
const DEFAULT_DEPTH: FocusDepth = 2;
const CHAIN = "bsc";
const CHAIN_ID = 56;

function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const mql = window.matchMedia(query);
    const onChange = (event: MediaQueryListEvent) => setMatches(event.matches);
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}

export interface GraphPageProps {
  /** 全局搜索跳转：地址不在当前数据集时，提示可前往地址详情页（合并原顶部横条搜索功能） */
  onOpenAddress?: (address: string) => void;
}

export default function GraphPage({ onOpenAddress }: GraphPageProps) {
  const [nodes, setNodes, onNodesChange] = useNodesStateSafe();
  const [edges, setEdges, onEdgesChange] = useEdgesStateSafe();
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const [loading, setLoading] = useState(true);
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [searchInput, setSearchInput] = useState("");
  const [focus, setFocus] = useState<FocusSelection | null>(null);
  const [direction, setDirection] = useState<FocusDirection>(DEFAULT_DIRECTION);
  const [depth, setDepth] = useState<FocusDepth>(DEFAULT_DEPTH);
  const [kindFilter, setKindFilter] = useState<GraphKind>("all");
  const [addressStats, setAddressStats] = useState<AddressStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(false);
  const [transactions, setTransactions] = useState<FlowEdge[]>([]);
  const [txLoading, setTxLoading] = useState(false);
  const [savingSnapshot, setSavingSnapshot] = useState(false);
  const [investigating, setInvestigating] = useState(false);
  const lastCenterRef = useRef<string | null>(null);

  // 响应式：≥1280 常驻；960–1279 可折叠；<960 Drawer（<768 全屏）
  const isWide = useMediaQuery("(min-width: 1280px)");
  const isMedium = useMediaQuery("(min-width: 960px)");
  const isSmall = useMediaQuery("(max-width: 767px)");
  const inspectorMode: "dock" | "collapsible" | "drawer" = isWide ? "dock" : isMedium ? "collapsible" : "drawer";
  const [inspectorCollapsed, setInspectorCollapsed] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  useEffect(() => {
    if (inspectorMode === "drawer" && focus) setDrawerOpen(true);
  }, [inspectorMode, focus]);

  // 实时资产（聚焦时查询）。tokens 传 undefined（保持引用稳定，避免
  // 每次渲染重建数组导致 useAddressAssets 的 load 回调失效 → 请求风暴）
  const assetsView = useAddressAssets({
    chain: CHAIN,
    chainId: CHAIN_ID,
    address: focus?.address ?? null,
  });

  useEffect(() => {
    let alive = true;
    fetchGraph(5000)
      .then((nextGraph) => {
        if (alive) setGraph(nextGraph);
      })
      .catch(() => {
        if (alive) setGraph(null);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, []);

  const workspaceMode = useMemo<WorkspaceMode>(
    () => (focus
      ? { kind: "focus", kindFilter, center: focus.address, direction: focus.direction, depth: focus.depth }
      : { kind: "global", kindFilter }),
    [focus, kindFilter],
  );

  const workspaceGraph = useMemo(() => {
    if (!graph) return null;
    return buildWorkspaceGraph(graph, workspaceMode);
  }, [graph, workspaceMode]);

  const elements = useMemo(
    () => (workspaceGraph ? layoutWorkspaceGraph(workspaceGraph, workspaceMode) : null),
    [workspaceGraph, workspaceMode],
  );

  useEffect(() => {
    if (!elements) return;
    setNodes(elements.nodes);
    setEdges(elements.edges);
  }, [elements, setEdges, setNodes]);

  useEffect(() => {
    if (!flowInstance || !elements?.nodes.length) return;
    const frame = window.requestAnimationFrame(() => {
      void flowInstance.fitView({ padding: 0.16, maxZoom: 1, duration: 260 });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [elements, flowInstance]);

  const onInit = useCallback((instance: ReactFlowInstance) => {
    setFlowInstance(instance);
  }, []);

  // 聚焦时自动加载地址统计（设计 §19.2）
  useEffect(() => {
    if (!focus) {
      setAddressStats(null);
      return;
    }
    let alive = true;
    setStatsLoading(true);
    fetchAddressStats(focus.address)
      .then((next) => {
        if (alive) setAddressStats(next);
      })
      .catch(() => {
        if (alive) setAddressStats(null);
      })
      .finally(() => {
        if (alive) setStatsLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [focus]);

  // 聚焦时自动加载交易记录（Inspector）
  useEffect(() => {
    if (!focus) {
      setTransactions([]);
      setTxLoading(false);
      return;
    }
    let alive = true;
    setTxLoading(true);
    fetchFlows(focus.address)
      .then((next) => {
        if (alive) setTransactions(next ?? []);
      })
      .catch(() => {
        if (alive) setTransactions([]);
      })
      .finally(() => {
        if (alive) setTxLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [focus]);

  // 聚焦中心（定位失败不清空现有视图）
  const onLocate = useCallback(() => {
    const raw = searchInput.trim();
    if (!isValidAddress(raw)) {
      if (isValidTxHash(raw)) {
        void message.info("交易哈希定位暂未接入，请输入 EVM 地址");
      } else {
        void message.warning("请输入有效的 EVM 地址（0x + 40 位十六进制）");
      }
      return;
    }
    const addr = normalizeAddress(raw);
    if (graph && !graph.nodes.some((n) => n.id.toLowerCase() === addr)) {
      // 合并顶部横条全局搜索：数据集外地址可前往地址详情页
      if (onOpenAddress) {
        void message.warning({
          content: "未在当前数据集中找到该地址 — 点击前往地址详情页",
          duration: 5,
          onClick: () => onOpenAddress(addr),
        });
      } else {
        void message.error("未在当前数据集中找到该地址");
      }
      return;
    }
    lastCenterRef.current = addr;
    setFocus({ address: addr, direction, depth });
    setSearchInput("");
  }, [searchInput, graph, direction, depth, onOpenAddress]);

  const onClearFocus = useCallback(() => {
    setFocus(null);
    setSearchInput("");
    setAddressStats(null);
    if (inspectorMode === "drawer") setDrawerOpen(false);
  }, [inspectorMode]);

  // 方向切换：聚焦时重算子图；全局时若有上次中心则复用，否则提示
  const onDirection = useCallback(
    (next: FocusDirection) => {
      setDirection(next);
      if (focus) {
        setFocus({ ...focus, direction: next });
        return;
      }
      if (next !== "all" && lastCenterRef.current) {
        setFocus({ address: lastCenterRef.current, direction: next, depth });
      } else if (next !== "all") {
        void message.info("请先搜索或点击画布地址选择中心");
      }
    },
    [focus, depth],
  );

  // 深度切换：只重算可见子图，不清空中心
  const onDepth = useCallback(
    (next: FocusDepth) => {
      setDepth(next);
      setFocus((current) => (current ? { ...current, depth: next } : current));
    },
    [],
  );

  const onNodeClick = useCallback(
    (_event: ReactMouseEvent, node: Node) => {
      const addr = String(node.data?.address ?? node.id);
      if (!isValidAddress(addr)) return;
      const normalized = normalizeAddress(addr);
      // 点击当前聚焦中心：保持视口不变
      if (focus && focus.address === normalized) return;
      lastCenterRef.current = normalized;
      setFocus({ address: normalized, direction, depth });
    },
    [direction, depth, focus],
  );

  // Inspector 相邻地址（从聚焦子图聚合）
  const neighbors = useMemo<NeighborInfo[]>(() => {
    if (!workspaceGraph || !focus) return [];
    const center = focus.address.toLowerCase();
    const map = new Map<string, NeighborInfo>();
    for (const edge of workspaceGraph.edges) {
      const s = edge.source.toLowerCase();
      const t = edge.target.toLowerCase();
      if (s === center) {
        const current = map.get(t);
        map.set(t, {
          address: t,
          direction: "out",
          token: edge.data.token ?? "",
          amount: (current?.amount ?? 0) + (edge.data.amount ?? 0),
          txCount: (current?.txCount ?? 0) + (edge.data.txCount ?? 1),
        });
      } else if (t === center) {
        const current = map.get(s);
        map.set(s, {
          address: s,
          direction: "in",
          token: edge.data.token ?? "",
          amount: (current?.amount ?? 0) + (edge.data.amount ?? 0),
          txCount: (current?.txCount ?? 0) + (edge.data.txCount ?? 1),
        });
      }
    }
    return [...map.values()].sort((a, b) => b.amount - a.amount);
  }, [workspaceGraph, focus]);

  // 中心节点数据（Inspector）
  const centerNode = useMemo<EnhancedNodeData | null>(() => {
    if (!workspaceGraph || !focus) return null;
    return workspaceGraph.nodes.find((n) => n.id === focus.address)?.data ?? null;
  }, [workspaceGraph, focus]);

  const onSelectNeighbor = useCallback(
    (address: string) => {
      // 与 onLocate/onNodeClick 一致：先校验再进入聚焦（防御性，防止
      // 脏地址流入资产/快照/调查/fetchFlows 请求路径）
      if (!isValidAddress(address)) return;
      const normalized = normalizeAddress(address);
      lastCenterRef.current = normalized;
      setFocus({ address: normalized, direction, depth });
      if (inspectorMode === "drawer") setDrawerOpen(true);
    },
    [direction, depth, inspectorMode],
  );

  const onSaveSnapshot = async () => {
    if (!focus) return;
    setSavingSnapshot(true);
    try {
      const result = await saveBalanceSnapshot(CHAIN, CHAIN_ID, focus.address);
      if (result) {
        void message.success("余额快照已保存");
      } else {
        void message.error("保存快照失败（RPC 不可用）");
      }
    } catch {
      void message.error("保存快照失败");
    } finally {
      setSavingSnapshot(false);
    }
  };

  const onInvestigate = async () => {
    if (!focus) return;
    setInvestigating(true);
    try {
      const result = await createInvestigation({
        address: focus.address,
        chain: CHAIN,
        objective: "追踪当前地址资金流向，识别关键路径与沉淀位置",
        expected_result: ["资金路径", "沉淀地址", "交易所入口"],
        mode: "fund_trace",
      });
      if (result?.investigation) {
        void message.success(`调查已启动：${result.investigation.id}`);
      } else {
        void message.error("启动调查失败");
      }
    } catch {
      void message.error("启动调查失败");
    } finally {
      setInvestigating(false);
    }
  };

  const onCloseInspector = useCallback(() => {
    if (inspectorMode === "drawer") setDrawerOpen(false);
    else onClearFocus();
  }, [inspectorMode, onClearFocus]);

  const inspector = (
    <FlowInspector
      address={focus?.address ?? null}
      node={centerNode}
      direction={direction}
      neighbors={neighbors}
      transactions={transactions}
      txLoading={txLoading}
      statsLoading={statsLoading}
      addressStats={addressStats}
      assets={assetsView.assets}
      assetsState={assetsView.state}
      onAssetsRefresh={assetsView.refresh}
      savingSnapshot={savingSnapshot}
      onSaveSnapshot={onSaveSnapshot}
      investigating={investigating}
      onInvestigate={onInvestigate}
      onExitFocus={onClearFocus}
      onSelectNeighbor={onSelectNeighbor}
      onClose={onCloseInspector}
      onCollapse={inspectorMode === "drawer" ? undefined : () => setInspectorCollapsed(true)}
    />
  );

  return (
    <div className="ds-page analytics-page analytics-graph-page">
      <FlowWorkspaceHeader
        searchInput={searchInput}
        onSearchInput={setSearchInput}
        onLocate={onLocate}
        focus={focus}
        direction={direction}
        depth={depth}
        onDirection={onDirection}
        onDepth={onDepth}
        kindFilter={kindFilter}
        onKindFilter={setKindFilter}
        onClearFocus={onClearFocus}
      />

      <div className="flow-workspace-body">
        <FlowCanvasShell
          loading={loading}
          graph={graph}
          nodes={nodes}
          edges={edges}
          focus={focus}
          truncated={workspaceGraph?.truncated ?? false}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onInit={onInit}
          onNodeClick={onNodeClick}
          onExitFocus={onClearFocus}
        />

        {inspectorMode === "drawer" ? null : inspectorCollapsed ? (
          <div className="flow-inspector-collapse-bar" title="展开地址详情">
            <button
              type="button"
              aria-label="展开地址详情"
              onClick={() => setInspectorCollapsed(false)}
            >
              <DoubleRightOutlined />
            </button>
          </div>
        ) : (
          <div className={inspectorMode === "collapsible" ? "flow-inspector-collapsible" : "flow-inspector-dock"}>
            {inspector}
            <button
              type="button"
              className="flow-inspector-collapse-toggle"
              aria-label="折叠地址详情"
              title="折叠地址详情"
              onClick={() => setInspectorCollapsed(true)}
            >
              <DoubleRightOutlined style={{ transform: "rotate(180deg)" }} />
            </button>
          </div>
        )}
      </div>

      <div className="flow-workspace-statusbar">
        <FlowGraphStatsBar
          chain={CHAIN}
          chainId={CHAIN_ID}
          token=""
          visibleNodes={nodes.length}
          visibleEdges={edges.length}
          truncated={workspaceGraph?.truncated ?? false}
          balanceQueried={0}
          balanceTotal={0}
        />
      </div>

      {/* 768–959 抽屉（340px）/ <768 全屏抽屉（设计 §5.5） */}
      <Drawer
        className="flow-inspector-drawer"
        placement="right"
        width={isSmall ? "100%" : 340}
        open={inspectorMode === "drawer" && drawerOpen}
        onClose={() => setDrawerOpen(false)}
        closable={false}
      >
        {inspector}
      </Drawer>
    </div>
  );
}

// useNodesState/useEdgesState 的轻量封装（类型更简洁）
function useNodesStateSafe() {
  return useNodesState<Node>([]);
}
function useEdgesStateSafe() {
  return useEdgesState<Edge>([]);
}
