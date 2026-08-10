import { Descriptions } from "antd";
import { HistoricalPrice } from "./HistoricalPrice";
import { HistoricalValue } from "./HistoricalValue";
import { PriceConfidenceBadge } from "./PriceConfidenceBadge";

export interface PriceEvidence {
  price?: unknown;
  value?: unknown;
  timestamp?: unknown;
  source?: unknown;
  route?: unknown;
  type?: unknown;
  confidence?: number;
  ageSeconds?: number;
  symbol?: unknown;
}

function time(value: unknown): string {
  if (!value) return "—";
  const parsed = new Date(String(value));
  return Number.isNaN(parsed.getTime()) ? "—" : parsed.toLocaleString("zh-CN", { hour12: false, timeZone: "UTC" }) + " UTC";
}

export function PriceTooltip({ evidence }: { evidence: PriceEvidence }) {
  return <Descriptions className="xi-price-evidence" size="small" column={1} items={[
    { key: "price", label: "当时价格", children: <HistoricalPrice value={evidence.price} symbol={evidence.symbol} /> },
    { key: "value", label: "当时价值", children: <HistoricalValue value={evidence.value} approximate={false} /> },
    { key: "time", label: "价格分钟", children: time(evidence.timestamp) },
    { key: "source", label: "来源", children: String(evidence.source || "UNKNOWN") },
    { key: "route", label: "路径", children: String(evidence.route || "—") },
    { key: "type", label: "价格类型", children: String(evidence.type || "UNKNOWN") },
    { key: "age", label: "价格年龄", children: `${Number(evidence.ageSeconds || 0)} 秒` },
    { key: "confidence", label: "置信度", children: <PriceConfidenceBadge value={evidence.confidence} /> },
  ]} />;
}
