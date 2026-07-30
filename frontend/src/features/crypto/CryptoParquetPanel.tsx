import {
  CloudDownloadOutlined,
  DatabaseOutlined,
  FileExcelOutlined,
  FolderOpenOutlined,
  ReloadOutlined,
  RetweetOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  StopOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import type { Dayjs } from 'dayjs';
import dayjs from 'dayjs';
import {
  Alert,
  Button,
  Collapse,
  DatePicker,
  Descriptions,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Progress,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  Upload,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  cancelParquetTask,
  loadParquetJob,
  loadParquetJobs,
  loadParquetSettings,
  parquetOutputURL,
  previewParquetTask,
  retryParquetTask,
  saveParquetSettings,
  startParquetTask,
  uploadParquetAddresses,
  type AddressSummary,
  type ParquetFileTask,
  type ParquetJob,
  type ParquetPreview,
  type ParquetSettings,
  type ParquetStartPayload,
} from './cryptoParquetApi';
import './crypto-parquet.css';

const { RangePicker } = DatePicker;

type TaskFormValues = {
  chain_key: 'bsc';
  dates: [Dayjs, Dayjs];
  addresses: string;
  keep_source_files: boolean;
  export_csv: boolean;
};

const statusMeta: Record<string, { label: string; color: string }> = {
  queued: { label: '排队中', color: 'default' },
  running: { label: '运行中', color: 'processing' },
  downloading: { label: '下载中', color: 'processing' },
  downloaded: { label: '已下载', color: 'cyan' },
  processing: { label: '筛选中', color: 'blue' },
  done: { label: '已完成', color: 'success' },
  failed: { label: '失败', color: 'error' },
  canceled: { label: '已取消', color: 'warning' },
};

export function CryptoParquetPanel() {
  const [form] = Form.useForm<TaskFormValues>();
  const [settingsForm] = Form.useForm<ParquetSettings>();
  const [settings, setSettings] = useState<ParquetSettings | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [preview, setPreview] = useState<ParquetPreview | null>(null);
  const [previewKey, setPreviewKey] = useState('');
  const [job, setJob] = useState<ParquetJob | null>(null);
  const [jobs, setJobs] = useState<readonly ParquetJob[]>([]);
  const [loading, setLoading] = useState(false);
  const [uploading, setUploading] = useState(false);
  const pollingRef = useRef<number | undefined>(undefined);

  const rawAddresses = Form.useWatch('addresses', form) ?? '';
  const addressSummary = useMemo(() => summarizeAddresses(rawAddresses), [rawAddresses]);
  const active = job?.status === 'running' || job?.status === 'queued';
  const columns = useMemo<ColumnsType<ParquetFileTask>>(() => buildFileColumns(), []);

  useEffect(() => {
    void initialize();
    return () => {
      if (pollingRef.current !== undefined) window.clearInterval(pollingRef.current);
    };
  }, []);

  useEffect(() => {
    if (pollingRef.current !== undefined) {
      window.clearInterval(pollingRef.current);
      pollingRef.current = undefined;
    }
    if (!job?.id || !active) return;
    pollingRef.current = window.setInterval(() => void refreshJob(job.id), 1000);
    return () => {
      if (pollingRef.current !== undefined) window.clearInterval(pollingRef.current);
    };
  }, [job?.id, active]);

  async function initialize() {
    setLoading(true);
    try {
      const [loadedSettings, loadedJobs] = await Promise.all([
        loadParquetSettings(),
        loadParquetJobs(),
      ]);
      setSettings(loadedSettings);
      settingsForm.setFieldsValue(loadedSettings);
      setJobs(loadedJobs);
      const latest = loadedJobs.find((item) => item.status === 'running' || item.status === 'queued') ?? loadedJobs[0];
      if (latest) setJob(latest);
      form.setFieldsValue({
        chain_key: 'bsc',
        dates: [dayjs().subtract(2, 'day'), dayjs().subtract(2, 'day')],
        addresses: '',
        keep_source_files: loadedSettings.keep_source_files,
        export_csv: loadedSettings.export_csv,
      });
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取 Parquet 工作台失败');
    } finally {
      setLoading(false);
    }
  }

  function payloadFrom(values: TaskFormValues): ParquetStartPayload {
    return {
      chain_key: values.chain_key,
      addresses: values.addresses,
      start_date: values.dates[0].format('YYYY-MM-DD'),
      end_date: values.dates[1].format('YYYY-MM-DD'),
      keep_source_files: values.keep_source_files,
      export_csv: values.export_csv,
    };
  }

  function payloadKey(payload: ParquetStartPayload) {
    return JSON.stringify(payload);
  }

  async function runPreview() {
    const values = await form.validateFields();
    const payload = payloadFrom(values);
    setLoading(true);
    try {
      const result = await previewParquetTask(payload);
      setPreview(result);
      setPreviewKey(payloadKey(payload));
      if (!result.files.length) {
        message.warning('所选日期尚未发现可用 Parquet 分区');
      } else {
        message.success(`已发现 ${result.files.length} 个分区，共 ${formatBytes(result.total_bytes)}`);
      }
    } catch (error) {
      message.error(error instanceof Error ? error.message : '预检失败');
    } finally {
      setLoading(false);
    }
  }

  async function startTask() {
    const values = await form.validateFields();
    const payload = payloadFrom(values);
    if (!preview || previewKey !== payloadKey(payload)) {
      message.warning('参数已变化，请重新预检分区后再启动');
      return;
    }
    setLoading(true);
    try {
      const created = await startParquetTask(payload);
      setJob(created);
      setPreview(null);
      setPreviewKey('');
      setJobs(await loadParquetJobs());
      message.success('Parquet 下载与批量筛选任务已启动');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '启动任务失败');
    } finally {
      setLoading(false);
    }
  }

  async function refreshJob(id = job?.id) {
    if (!id) return;
    try {
      const next = await loadParquetJob(id);
      setJob(next);
      if (next.status !== 'running' && next.status !== 'queued') {
        setJobs(await loadParquetJobs());
      }
    } catch (error) {
      message.warning(error instanceof Error ? error.message : '刷新任务失败');
    }
  }

  async function cancelTask() {
    if (!job) return;
    try {
      setJob(await cancelParquetTask(job.id));
      message.info('正在安全取消；已完成分区和 .partial 文件会保留');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '取消任务失败');
    }
  }

  async function retryTask(target = job) {
    if (!target) return;
    setLoading(true);
    try {
      const next = await retryParquetTask(target.id);
      setJob(next);
      setJobs(await loadParquetJobs());
      message.success('已从检查点创建重试任务');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '重试失败');
    } finally {
      setLoading(false);
    }
  }

  async function saveSettings() {
    const values = await settingsForm.validateFields();
    setLoading(true);
    try {
      const saved = await saveParquetSettings(values);
      setSettings(saved);
      setSettingsOpen(false);
      form.setFieldsValue({
        keep_source_files: saved.keep_source_files,
        export_csv: saved.export_csv,
      });
      setPreview(null);
      message.success('存储与性能设置已保存');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '保存设置失败');
    } finally {
      setLoading(false);
    }
  }

  async function handleAddressUpload(file: File) {
    setUploading(true);
    try {
      const result = await uploadParquetAddresses(file);
      form.setFieldValue('addresses', result.raw);
      setPreview(null);
      setPreviewKey('');
      message.success(`已导入 ${result.summary.valid} 个有效地址`);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取地址文件失败');
    } finally {
      setUploading(false);
    }
  }

  const stageItems = job?.stages ?? [
    { key: 'addresses', label: '地址校验', status: 'queued', progress: 0 },
    { key: 'discover', label: '文件发现', status: 'queued', progress: 0 },
    { key: 'download', label: '分片下载', status: 'queued', progress: 0 },
    { key: 'schema', label: 'Schema 探测', status: 'queued', progress: 0 },
    { key: 'match', label: '批量匹配', status: 'queued', progress: 0 },
    { key: 'output', label: 'Parquet 输出', status: 'queued', progress: 0 },
  ];

  return (
    <div className="crypto-parquet-shell">
      <header className="crypto-parquet-header">
        <div>
          <h1>EVM Parquet 批量资金分析</h1>
          <p>按日期发现 AWS 公共分区，一次扫描匹配全部目标地址</p>
        </div>
        <Button
          icon={<SettingOutlined />}
          onClick={() => {
            if (settings) settingsForm.setFieldsValue(settings);
            setSettingsOpen(true);
          }}
        >
          存储与性能设置
        </Button>
      </header>

      <Alert
        className="crypto-parquet-source-alert"
        type="warning"
        showIcon
        message="当前公开源能力边界"
        description="AWS BNB 目录已实时核验为 blocks 与 transactions。本页只处理 transactions：普通交易、原生 BNB 转账和顶层合约创建候选；不把缺失的 Transfer logs、Trace 或回执伪装成完整结果。"
      />

      <div className="crypto-parquet-workspace">
        <section className="crypto-parquet-config">
          <div className="crypto-parquet-section-title">
            <div>
              <h2>任务配置</h2>
              <span>先预检体量，再开始下载</span>
            </div>
            <SafetyCertificateOutlined />
          </div>
          <Form<TaskFormValues>
            form={form}
            layout="vertical"
            requiredMark={false}
            onValuesChange={() => {
              setPreview(null);
              setPreviewKey('');
            }}
          >
            <Form.Item label="EVM 网络" name="chain_key">
              <Select
                options={[
                  { label: 'BSC Mainnet · Chain ID 56', value: 'bsc' },
                  { label: 'Ethereum Mainnet · 规划中', value: 'eth', disabled: true },
                ]}
              />
            </Form.Item>
            <Form.Item
              label="数据日期"
              name="dates"
              rules={[{ required: true, message: '请选择开始和结束日期' }]}
            >
              <RangePicker allowClear={false} className="crypto-parquet-date" />
            </Form.Item>
            <div className="crypto-parquet-address-label">
              <span>目标地址</span>
              <Upload
                accept=".xlsx,.xlsm,.csv,.txt"
                showUploadList={false}
                customRequest={({ file, onSuccess, onError }) => {
                  void handleAddressUpload(file as File)
                    .then(() => onSuccess?.({}))
                    .catch((error) => onError?.(error as Error));
                }}
              >
                <Button size="small" type="text" loading={uploading} icon={<UploadOutlined />}>
                  导入 XLSX / CSV / TXT
                </Button>
              </Upload>
            </div>
            <Form.Item
              name="addresses"
              rules={[
                { required: true, message: '请输入至少一个 EVM 地址' },
                {
                  validator: async (_, value) => {
                    if (summarizeAddresses(String(value ?? '')).valid === 0) {
                      throw new Error('没有格式正确的 0x EVM 地址');
                    }
                  },
                },
              ]}
            >
              <Input.TextArea
                rows={8}
                placeholder={'每行一个地址，例如：\n0x1111111111111111111111111111111111111111'}
                spellCheck={false}
              />
            </Form.Item>
            <div className="crypto-parquet-address-audit">
              <span><strong>{addressSummary.valid}</strong> 有效</span>
              <span><strong>{addressSummary.duplicates}</strong> 重复</span>
              <span className={addressSummary.invalid ? 'has-error' : ''}>
                <strong>{addressSummary.invalid}</strong> 无效
              </span>
            </div>
            <Space className="crypto-parquet-output-switches" size={20} wrap>
              <span>
                <Form.Item name="keep_source_files" valuePropName="checked" noStyle>
                  <Switch size="small" />
                </Form.Item>
                处理后保留源文件
              </span>
              <span>
                <Form.Item name="export_csv" valuePropName="checked" noStyle>
                  <Switch size="small" />
                </Form.Item>
                同时导出 CSV
              </span>
            </Space>
            <div className="crypto-parquet-primary-actions">
              <Button icon={<ReloadOutlined />} onClick={runPreview} loading={loading}>
                预检分区
              </Button>
              <Button
                type="primary"
                icon={<CloudDownloadOutlined />}
                onClick={startTask}
                loading={loading}
                disabled={!preview?.files.length || active}
              >
                开始下载与筛选
              </Button>
            </div>
          </Form>

          {preview && (
            <div className="crypto-parquet-preview">
              <div className="crypto-parquet-preview-head">
                <strong>预检结果</strong>
                <Tag color={preview.files.length ? 'blue' : 'default'}>{preview.files.length} 个分区</Tag>
              </div>
              <Descriptions column={1} size="small" colon={false}>
                <Descriptions.Item label="预计下载">{formatBytes(preview.total_bytes)}</Descriptions.Item>
                <Descriptions.Item label="磁盘可用">{formatBytes(preview.free_bytes)}</Descriptions.Item>
                <Descriptions.Item label="批量地址">{preview.addresses.valid.toLocaleString('zh-CN')} 个</Descriptions.Item>
              </Descriptions>
              {preview.total_bytes > 20 * 1024 ** 3 && (
                <Alert type="warning" showIcon message="本次源数据较大；任务会按分片流水线处理并及时清理 staging。" />
              )}
            </div>
          )}
        </section>

        <section className="crypto-parquet-monitor">
          <div className="crypto-parquet-section-title">
            <div>
              <h2>任务监控</h2>
              <span>{job ? `任务 ${job.id}` : '尚未启动任务'}</span>
            </div>
            {job && <StatusTag status={job.status} />}
          </div>

          <div className="crypto-parquet-pipeline">
            {stageItems.map((stage, index) => (
              <div className={`crypto-parquet-stage ${stage.status}`} key={stage.key}>
                <span>{index + 1}</span>
                <div>
                  <strong>{stage.label}</strong>
                  <small>{stage.detail || stageLabel(stage.status)}</small>
                </div>
              </div>
            ))}
          </div>

          {job ? (
            <>
              <div className="crypto-parquet-overall">
                <div className="crypto-parquet-overall-head">
                  <div>
                    <strong>{job.status === 'done' ? '任务已完成' : '总体进度'}</strong>
                    <span>
                      {formatBytes(job.downloaded_bytes)} / {formatBytes(job.total_bytes)}
                      {job.download_speed_bps > 0 ? ` · ${formatBytes(job.download_speed_bps)}/s` : ''}
                      {job.eta_seconds > 0 ? ` · 预计 ${formatDuration(job.eta_seconds)}` : ''}
                    </span>
                  </div>
                  <b>{Math.round(job.progress)}%</b>
                </div>
                <Progress
                  percent={Math.round(job.progress)}
                  showInfo={false}
                  status={job.status === 'failed' ? 'exception' : job.status === 'done' ? 'success' : 'active'}
                  strokeWidth={12}
                />
                <div className="crypto-parquet-live-stats">
                  <span>源记录 <strong>{job.source_rows.toLocaleString('zh-CN')}</strong></span>
                  <span>命中记录 <strong>{job.matched_rows.toLocaleString('zh-CN')}</strong></span>
                  <span>失败分区 <strong>{job.failed_files}</strong></span>
                  <span>有效地址 <strong>{job.addresses.valid}</strong></span>
                </div>
                <Space>
                  <Button icon={<ReloadOutlined />} onClick={() => refreshJob()} disabled={loading}>刷新</Button>
                  {active && (
                    <Button danger icon={<StopOutlined />} onClick={cancelTask}>
                      安全取消
                    </Button>
                  )}
                  {(job.status === 'failed' || job.status === 'canceled') && (
                    <Button type="primary" icon={<RetweetOutlined />} onClick={() => retryTask()}>
                      从检查点重试
                    </Button>
                  )}
                </Space>
              </div>

              {job.error && <Alert className="crypto-parquet-job-error" type="error" showIcon message={job.error} />}

              <div className="crypto-parquet-table-head">
                <div>
                  <strong>分区任务</strong>
                  <span>每个文件独立记录下载、校验、筛选与重试状态</span>
                </div>
                <span>{job.files.length} 个文件</span>
              </div>
              <Table
                rowKey="uri"
                size="small"
                columns={columns}
                dataSource={[...job.files]}
                pagination={false}
                scroll={{ x: 740, y: 350 }}
              />

              <ResultFiles job={job} />
            </>
          ) : (
            <Empty
              className="crypto-parquet-empty"
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="填写左侧参数并完成分区预检后，即可开始任务"
            />
          )}
        </section>
      </div>

      <Collapse
        className="crypto-parquet-history"
        ghost
        items={[
          {
            key: 'history',
            label: `最近任务（${jobs.length}）`,
            children: (
              <div className="crypto-parquet-history-list">
                {jobs.length ? jobs.map((item) => (
                  <button type="button" key={item.id} onClick={() => setJob(item)}>
                    <span>
                      <strong>{item.start_date} 至 {item.end_date}</strong>
                      <small>{item.id} · {item.addresses.valid} 个地址 · {formatBytes(item.total_bytes)}</small>
                    </span>
                    <span>
                      <StatusTag status={item.status} />
                      {(item.status === 'failed' || item.status === 'canceled') && (
                        <Tooltip title="从 ETag 检查点和 .partial 文件继续">
                          <Button
                            size="small"
                            type="text"
                            icon={<RetweetOutlined />}
                            onClick={(event) => {
                              event.stopPropagation();
                              void retryTask(item);
                            }}
                          />
                        </Tooltip>
                      )}
                    </span>
                  </button>
                )) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无历史任务" />}
              </div>
            ),
          },
        ]}
      />

      <Modal
        title="存储与性能设置"
        open={settingsOpen}
        onCancel={() => setSettingsOpen(false)}
        onOk={saveSettings}
        confirmLoading={loading}
        okText="保存设置"
        width={720}
      >
        <Alert
          type="info"
          showIcon
          message="业务目录必须使用非系统盘绝对路径；程序不会回退到 C 盘或用户临时目录。"
        />
        <Form<ParquetSettings> form={settingsForm} layout="vertical" className="crypto-parquet-settings-form">
          <Form.Item
            name="data_root"
            label="数据根目录"
            rules={[{ required: true, message: '请输入非系统盘绝对路径' }]}
          >
            <Input prefix={<FolderOpenOutlined />} placeholder="E:\codex\etl\backend\data\crypto_parquet" />
          </Form.Item>
          <div className="crypto-parquet-settings-grid">
            <Form.Item name="download_concurrency" label="下载并发">
              <InputNumber min={1} max={4} />
            </Form.Item>
            <Form.Item name="duckdb_threads" label="DuckDB 线程">
              <InputNumber min={1} max={32} />
            </Form.Item>
            <Form.Item name="memory_limit" label="DuckDB 内存限制">
              <Input placeholder="20GB" />
            </Form.Item>
            <Form.Item name="minimum_free_gb" label="最低保留空间（GB）">
              <InputNumber min={10} max={2048} />
            </Form.Item>
          </div>
          <Space size={24}>
            <Form.Item name="keep_source_files" label="源文件策略" valuePropName="checked">
              <Switch checkedChildren="长期保留" unCheckedChildren="处理后清理" />
            </Form.Item>
            <Form.Item name="export_csv" label="附加导出" valuePropName="checked">
              <Switch checkedChildren="生成 CSV" unCheckedChildren="仅 Parquet" />
            </Form.Item>
          </Space>
        </Form>
      </Modal>
    </div>
  );
}

