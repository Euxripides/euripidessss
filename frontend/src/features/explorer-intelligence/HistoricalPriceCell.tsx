import { Button, Popover, Tooltip } from "antd";
import { HistoricalValue } from "./HistoricalValue";
import { LargeValueBadge } from "./LargeValueBadge";
import { PriceTooltip } from "./PriceTooltip";

export interface HistoricalPriceCellProps {
  priceUSD?: unknown;
  usdValue?: unknown;
  priceTime?: unknown;
  source?: unknown;
  confidence?: unknown;
  symbol?: unknown;
  route?: unknown;
  priceType?: unknown;
  ageSeconds?: number;
  valuationStatus?: string;
}

function money(value: unknown, maximumFractionDigits = 8): string {
  if (value === null || value === undefined || value === "") return "--";
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return "--";
  if (parsed !== 0 && Math.abs(parsed) < 10 ** -maximumFractionDigits) return `$${parsed.toExponential(6)}`;
  return parsed.toLocaleString("en-US", { style: "currency", currency: "USD", maximumFractionDigits });
}

export function HistoricalPriceCell({ priceUSD, usdValue, priceTime, source, confidence, symbol, route, priceType, ageSeconds, valuationStatus }: HistoricalPriceCellProps) {
  if (valuationStatus === "BACKFILLING") return <span className="xi-price-missing">价格回填中</span>;
  if (priceUSD === null || priceUSD === undefined || priceUSD === "") {
    return <Tooltip title="暂无该时间点历史价格"><span className="xi-price-missing">—</span></Tooltip>;
  }
  const priceLabel = money(priceUSD, 12);
  const content = <PriceTooltip evidence={{ price: priceUSD, value: usdValue, timestamp: priceTime, source, route, type: priceType, confidence: Number(confidence || 0), ageSeconds, symbol }} />;
  return (
    <Popover content={content} title="历史价格证据" trigger="click" placement="bottomRight">
      <Button type="text" className="xi-price-cell" onClick={(event) => event.stopPropagation()}>
        <span><HistoricalValue value={usdValue} /><LargeValueBadge value={usdValue} /></span>
        <small>{priceLabel} / {String(symbol || "Token")}</small>
      </Button>
    </Popover>
  );
}
