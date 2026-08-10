import { Tooltip } from "antd";

export function formatHistoricalValue(value: unknown, compact = true): string {
  if (value === null || value === undefined || value === "") return "—";
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return "—";
  const options: Intl.NumberFormatOptions = compact && Math.abs(parsed) >= 1000
    ? { notation: "compact", maximumFractionDigits: 2 }
    : { maximumFractionDigits: 2 };
  return `${parsed.toLocaleString("en-US", options)} USDT`;
}

export function HistoricalValue({ value, approximate = true }: { value?: unknown; approximate?: boolean }) {
  const compact = formatHistoricalValue(value);
  if (compact === "—") return <span className="xi-price-missing">—</span>;
  const full = `${Number(value).toLocaleString("en-US", { maximumFractionDigits: 18 })} USDT`;
  return <Tooltip title={full}><strong>{approximate ? "≈ " : ""}{compact}</strong></Tooltip>;
}
