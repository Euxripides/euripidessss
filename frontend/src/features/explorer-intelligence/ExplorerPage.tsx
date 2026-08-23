import {
  ArrowDownOutlined,
  ArrowLeftOutlined,
  ArrowRightOutlined,
  CaretDownOutlined,
  CaretUpOutlined,
  CheckCircleFilled,
  CopyOutlined,
  DownOutlined,
  DownloadOutlined,
  EllipsisOutlined,
	FilterFilled,
  FilterOutlined,
  FolderAddOutlined,
  LineChartOutlined,
  MoreOutlined,
  NodeIndexOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  SaveOutlined,
  SearchOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  SwapOutlined,
  TagsOutlined,
} from "@ant-design/icons";
import {
  AutoComplete,
  Button,
  Card,
  Checkbox,
	ConfigProvider,
  DatePicker,
  Descriptions,
  Drawer,
  Dropdown,
  Empty,
  Input,
  InputNumber,
  Popover,
  QRCode,
  Segmented,
  Select,
  Skeleton,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import type { ThemeConfig } from "antd";
import type { ColumnsType } from "antd/es/table";
import dayjs from "dayjs";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { ClickHouseActivity, ClickHouseActivityStats } from "../analytics/ClickHouseExplorerTypes";
import { listAddressLibrary } from "../address-library/addressLibraryApi";
import { useAnalysisContext, type AnalysisDirection, type AnalysisWindow, type ExplorerChainKey } from "./analysisContext";
import { HistoricalPriceCell } from "./HistoricalPriceCell";
import { TokenIdentity } from "./TokenIdentity";
import { TxHashLink } from "../transaction/TxHashLink";
import { useAddressAssets } from "../analytics/useAddressAssets";
import type { AssetBalance } from "../analytics/flowAssetApi";
import { buildTransactionURL, isTransactionHash } from "./appRoute";
import {
  loadActivity,
	loadActivityStats,
  loadBlock,
  loadContract,
  loadCounterparties,
  loadDailyStats,
  loadExplorerHeader,
  loadExplorerHome,
  loadFinancialSummary,
  loadStrictFinancialAnalysis,
  loadToken,
  loadTransaction,
  searchExplorer,
  type CounterpartyFinancial,
  type ExplorerHeader,
  type ExplorerHome,
  type FinancialSummary,
  type SearchItem,
	type ActivityFilterParams,
} from "./explorerApi";
import "./explorer-intelligence.css";

const { Text, Title } = Typography;
const WINDOWS: AnalysisWindow[] = ["24H", "7D", "30D", "90D", "1Y", "ALL", "CUSTOM"];
const CHAINS: Array<{ value: ExplorerChainKey; label: string }> = [
  { value: "bsc", label: "BNB Smart Chain" },
  { value: "eth", label: "Ethereum" },
  { value: "base", label: "Base" },
  { value: "arbitrum", label: "Arbitrum One" },
];

type DetailState = { kind: SearchItem["kind"]; value: string; data?: Record<string, unknown>; activity?: ClickHouseActivity; tokenMode?: boolean; loading?: boolean; error?: string };
type QuickFilter = "all" | "in" | "out" | "large" | "cex" | "dex" | "bridge" | "usdt";

type ColumnPin = "left" | "right" | "none";
interface ColumnLayout { order: string[]; visible: string[]; pins: Record<string, ColumnPin> }
interface SavedExplorerView { id: string; name: string; state: ReturnType<typeof useAnalysisContext>["state"]; columnLayouts: Record<string, ColumnLayout>; savedAt: string }

const ACTIVITY_CACHE = new Map<string, { expires: number; value: Awaited<ReturnType<typeof loadActivity>> }>();
const FILTER_OPTION_CACHE = new Map<string, { expires: number; rows: ClickHouseActivity[] }>();
const CACHE_TTL_MS = 60_000;
const FILTER_OPTION_PAGE_SIZE = 200;
const FILTER_OPTION_ROW_LIMIT = 5_000;
const SAVED_VIEWS_KEY = "explorer-saved-views-v2";
const COLUMN_LAYOUT_KEY = "explorer-column-layouts-v2";
const ACTIVITY_SORT_OPTIONS = [
  { value: "time_desc", label: "时间：新→旧" }, { value: "time_asc", label: "时间：旧→新" },
  { value: "tx_asc", label: "交易哈希：升序" }, { value: "tx_desc", label: "交易哈希：降序" },
  { value: "method_asc", label: "方法：升序" }, { value: "method_desc", label: "方法：降序" },
  { value: "from_asc", label: "发送方：升序" }, { value: "from_desc", label: "发送方：降序" },
  { value: "direction_asc", label: "方向：升序" }, { value: "direction_desc", label: "方向：降序" },
  { value: "to_asc", label: "接收方：升序" }, { value: "to_desc", label: "接收方：降序" },
  { value: "token_asc", label: "Token：升序" }, { value: "token_desc", label: "Token：降序" },
  { value: "usd_asc", label: "USD：低→高" }, { value: "usd_desc", label: "USD：高→低" },
  { value: "status_asc", label: "状态：升序" }, { value: "status_desc", label: "状态：降序" },
];
const EXPLORER_FONT_FAMILY = '"Microsoft YaHei", "微软雅黑", "PingFang SC", "Noto Sans SC", Arial, sans-serif';
const EXPLORER_THEME: ThemeConfig = {
  token: {
    fontFamily: EXPLORER_FONT_FAMILY,
    fontSize: 15,
  },
};

interface Props { onNavigate: (page: string, address?: string) => void }

function short(value: unknown, head = 8, tail = 6): string {
  const text = String(value ?? "");
  return text.length > head + tail + 3 ? `${text.slice(0, head)}…${text.slice(-tail)}` : text || "--";
}

function number(value: unknown, available = true): string {
  if (!available || value === null || value === undefined || value === "") return "--";
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return String(value);
  return new Intl.NumberFormat("en-US", { maximumFractionDigits: 2, notation: Math.abs(parsed) >= 1_000_000 ? "compact" : "standard" }).format(parsed);
}

function usd(value: unknown, available = true): string {
  if (!available || value === null || value === undefined || value === "") return "--";
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return "--";
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: Math.abs(parsed) >= 100 ? 0 : 2 }).format(parsed);
}

function date(value: unknown): string {
  if (!value) return "--";
  const parsed = dayjs(String(value));
  return parsed.isValid() && parsed.year() > 1970 ? parsed.format("YYYY-MM-DD HH:mm:ss") : "--";
}

function AddressIdentity({ value, label, onOpen }: { value: unknown; label?: string; onOpen?: () => void }) {
  const address = String(value ?? "");
  if (!address) return <Text type="secondary">--</Text>;
  return <button type="button" className="xi-identity" onClick={onOpen} title={address}><span className="xi-identicon">{address.slice(2, 4).toUpperCase()}</span><span><b>{label || short(address)}</b>{label ? <small>{short(address)}</small> : null}</span></button>;
}

function Metric({ label, value, tone, title }: { label: string; value: string; tone?: "in" | "out"; title?: string }) {
  return <div className={`xi-metric ${tone ? `xi-metric-${tone}` : ""}`} title={title}><span>{label}</span><strong>{value}</strong></div>;
}

export default function ExplorerPage({ onNavigate }: Props) {
  const { state, update, reset } = useAnalysisContext();
  const chainId = state.chain === "bsc" ? 56 : state.chain === "eth" ? 1 : state.chain === "base" ? 8453 : 42161;
  const addressAssets = useAddressAssets({ chain: state.chain, chainId, address: state.rootAddress || null });
  const [query, setQuery] = useState(state.pendingQuery || "");
  const [options, setOptions] = useState<Array<{ value: string; label: ReactNode; item: SearchItem }>>([]);
  const [searching, setSearching] = useState(false);
  const [home, setHome] = useState<ExplorerHome | null>(null);
  const [header, setHeader] = useState<ExplorerHeader | null>(null);
  const [financial, setFinancial] = useState<FinancialSummary | null>(null);
  const [counterparties, setCounterparties] = useState<CounterpartyFinancial[]>([]);
  const [daily, setDaily] = useState<Array<Record<string, unknown>>>([]);
  const [loading, setLoading] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [filterOpen, setFilterOpen] = useState(false);
  const [detail, setDetail] = useState<DetailState | null>(null);
  const searchSequence = useRef(0);

  const openAddress = useCallback((address: string) => {
    const normalized = address.trim().toLowerCase();
    if (!/^0x[0-9a-f]{40}$/.test(normalized)) return void message.warning("请输入有效的 EVM 地址");
    update({ rootAddress: normalized, tab: "overview", pendingQuery: "" });
    setQuery("");
    setDetail(null);
  }, [update]);

  const openDetail = useCallback(async (kind: SearchItem["kind"], value: string) => {
    if (kind === "ADDRESS" || kind === "LABEL") return openAddress(value);
    if (kind === "TRANSACTION" && isTransactionHash(value)) {
      window.history.pushState(null, "", buildTransactionURL(state.chain, value));
      window.dispatchEvent(new PopStateEvent("popstate"));
      return;
    }
    setDetail({ kind, value, loading: true });
    try {
      let data: Record<string, unknown>;
      if (kind === "TRANSACTION") data = await loadTransaction(state.chain, value);
      else if (kind === "BLOCK") data = await loadBlock(state.chain, value);
      else if (kind === "TOKEN") data = await loadToken(state.chain, value);
      else if (kind === "CONTRACT") data = await loadContract(state.chain, value);
      else data = { kind, value, evidence_boundary: "Registry 结果不等同于链上身份定论" };
      setDetail({ kind, value, data });
    } catch (error) {
      setDetail({ kind, value, error: error instanceof Error ? error.message : "详情加载失败" });
    }
  }, [openAddress, state.chain]);

  const openActivityDetail = useCallback(async (row: ClickHouseActivity, tokenMode: boolean) => {
    setDetail({ kind: "TRANSACTION", value: row.transaction_hash, activity: row, tokenMode, loading: true });
    try {
      const data = await loadTransaction(state.chain, row.transaction_hash);
      setDetail({ kind: "TRANSACTION", value: row.transaction_hash, data, activity: row, tokenMode });
    } catch (error) {
      setDetail({ kind: "TRANSACTION", value: row.transaction_hash, activity: row, tokenMode, error: error instanceof Error ? error.message : "详情加载失败" });
    }
  }, [state.chain]);

  useEffect(() => {
    if (!state.pendingQuery) return;
    setQuery(state.pendingQuery);
    update({ pendingQuery: "" });
  }, [state.pendingQuery, update]);

  useEffect(() => {
    if (!query.trim()) return setOptions([]);
    const current = ++searchSequence.current;
    const timer = window.setTimeout(async () => {
      setSearching(true);
      try {
        const [explorerResult, libraryResult] = await Promise.allSettled([
          searchExplorer(state.chain, query.trim()),
          listAddressLibrary(state.chain, query.trim(), 20, true),
        ]);
        if (current !== searchSequence.current) return;
        const combined: SearchItem[] = explorerResult.status === "fulfilled" ? [...explorerResult.value.items] : [];
        const seen = new Set(combined.map((item) => `${item.kind}:${item.value.toLowerCase()}`));
        if (libraryResult.status === "fulfilled") {
          for (const asset of libraryResult.value.items) {
            const key = `ADDRESS:${asset.address}`;
            if (seen.has(key)) continue;
            seen.add(key);
            combined.push({
              kind: "ADDRESS",
              title: asset.label || asset.address,
              subtitle: `地址资产库 · ${asset.state}${asset.activity_rows > 0 ? ` · ${asset.activity_rows.toLocaleString()} 行` : ""}`,
              value: asset.address,
              chain_id: asset.chain_id,
              verified: asset.state === "AVAILABLE" || asset.state === "CERTIFIED",
            });
          }
        }
        setOptions(combined.map((item) => ({ value: `${item.kind}:${item.value}`, item, label: <div className="xi-search-option"><span className="xi-search-kind">{item.kind}</span><span><b>{item.title}</b><small>{item.subtitle}</small></span>{item.verified ? <CheckCircleFilled /> : null}</div> })));
      } catch { if (current === searchSequence.current) setOptions([]); }
      finally { if (current === searchSequence.current) setSearching(false); }
    }, 220);
    return () => window.clearTimeout(timer);
  }, [query, state.chain]);

  useEffect(() => {
    if (state.rootAddress) return;
    setLoading(true);
    loadExplorerHome(state.chain).then(setHome).catch((error: Error) => setErrors({ home: error.message })).finally(() => setLoading(false));
  }, [state.chain, state.rootAddress]);

  useEffect(() => {
    if (!state.rootAddress) return;
    setLoading(true); setErrors({});
    Promise.allSettled([
      loadExplorerHeader(state.chain, state.rootAddress),
      loadFinancialSummary(state.chain, state.rootAddress, state.window, state.from, state.to),
      loadCounterparties(state.chain, state.rootAddress, state.window),
      loadDailyStats(state.chain, state.rootAddress, state.from, state.to),
    ]).then(([h, f, c, d]) => {
      const next: Record<string, string> = {};
      if (h.status === "fulfilled") setHeader(h.value as ExplorerHeader); else next.header = "地址信息加载失败";
      if (f.status === "fulfilled") setFinancial(f.value as FinancialSummary); else { setFinancial(null); next.financial = "金融指标加载失败"; }
      if (c.status === "fulfilled") setCounterparties(c.value as CounterpartyFinancial[]); else { setCounterparties([]); next.counterparties = "交易对手加载失败"; }
      if (d.status === "fulfilled") setDaily(d.value as Array<Record<string, unknown>>); else { setDaily([]); next.daily = "趋势数据加载失败"; }
      setErrors(next);
    }).finally(() => setLoading(false));
  }, [state.chain, state.from, state.rootAddress, state.to, state.window]);

  const submitSearch = () => { const item = options[0]?.item; if (item) void openDetail(item.kind, item.value); };
  const portfolio = useMemo(() => {
    const items = addressAssets.assets?.assets ?? [];
    if (!items.length || items.some((item) => item.status !== "success")) return "--";
    return items.every((item) => Number(item.balance || 0) === 0) ? "$0.00" : "--";
  }, [addressAssets.assets]);

  return <ConfigProvider theme={EXPLORER_THEME}><div className="xi-page xi-page-v11">
    <section className="xi-searchbar xi-searchbar-v11">
      <Select value={state.chain} onChange={(chain) => update({ chain, rootAddress: "", tab: "overview" })} options={CHAINS} className="xi-chain-select" />
      <AutoComplete value={query} options={options} onSearch={setQuery} onChange={setQuery} onSelect={(_, option) => { setQuery(""); void openDetail(option.item.kind, option.item.value); }} className="xi-search">
        <Input prefix={<SearchOutlined />} suffix={searching ? <span className="xi-searching" /> : null} placeholder="搜索地址 / 交易哈希 / 区块 / Token / 实体" onPressEnter={submitSearch} allowClear />
      </AutoComplete>
      <Button type="primary" icon={<SearchOutlined />} onClick={submitSearch}>搜索</Button>
    </section>
    <nav className="xi-breadcrumb"><button type="button" onClick={() => { reset(); setHeader(null); }}>链上查询</button>{state.rootAddress ? <><span>/</span><Text>地址</Text></> : null}</nav>
    {state.rootAddress ? <AddressWorkspace header={header} financial={financial} counterparties={counterparties} daily={daily} liveAssets={addressAssets.assets?.assets ?? []} assetState={addressAssets.state} assetError={addressAssets.error} assetQueriedAt={addressAssets.queriedAt} portfolio={portfolio} loading={loading} errors={errors} onOpenAddress={openAddress} onOpenDetail={openDetail} onOpenActivity={openActivityDetail} onNavigate={onNavigate} onFilter={() => setFilterOpen(true)} /> : <ExplorerHomeView home={home} loading={loading} error={errors.home} onOpenDetail={openDetail} />}
    <FilterDrawer open={filterOpen} onClose={() => setFilterOpen(false)} />
    <DetailDrawer detail={detail} onClose={() => setDetail(null)} onOpenAddress={openAddress} onNavigate={onNavigate} />
  </div></ConfigProvider>;
}

