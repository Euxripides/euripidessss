import { ReloadOutlined } from "@ant-design/icons";
import { Button, Result, Skeleton, Tabs } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  fetchClickHouseTransactionDetail,
  fetchClickHouseTransactionInternals,
  fetchClickHouseTransactionLogs,
  fetchClickHouseTransactionTokenTransfers,
} from "../analytics/ClickHouseExplorerApi";
import type {
  ClickHouseTransactionDetail,
  ClickHouseTransactionInternal,
  ClickHouseTransactionLog,
  ClickHouseTransactionTokenTransfer,
} from "../analytics/ClickHouseExplorerTypes";
import { buildAppURL, isTransactionHash, type AppChain } from "../explorer-intelligence/appRoute";
import { EventLogList } from "./EventLogList";
import { DatasetPagination } from "./DatasetPagination";
import { InternalTransactionList } from "./InternalTransactionList";
import { TokenTransferList } from "./TokenTransferList";
import { TransactionHeader } from "./TransactionHeader";
import { TransactionOverview, type DatasetState } from "./TransactionOverview";
import { externalTransactionURL, routeChainToExplorer } from "./transactionRoutes";
import { appendUniqueBy, resourceTotal } from "./transactionLogic";
import "./transaction-detail.css";

interface Props {
  chain: AppChain;
  hash: string;
  onNavigate: (
    page: "explorer" | "analytics-graph" | "intelligence",
    context?: { txHash: string; fromAddress?: string; toAddress?: string },
  ) => void;
}

interface Resource<T> { state: DatasetState; items: T[]; count?: number; error?: string; nextCursor?: string; loadingMore?: boolean; loadMoreError?: string }
const emptyResource = <T,>(): Resource<T> => ({ state: "loading", items: [] });

function errorMessage(error: unknown, fallback: string): string {
  const detail = error instanceof Error ? error.message : fallback;
  if (/not found|TX_NOT_FOUND|404/i.test(detail)) return "未找到该交易，或当前链尚未索引该哈希。";
  return detail || fallback;
}

function tabLabel(label: string, resource?: Resource<unknown>): React.ReactNode {
  if (!resource) return label;
  return <span className="txd-tab-label">{label}<b>{resource.state === "ready" ? resource.count ?? resource.items.length : resource.state === "loading" ? "…" : "!"}</b></span>;
}

