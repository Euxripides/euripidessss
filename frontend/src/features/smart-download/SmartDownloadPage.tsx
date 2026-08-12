// 智能下载统一入口（Phase 4）：创建下载 / 任务中心 / 结果数据。
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Collapse,
  DatePicker,
  Drawer,
  Form,
  Input,
  message,
  Modal,
  InputNumber,
  Radio,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Timeline,
  Tooltip,
  Upload,
  Typography,
} from "antd";
import {
  CloudDownloadOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  FileSearchOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  StopOutlined,
  ThunderboltOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import type { UploadFile } from "antd";
import type { Dayjs } from "dayjs";
import {
  DATASET_LABELS,
  addressAction,
  addressDetail,
  batchAction,
  batchSnapshot,
  batchSummary,
  compareBatchRuns,
  createBatch,
  deleteTaskTemplate,
  datasetLedger,
  downloadResultExport,
  expandGraphCache,
  getBatchHardening,
  getBatchAccelerator,
  getBatchReport,
  getPrefetchStats,
  getPrefetchStatus,
  getSmartDownloadCapabilities,
  getTurboStatus,
  importAddressFile,
  instantiateTaskTemplate,
  listBatchAddresses,
  listBatches,
  listRegistry,
  listTaskTemplates,
  pinPrefetch,
  planBatch,
  previewPlannerV2,
  preflightBatch,
  queryResults,
  resultSummary,
  saveTaskTemplate,
  smartDownloadErrorMessage,
  switchBatchMode,
  upgradePrefetch,
  type AddressDetail,
  type AddressJob,
  type BatchSummary,
  type BatchSnapshot,
  type BatchJob,
  type BatchAcceleratorStatus,
  type CompareRunsResult,
  type CreateBatchRequest,
  type Dataset,
  type DownloadMode,
  type DownloadPriority,
  type HardeningStatus,
  type ImportResult,
  type IndexedResult,
  type JobReport,
  type LedgerEntry,
  type PlannerV2Preview,
  type PreflightEstimate,
  type RangeSpec,
  type ResourceProfile,
  type PrefetchCandidateView,
  type PrefetchStatus,
  type TurboStatus,
  type TaskTemplate,
} from "./smartDownloadApi";
import "./smart-download.css";

interface SmartDownloadPageProps {
  onOpenAddress: (address: string) => void;
  onNavigate: (page: string) => void;
}

const STATUS_COLOR: Record<string, string> = {
  CREATED: "default",
  RUNNING: "processing",
  DOWNLOADING: "processing",
  PAUSED: "warning",
  VALIDATING: "purple",
  COMPLETED: "success",
  VALIDATED: "success",
  PARTIAL: "orange",
  FAILED: "error",
  CANCELED: "default",
  WAITING: "default",
  PENDING: "default",
  EMPTY: "default",
  READY: "blue",
};

const chainOptions = [
  { value: "bsc", label: "BNB Smart Chain" },
  { value: "eth", label: "Ethereum" },
  { value: "base", label: "Base" },
  { value: "arbitrum", label: "Arbitrum One" },
];

const datasetOptions = Object.keys(DATASET_LABELS).map((d) => ({
  value: d,
  label: DATASET_LABELS[d],
}));

const CHAIN_KEYS = new Set(["bsc", "eth", "base", "arbitrum"]);
const TERMINAL_BATCH_STATUSES = new Set(["COMPLETED", "PARTIAL", "FAILED", "CANCELED"]);
const TERMINAL_ADDRESS_STATUSES = new Set(["COMPLETED", "PARTIAL", "FAILED", "CANCELED"]);
const BATCH_STATUS_FILTER_OPTIONS = [
  "CREATED",
  "RUNNING",
  "PAUSED",
  "PAUSED_BY_PRIORITY",
  "COMPLETED",
  "PARTIAL",
  "FAILED",
  "CANCELED",
].map((status) => ({ value: status, label: status }));

function canPauseBatch(status?: string): boolean {
  return status === "RUNNING";
}

function canResumeBatch(status?: string): boolean {
  return status === "CREATED" || status === "PAUSED";
}

// 多链输入：每行 `0x...` 后可跟空格加链名（如 `0x... eth`），缺省使用上方网络。
function parseAddressLines(text: string): {
  addresses: string[];
  overrides: Record<string, string>;
} {
  const addresses: string[] = [];
  const overrides: Record<string, string> = {};
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line) continue;
    const m = line.match(/^(0x[0-9a-fA-F]{40})(?:\s+([A-Za-z0-9_]+))?$/);
    if (!m) {
      addresses.push(line);
      continue;
    }
    addresses.push(m[1]);
    if (m[2] && CHAIN_KEYS.has(m[2].toLowerCase())) {
      overrides[m[1].toLowerCase()] = m[2].toLowerCase();
    }
  }
  return { addresses, overrides };
}

function statusClass(status?: string): string {
  if (!status) return "is-default";
  const s = status.toUpperCase();
  if (s === "RUNNING" || s === "DOWNLOADING") return "is-running";
  if (s === "VALIDATING") return "is-validating";
  if (s === "VALIDATED") return "is-validated";
  if (s === "COMPLETED") return "is-completed";
  if (s === "PARTIAL") return "is-partial";
  if (s === "FAILED") return "is-failed";
  if (s === "PAUSED" || s === "EMPTY") return "is-paused";
  return "is-default";
}

function StatusPill({ status }: { status?: string }) {
  return <span className={`sd-status-pill ${statusClass(status)}`}>{status ?? "—"}</span>;
}

function ModeTag({ mode }: { mode?: DownloadMode }) {
  const effectiveMode = mode ?? "AUTO";
  return (
    <Tag
      color={effectiveMode === "EMERGENCY" ? "red" : effectiveMode === "TURBO" ? "gold" : "blue"}
      icon={effectiveMode === "AUTO" ? undefined : <ThunderboltOutlined />}
    >
      {effectiveMode}
    </Tag>
  );
}

