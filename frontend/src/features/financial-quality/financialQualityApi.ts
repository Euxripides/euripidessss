import { getJson } from "../../api/client";

export type FinancialQualityChain = "bsc" | "eth" | "base" | "arbitrum";
export type FinancialQualityWindow = "24H" | "7D" | "30D" | "90D" | "1Y" | "ALL";

export interface Coverage {
  numerator: number;
  denominator: number;
  percentage: number | null;
  unknown: number;
  available: boolean;
  scope: string;
}

export interface FinancialQualityReport {
  chain_id: number;
  window: {
    name: string;
    start: string | null;
    end: string | null;
  };
  price: {
    transfers_requiring_price: number;
    priced: number;
    historical_price: number;
    fallback_price: number;
    missing_price: number;
    coverage: Coverage;
    fallback_ratio: Coverage;
  };
  cost_basis: {
    position_events: number;
    known_cost_basis: number;
    unknown_cost_basis: number;
    coverage: Coverage;
    status: string;
    reason: string;
  };
  dex_decode: DecodeQuality;
  bridge_decode: DecodeQuality;
  entity: {
    counterparties: number;
    known_entity: number;
    unknown_entity: number;
    coverage: Coverage;
  };
  last_updated?: string;
  generated_at: string;
}

interface DecodeQuality {
  candidates: number;
  decoded: number;
  missing: number;
  coverage: Coverage;
}

interface ApiErrorPayload {
  detail?: string;
  message?: string;
}

export async function fetchFinancialQuality(
  chain: FinancialQualityChain,
  window: FinancialQualityWindow,
): Promise<FinancialQualityReport> {
  const result = await getJson<FinancialQualityReport | ApiErrorPayload>(
    `/api/v2/financial-quality/${encodeURIComponent(chain)}?window=${encodeURIComponent(window)}`,
    "金融质量读取失败",
  );
  if (!result.response.ok) {
    const error = result.payload as ApiErrorPayload;
    throw new Error(error.detail || error.message || `金融质量读取失败（HTTP ${result.response.status}）`);
  }
  if (!isFinancialQualityReport(result.payload)) {
    throw new Error("金融质量读取失败：后端响应不符合 financialquality.Report 契约");
  }
  return result.payload;
}

function isFinancialQualityReport(value: FinancialQualityReport | ApiErrorPayload): value is FinancialQualityReport {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<FinancialQualityReport>;
  return typeof candidate.chain_id === "number"
    && typeof candidate.generated_at === "string"
    && typeof candidate.price?.missing_price === "number"
    && typeof candidate.cost_basis?.unknown_cost_basis === "number"
    && typeof candidate.dex_decode?.missing === "number"
    && typeof candidate.bridge_decode?.missing === "number"
    && typeof candidate.entity?.unknown_entity === "number";
}

