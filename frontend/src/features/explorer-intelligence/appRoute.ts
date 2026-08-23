// 应用路由：URL ↔ 页面状态 的可逆映射（UAT-FIX-001/002）。
// 纯函数，便于表驱动测试；禁止在组件内散落 URL 拼接。

export type AppChain = "bsc" | "eth" | "base" | "arbitrum";

export interface AppRoute {
  active: string;
  chain?: AppChain;
  address?: string;
  txHash?: string;
  query?: Record<string, string>;
}

const CHAINS: AppChain[] = ["bsc", "eth", "base", "arbitrum"];
const EVM_ADDRESS = /^0x[0-9a-fA-F]{40}$/;
const EVM_TRANSACTION_HASH = /^0x[0-9a-fA-F]{64}$/;

/** 页面 key → 规范路径（无参数部分）。 */
const PAGE_PATHS: Record<string, string> = {
  explorer: "/explorer",
  "smart-download": "/smart-download",
  "analytics-address": "/analytics-address",
  risk: "/risk",
  "analytics-graph": "/fund-flow",
  "analytics-dashboard": "/analytics-dashboard",
  "analytics-report": "/analytics-report",
  "crypto-address": "/crypto-address",
  "data-quality": "/data-quality",
  "financial-quality": "/financial-quality",
  "price-engine": "/price-engine",
  intelligence: "/intelligence",
  reports: "/reports",
  "system-settings": "/system-settings",
  clean: "/clean",
  "crypto-rpc": "/crypto-rpc",
  "crypto-datasource": "/crypto-datasource",
};

/** 已知页面集合（含无独立 URL 的子项 fallback）。 */
export const KNOWN_PAGES = new Set<string>(Object.keys(PAGE_PATHS));

function validChain(value: string | undefined | null): value is AppChain {
  return !!value && (CHAINS as string[]).includes(value);
}

function paramsToRecord(search: string): Record<string, string> {
  const out: Record<string, string> = {};
  new URLSearchParams(search).forEach((v, k) => { out[k] = v; });
  return out;
}

/**
 * 解析 location 为应用路由；无法识别的路径返回 null（调用方按默认页处理）。
 * /explorer/{chain}[/address/{addr}][?...] 与 PAGE_PATHS 中的页面路径均支持。
 */
export function parseAppRoute(location: Pick<Location, "pathname" | "search">): AppRoute | null {
  const { pathname, search } = location;
  // 先识别交易详情路径，再由详情页给出精确的 Hash 格式错误；
  // 否则错误深链会悄悄回退首页，用户无法判断是“未找到”还是输入不合法。
  const transaction = pathname.match(/^\/(bsc|eth|base|arbitrum)\/tx\/([^/]{1,256})\/?$/);
  if (transaction) {
    return {
      active: "transaction",
      chain: transaction[1] as AppChain,
      txHash: transaction[2].toLowerCase(),
      query: paramsToRecord(search),
    };
  }
  // /explorer/{chain} 或 /explorer/{chain}/address/{addr}；其余后缀不识别
  const m = pathname.match(/^\/explorer\/(bsc|eth|base|arbitrum)(\/.*)?$/);
  if (m) {
    const chain = m[1] as AppChain;
    const rest = m[2] ?? "";
    if (rest === "" || rest === "/") {
      return { active: "explorer", chain, address: undefined, query: paramsToRecord(search) };
    }
    const am = rest.match(/^\/address\/(0x[0-9a-fA-F]{40})(\/.*)?$/);
    if (am) {
      return { active: "explorer", chain, address: am[1].toLowerCase(), query: paramsToRecord(search) };
    }
    return null; // /explorer/{chain}/其他非法路径
  }
  for (const [page, path] of Object.entries(PAGE_PATHS)) {
    if (page === "explorer") continue; // explorer 由上方正则精确匹配
    if (pathname === path || pathname.startsWith(path + "/")) {
      return { active: page, query: paramsToRecord(search) };
    }
  }
  return null;
}

/** 由路由生成规范 URL；与 parseAppRoute 互为可逆（参数顺序规范化为 URLSearchParams 顺序）。 */
export function buildAppURL(route: AppRoute): string {
  const q = new URLSearchParams(route.query ?? {});
  const query = q.toString();
  let base: string;
  if (route.active === "explorer") {
    base = route.chain ? `/explorer/${route.chain}` : "/explorer";
    if (route.address) base += `/address/${route.address}`;
  } else if (route.active === "transaction" && route.chain && route.txHash) {
    base = `/${route.chain}/tx/${route.txHash.toLowerCase()}`;
  } else {
    base = PAGE_PATHS[route.active] ?? "/";
  }
  return query ? `${base}?${query}` : base;
}

export function isTransactionHash(value: unknown): value is string {
  return EVM_TRANSACTION_HASH.test(String(value ?? "").trim());
}

export function chainIdToAppChain(chainId: number | string | undefined): AppChain {
  const normalized = String(chainId ?? "").toLowerCase();
  if (normalized === "1" || normalized === "ethereum" || normalized === "eth") return "eth";
  if (normalized === "8453" || normalized === "base") return "base";
  if (normalized === "42161" || normalized === "arbitrum") return "arbitrum";
  return "bsc";
}

export function buildTransactionURL(chain: AppChain | number | string, hash: string): string {
  return buildAppURL({ active: "transaction", chain: chainIdToAppChain(chain), txHash: hash.toLowerCase() });
}

/** 校验链值，非法回退默认链。 */
export function normalizeChain(value: string | undefined | null, fallback: AppChain = "bsc"): AppChain {
  return validChain(value) ? value : fallback;
}
