// V2.0 实时资产 Hook（设计 §7/§16）
//
// 职责：查询单地址实时资产、状态判定（实时/缓存/过期/失败）、
// 失败 Token 不显示为 0、快速切换中心时旧请求不覆盖新地址（AbortController）。

import { useCallback, useEffect, useRef, useState } from "react";
import { fetchAddressAssets, type AddressAssets, type AssetBalance, type AssetState } from "./flowAssetApi";

export interface UseAddressAssetsOptions {
  chain: string;
  chainId: number;
  address: string | null;
  tokens?: string[];
  autoLoad?: boolean;
}

export interface AddressAssetsView {
  assets: AddressAssets | null;
  state: "idle" | "loading" | "ready" | "failed";
  queriedAt: string | null;
  refresh: () => void;
}

export function useAddressAssets(options: UseAddressAssetsOptions): AddressAssetsView {
  const { chain, chainId, address, tokens, autoLoad = true } = options;
  const [assets, setAssets] = useState<AddressAssets | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "ready" | "failed">("idle");
  const abortRef = useRef<AbortController | null>(null);
  const requestSeq = useRef(0);

  const load = useCallback(() => {
    if (!address) {
      setAssets(null);
      setState("idle");
      return;
    }
    // 取消旧请求（设计 §26：旧请求使用 AbortController 取消）
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const seq = ++requestSeq.current;

    setState("loading");
    fetchAddressAssets({ chain, chain_id: chainId, address, tokens, force_refresh: false })
      .then((next) => {
        if (seq !== requestSeq.current || controller.signal.aborted) return; // 旧请求不覆盖新地址
        setAssets(next);
        setState(next ? "ready" : "failed");
      })
      .catch(() => {
        if (seq !== requestSeq.current || controller.signal.aborted) return;
        setAssets(null);
        setState("failed");
      });
  }, [chain, chainId, address, tokens]);

  const refresh = useCallback(() => {
    if (!address) return;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const seq = ++requestSeq.current;
    setState("loading");
    fetchAddressAssets({ chain, chain_id: chainId, address, tokens, force_refresh: true })
      .then((next) => {
        if (seq !== requestSeq.current || controller.signal.aborted) return;
        setAssets(next);
        setState(next ? "ready" : "failed");
      })
      .catch(() => {
        if (seq !== requestSeq.current || controller.signal.aborted) return;
        setState("failed");
      });
  }, [chain, chainId, address, tokens]);

  useEffect(() => {
    if (autoLoad) load();
    return () => {
      abortRef.current?.abort();
    };
  }, [autoLoad, load]);

  return {
    assets,
    state,
    queriedAt: assets?.queried_at ?? null,
    refresh,
  };
}

// 状态标签与时效（设计 §7.4）
export function assetStateLabel(state: AssetState | undefined): { text: string; tone: string } {
  switch (state) {
    case "fresh": return { text: "实时", tone: "fresh" };
    case "cached": return { text: "缓存", tone: "cached" };
    case "stale": return { text: "已过期", tone: "stale" };
    case "partial": return { text: "部分成功", tone: "partial" };
    case "failed": return { text: "查询失败", tone: "failed" };
    default: return { text: "未查询", tone: "idle" };
  }
}

// 失败 Token 不显示为 0（设计 §7.4/§26）
export function displayBalance(balance: AssetBalance): string {
  if (balance.status !== "success") return "—";
  return balance.balance;
}
