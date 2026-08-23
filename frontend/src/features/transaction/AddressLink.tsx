import { CopyOutlined, NodeIndexOutlined } from "@ant-design/icons";
import { Button, Tooltip, message } from "antd";
import { buildAppURL, type AppChain } from "../explorer-intelligence/appRoute";
import { shortAddress } from "./transactionFormat";

interface AddressLinkProps {
  chain: AppChain;
  address: string;
  label?: string;
  entityType?: string;
  prominent?: boolean;
  showActions?: boolean;
}

export function AddressLink({ chain, address, label, entityType, prominent = false, showActions = false }: AddressLinkProps) {
  const normalized = address.trim().toLowerCase();
  const valid = /^0x[0-9a-f]{40}$/.test(normalized);
  const zero = /^0x0{40}$/.test(normalized);
  const href = valid ? buildAppURL({ active: "explorer", chain, address: normalized }) : "";
  const primary = zero ? "Zero Address" : label || shortAddress(normalized);
  const secondary = label ? shortAddress(normalized) : entityType;

  const navigate = (event: React.MouseEvent<HTMLAnchorElement>) => {
    event.stopPropagation();
    if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    window.history.pushState(null, "", href);
    window.dispatchEvent(new PopStateEvent("popstate"));
  };

  if (!valid) return <span className="txd-address txd-address-invalid">—</span>;
  return (
    <span className={`txd-address ${prominent ? "txd-address-prominent" : ""}`}>
      <Tooltip title={normalized} mouseEnterDelay={0.35}>
        <a href={href} onClick={navigate}>
          <span className="txd-identicon" aria-hidden="true">{zero ? "0" : normalized.slice(2, 4).toUpperCase()}</span>
          <span className="txd-address-copy"><b>{primary}</b>{secondary ? <small>{secondary}</small> : null}</span>
        </a>
      </Tooltip>
      {showActions ? <span className="txd-address-actions">
        <Tooltip title="复制地址"><Button type="text" size="small" icon={<CopyOutlined />} aria-label="复制地址" onClick={() => void navigator.clipboard.writeText(normalized).then(() => message.success("地址已复制"))} /></Tooltip>
        <Tooltip title="在地址详情中查看图谱"><Button type="text" size="small" icon={<NodeIndexOutlined />} href={href} aria-label="查看地址详情" /></Tooltip>
      </span> : null}
    </span>
  );
}
