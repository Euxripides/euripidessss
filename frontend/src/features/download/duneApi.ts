export type DunePerformance = 'free' | 'small' | 'medium' | 'large';

export type DuneCellValue = string | number | boolean | null;

export type DuneRow = Record<string, DuneCellValue>;

export type DuneQueryValues = {
  readonly sql: string;
  readonly apiKey?: string;
  readonly queryId?: number;
  readonly webQuery?: boolean;
  readonly teamId?: number;
  readonly datasetId?: number;
  readonly queryVersion?: number;
  readonly performance?: DunePerformance;
  readonly timeoutSeconds?: number;
  readonly pollIntervalSeconds?: number;
  readonly allowPartialResults?: boolean;
  readonly limit?: number;
};

export type DunePageValues = {
  readonly executionId: string;
  readonly apiKey?: string;
  readonly queryId?: number;
  readonly offset: number;
  readonly limit: number;
  readonly allowPartialResults?: boolean;
};

export type DuneExportValues = DunePageValues & {
  readonly scope: 'page' | 'all';
};

export type DuneAuthStatus = {
  readonly hasApiKey: boolean;
  readonly hasCookie: boolean;
  readonly hasWebAuth: boolean;
  readonly source: string;
  readonly loginUrl: string;
};

export type DuneQueryResponse = {
  readonly executionId: string;
  readonly queryId: number;
  readonly state: string;
  readonly columns: readonly string[];
  readonly columnLabels: Record<string, string>;
  readonly columnTypes: readonly string[];
  readonly rows: readonly DuneRow[];
  readonly rowCount: number;
  readonly totalRowCount: number;
  readonly nextOffset: number | null;
  readonly nextUri: string;
};

export class DuneAuthRequiredError extends Error {
  readonly loginUrl: string;

  constructor(message: string, loginUrl: string) {
    super(message);
    this.name = 'DuneAuthRequiredError';
    this.loginUrl = loginUrl;
  }
}

export async function loadDuneAuthStatus(): Promise<DuneAuthStatus> {
  const payload = await requestJson('/api/dune/auth', { method: 'GET' });
  return parseDuneAuthStatus(payload);
}

export async function saveDuneAuth(values: { readonly apiKey?: string; readonly cookie?: string; readonly authorization?: string; readonly accessToken?: string }): Promise<void> {
  await requestJson('/api/dune/auth', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ api_key: values.apiKey ?? '', cookie: values.cookie ?? '', authorization: values.authorization ?? '', access_token: values.accessToken ?? '' }),
  });
}

export async function queryDuneSQL(values: DuneQueryValues): Promise<DuneQueryResponse> {
  const payload = await requestJson('/api/dune/query', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      sql: values.sql,
      api_key: values.apiKey,
      query_id: values.queryId ?? 0,
      web_query: values.webQuery ?? false,
      team_id: values.teamId ?? 0,
      dataset_id: values.datasetId ?? 0,
      query_version: values.queryVersion ?? 0,
      performance: values.performance ?? 'medium',
      timeout_seconds: values.timeoutSeconds ?? 900,
      poll_interval_seconds: values.pollIntervalSeconds ?? 2,
      allow_partial_results: values.allowPartialResults ?? true,
      limit: values.limit ?? 100,
    }),
  });
  return parseDuneQueryResponse(payload);
}

export async function loadDunePage(values: DunePageValues): Promise<DuneQueryResponse> {
  const payload = await requestJson('/api/dune/results', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      execution_id: values.executionId,
      api_key: values.apiKey,
      query_id: values.queryId ?? 0,
      offset: values.offset,
      limit: values.limit,
      allow_partial_results: values.allowPartialResults ?? true,
    }),
  });
  return parseDuneQueryResponse(payload);
}

export async function exportDuneExcel(values: DuneExportValues): Promise<string> {
  const response = await fetch('/api/dune/export', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      execution_id: values.executionId,
      api_key: values.apiKey,
      query_id: values.queryId ?? 0,
      scope: values.scope,
      offset: values.offset,
      limit: values.limit,
      allow_partial_results: values.allowPartialResults ?? true,
    }),
  });
  if (!response.ok) {
    throw await buildDuneError(response);
  }
  const blob = await response.blob();
  const filename = parseDownloadFilename(response.headers.get('Content-Disposition')) || `dune_${Date.now()}.xlsx`;
  saveBlob(blob, filename);
  return filename;
}

