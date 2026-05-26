export type FlowNode = {
  id: string;
  label: string;
  role: string;
  account_no?: string;
  account_name?: string;
  id_number?: string;
  amount_in: number;
  amount_out: number;
  tx_count: number;
  in_count?: number;
  out_count?: number;
  degree?: number;
  first_time?: string | null;
  last_time?: string | null;
  tags: string[];
};

export type FlowEdge = {
  id: string;
  source: string;
  target: string;
  amount: number;
  tx_count: number;
  label: string;
  avg_amount?: number;
  max_amount?: number;
  first_time?: string | null;
  last_time?: string | null;
};

export type FlowGraph = {
  nodes: FlowNode[];
  edges: FlowEdge[];
  meta?: Record<string, unknown>;
};

export type QualityReport = {
  rows_in: number;
  rows_out: number;
  removed_empty_required: number;
  removed_failed_feedback: number;
  removed_bad_headers: number;
  removed_bad_direction: number;
  removed_duplicates: number;
  unmatched_account_rows: number;
  files: Array<{
    filename: string;
    provider: string;
    rows_in: number;
    rows_out: number;
    mapped_columns: Record<string, string>;
    missing_required: string[];
  }>;
  warnings: string[];
};

export type ProcessResponse = {
  job_id: string;
  rows: number;
  columns: string[];
  preview: Record<string, unknown>[];
  report: QualityReport;
  summary: Record<string, unknown>;
  flow_graph: FlowGraph;
  download_url: string;
};

export type RuleAnalysis = {
  provider: string;
  provider_label: string;
  candidates: Array<Record<string, unknown>>;
  suggestions: Array<Record<string, unknown>>;
};
