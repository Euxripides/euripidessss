import {
  AppstoreOutlined,
  AuditOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  ExclamationCircleOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SaveOutlined,
  SettingOutlined,
  ThunderboltOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Col,
  Descriptions,
  Empty,
  Form,
  Input,
  InputNumber,
  List,
  Modal,
  Row,
  Select,
  Space,
  Spin,
  Switch,
  Tabs,
  Tag,
  Typography,
  message,
} from "antd";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { rpcApi } from "../rpc/rpcApi";
import {
  systemSettingsApi,
  type CleanupPreview,
  type SafeRecord,
  type SettingsPatch,
  type SystemBackup,
  type SystemSettingsSnapshot,
} from "./systemSettingsApi";
import {
  loadSystemNavigationPreference,
  resetSystemNavigationPreference,
  saveSystemNavigationPreference,
  type SystemNavigationPreference,
} from "./systemSettingsStore";
import "./system-settings.css";

type HealthSnapshot = { readonly status: string };
type CloudRuntimeSnapshot = {
  readonly runtime?: {
    readonly state?: string;
    readonly mode?: string;
    readonly available?: boolean;
    readonly queued_jobs?: number;
    readonly leased_jobs?: number;
  };
};
type OverviewItem = { readonly title: string; readonly description: string; readonly icon: ReactNode; readonly tone: string };
type ConfirmAction = { readonly kind: "cleanup"; readonly phrase: string } | { readonly kind: "restore"; readonly phrase: string; readonly backup: SystemBackup };

const NAV_OPTIONS = [
  { label: "Explorer", value: "explorer" },
  { label: "资金追踪", value: "analytics-graph" },
  { label: "下载中心", value: "smart-download" },
  { label: "RPC 节点", value: "crypto-rpc" },
  { label: "数据源", value: "crypto-datasource" },
  { label: "系统设置", value: "system-settings" },
];

const CLEANUP_OPTIONS = [
  { label: "运行日志", value: "logs" },
  { label: "历史输出文件", value: "outputs" },
];

const SERVER_FIELDS = [
  { key: "concurrency_level", label: "ETL 并发级别", type: "number", min: 1, max: 256, restart: true },
  { key: "max_file_size_mb", label: "单文件上限（MB）", type: "number", min: 1, max: 4096, restart: true },
  { key: "analytics_data_source", label: "分析数据源", type: "select", options: ["auto", "clickhouse", "duckdb"], restart: true },
  { key: "clickhouse_enabled", label: "启用 ClickHouse", type: "boolean", restart: true },
  { key: "clickhouse_required", label: "强制使用 ClickHouse", type: "boolean", restart: true },
  { key: "price_engine_enabled", label: "启用历史价格引擎", type: "boolean", restart: true },
  { key: "log_retention_days", label: "日志清理建议天数", type: "number", min: 1, max: 365, restart: false },
  { key: "output_retention_days", label: "输出清理建议天数", type: "number", min: 1, max: 365, restart: false },
  { key: "backup_retention_count", label: "设置快照保留数量", type: "number", min: 1, max: 50, restart: false },
] as const;

