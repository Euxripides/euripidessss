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
  Radio,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Timeline,
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
  createBatch,
  datasetLedger,
  downloadResultExport,
  importAddressFile,
  listBatchAddresses,
  listBatches,
  listRegistry,
  queryResults,
  resultSummary,
  type AddressDetail,
  type AddressJob,
  type BatchJob,
  type Dataset,
  type ImportResult,
  type IndexedResult,
  type LedgerEntry,
  type RangeSpec,
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

export default function SmartDownloadPage({ onOpenAddress, onNavigate }: SmartDownloadPageProps) {
  const [tab, setTab] = useState("create");
  const [refreshKey, setRefreshKey] = useState(0);
  const refreshTimer = useRef<number | null>(null);

  useEffect(() => {
    const es = new EventSource("/api/smart-download/events");
    es.onmessage = () => {
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

// ── 创建下载 ──

function CreateTab({ onCreated }: { onCreated: (batchId: string) => void }) {
  const [form] = Form.useForm();
  const [importResult, setImportResult] = useState<ImportResult | null>(null);
  const [importing, setImporting] = useState(false);
  const [creating, setCreating] = useState(false);

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
    skip_covered?: boolean;
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
        skip_covered: values.skip_covered,
        address_chain_overrides: Object.keys(chainOverrides).length > 0 ? chainOverrides : undefined,
      });
      if (!res?.batch) {
        void message.error("创建失败");
        return;
      }
      await batchAction(res.batch.id, "start");
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
        initialValues={{ chain_key: "bsc", datasets: ["transactions", "token_transfers", "balances"], range_mode: "FULL" }}
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
        <Form.Item name="skip_covered" valuePropName="checked" style={{ marginBottom: 8 }}>
          <Checkbox>本地已有数据直接复用（LOCAL HIT，跳过重复下载）</Checkbox>
        </Form.Item>
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
      return;
    }
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

  const batchColumns: ColumnsType<BatchJob> = [
    { title: "任务ID", dataIndex: "id", width: 300, ellipsis: true },
    { title: "链", dataIndex: "chain_key", width: 70, render: (v) => v?.toUpperCase() },
    { title: "地址数", dataIndex: "address_count", width: 80 },
    {
      title: "状态",
      dataIndex: "status",
      width: 110,
      render: (v) => <StatusPill status={v} />,
    },
    { title: "数据集", dataIndex: "dataset_types", render: (v: string[]) => v?.map((d) => DATASET_LABELS[d] ?? d).join("、") },
    {
      title: "操作",
      width: 220,
      render: (_, r) => (
        <Space size={4}>
          <Button size="small" onClick={() => setSelectedBatch(r.id)}>
            地址
          </Button>
          <Button
            size="small"
            icon={<PlayCircleOutlined />}
            disabled={r.status !== "CREATED" && r.status !== "PAUSED"}
            onClick={() => void runBatchAction(r.id, "resume")}
          />
          <Button
            size="small"
            icon={<PauseCircleOutlined />}
            disabled={r.status !== "RUNNING"}
            onClick={() => void runBatchAction(r.id, "pause")}
          />
          <Button
            size="small"
            danger
            icon={<StopOutlined />}
            disabled={r.status === "COMPLETED" || r.status === "CANCELED" || r.status === "FAILED"}
            onClick={() => void runBatchAction(r.id, "cancel")}
          />
        </Space>
      ),
    },
  ];

  const addressColumns: ColumnsType<AddressJob> = [
    { title: "地址", dataIndex: "address", width: 430, ellipsis: true },
    {
      title: "状态",
      dataIndex: "status",
      width: 110,
      render: (v) => <StatusPill status={v} />,
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
          <Button size="small" icon={<FileSearchOutlined />} onClick={() => void openDetail(r.id)}>
            详情
          </Button>
          <Button
            size="small"
            icon={<PauseCircleOutlined />}
            disabled={r.status !== "DOWNLOADING" && r.status !== "WAITING"}
            onClick={() => void runAddressAction(r.id, "pause")}
          />
          <Button
            size="small"
            icon={<PlayCircleOutlined />}
            disabled={r.status !== "PAUSED"}
            onClick={() => void runAddressAction(r.id, "resume")}
          />
          <Button
            size="small"
            danger
            icon={<StopOutlined />}
            disabled={r.status === "COMPLETED" || r.status === "CANCELED" || r.status === "FAILED"}
            onClick={() => void runAddressAction(r.id, "cancel")}
          />
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
        <Table rowKey="id" columns={batchColumns} dataSource={batches} pagination={false} size="small" />
      </Card>
      {selectedBatch && (
        <Card title={`地址任务（批次 ${selectedBatch.slice(0, 8)}…，共 ${total}）`} style={{ marginTop: 16 }}>
          <Table
            rowKey="id"
            columns={addressColumns}
            dataSource={addresses}
            size="small"
            pagination={{ current: page, pageSize: 50, total, onChange: setPage, showSizeChanger: false }}
          />
        </Card>
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
            {detail.datasets.map((dd) => (
              <Card
                key={dd.dataset.id}
                size="small"
                className="sd-dataset-card"
                title={
                  <Space>
                    {DATASET_LABELS[dd.dataset.dataset] ?? dd.dataset.dataset}
                    <StatusPill status={dd.dataset.status} />
                    <Tag>{dd.dataset.current_provider ?? "—"}</Tag>
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
                    description={`Score ${dd.dataset.validation.score} · Provider ${dd.dataset.validation.expected_count}/${dd.dataset.validation.actual_count}`}
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

  const columns = useMemo<ColumnsType<Record<string, unknown>>>(() => {
    if (rows.length === 0) return [];
    const keys = Object.keys(rows[0]).filter(
      (k) => !["source_provider", "source_range_id", "ingested_at"].includes(k),
    );
    return keys.map((k) => ({
      title: k,
      dataIndex: k,
      ellipsis: true,
      render: (v) => (typeof v === "object" ? JSON.stringify(v) : String(v ?? "")),
    }));
  }, [rows]);

  const entry = registry.find((r) => r.dataset_job_id === selected);

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
          rowKey="dataset_job_id"
          size="small"
          pagination={false}
          dataSource={registry}
          rowClassName={(r) => (r.dataset_job_id === selected ? "ant-table-row-selected" : "")}
          onRow={(r) => ({
            onClick: () => {
              setSelected(r.dataset_job_id);
              setPage(1);
            },
          })}
          columns={[
            { title: "地址", dataIndex: "address", ellipsis: true },
            { title: "数据集", dataIndex: "dataset", width: 130 },
            { title: "链", dataIndex: "chain_key", width: 70 },
            { title: "区间", render: (_, r) => `${r.from_block} - ${r.to_block}`, width: 170 },
            { title: "行数", dataIndex: "row_count", width: 110 },
            {
              title: "校验",
              dataIndex: "validation",
              width: 110,
              render: (v) => <StatusPill status={v} />,
            },
            {
              title: "操作",
              width: 260,
              render: (_, r) => (
                <Space size={4}>
                  <Button size="small" onClick={() => onOpenAddress(r.address)}>
                    地址画像
                  </Button>
                  <Button size="small" onClick={() => onNavigate("analytics-graph")}>
                    关系图
                  </Button>
                  <Button size="small" onClick={() => onNavigate("intelligence")}>
                    智能调查
                  </Button>
                </Space>
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
