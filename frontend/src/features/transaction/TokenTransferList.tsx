import { ArrowRightOutlined } from "@ant-design/icons";
import { Empty, Skeleton, Tooltip } from "antd";
import type { ClickHouseTransactionTokenTransfer } from "../analytics/ClickHouseExplorerTypes";
import { HistoricalValue } from "../explorer-intelligence/HistoricalValue";
import { TokenIdentity } from "../explorer-intelligence/TokenIdentity";
import type { AppChain } from "../explorer-intelligence/appRoute";
import { AddressLink } from "./AddressLink";
import { formatDateTime, formatNumber } from "./transactionFormat";
import { compareTransferOrder } from "./transactionLogic";

interface Props { chain: AppChain; items: ClickHouseTransactionTokenTransfer[]; loading: boolean; error?: string }

function Amount({ item }: { item: ClickHouseTransactionTokenTransfer }) {
  const amount = item.amount.formatted || item.amount.decimal;
  const historicalPrice = item.usd?.price_usd;
  const historicalValue = item.usd?.usd_value;
  const tooltip = <div className="txd-amount-tooltip"><span>数量</span><b>{item.amount.decimal || "—"} {item.token.symbol || "Token"}</b><span>历史单价</span><b>{historicalPrice ? `$${formatNumber(historicalPrice, 8)}` : "—"}</b><span>历史估值</span><b><HistoricalValue value={historicalValue} approximate={false} /></b><span>价格时间</span><b>{formatDateTime(item.usd?.price_time)}</b><span>价格源</span><b>{item.usd?.price_source || "—"}</b></div>;
  return <Tooltip title={tooltip} placement="left"><span className="txd-amount"><strong>{formatNumber(amount, 12)}</strong><HistoricalValue value={historicalValue} approximate={false} /></span></Tooltip>;
}

export function TokenTransferList({ chain, items, loading, error }: Props) {
  if (loading) return <div className="txd-tab-skeleton">{Array.from({ length: 5 }, (_, index) => <Skeleton key={index} active title={false} paragraph={{ rows: 1 }} />)}</div>;
  if (error) return <div className="txd-dataset-error"><b>代币转账暂不可用</b><span>{error}</span></div>;
  if (!items.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该交易没有代币转账" />;
  const sorted = [...items].sort(compareTransferOrder);
  return <div className="txd-transfer-list" role="list">
    <div className="txd-transfer-head" aria-hidden="true"><span>发送方</span><span /><span>接收方</span><span>代币</span><span>数量 / 历史估值</span></div>
    {sorted.map((item) => <article className="txd-transfer-row" role="listitem" key={`${item.log_index}:${item.batch_index}`}>
      <div className="txd-transfer-index">#{item.log_index}</div>
      <div className="txd-transfer-from"><span className="txd-mobile-label">发送方</span><AddressLink chain={chain} address={item.from.address} label={item.from.entity_name || item.from.label} entityType={item.from.entity_type} /></div>
      <span className="txd-transfer-arrow" aria-hidden="true"><ArrowRightOutlined /></span>
      <div className="txd-transfer-to"><span className="txd-mobile-label">接收方</span><AddressLink chain={chain} address={item.to.address} label={item.to.entity_name || item.to.label} entityType={item.to.entity_type} /></div>
      <div className="txd-transfer-token"><span className="txd-mobile-label">代币</span><TokenIdentity chainId={item.token.chain_id} address={item.token.contract} symbol={item.token.symbol} name={item.token.name} logoURI={item.token.logo_uri} verified={item.token.verified} spam={item.token.spam} /><small className="txd-standard">{item.standard || item.token.standard || "Token"}</small></div>
      <div className="txd-transfer-amount"><span className="txd-mobile-label">数量 / 历史估值</span><Amount item={item} /></div>
    </article>)}
  </div>;
}
