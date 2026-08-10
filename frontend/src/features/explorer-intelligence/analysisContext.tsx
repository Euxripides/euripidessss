import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

export type ExplorerChainKey = "bsc" | "eth" | "base" | "arbitrum";
export type AnalysisWindow = "24H" | "7D" | "30D" | "90D" | "1Y" | "ALL" | "CUSTOM";
export type AnalysisDirection = "all" | "in" | "out" | "self";

export interface AnalysisState {
  chain: ExplorerChainKey;
  rootAddress: string;
  window: AnalysisWindow;
  from?: string;
  to?: string;
  fromAddress?: string;
  toAddress?: string;
  counterparty?: string;
  tokens: string[];
  tokenSymbol?: string;
  direction: AnalysisDirection;
  minUSD?: string;
  maxUSD?: string;
  entityFilters: string[];
  entity?: string;
  entityRole?: string;
  protocolFilters: string[];
  methodFilters: string[];
  statusFilters: string[];
  selectedRows: string[];
  caseID?: string;
  tab: string;
  pageSize: 50 | 100 | 200;
  sort: string;
  pendingQuery?: string;
}

interface AnalysisContextValue {
  state: AnalysisState;
  update: (patch: Partial<AnalysisState>) => void;
  reset: () => void;
  clearFilters: () => void;
}

const STORAGE_KEY = "explorer-analysis-context-v2";
export const DEFAULT_ANALYSIS_STATE: AnalysisState = {
  chain: "bsc",
  rootAddress: "",
  window: "30D",
  tokens: [],
  direction: "all",
  entityFilters: [],
  protocolFilters: [],
  methodFilters: [],
  statusFilters: [],
  selectedRows: [],
  tab: "overview",
  pageSize: 100,
  sort: "time_desc",
};

const FILTER_RESET: Partial<AnalysisState> = {
  window: "30D",
  from: undefined,
  to: undefined,
  fromAddress: undefined,
  toAddress: undefined,
  counterparty: undefined,
  tokens: [],
  tokenSymbol: undefined,
  direction: "all",
  minUSD: undefined,
  maxUSD: undefined,
  entityFilters: [],
  entity: undefined,
  entityRole: undefined,
  protocolFilters: [],
  methodFilters: [],
  statusFilters: [],
  selectedRows: [],
};

const AnalysisContext = createContext<AnalysisContextValue | null>(null);
const EVM_ADDRESS = /^0x[0-9a-f]{40}$/;

function validChain(value: string | null): value is ExplorerChainKey {
  return value === "bsc" || value === "eth" || value === "base" || value === "arbitrum";
}

function stringList(params: URLSearchParams, key: string, fallback: string[] = []): string[] {
  const raw = params.get(key);
  return raw ? raw.split(",").map((item) => item.trim()).filter(Boolean).slice(0, 20) : fallback;
}

function validWindow(value: unknown): AnalysisWindow {
  return ["24H", "7D", "30D", "90D", "1Y", "ALL", "CUSTOM"].includes(String(value).toUpperCase())
    ? String(value).toUpperCase() as AnalysisWindow
    : "30D";
}

function validDirection(value: unknown): AnalysisDirection {
  return ["all", "in", "out", "self"].includes(String(value).toLowerCase()) ? String(value).toLowerCase() as AnalysisDirection : "all";
}

