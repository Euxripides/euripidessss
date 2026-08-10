import { Tag } from "antd";

export function LargeValueBadge({ value }: { value?: unknown }) {
  const amount = Number(value ?? 0);
  if (!Number.isFinite(amount) || amount < 100_000) return null;
  const level = amount >= 10_000_000 ? "10M+" : amount >= 1_000_000 ? "1M+" : amount >= 500_000 ? "500K+" : "100K+";
  return <Tag color={amount >= 1_000_000 ? "error" : "warning"}>{level}</Tag>;
}