function ResultFiles({ job }: { job: ParquetJob }) {
  if (!job.outputs.length) return null;
  return (
    <div className="crypto-parquet-results">
      <div className="crypto-parquet-table-head">
        <div>
          <strong>结果与清单</strong>
          <span>业务结果与审计清单分开下载</span>
        </div>
      </div>
      <div className="crypto-parquet-result-list">
        {job.outputs.map((path) => (
          <div key={path}>
            <span>
              {path.toLowerCase().endsWith('.csv') ? <FileExcelOutlined /> : <DatabaseOutlined />}
              <code>{path}</code>
            </span>
            <Button
              type="link"
              icon={<CloudDownloadOutlined />}
              href={parquetOutputURL(job.id, path)}
            >
              下载
            </Button>
          </div>
        ))}
      </div>
    </div>
  );
}

function buildFileColumns(): ColumnsType<ParquetFileTask> {
  return [
    {
      title: '日期 / 数据类型',
      key: 'partition',
      width: 130,
      fixed: 'left',
      render: (_, row) => (
        <span className="crypto-parquet-partition">
          <strong>{row.source_date}</strong>
          <small>{row.data_type}</small>
        </span>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 85,
      render: (value) => <StatusTag status={String(value)} />,
    },
    {
      title: '文件进度',
      key: 'progress',
      width: 180,
      render: (_, row) => (
        <div className="crypto-parquet-file-progress">
          <Progress
            percent={Math.round(row.progress)}
            size="small"
            status={row.status === 'failed' ? 'exception' : row.status === 'done' ? 'success' : 'active'}
          />
          <small>{formatBytes(row.downloaded_bytes)} / {formatBytes(row.size_bytes)}</small>
        </div>
      ),
    },
    {
      title: '源记录',
      dataIndex: 'source_rows',
      width: 100,
      align: 'right',
      render: (value) => Number(value || 0).toLocaleString('zh-CN'),
    },
    {
      title: '命中记录',
      dataIndex: 'matched_rows',
      width: 100,
      align: 'right',
      render: (value) => <strong>{Number(value || 0).toLocaleString('zh-CN')}</strong>,
    },
    {
      title: '错误 / 输出',
      key: 'detail',
      width: 145,
      ellipsis: true,
      render: (_, row) => row.error
        ? <Typography.Text type="danger" ellipsis={{ tooltip: row.error }}>{row.error}</Typography.Text>
        : <Typography.Text type="secondary" ellipsis={{ tooltip: row.output_path }}>{row.output_path || row.uri}</Typography.Text>,
    },
  ];
}

function StatusTag({ status }: { status: string }) {
  const meta = statusMeta[status] ?? { label: status || '未知', color: 'default' };
  return <Tag color={meta.color}>{meta.label}</Tag>;
}

function stageLabel(status: string) {
  return statusMeta[status]?.label ?? '等待';
}

function summarizeAddresses(raw: string): AddressSummary {
  const fields = raw.split(/[\s,，;；|]+/).map((item) => item.trim()).filter(Boolean);
  const seen = new Set<string>();
  let valid = 0;
  let invalid = 0;
  let duplicates = 0;
  for (const field of fields) {
    const normalized = field.toLowerCase();
    if (!/^0x[0-9a-f]{40}$/.test(normalized)) {
      invalid += 1;
      continue;
    }
    if (seen.has(normalized)) {
      duplicates += 1;
      continue;
    }
    seen.add(normalized);
    valid += 1;
  }
  return { input: fields.length, valid, invalid, duplicates };
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}`;
}

function formatDuration(seconds: number) {
  if (seconds < 60) return `${Math.max(1, Math.round(seconds))} 秒`;
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} 分钟`;
  return `${(seconds / 3600).toFixed(1)} 小时`;
}
