// 智能下载统一入口（Phase 4）：创建下载 / 任务中心 / 结果数据。
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Checkbox,
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
  createBatch,
  datasetLedger,
  downloadResultExport,
  expandGraphCache,
  getPrefetchStats,
  getPrefetchStatus,
  importAddressFile,
  listBatchAddresses,
  listBatches,
  listRegistry,
  pinPrefetch,
  planBatch,
  queryResults,
  resultSummary,
  upgradePrefetch,
  type AddressDetail,
  type AddressJob,
  type BatchSummary,
  type BatchSnapshot,
  type BatchJob,
  type Dataset,
  type ImportResult,
  type IndexedResult,
  type LedgerEntry,
  type RangeSpec,
  type PrefetchCandidateView,
  type PrefetchStatus,
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
  return `${b.chain_key.toUpperCase()} · ${b.address_count} 地址 · ${ds} · ${pad(t.getMonth() + 1)}-${pad(t.getDate())} ${pad(t.getHours())}:${pad(t.getMinutes())}`;
}

function abbr(v?: unknown): string {
  const s = String(v ?? "");
  if (s.length > 18) return `${s.slice(0, 10)}…${s.slice(-6)}`;
  return s;
}

function fmtTime(v?: unknown): string {
  const n = Number(v);
  if (!n || !Number.isFinite(n)) return "—";
  return new Date(n * 1000).toISOString().replace("T", " ").slice(0, 19);
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
    <div className="smart-download-page" style={{ padding: 24 }}>
      <Typography.Title level={4}>
        <ThunderboltOutlined /> 智能下载
      </Typography.Title>
      <Tabs
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
  const [importResult, setImportResult] = useState<ImportResult | null>(null);
  const [importing, setImporting] = useState(false);
  const [creating, setCreating] = useState(false);
  const [analyzing, setAnalyzing] = useState(false);
  const [planSummary, setPlanSummary] = useState<{
    rows: number;
    bytes: number;
    buckets: Record<string, number>;
    fullHits: number;
    partialHits: number;
    misses: number;
    reusedRanges: number;
  } | null>(null);
  const [submittedCount, setSubmittedCount] = useState(0);

  const handleFile = async (file: File) => {
    setImporting(true);
    const res = await importAddressFile(file);
    setImporting(false);
    if (!res) {
      void message.error("地址导入失败，请检查文件格式（TXT/CSV/XLSX）");
      return false;
    }
    setImportResult(res);
    void message.success(
      `识别地址列「${res.selected_column}」：有效 ${res.valid} / 重复 ${res.duplicates} / 无效 ${res.invalid}`,
    );
    return false;
  };

  const onCreate = async (values: {
    chain_key: string;
    addresses?: string;
    datasets?: Dataset[];
    range_mode: "FULL" | "TIME" | "BLOCK";
    from_block?: number;
    to_block?: number;
    date_range?: unknown[];
    force_redownload?: boolean;
  }) => {
    let addresses: string[] = importResult?.final_addresses ?? [];
    setSubmittedCount(addresses.length > 0 ? addresses.length : values.addresses?.split(/[\n,，;；\s]+/).filter(Boolean).length ?? 0);
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
    }
    if (values.range_mode === "TIME" && Array.isArray(values.date_range) && values.date_range.length === 2) {
      const [from, to] = values.date_range as [Dayjs, Dayjs];
      defaultRange.start_time = from.toISOString();
      defaultRange.end_time = to.toISOString();
    }
    setCreating(true);
    try {
      const res = await createBatch({
        chain_key: values.chain_key,
        addresses,
        datasets: values.datasets,
        default_range: defaultRange.mode === "FULL" ? undefined : defaultRange,
        skip_covered: !values.force_redownload,
        address_chain_overrides: Object.keys(chainOverrides).length > 0 ? chainOverrides : undefined,
      });
      if (!res?.batch) {
        void message.error("创建失败");
        return;
      }
      // Discovery 反馈：先分析数据规模，再自动进入任务中心（不要求用户确认）
      setAnalyzing(true);
      const plan = await planBatch(res.batch.id);
      if (plan && plan.datasets.length > 0) {
        const buckets: Record<string, number> = { S: 0, M: 0, L: 0, XL: 0 };
        let rows = 0;
        let bytes = 0;
        for (const d of plan.datasets) {
          rows += d.estimated_rows || 0;
          bytes += d.estimated_bytes || 0;
          buckets[d.size_class] = (buckets[d.size_class] ?? 0) + 1;
        }
        setPlanSummary({
          rows,
          bytes,
          buckets,
          fullHits: res.local_full_hits ?? 0,
          partialHits: res.local_partial_hits ?? 0,
          misses: res.local_misses ?? 0,
          reusedRanges: res.reused_ranges ?? 0,
        });
      }
      await batchAction(res.batch.id, "start");
      setAnalyzing(false);
      onCreated(res.batch.id);
    } finally {
      setCreating(false);
    }
  };

  return (
    <Card>
      <Form
        form={form}
        layout="vertical"
        onFinish={onCreate}
        initialValues={{ chain_key: "bsc", datasets: ["transactions", "token_transfers", "balances"], range_mode: "FULL", force_redownload: false }}
      >
        <Form.Item
          label="地址"
          name="addresses"
          extra="多链支持：每行 0x... 后可跟空格加链名（如 0x… eth），缺省使用上方网络"
        >
          <Input.TextArea rows={5} placeholder={"0x...\n0x..."} />
        </Form.Item>
        <Space style={{ marginBottom: 8 }}>
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
            <Tag color="blue">
              识别列「{importResult.selected_column}」 有效 {importResult.valid} / 重复 {importResult.duplicates} /
              无效 {importResult.invalid}
            </Tag>
          )}
        </Space>
        {importResult && (
          <Alert
            style={{ marginBottom: 16 }}
            type="info"
            showIcon
            message={`原始记录 ${importResult.rows} 行，最终任务地址 ${importResult.final_addresses?.length ?? importResult.valid} 个`}
            description={
              <div>
                列候选：
                {importResult.detected_columns
                  .filter((c) => c.valid > 0)
                  .map((c) => `${c.name}(${(c.confidence * 100).toFixed(1)}%)`)
                  .join("，")}
              </div>
            }
          />
        )}
        <Form.Item label="网络" name="chain_key" rules={[{ required: true }]}>
          <Select options={chainOptions} style={{ width: 240 }} />
        </Form.Item>
        <Form.Item label="数据" name="datasets" rules={[{ required: true }]}>
          <Checkbox.Group options={datasetOptions} />
        </Form.Item>
        <Form.Item label="时间范围" name="range_mode">
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
                <Form.Item name="date_range" label="时间区间">
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
        <Form.Item name="force_redownload" valuePropName="checked" style={{ marginBottom: 8 }}>
          <Checkbox>强制忽略本地缓存重新下载（默认自动复用本地已验证数据）</Checkbox>
        </Form.Item>
        {analyzing && (
          <Alert
            style={{ marginBottom: 12 }}
            type="info"
            showIcon
            message="正在分析数据规模…"
            description={
              planSummary
                ? `地址 ${submittedCount || "—"} 个 · 预计 ${Math.round(planSummary.rows).toLocaleString()} 行 · ≈ ${(planSummary.bytes / (1 << 30)).toFixed(1)} GB · 小型 ${planSummary.buckets.S} / 中型 ${planSummary.buckets.M} / 大型 ${planSummary.buckets.L} / 超大型 ${planSummary.buckets.XL} · 完全命中 ${planSummary.fullHits} / 部分命中 ${planSummary.partialHits} / 需下载 ${planSummary.misses} · 复用 ${planSummary.reusedRanges} 个区间 · 系统将自动选择最合适的数据源`
                : "正在探测每个地址的数据规模…"
            }
          />
        )}
        <Button type="primary" htmlType="submit" icon={<CloudDownloadOutlined />} loading={creating}>
          开始智能下载
        </Button>
      </Form>
    </Card>
  );
}

