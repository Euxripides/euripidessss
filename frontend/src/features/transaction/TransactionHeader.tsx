import { ArrowLeftOutlined, ExportOutlined, NodeIndexOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { Button, Skeleton, Space, Tooltip } from "antd";
import type { ClickHouseTransactionDetail } from "../analytics/ClickHouseExplorerTypes";
import type { AppChain } from "../explorer-intelligence/appRoute";
import { TxHashLink } from "./TxHashLink";
import { chainLabel, externalTransactionURL } from "./transactionRoutes";
import { formatDateTime } from "./transactionFormat";

interface Props {
  chain: AppChain;
  hash: string;
  transaction?: ClickHouseTransactionDetail;
  loading: boolean;
  onNavigate: (page: "explorer" | "analytics-graph" | "intelligence") => void;
}

function statusMeta(status: string | undefined) {
  const normalized = String(status || "PENDING").toUpperCase();
  if (["SUCCESS", "SUCCEEDED", "1"].includes(normalized)) return { label: "成功", tone: "success" };
  if (["FAILED", "FAILURE", "0"].includes(normalized)) return { label: "失败", tone: "failed" };
  return { label: "Pending", tone: "pending" };
}

export function TransactionHeader({ chain, hash, transaction, loading, onNavigate }: Props) {
  const status = statusMeta(transaction?.status);
  return <header className="txd-header">
    <div className="txd-header-top">
      <button type="button" className="txd-back" onClick={() => { if (window.history.length > 1) window.history.back(); else onNavigate("explorer"); }}><ArrowLeftOutlined /> 返回</button>
      <span className="txd-title">交易详情</span>
      <span className="txd-chain">{chainLabel(chain)}</span>
    </div>
    <div className="txd-hash-row">
      <TxHashLink chainId={chain} hash={hash} compact={false} showCopy />
      <Space size={4} className="txd-header-actions">
        <Tooltip title="外部区块浏览器仅作辅助"><Button type="text" icon={<ExportOutlined />} href={externalTransactionURL(chain, hash)} target="_blank" rel="noopener noreferrer">外部打开</Button></Tooltip>
        <Button type="text" icon={<SafetyCertificateOutlined />} onClick={() => onNavigate("intelligence")}>加入调查</Button>
        <Button type="text" icon={<NodeIndexOutlined />} onClick={() => onNavigate("analytics-graph")}>加入图谱</Button>
      </Space>
    </div>
    {loading && !transaction ? <Skeleton className="txd-header-skeleton" active title={false} paragraph={{ rows: 1, width: "42%" }} /> : <div className="txd-status-line">
      <span className={`txd-status txd-status-${status.tone}`}><i />{status.label}</span>
      {transaction ? <><span>·</span><span>Block {transaction.block_number.toLocaleString("en-US")}</span><span>·</span><time>{formatDateTime(transaction.block_time)}</time></> : null}
    </div>}
  </header>;
}