function ExplorerHomeView({ home, loading, error, onOpenDetail }: { home: ExplorerHome | null; loading: boolean; error?: string; onOpenDetail: (kind: SearchItem["kind"], value: string) => void }) {
  if (loading && !home) return <Skeleton active />;
  return <section className="xi-home-v11"><div className="xi-page-heading"><div><Text className="xi-eyebrow">ON-CHAIN EXPLORER</Text><Title level={2}>链上数据查询</Title><Text type="secondary">搜索地址、交易、区块、Token 或已验证实体</Text></div></div><div className="xi-home-strip"><Metric label="最新区块" value={number(home?.latest_block)} /><Metric label="交易" value={number(home?.transaction_count)} /><Metric label="代币转账" value={number(home?.token_transfer_count)} /><Metric label="完整覆盖区间" value={number(home?.coverage_ranges)} /></div><div className="xi-home-lists"><Section title="最近交易" error={error}><CompactObjectList rows={home?.latest_transactions ?? []} onOpen={(row) => onOpenDetail("TRANSACTION", String(row.tx_hash || ""))} /></Section><Section title="大额活动"><CompactObjectList rows={home?.large_transfers ?? []} onOpen={(row) => onOpenDetail("TRANSACTION", String(row.tx_hash || ""))} /></Section></div></section>;
}

function AddressWorkspace(props: { header: ExplorerHeader | null; financial: FinancialSummary | null; counterparties: CounterpartyFinancial[]; daily: Array<Record<string, unknown>>; liveAssets: AssetBalance[]; assetState: "idle" | "loading" | "ready" | "failed"; assetError: string | null; assetQueriedAt: string | null; portfolio: string; loading: boolean; errors: Record<string, string>; onOpenAddress: (address: string) => void; onOpenDetail: (kind: SearchItem["kind"], value: string) => void; onOpenActivity: (row: ClickHouseActivity, tokenMode: boolean) => void; onNavigate: (page: string, address?: string) => void; onFilter: () => void }) {
  const { state, update } = useAnalysisContext();
  const header = props.header;
  const coverage = header?.coverage.status ?? "NO_DATA";
  const noData = coverage === "NO_DATA";
  const system = header?.identity.address_type === "SYSTEM";
  const label = String(header?.labels?.[0]?.label_name || "");
  const confidence = String(header?.labels?.[0]?.confidence || "");
  const verified = Boolean(header?.labels?.[0]?.is_verified) || String(header?.labels?.[0]?.confidence || "").toUpperCase() === "VERIFIED";
  const title = system ? short(state.rootAddress, 10, 8) : label || short(state.rootAddress, 10, 8);
  const subtitle = system ? label : label ? state.rootAddress : "无已验证标签";
  const summary = header?.summary;
  const nativeSymbol = state.chain === "bsc" ? "BNB" : "ETH";
  const stableSymbol = state.chain === "base" || state.chain === "arbitrum" ? "USDC" : "USDT";
  const activeTab = state.tab.startsWith("analysis:") ? "analysis" : state.tab;
  const analysisMode = state.tab.startsWith("analysis:") ? state.tab.split(":")[1] : "funds";
  const exportURL = buildContextExportURL(state);
  const analysisItems = [
    { key: "funds", label: "资金分析" }, { key: "counterparties", label: "交易对手" }, { key: "related", label: "关联钱包" },
    { key: "retention", label: "Retention" }, { key: "pass-through", label: "Pass-through" }, { key: "pnl", label: "PnL" },
  ];
  const moreItems = [
    { key: "save", label: "保存视图" }, { key: "share", label: "复制分享链接" }, { key: "assets", label: "在数据资产中继续" }, { key: "label", label: "添加标签", disabled: true },
  ];
  const copy = () => navigator.clipboard.writeText(state.rootAddress).then(() => void message.success("地址已复制"));
  const moreClick = ({ key }: { key: string }) => { if (key === "save") saveCurrentView(state); if (key === "share") void navigator.clipboard.writeText(location.href).then(() => message.success("链接已复制")); if (key === "assets") props.onNavigate("clean", state.rootAddress); };
  const tabs = [
    { key: "overview", label: "概览", children: noData ? <NoDataState onSupplement={() => props.onNavigate("smart-download", state.rootAddress)} /> : <Overview header={header} financial={props.financial} daily={props.daily} counterparties={props.counterparties} errors={props.errors} onOpenAddress={props.onOpenAddress} onOpenDetail={props.onOpenDetail} /> },
    { key: "transactions", label: "交易", children: <ActivityTable kind="transactions" coverage={coverage} onOpenAddress={props.onOpenAddress} onOpenActivity={props.onOpenActivity} onNavigate={props.onNavigate} onFilter={props.onFilter} /> },
    { key: "token-transfers", label: "代币转账", children: <ActivityTable kind="token-transfers" coverage={coverage} tokenMode onOpenAddress={props.onOpenAddress} onOpenActivity={props.onOpenActivity} onNavigate={props.onNavigate} onFilter={props.onFilter} /> },
    { key: "internal-transactions", label: "内部交易", children: <ActivityTable kind="internal-transactions" coverage={coverage} onOpenAddress={props.onOpenAddress} onOpenActivity={props.onOpenActivity} onNavigate={props.onNavigate} onFilter={props.onFilter} /> },
    { key: "assets", label: "资产", children: <AssetsView header={header} liveAssets={props.liveAssets} state={props.assetState} error={props.assetError} queriedAt={props.assetQueriedAt} /> },
    { key: "contracts", label: "合约", children: <ActivityTable kind="contract-creations" coverage={coverage} onOpenAddress={props.onOpenAddress} onOpenActivity={props.onOpenActivity} onNavigate={props.onNavigate} onFilter={props.onFilter} /> },
    { key: "analysis", label: <Dropdown menu={{ items: analysisItems, onClick: ({ key }) => update({ tab: `analysis:${key}` }) }} trigger={["click"]}><span className="xi-analysis-tab">分析 <DownOutlined /></span></Dropdown>, children: <AnalysisView mode={analysisMode} financial={props.financial} daily={props.daily} counterparties={props.counterparties} errors={props.errors} onOpenAddress={props.onOpenAddress} /> },
  ];
  const coverageDetail = header?.coverage.detail;
  return <>
    <section className="xi-address-shell">
      <div className="xi-address-core">
        <div className="xi-large-identicon">{state.rootAddress.slice(2, 4).toUpperCase()}</div>
        <div className="xi-address-identity"><div className="xi-title-line"><Title level={3}>{title}</Title><Tag color={system ? "blue" : undefined}>{header?.identity.address_type || "EOA"}</Tag>{verified ? <CheckCircleFilled className="xi-verified" /> : null}</div><div className="xi-address-subtitle"><Text code>{subtitle}</Text>{label && !system ? <Text type="secondary">{verified ? `${confidence || "HIGH"} confidence` : "未验证"}</Text> : null}<Button type="text" size="small" icon={<CopyOutlined />} onClick={copy} /><Popover content={<QRCode value={state.rootAddress} size={160} />} trigger="click"><Button type="text" size="small" icon={<QrcodeOutlined />} /></Popover></div></div>
      </div>
      <div className="xi-header-meta">
        <InlineMeta label={nativeSymbol} value={balanceValue(props.liveAssets, nativeSymbol)} />
        <InlineMeta label={stableSymbol} value={balanceValue(props.liveAssets, stableSymbol)} />
        <InlineMeta label="Portfolio" value={props.portfolio !== "--" ? props.portfolio : usd(header?.balances.estimated_portfolio_usd, header?.balances.available)} />
        <InlineMeta label="首次活动" value={date(summary?.first_seen_time)} />
        <InlineMeta label="最后活动" value={date(summary?.last_seen_time)} />
        <Popover title="数据覆盖" content={<div className="xi-coverage-detail"><p>状态：{coverageLabel(coverage)}</p><p>覆盖区间：{number(coverageDetail?.ranges, coverage !== "NO_DATA")}</p><Button type="link" onClick={() => props.onNavigate("data-quality")}>查看数据质量</Button></div>}><button type="button" className="xi-coverage"><span>Coverage</span><strong className={`status-${coverage.toLowerCase()}`}>{coverageLabel(coverage)}</strong></button></Popover>
      </div>
      <div className="xi-primary-actions"><Button type="primary" icon={<NodeIndexOutlined />} onClick={() => props.onNavigate("analytics-graph", state.rootAddress)}>资金追踪</Button><Button icon={<SafetyCertificateOutlined />} onClick={() => props.onNavigate("intelligence", state.rootAddress)}>调查工作台</Button><Button icon={<DownloadOutlined />} href={exportURL}>导出</Button><Dropdown menu={{ items: moreItems, onClick: moreClick }}><Button icon={<EllipsisOutlined />} aria-label="更多操作" /></Dropdown></div>
    </section>
    <div className="xi-context-strip"><Segmented options={WINDOWS} value={state.window} onChange={(window) => update({ window: window as AnalysisWindow })} />{state.window === "CUSTOM" ? <DatePicker.RangePicker showTime onChange={(range) => update({ from: range?.[0]?.toISOString(), to: range?.[1]?.toISOString() })} /> : null}<Text type="secondary">交易对手 {number(summary?.unique_counterparty_count, !noData)} · 活动天数 {number(summary?.active_days, !noData)}</Text></div>
    <div className="xi-core-metrics"><Metric label="估算资产" value={props.portfolio !== "--" ? props.portfolio : usd(header?.balances.estimated_portfolio_usd, header?.balances.available)} /><Metric label="已定价总流入" value={usd(props.financial?.flow.total_in_usd, !noData)} tone={noData ? undefined : "in"} title="仅汇总具备历史美元价格的活动；未定价代币不计入" /><Metric label="已定价总流出" value={usd(props.financial?.flow.total_out_usd, !noData)} tone={noData ? undefined : "out"} title="仅汇总具备历史美元价格的活动；未定价代币不计入" /><Metric label="已定价净流量" value={usd(props.financial?.flow.netflow_usd, !noData)} title="已定价总流入减已定价总流出，不代表全部代币余额变化" /></div>
    <Tabs className="xi-tabs xi-tabs-v11" activeKey={activeTab} onChange={(tab) => update({ tab })} items={tabs} destroyInactiveTabPane={false} />
  </>;
}

function InlineMeta({ label, value }: { label: string; value: string }) { return <div className="xi-inline-meta"><span>{label}</span><strong>{value}</strong></div>; }

