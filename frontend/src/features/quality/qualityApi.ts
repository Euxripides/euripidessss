import { getJson } from "../../api/client";

export type DataQualityChain = "bsc" | "eth" | "base" | "arbitrum";
export type QualitySectionKey = "datasets" | "tokens" | "contracts" | "decoders" | "prices";

export interface Coverage {
  numerator: number;
  denominator: number;
  percentage: number;
  unknown: number;
  available: boolean;
  last_updated?: string;
}

export interface SemanticCompleteness {
  chain_id: number;
  overall: Coverage;
  transaction_status: Coverage;
  transaction_method: Coverage;
  token_metadata: Coverage;
  token_decimals: Coverage;
  token_logo: Coverage;
  contract_creator: Coverage;
  contract_abi: Coverage;
  entity_label: Coverage;
  event_decode: Coverage;
  historical_price: Coverage;
  generated_at: string;
}

export interface DatasetQuality {
  dataset: string;
  rows: number;
  last_updated?: string;
}

export interface DataQualityReport {
  chain_id: number;
  semantic_completeness: SemanticCompleteness;
  data_quality: {
    chain_id: number;
    total_rows: number;
    datasets: readonly DatasetQuality[];
    status: Coverage;
    method: Coverage;
    entity_label: Coverage;
    generated_at: string;
  };
  token_quality: {
    chain_id: number;
    known_tokens: number;
    verified: number;
    unverified: number;
    spam_tokens: number;
    missing_symbol: number;
    missing_decimals: number;
    missing_logo: number;
    metadata_coverage: Coverage;
    decimals_coverage: Coverage;
    logo_coverage: Coverage;
    last_updated?: string;
  };
  contract_quality: {
    chain_id: number;
    contracts: number;
    creator_coverage: Coverage;
    creation_tx_coverage: Coverage;
    proxy_detected: number;
    implementation_known: number;
    abi_coverage: Coverage;
    verified: number;
    token_detected: number;
    last_updated?: string;
  };
  decoder_quality: {
    chain_id: number;
    transactions_with_input: number;
    known_method: number;
    unknown_method: number;
    indexed_events: number;
    decoded_events: number;
    unknown_topic0: number;
    abi_decode_failures: number;
    method_coverage: Coverage;
    event_coverage: Coverage;
    scope: string;
    last_updated?: string;
  };
  price_quality: {
    chain_id: number;
    transfers_requiring_price: number;
    priced: number;
    historical_price: number;
    fallback_price: number;
    no_price: number;
    price_coverage: Coverage;
    historical_price_coverage: Coverage;
    price_provenance_available: boolean;
    last_updated?: string;
  };
  generated_at: string;
}

export interface QualityMetrics {
  rows: number;
  coverage: number | null;
  completeness: number | null;
  invalid: number;
  unknown: number;
  unpriced: number;
  unlabeled: number;
  decode_failed: number;
  last_updated: string;
}

export interface QualityItem extends QualityMetrics {
  key: string;
  name: string;
  description?: string;
}

export interface QualitySection extends QualityMetrics {
  items: readonly QualityItem[];
}

export interface DataQualitySections {
  datasets: QualitySection;
  tokens: QualitySection;
  contracts: QualitySection;
  decoders: QualitySection;
  prices: QualitySection;
}

/** View model consumed by the page; raw semantic-quality evidence remains available. */
export interface DataQualityResponse {
  chain_id: number;
  chain: string;
  generated_at: string;
  sections: DataQualitySections;
  report: DataQualityReport;
}

interface ApiErrorPayload {
  detail?: string;
  message?: string;
}

export async function fetchDataQuality(chain: DataQualityChain): Promise<DataQualityResponse> {
  const result = await getJson<DataQualityReport | ApiErrorPayload>(
    `/api/v2/data-quality/${encodeURIComponent(chain)}`,
    "数据质量读取失败",
  );
  if (!result.response.ok) {
    const error = result.payload as ApiErrorPayload;
    throw new Error(error.detail || error.message || `数据质量读取失败（HTTP ${result.response.status}）`);
  }
  if (!isDataQualityReport(result.payload)) {
    throw new Error("数据质量读取失败：后端响应不符合 semanticquality.Report 契约");
  }
  return toViewModel(result.payload, chain);
}

function isDataQualityReport(value: DataQualityReport | ApiErrorPayload): value is DataQualityReport {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<DataQualityReport>;
  return typeof candidate.chain_id === "number"
    && typeof candidate.generated_at === "string"
    && Boolean(candidate.semantic_completeness?.overall)
    && typeof candidate.data_quality?.total_rows === "number"
    && Array.isArray(candidate.data_quality.datasets)
    && typeof candidate.token_quality?.known_tokens === "number"
    && typeof candidate.contract_quality?.contracts === "number"
    && typeof candidate.decoder_quality?.transactions_with_input === "number"
    && typeof candidate.price_quality?.transfers_requiring_price === "number";
}

