// V2.0 实时资产面板（设计 §7/§8/§20）
//
// 展示实时/缓存/过期状态、失败不显示 0、保存余额快照（含历史对比）、
// 加入 Investigation Agent 一键调查。

import { CameraOutlined, ExperimentOutlined, ReloadOutlined } from "@ant-design/icons";
import { Button, Empty, message, Spin } from "antd";
import { useState } from "react";
import { DetailPanel } from "../../design-system/DesignSystem";
import { createInvestigation } from "../intelligence/intelligenceApi";
import { saveBalanceSnapshot, type SnapshotDiff } from "./flowAssetApi";
import { assetStateLabel, displayBalance, useAddressAssets } from "./useAddressAssets";
import { shortAddr } from "./format";

export interface FlowAddressAssetsProps {
  chain: string;
  chainId: number;
  address: string;
}

export default function FlowAddressAssets(props: FlowAddressAssetsProps) {
  const { assets, state, refresh } = useAddressAssets({
    chain: props.chain,
    chainId: props.chainId,
    address: props.address,
    tokens: [], // 默认资产（native + USDT + USDC）
  });
  const [saving, setSaving] = useState(false);
  const [snapshotDiff, setSnapshotDiff] = useState<SnapshotDiff[] | null>(null);
  const [investigating, setInvestigating] = useState(false);

  const status = assetStateLabel(assets?.status);
  const source = assets?.source ? `数据源 ${assets.source}` : "";

  const onSaveSnapshot = async () => {
    if (!props.address) return;
    setSaving(true);
    try {
      const result = await saveBalanceSnapshot(props.chain, props.chainId, props.address);
      if (result) {
        setSnapshotDiff(result.diff);
        void message.success("余额快照已保存");
      } else {
        void message.error("保存快照失败（RPC 不可用）");
      }
    } catch {
      void message.error("保存快照失败");
    } finally {
      setSaving(false);
    }
  };

  const onInvestigate = async () => {
    if (!props.address) return;
    setInvestigating(true);
    try {
      const result = await createInvestigation({
        address: props.address,
        chain: props.chain,
        objective: "追踪当前地址资金流向，识别关键路径与沉淀位置",
        expected_result: ["资金路径", "沉淀地址", "交易所入口"],
        mode: "fund_trace",
      });
      if (result?.investigation) {
        void message.success(`调查已启动：${result.investigation.id}`);
      } else {
        void message.error("启动调查失败");
      }
    } catch {
      void message.error("启动调查失败");
    } finally {
      setInvestigating(false);
    }
  };

  return (
    <DetailPanel
      className="analytics-assets-panel"
      title="实时资产"
      description={`BNB / USDT / USDC · 图边金额与实时余额口径不同（设计 §7）`}
      extra={(
        <div className="analytics-assets-actions">
          <Button
            size="small"
            icon={<CameraOutlined />}
            onClick={onSaveSnapshot}
            loading={saving}
            aria-label="保存余额快照"
          >
            保存快照
          </Button>
          <Button
            size="small"
            icon={<ExperimentOutlined />}
            onClick={onInvestigate}
            loading={investigating}
            aria-label="加入 Investigation Agent"
          >
            加入调查
          </Button>
          <Button
            size="small"
            icon={<ReloadOutlined />}
            onClick={refresh}
            disabled={state === "loading"}
            aria-label="刷新余额"
          >
            刷新余额
          </Button>
        </div>
      )}
    >
      {state === "loading" ? (
        <Spin size="small" />
      ) : !assets || assets.assets.length === 0 ? (
        <Empty description="实时资产查询失败（RPC 不可用或未配置）" image={Empty.PRESENTED_IMAGE_SIMPLE} />
      ) : (
        <>
          <div className="analytics-assets-list">
            {assets.assets.map((balance) => (
              <div key={balance.token_address || "native"} className="analytics-asset-row">
                <span className="analytics-asset-symbol">{balance.symbol}</span>
                <span className={`analytics-asset-balance ${balance.status === "success" ? "" : "failed"}`}>
                  {displayBalance(balance)}
                </span>
                <span className={`analytics-asset-status ${balance.status}`}>
                  {balance.status === "success" ? "" : "失败"}
                </span>
              </div>
            ))}
          </div>
          <div className="analytics-assets-meta">
            <span className={`analytics-asset-state ${status.tone}`}>{status.text}</span>
            {assets.block_number ? <span>区块 {shortAddr(assets.block_number, 8, 4)}</span> : null}
            <span>查询 {assets.queried_at ? new Date(assets.queried_at).toLocaleTimeString() : "—"}</span>
            {source ? <span>{source}</span> : null}
          </div>

          {/* V2.0 快照对比（设计 §8 变化量/变化率） */}
          {snapshotDiff && snapshotDiff.length > 0 ? (
            <div className="analytics-assets-diff">
              <div className="analytics-assets-diff-title">与最近快照对比</div>
              {snapshotDiff.map((d) => (
                <div key={d.symbol} className="analytics-assets-diff-row">
                  <span>{d.symbol}</span>
                  <span>{d.snapshot} → {d.current}</span>
                  <span className={d.change >= 0 ? "positive" : "negative"}>
                    {d.change >= 0 ? "+" : ""}{d.change.toFixed(2)} ({d.change_pct >= 0 ? "+" : ""}{d.change_pct.toFixed(1)}%)
                  </span>
                  <span className="muted">{d.snapshot_at}</span>
                </div>
              ))}
            </div>
          ) : null}
        </>
      )}
    </DetailPanel>
  );
}