export default function SystemSettingsPage() {
  const [local, setLocal] = useState<SystemNavigationPreference>(() => loadSystemNavigationPreference());
  const [health, setHealth] = useState<HealthSnapshot | null>(null);
  const [rpcHealth, setRpcHealth] = useState<Awaited<ReturnType<typeof rpcApi.health>> | null>(null);
  const [cloud, setCloud] = useState<CloudRuntimeSnapshot | null>(null);
  const [snapshot, setSnapshot] = useState<SystemSettingsSnapshot | null>(null);
  const [serverDraft, setServerDraft] = useState<SettingsPatch>({});
  const [serverError, setServerError] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [savingLocal, setSavingLocal] = useState(false);
  const [savingServer, setSavingServer] = useState(false);
  const [backupDescription, setBackupDescription] = useState("");
  const [backupBusy, setBackupBusy] = useState(false);
  const [cleanupCategories, setCleanupCategories] = useState<readonly string[]>(["logs", "outputs"]);
  const [cleanupDays, setCleanupDays] = useState(30);
  const [cleanupPreview, setCleanupPreview] = useState<CleanupPreview | null>(null);
  const [cleanupBusy, setCleanupBusy] = useState(false);
  const [confirmAction, setConfirmAction] = useState<ConfirmAction | null>(null);
  const [confirmInput, setConfirmInput] = useState("");

  const refresh = useCallback(async (notifyFailure = false) => {
    setLoading(true);
    const [serviceResult, rpcResult, cloudResult, settingsResult] = await Promise.allSettled([
      fetchJson<HealthSnapshot>("/api/health"),
      rpcApi.health(),
      fetchJson<CloudRuntimeSnapshot>("/api/scheduler/cloud/runtime"),
      systemSettingsApi.get(),
    ]);
    if (serviceResult.status === "fulfilled") setHealth(serviceResult.value);
    if (rpcResult.status === "fulfilled") setRpcHealth(rpcResult.value);
    if (cloudResult.status === "fulfilled") setCloud(cloudResult.value);
    if (settingsResult.status === "fulfilled") {
      setSnapshot(settingsResult.value);
      setServerError(undefined);
      setServerDraft({});
    } else {
      const detail = settingsResult.reason instanceof Error ? settingsResult.reason.message : "系统设置接口不可用";
      setServerError(detail);
      if (notifyFailure) message.warning(`后端设置未加载：${detail}`);
    }
    setLoading(false);
  }, []);

  useEffect(() => { void refresh(false); }, [refresh]);
  useEffect(() => {
    if (!local.autoRefreshHealth) return;
    const interval = window.setInterval(() => void refresh(false), local.healthRefreshIntervalSec * 1000);
    return () => window.clearInterval(interval);
  }, [local.autoRefreshHealth, local.healthRefreshIntervalSec, refresh]);

  const overview = useMemo<OverviewItem[]>(() => {
    const rpcStatus = rpcHealth?.overview ? `${rpcHealth.overview.healthy_endpoints}/${rpcHealth.overview.configured_endpoints}` : "—";
    const componentHealthy = snapshot?.components.filter((item) => isHealthy(item.status)).length;
    return [
      { title: "系统健康", description: health?.status === "ok" ? "服务在线" : health?.status ?? "尚未加载", icon: <SettingOutlined />, tone: "blue" },
      { title: "运行组件", description: snapshot?.components.length ? `${componentHealthy}/${snapshot.components.length} 正常` : "后端未提供", icon: <ThunderboltOutlined />, tone: "purple" },
      { title: "RPC 节点", description: `健康/已配置：${rpcStatus}`, icon: <CloudServerOutlined />, tone: "cyan" },
      { title: "配置状态", description: snapshot?.pendingRestart ? "存在待重启配置" : snapshot ? "配置已生效" : "后端未提供", icon: <SafetyCertificateOutlined />, tone: snapshot?.pendingRestart ? "orange" : "green" },
    ];
  }, [health?.status, rpcHealth?.overview, snapshot]);

  const saveLocal = useCallback(() => {
    setSavingLocal(true);
    try {
      saveSystemNavigationPreference(local);
      message.success("界面偏好已保存到本机浏览器");
    } finally {
      setSavingLocal(false);
    }
  }, [local]);

  const restoreLocal = useCallback(() => {
    resetSystemNavigationPreference();
    setLocal(loadSystemNavigationPreference());
    message.success("已恢复本机默认偏好");
  }, []);

  const saveServer = useCallback(async () => {
    if (!Object.keys(serverDraft).length) return message.info("没有待保存的运行配置");
    setSavingServer(true);
    try {
      const next = await systemSettingsApi.patch(serverDraft);
      setSnapshot(next);
      setServerDraft({});
      message.success(next.pendingRestart ? "配置已保存，部分设置将在重启后生效" : "配置已保存并生效");
    } catch (error) {
      message.error(readError(error, "运行配置保存失败"));
    } finally {
      setSavingServer(false);
    }
  }, [serverDraft]);

  const createBackup = useCallback(async () => {
    setBackupBusy(true);
    try {
      await systemSettingsApi.createBackup(backupDescription);
      const backups = await systemSettingsApi.listBackups();
      setSnapshot((current) => current ? { ...current, backups } : current);
      setBackupDescription("");
      message.success("设置快照已创建");
    } catch (error) {
      message.error(readError(error, "创建设置快照失败"));
    } finally {
      setBackupBusy(false);
    }
  }, [backupDescription]);

  const previewCleanup = useCallback(async () => {
    if (!cleanupCategories.length) return message.warning("请至少选择一个清理类别");
    setCleanupBusy(true);
    try {
      const preview = await systemSettingsApi.previewCleanup({ categories: cleanupCategories, olderThanDays: cleanupDays });
      setCleanupPreview(preview);
      message.success("清理预览已生成，尚未删除任何文件");
    } catch (error) {
      message.error(readError(error, "生成清理预览失败"));
    } finally {
      setCleanupBusy(false);
    }
  }, [cleanupCategories, cleanupDays]);

  const openCleanupConfirm = useCallback(() => {
    if (!cleanupPreview) return message.warning("请先生成清理预览");
    setConfirmInput("");
    setConfirmAction({ kind: "cleanup", phrase: capabilityString(snapshot?.capabilities, "cleanup_confirmation_phrase") ?? "DELETE EXPIRED FILES" });
  }, [cleanupPreview, snapshot?.capabilities]);

  const openRestoreConfirm = useCallback((backup: SystemBackup) => {
    setConfirmInput("");
    setConfirmAction({ kind: "restore", backup, phrase: capabilityString(snapshot?.capabilities, "restore_confirmation_phrase") ?? "RESTORE SETTINGS" });
  }, [snapshot?.capabilities]);

  const executeConfirmedAction = useCallback(async () => {
    if (!confirmAction || confirmInput !== confirmAction.phrase) return;
    setBackupBusy(confirmAction.kind === "restore");
    setCleanupBusy(confirmAction.kind === "cleanup");
    try {
      const next = confirmAction.kind === "restore"
        ? await systemSettingsApi.restoreBackup(confirmAction.backup.id, confirmInput)
        : await systemSettingsApi.executeCleanup({
          previewId: cleanupPreview?.previewId,
          categories: cleanupCategories,
          olderThanDays: cleanupDays,
          confirmation: confirmInput,
        });
      setSnapshot(next);
      setServerDraft({});
      setCleanupPreview(null);
      setConfirmAction(null);
      setConfirmInput("");
      message.success(confirmAction.kind === "restore" ? "设置快照已恢复" : "存储清理已完成");
    } catch (error) {
      message.error(readError(error, confirmAction.kind === "restore" ? "恢复设置快照失败" : "执行存储清理失败"));
    } finally {
      setBackupBusy(false);
      setCleanupBusy(false);
    }
  }, [cleanupCategories, cleanupDays, cleanupPreview?.previewId, confirmAction, confirmInput]);

  const tabItems = [
    { key: "preferences", label: <span><ToolOutlined /> 界面偏好</span>, children: <PreferencesPanel local={local} setLocal={setLocal} saving={savingLocal} onSave={saveLocal} onRestore={restoreLocal} /> },
    { key: "runtime", label: <span><ThunderboltOutlined /> 运行配置</span>, children: <RuntimePanel snapshot={snapshot} draft={serverDraft} setDraft={setServerDraft} loading={savingServer} onSave={saveServer} /> },
    { key: "storage", label: <span><DatabaseOutlined /> 存储维护</span>, children: <StoragePanel snapshot={snapshot} categories={cleanupCategories} setCategories={setCleanupCategories} days={cleanupDays} setDays={setCleanupDays} preview={cleanupPreview} busy={cleanupBusy} onPreview={previewCleanup} onExecute={openCleanupConfirm} /> },
    { key: "security", label: <span><AuditOutlined /> 安全审计与快照</span>, children: <SecurityPanel snapshot={snapshot} description={backupDescription} setDescription={setBackupDescription} busy={backupBusy} onCreate={createBackup} onRestore={openRestoreConfirm} /> },
  ];

  return (
    <div className="system-settings-page">
      <header className="system-settings-hero">
        <div>
          <Typography.Text className="system-settings-kicker">SYSTEM CONTROL</Typography.Text>
          <Typography.Title level={2}>系统设置</Typography.Title>
          <Typography.Paragraph>集中管理界面偏好、运行参数、存储维护和可审计的设置快照。敏感配置不会在此页面显示。</Typography.Paragraph>
        </div>
        <Button icon={<ReloadOutlined />} onClick={() => void refresh(true)} loading={loading}>刷新全部状态</Button>
      </header>

      {serverError && <Alert className="system-settings-offline" type="warning" showIcon message="后端设置控制台暂不可用" description={`${serverError}。本机界面偏好仍可使用和保存。`} />}
      {snapshot?.pendingRestart && <Alert className="system-settings-offline" type="warning" showIcon message="部分配置等待服务重启" description={snapshot.pendingRestartKeys.length ? snapshot.pendingRestartKeys.join("、") : "后端已记录待重启配置。"} />}

      <Row gutter={[12, 12]} className="system-settings-overview">
        {overview.map((item) => <Col xs={24} sm={12} xl={6} key={item.title}><Card className={`system-settings-card tone-${item.tone}`} loading={loading && !health}><Space align="start" size={12}><div className="system-settings-icon">{item.icon}</div><div><Typography.Text type="secondary">{item.title}</Typography.Text><div className="system-settings-value">{item.description}</div></div></Space></Card></Col>)}
      </Row>

      <Card className="system-settings-tabs"><Tabs items={tabItems} destroyOnHidden /></Card>
      <RuntimeStrip health={health} cloud={cloud} snapshot={snapshot} />

      <Modal
        open={Boolean(confirmAction)}
        title={confirmAction?.kind === "restore" ? "确认恢复设置快照" : "确认执行存储清理"}
        okText={confirmAction?.kind === "restore" ? "恢复快照" : "执行清理"}
        okButtonProps={{ danger: true, disabled: !confirmAction || confirmInput !== confirmAction.phrase }}
        confirmLoading={backupBusy || cleanupBusy}
        onOk={() => void executeConfirmedAction()}
        onCancel={() => { setConfirmAction(null); setConfirmInput(""); }}
      >
        <Alert type="warning" showIcon icon={<ExclamationCircleOutlined />} message="此操作会改变服务器状态" description={confirmAction?.kind === "restore" ? "恢复后，部分配置可能需要重启服务才能生效。" : "只会清理预览中列出的类别；删除后的运行文件无法从页面恢复。"} />
        <Typography.Paragraph className="system-confirm-copy">请输入精确短语 <Typography.Text code>{confirmAction?.phrase}</Typography.Text></Typography.Paragraph>
        <Input value={confirmInput} onChange={(event) => setConfirmInput(event.target.value)} placeholder="输入上方精确短语" autoComplete="off" />
      </Modal>
    </div>
  );
}

