export type SystemNavigationPreference = {
  rememberLastPage: boolean;
  defaultPage: string;
  autoRefreshHealth: boolean;
  healthRefreshIntervalSec: number;
  compactSidebar: boolean;
  graphEdgeLimit: number;
  downloadProgressToast: boolean;
};

const STORAGE_KEY = "etl.system.settings.v1";
const ALLOWED_PAGES = new Set(["explorer", "analytics-graph", "smart-download", "crypto-rpc", "crypto-datasource", "system-settings"]);

const DEFAULTS: SystemNavigationPreference = {
  rememberLastPage: true,
  defaultPage: "explorer",
  autoRefreshHealth: true,
  healthRefreshIntervalSec: 30,
  compactSidebar: false,
  graphEdgeLimit: 600,
  downloadProgressToast: true,
};

export function loadSystemNavigationPreference(): SystemNavigationPreference {
  if (typeof window === "undefined") return DEFAULTS;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULTS;
    const parsed = JSON.parse(raw) as Partial<SystemNavigationPreference>;
    return {
      rememberLastPage: readBoolean(parsed.rememberLastPage, DEFAULTS.rememberLastPage),
      defaultPage: typeof parsed.defaultPage === "string" && ALLOWED_PAGES.has(parsed.defaultPage) ? parsed.defaultPage : DEFAULTS.defaultPage,
      autoRefreshHealth: readBoolean(parsed.autoRefreshHealth, DEFAULTS.autoRefreshHealth),
      healthRefreshIntervalSec: clampInterval(parsed.healthRefreshIntervalSec),
      compactSidebar: readBoolean(parsed.compactSidebar, DEFAULTS.compactSidebar),
      graphEdgeLimit: clampInt(parsed.graphEdgeLimit, 50, 5000, DEFAULTS.graphEdgeLimit),
      downloadProgressToast: readBoolean(parsed.downloadProgressToast, DEFAULTS.downloadProgressToast),
    };
  } catch {
    return DEFAULTS;
  }
}

export function saveSystemNavigationPreference(values: SystemNavigationPreference): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify({
    rememberLastPage: values.rememberLastPage,
    defaultPage: ALLOWED_PAGES.has(values.defaultPage) ? values.defaultPage : DEFAULTS.defaultPage,
    autoRefreshHealth: values.autoRefreshHealth,
    healthRefreshIntervalSec: clampInterval(values.healthRefreshIntervalSec),
    compactSidebar: values.compactSidebar,
    graphEdgeLimit: clampInt(values.graphEdgeLimit, 50, 5000, DEFAULTS.graphEdgeLimit),
    downloadProgressToast: values.downloadProgressToast,
  }));
}

export function resetSystemNavigationPreference(): void {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem(STORAGE_KEY);
}

function clampInterval(value: number | undefined): number {
  if (!Number.isFinite(value)) return DEFAULTS.healthRefreshIntervalSec;
  return Math.min(300, Math.max(10, Math.round(value ?? DEFAULTS.healthRefreshIntervalSec)));
}

function clampInt(value: number | undefined, min: number, max: number, fallback: number): number {
  if (!Number.isFinite(value)) return fallback;
  return Math.min(max, Math.max(min, Math.round(value ?? fallback)));
}

function readBoolean(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback;
}