// ── 任务中心 ──

function TasksTab({ refreshKey }: { refreshKey: number }) {
  const [batches, setBatches] = useState<BatchJob[]>([]);
  const [selectedBatch, setSelectedBatch] = useState<string | null>(null);
  const [addresses, setAddresses] = useState<AddressJob[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState<string | undefined>(undefined);
  const [detail, setDetail] = useState<AddressDetail | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [ledgers, setLedgers] = useState<Record<string, LedgerEntry[]>>({});
  const [batchSnap, setBatchSnap] = useState<BatchSnapshot | null>(null);
  const [summary, setSummary] = useState<BatchSummary | null>(null);

  const load = () => {
    void listBatches().then(setBatches);
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  useEffect(() => {
    if (!selectedBatch) {
      setAddresses([]);
      setBatchSnap(null);
      return;
    }
    void batchSnapshot(selectedBatch).then(setBatchSnap);
    void batchSummary(selectedBatch).then(setSummary);
    void listBatchAddresses(selectedBatch, page, 50, statusFilter).then((res) => {
      if (res) {
        setAddresses(res.addresses);
        setTotal(res.total);
      }
    });
  }, [selectedBatch, page, statusFilter, refreshKey]);

  const runBatchAction = async (id: string, action: string) => {
    await batchAction(id, action);
    load();
  };

  const runAddressAction = async (id: string, action: string) => {
    await addressAction(id, action);
    setRefresh();
  };

  const setRefresh = () => {
    // 地址操作后强制刷新当前页数据
    void listBatchAddresses(selectedBatch ?? "", page, 50, statusFilter).then((res) => {
      if (res) {
        setAddresses(res.addresses);
        setTotal(res.total);
      }
    });
  };

  const openDetail = async (addressId: string) => {
    const d = await addressDetail(addressId);
    if (!d) return;
    setDetail(d);
    setDetailOpen(true);
    const map: Record<string, LedgerEntry[]> = {};
    await Promise.all(
      d.datasets.map(async (dd) => {
        map[dd.dataset.id] = await datasetLedger(dd.dataset.id);
      }),
    );
    setLedgers(map);
  };

  const hasProviderSwitch = Object.values(ledgers).some((list) =>
    list.some((e) => e.event === "PROVIDER_SWITCHED"),
  );

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
    {
      title: "状态",
      dataIndex: "status",
      width: 110,
      render: (v) => <StatusPill status={v} />,
    },
    {
      title: "操作",
      width: 220,
      render: (_, r) => (
        <Space size={4}>
          <Tooltip title="查看地址任务">
            <Button size="small" onClick={() => setSelectedBatch(r.id)}>
              地址
            </Button>
          </Tooltip>
          <Tooltip title={r.status === "CREATED" || r.status === "PAUSED" ? "继续" : "当前状态不可继续"}>
            <Button
              size="small"
              icon={<PlayCircleOutlined />}
              disabled={r.status !== "CREATED" && r.status !== "PAUSED"}
              onClick={() => void runBatchAction(r.id, "resume")}
            />
          </Tooltip>
          <Tooltip title={r.status === "RUNNING" ? "暂停全部" : "当前状态不可暂停"}>
            <Button
              size="small"
              icon={<PauseCircleOutlined />}
              disabled={r.status !== "RUNNING"}
              onClick={() => void runBatchAction(r.id, "pause")}
            />
          </Tooltip>
          <Tooltip title="取消任务（保留已下载数据）">
            <Button
              size="small"
              danger
              icon={<StopOutlined />}
              disabled={r.status === "COMPLETED" || r.status === "CANCELED" || r.status === "FAILED"}
              onClick={() => void runBatchAction(r.id, "cancel")}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

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
          <Tooltip title={r.status === "DOWNLOADING" || r.status === "WAITING" ? "暂停" : "当前状态不可暂停"}>
            <Button
              size="small"
              icon={<PauseCircleOutlined />}
              disabled={r.status !== "DOWNLOADING" && r.status !== "WAITING"}
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
              disabled={r.status === "COMPLETED" || r.status === "CANCELED" || r.status === "FAILED"}
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
        title={`任务列表（${batches.length}）`}
        extra={
          <Space>
            <Select
              allowClear
              placeholder="状态筛选"
              style={{ width: 140 }}
              value={statusFilter}
              onChange={setStatusFilter}
              options={Object.keys(STATUS_COLOR).map((s) => ({ value: s, label: s }))}
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
          dataSource={batches}
          pagination={false}
          size="small"
          onRow={(r) => ({ onClick: () => setSelectedBatch(r.id), style: { cursor: "pointer" } })}
        />
      </Card>
      {selectedBatch && (
        <>
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
                    <Button size="small" icon={<PauseCircleOutlined />} onClick={() => void runBatchAction(selectedBatch, "pause")} />
                  </Tooltip>
                  <Tooltip title="继续全部">
                    <Button size="small" icon={<PlayCircleOutlined />} onClick={() => void runBatchAction(selectedBatch, "resume")} />
                  </Tooltip>
                  <Tooltip title="取消任务（保留已下载数据）">
                    <Button size="small" danger icon={<StopOutlined />} onClick={() => void runBatchAction(selectedBatch, "cancel")} />
                  </Tooltip>
                </Space>
              </Space>
            </Card>
          )}
          <Card
            title={
              <Space>
                <span>地址任务（共 {total}）</span>
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
            }
            style={{ marginTop: 16 }}
          >
            <Table
              rowKey="id"
              columns={addressColumns}
              dataSource={addresses}
              size="small"
              onRow={(r) => ({ onClick: () => void openDetail(r.id), style: { cursor: "pointer" } })}
              pagination={{ current: page, pageSize: 50, total, onChange: setPage, showSizeChanger: false }}
            />
          </Card>
        </>
      )}
      <Drawer className="sd-drawer" title="地址详情" width={760} open={detailOpen} onClose={() => setDetailOpen(false)}>
        {detail && (
          <Space direction="vertical" style={{ width: "100%" }} size="middle">
            <Alert
              type="info"
              showIcon
              message={
                <Space>
                  <code>{detail.address.address}</code>
                  <StatusPill status={detail.address.status} />
                  <div style={{ width: 200 }}>
                    <ProgressBar
                      percent={detail.address.progress?.percent}
                      status={detail.address.status}
                      rows={detail.address.progress?.rows_current}
                      speed={detail.address.progress?.speed_rows_per_sec}
                      eta={detail.address.progress?.eta_seconds}
                    />
                  </div>
                </Space>
              }
            />
            <Alert
              type="info"
              showIcon
              message={
                <Space wrap>
                  <span>链 {detail.address.chain_key?.toUpperCase()}</span>
                  <span>
                    范围 {detail.address.range?.mode ?? "FULL"}
                    {detail.address.range?.from_block != null
                      ? ` ${detail.address.range.from_block} - ${detail.address.range.to_block ?? "∞"}`
                      : ""}
                  </span>
                  <span>
                    ETA{" "}
                    {detail.address.progress?.eta_seconds
                      ? `${Math.round(detail.address.progress.eta_seconds)}s`
                      : "—"}
                  </span>
                  <span>
                    已下载 {detail.address.progress?.rows_current?.toLocaleString() ?? 0} 行
                  </span>
                </Space>
              }
            />
            {hasProviderSwitch && (
              <Alert
                type="warning"
                showIcon
                message="已自动切换下载方式"
                description="已完成数据不会重新下载；新 Provider 已从未完成区间继续。"
              />
            )}
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
                  <Alert
                    style={{ margin: "8px 0" }}
                    type={dd.dataset.validation.status === "VALIDATED" ? "success" : "warning"}
                    showIcon
                    message={`校验 ${dd.dataset.validation.status} · 完整性 ${(dd.dataset.validation.coverage * 100).toFixed(2)}% · 唯一 ${dd.dataset.validation.unique_key_count} · 重复 ${dd.dataset.validation.duplicate_count}`}
                    description={`Score ${dd.dataset.validation.score} · Provider ${dd.dataset.validation.expected_count}/${dd.dataset.validation.actual_count} · 缺口 ${(dd.dataset.validation as { gaps?: Array<unknown> }).gaps?.length ?? 0}`}
                  />
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

  useEffect(() => {
    void listRegistry().then((list) => {
      setRegistry(list);
      if (!selected && list.length > 0) setSelected(list[0].dataset_job_id);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  useEffect(() => {
    if (!selected) return;
    setLoading(true);
    void resultSummary(selected).then(setSummary);
    void queryResults(selected, page, 50, "", filter).then((res) => {
      if (res) {
        setRows(res.rows);
        setTotal(res.total);
      }
      setLoading(false);
    });
  }, [selected, page, filter, refreshKey]);

  const entry = registry.find((r) => r.dataset_job_id === selected);

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
          }
        >;
      }
    >();
    for (const e of registry) {
      let g = map.get(e.address);
      if (!g) {
        g = { address: e.address, chain_key: e.chain_key, datasets: new Map() };
        map.set(e.address, g);
      }
      let d = g.datasets.get(e.dataset);
      if (!d) {
        d = {
          name: e.dataset,
          from: e.from_block,
          to: e.to_block,
          rows: 0,
          validation: e.validation,
          certification: e.certification,
          latest: e.dataset_job_id,
        };
        g.datasets.set(e.dataset, d);
      }
      d.from = Math.min(d.from, e.from_block);
      d.to = Math.max(d.to, e.to_block);
      d.rows += e.row_count;
      d.latest = e.dataset_job_id;
      d.validation = e.validation;
      d.certification = e.certification;
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
              onSearch={setFilter}
            />
            <Button
              icon={<ReloadOutlined />}
              onClick={() => void listRegistry().then(setRegistry)}
            >
              刷新
            </Button>
          </Space>
        }
      >
        <Table
          rowKey="address"
          size="small"
          pagination={false}
          dataSource={grouped}
          expandable={{
            expandedRowRender: (g) => (
              <Table
                rowKey="name"
                size="small"
                pagination={false}
                dataSource={g.datasets}
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
                  完整性 {((Number(summary.validation && (summary.validation as { coverage?: number }).coverage) ?? 1) * 100).toFixed(2)}%
                  · {total.toLocaleString()} 行
                </span>
              ) : null}
            </Space>
          }
          extra={
            <Space>
              <span className="sd-status-pill is-validated">
                {total > 300000 ? "CSV（>30 万行）" : "XLSX（≤30 万行）"}
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
          <Table
            rowKey={(r) =>
              (r.transaction_hash as string) ||
              (r.address as string) ||
              `${r.block_number}-${r.log_index}` ||
              JSON.stringify(r).slice(0, 32)
            }
            loading={loading}
            size="small"
            dataSource={rows}
            columns={columns}
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