function toViewModel(report: DataQualityReport, chain: DataQualityChain): DataQualityResponse {
  const data = report.data_quality;
  const token = report.token_quality;
  const contract = report.contract_quality;
  const decoder = report.decoder_quality;
  const price = report.price_quality;
  const semantic = report.semantic_completeness;

  return {
    chain_id: report.chain_id,
    chain,
    generated_at: report.generated_at,
    report,
    sections: {
      datasets: {
        ...emptyMetrics(),
        rows: data.total_rows,
        coverage: availablePercentage(semantic.overall),
        completeness: availablePercentage(semantic.overall),
        invalid: data.status.unknown,
        unknown: data.method.unknown + data.entity_label.unknown,
        unlabeled: data.entity_label.unknown,
        last_updated: data.generated_at,
        items: data.datasets.map((dataset) => ({
          ...emptyMetrics(),
          key: dataset.dataset,
          name: dataset.dataset,
          rows: dataset.rows,
          invalid: 0,
          last_updated: dataset.last_updated || data.generated_at,
        })),
      },
      tokens: {
        ...emptyMetrics(),
        rows: token.known_tokens,
        coverage: availablePercentage(token.metadata_coverage),
        completeness: averageAvailable([token.metadata_coverage, token.decimals_coverage, token.logo_coverage]),
        invalid: token.spam_tokens + token.unverified,
        unknown: token.missing_symbol + token.missing_decimals + token.missing_logo,
        last_updated: token.last_updated || report.generated_at,
        items: [
          coverageItem("token-metadata", "Token metadata", token.metadata_coverage),
          coverageItem("token-decimals", "Token decimals", token.decimals_coverage),
          coverageItem("token-logo", "Token logo", token.logo_coverage),
        ],
      },
      contracts: {
        ...emptyMetrics(),
        rows: contract.contracts,
        coverage: availablePercentage(contract.creator_coverage),
        completeness: averageAvailable([contract.creator_coverage, contract.creation_tx_coverage, contract.abi_coverage]),
        unknown: contract.creator_coverage.unknown + contract.creation_tx_coverage.unknown + contract.abi_coverage.unknown,
        unlabeled: semantic.entity_label.unknown,
        last_updated: contract.last_updated || report.generated_at,
        items: [
          coverageItem("contract-creator", "Contract creator", contract.creator_coverage),
          coverageItem("contract-creation-tx", "Creation transaction", contract.creation_tx_coverage),
          coverageItem("contract-abi", "Contract ABI", contract.abi_coverage),
        ],
      },
      decoders: {
        ...emptyMetrics(),
        rows: decoder.transactions_with_input + decoder.indexed_events,
        coverage: availablePercentage(decoder.event_coverage),
        completeness: averageAvailable([decoder.method_coverage, decoder.event_coverage]),
        unknown: decoder.unknown_method + decoder.unknown_topic0,
        decode_failed: decoder.abi_decode_failures,
        last_updated: decoder.last_updated || report.generated_at,
        items: [
          coverageItem("method-decode", "Method decode", decoder.method_coverage, "transaction_input"),
          coverageItem("event-decode", "Event decode", decoder.event_coverage, decoder.scope),
        ],
      },
      prices: {
        ...emptyMetrics(),
        rows: price.transfers_requiring_price,
        coverage: availablePercentage(price.price_coverage),
        completeness: availablePercentage(price.historical_price_coverage),
        unknown: price.historical_price_coverage.unknown,
        unpriced: price.no_price,
        last_updated: price.last_updated || report.generated_at,
        items: [
          coverageItem("price", "Price coverage", price.price_coverage),
          coverageItem(
            "historical-price",
            "Historical price provenance",
            price.historical_price_coverage,
            price.price_provenance_available ? "来源证据可用" : "来源证据不可用",
          ),
        ],
      },
    },
  };
}

function emptyMetrics(): QualityMetrics {
  return {
    rows: 0,
    coverage: null,
    completeness: null,
    invalid: 0,
    unknown: 0,
    unpriced: 0,
    unlabeled: 0,
    decode_failed: 0,
    last_updated: "",
  };
}

function coverageItem(key: string, name: string, coverage: Coverage, description?: string): QualityItem {
  return {
    ...emptyMetrics(),
    key,
    name,
    description: coverage.available ? description : `${description ? `${description}；` : ""}指标当前不可用`,
    rows: coverage.denominator,
    coverage: availablePercentage(coverage),
    completeness: availablePercentage(coverage),
    unknown: coverage.unknown,
    last_updated: coverage.last_updated || "",
  };
}

function availablePercentage(coverage: Coverage): number | null {
  return coverage.available ? coverage.percentage : null;
}

function averageAvailable(coverages: readonly Coverage[]): number | null {
  const available = coverages.filter((coverage) => coverage.available);
  if (available.length === 0) return null;
  return available.reduce((total, coverage) => total + coverage.percentage, 0) / available.length;
}
