const SETTINGS_ACTION_HEADER = "X-System-Settings-Action";
const SETTINGS_ACTION_VALUE = "local-console";
const EDITABLE_SETTING_KEYS = new Set([
  "concurrency_level",
  "max_file_size_mb",
  "analytics_data_source",
  "clickhouse_enabled",
  "clickhouse_required",
  "price_engine_enabled",
  "log_retention_days",
  "output_retention_days",
  "backup_retention_count",
]);

export type SafeScalar = string | number | boolean | null;
export interface SafeRecord {
  [key: string]: SafeScalar | SafeRecord | readonly SafeScalar[];
}

export type SystemComponentStatus = {
  readonly key: string;
  readonly name: string;
  readonly status: string;
  readonly detail?: string;
};

export type SystemStorageSnapshot = {
  readonly pathHint?: string;
  readonly usedBytes?: number;
  readonly freeBytes?: number;
  readonly totalBytes?: number;
  readonly reclaimableBytes?: number;
  readonly fileCount?: number;
};

export type SystemBackup = {
  readonly id: string;
  readonly createdAt?: string;
  readonly sizeBytes?: number;
  readonly status?: string;
  readonly description?: string;
};

export type SystemAuditEntry = {
  readonly id: string;
  readonly action: string;
  readonly status?: string;
  readonly createdAt?: string;
  readonly actor?: string;
  readonly summary?: string;
};

export type SystemSettingsSnapshot = {
  readonly settings: SafeRecord;
  readonly effective: SafeRecord;
  readonly pendingRestart: boolean;
  readonly pendingRestartKeys: readonly string[];
  readonly runtime: SafeRecord;
  readonly components: readonly SystemComponentStatus[];
  readonly storage?: SystemStorageSnapshot;
  readonly backups: readonly SystemBackup[];
  readonly audit: readonly SystemAuditEntry[];
  readonly capabilities: SafeRecord;
};

export type CleanupPreview = {
  readonly previewId?: string;
  readonly fileCount?: number;
  readonly reclaimableBytes?: number;
  readonly categories: readonly string[];
  readonly expiresAt?: string;
  readonly warnings: readonly string[];
};

export type SettingsPatch = Record<string, SafeScalar>;

export const systemSettingsApi = {
  async get(): Promise<SystemSettingsSnapshot> {
    const payload = await requestJson("/api/system/settings", { method: "GET" });
    return normalizeSettingsSnapshot(payload);
  },

  async patch(patch: SettingsPatch): Promise<SystemSettingsSnapshot> {
    const sanitizedPatch = sanitizeSettingsPatch(patch);
    if (!Object.keys(sanitizedPatch).length) throw new Error("没有可提交的白名单设置");
    const payload = await requestJson("/api/system/settings", {
      method: "PATCH",
      headers: actionHeaders(),
      body: JSON.stringify({ settings: sanitizedPatch }),
    });
    return normalizeSettingsSnapshot(payload);
  },

  async listBackups(): Promise<readonly SystemBackup[]> {
    const payload = await requestJson("/api/system/settings/backups", { method: "GET" });
    return normalizeBackups(readEnvelope(payload).backups ?? readEnvelope(payload).items ?? payload);
  },

  async createBackup(description?: string): Promise<SystemBackup> {
    const payload = await requestJson("/api/system/settings/backups", {
      method: "POST",
      headers: actionHeaders(),
      body: JSON.stringify({ description: normalizeDescription(description) }),
    });
    const record = readEnvelope(payload).backup ?? readEnvelope(payload);
    return normalizeBackup(record, 0);
  },

  async restoreBackup(backupId: string, confirmation: string): Promise<SystemSettingsSnapshot> {
    const id = encodeURIComponent(requireSafeIdentifier(backupId));
    const payload = await requestJson(`/api/system/settings/backups/${id}/restore`, {
      method: "POST",
      headers: actionHeaders(),
      body: JSON.stringify({ confirmation, confirm_phrase: confirmation }),
    });
    return normalizeSettingsSnapshot(payload);
  },

  async previewCleanup(input: { readonly categories: readonly string[]; readonly olderThanDays: number }): Promise<CleanupPreview> {
    const payload = await requestJson("/api/system/settings/cleanup/preview", {
      method: "POST",
      headers: actionHeaders(),
      body: JSON.stringify({ categories: normalizeCategories(input.categories), older_than_days: clampInteger(input.olderThanDays, 1, 3650) }),
    });
    return normalizeCleanupPreview(payload);
  },

  async executeCleanup(input: { readonly previewId?: string; readonly categories: readonly string[]; readonly olderThanDays: number; readonly confirmation: string }): Promise<SystemSettingsSnapshot> {
    const payload = await requestJson("/api/system/settings/cleanup/execute", {
      method: "POST",
      headers: actionHeaders(),
      body: JSON.stringify({
        preview_id: normalizeIdentifier(input.previewId),
        categories: normalizeCategories(input.categories),
        older_than_days: clampInteger(input.olderThanDays, 1, 3650),
        confirmation: input.confirmation,
        confirm_phrase: input.confirmation,
      }),
    });
    return normalizeSettingsSnapshot(payload);
  },
};

