import { CodeOutlined } from "@ant-design/icons";
import { Empty, Skeleton } from "antd";
import type { ClickHouseTransactionLog } from "../analytics/ClickHouseExplorerTypes";
import type { AppChain } from "../explorer-intelligence/appRoute";
import { AddressLink } from "./AddressLink";
import { logIndex, shortAddress } from "./transactionFormat";

interface Props { chain: AppChain; items: ClickHouseTransactionLog[]; loading: boolean; error?: string }

function decodedEntries(value: ClickHouseTransactionLog["decoded"]): Array<[string, unknown]> {
  return value && typeof value === "object" ? Object.entries(value) : [];
}

function rawValue(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  return typeof value === "string" ? value : JSON.stringify(value);
}

export function EventLogList({ chain, items, loading, error }: Props) {
  if (loading) return <div className="txd-tab-skeleton">{Array.from({ length: 4 }, (_, index) => <Skeleton key={index} active title={false} paragraph={{ rows: 2 }} />)}</div>;
  if (error) return <div className="txd-dataset-error"><b>事件日志暂不可用</b><span>{error}</span></div>;
  if (!items.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该交易没有已索引事件日志" />;
  const sorted = [...items].sort((a, b) => logIndex(a.log_index) - logIndex(b.log_index));
  return <div className="txd-log-list">{sorted.map((item, index) => {
    const decoded = decodedEntries(item.decoded);
    return <article className="txd-log-item" key={`${item.log_index}:${index}`}>
      <div className="txd-log-heading"><span className="txd-log-index">#{item.log_index}</span><div><strong>{item.event_name || "Unknown Event"}</strong><small>{decoded.length ? "Decoded" : "Raw only"}{item.removed ? " · Removed" : ""}</small></div><CodeOutlined /></div>
      <div className="txd-log-contract"><span>Contract</span><AddressLink chain={chain} address={item.contract.address} label={item.contract.entity_name || item.contract.label} /></div>
      {decoded.length ? <dl className="txd-decoded">{decoded.map(([key, value]) => <div key={key}><dt>{key}</dt><dd>{/^0x[0-9a-fA-F]{40}$/.test(String(value)) ? <AddressLink chain={chain} address={String(value)} /> : <span title={rawValue(value)}>{shortAddress(rawValue(value), 16, 12)}</span>}</dd></div>)}</dl> : <p className="txd-unknown-event">ABI 未匹配，保留 Topic 和 Data 作为原始证据。</p>}
      <details className="txd-raw-log"><summary>查看 Raw</summary><dl>{["topic0", "topic1", "topic2", "topic3", "data"].map((key) => <div key={key}><dt>{key}</dt><dd><code>{rawValue(item[key as keyof ClickHouseTransactionLog])}</code></dd></div>)}</dl>{item.provenance ? <div className="txd-provenance"><span>provenance</span><code>{rawValue(item.provenance)}</code></div> : null}</details>
    </article>;
  })}</div>;
}
