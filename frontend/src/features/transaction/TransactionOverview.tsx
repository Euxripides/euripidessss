import { ArrowRightOutlined, CheckCircleFilled, ClockCircleOutlined, CloseCircleFilled } from "@ant-design/icons";
import { Skeleton, Tooltip } from "antd";
import type { ClickHouseTransactionDetail } from "../analytics/ClickHouseExplorerTypes";
import type { AppChain } from "../explorer-intelligence/appRoute";
import { AddressLink } from "./AddressLink";
import { chainNativeSymbol } from "./transactionRoutes";
import { formatDateTime, formatNumber } from "./transactionFormat";

export type DatasetState = "loading" | "ready" | "error";

interface Props {
  chain: AppChain;
  transaction?: ClickHouseTransactionDetail;
  loading: boolean;
  counts: { transfers?: number; internals?: number; logs?: number };
  loaded: { transfers: number; internals: number; logs: number };
  states: { transfers: DatasetState; internals: DatasetState; logs: DatasetState };
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <div className="txd-field"><span>{label}</span><strong>{children}</strong></div>;
}

function CoverageMark({ label, state, count, loaded }: { label: string; state: DatasetState; count?: number; loaded: number }) {
  return <li className={`txd-coverage-${state}`}>
    {state === "ready" ? <CheckCircleFilled /> : state === "error" ? <CloseCircleFilled /> : <ClockCircleOutlined spin />}
    <span>{label}</span>
    <b>{state === "ready" ? loaded < (count ?? 0) ? `${loaded}/${count}` : `${count ?? 0} 条` : state === "loading" ? "加载中" : "暂不可用"}</b>
  </li>;
}

export function TransactionOverview({ chain, transaction, loading, counts, loaded, states }: Props) {
  if (loading && !transaction) return <div className="txd-overview txd-overview-loading"><Skeleton active paragraph={{ rows: 5 }} /></div>;
  if (!transaction) return null;
  const method = transaction.method_name || transaction.method_id || "Contract Call";
  return <section className="txd-overview" aria-label="交易概览">
    <div className="txd-route">
      <div><span className="txd-route-label">From</span><AddressLink chain={chain} address={transaction.from_address} prominent showActions /></div>
      <span className="txd-route-arrow" aria-hidden="true"><ArrowRightOutlined /></span>
      <div><span className="txd-route-label">To</span><AddressLink chain={chain} address={transaction.to_address || transaction.created_contract_address} label={transaction.is_contract_creation ? "Contract Creation" : undefined} entityType={transaction.is_contract_creation ? "Contract" : undefined} prominent showActions /></div>
    </div>
    <div className="txd-field-grid">
      <Field label="Block">{transaction.block_number.toLocaleString("en-US")}</Field>
      <Field label="Timestamp">{formatDateTime(transaction.block_time)}</Field>
      <Field label="Method"><Tooltip title={method}><span className="txd-method">{method}</span></Tooltip></Field>
      <Field label="Value">{formatNumber(transaction.value_decimal)} {transaction.native_symbol || chainNativeSymbol(chain)}</Field>
      <Field label="Gas Used">{transaction.gas_used.toLocaleString("en-US")}</Field>
      <Field label="Gas Fee">{formatNumber(transaction.transaction_fee_native, 12)} {transaction.native_symbol || chainNativeSymbol(chain)}</Field>
      <Field label="Token Transfers">{states.transfers === "ready" ? counts.transfers ?? 0 : "…"}</Field>
      <Field label="Internal Tx">{states.internals === "ready" ? counts.internals ?? 0 : "…"}</Field>
      <Field label="Event Logs">{states.logs === "ready" ? counts.logs ?? 0 : "…"}</Field>
    </div>
    <div className="txd-summary-band">
      <div><span>交易摘要</span><strong>{method}</strong><small>{transaction.transaction_type || (transaction.input && transaction.input !== "0x" ? "合约调用" : "原生转账")}</small></div>
      <div className="txd-coverage"><span>数据完整度</span><ul>
        <li className="txd-coverage-ready"><CheckCircleFilled /><span>基础交易</span><b>Ready</b></li>
        <CoverageMark label="代币转账" state={states.transfers} count={counts.transfers} loaded={loaded.transfers} />
        <CoverageMark label="内部交易" state={states.internals} count={counts.internals} loaded={loaded.internals} />
        <CoverageMark label="事件日志" state={states.logs} count={counts.logs} loaded={loaded.logs} />
      </ul></div>
    </div>
  </section>;
}