function metricNumber(value?: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function formatRate(value?: number): string {
  const rate = metricNumber(value);
  return rate > 0 ? Math.round(rate).toLocaleString() : "—";
}

function formatDuration(value?: number): string {
  const seconds = metricNumber(value);
  if (seconds <= 0) return "—";
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const minutes = Math.floor(seconds / 60);
  const rest = Math.round(seconds % 60);
  if (minutes < 60) return `${minutes}m ${rest}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

function formatBytes(value?: number): string {
  const bytes = metricNumber(value);
  if (bytes <= 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  return `${(bytes / 1024 ** index).toFixed(index > 2 ? 2 : 1)} ${units[index]}`;
}

const PROFILE_TO_MODE: Record<ResourceProfile, DownloadMode> = {
  STANDARD: "AUTO",
  PERFORMANCE: "TURBO",
  EXTREME: "EMERGENCY",
};

const BOTTLENECK_LABELS: Record<string, string> = {
  SOURCE: "数据源",
  NETWORK: "网络",
  RPC: "RPC",
  CLOUD: "SQD Cloud",
  PARSER: "解析器",
  CLICKHOUSE: "ClickHouse 写入",
  DISK: "磁盘",
  VALIDATION: "数据校验",
  UNKNOWN: "尚未识别",
};

function GuardTag({ label, guard }: { label: string; guard?: { status?: string; message?: string } }) {
  const status = guard?.status ?? "UNKNOWN";
  const color = status === "OK" ? "green" : status === "WARNING" ? "gold" : status === "BLOCKED" || status === "CRITICAL" ? "red" : "default";
  return <Tooltip title={guard?.message ?? "暂无详细信息"}><Tag color={color}>{label} · {status}</Tag></Tooltip>;
}

function TurboMetric({
  label,
  value,
  suffix,
  detail,
}: {
  label: string;
  value: string;
  suffix?: string;
  detail?: string;
}) {
  return (
    <div className="sd-turbo-metric">
      <span className="sd-turbo-metric-label">{label}</span>
      <strong>{value}</strong>
      {suffix ? <span className="sd-turbo-metric-suffix">{suffix}</span> : null}
      {detail ? <small>{detail}</small> : null}
    </div>
  );
}

function plannerCount(value?: number): string {
  return typeof value === "number" && Number.isFinite(value) ? Math.round(value).toLocaleString() : "—";
}

function plannerRatio(value?: number): string {
  return typeof value === "number" && Number.isFinite(value) ? `${(value * 100).toFixed(1)}%` : "—";
}

function PlannerV2Metrics({ preview, compact = false }: { preview: PlannerV2Preview; compact?: boolean }) {
  return (
    <div className={`sd-planner-grid ${compact ? "is-compact" : ""}`}>
      <TurboMetric label="地址数" value={plannerCount(preview.address_count)} />
      <TurboMetric label="原始任务单元" value={plannerCount(preview.input_jobs)} />
      <TurboMetric
        label="合并后工作负载"
        value={plannerCount(preview.merged_workloads)}
        detail={preview.bundle_savings === undefined ? undefined : `Dataset Bundle 再节省 ${plannerCount(preview.bundle_savings)}`}
      />
      <TurboMetric
        label="地址组"
        value={plannerCount(preview.address_groups)}
        detail={preview.heavy_address_count === undefined && preview.split_count === undefined
          ? undefined
          : `Heavy ${plannerCount(preview.heavy_address_count)} · Split ${plannerCount(preview.split_count)}`}
      />
      <TurboMetric
        label="Coverage 复用"
        value={plannerRatio(preview.coverage_reuse_ratio)}
        detail={preview.coverage_hits === undefined ? undefined : `${plannerCount(preview.coverage_hits)} hits`}
      />
      <TurboMetric
        label="Provider 请求节省"
        value={plannerCount(preview.provider_requests_saved)}
        detail={preview.provider_request_reduction_ratio === undefined ? undefined : `减少 ${plannerRatio(preview.provider_request_reduction_ratio)}`}
      />
      <TurboMetric
        label="重复工作已避免"
        value={plannerCount(preview.duplicate_work_avoided)}
        detail={preview.duplicate_work_ratio === undefined ? undefined : plannerRatio(preview.duplicate_work_ratio)}
      />
      <TurboMetric
        label="下载放大"
        value={preview.download_amplification === undefined ? "—" : `${preview.download_amplification.toFixed(2)}×`}
        detail="越接近 1 越好"
      />
    </div>
  );
}

function AcceleratorFlag({ label, active, color }: { label: string; active: boolean; color: string }) {
  return active ? <Tag color={color}>{label}</Tag> : null;
}

function AcceleratorPanel({ accelerator }: { accelerator: BatchAcceleratorStatus }) {
  const assignedWorkloads = new Set(
    accelerator.datasets.flatMap((dataset) =>
      dataset.groups.flatMap((group) => group.workloads.map((workload) => workload.id)),
    ),
  );
  const rootWorkloads = accelerator.shared_workloads.filter((workload) => !assignedWorkloads.has(workload.id));
  const datasetItems = accelerator.datasets.map((dataset) => ({
    key: `dataset-${dataset.dataset}`,
    label: (
      <Space wrap>
        <b>{DATASET_LABELS[dataset.dataset] ?? dataset.dataset}</b>
        <StatusPill status={dataset.status} />
        <span className="sd-meta">{plannerCount(dataset.address_count)} 地址 · {plannerCount(dataset.group_count)} groups · {plannerCount(dataset.workload_count)} workloads</span>
      </Space>
    ),
    children: dataset.groups.length > 0 ? (
      <Collapse
        className="sd-accelerator-groups"
        size="small"
        items={dataset.groups.map((group) => ({
          key: group.id,
          label: (
            <Space wrap>
              <code>{abbr(group.id)}</code>
              <StatusPill status={group.status} />
              <span>{plannerCount(group.address_count)} 地址</span>
              <span>{plannerCount(group.workloads.length)} workloads</span>
              <AcceleratorFlag label="重负载地址" active={group.heavy_address} color="volcano" />
              <AcceleratorFlag label="异常地址" active={group.poison_address} color="red" />
              <AcceleratorFlag label="已拆分" active={group.split} color="orange" />
            </Space>
          ),
          children: group.workloads.length > 0 ? (
            <div className="sd-shared-workloads">
              {group.workloads.map((workload) => (
                <div className="sd-shared-workload" key={workload.id}>
                  <div>
                    <Space wrap>
                      <code>{abbr(workload.id)}</code>
                      <StatusPill status={workload.status} />
                      {workload.provider ? <Tag>{workload.provider.toUpperCase()}</Tag> : null}
                      <AcceleratorFlag label="复用运行中任务" active={workload.join_existing} color="cyan" />
                      <AcceleratorFlag label="命中本地覆盖" active={workload.coverage_hit} color="green" />
                      <AcceleratorFlag label="重负载" active={workload.heavy_address} color="volcano" />
                      <AcceleratorFlag label="异常" active={workload.poison_address} color="red" />
                      <AcceleratorFlag label="已拆分" active={workload.split} color="orange" />
                    </Space>
                    <div className="sd-workload-meta">
                      <span>Range {Number.isFinite(workload.from_block) ? workload.from_block?.toLocaleString() : "—"} – {Number.isFinite(workload.to_block) ? workload.to_block?.toLocaleString() : "—"}</span>
                      <span>{plannerCount(workload.address_count)} 地址</span>
                      <span>ref_count {plannerCount(workload.ref_count)}</span>
                      {workload.datasets.length > 0 ? <span>{workload.datasets.map((item) => DATASET_LABELS[item] ?? item).join(" / ")}</span> : null}
                    </div>
                  </div>
                  {workload.error ? <Typography.Text type="danger" ellipsis={{ tooltip: workload.error }}>{workload.error}</Typography.Text> : null}
                </div>
              ))}
            </div>
          ) : <Typography.Text type="secondary">后端未返回该组的共享工作负载明细。</Typography.Text>,
        }))}
      />
    ) : <Typography.Text type="secondary">后端未返回该数据集的地址组明细。</Typography.Text>,
  }));
  if (rootWorkloads.length > 0) {
    datasetItems.push({
      key: "root-shared-workloads",
      label: <Space><b>共享工作负载</b><span className="sd-meta">{rootWorkloads.length} 个跨任务工作负载</span></Space>,
      children: (
        <div className="sd-shared-workloads">
          {rootWorkloads.map((workload) => (
            <div className="sd-shared-workload" key={workload.id}>
              <Space wrap>
                <code>{abbr(workload.id)}</code>
                <StatusPill status={workload.status} />
                <span>ref_count {plannerCount(workload.ref_count)}</span>
                <AcceleratorFlag label="复用运行中任务" active={workload.join_existing} color="cyan" />
                <AcceleratorFlag label="命中本地覆盖" active={workload.coverage_hit} color="green" />
              </Space>
            </div>
          ))}
        </div>
      ),
    });
  }
  return (
    <Card
      className="sd-accelerator"
      size="small"
      title={<Space><span>批量下载加速器</span><Tag color="geekblue">V3.3</Tag><StatusPill status={accelerator.status} /></Space>}
      extra={accelerator.updated_at ? <span className="sd-meta">更新 {new Date(accelerator.updated_at).toLocaleString()}</span> : null}
    >
      <PlannerV2Metrics preview={accelerator.summary} compact />
      <div className="sd-accelerator-hint">批次 → 数据集 → 地址组 → 共享工作负载；默认折叠，单地址仅在需要时进入详情。</div>
      {datasetItems.length > 0 ? (
        <Collapse className="sd-accelerator-tree" items={datasetItems} />
      ) : (
        <Typography.Text type="secondary">加速器已返回汇总，但未提供可展开的层级明细。</Typography.Text>
      )}
    </Card>
  );
}

function TurboState({ label, active }: { label: string; active?: boolean }) {
  return <Tag color={active ? "processing" : "default"}>{label} {active ? "ON" : "IDLE"}</Tag>;
}

function TurboDashboard({ status }: { status: TurboStatus }) {
  const coverage = Math.max(0, Math.min(100, metricNumber(status.coverage_percent)));
  const reportedRelevantCoverage = metricNumber(status.relevant_range_coverage_percent);
  const relevantCertification = status.relevant_range_certification ??
    (reportedRelevantCoverage >= 100 ? "CERTIFIED" : "PENDING");
  const relevantCertified = reportedRelevantCoverage >= 99.999 ||
    ["CERTIFIED", "RANGE_CERTIFIED", "BATCH_CERTIFIED"].includes(relevantCertification.toUpperCase());
  const relevantCoverage = Math.max(
    0,
    Math.min(100, reportedRelevantCoverage || (relevantCertified ? 100 : 0)),
  );

  return (
    <Card
      size="small"
      className={`sd-turbo-dashboard ${status.mode === "EMERGENCY" ? "is-emergency" : ""}`}
      style={{ marginTop: 16 }}
      title={<Space><ThunderboltOutlined />Turbo Dashboard <ModeTag mode={status.mode} /></Space>}
      extra={<Tag color={status.priority === "URGENT" ? "red" : "blue"}>{status.priority ?? "NORMAL"}</Tag>}
    >
      <div className="sd-turbo-grid">
        <TurboMetric label="当前可用覆盖" value={`${coverage.toFixed(2)}%`} />
        <TurboMetric
          label="当前相关区间"
          value={`${relevantCoverage.toFixed(2)}%`}
          detail={relevantCertification}
        />
        <TurboMetric
          label="SQD Cloud"
          value={`${metricNumber(status.cloud_jobs).toLocaleString()} jobs`}
          detail={`${formatRate(status.cloud_rows_per_second)} rows/s`}
        />
        <TurboMetric
          label="RPC"
          value={`${metricNumber(status.rpc_workers).toLocaleString()} workers`}
          detail={`${formatRate(status.rpc_rows_per_second)} rows/s`}
        />
        <TurboMetric label="Parser" value={formatRate(status.parser_rows_per_second)} suffix="rows/s" />
        <TurboMetric label="ClickHouse" value={formatRate(status.clickhouse_rows_per_second)} suffix="rows/s" />
        <TurboMetric label="TTFA" value={formatDuration(status.time_to_first_data_seconds)} />
        <TurboMetric label="TTFR" value={formatDuration(status.time_to_first_relevant_range_seconds)} />
        <TurboMetric label="ETA" value={formatDuration(status.eta_seconds)} />
      </div>
      <div className="sd-turbo-certification">
        <Tag color={relevantCertified ? "green" : relevantCoverage > 0 ? "gold" : "default"}>
          相关区间 {relevantCertification}
        </Tag>
        <span>相关区间优先认证；整批任务可在后台继续完成。</span>
      </div>
      <div className="sd-turbo-states">
        <TurboState label={`Burst ${status.burst_level ?? "L1"}`} active={status.burst_active} />
        <TurboState label="Backpressure" active={status.backpressure_active} />
        <TurboState label="Preemption" active={status.preemption_active} />
        <TurboState label="Work stealing" active={status.work_stealing_active} />
        <TurboState label="Re-shard" active={status.reshard_active} />
        <TurboState label="Hedge" active={status.hedge_active} />
      </div>
      <Collapse
        ghost
        size="small"
        className="sd-turbo-advanced"
        items={[{
          key: "advanced",
		  label: "高级 · 底层通道与 Range 信息",
          children: (
            <div className="sd-turbo-advanced-grid">
              <span>Cloud ranges <b>{metricNumber(status.cloud_ranges).toLocaleString()}</b></span>
              <span>RPC ranges <b>{metricNumber(status.rpc_ranges).toLocaleString()}</b></span>
              <span>运行 <b>{metricNumber(status.running_ranges).toLocaleString()}</b></span>
              <span>待处理 <b>{metricNumber(status.pending_ranges).toLocaleString()}</b></span>
              <span>完成 <b>{metricNumber(status.completed_ranges).toLocaleString()}</b></span>
              <span>失败 <b>{metricNumber(status.failed_ranges).toLocaleString()}</b></span>
              <span>覆盖区块 <b>{metricNumber(status.covered_blocks).toLocaleString()}</b> / {metricNumber(status.total_blocks).toLocaleString()}</span>
              <span>总吞吐 <b>{formatRate(status.rows_per_second)}</b> rows/s</span>
              <span>Cloud <b>{status.cloud_available ? "AVAILABLE" : "UNAVAILABLE"}</b></span>
              <span>RPC <b>{status.rpc_available ? "AVAILABLE" : "UNAVAILABLE"}</b></span>
            </div>
          ),
        }]}
      />
    </Card>
  );
}

function ProfileTag({ profile }: { profile?: ResourceProfile }) {
  const value = profile ?? "STANDARD";
  const label = value === "EXTREME" ? "极速" : value === "PERFORMANCE" ? "高性能" : "标准";
  return <Tag color={value === "EXTREME" ? "red" : value === "PERFORMANCE" ? "gold" : "blue"}>{label}</Tag>;
}

function HardeningPanel({ status, report }: { status: HardeningStatus; report: JobReport | null }) {
  const pipeline = [
    { key: "DOWNLOAD", label: "Download", value: status.pipeline.download },
    { key: "PARSER", label: "Parse", value: status.pipeline.parse },
    { key: "CLICKHOUSE", label: "ClickHouse", value: status.pipeline.clickhouse },
  ];
  const bottleneck = (status.slowest_stage ?? status.bottleneck).toUpperCase();
  const slowest = ["SOURCE", "NETWORK", "RPC", "CLOUD"].includes(bottleneck) ? "DOWNLOAD" : bottleneck;
  const advanced = [
    ["Cloud Jobs", status.cloud_jobs],
    ["RPC Workers", status.rpc_workers],
    ["Range Ledger", status.range_ledger],
    ["Retries", status.retries],
    ["Errors", status.errors],
    ["Checkpoints", status.checkpoints],
    ["Gap Repair", status.gap_repairs],
  ] as const;
  return (
    <Card className="sd-hardening" size="small" title="Production Hardening" extra={<Tag color="blue">V3.2</Tag>}>
      <div className="sd-hardening-summary">
        <TurboMetric label="ETA V2" value={formatDuration(status.eta_seconds)} detail={`区间 ${formatDuration(status.eta_lower_seconds)} – ${formatDuration(status.eta_upper_seconds)} · 置信 ${(status.eta_confidence * 100).toFixed(0)}%`} />
        <TurboMetric label="统一瓶颈" value={BOTTLENECK_LABELS[status.bottleneck.toUpperCase()] ?? status.bottleneck} detail={status.slowest_stage ? `最慢层 ${status.slowest_stage}` : undefined} />
        <TurboMetric label="Stall Detector" value={status.stalled ? "STALL" : "正常"} detail={status.stalled ? `${status.stall_stage ?? "UNKNOWN"} · ${formatDuration(status.stall_seconds)}` : "持续检测进度"} />
        <TurboMetric label="Self Recovery" value={status.self_recovery ? "恢复中" : status.recovery_status ?? "待命"} detail={status.recovery_actions.join(" / ") || "最小范围恢复"} />
      </div>
      <div className="sd-pipeline">
        {pipeline.map((stage) => (
          <div key={stage.key} className={`sd-pipeline-stage ${slowest.includes(stage.key) ? "is-slowest" : ""}`}>
            <span>{stage.label}</span>
            <b>{formatRate(stage.value.rows_per_second)} rows/s</b>
            <small>{stage.value.status ?? "RUNNING"}{stage.value.queue_depth ? ` · Queue ${stage.value.queue_depth}` : ""}</small>
          </div>
        ))}
      </div>
      {slowest !== "UNKNOWN" ? (
        <div className="sd-hardening-note">
          当前最慢层：{BOTTLENECK_LABELS[status.bottleneck.toUpperCase()] ?? status.slowest_stage ?? status.bottleneck}
        </div>
      ) : null}
      <div className="sd-hardening-guards">
        <GuardTag label="Storage" guard={status.guards.storage} />
        <GuardTag label="RPC Quota" guard={status.guards.rpc} />
        <GuardTag label="Cloud Budget" guard={status.guards.cloud} />
        <GuardTag label="ClickHouse" guard={status.guards.clickhouse} />
      </div>
      {status.failure ? (
        <div className="sd-hardening-note sd-hardening-error">
          <b>
            {status.failure.error_type ?? "任务失败"} · {status.failure.stage ?? "UNKNOWN"}
          </b>
          <span>
            Dataset {status.failure.dataset ?? "—"} · Range {status.failure.range ?? "—"} · Provider{" "}
            {status.failure.provider ?? "—"} · 完成 {status.failure.completed_percent ?? 0}% · 恢复点{" "}
            {status.failure.checkpoint ?? "—"} · 建议{" "}
            {status.failure.recommended_action ?? "检查错误后从 checkpoint 继续"}
          </span>
        </div>
      ) : null}
      {report ? (
        <div className="sd-job-report">
          <b>Job Report</b>
          <span>Rows {report.rows.toLocaleString()}</span>
          <span>Coverage {report.coverage.toFixed(2)}%</span>
          <span>Duplicates {report.duplicates.toLocaleString()}</span>
          <span>TTFA {formatDuration(report.ttfa_seconds)}</span>
          <span>总耗时 {formatDuration(report.total_duration_seconds)}</span>
          <span>峰值 {formatRate(report.peak_throughput_rows_per_sec)} rows/s</span>
          <span>平均 {formatRate(report.average_throughput_rows_per_sec)} rows/s</span>
          <span>Retry {report.retry_count} · Gap Repair {report.gap_repair_count}</span>
          <StatusPill status={report.certification ?? report.result} />
        </div>
      ) : null}
      <Collapse
        ghost
        className="sd-hardening-advanced"
        items={[{
          key: "hardening-advanced",
		  label: "高级 · 执行明细",
          children: (
            <div className="sd-hardening-advanced-grid">
              {advanced.map(([label, values]) => (
                <div key={label}>
                  <b>{label}</b><Tag>{values.length}</Tag>
                  <Typography.Text ellipsis={{ tooltip: values.length > 0 ? JSON.stringify(values.slice(0, 3)) : "暂无记录" }}>
                    {values.length > 0 ? JSON.stringify(values.slice(0, 3)) : "暂无记录"}
                  </Typography.Text>
                </div>
              ))}
            </div>
          ),
        }]}
      />
    </Card>
  );
}

function ProgressBar({
  percent,
  status,
  rows,
  speed,
  eta,
}: {
  percent?: number;
  status?: string;
  rows?: number;
  speed?: number;
  eta?: number;
}) {
  const pct = Math.max(0, Math.min(1, percent ?? 0)) * 100;
  return (
    <div className="sd-progress">
      <div className="sd-progress-track">
        <div className={`sd-progress-fill ${statusClass(status)}`} style={{ width: `${pct}%` }} />
      </div>
      <div className="sd-progress-info">
        <span className="sd-percent">{Math.round(pct)}%</span>
        {typeof rows === "number" && rows >= 0 && <span className="sd-meta">{rows.toLocaleString()} 行</span>}
        {typeof speed === "number" && speed > 0 && (
          <span className="sd-speed">{Math.round(speed).toLocaleString()} rows/s</span>
        )}
        {typeof eta === "number" && eta > 0 && <span className="sd-meta">ETA {Math.round(eta)}s</span>}
      </div>
    </div>
  );
}

function confidenceLabel(c?: number): string {
  if (c == null) return "—";
  if (c >= 0.9) return "高";
  if (c >= 0.7) return "中";
  return "低";
}

function batchDisplayName(b: BatchJob): string {
  const t = new Date(b.created_at);
  const pad = (n: number) => String(n).padStart(2, "0");
  const ds = (b.dataset_types ?? [])
    .map((d) => DATASET_LABELS[d] ?? d)
    .join("/");
  const time = Number.isNaN(t.getTime())
    ? "历史任务"
    : `${pad(t.getMonth() + 1)}-${pad(t.getDate())} ${pad(t.getHours())}:${pad(t.getMinutes())}`;
  return `${b.chain_key.toUpperCase()} · ${b.address_count} 地址 · ${ds || "未记录数据集"} · ${time}`;
}

function abbr(v?: unknown): string {
  const s = String(v ?? "");
  if (s.length > 18) return `${s.slice(0, 10)}…${s.slice(-6)}`;
  return s;
}

function fmtTime(v?: unknown): string {
  if (v == null || v === "") return "—";
  let date: Date;
  if (typeof v === "number" || (typeof v === "string" && /^[-+]?\d+(?:\.\d+)?$/.test(v.trim()))) {
    const numeric = Number(v);
    if (!Number.isFinite(numeric)) return "—";
    date = new Date(Math.abs(numeric) < 1_000_000_000_000 ? numeric * 1000 : numeric);
  } else if (typeof v === "string") {
    date = new Date(v);
  } else {
    return "—";
  }
  if (!Number.isFinite(date.getTime())) return "—";
  return date.toISOString().replace("T", " ").slice(0, 19);
}

function compareIndexedResults(left: IndexedResult, right: IndexedResult): number {
  const leftQuality = [
    left.certification?.toUpperCase() === "CERTIFIED" ? 1 : 0,
    left.merged_parquet ? 1 : 0,
    left.row_count > 0 ? 1 : 0,
  ];
  const rightQuality = [
    right.certification?.toUpperCase() === "CERTIFIED" ? 1 : 0,
    right.merged_parquet ? 1 : 0,
    right.row_count > 0 ? 1 : 0,
  ];
  for (let index = 0; index < leftQuality.length; index += 1) {
    if (leftQuality[index] !== rightQuality[index]) return leftQuality[index] - rightQuality[index];
  }
  const leftIndexedAt = new Date(left.indexed_at).getTime();
  const rightIndexedAt = new Date(right.indexed_at).getTime();
  const leftIndexedValue = Number.isFinite(leftIndexedAt) ? leftIndexedAt : Number.NEGATIVE_INFINITY;
  const rightIndexedValue = Number.isFinite(rightIndexedAt) ? rightIndexedAt : Number.NEGATIVE_INFINITY;
  if (leftIndexedValue !== rightIndexedValue) return leftIndexedValue - rightIndexedValue;
  return left.dataset_job_id.localeCompare(right.dataset_job_id);
}

function pickAuthoritativeResult(entries: IndexedResult[]): IndexedResult | undefined {
  let authoritative: IndexedResult | undefined;
  for (const entry of entries) {
    if (!authoritative || compareIndexedResults(entry, authoritative) > 0) authoritative = entry;
  }
  return authoritative;
}

function fmtAmount(v?: unknown): string {
  const n = Number(v);
  if (!Number.isFinite(n)) return String(v ?? "");
  return n.toLocaleString("zh-CN", { maximumFractionDigits: 6 });
}

function directionFor(row: Record<string, unknown>, target?: string): string {
  if (!target) return "—";
  const from = String(row.from_address ?? "").toLowerCase();
  const to = String(row.to_address ?? "").toLowerCase();
  const t = target.toLowerCase();
  if (from === t && to === t) return "SELF";
  if (to === t) return "IN";
  if (from === t) return "OUT";
  return "—";
}

function copyText(v: string) {
  void navigator.clipboard?.writeText(v).then(() => message.success("已复制"));
}

export default function SmartDownloadPage({ onOpenAddress, onNavigate }: SmartDownloadPageProps) {
  const [tab, setTab] = useState("create");
  const [refreshKey, setRefreshKey] = useState(0);
  const refreshTimer = useRef<number | null>(null);

  useEffect(() => {
    const es = new EventSource("/api/smart-download/events");
    es.onmessage = (e) => {
      if (e.data && e.data.includes("resync_required")) {
        // 事件缓冲过期：重新拉取快照（设计 §32）
        setRefreshKey((k) => k + 1);
        return;
      }
      if (tab === "create") return;
      if (refreshTimer.current !== null) window.clearTimeout(refreshTimer.current);
      refreshTimer.current = window.setTimeout(() => setRefreshKey((k) => k + 1), 400);
    };
    es.onerror = () => {
      /* EventSource 自动重连 */
    };
    return () => {
      es.close();
      if (refreshTimer.current !== null) window.clearTimeout(refreshTimer.current);
    };
  }, [tab]);

  const onBatchCreated = (batchId: string) => {
    setTab("tasks");
    setRefreshKey((k) => k + 1);
    void message.success("智能下载已启动，可在任务中心查看进度");
    void batchId;
  };

  return (
    <div className="smart-download-page">
      <header className="sd-page-header">
        <div>
          <Typography.Title level={3}>
            <ThunderboltOutlined /> 智能下载
          </Typography.Title>
          <Typography.Text>配置一次下载目标，系统自动完成资源选择、调度、校验和结果入库。</Typography.Text>
        </div>
      </header>
      <Tabs
        className="sd-main-tabs"
        activeKey={tab}
        onChange={setTab}
        items={[
          { key: "create", label: "创建下载", children: <CreateTab onCreated={onBatchCreated} /> },
          { key: "tasks", label: "任务中心", children: <TasksTab refreshKey={refreshKey} /> },
          { key: "prefetch", label: "智能预取", children: <PrefetchTab refreshKey={refreshKey} /> },
          {
            key: "results",
            label: "结果数据",
            children: (
              <ResultsTab refreshKey={refreshKey} onOpenAddress={onOpenAddress} onNavigate={onNavigate} />
            ),
          },
        ]}
      />
    </div>
  );
}

// ── 智能预取（Investigation Data Cache V2 设计 §51、§63-§64）──

function PrefetchTab({ refreshKey }: { refreshKey: number }) {
  const [status, setStatus] = useState<PrefetchStatus | null>(null);
  const [pinOpen, setPinOpen] = useState(false);
  const [pinning, setPinning] = useState(false);
  const [form] = Form.useForm();

  const load = () => {
    void getPrefetchStatus("default").then(setStatus);
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  const onUpgrade = async (row: PrefetchCandidateView) => {
    const res = await upgradePrefetch("default", "bsc", row.address);
    if (res) {
      void message.success("已升级为 Interactive，任务进度保持不变");
      load();
    }
  };

  const onPin = async () => {
    const values = await form.validateFields();
    setPinning(true);
    try {
      const res = await pinPrefetch("default", {
        chain_key: values.chain_key,
        address: values.address,
        reason: values.reason,
        from_block: values.from_block,
        to_block: values.to_block,
      });
      if (res) {
        void message.success("已加入 HOT 预取队列");
        setPinOpen(false);
        form.resetFields();
        load();
      }
    } finally {
      setPinning(false);
    }
  };

  const stats = status?.stats;
  const columns: ColumnsType<PrefetchCandidateView> = [
    {
      title: "地址",
      dataIndex: "address",
      width: 200,
      render: (v) => (
        <Tooltip title={v}>
          <code>{v}</code>
        </Tooltip>
      ),
    },
    {
      title: "优先级",
      dataIndex: "priority",
      width: 90,
      render: (v) => (
        <Tag color={v === "HOT" ? "red" : v === "WARM" ? "orange" : "default"}>
          {v}
        </Tag>
      ),
    },
    { title: "状态", dataIndex: "status", width: 110, render: (v) => <StatusPill status={v} /> },
    { title: "评分", dataIndex: "score", width: 80, render: (v) => (v ?? 0).toFixed(1) },
    {
      title: "原因",
      dataIndex: "reasons",
      render: (v: string[] | undefined) => (v ?? []).join(" / ") || "—",
    },
    {
      title: "区间",
      width: 160,
      render: (_, r) => `${r.from_block} - ${r.to_block}`,
    },
    {
      title: "Batch",
      dataIndex: "batch_id",
      width: 130,
      render: (v) => (v ? <code>{v.slice(0, 8)}…</code> : "—"),
    },
    {
      title: "操作",
      width: 140,
      render: (_, r) => (
        <Space size={4}>
          <Button
            size="small"
            type="primary"
            ghost
            disabled={!r.batch_id || r.status === "READY" || r.status === "EVICTED"}
            onClick={() => void onUpgrade(r)}
          >
            立即展开
          </Button>
          <Button size="small" icon={<ReloadOutlined />} onClick={load} />
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Space wrap style={{ marginBottom: 12 }}>
        <Card size="small" style={{ minWidth: 120 }}>
          <div className="sd-meta">总任务 {stats?.total_jobs ?? 0}</div>
        </Card>
        <Card size="small" style={{ minWidth: 120 }}>
          <div className="sd-meta">活动中 {stats?.active_jobs ?? 0}</div>
        </Card>
        <Card size="small" style={{ minWidth: 120 }}>
          <div className="sd-meta">已就绪 {stats?.ready_jobs ?? 0}</div>
        </Card>
        <Card size="small" style={{ minWidth: 140 }}>
          <div className="sd-meta">交互升级 {stats?.interactive_upgrades ?? 0}</div>
        </Card>
        <Card size="small" style={{ minWidth: 140 }}>
          <div className="sd-meta">
            命中率 {((stats?.feedback.hit_rate ?? 0) * 100).toFixed(1)}%
          </div>
        </Card>
        <Card size="small" style={{ minWidth: 160 }}>
          <div className="sd-meta">
            节省延迟 {(stats?.feedback.saved_latency_avg ?? 0).toFixed(1)}s/次
          </div>
        </Card>
        <Button type="primary" icon={<ThunderboltOutlined />} onClick={() => setPinOpen(true)}>
          Pin 地址
        </Button>
      </Space>
      <Table
        rowKey="id"
        size="small"
        dataSource={status?.candidates ?? []}
        columns={columns}
        scroll={{ x: 940 }}
        pagination={{ pageSize: 20 }}
        locale={{ emptyText: "暂无预取候选 — 使用「创建下载」或图扩展接口生成候选后自动出现" }}
      />
      <Modal
        open={pinOpen}
        onCancel={() => setPinOpen(false)}
        onOk={() => void onPin()}
        confirmLoading={pinning}
        title="Pin 预取地址（HOT）"
        width={520}
      >
        <Form form={form} layout="vertical" initialValues={{ chain_key: "bsc" }}>
          <Form.Item label="链" name="chain_key" rules={[{ required: true }]}>
            <Select options={chainOptions} />
          </Form.Item>
          <Form.Item label="地址" name="address" rules={[{ required: true, message: "请输入 EVM 地址" }]}>
            <Input placeholder="0x…" />
          </Form.Item>
          <Space size={12} style={{ display: "flex" }}>
            <Form.Item label="起始区块" name="from_block" rules={[{ required: true }]}>
              <InputNumber min={0} style={{ width: 200 }} />
            </Form.Item>
            <Form.Item label="结束区块" name="to_block" rules={[{ required: true }]}>
              <InputNumber min={1} style={{ width: 200 }} />
            </Form.Item>
          </Space>
          <Form.Item label="原因" name="reason">
            <Input placeholder="手工固定 / Agent 推荐" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}

// ── 创建下载 ──

function CreateTab({ onCreated }: { onCreated: (batchId: string) => void }) {
  const [form] = Form.useForm();
  const selectedChain = (Form.useWatch("chain_key", form) as string | undefined) ?? "bsc";
  const selectedMode = (Form.useWatch("mode", form) as DownloadMode | undefined) ?? "AUTO";
  const [importResult, setImportResult] = useState<ImportResult | null>(null);
  const [importing, setImporting] = useState(false);
  const [creating, setCreating] = useState(false);
  const [preflighting, setPreflighting] = useState(false);
  const [preflight, setPreflight] = useState<PreflightEstimate | null>(null);
  const [plannerPreview, setPlannerPreview] = useState<PlannerV2Preview | null>(null);
  const [preflightRequest, setPreflightRequest] = useState<CreateBatchRequest | null>(null);
  const [templates, setTemplates] = useState<TaskTemplate[]>([]);
  const [templateName, setTemplateName] = useState("");
  const [instantiatingTemplateId, setInstantiatingTemplateId] = useState<string | null>(null);
  const [availableDatasets, setAvailableDatasets] = useState<string[] | null>(null);
  const capabilityRequestSequence = useRef(0);
  const importRequestSequence = useRef(0);

  const capabilityDatasetOptions = useMemo(() => {
    const available = availableDatasets ? new Set(availableDatasets) : null;
    return datasetOptions.map((option) => {
      const disabled = available !== null && !available.has(option.value);
      return {
        ...option,
        disabled,
        label: disabled ? `${option.label}（当前不可用）` : option.label,
      };
    });
  }, [availableDatasets]);

  const refreshTemplates = () => void listTaskTemplates().then(setTemplates);

  useEffect(() => {
    refreshTemplates();
  }, []);

  useEffect(() => {
    const sequence = ++capabilityRequestSequence.current;
    setAvailableDatasets(null);
    void getSmartDownloadCapabilities(selectedChain, selectedMode).then((capabilities) => {
      if (sequence !== capabilityRequestSequence.current || !capabilities) return;
      setAvailableDatasets(capabilities.available_datasets);
      const available = new Set(capabilities.available_datasets);
      const current = (form.getFieldValue("datasets") as Dataset[] | undefined) ?? [];
      const supported = current.filter((dataset) => available.has(dataset));
      if (supported.length !== current.length) {
        form.setFieldValue("datasets", supported);
        setPreflight(null);
        setPlannerPreview(null);
        setPreflightRequest(null);
        void message.warning("已移除当前网络与执行模式下不可用的数据集");
      }
    });
    return () => {
      if (capabilityRequestSequence.current === sequence) capabilityRequestSequence.current += 1;
    };
  }, [form, selectedChain, selectedMode]);

  const handleFile = async (file: File) => {
    const sequence = ++importRequestSequence.current;
    setImporting(true);
    setImportResult(null);
    try {
      const res = await importAddressFile(file);
      if (sequence !== importRequestSequence.current || !res) return false;
      setImportResult(res);
      setPreflight(null);
      setPlannerPreview(null);
      setPreflightRequest(null);
      void message.success(
        `识别地址列「${res.selected_column}」：原始 ${res.rows} 行 → 任务地址 ${
          res.final_addresses?.length ?? res.valid
        } 个（有效 ${res.valid} / 重复 ${res.duplicates} / 无效 ${res.invalid}）`,
      );
    } catch (error) {
      if (sequence === importRequestSequence.current) {
        void message.error(smartDownloadErrorMessage(error, "地址导入失败，请检查文件格式（TXT/CSV/XLSX）"));
      }
    } finally {
      if (sequence === importRequestSequence.current) setImporting(false);
    }
    return false;
  };

  const onCreate = async (values: {
    chain_key: string;
    addresses?: string;
    datasets?: Dataset[];
    range_mode: "FULL" | "TIME" | "BLOCK";
    from_block?: number;
    to_block?: number;
    date_range?: [Dayjs, Dayjs];
    force_redownload?: boolean;
    mode: DownloadMode;
    priority: DownloadPriority;
    relevant_range_enabled?: boolean;
    reuse_relevant_range?: boolean;
    relevant_from_block?: number;
    relevant_to_block?: number;
    emergency_burst?: boolean;
    resource_profile: ResourceProfile;
  }) => {
    let addresses: string[] = importResult?.final_addresses ?? [];
    const parsed = parseAddressLines(values.addresses ?? "");
    const chainOverrides = parsed.overrides;
    if (addresses.length === 0 && values.addresses) {
      addresses = parsed.addresses;
    }
    if (addresses.length === 0) {
      void message.warning("请输入地址或上传地址文件");
      return;
    }
    if (!values.datasets || values.datasets.length === 0) {
      void message.warning("请至少选择一类数据");
      return;
    }
    const defaultRange: RangeSpec = { mode: values.range_mode };
    if (values.range_mode === "BLOCK") {
      defaultRange.from_block = Number(values.from_block) || 0;
      defaultRange.to_block = Number(values.to_block) || 0;
      if (defaultRange.from_block > defaultRange.to_block) {
        void message.warning("下载区间的起始区块不能大于结束区块");
        return;
      }
    }
    if (values.range_mode === "TIME") {
      const [from, to] = values.date_range ?? [];
      if (!from?.isValid() || !to?.isValid() || !from.isBefore(to)) {
        void message.warning("请选择有效的起止时间，且结束时间必须晚于开始时间");
        return;
      }
      defaultRange.start_time = from.toISOString();
      defaultRange.end_time = to.toISOString();
    }
    let relevantRange: RangeSpec | undefined;
    if (values.relevant_range_enabled) {
      if (values.reuse_relevant_range && defaultRange.mode !== "FULL") {
        relevantRange = { ...defaultRange };
      } else {
        const relevantFrom = Number(values.relevant_from_block);
        const relevantTo = Number(values.relevant_to_block);
        if (!Number.isFinite(relevantFrom) || !Number.isFinite(relevantTo) || relevantFrom < 0 || relevantTo < relevantFrom) {
          void message.warning("请输入有效的相关区块范围，且起始区块不能大于结束区块");
          return;
        }
        relevantRange = { mode: "BLOCK", from_block: relevantFrom, to_block: relevantTo };
      }
    }
    const request = {
      chain_key: values.chain_key,
      mode: values.mode,
      addresses,
      datasets: values.datasets,
      priority: values.priority,
      relevant_range: relevantRange,
      relevant_ranges: relevantRange ? [relevantRange] : undefined,
      emergency_burst: values.mode === "EMERGENCY" && Boolean(values.emergency_burst),
      burst_level: values.mode === "EMERGENCY" && values.emergency_burst
        ? "L3" as const
        : values.mode === "TURBO"
          ? "L2" as const
          : "L1" as const,
      resource_profile: values.resource_profile,
      default_range: defaultRange.mode === "FULL" ? undefined : defaultRange,
      skip_covered: !values.force_redownload,
      address_chain_overrides: Object.keys(chainOverrides).length > 0 ? chainOverrides : undefined,
    };
    setPreflightRequest(request);
    if (!preflight) {
      setPreflighting(true);
      try {
        const [estimate, nextPlannerPreview] = await Promise.all([
          preflightBatch(request),
          previewPlannerV2(request),
        ]);
        setPreflight(estimate);
        setPlannerPreview(nextPlannerPreview);
        if (!estimate) {
          void message.error("生产预检失败，未创建任务");
        } else if (estimate.blocked) {
          void message.error("资源保护策略已阻止启动，请调整范围或资源档位");
        } else {
          void message.success("生产预检通过，请确认估算后开始下载");
        }
      } finally {
        setPreflighting(false);
      }
      return;
    }
    if (preflight.blocked) return;
    setCreating(true);
    const analyzingKey = "sd-analyzing";
    try {
      const res = await createBatch(request);
      if (!res?.batch) {
        void message.error("创建失败");
        return;
      }
      // Discovery 反馈：先分析数据规模，再自动进入任务中心（不要求用户确认）
      message.loading({ key: analyzingKey, content: "正在分析数据规模…", duration: 0 });
      const plan = await planBatch(res.batch.id);
      const plannedDatasets = plan?.datasets ?? [];
      if (plannedDatasets.length > 0) {
        const buckets: Record<string, number> = { S: 0, M: 0, L: 0, XL: 0 };
        let rows = 0;
        let bytes = 0;
        for (const d of plannedDatasets) {
          rows += d.estimated_rows || 0;
          bytes += d.estimated_bytes || 0;
          buckets[d.size_class] = (buckets[d.size_class] ?? 0) + 1;
        }
        const hits = res.local_full_hits ?? 0;
        const partial = res.local_partial_hits ?? 0;
        const misses = res.local_misses ?? 0;
        const reused = res.reused_ranges ?? 0;
        message.loading({
          key: analyzingKey,
          content: `地址 ${addresses.length} 个 · 预计 ${Math.round(rows).toLocaleString()} 行 · ≈ ${(
            bytes /
            (1 << 30)
          ).toFixed(1)} GB · 小型 ${buckets.S} / 中型 ${buckets.M} / 大型 ${buckets.L} / 超大型 ${
            buckets.XL
          } · 完全命中 ${hits} / 部分命中 ${partial} / 需下载 ${misses} · 复用 ${reused} 个区间 · 系统将自动选择最合适的数据源`,
          duration: 0,
        });
      }
      await batchAction(res.batch.id, "start");
      message.success({ key: analyzingKey, content: "数据规模分析完成，任务已开始下载", duration: 3 });
      onCreated(res.batch.id);
      setPreflight(null);
      setPlannerPreview(null);
      setPreflightRequest(null);
    } catch (err) {
      message.destroy(analyzingKey);
      message.error(err instanceof Error ? err.message : "任务启动失败");
    } finally {
      setCreating(false);
    }
  };

  const loadTemplate = (template: TaskTemplate) => {
    const config = template.configuration ?? {};
    const range = config.default_range as RangeSpec | undefined;
    form.setFieldsValue({
      chain_key: template.chain_key ?? config.chain_key ?? "bsc",
      addresses: Array.isArray(config.addresses) ? config.addresses.join("\n") : "",
      datasets: template.datasets.length > 0 ? template.datasets : config.datasets,
      resource_profile: template.resource_profile,
      mode: config.mode ?? PROFILE_TO_MODE[template.resource_profile],
      priority: config.priority ?? "NORMAL",
      range_mode: range?.mode ?? "FULL",
      from_block: range?.from_block,
      to_block: range?.to_block,
      force_redownload: config.skip_covered === false,
      relevant_range_enabled: Boolean(config.relevant_range || config.relevant_ranges?.length),
      relevant_from_block: config.relevant_range?.from_block ?? config.relevant_ranges?.[0]?.from_block,
      relevant_to_block: config.relevant_range?.to_block ?? config.relevant_ranges?.[0]?.to_block,
      emergency_burst: Boolean(config.emergency_burst),
    });
    setImportResult(null);
    setPreflight(null);
    setPlannerPreview(null);
    setPreflightRequest(null);
    void message.success(`已加载模板「${template.name}」，请执行生产预检`);
  };

  const saveTemplate = async () => {
    const name = templateName.trim();
    if (!name) {
      void message.warning("请输入模板名称");
      return;
    }
    if (!preflightRequest) {
      void message.warning("请先执行生产预检，再保存已验证的任务配置");
      return;
    }
    const profile = preflightRequest.resource_profile ?? "STANDARD";
    const saved = await saveTaskTemplate({
      name,
      resource_profile: profile,
      configuration: preflightRequest,
    });
    if (!saved) return;
    setTemplateName("");
    refreshTemplates();
    void message.success("任务模板已保存");
  };

  const removeTemplate = async (template: TaskTemplate) => {
    if (!await deleteTaskTemplate(template.id)) return;
    refreshTemplates();
    void message.success(`已删除模板「${template.name}」`);
  };

  const instantiateTemplate = async (template: TaskTemplate) => {
    const request = template.configuration as CreateBatchRequest;
    setInstantiatingTemplateId(template.id);
    try {
      const estimate = await preflightBatch(request);
      if (!estimate) {
        void message.error("模板生产预检失败，未创建任务");
        return;
      }
      if (estimate.blocked) {
        void message.error(`模板已被资源保护策略阻止：${estimate.block_reasons.join("；") || "请检查资源状态"}`);
        return;
      }
      const batch = await instantiateTaskTemplate(template.id);
      if (!batch) {
        void message.error("模板实例化失败");
        return;
      }
      const started = await batchAction(batch.id, "start");
      if (!started) {
        void message.error("模板任务已创建，但启动失败，请到任务中心继续");
        onCreated(batch.id);
        return;
      }
      void message.success(`模板「${template.name}」预检通过，任务已启动`);
      onCreated(batch.id);
    } catch (error) {
      void message.error(smartDownloadErrorMessage(error, "模板实例化失败"));
    } finally {
      setInstantiatingTemplateId(null);
    }
  };

  return (
    <Card className="sd-create-card">
      <Form
        className="sd-create-form"
        form={form}
        layout="vertical"
        onFinish={onCreate}
        onValuesChange={() => { setPreflight(null); setPlannerPreview(null); setPreflightRequest(null); }}
		initialValues={{
          chain_key: "bsc",
          mode: "AUTO",
          resource_profile: "STANDARD",
          priority: "NORMAL",
          datasets: ["transactions", "token_transfers", "balances"],
          range_mode: "FULL",
          force_redownload: false,
          relevant_range_enabled: false,
          reuse_relevant_range: true,
          emergency_burst: false,
        }}
      >
        <div className="sd-template-bar">
          <div>
            <b>任务模板</b>
            <span>保存常用链、数据集、区间和资源档位；模板不会自动启动任务。</span>
          </div>
          <Space wrap>
            <Select
              allowClear
              placeholder="加载已有模板"
              style={{ width: 250 }}
              options={templates.map((template) => ({ value: template.id, label: template.name }))}
              onChange={(id) => {
                const template = templates.find((item) => item.id === id);
                if (template) loadTemplate(template);
              }}
            />
            <Input value={templateName} onChange={(event) => setTemplateName(event.target.value)} placeholder="新模板名称" style={{ width: 180 }} maxLength={80} />
            <Button onClick={() => void saveTemplate()}>保存当前配置</Button>
          </Space>
          {templates.length > 0 ? (
            <div className="sd-template-list">
              {templates.map((template) => (
                <Space.Compact key={template.id} className="sd-template-item">
                  <Tag closable onClose={(event) => { event.preventDefault(); void removeTemplate(template); }}>
                    {template.name} · {template.resource_profile}
                  </Tag>
                  <Button
                    size="small"
                    loading={instantiatingTemplateId === template.id}
                    disabled={instantiatingTemplateId !== null}
                    onClick={() => void instantiateTemplate(template)}
                  >
                    预检并创建
                  </Button>
                </Space.Compact>
              ))}
            </div>
          ) : null}
        </div>
        <section className="sd-form-section sd-form-section-primary">
          <div className="sd-form-section-head">
            <b>1. 下载对象</b>
            <span>填写地址或上传文件，支持同一批次处理多个地址。</span>
          </div>
        <Form.Item
          className="sd-address-field"
          label="地址"
          name="addresses"
          extra="多链支持：每行 0x... 后可跟空格加链名（如 0x… eth），缺省使用上方网络"
        >
          <Input.TextArea rows={5} placeholder={"0x...\n0x..."} />
        </Form.Item>
        <Space className="sd-upload-row" style={{ marginBottom: 8 }} wrap>
          <Upload
            accept=".csv,.xlsx,.xlsm,.txt"
            showUploadList={false}
            beforeUpload={(file: UploadFile) => {
              void handleFile(file as unknown as File);
              return false;
            }}
          >
            <Button icon={<UploadOutlined />} loading={importing}>
              上传 CSV / XLSX / TXT
            </Button>
          </Upload>
          {importResult && (
            <>
              <Tag color="blue">
                识别列「{importResult.selected_column}」 · 原始 {importResult.rows} 行 / 有效 {importResult.valid} /
                重复 {importResult.duplicates} / 无效 {importResult.invalid} / 最终任务地址{" "}
                {importResult.final_addresses?.length ?? importResult.valid} 个
              </Tag>
              <Button
                size="small"
                onClick={() => {
                  importRequestSequence.current += 1;
                  setImportResult(null);
                  setPreflight(null);
                  setPlannerPreview(null);
                  setPreflightRequest(null);
                  void message.success("已清除导入地址，可改用手工输入");
                }}
              >
                清除导入
              </Button>
            </>
          )}
        </Space>
        </section>
        <section className="sd-form-section">
          <div className="sd-form-section-head">
            <b>2. 调度策略</b>
            <span>选择网络、优先级与目标速度，其余资源由系统自动编排。</span>
          </div>
        <Space className="sd-config-row" size="large" wrap align="start">
          <Form.Item label="网络" name="chain_key" rules={[{ required: true }]}>
            <Select options={chainOptions} style={{ width: 240 }} />
          </Form.Item>
          <Form.Item
            label="任务优先级"
            name="priority"
            rules={[{ required: true }]}
            extra="URGENT 会抢占 NORMAL / BACKGROUND；完成后后台任务自动恢复"
          >
            <Select
              style={{ width: 210 }}
              options={[
                { value: "URGENT", label: "URGENT · 当前调查" },
                { value: "HIGH", label: "HIGH · 高优先" },
                { value: "NORMAL", label: "NORMAL · 普通" },
                { value: "BACKGROUND", label: "BACKGROUND · 后台" },
              ]}
            />
          </Form.Item>
        </Space>
        <Form.Item className="sd-profile-field" label="资源档位" name="resource_profile" rules={[{ required: true }]} extra="只选择目标速度，系统自动配置 Cloud、RPC 和 Writer 资源。">
          <Radio.Group className="sd-mode-selector">
            <Radio.Button value="STANDARD" onClick={() => form.setFieldValue("mode", "AUTO")}>
              <span className="sd-mode-option"><b>标准</b><small>STANDARD · 低资源，适合普通任务</small></span>
            </Radio.Button>
            <Radio.Button value="PERFORMANCE" onClick={() => form.setFieldValue("mode", "TURBO")}>
              <span className="sd-mode-option"><b>高性能</b><small>PERFORMANCE · 更多 Cloud / RPC，适合大批量任务</small></span>
            </Radio.Button>
            <Radio.Button value="EXTREME" onClick={() => form.setFieldValue("mode", "EMERGENCY")}>
              <span className="sd-mode-option"><b>极速</b><small>EXTREME · Turbo 时间优先，仍受全部 Guard 保护</small></span>
            </Radio.Button>
          </Radio.Group>
        </Form.Item>
        <Collapse
          ghost
          size="small"
          items={[{
            key: "mode-compat",
			label: "高级 · 兼容执行模式",
            children: (
              <Form.Item label="执行模式" name="mode" rules={[{ required: true }]} extra="保留 AUTO / TURBO / EMERGENCY 兼容；一般无需手动调整。">
                <Radio.Group options={["AUTO", "TURBO", "EMERGENCY"]} optionType="button" />
              </Form.Item>
            ),
          }]}
        />
        <Form.Item noStyle shouldUpdate={(prev, cur) => prev.mode !== cur.mode}>
          {({ getFieldValue }) => {
            const emergencyMode = getFieldValue("mode") === "EMERGENCY";
            return (
              <div className={`sd-emergency-control ${emergencyMode ? "is-active" : ""}`}>
                <div>
                  <b>紧急资源爆发</b>
                  <p>允许更多 Cloud jobs、更多 RPC workers、更高 Writer priority，并可暂停低优先级任务。</p>
                </div>
                <Form.Item name="emergency_burst" valuePropName="checked" style={{ margin: 0 }}>
                  <Switch disabled={!emergencyMode} checkedChildren="开启" unCheckedChildren="关闭" />
                </Form.Item>
                <div className="sd-cost-guard">
                  <b>Cost Guard 始终生效</b>
                  <span>即使开启 Emergency，Cloud Jobs 硬上限、RPC hard quota、磁盘保护与 ClickHouse 吞吐保护也不会解除；接入供应商预算指标后还会执行金额预算止损。</span>
                </div>
              </div>
            );
          }}
        </Form.Item>
        </section>
        <section className="sd-form-section">
          <div className="sd-form-section-head">
            <b>3. 数据与范围</b>
            <span>明确需要的数据集和下载边界，可选相关区间用于优先认证。</span>
          </div>
        <Form.Item className="sd-dataset-field" label="数据" name="datasets" rules={[{ required: true, message: "请至少选择一类数据" }]}>
          <Checkbox.Group options={capabilityDatasetOptions} />
        </Form.Item>
        <Form.Item className="sd-range-field" label="下载范围" name="range_mode">
          <Radio.Group>
            <Radio.Button value="FULL">全量</Radio.Button>
            <Radio.Button value="TIME">时间</Radio.Button>
            <Radio.Button value="BLOCK">区块</Radio.Button>
          </Radio.Group>
        </Form.Item>
        <Form.Item noStyle shouldUpdate={(prev, cur) => prev.range_mode !== cur.range_mode}>
          {({ getFieldValue }) => {
            const mode = getFieldValue("range_mode");
            if (mode === "TIME") {
              return (
                <Form.Item
                  name="date_range"
                  label="时间区间"
                  rules={[
                    { required: true, message: "请选择起止时间" },
                    {
                      validator: (_, value?: [Dayjs, Dayjs]) => {
                        if (!value || value.length !== 2 || !value[0]?.isValid() || !value[1]?.isValid()) {
                          return Promise.reject(new Error("请选择有效的起止时间"));
                        }
                        if (!value[0].isBefore(value[1])) {
                          return Promise.reject(new Error("结束时间必须晚于开始时间"));
                        }
                        return Promise.resolve();
                      },
                    },
                  ]}
                >
                  <DatePicker.RangePicker showTime />
                </Form.Item>
              );
            }
            if (mode === "BLOCK") {
              return (
                <Space>
                  <Form.Item name="from_block" label="起始区块" style={{ marginBottom: 0 }}>
                    <Input type="number" placeholder="如 40000000" />
                  </Form.Item>
                  <Form.Item name="to_block" label="结束区块" style={{ marginBottom: 0 }}>
                    <Input type="number" placeholder="如 45000000" />
                  </Form.Item>
                </Space>
              );
            }
            return null;
          }}
        </Form.Item>
        <Form.Item
          noStyle
          shouldUpdate={(prev, cur) =>
            prev.range_mode !== cur.range_mode ||
            prev.relevant_range_enabled !== cur.relevant_range_enabled ||
            prev.reuse_relevant_range !== cur.reuse_relevant_range
          }
        >
          {({ getFieldValue }) => {
            const enabled = Boolean(getFieldValue("relevant_range_enabled"));
            const rangeMode = getFieldValue("range_mode") as RangeSpec["mode"];
            const canReuse = rangeMode !== "FULL";
            const reuse = canReuse && Boolean(getFieldValue("reuse_relevant_range"));
            return (
              <div className="sd-relevant-range">
                <Form.Item name="relevant_range_enabled" valuePropName="checked" style={{ marginBottom: enabled ? 10 : 0 }}>
                  <Checkbox>
                    <b>设置当前相关区间</b>（优先完成并认证，用于缩短 TTFR）
                  </Checkbox>
                </Form.Item>
                {enabled ? (
                  <>
                    {canReuse ? (
                      <Form.Item name="reuse_relevant_range" valuePropName="checked" style={{ marginBottom: reuse ? 0 : 10 }}>
                        <Checkbox>复用上方下载区间作为相关区间</Checkbox>
                      </Form.Item>
                    ) : null}
                    {!reuse ? (
                      <Space wrap align="start">
                        <Form.Item name="relevant_from_block" label="相关起始区块" style={{ marginBottom: 0 }}>
                          <InputNumber min={0} precision={0} placeholder="如 94000000" />
                        </Form.Item>
                        <Form.Item name="relevant_to_block" label="相关结束区块" style={{ marginBottom: 0 }}>
                          <InputNumber min={0} precision={0} placeholder="如 95000000" />
                        </Form.Item>
                      </Space>
                    ) : (
                      <span className="sd-meta">将复用当前{rangeMode === "TIME" ? "时间" : "区块"}范围。</span>
                    )}
                  </>
                ) : null}
              </div>
            );
          }}
        </Form.Item>
        <Form.Item name="force_redownload" valuePropName="checked" style={{ marginBottom: 8 }}>
          <Checkbox>强制忽略本地缓存重新下载（默认自动复用本地已验证数据）</Checkbox>
        </Form.Item>
        </section>
        {preflight ? (
          <div className={`sd-preflight ${preflight.blocked ? "is-blocked" : "is-ready"}`}>
            <div className="sd-preflight-head">
              <div>
                <b>生产预检 {preflight.blocked ? "未通过" : "已通过"}</b>
                <span>置信度 {(preflight.confidence * 100).toFixed(0)}% · {preflight.resource_profile}</span>
              </div>
              <Space wrap>
                <GuardTag label="Storage" guard={preflight.guards.storage} />
                <GuardTag label="RPC Quota" guard={preflight.guards.rpc} />
                <GuardTag label="Cloud Budget" guard={preflight.guards.cloud} />
              </Space>
            </div>
            <div className="sd-preflight-grid">
              <TurboMetric label="预计区块" value={preflight.estimated_blocks.toLocaleString()} />
              <TurboMetric label="地址 / Dataset" value={`${preflight.address_count} / ${preflight.dataset_count}`} />
              <TurboMetric label="预计 Rows" value={preflight.estimated_rows.toLocaleString()} />
              <TurboMetric label="预计数据量" value={formatBytes(preflight.estimated_bytes)} />
              <TurboMetric label="Cloud Jobs" value={preflight.estimated_cloud_jobs.toLocaleString()} />
              <TurboMetric label="RPC Calls" value={preflight.estimated_rpc_calls.toLocaleString()} />
              <TurboMetric label="预计 ETA" value={formatDuration(preflight.estimated_eta_seconds)} />
              <TurboMetric label="磁盘增长" value={formatBytes(preflight.estimated_disk_growth_bytes)} />
            </div>
            <div className="sd-preflight-basis">
              <span>资源配置：Workers {preflight.profile.workers} · Cloud {preflight.profile.cloud_jobs} · RPC {preflight.profile.rpc_workers}</span>
              <span>估算依据：{preflight.basis.length > 0 ? preflight.basis.join(" / ") : "实时资源与历史性能样本"}</span>
              {preflight.block_reasons.length > 0 ? <b>{preflight.block_reasons.join("；")}</b> : null}
            </div>
          </div>
        ) : null}
        {plannerPreview ? (
          <div className="sd-planner-preview">
            <div className="sd-planner-head">
              <div>
                <b>批量规划器 V2 预览</b>
                <span>先合并地址、Range 与 Dataset，再交给 Provider；地址数不会直接放大成同等数量的下载任务。</span>
              </div>
              <Tag color="geekblue">WORKLOAD-CENTRIC</Tag>
            </div>
            <PlannerV2Metrics preview={plannerPreview} />
          </div>
        ) : null}
        <div className="sd-create-actions">
        <Button
          type="primary"
          htmlType="submit"
          icon={<CloudDownloadOutlined />}
          loading={creating || preflighting}
          disabled={Boolean(preflight?.blocked)}
        >
          {preflighting ? "正在执行生产预检…" : preflight ? "预检通过，开始智能下载" : "先执行生产预检"}
        </Button>
        <span>预检不会创建任务；通过后再次确认才开始下载。</span>
        </div>
      </Form>
    </Card>
  );
}

// ── 任务中心 ──

function TasksTab({ refreshKey }: { refreshKey: number }) {
  const [batches, setBatches] = useState<BatchJob[]>([]);
  const [selectedBatch, setSelectedBatch] = useState<string | null>(null);
  const [addresses, setAddresses] = useState<AddressJob[]>([]);
  const [addressesExpanded, setAddressesExpanded] = useState(false);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState<string | undefined>(undefined);
  const [batchPage, setBatchPage] = useState(1);
  const [batchPageSize, setBatchPageSize] = useState(10);
  const [detail, setDetail] = useState<AddressDetail | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [ledgers, setLedgers] = useState<Record<string, LedgerEntry[]>>({});
  const [batchSnap, setBatchSnap] = useState<BatchSnapshot | null>(null);
  const [summary, setSummary] = useState<BatchSummary | null>(null);
  const [turboStatus, setTurboStatus] = useState<TurboStatus | null>(null);
  const [hardening, setHardening] = useState<HardeningStatus | null>(null);
  const [accelerator, setAccelerator] = useState<BatchAcceleratorStatus | null>(null);
  const [jobReport, setJobReport] = useState<JobReport | null>(null);
  const [compareIds, setCompareIds] = useState<string[]>([]);
  const [compareResult, setCompareResult] = useState<CompareRunsResult | null>(null);
  const [comparableBatchIds, setComparableBatchIds] = useState<Set<string>>(() => new Set());
  const [compareError, setCompareError] = useState<string | null>(null);
  const [comparing, setComparing] = useState(false);
  const [switchingMode, setSwitchingMode] = useState(false);
  const batchListRequestSequence = useRef(0);
  const batchDetailRequestSequence = useRef(0);
  const addressListRequestSequence = useRef(0);
  const addressDetailRequestSequence = useRef(0);

  const load = () => {
    const sequence = ++batchListRequestSequence.current;
    void listBatches().then(async (items) => {
      const reports = await Promise.all(items.map((batch) =>
        ["COMPLETED", "PARTIAL", "FAILED", "CANCELED"].includes(batch.status)
          ? getBatchReport(batch.id)
          : Promise.resolve(null),
      ));
      if (sequence !== batchListRequestSequence.current) return;
      const nextComparableIds = new Set(
        items.filter((_, index) => reports[index] !== null).map((batch) => batch.id),
      );
      setBatches(items.map((batch, index) => {
        const report = reports[index];
        return report ? {
          ...batch,
          resource_profile: report.resource_profile ?? batch.resource_profile,
          rows: report.rows,
          duration_seconds: report.total_duration_seconds,
          ttfa_seconds: report.ttfa_seconds,
          average_throughput_rows_per_sec: report.average_throughput_rows_per_sec,
          result: report.certification ?? report.result ?? batch.status,
        } : batch;
      }));
      setComparableBatchIds(nextComparableIds);
      setCompareIds((current) => current.filter((id) => nextComparableIds.has(id)));
      setCompareResult(null);
      setCompareError(null);
    });
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  useEffect(() => {
    if (!selectedBatch) {
      batchDetailRequestSequence.current += 1;
      addressListRequestSequence.current += 1;
      setAddresses([]);
      setAddressesExpanded(false);
      setBatchSnap(null);
      setSummary(null);
      setTurboStatus(null);
      setHardening(null);
      setAccelerator(null);
      setJobReport(null);
      return;
    }
    const detailSequence = ++batchDetailRequestSequence.current;
    const selectedStatus = batches.find((batch) => batch.id === selectedBatch)?.status;
    const reportRequest = selectedStatus && ["COMPLETED", "PARTIAL", "FAILED", "CANCELED"].includes(selectedStatus)
      ? getBatchReport(selectedBatch)
      : Promise.resolve(null);
    void Promise.all([
      batchSnapshot(selectedBatch),
      batchSummary(selectedBatch),
      getTurboStatus(selectedBatch),
      getBatchHardening(selectedBatch),
      getBatchAccelerator(selectedBatch),
      reportRequest,
    ]).then(([snapshot, nextSummary, nextTurboStatus, nextHardening, nextAccelerator, nextReport]) => {
      if (detailSequence !== batchDetailRequestSequence.current) return;
      setBatchSnap(snapshot);
      setSummary(nextSummary);
      setTurboStatus(nextTurboStatus);
      setHardening(nextHardening);
      setAccelerator(nextAccelerator);
      setJobReport(nextReport);
    });
    if (addressesExpanded) {
      const addressSequence = ++addressListRequestSequence.current;
      void listBatchAddresses(selectedBatch, page, 50).then((res) => {
        if (addressSequence !== addressListRequestSequence.current) return;
        if (res) {
          setAddresses(res.addresses);
          setTotal(res.total);
        }
      });
    } else {
      addressListRequestSequence.current += 1;
    }
  }, [selectedBatch, page, refreshKey, addressesExpanded]);

  const runBatchAction = async (id: string, action: string) => {
    await batchAction(id, action);
    load();
  };

  const runModeSwitch = async (mode: DownloadMode) => {
    if (!selectedBatch) return;
    setSwitchingMode(true);
    try {
      const updated = await switchBatchMode(selectedBatch, mode);
      if (!updated) return;
      setBatches((current) => current.map((batch) => (batch.id === updated.id ? updated : batch)));
      setTurboStatus(await getTurboStatus(selectedBatch));
      void message.success(`已切换为 ${mode} 模式`);
    } finally {
      setSwitchingMode(false);
    }
  };

  const runCompare = async () => {
    if (compareIds.length !== 2) {
      void message.warning("请选择两个批次进行对比");
      return;
    }
    if (compareIds.some((id) => !comparableBatchIds.has(id))) {
      setCompareError("所选批次尚未生成终态报告，不能进行性能对比");
      return;
    }
    setComparing(true);
    setCompareError(null);
    try {
      const result = await compareBatchRuns(compareIds[0], compareIds[1]);
      if (!result || result.runs.length !== 2) throw new Error("后端未返回两个完整的运行报告");
      setCompareResult(result);
    } catch (error) {
      setCompareResult(null);
      setCompareError(smartDownloadErrorMessage(error, "任务对比失败"));
    } finally {
      setComparing(false);
    }
  };

  const runAddressAction = async (id: string, action: string) => {
    await addressAction(id, action);
    setRefresh();
  };

  const setRefresh = () => {
    // 地址操作后强制刷新当前页数据
    const sequence = ++addressListRequestSequence.current;
    void listBatchAddresses(selectedBatch ?? "", page, 50).then((res) => {
      if (sequence !== addressListRequestSequence.current) return;
      if (res) {
        setAddresses(res.addresses);
        setTotal(res.total);
      }
    });
  };

  const openDetail = async (addressId: string) => {
    const sequence = ++addressDetailRequestSequence.current;
    const d = await addressDetail(addressId);
    if (sequence !== addressDetailRequestSequence.current || !d) return;
    setDetail(d);
    setDetailOpen(true);
    const map: Record<string, LedgerEntry[]> = {};
    await Promise.all(
      d.datasets.map(async (dd) => {
        map[dd.dataset.id] = await datasetLedger(dd.dataset.id);
      }),
    );
    if (sequence !== addressDetailRequestSequence.current) return;
    setLedgers(map);
  };

  const hasProviderSwitch = Object.values(ledgers).some((list) =>
    list.some((e) => e.event === "PROVIDER_SWITCHED"),
  );

  useEffect(() => {
    if (detailOpen && hasProviderSwitch) {
      void message.warning("已自动切换下载方式：已完成数据不会重新下载，新 Provider 已从未完成区间继续");
    }
  }, [detailOpen, hasProviderSwitch]);

  const selectBatch = (batchId: string) => {
    setSelectedBatch(batchId);
    setAddressesExpanded(false);
    setAddresses([]);
    setTotal(0);
    setPage(1);
  };

  const filteredBatches = useMemo(
    () => (statusFilter ? batches.filter((batch) => batch.status === statusFilter) : batches),
    [batches, statusFilter],
  );
  const compareOptions = useMemo(
    () => batches
      .filter((batch) => comparableBatchIds.has(batch.id))
      .map((batch) => ({ value: batch.id, label: batchDisplayName(batch) })),
    [batches, comparableBatchIds],
  );
  const excludedComparisonCount = batches.length - compareOptions.length;

  const batchColumns: ColumnsType<BatchJob> = [
    {
      title: "任务",
      dataIndex: "id",
      width: 340,
      render: (v, r) => (
        <Tooltip title={v}>
          <span>{batchDisplayName(r)}</span>
          {r.prefetch && <Tag style={{ marginLeft: 8 }} color="purple">后台预取</Tag>}
        </Tooltip>
      ),
    },
    { title: "链", dataIndex: "chain_key", width: 70, render: (v) => v?.toUpperCase() },
    { title: "地址数", dataIndex: "address_count", width: 80 },
    { title: "档位", dataIndex: "resource_profile", width: 88, render: (v) => <ProfileTag profile={v} /> },
    { title: "模式", dataIndex: "mode", width: 100, render: (v) => <ModeTag mode={v} /> },
    { title: "Rows", dataIndex: "rows", width: 105, render: (v) => metricNumber(v).toLocaleString() },
    { title: "耗时", dataIndex: "duration_seconds", width: 90, render: (v) => formatDuration(v) },
    { title: "TTFA", dataIndex: "ttfa_seconds", width: 80, render: (v) => formatDuration(v) },
    { title: "平均吞吐", dataIndex: "average_throughput_rows_per_sec", width: 120, render: (v) => `${formatRate(v)} rows/s` },
    {
      title: "状态",
      dataIndex: "status",
      width: 110,
      render: (v) => <StatusPill status={v} />,
    },
    { title: "结果", dataIndex: "result", width: 105, render: (v, r) => <StatusPill status={v ?? r.status} /> },
    {
      title: "操作",
      width: 220,
      render: (_, r) => (
        <Space size={4}>
          <Tooltip title="查看地址任务">
            <Button size="small" onClick={() => selectBatch(r.id)}>
              地址
            </Button>
          </Tooltip>
          <Tooltip title={canResumeBatch(r.status) ? "继续" : "当前状态不可继续"}>
            <Button
              size="small"
              icon={<PlayCircleOutlined />}
              disabled={!canResumeBatch(r.status)}
              onClick={() => void runBatchAction(r.id, "resume")}
            />
          </Tooltip>
          <Tooltip title={canPauseBatch(r.status) ? "暂停全部" : "当前状态不可暂停"}>
            <Button
              size="small"
              icon={<PauseCircleOutlined />}
              disabled={!canPauseBatch(r.status)}
              onClick={() => void runBatchAction(r.id, "pause")}
            />
          </Tooltip>
          <Tooltip title="取消任务（保留已下载数据）">
            <Button
              size="small"
              danger
              icon={<StopOutlined />}
              disabled={TERMINAL_BATCH_STATUSES.has(r.status)}
              onClick={() => void runBatchAction(r.id, "cancel")}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  const currentBatch = batches.find((batch) => batch.id === selectedBatch);
  const modeSwitchDisabled = !currentBatch || TERMINAL_BATCH_STATUSES.has(currentBatch.status);

  const addressColumns: ColumnsType<AddressJob> = [
    {
      title: "地址",
      dataIndex: "address",
      width: 320,
      render: (v) => (
        <Tooltip title={v}>
          <code>{`${v.slice(0, 10)}…${v.slice(-6)}`}</code>
        </Tooltip>
      ),
    },
    {
      title: "状态",
      dataIndex: "status",
      width: 110,
      render: (v) => <StatusPill status={v} />,
    },
    {
      title: "当前数据",
      dataIndex: "current_dataset",
      width: 120,
      render: (v) => (v ? DATASET_LABELS[v] ?? v : "—"),
    },
    {
      title: "Provider",
      dataIndex: "current_provider",
      width: 130,
      render: (v, r) =>
        v ? (
          v === "local_hit" ? (
            <Tag color="green">LOCAL 已复用</Tag>
          ) : (
            <Tag color={r.cloud_tier ? "purple" : "blue"}>
              {v.toUpperCase()}
              {r.cloud_tier ? ` · ${r.cloud_tier}` : ""}
            </Tag>
          )
        ) : (
          "—"
        ),
    },
    {
      title: "总进度",
      width: 280,
      render: (_, r) => (
        <ProgressBar
          percent={r.progress?.percent}
          status={r.status}
          rows={r.progress?.rows_current}
          speed={r.progress?.speed_rows_per_sec}
          eta={r.progress?.eta_seconds}
        />
      ),
    },
    {
      title: "操作",
      width: 200,
      render: (_, r) => (
        <Space size={4}>
          <Tooltip title="查看详情 / Provider 历史 / 校验">
            <Button size="small" icon={<FileSearchOutlined />} onClick={() => void openDetail(r.id)}>
              详情
            </Button>
          </Tooltip>
          <Tooltip title={r.status === "DOWNLOADING" ? "暂停" : "当前状态不可暂停"}>
            <Button
              size="small"
              icon={<PauseCircleOutlined />}
              disabled={r.status !== "DOWNLOADING"}
              onClick={() => void runAddressAction(r.id, "pause")}
            />
          </Tooltip>
          <Tooltip title={r.status === "PAUSED" ? "继续" : "当前状态不可继续"}>
            <Button
              size="small"
              icon={<PlayCircleOutlined />}
              disabled={r.status !== "PAUSED"}
              onClick={() => void runAddressAction(r.id, "resume")}
            />
          </Tooltip>
          <Tooltip title="取消（保留已下载数据）">
            <Button
              size="small"
              danger
              icon={<StopOutlined />}
              disabled={TERMINAL_ADDRESS_STATUSES.has(r.status)}
              onClick={() => void runAddressAction(r.id, "cancel")}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  return (
    <div className="sd-task-center">
      <Card
		title={`最近任务（${batches.length}）`}
        extra={
          <Space>
            <Select
              allowClear
              placeholder="状态筛选"
              style={{ width: 140 }}
              value={statusFilter}
              onChange={(status) => {
                setStatusFilter(status);
                setBatchPage(1);
              }}
              options={BATCH_STATUS_FILTER_OPTIONS}
            />
            <Button icon={<ReloadOutlined />} onClick={load}>
              刷新
            </Button>
          </Space>
        }
      >
        <Table
          rowKey="id"
          columns={batchColumns}
          dataSource={filteredBatches}
          scroll={{ x: 1500 }}
          pagination={{
            current: batchPage,
            pageSize: batchPageSize,
            showSizeChanger: true,
            showTotal: (totalCount) => `共 ${totalCount} 个任务`,
            onChange: (nextPage, nextPageSize) => {
              setBatchPage(nextPage);
              setBatchPageSize(nextPageSize);
            },
          }}
          size="small"
          onRow={(r) => ({ onClick: () => selectBatch(r.id), style: { cursor: "pointer" } })}
        />
      </Card>
	  <Card className="sd-compare-card" size="small" title="运行对比" style={{ marginTop: 16 }}>
        <Space wrap>
          <Select
            mode="multiple"
            maxTagCount={2}
            value={compareIds}
            placeholder="选择两个批次"
            style={{ minWidth: 460 }}
            options={compareOptions}
            onChange={(ids) => {
              setCompareIds(ids.slice(-2));
              setCompareResult(null);
              setCompareError(null);
            }}
          />
          <Button disabled={compareIds.length !== 2} loading={comparing} onClick={() => void runCompare()}>对比运行</Button>
        </Space>
        <Typography.Text className="sd-compare-hint" type="secondary">
          仅终态且已生成运行报告的批次可比较
          {excludedComparisonCount > 0 ? `；已排除 ${excludedComparisonCount} 个不可比较批次` : ""}
        </Typography.Text>
        {compareOptions.length < 2 ? (
          <Alert
            className="sd-inline-feedback"
            type="info"
            showIcon
            message="暂无足够的可比较批次"
            description="至少需要两个已结束并成功生成运行报告的批次。"
          />
        ) : null}
        {compareError ? (
          <Alert
            className="sd-inline-feedback"
            type="error"
            showIcon
            closable
            message="运行对比失败"
            description={compareError}
            onClose={() => setCompareError(null)}
          />
        ) : null}
        {compareResult ? (
          <Table
            style={{ marginTop: 12 }}
            rowKey="batch_id"
            size="small"
            pagination={false}
            dataSource={compareResult.runs}
            columns={[
              { title: "Run", dataIndex: "label", render: (v, r) => v ?? abbr(r.batch_id) },
              { title: "档位", dataIndex: "resource_profile", render: (v) => <ProfileTag profile={v} /> },
              { title: "Rows", dataIndex: "rows", render: (v) => metricNumber(v).toLocaleString() },
              { title: "TTFA", dataIndex: "ttfa_seconds", render: (v) => formatDuration(v) },
              { title: "总耗时", dataIndex: "total_duration_seconds", render: (v) => formatDuration(v) },
              { title: "平均吞吐", dataIndex: "average_throughput_rows_per_sec", render: (v) => `${formatRate(v)} rows/s` },
              { title: "失败率", dataIndex: "failure_rate", render: (v) => `${(metricNumber(v) * (metricNumber(v) <= 1 ? 100 : 1)).toFixed(2)}%` },
            ]}
          />
        ) : null}
      </Card>
      {selectedBatch && (
        <>
          {currentBatch && (
            <Card size="small" style={{ marginTop: 16 }}>
              <Space size="middle" wrap>
                <span><b>一键升档</b></span>
                <Radio.Group
                  size="small"
                  optionType="button"
                  buttonStyle="solid"
                  value={currentBatch.mode ?? "AUTO"}
                  disabled={modeSwitchDisabled || switchingMode}
                  onChange={(event) => void runModeSwitch(event.target.value as DownloadMode)}
                  options={[
                    { label: "AUTO", value: "AUTO" },
                    { label: "TURBO", value: "TURBO" },
                    { label: "EMERGENCY", value: "EMERGENCY" },
                  ]}
                />
                <span className="sd-meta">
                  {switchingMode
                    ? "切换中…"
                    : currentBatch.mode === "EMERGENCY"
                      ? "紧急弹性爆发；已完成 Coverage 永不重做，相关区间认证后可自动降档"
                      : currentBatch.mode === "TURBO"
                        ? "SQD Cloud 与 RPC 真双通道并行，动态 Range 分配"
                        : "系统按覆盖、速度、成本与可靠性自动调度"}
                </span>
                {currentBatch.mode_switched_at ? <span className="sd-meta">切换于 {new Date(currentBatch.mode_switched_at).toLocaleString()}</span> : null}
              </Space>
            </Card>
          )}
          {summary && (
            <Card size="small" style={{ marginTop: 16 }}>
              <Space size="large" wrap>
                <span>
                  总进度 <b>{Math.round(summary.snapshot.progress_percent * 100)}%</b>
                </span>
                <span>
                  地址完成 <b>{summary.snapshot.ranges_current ?? 0}</b> / {summary.total}
                </span>
                <span>运行中 {summary.running}</span>
                <span>排队 {summary.queued}</span>
                <span className={summary.attention > 0 ? "sd-status-pill is-partial" : ""}>
                  需关注 {summary.attention}
                </span>
                <span>实时吞吐 {Math.round(summary.throughput_rows_per_sec).toLocaleString()} rows/s</span>
                <span>ETA {summary.snapshot.eta?.seconds ? `${Math.round(summary.snapshot.eta.seconds)}s` : "—"}</span>
                <Space>
                  <Tooltip title="暂停全部">
                    <Button
                      size="small"
                      icon={<PauseCircleOutlined />}
                      disabled={!canPauseBatch(currentBatch?.status)}
                      onClick={() => void runBatchAction(selectedBatch, "pause")}
                    />
                  </Tooltip>
                  <Tooltip title="继续全部">
                    <Button
                      size="small"
                      icon={<PlayCircleOutlined />}
                      disabled={!canResumeBatch(currentBatch?.status)}
                      onClick={() => void runBatchAction(selectedBatch, "resume")}
                    />
                  </Tooltip>
                  <Tooltip title="取消任务（保留已下载数据）">
                    <Button
                      size="small"
                      danger
                      icon={<StopOutlined />}
                      disabled={!currentBatch || TERMINAL_BATCH_STATUSES.has(currentBatch.status)}
                      onClick={() => void runBatchAction(selectedBatch, "cancel")}
                    />
                  </Tooltip>
                </Space>
              </Space>
            </Card>
          )}
          {turboStatus && (
            <TurboDashboard
              status={{
                ...turboStatus,
                mode: currentBatch?.mode ?? turboStatus.mode,
                priority: currentBatch?.priority ?? turboStatus.priority,
                burst_active: currentBatch?.emergency_burst ?? turboStatus.burst_active,
                burst_level: currentBatch?.burst_level ?? turboStatus.burst_level,
                eta_seconds: turboStatus.eta_seconds || summary?.snapshot.eta?.seconds,
              }}
            />
          )}
          {hardening ? <HardeningPanel status={hardening} report={jobReport} /> : null}
          {accelerator ? <AcceleratorPanel accelerator={accelerator} /> : null}
          {!hardening && jobReport ? (
            <Card size="small" title="Job Report" style={{ marginTop: 16 }}>
              <Space wrap size="large">
                <span>Rows <b>{jobReport.rows.toLocaleString()}</b></span>
                <span>Coverage <b>{jobReport.coverage.toFixed(2)}%</b></span>
                <span>Duplicates <b>{jobReport.duplicates}</b></span>
                <span>TTFA <b>{formatDuration(jobReport.ttfa_seconds)}</b></span>
                <span>总耗时 <b>{formatDuration(jobReport.total_duration_seconds)}</b></span>
                <span>平均吞吐 <b>{formatRate(jobReport.average_throughput_rows_per_sec)} rows/s</b></span>
                <StatusPill status={jobReport.certification ?? jobReport.result} />
              </Space>
            </Card>
          ) : null}
          <Collapse
            className="sd-address-on-demand"
            onChange={(keys) => setAddressesExpanded((Array.isArray(keys) ? keys : [keys]).includes("addresses"))}
            items={[{
              key: "addresses",
              label: (
              <Space>
                <b>单地址任务（按需展开{total > 0 ? ` · 共 ${total}` : ""}）</b>
                {batchSnap && (
                  <span style={{ width: 260, display: "inline-block" }}>
                    <ProgressBar
                      percent={batchSnap.progress_percent}
                      status={batchSnap.status}
                      rows={batchSnap.rows_current}
                      eta={batchSnap.eta?.seconds}
                    />
                  </span>
                )}
              </Space>
              ),
              children: (
                <Table
                  rowKey="id"
                  columns={addressColumns}
                  dataSource={addresses}
                  size="small"
                  loading={addressesExpanded && addresses.length === 0 && total === 0}
                  onRow={(r) => ({ onClick: () => void openDetail(r.id), style: { cursor: "pointer" } })}
                  pagination={{ current: page, pageSize: 50, total, onChange: setPage, showSizeChanger: false }}
                />
              ),
            }]}
          />
        </>
      )}
      <Drawer
        className="sd-drawer"
        title="地址详情"
        width={760}
        open={detailOpen}
        onClose={() => {
          addressDetailRequestSequence.current += 1;
          setDetailOpen(false);
        }}
      >
        {detail && (
          <Space direction="vertical" style={{ width: "100%" }} size="middle">
            <Space wrap>
              <code>{detail.address.address}</code>
              <StatusPill status={detail.address.status} />
              <span>链 {detail.address.chain_key?.toUpperCase()}</span>
              <span>
                范围 {detail.address.range?.mode ?? "FULL"}
                {detail.address.range?.from_block != null
                  ? ` ${detail.address.range.from_block} - ${detail.address.range.to_block ?? "∞"}`
                  : ""}
              </span>
              <span>
                ETA{" "}
                {detail.address.progress?.eta_seconds ? `${Math.round(detail.address.progress.eta_seconds)}s` : "—"}
              </span>
              <span>已下载 {detail.address.progress?.rows_current?.toLocaleString() ?? 0} 行</span>
              <div style={{ width: 220 }}>
                <ProgressBar
                  percent={detail.address.progress?.percent}
                  status={detail.address.status}
                  rows={detail.address.progress?.rows_current}
                  speed={detail.address.progress?.speed_rows_per_sec}
                  eta={detail.address.progress?.eta_seconds}
                />
              </div>
            </Space>
            {detail.datasets.map((dd) => (
              <Card
                key={dd.dataset.id}
                size="small"
                className="sd-dataset-card"
                title={
                  <Space>
                    {DATASET_LABELS[dd.dataset.dataset] ?? dd.dataset.dataset}
                    <StatusPill status={dd.dataset.status} />
                    {dd.dataset.current_provider === "local_hit" ? (
                      <Tag color="green">LOCAL 已复用</Tag>
                    ) : (
                      <Tag>{dd.dataset.current_provider ?? "—"}</Tag>
                    )}
                    {dd.dataset.current_provider === "sqd_cloud" && dd.dataset.cloud_tier && (
                      <Tag color="purple">
                        资源 {dd.dataset.cloud_tier === "XL" ? "高性能" : "标准"}（{dd.dataset.cloud_tier}）
                      </Tag>
                    )}
                    {dd.dataset.discovery_confidence ? (
                      <Tag color="cyan">
                        预计 {(dd.dataset.estimated_rows ?? 0).toLocaleString()} 行 · 置信{" "}
                        {confidenceLabel(dd.dataset.discovery_confidence)}
                      </Tag>
                    ) : null}
                    {dd.dataset.repair_rounds ? <Tag color="orange">补洞 ×{dd.dataset.repair_rounds}</Tag> : null}
                  </Space>
                }
              >
                <ProgressBar
                  percent={dd.dataset.progress?.percent}
                  status={dd.dataset.status}
                  rows={dd.dataset.downloaded_rows}
                  speed={dd.dataset.progress?.speed_rows_per_sec}
                  eta={dd.dataset.progress?.eta_seconds}
                />
                {dd.dataset.validation && (
                  <div className="sd-validation-note">
                    校验 {dd.dataset.validation.status} · 完整性 {(dd.dataset.validation.coverage * 100).toFixed(2)}% ·
                    唯一 {dd.dataset.validation.unique_key_count} · 重复 {dd.dataset.validation.duplicate_count} · Score{" "}
                    {dd.dataset.validation.score} · Provider {dd.dataset.validation.expected_count}/
                    {dd.dataset.validation.actual_count} · 缺口{" "}
                    {(dd.dataset.validation as { gaps?: Array<unknown> }).gaps?.length ?? 0}
                  </div>
                )}
                <Table
                  rowKey="id"
                  size="small"
                  pagination={false}
                  dataSource={dd.ranges}
                  columns={[
                    { title: "Range", render: (_, r) => `${r.from_block} - ${r.to_block}` },
                    {
                      title: "状态",
                      dataIndex: "status",
                      render: (v) => <StatusPill status={v} />,
                    },
                    { title: "Provider", dataIndex: "provider", width: 100 },
                    { title: "行数", dataIndex: "rows_committed", width: 90 },
                    { title: "尝试", dataIndex: "attempts", width: 70 },
                    { title: "错误", dataIndex: "error", ellipsis: true },
                  ]}
                />
                {ledgers[dd.dataset.id] && ledgers[dd.dataset.id].length > 0 && (
                  <div className="sd-ledger">
                    <Timeline
                      items={ledgers[dd.dataset.id].slice(-12).map((e) => ({
                        color: e.event.includes("FAILED") ? "red" : e.event.includes("COMPLETED") ? "green" : "blue",
                        children: (
                          <span>
                            {e.event} {e.range_id ? `[${e.range_id}]` : ""} {e.provider ? `· ${e.provider}` : ""}{" "}
                            {e.rows ? `· ${e.rows} 行` : ""}
                            {e.error ? `· ${e.error}` : ""}
                          </span>
                        ),
                      }))}
                    />
                  </div>
                )}
              </Card>
            ))}
          </Space>
        )}
      </Drawer>
    </div>
  );
}

// ── 结果数据 ──

function ResultsTab({
  refreshKey,
  onOpenAddress,
  onNavigate,
}: {
  refreshKey: number;
  onOpenAddress: (address: string) => void;
  onNavigate: (page: string) => void;
}) {
  const [registry, setRegistry] = useState<IndexedResult[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [summary, setSummary] = useState<Record<string, unknown> | null>(null);
  const [rows, setRows] = useState<Array<Record<string, unknown>>>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [filter, setFilter] = useState("");
  const [loading, setLoading] = useState(false);
  const [resultsError, setResultsError] = useState<string | null>(null);
  const registryRequestSequence = useRef(0);
  const resultRequestSequence = useRef(0);

  const loadRegistry = () => {
    const sequence = ++registryRequestSequence.current;
    void listRegistry().then((list) => {
      if (sequence !== registryRequestSequence.current) return;
      setRegistry(list);
      setSelected((current) => {
        const currentEntry = list.find((entry) => entry.dataset_job_id === current);
        const candidates = currentEntry
          ? list.filter(
              (entry) =>
                entry.chain_key === currentEntry.chain_key &&
                entry.address === currentEntry.address &&
                entry.dataset === currentEntry.dataset,
            )
          : list;
        return pickAuthoritativeResult(candidates)?.dataset_job_id ?? null;
      });
    });
  };

  useEffect(() => {
    loadRegistry();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  useEffect(() => {
    const sequence = ++resultRequestSequence.current;
    if (!selected) {
      setSummary(null);
      setRows([]);
      setTotal(0);
      setLoading(false);
      setResultsError(null);
      return;
    }
    setLoading(true);
    setResultsError(null);
    void Promise.all([
      resultSummary(selected).catch(() => null),
      queryResults(selected, page, 50, "", filter),
    ]).then(([nextSummary, res]) => {
      if (sequence !== resultRequestSequence.current) return;
      setSummary(nextSummary);
      if (res) {
        setRows(res.rows);
        setTotal(res.total);
      } else {
        setRows([]);
        setTotal(0);
      }
    }).catch((error: unknown) => {
      if (sequence !== resultRequestSequence.current) return;
      setRows([]);
      setTotal(0);
      setResultsError(smartDownloadErrorMessage(error, "查询结果失败"));
    }).finally(() => {
      if (sequence === resultRequestSequence.current) setLoading(false);
    });
    return () => {
      if (resultRequestSequence.current === sequence) resultRequestSequence.current += 1;
    };
  }, [selected, page, filter, refreshKey]);

  const entry = registry.find((r) => r.dataset_job_id === selected);
  const datasetTotal = entry?.row_count ?? 0;
  const summaryCoverageRaw = Number(
    summary?.validation && (summary.validation as { coverage?: number }).coverage,
  );
  const summaryCoverage = Number.isFinite(summaryCoverageRaw)
    ? Math.max(0, Math.min(1, summaryCoverageRaw))
    : null;

  // 按 地址 → 数据集 → 合并覆盖 分组（设计 §12-§13）
  const grouped = useMemo(() => {
    const map = new Map<
      string,
      {
        address: string;
        chain_key: string;
        datasets: Map<
          string,
          {
            name: string;
            from: number;
            to: number;
            rows: number;
            validation: string;
            certification?: string;
            latest: string;
            authoritative: IndexedResult;
          }
        >;
      }
    >();
    for (const e of registry) {
      const groupKey = `${e.chain_key}:${e.address}`;
      let g = map.get(groupKey);
      if (!g) {
        g = { address: e.address, chain_key: e.chain_key, datasets: new Map() };
        map.set(groupKey, g);
      }
      const current = g.datasets.get(e.dataset);
      if (!current || compareIndexedResults(e, current.authoritative) > 0) {
        g.datasets.set(e.dataset, {
          name: e.dataset,
          from: e.from_block,
          to: e.to_block,
          rows: e.row_count,
          validation: e.validation,
          certification: e.certification,
          latest: e.dataset_job_id,
          authoritative: e,
        });
      }
    }
    return [...map.values()].map((g) => ({ ...g, datasets: [...g.datasets.values()] }));
  }, [registry]);

  const hashRender = (v: unknown) => (
    <Tooltip title={String(v ?? "")}>
      <a onClick={() => copyText(String(v ?? ""))}>{abbr(v)}</a>
    </Tooltip>
  );

  const columns = useMemo<ColumnsType<Record<string, unknown>>>(() => {
    const ds = entry?.dataset ?? "";
    const target = entry?.address ?? "";
    const common: ColumnsType<Record<string, unknown>> = [
      { title: "时间", key: "time", render: (_, r) => fmtTime(r.block_time) },
      { title: "区块", dataIndex: "block_number", width: 90, render: (v) => String(v ?? "") },
      { title: "Tx Hash", key: "tx", render: (_, r) => hashRender(r.transaction_hash) },
    ];
    const addr = (key: string): NonNullable<ColumnsType<Record<string, unknown>>[number]> => ({
      title: key === "from_address" ? "From" : key === "to_address" ? "To" : key,
      dataIndex: key,
      width: 150,
      render: (v: unknown) => (
        <Tooltip title={String(v ?? "")}>
          <a onClick={() => copyText(String(v ?? ""))}>{abbr(v)}</a>
        </Tooltip>
      ),
    });
    const provider: NonNullable<ColumnsType<Record<string, unknown>>[number]> = {
      title: "Provider",
      dataIndex: "source_provider",
      width: 90,
      render: (v: unknown) => (v ? String(v).toUpperCase() : "—"),
    };
    switch (ds) {
      case "token_transfers":
        return [
          ...common,
          { title: "方向", key: "direction", width: 70, render: (_, r) => <Tag>{directionFor(r, target)}</Tag> },
          addr("from_address"),
          addr("to_address"),
          { title: "Token", dataIndex: "token_address", width: 140, render: (v) => hashRender(v) },
          { title: "Amount", dataIndex: "value_raw", render: (v) => <Tooltip title={`raw: ${String(v ?? "")}`}>{fmtAmount(v)}</Tooltip> },
          { title: "Log Index", dataIndex: "log_index", width: 90 },
          provider,
        ];
      case "transactions":
        return [
          ...common,
          { title: "方向", key: "direction", width: 70, render: (_, r) => <Tag>{directionFor(r, target)}</Tag> },
          addr("from_address"),
          addr("to_address"),
          { title: "Value", dataIndex: "value_raw", render: (v) => fmtAmount(v) },
          { title: "Method", dataIndex: "method_id", width: 100 },
          { title: "Status", dataIndex: "status", width: 80 },
          provider,
        ];
      case "balances":
        return [
          { title: "地址", dataIndex: "address", render: (v) => hashRender(v) },
          { title: "余额", dataIndex: "balance", render: (v) => fmtAmount(v) },
          { title: "Symbol", dataIndex: "symbol", width: 100 },
          provider,
        ];
      case "logs":
        return [
          ...common,
          { title: "Log Index", dataIndex: "log_index", width: 90 },
          { title: "合约", dataIndex: "contract_address", width: 150, render: (v) => hashRender(v) },
          { title: "Topics", dataIndex: "topics", ellipsis: true },
          provider,
        ];
      default:
        if (rows.length === 0) return [];
        const keys = Object.keys(rows[0]).filter(
          (k) => !["source_provider", "source_range_id", "ingested_at", "block_time", "chain_id"].includes(k),
        );
        return [
          { title: "时间", key: "time", render: (_, r) => fmtTime(r.block_time) },
          ...keys.map((k) => ({
            title: k,
            dataIndex: k,
            ellipsis: true,
            render: (v: unknown) => (typeof v === "object" ? JSON.stringify(v) : String(v ?? "")),
          })),
        ];
    }
  }, [rows, entry]);

  return (
    <Space direction="vertical" style={{ width: "100%" }} size="middle">
      <Card
        title="已入库结果"
        extra={
          <Space>
            <Input.Search
              placeholder="筛选 from_address:0x…"
              allowClear
              style={{ width: 260 }}
              onSearch={(value) => {
                setFilter(value);
                setPage(1);
              }}
            />
            <Button
              icon={<ReloadOutlined />}
              onClick={loadRegistry}
            >
              刷新
            </Button>
          </Space>
        }
      >
        <Table
          rowKey={(group) => `${group.chain_key}:${group.address}`}
          size="small"
          pagination={false}
          dataSource={grouped}
          scroll={{ x: 620 }}
          expandable={{
            expandedRowRender: (g) => (
              <Table
                rowKey="name"
                size="small"
                pagination={false}
                dataSource={g.datasets}
                scroll={{ x: 880 }}
                columns={[
                  { title: "数据集", dataIndex: "name", render: (v) => DATASET_LABELS[v] ?? v },
                  { title: "覆盖区间", render: (_, d) => `${d.from} → ${d.to}` },
                  { title: "行数", dataIndex: "rows", width: 110 },
                  {
                    title: "校验",
                    dataIndex: "validation",
                    width: 110,
                    render: (v) => <StatusPill status={v} />,
                  },
                  {
                    title: "认证",
                    dataIndex: "certification",
                    width: 110,
                    render: (v) => (v ? <StatusPill status={v === "CERTIFIED" ? "VALIDATED" : "PARTIAL"} /> : "—"),
                  },
                  {
                    title: "操作",
                    width: 260,
                    render: (_, d) => (
                      <Space size={4}>
                        <Button size="small" onClick={() => { setSelected(d.latest); setPage(1); }}>
                          查看
                        </Button>
                        <Button size="small" onClick={() => onOpenAddress(g.address)}>
                          地址画像
                        </Button>
                        <Button size="small" onClick={() => onNavigate("analytics-graph")}>
                          关系图
                        </Button>
                        <Button size="small" onClick={() => onNavigate("intelligence")}>
                          调查
                        </Button>
                      </Space>
                    ),
                  },
                ]}
              />
            ),
          }}
          columns={[
            {
              title: "地址",
              dataIndex: "address",
              ellipsis: true,
              render: (v) => (
                <Tooltip title={v}>
                  <code>{abbr(v)}</code>
                </Tooltip>
              ),
            },
            { title: "数据集数", render: (_, g) => g.datasets.length, width: 90 },
            { title: "链", dataIndex: "chain_key", width: 70, render: (v) => v?.toUpperCase() },
            {
              title: "操作",
              width: 120,
              render: (_, g) => (
                <Button
                  size="small"
                  onClick={() => {
                    const d = g.datasets[0];
                    if (d) {
                      setSelected(d.latest);
                      setPage(1);
                    }
                  }}
                >
                  查看数据
                </Button>
              ),
            },
          ]}
        />
      </Card>
      {entry && (
        <Card
          title={
            <Space>
              <DatabaseOutlined /> {DATASET_LABELS[entry.dataset] ?? entry.dataset} ·{" "}
              <code>{entry.address}</code>
              {summary ? (
                <span className="sd-status-pill is-validated">
                  完整性 {summaryCoverage === null ? "—" : `${(summaryCoverage * 100).toFixed(2)}%`}
                  · {datasetTotal.toLocaleString()} 行
                </span>
              ) : null}
            </Space>
          }
          extra={
            <Space>
              <span className="sd-status-pill is-validated">
                {datasetTotal > 300000 ? "CSV（>30 万行）" : "XLSX（≤30 万行）"}
              </span>
              <Button
                type="primary"
                icon={<DownloadOutlined />}
                onClick={() => downloadResultExport(entry.dataset_job_id)}
              >
                导出数据
              </Button>
            </Space>
          }
        >
          {resultsError ? (
            <Alert
              className="sd-inline-feedback"
              type="error"
              showIcon
              closable
              message="结果加载失败"
              description={resultsError}
              onClose={() => setResultsError(null)}
            />
          ) : null}
          <Table
            rowKey={(r) =>
              (r.transaction_hash
                ? `${String(r.transaction_hash)}:${String(r.log_index ?? r.trace_address ?? "")}`
                : undefined) ||
              (r.address as string) ||
              `${r.block_number}-${r.log_index}` ||
              JSON.stringify(r).slice(0, 32)
            }
            loading={loading}
            size="small"
            dataSource={rows}
            columns={columns}
            locale={{ emptyText: filter ? `没有符合筛选「${filter}」的数据` : "当前结果没有数据" }}
            scroll={{ x: "max-content" }}
            pagination={{
              current: page,
              pageSize: 50,
              total,
              onChange: setPage,
              showSizeChanger: false,
            }}
          />
        </Card>
      )}
    </Space>
  );
}