function PreferencesPanel(props: { readonly local: SystemNavigationPreference; readonly setLocal: React.Dispatch<React.SetStateAction<SystemNavigationPreference>>; readonly saving: boolean; readonly onSave: () => void; readonly onRestore: () => void }) {
  const { local, setLocal } = props;
  return <div className="system-settings-section"><div className="system-settings-section-head"><div><Typography.Title level={4}>界面与交互</Typography.Title><Typography.Text type="secondary">这些设置仅保存在当前浏览器，不会上传到服务器。</Typography.Text></div><Space wrap><Button onClick={props.onRestore}>恢复本机默认</Button><Button type="primary" icon={<SaveOutlined />} loading={props.saving} onClick={props.onSave}>保存界面偏好</Button></Space></div><Row gutter={[24, 8]}><Col xs={24} lg={12}><Form layout="vertical"><Form.Item label="默认进入页面"><Select value={local.defaultPage} options={NAV_OPTIONS} onChange={(value) => setLocal((current) => ({ ...current, defaultPage: value }))} /></Form.Item><SettingSwitch label="记住上次打开的页面" checked={local.rememberLastPage} onChange={(checked) => setLocal((current) => ({ ...current, rememberLastPage: checked }))} /><SettingSwitch label="使用紧凑侧栏" checked={local.compactSidebar} onChange={(checked) => setLocal((current) => ({ ...current, compactSidebar: checked }))} /><SettingSwitch label="显示下载进度通知" checked={local.downloadProgressToast} onChange={(checked) => setLocal((current) => ({ ...current, downloadProgressToast: checked }))} /></Form></Col><Col xs={24} lg={12}><Form layout="vertical"><SettingSwitch label="自动刷新运行状态" checked={local.autoRefreshHealth} onChange={(checked) => setLocal((current) => ({ ...current, autoRefreshHealth: checked }))} /><Form.Item label="状态刷新间隔（秒）"><InputNumber min={10} max={300} value={local.healthRefreshIntervalSec} disabled={!local.autoRefreshHealth} onChange={(value) => setLocal((current) => ({ ...current, healthRefreshIntervalSec: Number(value ?? 30) }))} style={{ width: "100%" }} /></Form.Item><Form.Item label="资金图默认边数上限" extra="只影响新打开的图，不修改已有分析结果。"><InputNumber min={50} max={5000} step={50} value={local.graphEdgeLimit} onChange={(value) => setLocal((current) => ({ ...current, graphEdgeLimit: Number(value ?? 600) }))} style={{ width: "100%" }} /></Form.Item></Form></Col></Row></div>;
}