function readInitialState(): AnalysisState {
  let saved: Partial<AnalysisState> = {};
  try { saved = JSON.parse(sessionStorage.getItem(STORAGE_KEY) || "{}") as Partial<AnalysisState>; } catch { saved = {}; }
  const params = new URLSearchParams(location.search);
  const pathMatch = location.pathname.match(/^\/explorer\/(bsc|eth|base|arbitrum)\/address\/(0x[0-9a-fA-F]{40})/);
  const chain = pathMatch?.[1] ?? params.get("chain") ?? saved.chain ?? DEFAULT_ANALYSIS_STATE.chain;
  const address = (pathMatch?.[2] ?? params.get("address") ?? saved.rootAddress ?? "").toLowerCase();
  const parsedPageSize = Number(params.get("page_size") ?? saved.pageSize ?? 100);
  return {
    ...DEFAULT_ANALYSIS_STATE,
    ...saved,
    chain: validChain(chain) ? chain : DEFAULT_ANALYSIS_STATE.chain,
    rootAddress: EVM_ADDRESS.test(address) ? address : "",
    window: validWindow(params.get("range") ?? params.get("window") ?? saved.window),
    from: params.get("from") ?? saved.from,
    to: params.get("to") ?? saved.to,
    fromAddress: params.get("from_address") ?? saved.fromAddress,
    toAddress: params.get("to_address") ?? saved.toAddress,
    counterparty: params.get("counterparty") ?? saved.counterparty,
    tokens: stringList(params, "token", saved.tokens),
    tokenSymbol: params.get("token_symbol") ?? saved.tokenSymbol,
    direction: validDirection(params.get("direction") ?? saved.direction),
    minUSD: params.get("min_usd") ?? saved.minUSD,
    maxUSD: params.get("max_usd") ?? saved.maxUSD,
    entityFilters: stringList(params, "entity_type", saved.entityFilters),
    entity: params.get("entity") ?? saved.entity,
    entityRole: params.get("entity_role") ?? saved.entityRole,
    protocolFilters: stringList(params, "protocol", saved.protocolFilters),
    methodFilters: stringList(params, "method", saved.methodFilters),
    statusFilters: stringList(params, "status", saved.statusFilters),
    selectedRows: [],
    caseID: params.get("case_id") ?? saved.caseID,
    tab: params.get("tab") ?? saved.tab ?? DEFAULT_ANALYSIS_STATE.tab,
    pageSize: parsedPageSize === 50 || parsedPageSize === 200 ? parsedPageSize : 100,
    sort: params.get("sort") ?? saved.sort ?? "time_desc",
  };
}

export function AnalysisProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AnalysisState>(readInitialState);
  const update = useCallback((patch: Partial<AnalysisState>) => setState((current) => ({ ...current, ...patch })), []);
  const reset = useCallback(() => setState(DEFAULT_ANALYSIS_STATE), []);
  const clearFilters = useCallback(() => setState((current) => ({ ...current, ...FILTER_RESET })), []);

  useEffect(() => {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify(state));
    const params = new URLSearchParams();
    if (state.window !== "30D") params.set("range", state.window.toLowerCase());
    if (state.tab !== "overview") params.set("tab", state.tab);
    if (state.from) params.set("from", state.from);
    if (state.to) params.set("to", state.to);
    if (state.fromAddress) params.set("from_address", state.fromAddress);
    if (state.toAddress) params.set("to_address", state.toAddress);
    if (state.counterparty) params.set("counterparty", state.counterparty);
    if (state.tokens.length) params.set("token", state.tokens.join(","));
    if (state.tokenSymbol) params.set("token_symbol", state.tokenSymbol);
    if (state.direction !== "all") params.set("direction", state.direction);
    if (state.minUSD) params.set("min_usd", state.minUSD);
    if (state.maxUSD) params.set("max_usd", state.maxUSD);
    if (state.entityFilters.length) params.set("entity_type", state.entityFilters.join(","));
    if (state.entity) params.set("entity", state.entity);
    if (state.entityRole) params.set("entity_role", state.entityRole);
    if (state.protocolFilters.length) params.set("protocol", state.protocolFilters.join(","));
    if (state.methodFilters.length) params.set("method", state.methodFilters.join(","));
    if (state.statusFilters.length) params.set("status", state.statusFilters.join(","));
    if (state.pageSize !== 100) params.set("page_size", String(state.pageSize));
    if (state.sort !== "time_desc") params.set("sort", state.sort);
    if (state.caseID) params.set("case_id", state.caseID);
    const base = state.rootAddress ? `/explorer/${state.chain}/address/${state.rootAddress}` : `/explorer/${state.chain}`;
    const next = `${base}${params.size ? `?${params.toString()}` : ""}`;
    if ((location.pathname === "/" || location.pathname.startsWith("/explorer")) && `${location.pathname}${location.search}` !== next) history.replaceState(null, "", next);
  }, [state]);

  const value = useMemo(() => ({ state, update, reset, clearFilters }), [clearFilters, reset, state, update]);
  return <AnalysisContext.Provider value={value}>{children}</AnalysisContext.Provider>;
}

export function useAnalysisContext(): AnalysisContextValue {
  const value = useContext(AnalysisContext);
  if (!value) throw new Error("useAnalysisContext must be used within AnalysisProvider");
  return value;
}