async function requestJson(url: string, init: RequestInit): Promise<unknown> {
  const response = await fetch(url, init);
  if (!response.ok) {
    throw await buildDuneError(response);
  }
  return response.json();
}

async function buildDuneError(response: Response): Promise<Error> {
  const text = await response.text();
  let detail = `Dune 请求失败（HTTP ${response.status}）`;
  let loginUrl = 'https://dune.com/settings/api';
  if (text) {
    try {
      const payload: unknown = JSON.parse(text);
      if (isRecord(payload)) {
        if (typeof payload.detail === 'string') detail = payload.detail;
        if (typeof payload.login_url === 'string') loginUrl = payload.login_url;
        if (payload.auth_required === true) return new DuneAuthRequiredError(detail, loginUrl);
      }
    } catch (error) {
      if (error instanceof SyntaxError) detail = text;
      else throw error;
    }
  }
  return new Error(detail);
}

function parseDuneAuthStatus(value: unknown): DuneAuthStatus {
  if (!isRecord(value)) return { hasApiKey: false, hasCookie: false, hasWebAuth: false, source: 'missing', loginUrl: 'https://dune.com/settings/api' };
  return {
    hasApiKey: value.has_api_key === true,
    hasCookie: value.has_cookie === true,
    hasWebAuth: value.has_web_auth === true,
    source: typeof value.source === 'string' ? value.source : 'missing',
    loginUrl: typeof value.login_url === 'string' ? value.login_url : 'https://dune.com/settings/api',
  };
}

function parseDuneQueryResponse(value: unknown): DuneQueryResponse {
  if (!isRecord(value)) throw new Error('Dune 响应格式不正确');
  const rows = Array.isArray(value.rows) ? value.rows.filter(isRecord).map(parseDuneRow) : [];
  return {
    executionId: stringField(value, 'execution_id'),
    queryId: numberField(value, 'query_id'),
    state: stringField(value, 'state'),
    columns: stringArrayField(value, 'columns'),
    columnLabels: stringMapField(value, 'column_labels'),
    columnTypes: stringArrayField(value, 'column_types'),
    rows,
    rowCount: numberField(value, 'row_count'),
    totalRowCount: numberField(value, 'total_row_count'),
    nextOffset: typeof value.next_offset === 'number' ? value.next_offset : null,
    nextUri: stringField(value, 'next_uri'),
  };
}

function parseDuneRow(value: Record<string, unknown>): DuneRow {
  const row: DuneRow = {};
  for (const [key, cell] of Object.entries(value)) {
    if (typeof cell === 'string' || typeof cell === 'number' || typeof cell === 'boolean' || cell === null) {
      row[key] = cell;
    } else {
      row[key] = JSON.stringify(cell);
    }
  }
  return row;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function stringField(value: Record<string, unknown>, key: string): string {
  return typeof value[key] === 'string' ? value[key] : '';
}

function numberField(value: Record<string, unknown>, key: string): number {
  return typeof value[key] === 'number' ? value[key] : 0;
}

function stringArrayField(value: Record<string, unknown>, key: string): readonly string[] {
  return Array.isArray(value[key]) ? value[key].filter((item): item is string => typeof item === 'string') : [];
}

function stringMapField(value: Record<string, unknown>, key: string): Record<string, string> {
  if (!isRecord(value[key])) return {};
  const result: Record<string, string> = {};
  for (const [name, label] of Object.entries(value[key])) {
    if (typeof label === 'string') result[name] = label;
  }
  return result;
}

function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

function parseDownloadFilename(header: string | null): string {
  if (!header) return '';
  const utf8 = header.match(/filename\*=UTF-8''([^;]+)/i);
  if (utf8?.[1]) return decodeURIComponent(utf8[1]);
  const ascii = header.match(/filename="?([^";]+)"?/i);
  return ascii?.[1] ?? '';
}