function RuntimePanel(props: { readonly snapshot: SystemSettingsSnapshot | null; readonly draft: SettingsPatch; readonly setDraft: React.Dispatch<React.SetStateAction<SettingsPatch>>; readonly loading: boolean; readonly onSave: () => void }) {
  const setValue = (key: string, value: string | number | boolean) => props.setDraft((draft) => ({ ...draft, [key]: value }));
  return (
    <div className="system-settings-section">
      <div className="system-settings-section-head">
        <div><Typography.Title level={4}>运行配置</Typography.Title><Typography.Text type="secondary">只提交控制台白名单字段。每项明确标注立即生效或重启后生效。</Typography.Text></div>
        <Button type="primary" icon={<SaveOutlined />} disabled={!props.snapshot || !Object.keys(props.draft).length} loading={props.loading} onClick={props.onSave}>保存运行配置</Button>
      </div>
      {props.snapshot ? <Row gutter={[20, 8]}>{SERVER_FIELDS.map((field) => {
        const current = readSetting(props.snapshot!.settings, field.key);
        const effective = readSetting(props.snapshot!.effective, field.key);
        const value = props.draft[field.key] ?? current;
        return <Col xs={24} md={12} key={field.key}><Form layout="vertical"><Form.Item
          label={<Space><span>{field.label}</span><Tag color={field.restart ? "orange" : "green"}>{field.restart ? "重启后生效" : "立即生效"}</Tag></Space>}
          extra={effective !== undefined && effective !== current ? `当前生效值：${formatScalar(effective)}` : undefined}
        >
          {field.type === "boolean"
            ? <Switch checked={typeof value === "boolean" ? value : false} onChange={(checked) => setValue(field.key, checked)} />
            : field.type === "select"
              ? <Select value={typeof value === "string" ? value : undefined} placeholder="后端未提供" options={field.options.map((item) => ({ label: item.toUpperCase(), value: item }))} onChange={(next) => setValue(field.key, next)} />
              : <InputNumber min={field.min} max={field.max} value={typeof value === "number" ? value : null} placeholder="后端未提供" onChange={(next) => { if (next !== null) setValue(field.key, next); }} style={{ width: "100%" }} />}
        </Form.Item></Form></Col>;
      })}</Row> : <Empty description="后端运行配置尚未加载" />}
    </div>
  );
}