function balanceValue(items: AssetBalance[], symbol: string): string {
  const item = items.find((row) => String(row.symbol || "").toUpperCase() === symbol);
  return item ? number(item.balance) : "--";
}

function coverageLabel(status: ExplorerHeader["coverage"]["status"]): string { return status === "COMPLETE" ? "完整" : status === "PARTIAL" ? "部分" : "未覆盖"; }

function Overview(props: { header: ExplorerHeader | null; financial: FinancialSummary | null; daily: Array<Record<string, unknown>>; counterparties: CounterpartyFinancial[]; errors: Record<string, string>; onOpenAddress: (address: string) => void; onOpenDetail: (kind: SearchItem["kind"], value: string) => void }) {
  const max = Math.max(1, ...props.daily.map((row) => Math.abs(Number(row.usd_netflow || 0))));
  return <div className="xi-overview-v11">
    <div className="xi-overview-top"><Section title="资金概览（已定价活动）" error={props.errors.financial}><div className="xi-financial-inline"><span>总流入<strong className="positive">{usd(props.financial?.flow.total_in_usd)}</strong></span><span>总流出<strong className="negative">{usd(props.financial?.flow.total_out_usd)}</strong></span><span>最大单笔流入<strong>{usd(props.financial?.largest_in_usd)}</strong></span><span>最大单笔流出<strong>{usd(props.financial?.largest_out_usd)}</strong></span></div></Section><Section title="活动趋势" error={props.errors.daily}>{props.daily.length ? <div className="xi-trend">{props.daily.slice(-45).map((row, index) => { const value = Number(row.usd_netflow || 0); return <Tooltip key={`${String(row.date)}-${index}`} title={`${date(row.date)} · ${usd(value)}`}><i className={value >= 0 ? "positive" : "negative"} style={{ height: `${Math.max(5, Math.abs(value) / max * 62)}px` }} /></Tooltip>; })}</div> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无趋势数据" />}</Section></div>
    <div className="xi-overview-top"><Section title="主要资金来源" error={props.errors.counterparties}><RankedCounterparties rows={[...props.counterparties].sort((a, b) => Number(b.in_usd || 0) - Number(a.in_usd || 0)).slice(0, 5)} valueKey="in_usd" onOpen={props.onOpenAddress} /></Section><Section title="主要资金去向" error={props.errors.counterparties}><RankedCounterparties rows={[...props.counterparties].sort((a, b) => Number(b.out_usd || 0) - Number(a.out_usd || 0)).slice(0, 5)} valueKey="out_usd" onOpen={props.onOpenAddress} /></Section></div>
    <RecentActivity onOpenAddress={props.onOpenAddress} onOpenDetail={props.onOpenDetail} />
  </div>;
}

function NoDataState({ onSupplement }: { onSupplement: () => void }) {
  return <div className="xi-no-data"><div className="xi-no-data-icon"><LineChartOutlined /></div><Title level={4}>暂无本地数据</Title><Text type="secondary">该地址尚未进入本地覆盖范围。指标保持未知，不会显示为已确认的 0。</Text><Button type="primary" onClick={onSupplement}>开始补齐数据</Button></div>;
}

function RecentActivity({ onOpenAddress, onOpenDetail }: { onOpenAddress: (address: string) => void; onOpenDetail: (kind: SearchItem["kind"], value: string) => void }) {
  const { state } = useAnalysisContext();
  const [rows, setRows] = useState<ClickHouseActivity[]>([]);
  const [loading, setLoading] = useState(true);
  useEffect(() => { setLoading(true); loadActivity(state.chain, state.rootAddress, "activity", 12).then((page) => setRows(page.items)).catch(() => setRows([])).finally(() => setLoading(false)); }, [state.chain, state.rootAddress]);
  return <Section title="Recent Activity" action={<Button type="link" onClick={() => state && location.assign(`${location.pathname}?tab=transactions`)}>查看全部交易</Button>}><Table className="xi-activity-table" rowKey={(row) => `${row.transaction_hash}:${row.event_index}`} columns={activityColumns(false, state.rootAddress, onOpenAddress, (row) => onOpenDetail("TRANSACTION", row.transaction_hash), () => [])} dataSource={rows} loading={loading} pagination={false} size="small" scroll={{ x: 1280 }} locale={{ emptyText: "暂无最近活动" }} /></Section>;
}

type ActivityFilterControls = {
  state: ReturnType<typeof useAnalysisContext>["state"];
  update: ReturnType<typeof useAnalysisContext>["update"];
	rows: ClickHouseActivity[];
	openFilter?: string;
	setOpenFilter: (key?: string) => void;
};

function ColumnFilterTitle({ label, active, content, open, onOpenChange }: { label: string; active: boolean; content: ReactNode; open: boolean; onOpenChange: (open: boolean) => void }) {
  const triggerRef = useRef<HTMLSpanElement | null>(null);
  useEffect(() => {
    if (!open) return;
    const closeOnOutside = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (!target || triggerRef.current?.contains(target) || (target as Element).closest?.(".ant-popover")) return;
      onOpenChange(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape") onOpenChange(false); };
    document.addEventListener("pointerdown", closeOnOutside, true);
    document.addEventListener("keydown", closeOnEscape, true);
    return () => { document.removeEventListener("pointerdown", closeOnOutside, true); document.removeEventListener("keydown", closeOnEscape, true); };
  }, [onOpenChange, open]);
  return <span ref={triggerRef} className={`xi-column-filter-title${active ? " is-active" : ""}`}><span>{label}</span><Popover content={content} trigger={[]} placement="bottomLeft" open={open} classNames={{ root: "xi-excel-filter-overlay" }}><Button type="text" size="small" aria-label={`筛选${label}`} icon={active ? <FilterFilled /> : <FilterOutlined />} onClick={(event) => { event.stopPropagation(); onOpenChange(!open); }} /></Popover></span>;
}

type ExcelFilterOption = { label: string; value: string };

function ExcelFilterFrame({ label, sort, sortAsc, sortDesc, onSort, onReset, onApply, children }: { label: string; sort: string; sortAsc: string; sortDesc: string; onSort: (sort: string) => void; onReset: () => void; onApply: () => void; children: ReactNode }) {
  return <div className="xi-column-filter-popover xi-excel-filter-menu">
    <div className="xi-excel-filter-caption">筛选字段：<strong>{label}</strong></div>
    <div className="xi-excel-filter-sort">
      <button type="button" className={sort === sortAsc ? "is-active" : ""} onClick={() => onSort(sortAsc)}><CaretUpOutlined /> 升序排列</button>
      <button type="button" className={sort === sortDesc ? "is-active" : ""} onClick={() => onSort(sortDesc)}><CaretDownOutlined /> 降序排列</button>
    </div>
    <div className="xi-excel-filter-divider" />
    {children}
    <div className="xi-excel-filter-actions"><Button size="small" onClick={onReset}>清除筛选</Button><Button size="small" type="primary" onClick={onApply}>确定</Button></div>
  </div>;
}

function ExcelChecklistColumnFilter({ label, values, options, placeholder, multiple = true, allowCustom = false, sort, sortAsc, sortDesc, onSort, onApply }: { label: string; values: string[]; options: ExcelFilterOption[]; placeholder: string; multiple?: boolean; allowCustom?: boolean; sort: string; sortAsc: string; sortDesc: string; onSort: (sort: string) => void; onApply: (values: string[]) => void }) {
  const defaultDraft = useCallback(() => values.length || !multiple ? values : options.map((option) => option.value), [multiple, options, values]);
  const [draft, setDraft] = useState(defaultDraft);
  const [query, setQuery] = useState("");
  useEffect(() => setDraft(defaultDraft()), [defaultDraft]);
  const normalizedQuery = query.trim();
  const mergedOptions = useMemo(() => {
    const result = [...options];
    values.forEach((value) => { if (value && !result.some((option) => option.value === value)) result.push({ label: value, value }); });
    if (allowCustom && normalizedQuery && !result.some((option) => option.value.toLowerCase() === normalizedQuery.toLowerCase())) result.unshift({ label: `使用“${normalizedQuery}”`, value: normalizedQuery });
    return result;
  }, [allowCustom, normalizedQuery, options, values]);
  const visible = mergedOptions.filter((option) => `${option.label} ${option.value}`.toLowerCase().includes(normalizedQuery.toLowerCase())).slice(0, 100);
  const allVisibleSelected = !multiple && !draft.length ? true : Boolean(visible.length) && visible.every((option) => draft.includes(option.value));
  const partiallySelected = visible.some((option) => draft.includes(option.value)) && !allVisibleSelected;
  const toggleAll = (checked: boolean) => setDraft(!multiple ? [] : checked ? [...new Set([...draft, ...visible.map((option) => option.value)])].slice(0, 20) : draft.filter((value) => !visible.some((option) => option.value === value)));
  const toggle = (value: string, checked: boolean) => setDraft(multiple ? checked ? [...new Set([...draft, value])].slice(0, 20) : draft.filter((item) => item !== value) : checked ? [value] : []);
  const reset = () => { setDraft(multiple ? options.map((option) => option.value) : []); setQuery(""); onApply([]); };
  const apply = () => {
    if (!draft.length && multiple) return void message.warning("请至少选择一个筛选值");
    const baseValues = options.map((option) => option.value);
    const allBaseSelected = Boolean(baseValues.length) && baseValues.every((value) => draft.includes(value)) && draft.every((value) => baseValues.includes(value));
		setQuery("");
    onApply(allBaseSelected ? [] : draft);
  };
  return <ExcelFilterFrame label={label} sort={sort} sortAsc={sortAsc} sortDesc={sortDesc} onSort={onSort} onReset={reset} onApply={apply}>
    <Input allowClear prefix={<SearchOutlined />} value={query} placeholder={placeholder} onChange={(event) => { const next = event.target.value; if (!query && next.trim() && !values.length && multiple) setDraft([]); setQuery(next); }} />
    <div className="xi-excel-filter-list">
      <label className="xi-excel-filter-select-all"><Checkbox checked={allVisibleSelected} indeterminate={partiallySelected} onChange={(event) => toggleAll(event.target.checked)} /> <span>全选{normalizedQuery ? "（搜索结果）" : ""}</span></label>
      {visible.length ? visible.map((option) => <label key={option.value} className="xi-excel-filter-option" title={option.value}><Checkbox checked={draft.includes(option.value)} onChange={(event) => toggle(option.value, event.target.checked)} /> <span>{option.label}</span></label>) : <div className="xi-excel-filter-empty">没有匹配项</div>}
    </div>
    <div className="xi-excel-filter-count">{draft.length ? `已选择 ${draft.length} 项` : "当前为全部值"}</div>
  </ExcelFilterFrame>;
}

function TimeColumnFilter({ controls }: { controls: ActivityFilterControls }) {
  const { state, update } = controls;
  const [window, setWindow] = useState<AnalysisWindow>(state.window);
  const [range, setRange] = useState<[dayjs.Dayjs, dayjs.Dayjs] | undefined>(state.from && state.to ? [dayjs(state.from), dayjs(state.to)] : undefined);
  useEffect(() => { setWindow(state.window); setRange(state.from && state.to ? [dayjs(state.from), dayjs(state.to)] : undefined); }, [state.from, state.to, state.window]);
  const reset = () => { setWindow("ALL"); setRange(undefined); update({ window: "ALL", from: undefined, to: undefined }); };
  return <ExcelFilterFrame label="时间" sort={state.sort} sortAsc="time_asc" sortDesc="time_desc" onSort={(sort) => { update({ sort }); controls.setOpenFilter(undefined); }} onReset={() => { reset(); controls.setOpenFilter(undefined); }} onApply={() => { update({ window, from: window === "CUSTOM" ? range?.[0].toISOString() : undefined, to: window === "CUSTOM" ? range?.[1].toISOString() : undefined }); controls.setOpenFilter(undefined); }}>
    <Segmented block value={window} options={WINDOWS} onChange={(value) => setWindow(value as AnalysisWindow)} />
    {window === "CUSTOM" ? <DatePicker.RangePicker showTime value={range} onChange={(value) => setRange(value?.[0] && value?.[1] ? [value[0], value[1]] : undefined)} /> : null}
  </ExcelFilterFrame>;
}

function USDColumnFilter({ controls }: { controls: ActivityFilterControls }) {
  const { state, update } = controls;
  const [minimum, setMinimum] = useState<string | undefined>(state.minUSD);
  const [maximum, setMaximum] = useState<string | undefined>(state.maxUSD);
  useEffect(() => { setMinimum(state.minUSD); setMaximum(state.maxUSD); }, [state.maxUSD, state.minUSD]);
  const [valuations, setValuations] = useState(state.valuationStatuses);
  useEffect(() => setValuations(state.valuationStatuses), [state.valuationStatuses]);
  const reset = () => { setMinimum(undefined); setMaximum(undefined); setValuations([]); update({ minUSD: undefined, maxUSD: undefined, valuationStatuses: [] }); };
  return <ExcelFilterFrame label="当时价值" sort={state.sort} sortAsc="usd_asc" sortDesc="usd_desc" onSort={(sort) => { update({ sort }); controls.setOpenFilter(undefined); }} onReset={() => { reset(); controls.setOpenFilter(undefined); }} onApply={() => { update({ minUSD: minimum, maxUSD: maximum, valuationStatuses: valuations }); controls.setOpenFilter(undefined); }}>
    <div className="xi-filter-pair"><InputNumber stringMode min="0" value={minimum} placeholder="最小 USDT" onChange={(value) => setMinimum(value ?? undefined)} /><InputNumber stringMode min="0" value={maximum} placeholder="最大 USDT" onChange={(value) => setMaximum(value ?? undefined)} /></div>
    <div className="xi-excel-filter-list xi-excel-filter-list-compact">{["VALUED", "LOW_CONFIDENCE", "NO_PRICE"].map((value) => <label key={value} className="xi-excel-filter-option"><Checkbox checked={valuations.includes(value)} onChange={(event) => setValuations(event.target.checked ? [...valuations, value] : valuations.filter((item) => item !== value))} /> <span>{value}</span></label>)}</div>
  </ExcelFilterFrame>;
}

function TokenColumnFilter({ controls }: { controls: ActivityFilterControls }) {
  const { state, update } = controls;
  const [symbol, setSymbol] = useState(state.tokenSymbol || "");
  const [contract, setContract] = useState(state.tokens[0] || "");
  useEffect(() => { setSymbol(state.tokenSymbol || ""); setContract(state.tokens[0] || ""); }, [state.tokenSymbol, state.tokens]);
  const apply = () => {
    const normalized = contract.trim().toLowerCase();
    if (normalized && !/^0x[0-9a-f]{40}$/.test(normalized)) { void message.warning("Token 合约地址格式不正确"); return false; }
    update({ tokenSymbol: symbol.trim() || undefined, tokens: normalized ? [normalized] : [] });
		return true;
  };
  const symbolOptions = uniqueFilterOptions(controls.rows.map((row) => row.token_symbol), state.tokenSymbol ? [state.tokenSymbol] : []);
  const reset = () => { setSymbol(""); setContract(""); update({ tokenSymbol: undefined, tokens: [] }); };
  return <ExcelFilterFrame label="Token" sort={state.sort} sortAsc="token_asc" sortDesc="token_desc" onSort={(sort) => { update({ sort }); controls.setOpenFilter(undefined); }} onReset={() => { reset(); controls.setOpenFilter(undefined); }} onApply={() => { if (apply()) controls.setOpenFilter(undefined); }}>
    <Input allowClear prefix={<SearchOutlined />} value={symbol} onChange={(event) => setSymbol(event.target.value)} placeholder="搜索或输入 Token Symbol" />
    <div className="xi-excel-filter-list xi-excel-filter-list-compact">{symbolOptions.filter((option) => !symbol || option.value.toLowerCase().includes(symbol.toLowerCase())).map((option) => <label key={option.value} className="xi-excel-filter-option"><Checkbox checked={symbol === option.value} onChange={(event) => setSymbol(event.target.checked ? option.value : "")} /> <span>{option.label}</span></label>)}</div>
    <Input allowClear value={contract} onChange={(event) => setContract(event.target.value)} onPressEnter={apply} placeholder="Token 合约地址（可选）" />
  </ExcelFilterFrame>;
}

function uniqueFilterOptions(values: Array<unknown>, selected: string[] = []): ExcelFilterOption[] {
  const unique = new Set<string>();
  [...selected, ...values.map((value) => String(value || "").trim())].forEach((value) => { if (value && value !== "--") unique.add(value); });
  return [...unique].sort((left, right) => left.localeCompare(right, "zh-CN", { numeric: true })).slice(0, 20).map((value) => ({ label: value, value }));
}

function TransferStatsBar({ stats, loading, error, tokenMode }: { stats: ClickHouseActivityStats | null; loading: boolean; error: string; tokenMode: boolean }) {
  const ratio = stats?.activity_count ? `${number((stats.priced_count / stats.activity_count) * 100)}%` : "--";
  const items = [
    [tokenMode ? "转账记录" : "活动记录", number(stats?.activity_count)], ["唯一交易", number(stats?.transaction_count)],
    ["转入 / 转出", `${number(stats?.incoming_count)} / ${number(stats?.outgoing_count)}`], ["唯一对手", number(stats?.unique_counterparties)],
    ["已定价流入", usd(stats?.incoming_usd)], ["已定价流出", usd(stats?.outgoing_usd)],
    ["已定价净额", usd(stats?.netflow_usd)], ["估值覆盖", ratio],
  ];
  if (error) return <div className="xi-transfer-stats-error">统计加载失败：{error}</div>;
  return <div className={`xi-transfer-stats${loading ? " is-loading" : ""}`}>{items.map(([label, value]) => <div key={label}><span>{label}</span><strong className={label === "已定价流入" ? "positive" : label === "已定价流出" ? "negative" : ""}>{loading && !stats ? "…" : value}</strong></div>)}</div>;
}

function ActivityTable({ kind, coverage, tokenMode = false, onOpenAddress, onOpenActivity, onNavigate, onFilter }: { kind: string; coverage: string; tokenMode?: boolean; onOpenAddress: (address: string) => void; onOpenActivity: (row: ClickHouseActivity, tokenMode: boolean) => void; onNavigate: (page: string, address?: string) => void; onFilter: () => void }) {
  const { state, update, clearFilters } = useAnalysisContext();
  const [page, setPage] = useState<{ items: ClickHouseActivity[]; next_cursor?: string; has_more: boolean }>({ items: [], has_more: false });
	const [pageScope, setPageScope] = useState("");
	const [stats, setStats] = useState<ClickHouseActivityStats | null>(null);
	const [statsLoading, setStatsLoading] = useState(false);
	const [statsError, setStatsError] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [cursor, setCursor] = useState<string | undefined>();
  const [history, setHistory] = useState<Array<string | undefined>>([]);
  const [pageNumber, setPageNumber] = useState(1);
  const [quick, setQuick] = useState<QuickFilter>(() => quickFromState(state));
  const [contextRow, setContextRow] = useState<ClickHouseActivity | null>(null);
  const [layout, setLayout] = useState<ColumnLayout>(() => loadColumnLayout(kind, tokenMode));
	const [openColumnFilter, setOpenColumnFilter] = useState<string>();
	const filterOptionScope = `${state.chain}:${state.rootAddress}:${kind}`;
	const [filterOptionSnapshot, setFilterOptionSnapshot] = useState<{ scope: string; rows: ClickHouseActivity[] }>({ scope: "", rows: [] });
  const tableScrollRef = useRef<HTMLDivElement | null>(null);

  const filterSignature = useMemo(() => JSON.stringify([state.transactionHash, state.direction, state.tokens, state.tokenSymbol, state.minUSD, state.maxUSD, state.entityFilters, state.entity, state.entityRole, state.protocolFilters, state.methodFilters, state.statusFilters, state.valuationStatuses, state.counterparty, state.fromAddress, state.toAddress, state.window, state.from, state.to]), [state.counterparty, state.direction, state.entity, state.entityFilters, state.entityRole, state.from, state.fromAddress, state.maxUSD, state.methodFilters, state.minUSD, state.protocolFilters, state.statusFilters, state.to, state.toAddress, state.tokenSymbol, state.tokens, state.transactionHash, state.valuationStatuses, state.window]);
	const requestFilters = useMemo(() => activityRequestFilters(state), [filterSignature]);
	const queryCursor = pageScope === filterSignature ? cursor : undefined;
  const cacheKey = useMemo(() => JSON.stringify([state.chain, state.rootAddress, kind, filterSignature, state.pageSize, queryCursor]), [filterSignature, kind, queryCursor, state.chain, state.pageSize, state.rootAddress]);
  const load = useCallback(async (next?: string, background = false) => {
    const key = JSON.stringify([state.chain, state.rootAddress, kind, filterSignature, state.pageSize, next]);
    const cached = ACTIVITY_CACHE.get(key);
    if (cached && cached.expires > Date.now()) { setPage(cached.value); setPageScope(filterSignature); return cached.value; }
    if (!background) setLoading(true);
    setError("");
    try {
      const value = await loadActivity(state.chain, state.rootAddress, kind, state.pageSize, next, requestFilters);
      ACTIVITY_CACHE.set(key, { expires: Date.now() + CACHE_TTL_MS, value });
      if (!background) { setPage(value); setPageScope(filterSignature); }
      return value;
    } catch (reason) {
      if (!background) setError(reason instanceof Error ? reason.message : "活动加载失败");
      return undefined;
    } finally { if (!background) setLoading(false); }
  }, [filterSignature, kind, requestFilters, state.chain, state.pageSize, state.rootAddress]);

  useEffect(() => { void load(queryCursor); }, [cacheKey, load, queryCursor]);
  useEffect(() => {
    if (pageScope !== filterSignature || !page.has_more || !page.next_cursor) return;
    const timer = window.setTimeout(() => { void load(page.next_cursor, true); }, 300);
    return () => window.clearTimeout(timer);
  }, [filterSignature, load, page.has_more, page.next_cursor, pageScope]);
  useEffect(() => { saveColumnLayout(kind, layout); }, [kind, layout]);
  useEffect(() => { setCursor(undefined); setHistory([]); setPageNumber(1); }, [filterSignature, kind, state.chain, state.rootAddress, state.pageSize]);
  useEffect(() => { setQuick(quickFromState(state)); }, [filterSignature]);
	useEffect(() => {
		let active = true;
		setStatsLoading(true); setStatsError("");
		void loadActivityStats(state.chain, state.rootAddress, kind, requestFilters).then((value) => { if (active) setStats(value); }).catch((reason) => { if (active) setStatsError(reason instanceof Error ? reason.message : "统计加载失败"); }).finally(() => { if (active) setStatsLoading(false); });
		return () => { active = false; };
	}, [filterSignature, kind, requestFilters, state.chain, state.rootAddress]);

  const applyQuick = (value: QuickFilter) => {
    setQuick(value);
    if (value === "all") clearFilters();
    else if (value === "in" || value === "out") update({ direction: value });
    else if (value === "large") update({ minUSD: "100000" });
    else if (value === "cex") update({ entityFilters: ["CEX"] });
    else if (value === "dex") update({ protocolFilters: ["DEX"] });
    else if (value === "bridge") update({ protocolFilters: ["BRIDGE"] });
    else if (value === "usdt") update({ tokenSymbol: "USDT" });
  };
	const rows = useMemo(() => sortActivityRows(page.items, state.sort), [page.items, state.sort]);
	useEffect(() => {
		let active = true;
		const cached = FILTER_OPTION_CACHE.get(filterOptionScope);
		if (cached && cached.expires > Date.now()) {
			setFilterOptionSnapshot({ scope: filterOptionScope, rows: cached.rows });
			return () => { active = false; };
		}
		void (async () => {
			const collected: ClickHouseActivity[] = [];
			let nextCursor: string | undefined;
			do {
				const result = await loadActivity(state.chain, state.rootAddress, kind, FILTER_OPTION_PAGE_SIZE, nextCursor);
				collected.push(...result.items);
				nextCursor = result.has_more ? result.next_cursor : undefined;
			} while (nextCursor && collected.length < FILTER_OPTION_ROW_LIMIT);
			if (!active) return;
			const optionRows = collected.slice(0, FILTER_OPTION_ROW_LIMIT);
			FILTER_OPTION_CACHE.set(filterOptionScope, { expires: Date.now() + CACHE_TTL_MS, rows: optionRows });
			setFilterOptionSnapshot({ scope: filterOptionScope, rows: optionRows });
		})().catch(() => { /* The visible table remains the fallback option source. */ });
		return () => { active = false; };
	}, [filterOptionScope, kind, state.chain, state.rootAddress]);
	useEffect(() => {
		if (openColumnFilter) return;
		setFilterOptionSnapshot((current) => {
			const merged = new Map<string, ClickHouseActivity>();
			if (current.scope === filterOptionScope) current.rows.forEach((row) => merged.set(activityKey(row), row));
			rows.forEach((row) => merged.set(activityKey(row), row));
			return { scope: filterOptionScope, rows: [...merged.values()] };
		});
	}, [filterOptionScope, openColumnFilter, rows]);
	useEffect(() => { setOpenColumnFilter(undefined); }, [kind, state.chain, state.rootAddress]);
	const filterOptionRows = filterOptionSnapshot.scope === filterOptionScope ? filterOptionSnapshot.rows : [];
	const filterControls = useMemo<ActivityFilterControls>(() => ({ state, update, rows: filterOptionRows, openFilter: openColumnFilter, setOpenFilter: setOpenColumnFilter }), [filterOptionRows, openColumnFilter, state, update]);
  const allColumns = useMemo(() => activityColumns(tokenMode, state.rootAddress, onOpenAddress, onOpenActivity, (row) => rowActions(row, tokenMode, state.rootAddress, onOpenAddress, onOpenActivity, onNavigate, update), filterControls), [filterControls, onNavigate, onOpenActivity, onOpenAddress, state.rootAddress, tokenMode, update]);
  const columns = useMemo(() => layout.order.map((key) => allColumns.find((column) => String(column.key) === key)).filter((column): column is NonNullable<typeof column> => Boolean(column) && layout.visible.includes(String(column?.key))).map((column) => { const pin = layout.pins[String(column.key)]; return { ...column, fixed: pin === "left" ? "left" as const : pin === "right" ? "right" as const : undefined }; }), [allColumns, layout]);
  const selectedKeys = state.selectedRows.filter((key) => rows.some((row) => activityKey(row) === key));
  const selected = rows.filter((row) => selectedKeys.includes(activityKey(row)));
  const contextMenu = contextRow ? rowActions(contextRow, tokenMode, state.rootAddress, onOpenAddress, onOpenActivity, onNavigate, update) : [];
  const emptyText = page.items.length > 0 ? "没有符合当前筛选条件的数据" : coverage === "PARTIAL" ? "数据覆盖不完整" : "暂无链上活动数据";
  const goNext = () => { if (!page.next_cursor) return; setHistory((items) => [...items, cursor]); setCursor(page.next_cursor); setPageNumber((value) => value + 1); tableScrollRef.current?.scrollIntoView({ block: "start" }); };
  const goPrevious = () => { const prior = [...history]; setCursor(prior.pop()); setHistory(prior); setPageNumber((value) => Math.max(1, value - 1)); tableScrollRef.current?.scrollIntoView({ block: "start" }); };
  const bulkMenu = [
    { key: "fund", label: "加入资金流", icon: <NodeIndexOutlined /> },
    { key: "export", label: "导出所选", icon: <DownloadOutlined /> },
    { key: "evidence", label: "创建调查证据", icon: <FolderAddOutlined /> },
    { key: "tag", label: "标记地址", icon: <TagsOutlined /> },
  ];
  const runBulk = (key: string) => {
    if (!selected.length) return void message.warning("请先选择交易");
    update({ selectedRows: selected.map(activityKey) });
    if (key === "fund") onNavigate("analytics-graph", state.rootAddress);
    else if (key === "evidence") onNavigate("intelligence", state.rootAddress);
    else if (key === "export") downloadActivityCSV(selected, `${kind}-selected.csv`);
    else saveAddressTags(selected, state.rootAddress);
  };
  return <Section title={tokenMode ? "代币转账" : "交易活动"} error={error} action={<Space><SavedViewsControl currentLayout={layout} onApplyLayout={setLayout} /><Popover content={<ColumnManager columns={allColumns} layout={layout} onChange={setLayout} tokenMode={tokenMode} />} trigger="click" placement="bottomRight"><Button icon={<SettingOutlined />}>列</Button></Popover><Button icon={<ReloadOutlined />} onClick={() => { ACTIVITY_CACHE.delete(cacheKey); void load(cursor); }}>刷新</Button></Space>}>
	<TransferStatsBar stats={stats} loading={statsLoading} error={statsError} tokenMode={tokenMode} />
    <div className="xi-table-sticky" ref={tableScrollRef}>
      <div className="xi-table-toolbar"><Segmented value={quick} onChange={(value) => applyQuick(value as QuickFilter)} options={[{ value: "all", label: "全部" }, { value: "in", label: "转入" }, { value: "out", label: "转出" }, { value: "large", label: "大额" }, { value: "cex", label: "CEX" }, { value: "dex", label: "DEX" }, { value: "bridge", label: "Bridge" }, { value: "usdt", label: "USDT" }]} /><Space><Select className="xi-sort-select" value={state.sort} onChange={(sort) => update({ sort })} options={ACTIVITY_SORT_OPTIONS} /><Button icon={<FilterOutlined />} onClick={onFilter}>高级筛选</Button></Space></div>
      <FilterChips onClear={clearFilters} />
      {selected.length ? <div className="xi-bulk-bar"><Text strong>已选择 {selected.length} 项</Text><Space>{bulkMenu.map((item) => <Button key={item.key} size="small" icon={item.icon} onClick={() => runBulk(item.key)}>{item.label}</Button>)}</Space></div> : null}
    </div>
    {loading && !page.items.length ? <div className="xi-table-skeleton">{Array.from({ length: 8 }, (_, index) => <Skeleton key={index} active title={false} paragraph={{ rows: 1, width: [index % 2 ? "92%" : "86%"] }} />)}</div> : <Dropdown trigger={["contextMenu"]} menu={{ items: contextMenu }}><div onContextMenu={(event) => { const row = (event.target as HTMLElement).closest("tr[data-row-key]")?.getAttribute("data-row-key"); setContextRow(rows.find((item) => activityKey(item) === row) || null); }}><Table className="xi-activity-table xi-activity-table-v2" rowKey={activityKey} columns={columns} dataSource={rows} loading={loading && Boolean(page.items.length)} pagination={false} size="small" sticky scroll={{ x: 1500 }} rowSelection={{ selectedRowKeys: selectedKeys, preserveSelectedRowKeys: true, onChange: (keys) => update({ selectedRows: keys.map(String) }) }} onRow={(row) => ({ tabIndex: 0, onClick: (event) => { if (!(event.target as HTMLElement).closest("button,a,input,.ant-dropdown")) onOpenActivity(row, tokenMode); }, onKeyDown: (event) => { if (event.key === "Enter") onOpenActivity(row, tokenMode); } })} locale={{ emptyText }} /></div></Dropdown>}
    <div className="xi-pagination"><Text type="secondary">第 {pageNumber} 页 · 本页 {rows.length} 条{loading ? " · 正在更新" : ""}</Text><Space><Select value={state.pageSize} onChange={(pageSize) => update({ pageSize })} options={[50, 100, 200].map((value) => ({ value, label: `${value} / 页` }))} /><Button disabled={!history.length || loading} onClick={goPrevious}>上一页</Button><Button disabled={!page.has_more || !page.next_cursor || loading} onClick={goNext}>下一页</Button></Space></div>
  </Section>;
}

function activityColumns(tokenMode: boolean, rootAddress: string, onOpenAddress: (address: string) => void, _onOpenActivity: (row: ClickHouseActivity, tokenMode: boolean) => void, actions: (row: ClickHouseActivity) => Array<{ key: string; label: string; icon?: ReactNode; onClick?: () => void }>, controls?: ActivityFilterControls): ColumnsType<ClickHouseActivity> {
  const fromAddress = (row: ClickHouseActivity) => row.direction === "IN" ? row.counterparty_address : row.address || rootAddress;
  const toAddress = (row: ClickHouseActivity) => row.direction === "IN" ? row.address || rootAddress : row.counterparty_address;
  const entity = (address: string, row: ClickHouseActivity) => address === row.counterparty_address ? row.counterparty_label : "";
	const title = (key: string, label: string, active: boolean, content: ReactNode) => controls ? <ColumnFilterTitle label={label} active={active} content={content} open={controls.openFilter === key} onOpenChange={(open) => controls.setOpenFilter(open ? key : undefined)} /> : label;
	const filterState = controls?.state;
	const filterRows = controls?.rows || [];
	const txOptions = uniqueFilterOptions(filterRows.map((row) => row.transaction_hash), filterState?.transactionHash ? [filterState.transactionHash] : []).map((option) => ({ ...option, label: short(option.value, 12, 10) }));
	const methodOptions = uniqueFilterOptions(filterRows.map((row) => row.method_name || row.method_id), filterState?.methodFilters);
	const fromOptions = uniqueFilterOptions(filterRows.map(fromAddress), filterState?.fromAddress ? [filterState.fromAddress] : []).map((option) => ({ ...option, label: short(option.value, 12, 10) }));
	const toOptions = uniqueFilterOptions(filterRows.map(toAddress), filterState?.toAddress ? [filterState.toAddress] : []).map((option) => ({ ...option, label: short(option.value, 12, 10) }));
  return [
    { key: "tx", title: title("tx", "交易哈希", Boolean(filterState?.transactionHash), <ExcelChecklistColumnFilter label="交易哈希" values={filterState?.transactionHash ? [filterState.transactionHash] : []} options={txOptions} placeholder="搜索或输入完整交易哈希" multiple={false} allowCustom sort={filterState?.sort || "time_desc"} sortAsc="tx_asc" sortDesc="tx_desc" onSort={(sort) => { controls?.update({ sort }); controls?.setOpenFilter(undefined); }} onApply={(values) => { controls?.update({ transactionHash: values[0] || undefined }); controls?.setOpenFilter(undefined); }} />), dataIndex: "transaction_hash", width: 156, fixed: "left", render: (value, row) => <TxHashLink chainId={row.chain_id} hash={String(value)} /> },
    { key: "method", title: title("method", "方法", Boolean(filterState?.methodFilters.length), <ExcelChecklistColumnFilter label="方法" values={filterState?.methodFilters || []} options={methodOptions} placeholder="搜索或输入方法名" allowCustom sort={filterState?.sort || "time_desc"} sortAsc="method_asc" sortDesc="method_desc" onSort={(sort) => { controls?.update({ sort }); controls?.setOpenFilter(undefined); }} onApply={(methodFilters) => { controls?.update({ methodFilters }); controls?.setOpenFilter(undefined); }} />), width: 112, render: (_, row) => row.method_name || row.method_id || "--" },
    { key: "time", title: title("time", "时间", Boolean(filterState && (filterState.window !== "ALL" || filterState.from || filterState.to)), controls ? <TimeColumnFilter controls={controls} /> : null), dataIndex: "block_time", width: 154, render: (value) => date(value) },
    { key: "from", title: title("from", "发送方", Boolean(filterState?.fromAddress), <ExcelChecklistColumnFilter label="发送方" values={filterState?.fromAddress ? [filterState.fromAddress] : []} options={fromOptions} placeholder="搜索或输入发送方地址" multiple={false} allowCustom sort={filterState?.sort || "time_desc"} sortAsc="from_asc" sortDesc="from_desc" onSort={(sort) => { controls?.update({ sort }); controls?.setOpenFilter(undefined); }} onApply={(values) => { controls?.update({ fromAddress: values[0]?.toLowerCase() || undefined }); controls?.setOpenFilter(undefined); }} />), width: 176, render: (_, row) => { const address = fromAddress(row); return <AddressIdentity value={address} label={entity(address, row)} onOpen={() => onOpenAddress(address)} />; } },
    { key: "direction", title: title("direction", "方向", Boolean(filterState && filterState.direction !== "all"), <ExcelChecklistColumnFilter label="方向" values={filterState && filterState.direction !== "all" ? [filterState.direction] : []} options={[{ label: "转入", value: "in" }, { label: "转出", value: "out" }, { label: "自转", value: "self" }]} placeholder="搜索方向" multiple={false} sort={filterState?.sort || "time_desc"} sortAsc="direction_asc" sortDesc="direction_desc" onSort={(sort) => { controls?.update({ sort }); controls?.setOpenFilter(undefined); }} onApply={(values) => { controls?.update({ direction: (values[0] || "all") as AnalysisDirection }); controls?.setOpenFilter(undefined); }} />), width: 82, render: (_, row) => <span className={`xi-direction xi-direction-${row.direction.toLowerCase()}`}>{row.direction === "IN" ? <><ArrowLeftOutlined /> 转入</> : row.direction === "OUT" ? <>转出 <ArrowRightOutlined /></> : "自转"}</span> },
    { key: "to", title: title("to", "接收方", Boolean(filterState?.toAddress), <ExcelChecklistColumnFilter label="接收方" values={filterState?.toAddress ? [filterState.toAddress] : []} options={toOptions} placeholder="搜索或输入接收方地址" multiple={false} allowCustom sort={filterState?.sort || "time_desc"} sortAsc="to_asc" sortDesc="to_desc" onSort={(sort) => { controls?.update({ sort }); controls?.setOpenFilter(undefined); }} onApply={(values) => { controls?.update({ toAddress: values[0]?.toLowerCase() || undefined }); controls?.setOpenFilter(undefined); }} />), width: 176, render: (_, row) => { const address = toAddress(row); return <AddressIdentity value={address} label={entity(address, row)} onOpen={() => onOpenAddress(address)} />; } },
    { key: "value", title: tokenMode ? "数量" : "价值", dataIndex: "amount", align: "right", width: 112, render: (value) => number(value) },
    ...(tokenMode ? [{ key: "token", title: title("token", "Token", Boolean(filterState && (filterState.tokens.length || filterState.tokenSymbol)), controls ? <TokenColumnFilter controls={controls} /> : null), width: 220, render: (_: unknown, row: ClickHouseActivity) => <TokenIdentity chainId={row.chain_id} symbol={row.token_symbol} name={row.token_name} address={row.token_address} logoURI={row.token_logo_uri} verified={row.token_verified} spam={row.token_spam} /> }] : []),
    { key: "usd", title: title("usd", "当时价值", Boolean(filterState && (filterState.minUSD || filterState.maxUSD || filterState.valuationStatuses.length)), controls ? <USDColumnFilter controls={controls} /> : null), align: "right", width: 180, render: (_, row) => <HistoricalPriceCell priceUSD={row.historical_price_usdt ?? row.price_usd} usdValue={row.historical_value_usdt ?? row.usd_value} priceTime={row.price_timestamp ?? row.price_time} source={row.price_source} confidence={row.price_confidence} symbol={row.token_symbol || "BNB"} route={row.price_route} priceType={row.price_type} ageSeconds={row.price_age_seconds} valuationStatus={row.valuation_status} /> },
    ...(!tokenMode ? [{ key: "fee", title: "手续费", align: "right" as const, width: 92, render: () => "--" }] : []),
    { key: "status", title: title("status", "状态", Boolean(filterState?.statusFilters.length), <ExcelChecklistColumnFilter label="状态" values={filterState?.statusFilters || []} options={["SUCCESS", "FAILED", "UNKNOWN"].map((value) => ({ label: value === "SUCCESS" ? "成功" : value === "FAILED" ? "失败" : "未知", value }))} placeholder="搜索状态" sort={filterState?.sort || "time_desc"} sortAsc="status_asc" sortDesc="status_desc" onSort={(sort) => { controls?.update({ sort }); controls?.setOpenFilter(undefined); }} onApply={(statusFilters) => { controls?.update({ statusFilters }); controls?.setOpenFilter(undefined); }} />), dataIndex: "status", width: 82, render: (value) => value === "SUCCESS" ? <Tag color="success">成功</Tag> : value === "FAILED" ? <Tag color="error">失败</Tag> : <Tag>未知</Tag> },
    { key: "from_entity", title: "From Entity", width: 130, render: (_, row) => row.direction === "OUT" ? "--" : row.counterparty_label || row.counterparty_entity_type || "--" },
    { key: "to_entity", title: "To Entity", width: 130, render: (_, row) => row.direction === "IN" ? "--" : row.counterparty_label || row.counterparty_entity_type || "--" },
    { key: "protocol", title: "协议", width: 110, render: (_, row) => activityProtocol(row) || "--" },
    { key: "contract", title: "合约", width: 140, render: (_, row) => row.token_address ? short(row.token_address) : "--" },
    { key: "gas_used", title: "Gas Used", align: "right", width: 105, render: (_, row) => String((row as unknown as Record<string, unknown>).gas_used ?? "--") },
    { key: "gas_price", title: "Gas Price", align: "right", width: 105, render: (_, row) => String((row as unknown as Record<string, unknown>).gas_price ?? "--") },
    { key: "actions", title: "", fixed: "right", width: 48, render: (_, row) => <Dropdown menu={{ items: actions(row) }} trigger={["click"]}><Button type="text" icon={<MoreOutlined />} aria-label="交易操作" onClick={(event) => event.stopPropagation()} /></Dropdown> },
  ];
}

function activityKey(row: ClickHouseActivity): string { return `${row.transaction_hash}:${row.event_index}:${row.address}`; }
function historicalUSDT(row: ClickHouseActivity): number { return Number(row.historical_value_usdt ?? row.usd_value ?? Number.NaN); }
function activityProtocol(row: ClickHouseActivity): string { const text = `${row.activity_type} ${row.method_name}`.toUpperCase(); return text.includes("BRIDGE") ? "BRIDGE" : ["SWAP", "DEX", "PAIR", "LIQUIDITY"].some((item) => text.includes(item)) ? "DEX" : ""; }
function quickFromState(state: ReturnType<typeof useAnalysisContext>["state"]): QuickFilter { if (state.tokenSymbol === "USDT") return "usdt"; if (state.entityFilters.includes("CEX")) return "cex"; if (state.protocolFilters.includes("DEX")) return "dex"; if (state.protocolFilters.includes("BRIDGE")) return "bridge"; if (state.minUSD === "100000") return "large"; return state.direction === "in" || state.direction === "out" ? state.direction : "all"; }

function activityRequestFilters(state: ReturnType<typeof useAnalysisContext>["state"]): ActivityFilterParams {
  const cutoffDays: Record<string, number> = { "24H": 1, "7D": 7, "30D": 30, "90D": 90, "1Y": 365 };
	const derivedFrom = cutoffDays[state.window] ? dayjs().subtract(cutoffDays[state.window], "day").toISOString() : undefined;
	return {
		transactionHash: state.transactionHash, direction: state.direction === "all" ? undefined : state.direction,
		counterparty: state.counterparty, fromAddress: state.fromAddress, toAddress: state.toAddress,
		tokens: state.tokens, tokenSymbol: state.tokenSymbol, minUSD: state.minUSD, maxUSD: state.maxUSD,
		methods: state.methodFilters, statuses: state.statusFilters, entityTypes: state.entityFilters, entity: state.entity,
		protocols: state.protocolFilters, valuationStatuses: state.valuationStatuses,
		from: state.window === "CUSTOM" ? state.from : derivedFrom, to: state.window === "CUSTOM" ? state.to : undefined,
	};
}

function sortActivityRows(rows: ClickHouseActivity[], sort: string): ClickHouseActivity[] {
	const descending = sort.endsWith("_desc");
	const field = sort.replace(/_(asc|desc)$/, "");
	const value = (row: ClickHouseActivity): string | number => {
		if (field === "time") return dayjs(row.block_time).valueOf();
		if (field === "usd") return historicalUSDT(row) || 0;
		if (field === "tx") return row.transaction_hash || "";
		if (field === "method") return row.method_name || row.method_id || "";
		if (field === "from") return row.direction === "IN" ? row.counterparty_address : row.address;
		if (field === "to") return row.direction === "IN" ? row.address : row.counterparty_address;
		if (field === "direction") return row.direction || "";
		if (field === "token") return row.token_symbol || row.token_address || "";
		if (field === "status") return row.status || "";
		return dayjs(row.block_time).valueOf();
	};
	return [...rows].sort((left, right) => {
		const a = value(left); const b = value(right);
		const result = typeof a === "number" && typeof b === "number" ? a - b : String(a).localeCompare(String(b), "zh-CN", { numeric: true });
		return descending ? -result : result;
	});
}

function rowActions(row: ClickHouseActivity, tokenMode: boolean, rootAddress: string, onOpenAddress: (address: string) => void, onOpenActivity: (row: ClickHouseActivity, tokenMode: boolean) => void, onNavigate: (page: string, address?: string) => void, update: ReturnType<typeof useAnalysisContext>["update"]) {
  const from = row.direction === "IN" ? row.counterparty_address : row.address || rootAddress;
  const to = row.direction === "IN" ? row.address || rootAddress : row.counterparty_address;
  const pass = (page: string) => { update({ selectedRows: [activityKey(row)] }); onNavigate(page, rootAddress); };
  return [
    { key: "view", label: "查看交易", onClick: () => onOpenActivity(row, tokenMode) },
    { key: "from", label: "查看 From 地址", onClick: () => onOpenAddress(from) },
    { key: "to", label: "查看 To 地址", onClick: () => onOpenAddress(to) },
    { key: "fund", label: "加入资金追踪", onClick: () => pass("analytics-graph") },
    { key: "upstream", label: "向上追踪", onClick: () => { update({ direction: "in", selectedRows: [activityKey(row)] }); onNavigate("analytics-graph", rootAddress); } },
    { key: "downstream", label: "向下追踪", onClick: () => { update({ direction: "out", selectedRows: [activityKey(row)] }); onNavigate("analytics-graph", rootAddress); } },
    { key: "investigate", label: "启动调查", onClick: () => pass("intelligence") },
    { key: "export", label: "导出此交易", onClick: () => downloadActivityCSV([row], `${row.transaction_hash}.csv`) },
    { key: "copy", label: "复制 Tx Hash", onClick: () => void navigator.clipboard.writeText(row.transaction_hash).then(() => message.success("Tx Hash 已复制")) },
  ];
}

function FilterChips({ onClear }: { onClear: () => void }) {
  const { state, update } = useAnalysisContext();
  const chips: Array<{ key: string; label: string; clear: () => void }> = [];
	if (state.transactionHash) chips.push({ key: "transaction-hash", label: `Tx: ${short(state.transactionHash)}`, clear: () => update({ transactionHash: undefined }) });
  if (state.direction !== "all") chips.push({ key: "direction", label: state.direction.toUpperCase(), clear: () => update({ direction: "all" }) });
	if (state.fromAddress) chips.push({ key: "from-address", label: `From: ${short(state.fromAddress)}`, clear: () => update({ fromAddress: undefined }) });
	if (state.toAddress) chips.push({ key: "to-address", label: `To: ${short(state.toAddress)}`, clear: () => update({ toAddress: undefined }) });
  if (state.tokenSymbol) chips.push({ key: "token-symbol", label: state.tokenSymbol, clear: () => update({ tokenSymbol: undefined }) });
  state.tokens.forEach((token) => chips.push({ key: `token-${token}`, label: `Token: ${short(token)}`, clear: () => update({ tokens: state.tokens.filter((item) => item !== token) }) }));
  if (state.minUSD) chips.push({ key: "min", label: `≥ ${usd(state.minUSD)}`, clear: () => update({ minUSD: undefined }) });
  if (state.maxUSD) chips.push({ key: "max", label: `≤ ${usd(state.maxUSD)}`, clear: () => update({ maxUSD: undefined }) });
  state.entityFilters.forEach((entity) => chips.push({ key: `entity-${entity}`, label: `Entity: ${entity}`, clear: () => update({ entityFilters: state.entityFilters.filter((item) => item !== entity) }) }));
  state.protocolFilters.forEach((protocol) => chips.push({ key: `protocol-${protocol}`, label: `Protocol: ${protocol}`, clear: () => update({ protocolFilters: state.protocolFilters.filter((item) => item !== protocol) }) }));
  state.methodFilters.forEach((method) => chips.push({ key: `method-${method}`, label: `Method: ${method}`, clear: () => update({ methodFilters: state.methodFilters.filter((item) => item !== method) }) }));
  state.statusFilters.forEach((status) => chips.push({ key: `status-${status}`, label: `Status: ${status}`, clear: () => update({ statusFilters: state.statusFilters.filter((item) => item !== status) }) }));
	state.valuationStatuses.forEach((status) => chips.push({ key: `valuation-${status}`, label: `估值: ${status}`, clear: () => update({ valuationStatuses: state.valuationStatuses.filter((item) => item !== status) }) }));
  if (state.window !== "ALL") chips.push({ key: "window", label: state.window, clear: () => update({ window: "ALL", from: undefined, to: undefined }) });
  if (!chips.length) return null;
  return <div className="xi-active-chips">{chips.map((chip) => <Tag closable key={chip.key} onClose={chip.clear}>{chip.label}</Tag>)}<Button type="link" size="small" onClick={onClear}>Clear All</Button></div>;
}

function ColumnManager({ columns, layout, onChange, tokenMode }: { columns: ColumnsType<ClickHouseActivity>; layout: ColumnLayout; onChange: (layout: ColumnLayout) => void; tokenMode: boolean }) {
  const presets: Record<string, string[]> = {
    默认: defaultVisibleColumns(tokenMode),
    调查: ["tx", "time", "from", "from_entity", "direction", "to", "to_entity", "usd", "method", "status", "actions"],
    资金: ["tx", "time", "from", "direction", "to", "value", ...(tokenMode ? ["token"] : []), "usd", "fee", "actions"],
    合约: ["tx", "method", "from", "to", "contract", "protocol", "status", "actions"],
    紧凑: ["tx", "time", "direction", "value", ...(tokenMode ? ["token"] : []), "usd", "actions"],
  };
  const move = (key: string, delta: number) => {
    const order = [...layout.order]; const index = order.indexOf(key); const target = index + delta;
    if (index < 0 || target < 0 || target >= order.length) return;
    [order[index], order[target]] = [order[target], order[index]];
    onChange({ ...layout, order });
  };
  return <div className="xi-column-manager"><div className="xi-column-presets">{Object.keys(presets).map((name) => <Button key={name} size="small" onClick={() => onChange({ ...layout, visible: presets[name].filter((key) => layout.order.includes(key)) })}>{name}</Button>)}</div><div className="xi-column-list">{layout.order.map((key, index) => { const column = columns.find((item) => String(item.key) === key); if (!column) return null; return <div key={key} draggable onDragStart={(event) => event.dataTransfer.setData("text/plain", key)} onDragOver={(event) => event.preventDefault()} onDrop={(event) => { const from = event.dataTransfer.getData("text/plain"); const order = layout.order.filter((item) => item !== from); const target = order.indexOf(key); order.splice(target, 0, from); onChange({ ...layout, order }); }}><Checkbox checked={layout.visible.includes(key)} disabled={key === "actions"} onChange={(event) => onChange({ ...layout, visible: event.target.checked ? [...layout.visible, key] : layout.visible.filter((item) => item !== key) })}>{String(column.title || "操作")}</Checkbox><span><Button type="text" size="small" icon={<CaretUpOutlined />} disabled={index === 0} onClick={() => move(key, -1)} /><Button type="text" size="small" icon={<CaretDownOutlined />} disabled={index === layout.order.length - 1} onClick={() => move(key, 1)} /><Select size="small" value={layout.pins[key] || "none"} onChange={(pin) => onChange({ ...layout, pins: { ...layout.pins, [key]: pin } })} options={[{ value: "none", label: "不固定" }, { value: "left", label: "左固定" }, { value: "right", label: "右固定" }]} /></span></div>; })}</div><Button block onClick={() => onChange(defaultColumnLayout(tokenMode))}>Reset</Button></div>;
}

function SavedViewsControl({ currentLayout, onApplyLayout }: { currentLayout: ColumnLayout; onApplyLayout: (layout: ColumnLayout) => void }) {
  const { state, update } = useAnalysisContext();
  const [views, setViews] = useState<SavedExplorerView[]>(readSavedViews);
  const save = () => {
    const name = window.prompt("视图名称", suggestedViewName(state));
    if (!name?.trim()) return;
    const allLayouts = readColumnLayouts();
    allLayouts[state.tab] = currentLayout;
    const next = [{ id: `${Date.now()}`, name: name.trim().slice(0, 40), state: { ...state, selectedRows: [] }, columnLayouts: allLayouts, savedAt: new Date().toISOString() }, ...views].slice(0, 30);
    localStorage.setItem(SAVED_VIEWS_KEY, JSON.stringify(next)); setViews(next); void message.success("视图已保存");
  };
  const content = <div className="xi-saved-views"><Button type="primary" block icon={<SaveOutlined />} onClick={save}>保存当前视图</Button>{views.length ? views.map((view) => <div key={view.id}><button type="button" onClick={() => { update(view.state); const layout = view.columnLayouts[view.state.tab]; if (layout) onApplyLayout(layout); void message.success(`已应用：${view.name}`); }}><b>{view.name}</b><small>{date(view.savedAt)}</small></button><Button type="text" danger size="small" onClick={() => { const next = views.filter((item) => item.id !== view.id); setViews(next); localStorage.setItem(SAVED_VIEWS_KEY, JSON.stringify(next)); }}>删除</Button></div>) : <Text type="secondary">暂无已保存视图</Text>}</div>;
  return <Popover title="Saved Views" content={content} trigger="click" placement="bottomRight"><Button icon={<SaveOutlined />}>视图</Button></Popover>;
}

function defaultVisibleColumns(tokenMode: boolean): string[] { return ["tx", "method", "time", "from", "direction", "to", "value", ...(tokenMode ? ["token"] : []), "usd", ...(tokenMode ? [] : ["fee"]), "status", "actions"]; }
function defaultColumnLayout(tokenMode: boolean): ColumnLayout { const order = ["tx", "method", "time", "from", "direction", "to", "value", "token", "usd", "fee", "status", "from_entity", "to_entity", "protocol", "contract", "gas_used", "gas_price", "actions"]; return { order, visible: defaultVisibleColumns(tokenMode), pins: { tx: "left", actions: "right" } }; }
function readColumnLayouts(): Record<string, ColumnLayout> { try { return JSON.parse(localStorage.getItem(COLUMN_LAYOUT_KEY) || "{}") as Record<string, ColumnLayout>; } catch { return {}; } }
function loadColumnLayout(kind: string, tokenMode: boolean): ColumnLayout { const value = readColumnLayouts()[kind]; return value?.order?.length ? value : defaultColumnLayout(tokenMode); }
function saveColumnLayout(kind: string, layout: ColumnLayout) { const all = readColumnLayouts(); all[kind] = layout; localStorage.setItem(COLUMN_LAYOUT_KEY, JSON.stringify(all)); }
function readSavedViews(): SavedExplorerView[] { try { const value = JSON.parse(localStorage.getItem(SAVED_VIEWS_KEY) || "[]") as SavedExplorerView[]; return Array.isArray(value) ? value.slice(0, 30) : []; } catch { return []; } }
function suggestedViewName(state: ReturnType<typeof useAnalysisContext>["state"]): string { return [state.tokenSymbol || (state.tokens.length ? "Token" : ""), state.direction !== "all" ? state.direction.toUpperCase() : "", state.minUSD ? `≥${usd(state.minUSD)}` : "", state.entityFilters[0] || ""].filter(Boolean).join(" ") || "案件目标地址"; }

function downloadActivityCSV(rows: ClickHouseActivity[], fileName: string) {
  const keys: Array<keyof ClickHouseActivity> = ["transaction_hash", "block_number", "block_time", "direction", "address", "counterparty_address", "amount", "token_name", "token_symbol", "token_address", "historical_price_usdt", "historical_value_usdt", "price_timestamp", "price_source", "price_route", "price_confidence", "price_type", "price_age_seconds", "valuation_status", "method_name", "status"];
  const escape = (value: unknown) => { const raw = String(value ?? ""); const safe = /^[=+\-@]/.test(raw) ? `'${raw}` : raw; return `"${safe.replace(/"/g, '""')}"`; };
  const content = [keys.join(","), ...rows.map((row) => keys.map((key) => escape(row[key])).join(","))].join("\r\n");
  const url = URL.createObjectURL(new Blob(["\ufeff", content], { type: "text/csv;charset=utf-8" }));
  const link = document.createElement("a"); link.href = url; link.download = fileName; link.click(); URL.revokeObjectURL(url);
}

function saveAddressTags(rows: ClickHouseActivity[], rootAddress: string) {
  const label = window.prompt("输入地址标签"); if (!label?.trim()) return;
  const addresses = Array.from(new Set(rows.flatMap((row) => [row.address || rootAddress, row.counterparty_address]).filter(Boolean)));
  const key = "explorer-local-address-tags-v1"; let tags: Record<string, string> = {};
  try { tags = JSON.parse(localStorage.getItem(key) || "{}") as Record<string, string>; } catch { tags = {}; }
  addresses.forEach((address) => { tags[address.toLowerCase()] = label.trim().slice(0, 80); }); localStorage.setItem(key, JSON.stringify(tags)); void message.success(`已标记 ${addresses.length} 个地址`);
}

function buildContextExportURL(state: ReturnType<typeof useAnalysisContext>["state"]): string {
  const params = new URLSearchParams({ window: state.window, dataset: "historical_activity", limit: "100000" });
  if (state.from) params.set("from", state.from);
  if (state.to) params.set("to", state.to);
  if (state.direction !== "all") params.set("direction", state.direction);
  if (state.tokens[0]) params.set("token", state.tokens[0]);
  if (state.minUSD) params.set("min_usd", state.minUSD);
  if (state.maxUSD) params.set("max_usd", state.maxUSD);
  if (state.entityFilters[0]) params.set("entity_type", state.entityFilters[0]);
  return `/api/v2/financial-export/${state.chain}/address/${state.rootAddress}.csv?${params.toString()}`;
}

function saveCurrentView(state: ReturnType<typeof useAnalysisContext>["state"]) {
  const name = window.prompt("视图名称", suggestedViewName(state)); if (!name?.trim()) return;
  const views = readSavedViews();
  const next: SavedExplorerView[] = [{ id: `${Date.now()}`, name: name.trim().slice(0, 40), state: { ...state, selectedRows: [] }, columnLayouts: readColumnLayouts(), savedAt: new Date().toISOString() }, ...views].slice(0, 30);
  localStorage.setItem(SAVED_VIEWS_KEY, JSON.stringify(next)); void message.success("当前视图已保存");
}

function AnalysisView({ mode, financial, daily, counterparties, errors, onOpenAddress }: { mode: string; financial: FinancialSummary | null; daily: Array<Record<string, unknown>>; counterparties: CounterpartyFinancial[]; errors: Record<string, string>; onOpenAddress: (address: string) => void }) {
  if (mode === "counterparties") return <CounterpartyTable rows={counterparties} onOpenAddress={onOpenAddress} error={errors.counterparties} />;
  if (mode === "related") return <Unavailable title="暂无已验证的关联钱包" detail="交易关系不等同于同一主体；仅展示 Registry 或案件证据支持的关联结果。" />;
  if (mode === "retention" || mode === "pass-through" || mode === "pnl") return <StrictAnalysisView mode={mode} />;
  return <div className="xi-analysis-grid"><Section title="金融摘要" error={errors.financial}><Descriptions column={2} items={[{ key: "1", label: "总流入", children: usd(financial?.flow.total_in_usd) }, { key: "2", label: "总流出", children: usd(financial?.flow.total_out_usd) }, { key: "3", label: "净流量", children: usd(financial?.flow.netflow_usd) }, { key: "4", label: "价格覆盖", children: coveragePercent(financial) }]} /></Section><Section title="时间范围"><Descriptions column={1} items={[{ key: "1", label: "首个统计日", children: date(daily[0]?.date) }, { key: "2", label: "最新统计日", children: date(daily[daily.length - 1]?.date) }]} /></Section></div>;
}

function StrictAnalysisView({ mode }: { mode: "retention" | "pass-through" | "pnl" }) {
  const { state } = useAnalysisContext();
  const [data, setData] = useState<Record<string, unknown> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => { setLoading(true); setError(""); loadStrictFinancialAnalysis(state.chain, state.rootAddress, mode).then(setData).catch((reason: Error) => setError(reason.message)).finally(() => setLoading(false)); }, [mode, state.chain, state.rootAddress]);
  if (loading) return <Section title={analysisTitle(mode)}><Skeleton active /></Section>;
  if (error) return <Unavailable title={`${analysisTitle(mode)} 暂不可用`} detail={error} />;
  if (mode === "pnl") {
    const rows = data ? [{ label: "已实现 PnL", value: usd(data.realized_pnl_usd) }, { label: "持仓市值", value: usd(data.position_market_value_usd) }, { label: "已知成本比例", value: ratio(data.known_cost_basis_ratio) }, { label: "置信度", value: String(data.financial_confidence || "--") }, { label: "当前价格状态", value: String(data.current_price_status || "--") }] : [];
    return <Section title="PnL"><div className="xi-strict-metrics">{rows.map((row) => <span key={row.label}><small>{row.label}</small><strong>{row.value}</strong></span>)}</div><Text type="secondary">仅 Canonical 买卖事件计入损益，普通转账不视为卖出。</Text></Section>;
  }
  const result = Array.isArray(data?.results) ? data.results[0] as Record<string, unknown> | undefined : undefined;
  const key = mode === "retention" ? "retention_windows" : "pass_through_windows";
  const windows = Array.isArray(result?.[key]) ? result[key] as Array<Record<string, unknown>> : [];
  if (!windows.length) return <Unavailable title={`${analysisTitle(mode)} 暂无可计算数据`} detail={mode === "retention" ? "严格 FIFO 留存需要可归属的流入与后续流出，不以净流量代替。" : "快速流转是行为指标，数据不足时保持未知。"} />;
  const columns: ColumnsType<Record<string, unknown>> = [{ title: "窗口", dataIndex: "window" }, { title: mode === "retention" ? "留存金额" : "匹配流出", align: "right", render: (_, row) => number(mode === "retention" ? row.retained_amount : row.matched_transfer_amount) }, { title: mode === "retention" ? "留存率" : "流转率", align: "right", render: (_, row) => ratio(mode === "retention" ? row.retention_ratio : row.pass_through_ratio) }, { title: "USD 覆盖", dataIndex: "usd_amount_coverage", align: "right", render: (value) => ratio(value) }];
  return <Section title={analysisTitle(mode)}><Table rowKey={(row) => String(row.window)} columns={columns} dataSource={windows} pagination={false} size="small" /><Text type="secondary">该指标仅用于行为筛查，不构成身份、归集或犯罪定性。</Text></Section>;
}

function analysisTitle(mode: "retention" | "pass-through" | "pnl"): string { return mode === "retention" ? "资金留存" : mode === "pass-through" ? "快速流转" : "PnL"; }
function ratio(value: unknown): string { if (value === null || value === undefined || value === "") return "--"; const parsed = Number(value); return Number.isFinite(parsed) ? `${(parsed <= 1 ? parsed * 100 : parsed).toFixed(2)}%` : "--"; }

function coveragePercent(financial: FinancialSummary | null): string {
  if (!financial || financial.price_coverage.activity_count === 0) return "--";
  const value = Number(financial.price_coverage.coverage_ratio);
  if (!Number.isFinite(value)) return "--";
  return `${value <= 1 ? value * 100 : value}%`;
}

function AssetsView({ header, liveAssets, state, error, queriedAt }: { header: ExplorerHeader | null; liveAssets: AssetBalance[]; state: "idle" | "loading" | "ready" | "failed"; error: string | null; queriedAt: string | null }) {
  const rows = liveAssets;
  if (state === "loading" && !rows.length) return <Section title="资产"><Skeleton active paragraph={{ rows: 3 }} /></Section>;
  if (state === "failed") return <Unavailable title="资产余额查询失败" detail={error || "RPC 不可用或当前链尚未配置资产查询。"} tone="error" />;
  if (!rows.length) return <Unavailable title="资产余额暂不可用" detail="实时余额查询尚未返回，未知余额不会显示为 0。" tone="info" />;
  const columns: ColumnsType<AssetBalance> = [
    { title: "资产", render: (_, row) => <TokenIdentity chainId={header?.identity.chain_id ?? 56} symbol={row.symbol} address={row.token_address} /> },
    { title: "余额", dataIndex: "balance", align: "right", render: (value, row) => row.status === "success" ? number(value) : "—" },
    { title: "查询状态", dataIndex: "status", width: 130, render: (value, row) => value === "success" ? <Tag color="success">已查询</Tag> : <Tooltip title={row.error || "RPC 不可用或当前链未配置节点"}><Tag color="error">查询失败</Tag></Tooltip> },
  ];
  const successful = rows.filter((row) => row.status === "success");
  const failed = rows.filter((row) => row.status !== "success");
  const failureDetails = [...new Set(failed.map((row) => row.error).filter(Boolean))].join("；");
  const allZero = failed.length === 0 && successful.length > 0 && successful.every((row) => Number(row.balance || 0) === 0);
  return <Section title="资产">
    {failed.length ? <InlineNotice tone="error">{failed.length === rows.length ? "资产查询失败" : "部分资产查询失败"}：{failureDetails || "当前链 RPC 未配置或不可用；未知余额不会显示为 0。"}</InlineNotice> : null}
    {allZero ? <InlineNotice tone="info">当前已查询资产的链上余额均为 0，余额以本次 RPC 查询时点为准。</InlineNotice> : null}
    {queriedAt ? <Text type="secondary">查询时间：{date(queriedAt)}</Text> : null}
    <Table dataSource={rows} rowKey={(row) => String(row.token_address || row.symbol)} pagination={false} columns={columns} />
  </Section>;
}

function CounterpartyTable({ rows, onOpenAddress, error }: { rows: CounterpartyFinancial[]; onOpenAddress: (address: string) => void; error?: string }) {
  const columns: ColumnsType<CounterpartyFinancial> = [{ title: "交易对手", dataIndex: "counterparty", render: (value, row) => <AddressIdentity value={value} label={row.entity_name} onOpen={() => onOpenAddress(value)} /> }, { title: "角色", dataIndex: "entity_role", render: (value) => value || "--" }, { title: "流入", dataIndex: "in_usd", align: "right", render: (value) => usd(value) }, { title: "流出", dataIndex: "out_usd", align: "right", render: (value) => usd(value) }, { title: "净流量", dataIndex: "netflow_usd", align: "right", render: (value) => usd(value) }, { title: "活动", dataIndex: "activity_count", align: "right", render: (value) => number(value) }, { title: "最近交互", dataIndex: "last_interaction", render: (value) => date(value) }];
  return <Section title="交易对手" error={error}><Table rowKey="counterparty" className="xi-activity-table" columns={columns} dataSource={rows} pagination={{ pageSize: 20, showSizeChanger: false }} size="small" /></Section>;
}

function Section({ title, error, action, children }: { title: string; error?: string; action?: ReactNode; children: ReactNode }) { return <section className="xi-section"><header><h3>{title}</h3>{action}</header>{error ? <div className="xi-inline-error">{error}</div> : children}</section>; }

function RankedCounterparties({ rows, valueKey, onOpen }: { rows: CounterpartyFinancial[]; valueKey: "in_usd" | "out_usd"; onOpen: (address: string) => void }) {
  if (!rows.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据" />;
  return <div className="xi-ranking">{rows.map((row, index) => <button type="button" key={row.counterparty} onClick={() => onOpen(row.counterparty)}><em>{index + 1}</em><span><b>{row.entity_name || short(row.counterparty)}</b><small>{short(row.counterparty)}</small></span><strong>{usd(row[valueKey])}</strong></button>)}</div>;
}

function CompactObjectList({ rows, onOpen }: { rows: Array<Record<string, unknown>>; onOpen: (row: Record<string, unknown>) => void }) {
  if (!rows.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据" />;
  return <div className="xi-compact-list">{rows.slice(0, 10).map((row, index) => <button type="button" key={`${String(row.tx_hash)}-${index}`} onClick={() => onOpen(row)}><span><b>{short(row.tx_hash)}</b><small>{date(row.block_time)}</small></span><span><b>{String(row.method_name || row.token_symbol || row.direction || "Transfer")}</b><small>Block {number(row.block_number)}</small></span><strong>{row.historical_value_usdt ? `${number(row.historical_value_usdt)} USDT` : "—"}</strong></button>)}</div>;
}

function FilterDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { state, update, clearFilters } = useAnalysisContext();
  const [token, setToken] = useState("");
  return <Drawer title="高级筛选" width={460} open={open} onClose={onClose} extra={<Button type="link" onClick={clearFilters}>Clear All</Button>}><div className="xi-filter-form">
    <fieldset><legend>时间</legend><Segmented block value={state.window} onChange={(window) => update({ window: window as AnalysisWindow })} options={WINDOWS} />{state.window === "CUSTOM" ? <DatePicker.RangePicker showTime value={state.from && state.to ? [dayjs(state.from), dayjs(state.to)] : undefined} onChange={(range) => update({ from: range?.[0]?.toISOString(), to: range?.[1]?.toISOString() })} /> : null}</fieldset>
    <fieldset><legend>地址</legend><label>From<Input allowClear value={state.fromAddress} onChange={(event) => update({ fromAddress: event.target.value.trim().toLowerCase() || undefined })} placeholder="0x…" /></label><label>To<Input allowClear value={state.toAddress} onChange={(event) => update({ toAddress: event.target.value.trim().toLowerCase() || undefined })} placeholder="0x…" /></label><label>Counterparty<Input allowClear value={state.counterparty} onChange={(event) => update({ counterparty: event.target.value.trim().toLowerCase() || undefined })} placeholder="0x…" /></label></fieldset>
    <fieldset><legend>Token</legend><label>Token Symbol<Input allowClear value={state.tokenSymbol} onChange={(event) => update({ tokenSymbol: event.target.value.trim().toUpperCase() || undefined })} placeholder="USDT" /></label><label>Token Contract<Input value={token} onChange={(event) => setToken(event.target.value)} onPressEnter={() => { const value = token.trim().toLowerCase(); if (/^0x[0-9a-f]{40}$/.test(value) && !state.tokens.includes(value)) update({ tokens: [...state.tokens, value] }); else if (value) void message.warning("Token 合约地址格式不正确"); setToken(""); }} placeholder="输入合约地址后回车" /></label><div>{state.tokens.map((item) => <Tag closable key={item} onClose={() => update({ tokens: state.tokens.filter((value) => value !== item) })}>{short(item)}</Tag>)}</div></fieldset>
    <fieldset><legend>金额</legend><div className="xi-filter-pair"><label>USD Min<InputNumber stringMode min="0" value={state.minUSD} onChange={(value) => update({ minUSD: value ?? undefined })} /></label><label>USD Max<InputNumber stringMode min="0" value={state.maxUSD} onChange={(value) => update({ maxUSD: value ?? undefined })} /></label></div><label>估值状态<Select mode="multiple" value={state.valuationStatuses} onChange={(valuationStatuses) => update({ valuationStatuses })} options={["VALUED", "LOW_CONFIDENCE", "NO_PRICE"].map((value) => ({ value, label: value }))} /></label></fieldset>
    <fieldset><legend>交易</legend><label>交易哈希<Input allowClear value={state.transactionHash} onChange={(event) => update({ transactionHash: event.target.value.trim().toLowerCase() || undefined })} placeholder="0x…" /></label><label>方向<Select value={state.direction} onChange={(direction: AnalysisDirection) => update({ direction })} options={[{ value: "all", label: "全部" }, { value: "in", label: "转入" }, { value: "out", label: "转出" }, { value: "self", label: "自转" }]} /></label><label>Method<Select mode="tags" value={state.methodFilters} onChange={(methodFilters) => update({ methodFilters: methodFilters.slice(0, 10) })} tokenSeparators={[","]} placeholder="方法名或 Method ID" /></label><label>Status<Select mode="multiple" value={state.statusFilters} onChange={(statusFilters) => update({ statusFilters })} options={["SUCCESS", "FAILED", "UNKNOWN"].map((value) => ({ value, label: value }))} /></label></fieldset>
    <fieldset><legend>实体</legend><label>Entity Type<Select mode="multiple" value={state.entityFilters} onChange={(entityFilters) => update({ entityFilters })} options={["CEX", "DEX", "BRIDGE", "PROTOCOL", "CONTRACT", "EOA"].map((value) => ({ value, label: value }))} /></label><label>Entity<Input allowClear value={state.entity} onChange={(event) => update({ entity: event.target.value || undefined })} /></label><label>Role<Input allowClear value={state.entityRole} onChange={(event) => update({ entityRole: event.target.value || undefined })} /></label></fieldset>
    <fieldset><legend>协议</legend><Select mode="multiple" value={state.protocolFilters} onChange={(protocolFilters) => update({ protocolFilters })} options={[{ value: "DEX", label: "DEX" }, { value: "BRIDGE", label: "Bridge" }]} /></fieldset>
  </div></Drawer>;
}

function DetailDrawer({ detail, onClose, onOpenAddress, onNavigate }: { detail: DetailState | null; onClose: () => void; onOpenAddress: (address: string) => void; onNavigate: (page: string, address?: string) => void }) {
  const { state, update } = useAnalysisContext();
  const row = detail?.activity;
  const data = detail?.data || {};
  const from = row ? row.direction === "IN" ? row.counterparty_address : row.address || state.rootAddress : String(data.from_address || data.from || "");
  const to = row ? row.direction === "IN" ? row.address || state.rootAddress : row.counterparty_address : String(data.to_address || data.to || "");
  const structured = row ? [
    ...(detail?.tokenMode ? [
      { key: "token", label: "Token", children: <TokenIdentity chainId={row.chain_id} symbol={row.token_symbol} name={row.token_name} address={row.token_address} logoURI={row.token_logo_uri} verified={row.token_verified} spam={row.token_spam} /> },
      { key: "amount", label: "数量", children: number(row.amount) },
      { key: "price", label: "当时单价", children: row.historical_price_usdt ? `${number(row.historical_price_usdt)} USDT / ${row.token_symbol || "Token"}` : "暂无历史价格" },
      { key: "usd", label: "当时 USDT 价值", children: row.historical_value_usdt ? `${number(row.historical_value_usdt)} USDT` : "—" },
      { key: "price_route", label: "价格路径", children: row.price_route || "—" },
      { key: "price_type", label: "价格类型", children: row.price_type || "UNKNOWN" },
      { key: "valuation_status", label: "估值状态", children: row.valuation_status || "NO_PRICE" },
      { key: "price_time", label: "价格时间", children: row.price_time ? date(row.price_time) : "--" },
      { key: "price_source", label: "价格来源", children: row.price_source || "UNKNOWN" },
      { key: "price_confidence", label: "价格可信度", children: row.price_confidence || "UNKNOWN" },
    ] : []),
    { key: "status", label: "Status", children: row.status || "UNKNOWN" },
    { key: "block", label: "Block", children: number(row.block_number) },
    { key: "time", label: "Time", children: date(row.block_time) },
    { key: "from", label: "From", children: <AddressIdentity value={from} onOpen={() => { onClose(); onOpenAddress(from); }} /> },
    { key: "to", label: "To", children: <AddressIdentity value={to} onOpen={() => { onClose(); onOpenAddress(to); }} /> },
    { key: "method", label: "Method", children: row.method_name || row.method_id || "--" },
    ...(!detail?.tokenMode ? [{ key: "value", label: "Native Value", children: number(row.amount) }, { key: "fee", label: "Fee", children: String(data.transaction_fee_native ?? data.fee ?? "--") }] : []),
    { key: "entity", label: "Entity / Role", children: [row.counterparty_label, row.counterparty_entity_type].filter(Boolean).join(" · ") || "--" },
    { key: "internal", label: "Internal Tx Count", children: Array.isArray(data.internal_transactions) ? data.internal_transactions.length : String(data.internal_transaction_count ?? "--") },
    { key: "events", label: "Event Count", children: Array.isArray(data.events) ? data.events.length : String(data.event_count ?? "--") },
  ] : Object.entries(data).filter(([, value]) => typeof value !== "object").slice(0, 36).map(([key, value]) => ({ key, label: key, children: /address$/.test(key) && /^0x[0-9a-fA-F]{40}$/.test(String(value)) ? <AddressIdentity value={value} onOpen={() => { onClose(); onOpenAddress(String(value)); }} /> : key.includes("time") ? date(value) : String(value ?? "--") }));
  const action = (page: string) => { if (row) update({ selectedRows: [activityKey(row)] }); onClose(); onNavigate(page, state.rootAddress); };
  const exportRow = () => { if (row) downloadActivityCSV([row], `${row.transaction_hash}.csv`); };
  const fullURL = detail?.kind === "TRANSACTION" && isTransactionHash(detail.value) ? buildTransactionURL(state.chain, detail.value) : undefined;
  return <Drawer className="xi-detail-drawer" title={detail?.tokenMode ? "Token Transfer" : `${detail?.kind || ""} 详情`} width={640} open={!!detail} onClose={onClose} extra={row ? <Space><Button href={fullURL} target="_blank">完整详情</Button><Button type="primary" icon={<NodeIndexOutlined />} onClick={() => action("analytics-graph")}>资金追踪</Button><Button icon={<SafetyCertificateOutlined />} onClick={() => action("intelligence")}>调查工作台</Button><Button icon={<DownloadOutlined />} onClick={exportRow}>导出</Button></Space> : null}>{detail?.loading && !row ? <Skeleton active /> : <><div className="xi-detail-title"><Tag>{detail?.tokenMode ? "TOKEN TRANSFER" : detail?.kind}</Tag><Text code copyable>{detail?.value}</Text></div>{detail?.error ? <div className="xi-inline-error">完整 Canonical 详情暂不可用，以下为活动索引记录：{detail.error}</div> : null}<Descriptions bordered size="small" column={1} items={structured} /><Text type="secondary">历史 USD 使用事件发生时价格；UNKNOWN 与缺失值不会被推断补齐。</Text></>}</Drawer>;
}

function Unavailable({ title, detail, tone }: { title: string; detail: string; tone?: "info" | "error" }) { return <div className={`xi-unavailable xi-unavailable-${tone || "info"}`}><SwapOutlined /><Title level={5}>{title}</Title><Text type="secondary">{detail}</Text></div>; }

function InlineNotice({ tone, children }: { tone: "info" | "error"; children: ReactNode }) {
  return <div className={`xi-inline-notice xi-inline-notice-${tone}`}><Text>{children}</Text></div>;
}
