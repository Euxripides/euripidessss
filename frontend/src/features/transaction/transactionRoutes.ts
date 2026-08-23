import type { ExplorerChain } from "../analytics/ClickHouseExplorerTypes";
import { chainIdToAppChain, type AppChain } from "../explorer-intelligence/appRoute";

export function routeChainToExplorer(chain: AppChain): ExplorerChain {
  return chain;
}

export function chainLabel(chain: AppChain): string {
  return chain === "bsc" ? "BNB Smart Chain" : chain === "eth" ? "Ethereum" : chain === "base" ? "Base" : "Arbitrum One";
}

export function chainNativeSymbol(chain: AppChain): string {
  return chain === "bsc" ? "BNB" : "ETH";
}

export function externalTransactionURL(chain: AppChain | number | string, hash: string): string {
  const normalized = chainIdToAppChain(chain);
  const base = normalized === "bsc"
    ? "https://bscscan.com"
    : normalized === "eth"
      ? "https://etherscan.io"
      : normalized === "base"
        ? "https://basescan.org"
        : "https://arbiscan.io";
  return `${base}/tx/${encodeURIComponent(hash)}`;
}