function StoragePanel(props: { readonly snapshot: SystemSettingsSnapshot | null; readonly categories: readonly string[]; readonly setCategories: (value: readonly string[]) => void; readonly days: number; readonly setDays: (value: number) => void; readonly preview: CleanupPreview | null; readonly busy: boolean; readonly onPreview: () => void; readonly onExecute: () => void }) {
  const storage = props.snapshot?.storage;
  return <div className="system-settings-section"><Typography.Title level={4}>存储概览</Typography.Title><Descriptions bordered size="small" column={{ xs: 1, md: 2 }}><Descriptions.Item label="位置提示">{storage?.pathHint ?? "后端未提供"}</Descriptions.Item><Descriptions.Item label="文件数量">{formatNumber(storage?.fileCount)}</Descriptions.Item><Descriptions.Item label="已用空间">{formatBytes(storage?.usedBytes)}</Descriptions.Item><Descriptions.Item label="可用空间">{formatBytes(storage?.freeBytes)}</Descriptions.Item><Descriptions.Item label="总空间">{formatBytes(storage?.totalBytes)}</Descriptions.Item><Descriptions.Item label="预计可回收">{formatBytes(storage?.reclaimableBytes)}</Descriptions.Item></Descriptions><div className="system-settings-subsection"><Typography.Title level={4}>安全清理</Typography.Title><Alert type="info" showIcon message="必须先预览，再执行" description="预览不会删除文件；执行时还需要输入精确确认短语。系统不会接受任意磁盘路径。" /><Form layout="vertical" className="system-cleanup-form"><Form.Item label="清理类别"><Checkbox.Group options={CLEANUP_OPTIONS} value={[...props.categories]} onChange={(values) => { props.setCategories(values.filter((item): item is string => typeof item === "string")); }} /></Form.Item><Form.Item label="仅处理早于（天）"><InputNumber min={1} max={3650} value={props.days} onChange={(value) => props.setDays(Number(value ?? 30))} /></Form.Item><Space wrap><Button icon={<ReloadOutlined />} loading={props.busy} onClick={props.onPreview}>生成清理预览</Button><Button danger icon={<DeleteOutlined />} disabled={!props.preview} onClick={props.onExecute}>确认并执行</Button></Space></Form>{props.preview && <Card size="small" className="system-cleanup-preview" title="清理预览（尚未执行）"><Descriptions size="small" column={{ xs: 1, sm: 2 }}><Descriptions.Item label="文件数量">{formatNumber(props.preview.fileCount)}</Descriptions.Item><Descriptions.Item label="可回收空间">{formatBytes(props.preview.reclaimableBytes)}</Descriptions.Item><Descriptions.Item label="类别">{props.preview.categories.length ? props.preview.categories.join("、") : "后端未提供"}</Descriptions.Item><Descriptions.Item label="预览失效时间">{formatTime(props.preview.expiresAt)}</Descriptions.Item></Descriptions>{props.preview.warnings.length > 0 && <Alert type="warning" showIcon message={props.preview.warnings.join("；")} />}</Card>}</div></div>;
}

