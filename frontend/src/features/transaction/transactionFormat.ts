import dayjs from "dayjs";

export function shortAddress(value: unknown, head = 8, tail = 6): string {
  const text = String(value ?? "").trim();
  return text.length > head + tail + 1 ? `${text.slice(0, head)}…${text.slice(-tail)}` : text || "—";
}

export function formatDateTime(value: unknown): string {
  const parsed = dayjs(String(value ?? ""));
  return parsed.isValid() && parsed.year() > 1970 ? parsed.format("YYYY-MM-DD HH:mm:ss") : "—";
}

export function formatNumber(value: unknown, maximumFractionDigits = 8): string {
  if (value === null || value === undefined || value === "") return "—";
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return String(value);
  if (parsed !== 0 && Math.abs(parsed) < 0.000001) {
    return parsed.toLocaleString("en-US", { maximumFractionDigits: 12, useGrouping: false });
  }
  return parsed.toLocaleString("en-US", { maximumFractionDigits });
}

export function logIndex(value: number | string): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : Number.MAX_SAFE_INTEGER;
}