function actionHeaders(): HeadersInit {
  return { "Content-Type": "application/json", [SETTINGS_ACTION_HEADER]: SETTINGS_ACTION_VALUE };
}

async function requestJson(url: string, init: RequestInit): Promise<unknown> {
  const response = await fetch(url, { ...init, cache: "no-store", headers: { Accept: "application/json", ...(init.headers ?? {}) } });
  const text = await response.text();
  let payload: unknown = {};
  if (text) {
    try {
      payload = JSON.parse(text) as unknown;
    } catch {
      throw new Error("后端返回内容无法解析");
    }
  }
  if (!response.ok) throw new Error(readError(payload) || `请求失败（HTTP ${response.status}）`);
  return payload;
}

function normalizeSettingsSnapshot(payload: unknown): SystemSettingsSnapshot {
  const root = readEnvelope(payload);
  const pending = root.pending_restart;
  const pendingKeys = (Array.isArray(pending)
    ? pending.filter((item): item is string => typeof item === "string").slice(0, 50)
    : readStringArray(root.pending_restart_keys)).filter((key) => EDITABLE_SETTING_KEYS.has(key));
  return {
    settings: sanitizeRecord(root.settings ?? root.sanitized_settings ?? root.sanitized),
    effective: sanitizeRecord(root.effective ?? root.effective_settings),
    pendingRestart: typeof pending === "boolean" ? pending : pendingKeys.length > 0,
    pendingRestartKeys: pendingKeys,
    runtime: sanitizeRecord(root.runtime),
    components: normalizeComponents(root.components),
    storage: normalizeStorage(root.storage),
    backups: normalizeBackups(root.backups),
    audit: normalizeAudit(root.audit),
    capabilities: sanitizeRecord(root.capabilities),
  };
}

function normalizeComponents(value: unknown): readonly SystemComponentStatus[] {
  if (Array.isArray(value)) {
    return value.slice(0, 100).map((item, index) => normalizeComponent(item, `component-${index + 1}`));
  }
  if (!isRecord(value)) return [];
  return Object.entries(value).slice(0, 100).map(([key, item]) => normalizeComponent(item, key));
}

function normalizeComponent(value: unknown, fallbackKey: string): SystemComponentStatus {
  const item = isRecord(value) ? value : {};
  return {
    key: readString(item.key) ?? fallbackKey,
    name: readString(item.name) ?? readString(item.label) ?? fallbackKey,
    status: readString(item.status) ?? readString(item.state) ?? "unknown",
    detail: sanitizeText(readString(item.detail) ?? readString(item.message)),
  };
}

function normalizeStorage(value: unknown): SystemStorageSnapshot | undefined {
  if (!isRecord(value)) return undefined;
  return {
    pathHint: readString(value.path_hint),
    usedBytes: readFiniteNumber(value.used_bytes),
    freeBytes: readFiniteNumber(value.free_bytes),
    totalBytes: readFiniteNumber(value.total_bytes),
    reclaimableBytes: readFiniteNumber(value.reclaimable_bytes),
    fileCount: readFiniteNumber(value.file_count),
  };
}

function normalizeBackups(value: unknown): readonly SystemBackup[] {
  const source = Array.isArray(value) ? value : isRecord(value) && Array.isArray(value.items) ? value.items : [];
  return source.slice(0, 200).map(normalizeBackup).filter((item) => item.id !== "");
}

function normalizeBackup(value: unknown, index: number): SystemBackup {
  const item = isRecord(value) ? value : {};
  return {
    id: normalizeIdentifier(readString(item.id) ?? readString(item.backup_id)) ?? "",
    createdAt: readString(item.created_at),
    sizeBytes: readFiniteNumber(item.size_bytes),
    status: readString(item.status),
    description: sanitizeText(readString(item.description) ?? readString(item.summary)),
  };
}

