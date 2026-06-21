type AccountStatus = 'pending' | 'registering' | 'verifying' | 'logging_in' | 'captcha' | 'done' | 'failed';

export type DuneBatchAccount = {
  readonly email: string;
  readonly username: string;
  readonly password: string;
  readonly status: AccountStatus;
  readonly error?: string;
  readonly teamId?: number;
};

export type DuneBatchTask = {
  readonly id: string;
  readonly total: number;
  readonly completed: number;
  readonly failed: number;
  readonly status: 'idle' | 'running' | 'stopped' | 'done';
  readonly accounts: readonly DuneBatchAccount[];
};

export type DuneBatchStartValues = {
  readonly total: number;
  readonly domain: string;
  readonly intervalSeconds: number;
  readonly imapHost: string;
  readonly imapUser: string;
  readonly imapPassword: string;
};

export async function startDuneBatch(values: DuneBatchStartValues): Promise<DuneBatchTask> {
  const payload = await requestJson('/api/dune/batch/start', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      total: values.total,
      domain: values.domain,
      interval_seconds: values.intervalSeconds,
      imap_host: values.imapHost,
      imap_user: values.imapUser,
      imap_password: values.imapPassword,
    }),
  });
  return parseTask(payload);
}

export async function stopDuneBatch(): Promise<DuneBatchTask> {
  const payload = await requestJson('/api/dune/batch/stop', { method: 'POST' });
  return parseTask(payload);
}

export async function loadDuneBatchStatus(): Promise<DuneBatchTask> {
  const payload = await requestJson('/api/dune/batch/status', { method: 'GET' });
  return parseTask(payload);
}

export async function exportDuneBatchCSV(): Promise<string> {
  const response = await fetch('/api/dune/batch/export');
  if (!response.ok) throw await buildError(response);
  const blob = await response.blob();
  const filename = parseDownloadFilename(response.headers.get('Content-Disposition')) || `dune_accounts_${Date.now()}.csv`;
  saveBlob(blob, filename);
  return filename;
}

async function requestJson(url: string, init: RequestInit): Promise<unknown> {
  const response = await fetch(url, init);
  if (!response.ok) throw await buildError(response);
  return response.json();
}

async function buildError(response: Response): Promise<Error> {
  const text = await response.text();
  if (text) {
    try {
      const payload: unknown = JSON.parse(text);
      if (isRecord(payload) && typeof payload.detail === 'string') return new Error(payload.detail);
    } catch (error) {
      if (error instanceof SyntaxError) return new Error(text);
      throw error;
    }
  }
  return new Error(`Dune 批量注册请求失败（HTTP ${response.status}）`);
}

function parseTask(value: unknown): DuneBatchTask {
  if (!isRecord(value)) throw new Error('Dune 批量注册响应格式不正确');
  return {
    id: stringField(value, 'id'),
    total: numberField(value, 'total'),
    completed: numberField(value, 'completed'),
    failed: numberField(value, 'failed'),
    status: taskStatus(value.status),
    accounts: Array.isArray(value.accounts) ? value.accounts.filter(isRecord).map(parseAccount) : [],
  };
}

function parseAccount(value: Record<string, unknown>): DuneBatchAccount {
  return {
    email: stringField(value, 'email'),
    username: stringField(value, 'username'),
    password: stringField(value, 'password'),
    status: accountStatus(value.status),
    error: typeof value.error === 'string' ? value.error : undefined,
    teamId: typeof value.team_id === 'number' ? value.team_id : undefined,
  };
}

function taskStatus(value: unknown): DuneBatchTask['status'] {
  switch (value) {
    case 'running':
    case 'stopped':
    case 'done':
      return value;
    default:
      return 'idle';
  }
}

function accountStatus(value: unknown): AccountStatus {
  switch (value) {
    case 'registering':
    case 'verifying':
    case 'logging_in':
    case 'captcha':
    case 'done':
    case 'failed':
      return value;
    default:
      return 'pending';
  }
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
