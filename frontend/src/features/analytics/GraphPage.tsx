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

import { DoubleRightOutlined, ExperimentOutlined, HistoryOutlined } from "@ant-design/icons";
import { Button as AntButton, Drawer, message, Modal, Space } from "antd";
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
  setGraphColorScheme,
  type GraphKind,
  type WorkspaceMode,
} from "./flowWorkspaceGraph";
import {
  isValidAddress,
  isValidTxHash,
  normalizeAddress,
  type EnhancedEdgeData,
  type EnhancedNodeData,
  type FocusDepth,
  type FocusDirection,
  type FocusSelection,
} from "./graphUpgrade";
import { useAddressAssets } from "./useAddressAssets";
import FlowWorkspaceHeader from "./flowWorkspaceHeader";
import SmartFillPanel from "./SmartFillPanel";
import TimeReplayBar from "./TimeReplayBar";
import InvestigationSidebar from "./InvestigationSidebar";
import type { Hypothesis } from "./FlowV3Extras";
import type {
  GraphBookmark,
  GraphFilters,
  GraphViewMode,
  PathQuery,
  ResultRow,
  TempGroup,
} from "./FlowLeftPanel";
import { expandGraphCache, queryCoverageIndex, type CoverageQueryResult } from "../smart-download/smartDownloadApi";
import { resolveEntity, searchEntities, type EntityResolution } from "../entity/entityApi";
import { analyzeFundFlow, type FlowPath, type FundFlowAnalysis } from "../fundflow/fundFlowApi";
import "./graph-page.css";
import "./graph-page-light.css";
import { useAnalysisContext } from "../explorer-intelligence/analysisContext";

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
  const { state: analysisState } = useAnalysisContext();
  const [nodes, setNodes, onNodesChange] = useNodesStateSafe();
  const [edges, setEdges, onEdgesChange] = useEdgesStateSafe();
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const [loading, setLoading] = useState(true);
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [searchInput, setSearchInput] = useState(analysisState.rootAddress);
  const [focus, setFocus] = useState<FocusSelection | null>(null);
  const [direction, setDirection] = useState<FocusDirection>(DEFAULT_DIRECTION);
  const [depth, setDepth] = useState<FocusDepth>(DEFAULT_DEPTH);
  const [kindFilter, setKindFilter] = useState<GraphKind>("all");
  const [addressStats, setAddressStats] = useState<AddressStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(false);
  const [transactions, setTransactions] = useState<FlowEdge[]>([]);
  const [txLoading, setTxLoading] = useState(false);
  const [entityInfo, setEntityInfo] = useState<EntityResolution | null>(null);
  const [lightMode, setLightMode] = useState(true);
  const [viewMode, setViewMode] = useState<GraphViewMode>("normal");
  const [filters, setFilters] = useState<GraphFilters>({
    onlyLarge: false,
    hideContracts: false,
    onlyExchange: false,
    hideWeak: false,
    minAmount: 0,
  });
  const [fundFlow, setFundFlow] = useState<FundFlowAnalysis | null>(null);
  const [analyzing, setAnalyzing] = useState(false);
  const [highlightPathId, setHighlightPathId] = useState<string | null>(null);
  const [bookmarks, setBookmarks] = useState<GraphBookmark[]>([]);
  const [zoomLevel, setZoomLevel] = useState<"far" | "medium" | "near">("medium");
  const [lens, setLens] = useState("");
  const [valueCoverage, setValueCoverage] = useState(100);
  const [queryResults, setQueryResults] = useState<FlowPath[]>([]);
  const [selectedNodes, setSelectedNodes] = useState<string[]>([]);
  const [groups, setGroups] = useState<TempGroup[]>([]);
  const [coverage, setCoverage] = useState<CoverageQueryResult | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [hypothesisResult, setHypothesisResult] = useState<string | null>(null);
  const [entityCollapse, setEntityCollapse] = useState(false);
  const [replayMin, setReplayMin] = useState(0);
  const [replayMax, setReplayMax] = useState(0);
  const [replayTime, setReplayTime] = useState(0);
  const [replayPlaying, setReplayPlaying] = useState(false);
  const [replaySpeed, setReplaySpeed] = useState(2);
  const [multiRoots, setMultiRoots] = useState<string[]>([]);
  const [multiRootInput, setMultiRootInput] = useState("");
  const [multiRootResult, setMultiRootResult] = useState<FundFlowAnalysis | null>(null);
  const [showCommonOnly, setShowCommonOnly] = useState(false);
  const [baseline, setBaseline] = useState<FundFlowAnalysis | null>(null);
  const [diffOpen, setDiffOpen] = useState(false);
  const [diffText, setDiffText] = useState("");
  const [hypotheses, setHypotheses] = useState<Array<{ name: string; addresses: string[]; status: string; createdAt: string }>>([]);
  const [historyPast, setHistoryPast] = useState<Array<{ viewMode: GraphViewMode; lens: string; filters: GraphFilters }>>([]);
  const [historyFuture, setHistoryFuture] = useState<Array<{ viewMode: GraphViewMode; lens: string; filters: GraphFilters }>>([]);
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
  const [showGlobal, setShowGlobal] = useState(false);
  const [coreAddress, setCoreAddress] = useState<string | null>(null);
  // V2.2 智能数据补充面板（Smart Download Orchestrator）
  const [smartFillOpen, setSmartFillOpen] = useState(false);
  useEffect(() => {
    if (inspectorMode === "drawer" && focus) setDrawerOpen(true);
  }, [inspectorMode, focus]);

  // 书签恢复（本地持久化，设计 §18）
  useEffect(() => {
    try {
      const raw = window.localStorage.getItem("graph-v2-bookmarks");
      if (raw) {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed)) {
          setBookmarks(
            parsed.filter(
              (b: GraphBookmark) => b && typeof b === "object" && typeof b.name === "string" && typeof b.address === "string" && typeof b.mode === "string",
            ).slice(0, 20),
          );
        }
      }
      const groupsRaw = window.localStorage.getItem("graph-v3-groups");
      if (groupsRaw) {
        const parsed = JSON.parse(groupsRaw);
        if (Array.isArray(parsed)) {
          setGroups(parsed.filter((g: TempGroup) => g && typeof g.name === "string" && Array.isArray(g.addresses)));
        }
      }
      const hypRaw = window.localStorage.getItem("graph-v3-hypotheses");
      if (hypRaw) {
        const parsed = JSON.parse(hypRaw);
        if (Array.isArray(parsed)) {
          setHypotheses(parsed.filter((h) => h && typeof h.name === "string" && Array.isArray(h.addresses) && typeof h.status === "string"));
        }
      }
    } catch {
      /* ignore */
    }
  }, []);

  // V3 时间回放：从 Fund Flow 路径构建时间范围（设计 §3-§5）
  useEffect(() => {
    const blocks: number[] = [];
    for (const p of fundFlow?.paths ?? []) {
      for (const n of p.nodes) {
        if (n.block_number) blocks.push(n.block_number);
      }
    }
    if (blocks.length === 0) {
      setReplayMin(0);
      setReplayMax(0);
      return;
    }
    const min = Math.min(...blocks);
    const max = Math.max(...blocks);
    setReplayMin(min);
    setReplayMax(max);
    setReplayTime((t) => (t === 0 || t < min ? min : t));
  }, [fundFlow]);

  // 播放推进（每 500ms 前进 replaySpeed*3 个区块）
  useEffect(() => {
    if (!replayPlaying || replayMax <= replayMin) return;
    const timer = window.setInterval(() => {
      setReplayTime((t) => {
        const next = t + replaySpeed * 3;
        if (next >= replayMax) {
          setReplayPlaying(false);
          return replayMax;
        }
        return next;
      });
    }, 500);
    return () => window.clearInterval(timer);
  }, [replayPlaying, replayMax, replayMin, replaySpeed]);

  // V3 多根联合调查（设计 §22-§23）：依次分析并合并
  const multiRootRunRef = useRef(0);
  useEffect(() => {
    if (multiRoots.length < 2) {
      setMultiRootResult(null);
      return;
    }
    const run = ++multiRootRunRef.current;
    let alive = true;
    (async () => {
      const merged: FundFlowAnalysis = {
        root_address: multiRoots.join(","),
        chain_key: CHAIN,
        goal: "cashout",
        cache_hit: false,
        generated_at: new Date().toISOString(),
        paths: [], profit: [], settlements: [], cashouts: [], round_trips: [],
        conservation: [],
        graph: { root: multiRoots.join(","), nodes: [], edges: [], collapsed_entities: 0 },
        summary: {},
      };
      for (const root of multiRoots.slice(0, 5)) {
        const r = await analyzeFundFlow({ chain_key: CHAIN, root_address: root, goal: "cashout", max_depth: 2 });
        if (!alive || run !== multiRootRunRef.current) return;
        if (!r) continue;
        merged.paths = [...(merged.paths ?? []), ...(r.paths ?? [])];
        merged.profit = [...(merged.profit ?? []), ...(r.profit ?? [])];
        merged.settlements = [...(merged.settlements ?? []), ...(r.settlements ?? [])];
        merged.cashouts = [...(merged.cashouts ?? []), ...(r.cashouts ?? [])];
        merged.graph!.nodes = [...(merged.graph?.nodes ?? []), ...(r.graph?.nodes ?? [])];
        merged.graph!.edges = [...(merged.graph?.edges ?? []), ...(r.graph?.edges ?? [])];
      }
      if (alive && run === multiRootRunRef.current) setMultiRootResult(merged);
    })();
    return () => {
      alive = false;
    };
  }, [multiRoots]);

  // Workspace History（设计 §35、§48-6）
  const historyInitRef = useRef(true);
  useEffect(() => {
    if (historyInitRef.current) {
      historyInitRef.current = false;
      return;
    }
    setHistoryPast((past) => [...past.slice(-29), { viewMode, lens, filters }]);
    setHistoryFuture([]);
  }, [viewMode, lens, filters]);

  const undoHistory = () => {
    setHistoryPast((past) => {
      if (past.length === 0) return past;
      const prev = past[past.length - 1];
      setHistoryFuture((f) => [...f, { viewMode, lens, filters }]);
      setViewMode(prev.viewMode);
      setLens(prev.lens);
      setFilters({ ...prev.filters });
      return past.slice(0, -1);
    });
  };

  const redoHistory = () => {
    setHistoryFuture((future) => {
      if (future.length === 0) return future;
      const next = future[future.length - 1];
      setHistoryPast((p) => [...p, { viewMode, lens, filters }]);
      setViewMode(next.viewMode);
      setLens(next.lens);
      setFilters({ ...next.filters });
      return future.slice(0, -1);
    });
  };

  // Ctrl+K 命令面板（设计 §37）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // 调查透镜 → 视图/过滤预设（设计 §6-§12）
  useEffect(() => {
    switch (lens) {
      case "资金主干":
        setViewMode("paths");
        setFilters((f) => ({ ...f, hideWeak: true, onlyLarge: false, minAmount: 0 }));
        break;
      case "大额流向":
        setViewMode("normal");
        setFilters((f) => ({ ...f, onlyLarge: true, minAmount: 1000 }));
        break;
      case "快速转移":
        setViewMode("paths");
        setFilters((f) => ({ ...f, hideWeak: true }));
        break;
      case "沉淀":
        setViewMode("settlement");
        break;
      case "获利":
        setViewMode("profit");
        break;
      case "交易所落点":
        setViewMode("cashout");
        break;
      case "风险暴露":
        setViewMode("normal");
        setFilters((f) => ({ ...f, hideContracts: false }));
        break;
      case "跨链":
        setViewMode("paths");
        break;
      default:
        break;
    }
  }, [lens]);

  // 聚焦地址 Coverage 叠加（设计 §28）
  useEffect(() => {
    if (!focus) {
      setCoverage(null);
      return;
    }
    let alive = true;
    void queryCoverageIndex({ chain_key: CHAIN, address: focus.address, dataset: "token_transfers", from_block: 0, to_block: 0 })
      .then((r) => {
        if (alive) setCoverage(r);
      })
      .catch(() => {
        if (alive) setCoverage(null);
      });
    return () => {
      alive = false;
    };
  }, [focus]);

  // 聚焦时自动分析关键路径（设计 §10.1、§26）
  useEffect(() => {
    if (!focus) {
      setFundFlow(null);
      return;
    }
    let alive = true;
    setAnalyzing(true);
    analyzeFundFlow({
      chain_key: CHAIN,
      root_address: focus.address,
      goal: "cashout",
      max_depth: 2,
    })
      .then((r) => {
        if (alive) setFundFlow(r);
      })
      .catch(() => {
        if (alive) setFundFlow(null);
      })
      .finally(() => {
        if (alive) setAnalyzing(false);
      });
    return () => {
      alive = false;
    };
  }, [focus]);

  const saveBookmark = () => {
    if (!focus) {
      void message.info("请先聚焦一个地址");
      return;
    }
    const b: GraphBookmark = {
      name: `${focus.address.slice(0, 8)}… ${viewMode}`,
      address: focus.address,
      direction,
      depth,
      mode: viewMode,
      filters: { ...filters },
      lens,
      valueCoverage,
      highlightPathId,
      replayTime,
      nodes: nodes.map((n) => ({ id: String(n.id), position: n.position })),
      savedAt: new Date().toLocaleString(),
    };
    const next = [b, ...bookmarks].slice(0, 20);
    setBookmarks(next);
    try {
      window.localStorage.setItem("graph-v2-bookmarks", JSON.stringify(next));
    } catch {
      /* ignore */
    }
    // Snapshot Diff（§34、§48-6）：与上一个快照比较节点/边变化
    if (bookmarks.length > 0) {
      const prev = bookmarks[0];
      const prevIds = new Set((prev.nodes ?? []).map((n) => n.id));
      const curIds = new Set(b.nodes?.map((n) => n.id) ?? []);
      const added = [...curIds].filter((id) => !prevIds.has(id)).length;
      const removed = [...prevIds].filter((id) => !curIds.has(id)).length;
      setDiffText(`快照对比：新增节点 ${added}，移除节点 ${removed}，模式 ${prev.mode} → ${viewMode}`);
    }
    void message.success("书签已保存");
  };

  const restoreBookmark = (b: GraphBookmark) => {
    if (!b || typeof b.mode !== "string" || typeof b.address !== "string") {
      void message.warning("书签数据格式异常，已忽略");
      return;
    }
    setViewMode(b.mode);
    setFilters({ ...b.filters });
    if (b.lens) setLens(b.lens);
    if (typeof b.valueCoverage === "number") setValueCoverage(b.valueCoverage);
    if (b.highlightPathId) setHighlightPathId(b.highlightPathId);
    if (b.replayTime) setReplayTime(b.replayTime);
    if (b.nodes?.length) {
      setNodes((cur) =>
        cur.map((n) => {
          const saved = b.nodes?.find((s) => s.id === String(n.id));
          return saved ? { ...n, position: saved.position } : n;
        }),
      );
    }
    setCoreAddress(b.address);
    setDirection(b.direction as FocusDirection);
    setDepth(b.depth as FocusDepth);
    setFocus({ address: b.address, direction: b.direction as FocusDirection, depth: b.depth as FocusDepth });
    void message.success(`已恢复书签：${b.name}`);
  };

  const runPathQuery = (q: PathQuery) => {
    if (!fundFlow) {
      void message.info("请先分析关键路径");
      return;
    }
    const mustPass = q.mustPass.trim().toLowerCase();
    const results = (fundFlow.paths ?? []).filter((p) => {
      if (q.terminal !== "ANY" && !p.terminal_type?.toUpperCase().includes(q.terminal)) return false;
      if (q.minAmount > 0) {
        const v = Number(p.total_amount);
        if (!Number.isFinite(v) || v < q.minAmount) return false;
      }
      if (p.hops > q.maxHops) return false;
      if (mustPass && !p.nodes.some((n) => n.address.toLowerCase() === mustPass)) return false;
      return true;
    });
    setQueryResults(results);
    void message.success(`路径查询：命中 ${results.length} 条`);
  };

  const createGroup = (name: string) => {
    if (selectedNodes.length < 2) return;
    const next = [...groups.filter((g) => g.name !== name), { name, addresses: [...selectedNodes] }];
    setGroups(next);
    try {
      window.localStorage.setItem("graph-v3-groups", JSON.stringify(next));
    } catch {
      /* ignore */
    }
    void message.success(`临时组「${name}」已建立（${selectedNodes.length} 个地址）`);
  };

  const removeGroup = (name: string) => {
    const next = groups.filter((g) => g.name !== name);
    setGroups(next);
    try {
      window.localStorage.setItem("graph-v3-groups", JSON.stringify(next));
    } catch {
      /* ignore */
    }
  };

  const testHypothesis = async () => {
    if (selectedNodes.length < 2) return;
    const entities = new Set<string>();
    const names = new Set<string>();
    for (const addr of selectedNodes.slice(0, 10)) {
      const r = await resolveEntity(CHAIN, addr, "default");
      if (r?.entity?.id) {
        entities.add(r.entity.id);
        names.add(r.entity.name);
      }
    }
    if (entities.size === 1) {
      const result = `支持：${selectedNodes.length} 个地址均属于实体「${[...names][0]}」`;
      setHypothesisResult(result);
      void message.info(result);
    } else if (entities.size > 1) {
      const result = `弱支持/反证：命中 ${entities.size} 个不同实体（${[...names].slice(0, 3).join("、")}）`;
      setHypothesisResult(result);
      void message.info(result);
    } else {
      const result = "未验证：所选地址暂无实体归属，需要更多证据";
      setHypothesisResult(result);
      void message.info(result);
    }
  };

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
    () => {
      if (focus) {
        return { kind: "focus", kindFilter, center: focus.address, direction: focus.direction, depth: focus.depth };
      }
      if (showGlobal && coreAddress) {
        // 全局视图 = 以核心地址为中心向外延伸（全部层、上下游），不是整张数据集图
        return { kind: "focus", kindFilter, center: coreAddress, direction: "both", depth: 0 };
      }
      return { kind: "global", kindFilter };
    },
    [focus, kindFilter, showGlobal, coreAddress],
  );

  const workspaceGraph = useMemo(() => {
    if (!graph) return null;
    return buildWorkspaceGraph(graph, workspaceMode);
  }, [graph, workspaceMode]);

  const elements = useMemo(
    () => {
      setGraphColorScheme(lightMode); // 先切配色再布局，避免首帧黑色边
      return workspaceGraph ? layoutWorkspaceGraph(workspaceGraph, workspaceMode) : null;
    },
    [workspaceGraph, workspaceMode, lightMode],
  );

  // 默认不铺开全局大图：未聚焦且未主动选择全局视图时显示空状态（设计 §10.1、§31）
  const viewReady = focus !== null || showGlobal;

  // 白底/深色切换时同步关系边配色（§6.2）
  useEffect(() => {
    setGraphColorScheme(lightMode);
  }, [lightMode]);

  useEffect(() => {
    if (!elements || !viewReady) {
      setNodes([]);
      setEdges([]);
      return;
    }
    setNodes(elements.nodes);
    setEdges(elements.edges);
  }, [elements, setEdges, setNodes, viewReady]);

  // V2：视图模式 / 过滤 / 路径高亮（设计 §8.2、§9、§21）
  const fundFlowSets = useMemo(() => {
    if (!fundFlow) return null;
    const pathSet = new Set<string>();
    for (const p of fundFlow.paths ?? []) {
      for (const n of p.nodes) pathSet.add(n.address.toLowerCase());
    }
    const entitySet = new Set<string>();
    for (const n of fundFlow.graph?.nodes ?? []) {
      if (n.entity_id) entitySet.add(n.address.toLowerCase());
    }
    const settleSet = new Set<string>((fundFlow.settlements ?? []).map((s) => s.address.toLowerCase()));
    const profitSet = new Set<string>((fundFlow.profit ?? []).map((p) => p.address.toLowerCase()));
    const exchangeSet = new Set<string>();
    for (const c of fundFlow.cashouts ?? []) {
      exchangeSet.add(c.source_address.toLowerCase());
      exchangeSet.add(c.destination_address.toLowerCase());
    }
    return { pathSet, entitySet, settleSet, profitSet, exchangeSet };
  }, [fundFlow]);

  const highlightSet = useMemo(() => {
    const set = new Set<string>();
    if (!fundFlow || !fundFlowSets) return set;
    if (highlightPathId) {
      const p = fundFlow.paths?.find((x) => x.id === highlightPathId);
      p?.nodes.forEach((n) => set.add(n.address.toLowerCase()));
    } else if (viewMode !== "normal") {
      if (viewMode === "paths") fundFlowSets.pathSet.forEach((a) => set.add(a));
      if (viewMode === "entity") fundFlowSets.entitySet.forEach((a) => set.add(a));
      if (viewMode === "settlement") fundFlowSets.settleSet.forEach((a) => set.add(a));
      if (viewMode === "profit") fundFlowSets.profitSet.forEach((a) => set.add(a));
      if (viewMode === "cashout") fundFlowSets.exchangeSet.forEach((a) => set.add(a));
    }
    return set;
  }, [fundFlow, fundFlowSets, highlightPathId, viewMode]);

  // V3 时间线事件（设计 §3-§5、§48-2）
  const timelineEvents = useMemo(() => {
    const active = multiRootResult ?? fundFlow;
    if (!active) return [] as Array<{ time: number; type: string; summary: string; amount: string; address: string }>;
    const events: Array<{ time: number; type: string; summary: string; amount: string; address: string }> = [];
    for (const p of active.paths ?? []) {
      for (const n of p.nodes) {
        events.push({
          time: Number(n.block_number ?? 0),
          type: n.edge_type ?? "FLOW",
          summary: `${p.path_type} · ${n.address.slice(0, 10)}…`,
          amount: n.in_amount ?? "",
          address: n.address,
        });
      }
    }
    return events.sort((a, b) => a.time - b.time);
  }, [fundFlow, multiRootResult]);

  // V3 时间回放可见节点集（设计 §3-§5）
  const replayVisibleSet = useMemo(() => {
    if (replayMax <= replayMin || replayTime <= replayMin) return null;
    const set = new Set<string>();
    for (const ev of timelineEvents) {
      if (ev.time <= replayTime) set.add(ev.address.toLowerCase());
    }
    return set;
  }, [replayMin, replayMax, replayTime, timelineEvents]);

  // V3 多根共同节点（设计 §23）
  const commonSet = useMemo(() => {
    if (!multiRootResult || multiRoots.length < 2 || !showCommonOnly) return null;
    const counts = new Map<string, number>();
    for (const n of multiRootResult.graph?.nodes ?? []) {
      const a = n.address.toLowerCase();
      counts.set(a, (counts.get(a) ?? 0) + 1);
    }
    const set = new Set<string>();
    counts.forEach((c, a) => {
      if (c >= multiRoots.length) set.add(a);
    });
    return set;
  }, [multiRootResult, multiRoots, showCommonOnly]);

  // V3 Edge Timeline Preview（§30）：按 from|to 聚合区块时间/金额
  const edgeTimelineMap = useMemo(() => {
    const active = multiRootResult ?? fundFlow;
    const map = new Map<string, Array<{ time: number; amount: number }>>();
    for (const e of active?.graph?.edges ?? []) {
      if (!e.block_number) continue;
      const key = `${e.from.toLowerCase()}|${e.to.toLowerCase()}`;
      const arr = map.get(key) ?? [];
      arr.push({ time: Number(e.block_number), amount: Number(e.amount ?? 0) });
      map.set(key, arr);
    }
    map.forEach((arr) => arr.sort((a, b) => a.time - b.time));
    return map;
  }, [fundFlow, multiRootResult]);

  const filteredNodes = useMemo(() => {
    if (!fundFlowSets && viewMode === "normal" && !filters.onlyLarge && !filters.hideContracts &&
        !filters.onlyExchange && !filters.hideWeak && filters.minAmount <= 0) {
      return nodes;
    }
    return nodes
      .filter((n) => {
        const data = n.data as EnhancedNodeData;
        const addr = data.address.toLowerCase();
        if (replayVisibleSet && !replayVisibleSet.has(addr) && !data.isRoot) return false;
        if (commonSet && !commonSet.has(addr) && !data.isRoot) return false;
        if (filters.hideContracts && data.kind === "合约") return false;
        if (viewMode === "paths" && fundFlowSets && !fundFlowSets.pathSet.has(addr)) return false;
        if (viewMode === "entity" && fundFlowSets && !fundFlowSets.entitySet.has(addr)) return false;
        if (viewMode === "settlement" && fundFlowSets && !fundFlowSets.settleSet.has(addr)) return false;
        if (viewMode === "profit" && fundFlowSets && !fundFlowSets.profitSet.has(addr)) return false;
        if (viewMode === "cashout" && fundFlowSets && !fundFlowSets.exchangeSet.has(addr)) return false;
        if (filters.onlyExchange && fundFlowSets && !fundFlowSets.exchangeSet.has(addr)) return false;
        return true;
      })
      .map((n) => {
        const addr = (n.data as EnhancedNodeData).address.toLowerCase();
        if (highlightSet.has(addr)) {
          return { ...n, className: `${n.className ?? ""} path-highlight` };
        }
        return n;
      });
  }, [nodes, viewMode, filters, fundFlowSets, highlightSet, replayVisibleSet, commonSet]);

  const filteredEdges = useMemo(() => {
    const kept = new Set(filteredNodes.map((n) => String(n.id)));
    let out = edges.filter((e) => {
      if (!kept.has(e.source) || !kept.has(e.target)) return false;
      const data = e.data as EnhancedEdgeData;
      if (filters.minAmount > 0 && data.amount < filters.minAmount) return false;
      if (filters.onlyLarge && data.amount < 1000) return false;
      if (filters.hideWeak && data.txCount < 2 && data.amount < 1000) return false;
      return true;
    });
    // 价值覆盖减噪（设计 §25）：保留解释 X% 资金的最小子图
    if (valueCoverage < 100 && out.length > 1) {
      const total = out.reduce((s, e) => s + (e.data as EnhancedEdgeData).amount, 0);
      if (total > 0) {
        const sorted = [...out].sort((a, b) => (b.data as EnhancedEdgeData).amount - (a.data as EnhancedEdgeData).amount);
        let acc = 0;
        const keptEdges: typeof out = [];
        for (const e of sorted) {
          keptEdges.push(e);
          acc += (e.data as EnhancedEdgeData).amount;
          if ((acc / total) * 100 >= valueCoverage) break;
        }
        out = keptEdges;
      }
    }
    return out.map((e) => {
      const data = e.data as EnhancedEdgeData;
      const key = `${String(e.source).toLowerCase()}|${String(e.target).toLowerCase()}`;
      const tl = edgeTimelineMap.get(key);
      return tl ? { ...e, data: { ...data, timeline: tl } } : e;
    });
  }, [edges, filteredNodes, filters, valueCoverage, edgeTimelineMap]);

  // V3 Entity Graph Collapse UI（§10.4、§39-4）：实体图模式下按实体折叠
  const entityByAddr = useMemo(() => {
    const active = multiRootResult ?? fundFlow;
    const map = new Map<string, { id: string; name: string }>();
    for (const n of active?.graph?.nodes ?? []) {
      if (n.entity_id) map.set(n.address.toLowerCase(), { id: n.entity_id, name: n.entity_name ?? n.entity_id });
    }
    return map;
  }, [fundFlow, multiRootResult]);

  const displayNodes = useMemo(() => {
    if (viewMode !== "entity" || !entityCollapse) return filteredNodes;
    const groups = new Map<string, { ids: string[]; first: EnhancedNodeData; maxRisk: number; inSum: number; outSum: number }>();
    const out: Node[] = [];
    for (const n of filteredNodes) {
      const data = n.data as EnhancedNodeData;
      const ent = entityByAddr.get(data.address.toLowerCase());
      if (!ent) {
        out.push(n);
        continue;
      }
      let g = groups.get(ent.id);
      if (!g) {
        g = { ids: [], first: data, maxRisk: data.risk, inSum: 0, outSum: 0 };
        groups.set(ent.id, g);
      }
      g.ids.push(String(n.id));
      g.maxRisk = Math.max(g.maxRisk, data.risk);
      g.inSum += data.inAmount;
      g.outSum += data.outAmount;
    }
    groups.forEach((g, id) => {
      out.push({
        id: `entity_${id}`,
        type: "address",
        position: { x: 0, y: 0 },
        data: {
          ...g.first,
          address: g.first.address,
          kind: "实体",
          risk: g.maxRisk,
          inAmount: g.inSum,
          outAmount: g.outSum,
          entityName: g.first.entityName ?? g.first.address,
          isRoot: g.first.isRoot,
        },
      });
    });
    return out;
  }, [filteredNodes, viewMode, entityCollapse, entityByAddr]);

  const displayEdges = useMemo(() => {
    if (viewMode !== "entity" || !entityCollapse) return filteredEdges;
    const keyOf = (addr: string) => {
      const ent = entityByAddr.get(addr.toLowerCase());
      return ent ? `entity_${ent.id}` : addr.toLowerCase();
    };
    const agg = new Map<string, { source: string; target: string; amount: number; txCount: number; token: string }>();
    for (const e of filteredEdges) {
      const s = keyOf(String(e.source));
      const t = keyOf(String(e.target));
      if (s === t) continue;
      const k = `${s}|${t}`;
      const d = e.data as EnhancedEdgeData;
      const cur = agg.get(k) ?? { source: s, target: t, amount: 0, txCount: 0, token: d.token ?? "" };
      cur.amount += d.amount;
      cur.txCount += d.txCount;
      agg.set(k, cur);
    }
    return [...agg.values()].map((a, i) => ({
      id: `agg-${i}`,
      source: a.source,
      target: a.target,
      type: "flow",
      data: { amount: a.amount, txCount: a.txCount, token: a.token, kind: "aggregate" },
    }));
  }, [filteredEdges, viewMode, entityCollapse, entityByAddr]);

  // 图 ↔ 结果表双向联动（设计 §39）：按选中节点过滤结果行
  const resultRows = useMemo<ResultRow[]>(() => {
    const active = multiRootResult ?? fundFlow;
    if (!active) return [];
    const sel = new Set(selectedNodes.map((a) => a.toLowerCase()));
    const rows: ResultRow[] = [];
    for (const c of active.cashouts ?? []) {
      if (sel.has(c.source_address.toLowerCase()) || sel.has(c.destination_address.toLowerCase())) {
        rows.push({ type: "落点", label: c.entity_name ?? c.destination_address, amount: c.amount ?? "" });
      }
    }
    for (const s of active.settlements ?? []) {
      if (sel.has(s.address.toLowerCase())) {
        rows.push({ type: "沉淀", label: s.address, amount: s.retained_value });
      }
    }
    for (const p of active.profit ?? []) {
      if (sel.has(p.address.toLowerCase())) {
        rows.push({ type: "获利", label: p.address, amount: p.net_profit });
      }
    }
    for (const p of active.paths ?? []) {
      if (p.nodes.some((n) => sel.has(n.address.toLowerCase()))) {
        rows.push({ type: "路径", label: p.path_type, amount: p.total_amount ?? "", pathId: p.id });
      }
    }
    return rows.slice(0, 30);
  }, [fundFlow, multiRootResult, selectedNodes]);

  // V3 Investigation Copilot 规则建议（设计 §27、§49-5）
  const copilot = useMemo(() => {
    const active = multiRootResult ?? fundFlow;
    if (!active) return [] as Array<{ id: string; text: string; action: string }>;
    const tips: Array<{ id: string; text: string; action: string }> = [];
    for (const p of active.paths ?? []) {
      if (p.hops >= 2) {
        tips.push({ id: "cp_" + p.id, text: `路径 ${p.path_type}（${p.hops} 跳）建议继续展开下游`, action: "paths" });
      }
    }
    for (const s of active.settlements ?? []) {
      tips.push({ id: "cp_settle_" + s.address, text: `${s.address.slice(0, 10)}… 沉淀候选，建议查看历史来源`, action: "settlement" });
    }
    for (const c of active.cashouts ?? []) {
      tips.push({ id: "cp_cash_" + c.destination_address, text: `${c.entity_name ?? "服务"} 落点可作为证据`, action: "cashout" });
    }
    if (coverage && !coverage.full_hit) {
      tips.push({ id: "cp_coverage", text: `当前焦点 Coverage 仅 ${(coverage.coverage_ratio * 100).toFixed(0)}%，建议补齐数据`, action: "fill" });
    }
    for (const c of active.conservation ?? []) {
      if (!c.pass) {
        tips.push({ id: "cp_cons_" + c.address, text: `${c.address.slice(0, 10)}… 资金守恒异常，建议校验/补洞`, action: "fill" });
      }
    }
    return tips.slice(0, 10);
  }, [fundFlow, multiRootResult, coverage]);

  const runCopilotAction = (action: string) => {
    if (action === "fill") onSmartFill();
    else if (action === "settlement") setViewMode("settlement");
    else if (action === "profit") setViewMode("profit");
    else if (action === "cashout") setViewMode("cashout");
    else setViewMode("paths");
  };

  const addMultiRoot = () => {
    const addr = multiRootInput.trim().toLowerCase();
    if (!isValidAddress(addr)) {
      void message.warning("请输入合法 EVM 地址");
      return;
    }
    if (multiRoots.includes(addr)) return;
    setMultiRoots((r) => [...r, addr]);
    setMultiRootInput("");
    if (graph && !graph.nodes.some((n) => n.id.toLowerCase() === addr)) {
      void message.info(`${addr.slice(0, 10)}… 本地暂无数据，联合分析可能为空；聚焦后可自动补数`);
    }
  };

  const addHypothesis = () => {
    if (selectedNodes.length < 2) {
      void message.info("请先多选至少 2 个节点");
      return;
    }
    const h: Hypothesis = {
      name: `假设_${new Date().toISOString().slice(5, 16).replace("T", " ")}`,
      addresses: [...selectedNodes],
      status: "DRAFT",
      createdAt: new Date().toLocaleString(),
    };
    const next = [...hypotheses, h];
    setHypotheses(next);
    try {
      window.localStorage.setItem("graph-v3-hypotheses", JSON.stringify(next));
    } catch {
      /* ignore */
    }
    void message.success("假设已建立（DRAFT）");
  };

  const updateHypothesisStatus = (name: string, status: string) => {
    const next = hypotheses.map((h) => (h.name === name ? { ...h, status } : h));
    setHypotheses(next);
    try {
      window.localStorage.setItem("graph-v3-hypotheses", JSON.stringify(next));
    } catch {
      /* ignore */
    }
  };

  const onTimelineClick = (address: string) => {
    const active = multiRootResult ?? fundFlow;
    const p = (active?.paths ?? []).find((x) => x.nodes.some((n) => n.address.toLowerCase() === address.toLowerCase()));
    if (p) {
      setHighlightPathId(p.id);
      void message.success("已在图中高亮对应路径");
    } else {
      setSelectedNodes([address]);
    }
  };

  const showGlobalExtension = () => {
    if (!coreAddress) {
      void message.info("请先输入或点击一个核心地址，再查看全局延伸关系");
      return;
    }
    setShowGlobal(true);
    void message.info(`已显示核心地址延伸关系：${coreAddress.slice(0, 10)}…`);
  };

  const saveDiffBaseline = () => {
    setBaseline(multiRootResult ?? fundFlow);
    void message.success("已保存对比基线");
  };

  const compareDiff = () => {
    const cur = multiRootResult ?? fundFlow;
    if (!baseline || !cur) {
      setDiffText("请先保存对比基线");
      setDiffOpen(true);
      return;
    }
    const basePaths = new Set((baseline.paths ?? []).map((p) => p.id));
    const curPaths = new Set((cur.paths ?? []).map((p) => p.id));
    const added = (cur.paths ?? []).filter((p) => !basePaths.has(p.id)).length;
    const removed = (baseline.paths ?? []).filter((p) => !curPaths.has(p.id)).length;
    const cashAdded = Math.max(0, (cur.cashouts?.length ?? 0) - (baseline.cashouts?.length ?? 0));
    const settleAdded = Math.max(0, (cur.settlements?.length ?? 0) - (baseline.settlements?.length ?? 0));
    setDiffText(`新增路径 ${added}，移除路径 ${removed}，新增落点 ${cashAdded}，新增沉淀 ${settleAdded}`);
    setDiffOpen(true);
  };

  useEffect(() => {
    if (!flowInstance || !elements?.nodes.length || !viewReady) return;
    const frame = window.requestAnimationFrame(() => {
      void flowInstance.fitView({ padding: 0.16, maxZoom: 1, duration: 260 });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [elements, flowInstance, viewReady]);

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

  // Investigation Cache V2：聚焦地址自动预热图扩展缓存（设计 §46-§47）
  useEffect(() => {
    if (!focus) return;
    void expandGraphCache({
      investigation_id: "default",
      chain_key: CHAIN,
      address: focus.address,
      direction: focus.direction,
      depth: focus.depth,
    }).catch(() => {
      /* 预热失败不打断用户操作 */
    });
  }, [focus]);

  // Entity Intelligence：聚焦地址解析实体/标签/证据（Graph Node Label Overlay）
  useEffect(() => {
    if (!focus) {
      setEntityInfo(null);
      return;
    }
    let alive = true;
    resolveEntity(CHAIN, focus.address, "default")
      .then((r) => {
        if (alive) setEntityInfo(r);
      })
      .catch(() => {
        if (alive) setEntityInfo(null);
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
  const onLocate = useCallback(async () => {
    const raw = searchInput.trim();
    if (!isValidAddress(raw)) {
      // V2 §29 搜索增强：支持实体名 / 书签名定位
      const active = multiRootResult ?? fundFlow;
      const lower = raw.toLowerCase();
      const node = (active?.graph?.nodes ?? []).find(
        (n) => n.entity_name?.toLowerCase().includes(lower) || n.entity_id?.toLowerCase().includes(lower),
      );
      const bookmark = bookmarks.find((b) => b.name.toLowerCase().includes(lower));
      if (node) {
        lastCenterRef.current = node.address;
        setCoreAddress(node.address);
        setFocus({ address: node.address, direction, depth });
        setSearchInput("");
        void message.success(`已定位实体：${node.entity_name ?? node.address}`);
        return;
      }
      if (bookmark) {
        restoreBookmark(bookmark);
        return;
      }
      const hits = await searchEntities(raw);
      const first = hits?.items?.find((e) => e.addresses?.length);
      if (first?.addresses?.length) {
        const addr = first.addresses[0].toLowerCase();
        lastCenterRef.current = addr;
        setCoreAddress(addr);
        setFocus({ address: addr, direction, depth });
        setSearchInput("");
        void message.success(`已定位实体：${first.name}`);
        return;
      }
      if (isValidTxHash(raw)) {
        void message.info("交易哈希定位暂未接入，请输入 EVM 地址");
      } else {
        void message.warning("请输入有效的 EVM 地址（0x + 40 位十六进制）");
      }
      return;
    }
    const addr = normalizeAddress(raw);
    if (graph && !graph.nodes.some((n) => n.id.toLowerCase() === addr)) {
      // 地址不在本地库：仍可聚焦，并自动打开智能补数（下载完成后自动回填图）
      lastCenterRef.current = addr;
      setCoreAddress(addr);
      setFocus({ address: addr, direction, depth });
      setSearchInput("");
      void message.warning("本地暂无该地址数据，已自动打开智能补充");
      setSmartFillOpen(true);
      return;
    }
    lastCenterRef.current = addr;
    setCoreAddress(addr);
    setFocus({ address: addr, direction, depth });
    setSearchInput("");
  }, [searchInput, graph, direction, depth, onOpenAddress, multiRootResult, fundFlow, bookmarks, restoreBookmark]);

  const onClearFocus = useCallback(() => {
    setFocus(null);
    setShowGlobal(false);
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
      if (next === "all") {
        if (coreAddress) {
          setShowGlobal(true);
          void message.info(`已显示核心地址延伸关系：${coreAddress.slice(0, 10)}…`);
        } else {
          void message.info("请先输入或点击一个核心地址，再查看全局延伸关系");
        }
        return;
      }
      if (lastCenterRef.current) {
        setCoreAddress(lastCenterRef.current);
        setFocus({ address: lastCenterRef.current, direction: next, depth });
      } else {
        void message.info("请先搜索或点击画布地址选择中心");
      }
    },
    [focus, depth, coreAddress],
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
      setCoreAddress(normalized);
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
    if (inspectorMode === "drawer") {
      // 抽屉模式：只关闭抽屉（保留聚焦，移动端可随时再看）
      setDrawerOpen(false);
    } else {
      // dock/collapsible：退出聚焦并折叠地址详情面板（右上角 × = 关闭详情）
      onClearFocus();
      setInspectorCollapsed(true);
    }
  }, [inspectorMode, onClearFocus]);

  // V2.2 智能数据补充：打开面板（基于当前聚焦地址）
  const onSmartFill = useCallback(() => {
    if (!focus) return;
    setSmartFillOpen(true);
  }, [focus]);

  // V2.2 智能数据补充完成后刷新图谱（重新拉取 analytics graph 数据）
  const onRefreshGraph = useCallback(async () => {
    setLoading(true);
    try {
      const nextGraph = await fetchGraph(5000);
      setGraph(nextGraph);
      setSmartFillOpen(false);
      void message.success("图谱已刷新");
    } catch {
      void message.error("刷新图谱失败");
    } finally {
      setLoading(false);
    }
  }, []);

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
        entityInfo={entityInfo}
      onSmartFill={onSmartFill}
      onExitFocus={onClearFocus}
      onSelectNeighbor={onSelectNeighbor}
      onClose={onCloseInspector}
      onCollapse={inspectorMode === "drawer" ? undefined : () => setInspectorCollapsed(true)}
    />
  );

  return (
    <div className={`ds-page analytics-page analytics-graph-page${lightMode ? " graph-light" : ""}`}>
      {hypothesisResult && (
        <div className="flow-hypothesis-banner">
          <ExperimentOutlined /> 假设验证：{hypothesisResult}
          <AntButton size="small" type="text" onClick={() => setHypothesisResult(null)}>
            关闭
          </AntButton>
        </div>
      )}
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
        <InvestigationSidebar
          focusAddress={focus?.address ?? null}
          lightMode={lightMode}
          viewMode={viewMode}
          lens={lens}
          filters={filters}
          valueCoverage={valueCoverage}
          entityCollapse={entityCollapse}
          onEntityCollapse={setEntityCollapse}
          fundFlow={multiRootResult ?? fundFlow}
          analyzing={analyzing}
          bookmarks={bookmarks}
          highlightPathId={highlightPathId}
          selectedNodes={selectedNodes}
          groups={groups}
          coverage={coverage}
          resultRows={resultRows}
          onLightMode={setLightMode}
          onViewMode={setViewMode}
          onLens={setLens}
          onFilters={setFilters}
          onValueCoverage={setValueCoverage}
          onAnalyze={() => {
            if (!focus) return;
            setAnalyzing(true);
            void analyzeFundFlow({ chain_key: CHAIN, root_address: focus.address, goal: "cashout", max_depth: 2 })
              .then(setFundFlow)
              .finally(() => setAnalyzing(false));
          }}
          onHighlightPath={setHighlightPathId}
          onSaveBookmark={saveBookmark}
          onRestoreBookmark={restoreBookmark}
          onSmartFill={onSmartFill}
          onOpenAddress={() => {
            if (focus && onOpenAddress) onOpenAddress(focus.address);
          }}
          onPathQuery={runPathQuery}
          queryResults={queryResults}
          onCreateGroup={createGroup}
          onRemoveGroup={removeGroup}
          onTestHypothesis={() => void testHypothesis()}
          onCommandPalette={() => setPaletteOpen(true)}
          canShowGlobal={!!coreAddress}
          onShowGlobalExtension={showGlobalExtension}
          multiRoots={multiRoots}
          multiRootInput={multiRootInput}
          showCommonOnly={showCommonOnly}
          hypotheses={hypotheses}
          copilot={copilot}
          timeline={timelineEvents}
          canUndo={historyPast.length > 0}
          canRedo={historyFuture.length > 0}
          onMultiRootInput={setMultiRootInput}
          onAddMultiRoot={addMultiRoot}
          onRemoveMultiRoot={(addr) => setMultiRoots((r) => r.filter((x) => x !== addr))}
          onShowCommonOnly={setShowCommonOnly}
          onAddHypothesis={addHypothesis}
          onHypothesisStatus={updateHypothesisStatus}
          onCopilotAction={runCopilotAction}
          onTimelineClick={onTimelineClick}
          onSaveBaseline={saveDiffBaseline}
          onCompareDiff={compareDiff}
          onUndo={undoHistory}
          onRedo={redoHistory}
        />
        <FlowCanvasShell
          loading={loading}
          graph={viewReady ? graph : null}
          nodes={displayNodes}
          edges={displayEdges}
          focus={focus}
          pending={!viewReady}
          emptyDescription="输入 EVM 地址开始分析（或点击顶部「全局视图」查看全部关系）"
          globalTitle={showGlobal && coreAddress ? "全局延伸视图" : undefined}
          globalHint={
            showGlobal && coreAddress
              ? `以 ${coreAddress.slice(0, 10)}… 为中心向外延伸（全部层）`
              : undefined
          }
          truncated={workspaceGraph?.truncated ?? false}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onInit={onInit}
          onNodeClick={onNodeClick}
          onExitFocus={onClearFocus}
          onViewportChange={(zoom) => setZoomLevel(zoom < 0.45 ? "far" : zoom > 0.9 ? "near" : "medium")}
          zoomLevel={zoomLevel}
          onSelectionChange={(ids) =>
            setSelectedNodes((prev) => {
              const cur = [...prev].sort().join(",");
              const next = [...ids].sort().join(",");
              return cur === next ? prev : ids;
            })
          }
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

      {replayMax > replayMin && (
        <TimeReplayBar
          minTime={replayMin}
          maxTime={replayMax}
          currentTime={replayTime}
          playing={replayPlaying}
          speed={replaySpeed}
          onTogglePlay={() => setReplayPlaying((p) => !p)}
          onChange={setReplayTime}
          onSpeed={setReplaySpeed}
        />
      )}

      {diffText && (
        <div className="flow-hypothesis-banner flow-diff-banner">
          <HistoryOutlined /> 图谱 Diff：{diffText}
          <AntButton size="small" type="text" onClick={() => setDiffText("")}>
            关闭
          </AntButton>
        </div>
      )}

      <div className="flow-workspace-statusbar">
        <FlowGraphStatsBar
          chain={CHAIN}
          chainId={CHAIN_ID}
          token=""
          visibleNodes={displayNodes.length}
          visibleEdges={displayEdges.length}
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

      {/* V2.2 智能数据补充面板（Smart Download Orchestrator） */}
      <SmartFillPanel
        open={smartFillOpen}
        address={focus?.address ?? ""}
        chain={CHAIN}
        onClose={() => setSmartFillOpen(false)}
        onRefreshGraph={onRefreshGraph}
      />

      {/* V3 命令面板（设计 §37） */}
      <Modal open={paletteOpen} onCancel={() => setPaletteOpen(false)} footer={null} title="命令面板（Ctrl+K）" width={480}>
        <Space direction="vertical" style={{ width: "100%" }}>
          <AntButton block onClick={() => { setViewMode("settlement"); setPaletteOpen(false); }}>
            切换到沉淀图
          </AntButton>
          <AntButton block onClick={() => { setViewMode("profit"); setPaletteOpen(false); }}>
            切换到获利图
          </AntButton>
          <AntButton block onClick={() => { setViewMode("cashout"); setPaletteOpen(false); }}>
            切换到交易所落点图
          </AntButton>
          <AntButton block onClick={() => { saveBookmark(); setPaletteOpen(false); }}>
            保存当前视图快照
          </AntButton>
          <AntButton block disabled={!focus} onClick={() => { onSmartFill(); setPaletteOpen(false); }}>
            自动补数当前节点
          </AntButton>
          <AntButton block disabled={!focus} onClick={() => { if (focus && onOpenAddress) onOpenAddress(focus.address); setPaletteOpen(false); }}>
            打开地址画像
          </AntButton>
        </Space>
      </Modal>
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
