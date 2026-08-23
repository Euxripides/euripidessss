import { CopyOutlined, ExportOutlined } from "@ant-design/icons";
import { Button, Tooltip, message } from "antd";
import { useCallback, useEffect, useRef } from "react";
import { prefetchClickHouseTransactionDetail } from "../analytics/ClickHouseExplorerApi";
import { buildTransactionURL, chainIdToAppChain, isTransactionHash } from "../explorer-intelligence/appRoute";
import { externalTransactionURL, routeChainToExplorer } from "./transactionRoutes";
import "./transaction-detail.css";

export interface TxHashLinkProps {
  chainId?: number | string;
  hash: string;
  compact?: boolean;
  showCopy?: boolean;
  showExternal?: boolean;
  openInNewTab?: boolean;
  className?: string;
}

export function shortenTransactionHash(hash: string, compact = true): string {
  const value = hash.trim();
  if (!compact || value.length <= 22) return value;
  return `${value.slice(0, 12)}…${value.slice(-8)}`;
}

export function TxHashLink({
  chainId = 56,
  hash,
  compact = true,
  showCopy = false,
  showExternal = false,
  openInNewTab = false,
  className = "",
}: TxHashLinkProps) {
  const normalizedHash = hash.trim().toLowerCase();
  const chain = chainIdToAppChain(chainId);
  const href = isTransactionHash(normalizedHash) ? buildTransactionURL(chain, normalizedHash) : "";
  const prefetchTimer = useRef<number | undefined>(undefined);

  const cancelPrefetch = useCallback(() => {
    if (prefetchTimer.current !== undefined) window.clearTimeout(prefetchTimer.current);
    prefetchTimer.current = undefined;
  }, []);

  const schedulePrefetch = useCallback(() => {
    if (!href || prefetchTimer.current !== undefined) return;
    prefetchTimer.current = window.setTimeout(() => {
      prefetchTimer.current = undefined;
      prefetchClickHouseTransactionDetail(normalizedHash, routeChainToExplorer(chain));
    }, 300);
  }, [chain, href, normalizedHash]);

  useEffect(() => cancelPrefetch, [cancelPrefetch]);

  if (!href) return <span className={`tx-hash-invalid ${className}`.trim()} title={hash}>{shortenTransactionHash(hash)}</span>;

  const copyHash = async () => {
    try {
      await navigator.clipboard.writeText(normalizedHash);
      void message.success("交易哈希已复制");
    } catch {
      void message.error("复制失败，请手动复制");
    }
  };

  return (
    <span className={`tx-hash-control ${className}`.trim()}>
      <Tooltip title={normalizedHash} mouseEnterDelay={0.35}>
        <a
          className="tx-hash-link"
          href={href}
          target={openInNewTab ? "_blank" : undefined}
          rel={openInNewTab ? "noreferrer" : undefined}
          onPointerEnter={schedulePrefetch}
          onPointerLeave={cancelPrefetch}
          onFocus={schedulePrefetch}
          onBlur={cancelPrefetch}
          onClick={(event) => {
            event.stopPropagation();
            if (openInNewTab || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
            event.preventDefault();
            if (`${window.location.pathname}${window.location.search}` !== href) window.history.pushState(null, "", href);
            window.dispatchEvent(new PopStateEvent("popstate"));
          }}
        >
          {shortenTransactionHash(normalizedHash, compact)}
        </a>
      </Tooltip>
      {showCopy ? (
        <Tooltip title="复制交易哈希">
          <Button className="tx-hash-action" type="text" size="small" icon={<CopyOutlined />} aria-label="复制交易哈希" onClick={(event) => { event.stopPropagation(); void copyHash(); }} />
        </Tooltip>
      ) : null}
      {showExternal ? (
        <Tooltip title="在外部区块浏览器中打开">
          <Button className="tx-hash-action" type="text" size="small" icon={<ExportOutlined />} aria-label="外部浏览器" href={externalTransactionURL(chain, normalizedHash)} target="_blank" rel="noreferrer" onClick={(event) => event.stopPropagation()} />
        </Tooltip>
      ) : null}
    </span>
  );
}
