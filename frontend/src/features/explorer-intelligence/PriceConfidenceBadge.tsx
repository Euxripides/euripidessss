import { Tag } from "antd";

export function PriceConfidenceBadge({ value }: { value?: number }) {
  const confidence = Number(value ?? 0);
  const meta = confidence >= 0.85 ? ["高", "success"] : confidence >= 0.6 ? ["中", "processing"] : confidence > 0 ? ["低", "warning"] : ["未知", "default"];
  return <Tag color={meta[1]}>{meta[0]} · {(confidence * 100).toFixed(0)}%</Tag>;
}