function normalizeAudit(value: unknown): readonly SystemAuditEntry[] {
  const source = Array.isArray(value) ? value : isRecord(value) && Array.isArray(value.items) ? value.items : [];
  return source.slice(0, 200).map((entry, index) => {
    const item = isRecord(entry) ? entry : {};
    return {
      id: readString(item.id) ?? `audit-${index + 1}`,
      action: readString(item.action) ?? readString(item.event) ?? "未知操作",
      status: readString(item.status),
      createdAt: readString(item.created_at),
      actor: readString(item.actor),
      summary: sanitizeText(readString(item.summary) ?? readString(item.detail)),
    };
  });
}

function normalizeCleanupPreview(payload: unknown): CleanupPreview {
  const root = readEnvelope(payload);
  const preview = isRecord(root.preview) ? root.preview : root;
  return {
    previewId: normalizeIdentifier(readString(preview.preview_id) ?? readString(preview.id)),
    fileCount: readFiniteNumber(preview.file_count),
    reclaimableBytes: readFiniteNumber(preview.reclaimable_bytes),
    categories: readStringArray(preview.categories),
    expiresAt: readString(preview.expires_at),
    warnings: readStringArray(preview.warnings),
  };
}

function sanitizeRecord(value: unknown, depth = 0): SafeRecord {
  if (!isRecord(value) || depth > 4) return {};
  const result: SafeRecord = {};
  for (const [key, raw] of Object.entries(value).slice(0, 200)) {
    if (!isSafeDisplayKey(key)) continue;
    if (raw === null || typeof raw === "string" || typeof raw === "boolean" || (typeof raw === "number" && Number.isFinite(raw))) {
      result[key] = typeof raw === "string" ? raw.slice(0, 500) : raw;
    } else if (Array.isArray(raw)) {
      result[key] = raw.slice(0, 100).filter((item): item is SafeScalar => item === null || typeof item === "string" || typeof item === "boolean" || (typeof item === "number" && Number.isFinite(item)));
    } else if (isRecord(raw)) {
      result[key] = sanitizeRecord(raw, depth + 1);
    }
  }
  return result;
}

function isSafeDisplayKey(key: string): boolean {
  const normalized = key.toLowerCase();
  if (normalized === "path_hint") return true;
  return !/(secret|password|passwd|token|api[_-]?key|private|credential|authorization|cookie|endpoint|dsn|uri|url|path|directory|folder)/i.test(normalized);
}

function readEnvelope(value: unknown): Record<string, unknown> {
  if (!isRecord(value)) return {};
  return isRecord(value.data) ? value.data : value;
}

function readError(payload: unknown): string {
  const root = readEnvelope(payload);
  return sanitizeText(readString(root.detail) ?? readString(root.message) ?? readString(root.error)) ?? "";
}

function readString(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const normalized = value.trim();
  return normalized ? normalized.slice(0, 1000) : undefined;
}

function readStringArray(value: unknown): readonly string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string").map((item) => item.trim()).filter(Boolean).slice(0, 100);
}

function readFiniteNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined;
}

function normalizeIdentifier(value: string | undefined): string | undefined {
  if (!value) return undefined;
  const normalized = value.trim();
  return /^[a-zA-Z0-9._:-]{1,160}$/.test(normalized) ? normalized : undefined;
}

function requireSafeIdentifier(value: string): string {
  const normalized = normalizeIdentifier(value);
  if (!normalized) throw new Error("快照标识无效");
  return normalized;
}

function normalizeDescription(value: string | undefined): string {
  return typeof value === "string" ? value.trim().slice(0, 120) : "";
}

function sanitizeText(value: string | undefined): string | undefined {
  if (!value) return undefined;
  return value
    .replace(/https?:\/\/\S+/gi, "[地址已隐藏]")
    .replace(/(?:[a-zA-Z]:\\|\/)(?:[^\s;,]+[\\/])+[^\s;,]*/g, "[路径已隐藏]")
    .replace(/(?:secret|password|token|api[_-]?key)\s*[:=]\s*\S+/gi, "[敏感值已隐藏]")
    .slice(0, 500);
}

function normalizeCategories(values: readonly string[]): readonly string[] {
  const allowed = new Set(["logs", "outputs"]);
  return [...new Set(values.filter((value) => allowed.has(value)))];
}

function sanitizeSettingsPatch(value: SettingsPatch): SettingsPatch {
  const result: SettingsPatch = {};
  for (const [key, item] of Object.entries(value)) {
    if (!EDITABLE_SETTING_KEYS.has(key)) continue;
    if (item === null || typeof item === "string" || typeof item === "boolean" || (typeof item === "number" && Number.isFinite(item))) result[key] = item;
  }
  return result;
}

function clampInteger(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) return min;
  return Math.min(max, Math.max(min, Math.round(value)));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