function SecurityPanel(props: { readonly snapshot: SystemSettingsSnapshot | null; readonly description: string; readonly setDescription: (value: string) => void; readonly busy: boolean; readonly onCreate: () => void; readonly onRestore: (backup: SystemBackup) => void }) {
  const backups = props.snapshot?.backups ?? [];
  const audit = props.snapshot?.audit ?? [];
  return <div className="system-settings-section"><Row gutter={[20, 20]}><Col xs={24} xl={12}><Typography.Title level={4}>设置快照</Typography.Title><Typography.Paragraph type="secondary">快照只由后端生成和保存；页面不显示敏感配置值，也不接受自定义保存路径。</Typography.Paragraph><Space.Compact block><Input value={props.description} maxLength={120} showCount placeholder="快照说明（可选）" onChange={(event) => props.setDescription(event.target.value)} /><Button type="primary" icon={<AppstoreOutlined />} loading={props.busy} disabled={!props.snapshot} onClick={props.onCreate}>创建快照</Button></Space.Compact><List className="system-settings-list" locale={{ emptyText: "暂无设置快照" }} dataSource={[...backups]} renderItem={(backup) => <List.Item actions={[<Button key="restore" danger size="small" onClick={() => props.onRestore(backup)}>恢复</Button>]}><List.Item.Meta title={<Space wrap><Typography.Text>{backup.description ?? "设置快照"}</Typography.Text>{backup.status && <Tag>{backup.status}</Tag>}</Space>} description={<span>{formatTime(backup.createdAt)} · {formatBytes(backup.sizeBytes)} · ID {shortId(backup.id)}</span>} /></List.Item>} /></Col><Col xs={24} xl={12}><Typography.Title level={4}>安全审计</Typography.Title><Alert type="success" showIcon message="写操作受本机控制台保护" description="修改配置、创建或恢复快照、执行清理都携带操作来源标识；服务器还会独立验证本机来源与白名单。" /><List className="system-settings-list" locale={{ emptyText: "后端未提供审计记录" }} dataSource={[...audit]} renderItem={(entry) => <List.Item><List.Item.Meta title={<Space wrap><span>{entry.action}</span>{entry.status && <Tag color={isHealthy(entry.status) ? "green" : "default"}>{entry.status}</Tag>}</Space>} description={[formatTime(entry.createdAt), entry.actor, entry.summary].filter(Boolean).join(" · ")} /></List.Item>} /></Col></Row></div>;
}

