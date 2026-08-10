export function formatHistoricalPrice(value: unknown, maximumFractionDigits = 12): string {
  if (value === null || value === undefined || value === "") return "—";
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return "—";
  if (parsed !== 0 && Math.abs(parsed) < 10 ** -maximumFractionDigits) return parsed.toExponential(6);
  return parsed.toLocaleString("en-US", { maximumFractionDigits });
}

export function HistoricalPrice({ value, symbol }: { value?: unknown; symbol?: unknown }) {
  const label = formatHistoricalPrice(value);
  return <span>{label === "—" ? label : `${label} USDT/${String(symbol || "Token")}`}</span>;
}
