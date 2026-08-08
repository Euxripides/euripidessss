// 地址关系图 V2.0 UI 整改 — 右侧地址详情 Inspector（设计 §5.4）
//
// 结构：地址详情（完整地址/复制/角色/标签来源）→ 图边统计 → 实时资产 →
// Tabs（相邻地址/交易记录/统计/证据）→ 证据边界说明 → 操作（保存快照/加入调查/退出聚焦）。

import {
  ArrowLeftOutlined,
  ArrowRightOutlined,
  CameraOutlined,
  CloseOutlined,
  CopyOutlined,
  DoubleRightOutlined,
  ExperimentOutlined,
  ReloadOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { Button, Empty, Space, Spin, Tabs, Tag, message } from "antd";
import { useState } from "react";
import type { AddressAssets } from "./flowAssetApi";
import { assetStateLabel, displayBalance } from "./useAddressAssets";
import type { AddressStats } from "./flowStatsApi";
import { directionLabel, fmtAmount, riskTone } from "./flowWorkspaceGraph";
import type { EnhancedNodeData, FocusDirection } from "./graphUpgrade";
import type { FlowEdge } from "./analyticsApi";
import { shortAddr } from "./format";
import { CONFIDENCE_TIER_LABELS, ENTITY_TYPE_LABELS, type EntityResolution } from "../entity/entityApi";

export interface NeighborInfo {
  address: string;
  direction: "in" | "out";
  token: string;
  amount: number;
  txCount: number;
}

export interface FlowInspectorProps {
  address: string | null;
  node: EnhancedNodeData | null;
  direction: FocusDirection;
  neighbors: NeighborInfo[];
  transactions: FlowEdge[];
  txLoading: boolean;
  statsLoading: boolean;
  addressStats: AddressStats | null;
  assets: AddressAssets | null;
  assetsState: "idle" | "loading" | "ready" | "failed";
  entityInfo?: EntityResolution | null;
  onAssetsRefresh: () => void;
  savingSnapshot: boolean;
  onSaveSnapshot: () => void;
  investigating: boolean;
  onInvestigate: () => void;
  /** V2.2 智能数据补充（Smart Download Orchestrator） */
  onSmartFill: () => void;
  onExitFocus: () => void;
  onSelectNeighbor: (address: string) => void;
  onClose: () => void;
  /** 折叠/展开地址详情（dock 与 collapsible 模式可用；drawer 模式不传） */
  onCollapse?: () => void;
}

function MetricCell({ label, value, tone }: { label: string; value: string; tone?: "positive" | "negative" }) {
  return (
    <div className="flow-metric-cell">
      <span>{label}</span>
      <strong className={tone ?? ""}>{value}</strong>
    </div>
  );
}

function NeighborRow({ item, onSelect }: { item: NeighborInfo; onSelect: (address: string) => void }) {
  return (
    <button type="button" className="flow-neighbor-item" onClick={() => onSelect(item.address)}>
      <span className={`flow-neighbor-dir ${item.direction === "in" ? "is-in" : "is-out"}`}>
        {item.direction === "in" ? <ArrowLeftOutlined /> : <ArrowRightOutlined />}
      </span>
      <span className="flow-neighbor-main">
        <span className="flow-neighbor-address">{item.address}</span>
        <span className="flow-neighbor-meta">
          {item.direction === "in" ? "流入" : "流出"} · {item.txCount} 笔 · {fmtAmount(item.amount)} {item.token ?? ""}
        </span>
      </span>
    </button>
  );
}

function TxRow({ tx }: { tx: FlowEdge }) {
  const incoming = tx.direction === "incoming";
  return (
    <div className="flow-tx-item">
      <span className={`flow-tx-dir ${incoming ? "is-in" : "is-out"}`}>
        {incoming ? <ArrowLeftOutlined /> : <ArrowRightOutlined />}
      </span>
      <span className="flow-tx-main">
        <span className="flow-tx-hash" title={tx.tx_hash}>{shortAddr(tx.tx_hash, 10, 8)}</span>
        <span className="flow-tx-meta">
          {tx.block ? `区块 ${shortAddr(tx.block, 6, 4)} · ` : ""}{incoming ? "流入" : "流出"} {tx.token}
        </span>
      </span>
      <strong className="flow-tx-amount">{fmtAmount(Number.parseFloat(tx.amount) || 0)}</strong>
    </div>
  );
}

export default function FlowInspector({
  address,
  node,
  direction,
  neighbors,
  transactions,
  txLoading,
  statsLoading,
  addressStats,
  assets,
  assetsState,
  entityInfo,
  onAssetsRefresh,
  savingSnapshot,
  onSaveSnapshot,
  investigating,
  onInvestigate,
  onSmartFill,
  onExitFocus,
  onSelectNeighbor,
  onClose,
  onCollapse,
}: FlowInspectorProps) {
  const [activeTab, setActiveTab] = useState("neighbors");
  const status = assetStateLabel(assets?.status);

  const onCopyAddress = async () => {
    if (!address) return;
    try {
      await navigator.clipboard.writeText(address);
      void message.success("地址已复制");
    } catch {
      void message.error("复制失败，请手动选择复制");
    }
  };

  if (!address || !node) {
    return (
      <aside className="flow-inspector" aria-label="地址详情">
        <div className="flow-inspector-header">
          <span className="flow-inspector-title">地址详情</span>
          <Button size="small" type="text" icon={<CloseOutlined />} onClick={onClose} aria-label="关闭详情" />
        </div>
        <div className="flow-inspector-empty">
          <Empty
            description="未选择地址 — 搜索地址或点击画布节点查看详情"
            image={Empty.PRESENTED_IMAGE_SIMPLE}
          />
        </div>
      </aside>
    );
  }

  const netFlow = node.outAmount - node.inAmount;

  return (
    <aside className="flow-inspector" aria-label="地址详情">
      <div className="flow-inspector-header">
        <span className="flow-inspector-title">地址详情</span>
        <div className="flow-inspector-header-actions">
          {onCollapse ? (
            <Button
              size="small"
              type="text"
              icon={<DoubleRightOutlined />}
              onClick={onCollapse}
              aria-label="收起地址详情"
              title="折叠地址详情"
            />
          ) : null}
          <Button size="small" type="text" icon={<CloseOutlined />} onClick={onClose} aria-label="关闭详情" />
        </div>
      </div>

      <div className="flow-inspector-body">
        {/* 地址详情 */}
        <section className="flow-inspector-section">
          <div className="flow-inspector-address-row">
            <code className="flow-inspector-address">{address}</code>
            <Button size="small" type="text" icon={<CopyOutlined />} onClick={onCopyAddress} aria-label="复制完整地址" />
          </div>
          <div className="flow-inspector-tags">
            <span className={`flow-inspector-role ${riskTone(node.risk)}`}>{node.kind}</span>
            <span className="flow-inspector-source">标签来源：当前数据集图关系</span>
          </div>
          {entityInfo && (entityInfo.entity || (entityInfo.labels?.length ?? 0) > 0) && (
            <div className="flow-inspector-entity">
              {entityInfo.entity && (
                <div className="flow-inspector-entity-name">
                  <strong>{entityInfo.entity.name}</strong>
                  <Tag color="blue">
                    {ENTITY_TYPE_LABELS[entityInfo.entity.entity_type] ?? entityInfo.entity.entity_type}
                  </Tag>
                </div>
              )}
              <Space wrap size={4}>
                {(entityInfo.labels ?? []).map((l) => (
                  <Tag key={l.label + l.scope} color={l.scope === "INVESTIGATION" ? "orange" : "geekblue"}>
                    {l.label}
                  </Tag>
                ))}
              </Space>
              <div className="flow-inspector-entity-meta">
                可信度 {CONFIDENCE_TIER_LABELS[entityInfo.confidence_tier] ?? entityInfo.confidence_tier} ·{" "}
                证据 {entityInfo.evidence?.length ?? 0} · 来源可展开
              </div>
            </div>
          )}
        </section>

        {/* 图边统计 */}
        <section className="flow-inspector-section">
          <h4 className="flow-inspector-section-title">资金统计 · 图边归因</h4>
          <div className="flow-metrics-grid">
            <MetricCell label="图边归因流入" value={fmtAmount(node.inAmount)} />
            <MetricCell label="图边归因流出" value={fmtAmount(node.outAmount)} />
            <MetricCell
              label="图边净流量"
              value={`${netFlow >= 0 ? "+" : ""}${fmtAmount(netFlow)}`}
              tone={netFlow >= 0 ? "positive" : "negative"}
            />
            <MetricCell label="关联交易" value={`${node.inCount + node.outCount} 笔`} />
            <MetricCell label="直接上游" value={`${node.upstream} 个`} />
            <MetricCell label="直接下游" value={`${node.downstream} 个`} />
          </div>
        </section>

        {/* 实时资产 */}
        <section className="flow-inspector-section">
          <h4 className="flow-inspector-section-title">资产统计 · 实时链上</h4>
          {assetsState === "loading" || assetsState === "idle" ? (
            <Spin size="small" />
          ) : assetsState === "failed" ? (
            <div className="flow-assets-empty">
              <span>实时资产查询失败（RPC 不可用或未配置）</span>
              <Button size="small" icon={<ReloadOutlined />} onClick={onAssetsRefresh}>重试</Button>
            </div>
          ) : !assets || assets.assets.length === 0 ? (
            <div className="flow-assets-empty">
              <span>查询成功但未返回资产（该地址无已配置 Token 余额）</span>
              <Button size="small" icon={<ReloadOutlined />} onClick={onAssetsRefresh}>重试</Button>
            </div>
          ) : (
            <>
              <div className="flow-assets-list">
                {assets.assets.map((balance) => (
                  <div key={balance.token_address || "native"} className="flow-asset-row">
                    <span className="flow-asset-symbol">{balance.symbol}</span>
                    <span className={`flow-asset-balance ${balance.status === "success" ? "" : "failed"}`}>
                      {displayBalance(balance)}
                    </span>
                    <span className={`flow-asset-status ${balance.status}`}>
                      {balance.status === "success" ? "" : "失败"}
                    </span>
                  </div>
                ))}
              </div>
              <div className="flow-assets-meta">
                <span className={`flow-asset-state ${status.tone}`}>{status.text}</span>
                {assets.block_number ? <span>区块 {shortAddr(assets.block_number, 8, 4)}</span> : null}
                <span>查询 {assets.queried_at ? new Date(assets.queried_at).toLocaleTimeString() : "—"}</span>
                {assets.source ? <span>数据源 {assets.source}</span> : null}
              </div>
            </>
          )}
        </section>

        {/* Tabs */}
        <section className="flow-inspector-section flow-inspector-tabs">
          <Tabs
            activeKey={activeTab}
            onChange={setActiveTab}
            size="small"
            items={[
              {
                key: "neighbors",
                label: `相邻地址 (${neighbors.length})`,
                children: neighbors.length === 0 ? (
                  <Empty description="当前范围内没有相邻地址" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                ) : (
                  <div className="flow-neighbor-list">
                    {neighbors.map((item) => (
                      <NeighborRow key={item.address} item={item} onSelect={onSelectNeighbor} />
                    ))}
                  </div>
                ),
              },
              {
                key: "transactions",
                label: `交易记录 (${transactions.length})`,
                children: txLoading ? (
                  <Spin size="small" />
                ) : transactions.length === 0 ? (
                  <Empty description="交易明细暂不可用（需 DuckDB 数据源）" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                ) : (
                  <div className="flow-tx-list">
                    {transactions.slice(0, 80).map((tx, index) => (
                      <TxRow key={`${tx.tx_hash}-${index}`} tx={tx} />
                    ))}
                  </div>
                ),
              },
              {
                key: "stats",
                label: "统计",
                children: statsLoading ? (
                  <Spin size="small" />
                ) : !addressStats ? (
                  <Empty description="地址统计暂不可用（需 DuckDB 数据源）" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                ) : (
                  <div className="flow-stats-grid">
                    <MetricCell label="总交易" value={`${addressStats.tx_count}`} />
                    <MetricCell label="流入笔数" value={`${addressStats.in_count}`} />
                    <MetricCell label="流出笔数" value={`${addressStats.out_count}`} />
                    <MetricCell label="直接上游" value={`${addressStats.unique_upstream}`} />
                    <MetricCell label="直接下游" value={`${addressStats.unique_downstream}`} />
                    <MetricCell label="活跃天数" value={`${addressStats.active_days}`} />
                    <MetricCell
                      label="净流量"
                      value={`${Number(addressStats.net_flow) >= 0 ? "+" : ""}${fmtAmount(Number(addressStats.net_flow))}`}
                      tone={Number(addressStats.net_flow) >= 0 ? "positive" : "negative"}
                    />
                    <MetricCell label="主导 Token" value={shortAddr(addressStats.dominant_token, 6, 4)} />
                    <MetricCell label="Top1 来源占比" value={`${(addressStats.top1_source_ratio * 100).toFixed(0)}%`} />
                    <MetricCell label="Top5 来源占比" value={`${(addressStats.top5_source_ratio * 100).toFixed(0)}%`} />
                    <MetricCell label="Top1 去向占比" value={`${(addressStats.top1_target_ratio * 100).toFixed(0)}%`} />
                    <MetricCell label="Top5 去向占比" value={`${(addressStats.top5_target_ratio * 100).toFixed(0)}%`} />
                    <MetricCell label="近 24h" value={`${addressStats.recent_24h}`} />
                    <MetricCell label="近 7 天" value={`${addressStats.recent_7d}`} />
                    <MetricCell label="近 30 天" value={`${addressStats.recent_30d}`} />
                  </div>
                ),
              },
              {
                key: "evidence",
                label: "证据",
                children: (
                  <div className="flow-evidence">
                    <h4>证据边界说明</h4>
                    <ul>
                      <li>关系来自当前数据集图边聚合（聚焦从完整数据 BFS 计算），不代表全链事实。</li>
                      <li>图边金额与实时余额口径不同：前者为数据集内归因，后者为链上 RPC 查询。</li>
                      <li>“上游/下游”是相对当前中心地址的图关系，不构成实体身份结论。</li>
                      <li>交易所标记仅依据公开标签证据；无调证材料时不会标注“充值/归集”结论。</li>
                      <li>显示“已截断”时，统计仅代表可见关系，不代表完整数据集。</li>
                    </ul>
                    <p className="flow-evidence-note">当前中心：{directionLabel(direction)} · 数据集内 {node.upstream + node.downstream} 个直接相邻地址</p>
                  </div>
                ),
              },
            ]}
          />
        </section>
      </div>

      {/* 底部操作 */}
      <div className="flow-inspector-footer">
        <Button size="small" icon={<CameraOutlined />} onClick={onSaveSnapshot} loading={savingSnapshot} aria-label="保存余额快照">
          保存快照
        </Button>
        <Button size="small" icon={<ExperimentOutlined />} onClick={onInvestigate} loading={investigating} aria-label="加入 Investigation Agent">
          加入调查
        </Button>
        <Button
          size="small"
          icon={<ThunderboltOutlined />}
          onClick={onSmartFill}
          aria-label="智能数据补充"
          title="分析数据需求并自动选择数据源下载（历史交易 SQD / 实时余额 RPC / 标签 Browser）"
        >
          智能补充
        </Button>
        <Button size="small" type="primary" ghost onClick={onExitFocus} aria-label="退出聚焦">
          退出聚焦
        </Button>
      </div>
    </aside>
  );
}
