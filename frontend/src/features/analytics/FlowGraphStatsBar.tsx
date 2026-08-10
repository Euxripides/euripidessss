// V2.0 图谱统计栏（设计 §10/§11/§19）
//
// 底部常驻统计：节点/关系/图边流入流出净流量/交易/实体/余额完成度/完整性。
// 统计范围与截断声明必须可见（设计 §6.7）。

import { useEffect, useState } from "react";
import { fetchFlowStats, type FlowStats } from "./flowStatsApi";

export interface FlowGraphStatsBarProps {
  chain: string;
  chainId: number;
  token: string;
  visibleNodes: number;
  visibleEdges: number;
  truncated: boolean;
  balanceQueried: number;
  balanceTotal: number;
}

function fmtCompact(value: string | number): string {
  const n = typeof value === "string" ? Number.parseFloat(value) : value;
  if (!Number.isFinite(n)) return "0";
  if (Math.abs(n) >= 1e12) return `${(n / 1e12).toFixed(2)}T`;
  if (Math.abs(n) >= 1e9) return `${(n / 1e9).toFixed(2)}B`;
  if (Math.abs(n) >= 1e6) return `${(n / 1e6).toFixed(2)}M`;
  if (Math.abs(n) >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
  return n.toFixed(0);
}

export default function FlowGraphStatsBar(props: FlowGraphStatsBarProps) {
  const [stats, setStats] = useState<FlowStats | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let alive = true;
    if (props.visibleNodes === 0 && props.visibleEdges === 0) {
      setStats(null);
      setLoading(false);
      return () => {
        alive = false;
      };
    }
    setLoading(true);
    fetchFlowStats(props.chain, props.chainId, props.token)
      .then((next) => {
        if (alive) setStats(next);
      })
      .catch(() => {
        if (alive) setStats(null);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [props.chain, props.chainId, props.token, props.visibleEdges, props.visibleNodes]);

  const graph = stats?.graph;
  const flow = stats?.flow;
  const entities = stats?.entities;
  const balancePct = props.balanceTotal > 0 ? Math.round((props.balanceQueried / props.balanceTotal) * 100) : 0;
  const completeness = props.truncated ? "已截断" : (stats?.completeness?.complete ? "完整" : "部分");

  return (
    <div className="analytics-graph-statsbar">
      <span title="可见节点"><strong>{props.visibleNodes}</strong> 节点</span>
      <span title="可见关系"><strong>{props.visibleEdges}</strong> 关系</span>
      {loading ? <span className="muted">统计加载中…</span> : graph ? (
        <>
          <span title="全量交易"><strong>{graph.tx_count}</strong> 交易</span>
          <span title="图边总流入">
            流入 <strong>{fmtCompact(flow?.total_in ?? 0)}</strong>
          </span>
          <span title="图边总流出">
            流出 <strong>{fmtCompact(flow?.total_out ?? 0)}</strong>
          </span>
          <span title="图边净流量">
            净 <strong className={Number(flow?.net ?? 0) >= 0 ? "positive" : "negative"}>
              {Number(flow?.net ?? 0) >= 0 ? "+" : ""}{fmtCompact(flow?.net ?? 0)}
            </strong>
          </span>
          {entities ? (
            <>
              <span title="交易所地址"><strong>{entities.exchange}</strong> 交易所</span>
              <span title="高风险地址"><strong>{entities.risk}</strong> 高风险</span>
            </>
          ) : null}
        </>
      ) : null}
      <span title="实时余额查询完成度">
        余额 <strong>{props.balanceQueried}/{props.balanceTotal}</strong> ({balancePct}%)
      </span>
      <span title="数据完整性" className={props.truncated ? "truncated" : "complete"}>
        完整性 {completeness}
      </span>
      <span className="muted" title="统计范围">
        {props.chain.toUpperCase()} / {props.token || "全部 Token"}
      </span>
      {stats?.scope ? (
        <span className="muted">
          {stats.scope.start_date || "起始"} 至 {stats.scope.end_date || "最新"}
        </span>
      ) : null}
    </div>
  );
}
