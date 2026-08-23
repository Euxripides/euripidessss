import { ArrowRightOutlined, CheckCircleFilled, CloseCircleFilled } from "@ant-design/icons";
import { Empty, Skeleton } from "antd";
import type { ClickHouseTransactionInternal } from "../analytics/ClickHouseExplorerTypes";
import type { AppChain } from "../explorer-intelligence/appRoute";
import { AddressLink } from "./AddressLink";
import { chainNativeSymbol } from "./transactionRoutes";
import { formatNumber } from "./transactionFormat";

interface Props { chain: AppChain; items: ClickHouseTransactionInternal[]; loading: boolean; error?: string }

function traceAddress(value: ClickHouseTransactionInternal["trace_address"]): string { return String(value ?? "0"); }

export function InternalTransactionList({ chain, items, loading, error }: Props) {
  if (loading) return <div className="txd-tab-skeleton">{Array.from({ length: 4 }, (_, index) => <Skeleton key={index} active title={false} paragraph={{ rows: 1 }} />)}</div>;
  if (error) return <div className="txd-dataset-error"><b>内部交易暂不可用</b><span>{error}</span></div>;
  if (!items.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该交易没有内部调用" />;
  return <div className="txd-internal-list">
    <div className="txd-internal-head"><span>调用方</span><span /><span>接收方</span><span>类型</span><span>Value</span><span>状态</span></div>
    {items.map((item, index) => {
      const failed = !item.success;
      return <details className="txd-internal-row" key={`${traceAddress(item.trace_address)}:${index}`}>
        <summary>
          <div><span className="txd-mobile-label">调用方</span><AddressLink chain={chain} address={item.from.address} label={item.from.entity_name || item.from.label} /></div>
          <span className="txd-transfer-arrow"><ArrowRightOutlined /></span>
          <div><span className="txd-mobile-label">接收方</span><AddressLink chain={chain} address={item.to.address} label={item.to.entity_name || item.to.label} /></div>
          <strong className="txd-call-type">{item.call_type || "CALL"}</strong>
          <span className="txd-internal-value">{formatNumber(item.amount.formatted || item.amount.decimal, 12)} {chainNativeSymbol(chain)}</span>
          <span className={`txd-internal-status ${failed ? "is-failed" : ""}`}>{failed ? <CloseCircleFilled /> : <CheckCircleFilled />}{failed ? "失败" : "成功"}</span>
        </summary>
        <div className="txd-technical-grid"><span>trace_address<b>{traceAddress(item.trace_address)}</b></span><span>gas<b>{String(item.gas ?? "—")}</b></span><span>gas_used<b>{String(item.gas_used ?? "—")}</b></span><span>error<b>{item.error || "—"}</b></span><span className="txd-technical-wide">input<code>{item.input || "—"}</code></span><span className="txd-technical-wide">output<code>{item.output || "—"}</code></span></div>
      </details>;
    })}
  </div>;
}