export default function TransactionDetailPage({ chain, hash, onNavigate }: Props) {
  const normalizedHash = hash.trim().toLowerCase();
  const valid = isTransactionHash(normalizedHash);
  const [retryKey, setRetryKey] = useState(0);
  const [overview, setOverview] = useState<{ loading: boolean; data?: ClickHouseTransactionDetail; error?: string }>({ loading: valid });
  const [transfers, setTransfers] = useState<Resource<ClickHouseTransactionTokenTransfer>>(emptyResource);
  const [internals, setInternals] = useState<Resource<ClickHouseTransactionInternal>>(emptyResource);
  const [logs, setLogs] = useState<Resource<ClickHouseTransactionLog>>(emptyResource);
  const scopeKey = `${chain}:${normalizedHash}:${retryKey}`;
  const scopeRef = useRef(scopeKey);
  scopeRef.current = scopeKey;

  useEffect(() => {
    if (!valid) {
      setOverview({ loading: false, error: "交易哈希格式错误" });
      setTransfers({ state: "error", items: [], error: "交易哈希格式错误" });
      setInternals({ state: "error", items: [], error: "交易哈希格式错误" });
      setLogs({ state: "error", items: [], error: "交易哈希格式错误" });
      return;
    }
    let active = true;
    const apiChain = routeChainToExplorer(chain);
    setOverview({ loading: true });
    setTransfers(emptyResource());
    setInternals(emptyResource());
    setLogs(emptyResource());

    void fetchClickHouseTransactionDetail(normalizedHash, apiChain)
      .then((data) => { if (active) setOverview({ loading: false, data }); })
      .catch((error) => { if (active) setOverview({ loading: false, error: errorMessage(error, "交易概览加载失败") }); });
    void fetchClickHouseTransactionTokenTransfers(normalizedHash, apiChain)
      .then((data) => { if (active) setTransfers({ state: "ready", items: data.items ?? [], count: resourceTotal(data.total_count, data.count, data.items?.length ?? 0), nextCursor: data.next_cursor }); })
      .catch((error) => { if (active) setTransfers({ state: "error", items: [], error: errorMessage(error, "代币转账加载失败") }); });
    void fetchClickHouseTransactionInternals(normalizedHash, apiChain)
      .then((data) => { if (active) setInternals({ state: "ready", items: data.items ?? [], count: resourceTotal(data.total_count, data.count, data.items?.length ?? 0), nextCursor: data.next_cursor }); })
      .catch((error) => { if (active) setInternals({ state: "error", items: [], error: errorMessage(error, "内部交易加载失败") }); });
    void fetchClickHouseTransactionLogs(normalizedHash, apiChain)
      .then((data) => { if (active) setLogs({ state: "ready", items: data.items ?? [], count: resourceTotal(data.total_count, data.count, data.items?.length ?? 0), nextCursor: data.next_cursor }); })
      .catch((error) => { if (active) setLogs({ state: "error", items: [], error: errorMessage(error, "事件日志加载失败") }); });
    return () => { active = false; };
  }, [chain, normalizedHash, retryKey, valid]);

  const counts = useMemo(() => ({ transfers: transfers.count, internals: internals.count, logs: logs.count }), [internals.count, logs.count, transfers.count]);
  const states = useMemo(() => ({ transfers: transfers.state, internals: internals.state, logs: logs.state }), [internals.state, logs.state, transfers.state]);
  const loaded = useMemo(() => ({ transfers: transfers.items.length, internals: internals.items.length, logs: logs.items.length }), [internals.items.length, logs.items.length, transfers.items.length]);
  const loadMoreTransfers = async () => {
    const cursor = transfers.nextCursor;
    if (!cursor || transfers.loadingMore) return;
    const requestScope = scopeKey;
    setTransfers((current) => ({ ...current, loadingMore: true, loadMoreError: undefined }));
    try {
      const data = await fetchClickHouseTransactionTokenTransfers(normalizedHash, routeChainToExplorer(chain), { limit: 500, cursor });
      if (scopeRef.current !== requestScope) return;
      setTransfers((current) => {
        const items = appendUniqueBy(current.items, data.items ?? [], (item) => `${item.log_index}:${item.batch_index}`);
        return { ...current, state: "ready", items, count: resourceTotal(data.total_count, data.count, items.length), nextCursor: data.next_cursor, loadingMore: false, loadMoreError: undefined };
      });
    } catch (error) {
      if (scopeRef.current === requestScope) setTransfers((current) => ({ ...current, loadingMore: false, loadMoreError: errorMessage(error, "代币转账下一页加载失败") }));
    }
  };
  const loadMoreInternals = async () => {
    const cursor = internals.nextCursor;
    if (!cursor || internals.loadingMore) return;
    const requestScope = scopeKey;
    setInternals((current) => ({ ...current, loadingMore: true, loadMoreError: undefined }));
    try {
      const data = await fetchClickHouseTransactionInternals(normalizedHash, routeChainToExplorer(chain), { limit: 500, cursor });
      if (scopeRef.current !== requestScope) return;
      setInternals((current) => {
        const items = appendUniqueBy(current.items, data.items ?? [], (item) => `${item.trace_index}:${item.trace_address}`);
        return { ...current, state: "ready", items, count: resourceTotal(data.total_count, data.count, items.length), nextCursor: data.next_cursor, loadingMore: false, loadMoreError: undefined };
      });
    } catch (error) {
      if (scopeRef.current === requestScope) setInternals((current) => ({ ...current, loadingMore: false, loadMoreError: errorMessage(error, "内部交易下一页加载失败") }));
    }
  };
  const loadMoreLogs = async () => {
    const cursor = logs.nextCursor;
    if (!cursor || logs.loadingMore) return;
    const requestScope = scopeKey;
    setLogs((current) => ({ ...current, loadingMore: true, loadMoreError: undefined }));
    try {
      const data = await fetchClickHouseTransactionLogs(normalizedHash, routeChainToExplorer(chain), { limit: 500, cursor });
      if (scopeRef.current !== requestScope) return;
      setLogs((current) => {
        const items = appendUniqueBy(current.items, data.items ?? [], (item) => String(item.log_index));
        return { ...current, state: "ready", items, count: resourceTotal(data.total_count, data.count, items.length), nextCursor: data.next_cursor, loadingMore: false, loadMoreError: undefined };
      });
    } catch (error) {
      if (scopeRef.current === requestScope) setLogs((current) => ({ ...current, loadingMore: false, loadMoreError: errorMessage(error, "事件日志下一页加载失败") }));
    }
  };
  const navigate = (page: "explorer" | "analytics-graph" | "intelligence") => {
    if (page === "explorer") {
      const url = buildAppURL({ active: "explorer", chain });
      window.history.pushState(null, "", url);
      window.dispatchEvent(new PopStateEvent("popstate"));
      return;
    }
    onNavigate(page, {
      txHash: normalizedHash,
      fromAddress: overview.data?.from_address,
      toAddress: overview.data?.to_address,
    });
  };

  if (!valid) return <main className="txd-page"><Result status="warning" title="交易哈希格式错误" subTitle="请检查 URL：交易哈希必须是 0x + 64 位十六进制字符。" extra={<Button onClick={() => navigate("explorer")}>返回 Explorer</Button>} /></main>;

  return <main className="txd-page">
    <TransactionHeader chain={chain} hash={normalizedHash} transaction={overview.data} loading={overview.loading} onNavigate={navigate} />
    <TransactionOverview chain={chain} transaction={overview.data} loading={overview.loading} counts={counts} loaded={loaded} states={states} />
    {overview.error ? <div className="txd-overview-error"><div><b>{overview.error}</b><span>基础交易信息暂不可用，其他数据集仍在独立加载。</span></div><div><Button icon={<ReloadOutlined />} onClick={() => setRetryKey((value) => value + 1)}>重新查询</Button><Button href={externalTransactionURL(chain, normalizedHash)} target="_blank" rel="noopener noreferrer">外部浏览器查看</Button></div></div> : null}
    {overview.loading && !overview.data ? <div className="txd-tabs-placeholder"><Skeleton active title={{ width: 260 }} paragraph={{ rows: 3 }} /></div> : <Tabs className="txd-tabs" defaultActiveKey="overview" animated={false} items={[
      { key: "overview", label: "概览", children: overview.data ? <div className="txd-overview-tab"><section><span>合约调用</span><b>{overview.data.method_name || overview.data.method_id || "—"}</b><p>{overview.data.input && overview.data.input !== "0x" ? `Input ${overview.data.input.slice(0, 18)}…` : "无合约输入数据"}</p></section><section><span>数据边界</span><b>{[transfers, internals, logs].every((item) => item.state === "ready" && item.items.length >= (item.count ?? 0)) ? "数据完整" : [transfers, internals, logs].some((item) => item.state === "loading") ? "正在补充数据" : "尚有分页数据可加载"}</b><p>价格缺失时保留为 —，不使用当前价格伪装历史估值。</p></section></div> : null },
      { key: "transfers", label: tabLabel("代币转账", transfers), children: <><TokenTransferList chain={chain} items={transfers.items} loading={transfers.state === "loading"} error={transfers.error} /><DatasetPagination label="代币转账" loadedCount={transfers.items.length} totalCount={transfers.count ?? transfers.items.length} nextCursor={transfers.nextCursor} loading={transfers.loadingMore} error={transfers.loadMoreError} onLoadMore={() => void loadMoreTransfers()} /></> },
      { key: "internals", label: tabLabel("内部交易", internals), children: <><InternalTransactionList chain={chain} items={internals.items} loading={internals.state === "loading"} error={internals.error} /><DatasetPagination label="内部交易" loadedCount={internals.items.length} totalCount={internals.count ?? internals.items.length} nextCursor={internals.nextCursor} loading={internals.loadingMore} error={internals.loadMoreError} onLoadMore={() => void loadMoreInternals()} /></> },
      { key: "logs", label: tabLabel("事件日志", logs), children: <><EventLogList chain={chain} items={logs.items} loading={logs.state === "loading"} error={logs.error} /><DatasetPagination label="事件日志" loadedCount={logs.items.length} totalCount={logs.count ?? logs.items.length} nextCursor={logs.nextCursor} loading={logs.loadingMore} error={logs.loadMoreError} onLoadMore={() => void loadMoreLogs()} /></> },
    ]} />}
  </main>;
}