function RuntimeStrip(props: { readonly health: HealthSnapshot | null; readonly cloud: CloudRuntimeSnapshot | null; readonly snapshot: SystemSettingsSnapshot | null }) {
  return <Card className="system-runtime-strip" size="small" title="运行态详情"><Descriptions size="small" column={{ xs: 1, sm: 2, xl: 4 }}><Descriptions.Item label="后端 API">{props.health?.status ?? "—"}</Descriptions.Item><Descriptions.Item label="Cloud Runtime">{props.cloud?.runtime?.state ?? "—"}{props.cloud?.runtime?.mode ? ` · ${props.cloud.runtime.mode}` : ""}</Descriptions.Item><Descriptions.Item label="Cloud 队列">{formatNumber(props.cloud?.runtime?.queued_jobs)} / {formatNumber(props.cloud?.runtime?.leased_jobs)}</Descriptions.Item><Descriptions.Item label="组件状态">{props.snapshot?.components.length ? props.snapshot.components.map((item) => `${item.name}: ${item.status}`).join("；") : "后端未提供"}</Descriptions.Item></Descriptions></Card>;
}

function SettingSwitch(props: { readonly label: string; readonly checked: boolean; readonly onChange: (checked: boolean) => void }) {
  return <Form.Item label={props.label}><Switch checked={props.checked} onChange={props.onChange} /></Form.Item>;
}

function readSetting(record: SafeRecord, key: string): string | number | boolean | null | undefined {
  const direct = record[key];
  if (direct === null || typeof direct === "string" || typeof direct === "number" || typeof direct === "boolean") return direct;
  for (const group of ["runtime", "server", "storage", "scheduler"]) {
    const nested = record[group];
    if (isSafeRecord(nested)) {
      const value = nested[key];
      if (value === null || typeof value === "string" || typeof value === "number" || typeof value === "boolean") return value;
    }
  }
  return undefined;
}

function isSafeRecord(value: SafeRecord[string] | undefined): value is SafeRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function capabilityString(record: SafeRecord | undefined, key: string): string | undefined {
  const value = record?.[key];
  return typeof value === "string" && value.trim() ? value.trim().slice(0, 80) : undefined;
}

function isHealthy(value: string | undefined): boolean { return /^(ok|healthy|ready|running|success|available|configured)$/i.test(value ?? ""); }
function formatScalar(value: string | number | boolean | null): string { return value === null ? "—" : typeof value === "boolean" ? value ? "开启" : "关闭" : String(value); }
function formatNumber(value: number | undefined): string { return value === undefined ? "—" : value.toLocaleString(); }
function formatBytes(value: number | undefined): string { if (value === undefined) return "—"; if (value < 1024) return `${value} B`; const units = ["KB", "MB", "GB", "TB", "PB"]; let current = value / 1024; let index = 0; while (current >= 1024 && index < units.length - 1) { current /= 1024; index += 1; } return `${current.toFixed(current >= 10 ? 1 : 2)} ${units[index]}`; }
function formatTime(value: string | undefined): string { if (!value) return "—"; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false }); }
function shortId(value: string): string { return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value; }
function readError(error: unknown, fallback: string): string { return error instanceof Error && error.message ? error.message : fallback; }

async function fetchJson<T>(url: string): Promise<T> {
  const response = await fetch(url, { cache: "no-store", headers: { Accept: "application/json" } });
  const text = await response.text();
  let payload: unknown = {};
  if (text) { try { payload = JSON.parse(text) as unknown; } catch { throw new Error("后端返回内容无法解析"); } }
  if (!response.ok) throw new Error(readPayloadDetail(payload) || `请求失败（HTTP ${response.status}）`);
  return payload as T;
}

function readPayloadDetail(payload: unknown): string {
  if (typeof payload !== "object" || payload === null) return "";
  const value = (payload as { detail?: unknown }).detail;
  return typeof value === "string" ? value : "";
}
