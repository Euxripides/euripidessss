import {
  CloudDownloadOutlined,
  DownloadOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  StopOutlined,
} from '@ant-design/icons';
import { Alert, Button, Card, Checkbox, Form, Input, InputNumber, Progress, Radio, Select, Space, Statistic, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  cancelCryptoDownload,
  loadCryptoDownloadJob,
  loadCryptoDownloadJobs,
  loadCryptoDownloadSettings,
  resumeCryptoDownload,
  saveCryptoDownloadSettings,
  startCryptoDownload,
  type CryptoDownloadAddressProgress,
  type CryptoDownloadJob,
  type CryptoDownloadSource,
  type CryptoDownloadStartValues,
} from './cryptoDownloadApi';
import './crypto-download.css';

type CryptoDownloadFormValues = {
  readonly source: CryptoDownloadSource;
  readonly addresses: string;
  readonly chains: string[];
  readonly rpcUrl?: string;
  readonly csvEmail?: string;
  readonly csvImapHost?: string;
  readonly csvImapPort?: number;
  readonly csvImapUser?: string;
  readonly csvImapPassword?: string;
  readonly csvStartTime?: number;
  readonly csvEndTime?: number;
  readonly csvRequestHar?: string;
  readonly outputDir?: string;
  readonly outputPrefix?: string;
  readonly workers?: number;
  readonly rps?: number;
  readonly timeoutSeconds?: number;
  readonly retries?: number;
  readonly pageSize?: number;
  readonly startBlock?: number;
  readonly endBlock?: number;
  readonly cutoffBlock?: number;
  readonly blockBatch?: number;
  readonly logBatch?: number;
  readonly traceMode?: string;
  readonly rawDir?: string;
  readonly details?: boolean;
  readonly scanNative?: boolean;
  readonly incremental?: boolean;
  readonly riskCooldownSecs?: number;
};

const chainOptions = ['ETH', 'BSC', 'POLYGON', 'ARBITRUM', 'BASE', 'OP', 'AVAXC'].map((chain) => ({ value: chain, label: chain }));
const sourceOptions = [
  { label: 'RPC', value: 'rpc' },
  { label: 'OKLink CSV', value: 'csv' },
  { label: '浏览器', value: 'browser' },
];

export function CryptoDownloadPanel() {
  const [form] = Form.useForm<CryptoDownloadFormValues>();
  const [loading, setLoading] = useState(false);
  const [currentJob, setCurrentJob] = useState<CryptoDownloadJob | null>(null);
  const [jobs, setJobs] = useState<readonly CryptoDownloadJob[]>([]);
  const [source, setSource] = useState<CryptoDownloadSource>('rpc');
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    void bootstrap();
    return () => {
      if (timerRef.current) window.clearInterval(timerRef.current);
    };
  }, []);

  useEffect(() => {
    if (!currentJob?.id || !currentJob.running) {
      if (timerRef.current) window.clearInterval(timerRef.current);
      timerRef.current = null;
      return;
    }
    if (timerRef.current) return;
    timerRef.current = window.setInterval(() => void refreshJob(false), 1000);
  }, [currentJob?.id, currentJob?.running]);

  const addressColumns = useMemo<ColumnsType<CryptoDownloadAddressProgress>>(
    () => [
      {
        title: '地址',
        dataIndex: 'address',
        width: 300,
        render: (address: string, item) => (
          <div className="crypto-download-address">
            <Typography.Text code ellipsis={{ tooltip: address }}>{address}</Typography.Text>
            <Space size={4}>
              <Tag>{item.chain || '-'}</Tag>
              <Tag color={statusColor(item.status)}>{statusText(item.status)}</Tag>
            </Space>
          </div>
        ),
      },
      {
        title: '进度',
        dataIndex: 'progress',
        width: 180,
        render: (_, item) => <Progress percent={Math.round(item.progress ?? 0)} size="small" />,
      },
      {
        title: '下载量',
        width: 150,
        render: (_, item) => `${formatCount(item.downloaded)} / ${formatTotal(item.total)}`,
      },
      {
        title: '最近状态',
        dataIndex: 'message',
        ellipsis: true,
        render: (value: string) => value || '-',
      },
      {
        title: '操作',
        width: 88,
        render: (_, item) => (
          <Button
            size="small"
            icon={<StopOutlined />}
            disabled={!currentJob?.running || terminalStatus(item.status)}
            onClick={() => void cancelAddress(item.index)}
          >
            取消
          </Button>
        ),
      },
    ],
    [currentJob?.running],
  );

  async function bootstrap() {
    try {
      const [settings, list] = await Promise.all([loadCryptoDownloadSettings(), loadCryptoDownloadJobs()]);
      const nextSource = (settings.source ?? 'rpc') as CryptoDownloadSource;
      const outputDir = settings.outputDir && settings.outputDir !== 'exports' ? settings.outputDir : 'backend/data/crypto_download/exports';
      const rawDir = settings.rawDir && settings.rawDir !== 'raw' ? settings.rawDir : 'backend/data/crypto_download/raw';
      setSource(nextSource);
      form.setFieldsValue({
        source: nextSource,
        chains: ['ETH'],
        workers: 4,
        rps: 2,
        timeoutSeconds: 30,
        retries: 4,
        pageSize: 50,
        outputPrefix: 'wallet_export',
        traceMode: 'auto',
        blockBatch: 100,
        logBatch: 50,
        riskCooldownSecs: 900,
        details: true,
        scanNative: true,
        ...settings,
        rawDir,
        outputDir,
      });
      setJobs(list);
      setCurrentJob(list.find((job) => job.running) ?? list[0] ?? null);
    } catch (error) {
      message.warning(error instanceof Error ? error.message : '读取虚拟币下载状态失败');
    }
  }

  async function submit(values: CryptoDownloadFormValues) {
    const addressChains = parseAddressChains(values.addresses, values.chains);
    if (!addressChains.length) {
      message.warning('请至少输入一个地址');
      return;
    }
    setLoading(true);
    try {
      const payload: CryptoDownloadStartValues = {
        ...values,
        chains: (values.chains ?? []).join(','),
        addressChains,
        addresses: values.addresses,
      };
      await saveCryptoDownloadSettings({
        source: values.source,
        csvEmail: values.csvEmail,
        csvImapHost: values.csvImapHost,
        csvImapPort: values.csvImapPort,
        csvImapUser: values.csvImapUser,
        csvImapPassword: values.csvImapPassword,
        csvStartTime: values.csvStartTime,
        csvEndTime: values.csvEndTime,
        workers: values.workers,
        rps: values.rps,
        timeoutSeconds: values.timeoutSeconds,
        rawDir: values.rawDir,
        outputDir: values.outputDir,
        outputPrefix: values.outputPrefix,
        incremental: values.incremental,
        riskCooldownSecs: values.riskCooldownSecs,
      });
      const job = await startCryptoDownload(payload);
      setCurrentJob(job);
      await refreshJobs();
      message.success('虚拟币下载任务已启动');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '启动虚拟币下载失败');
    } finally {
      setLoading(false);
    }
  }

  async function refreshJob(showMessage = true) {
    if (!currentJob?.id) return;
    try {
      const job = await loadCryptoDownloadJob(currentJob.id);
      setCurrentJob(job);
      if (showMessage) message.success('已刷新任务状态');
      if (!job.running) await refreshJobs();
    } catch (error) {
      if (showMessage) message.error(error instanceof Error ? error.message : '刷新失败');
    }
  }

  async function refreshJobs() {
    try {
      setJobs(await loadCryptoDownloadJobs());
    } catch {
      // keep current list
    }
  }

  async function cancelJob() {
    if (!currentJob?.id) return;
    try {
      setCurrentJob(await cancelCryptoDownload(currentJob.id));
      await refreshJobs();
      message.success('已请求取消任务');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '取消失败');
    }
  }

  async function cancelAddress(index: number) {
    if (!currentJob?.id) return;
    try {
      setCurrentJob(await cancelCryptoDownload(currentJob.id, index));
      message.success('已请求取消该地址');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '取消地址失败');
    }
  }

  async function resumeJob() {
    if (!currentJob?.id) return;
    try {
      setCurrentJob(await resumeCryptoDownload(currentJob.id));
      message.success('已继续下载');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '继续下载失败');
    }
  }

  const canResume = currentJob?.status === 'paused' || currentJob?.status === 'cooling';
  const resultFiles = currentJob?.results ?? [];
  const errors = currentJob?.errors ?? [];

  return (
    <div className="crypto-download-shell">
      <section className="topbar">
        <div>
          <div className="topbar-title-row">
            <h1>虚拟币数据下载</h1>
          </div>
          <p>按地址和链批量下载链上交易、代币转账、资产与下载报告，支持断点续跑、邮箱 CSV 和地址级取消。</p>
        </div>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={() => void refreshJob()}>刷新</Button>
          <Button icon={<PlayCircleOutlined />} disabled={!canResume} onClick={() => void resumeJob()}>继续</Button>
          <Button danger icon={<PauseCircleOutlined />} disabled={!currentJob?.running} onClick={() => void cancelJob()}>取消任务</Button>
        </Space>
      </section>

      <div className="crypto-download-layout">
        <Card className="crypto-download-form-card">
          <Form form={form} layout="vertical" onFinish={submit} onValuesChange={(changed) => {
            if ('source' in changed) setSource(changed.source);
          }}>
            <Form.Item name="source" label="数据源">
              <Radio.Group optionType="button" buttonStyle="solid" options={sourceOptions} />
            </Form.Item>
            <Form.Item name="addresses" label="地址列表" rules={[{ required: true, message: '请输入地址' }]}>
              <Input.TextArea rows={8} placeholder="每行一个地址；也支持：地址,链 或 地址 链" />
            </Form.Item>
            <Form.Item name="chains" label="默认链">
              <Select mode="multiple" options={chainOptions} />
            </Form.Item>
            {source === 'rpc' && (
              <Form.Item name="rpcUrl" label="RPC URL">
                <Input placeholder="留空则使用内置公共 RPC；也可填写单链 RPC" />
              </Form.Item>
            )}
            {source === 'csv' && (
              <div className="crypto-download-grid">
                <Form.Item name="csvEmail" label="接收邮箱"><Input /></Form.Item>
                <Form.Item name="csvImapHost" label="IMAP Host"><Input placeholder="imap.gmail.com" /></Form.Item>
                <Form.Item name="csvImapPort" label="IMAP 端口"><InputNumber min={1} className="full" /></Form.Item>
                <Form.Item name="csvImapUser" label="IMAP 用户"><Input /></Form.Item>
                <Form.Item name="csvImapPassword" label="IMAP 密码"><Input.Password /></Form.Item>
                <Form.Item name="csvRequestHar" label="OKLink HAR"><Input placeholder="可选，复用签名请求" /></Form.Item>
              </div>
            )}
            <div className="crypto-download-grid compact">
              <Form.Item name="workers" label="并发"><InputNumber min={1} max={64} className="full" /></Form.Item>
              <Form.Item name="rps" label="RPS"><InputNumber min={0} step={0.5} className="full" /></Form.Item>
              <Form.Item name="timeoutSeconds" label="超时秒"><InputNumber min={1} className="full" /></Form.Item>
              <Form.Item name="retries" label="重试"><InputNumber min={0} className="full" /></Form.Item>
              <Form.Item name="pageSize" label="页大小"><InputNumber min={1} max={100} className="full" /></Form.Item>
              <Form.Item name="riskCooldownSecs" label="429 冷却秒"><InputNumber min={1} className="full" /></Form.Item>
            </div>
            <div className="crypto-download-grid">
              <Form.Item name="outputDir" label="输出目录"><Input /></Form.Item>
              <Form.Item name="outputPrefix" label="文件前缀"><Input /></Form.Item>
              <Form.Item name="rawDir" label="Raw 目录"><Input /></Form.Item>
              <Form.Item name="traceMode" label="内部交易模式">
                <Select options={['auto', 'trace-filter', 'debug-all', 'none'].map((value) => ({ value, label: value }))} />
              </Form.Item>
            </div>
            <Space size={[16, 8]} wrap className="crypto-download-checks">
              <Form.Item name="incremental" valuePropName="checked"><Checkbox>断点续跑</Checkbox></Form.Item>
              <Form.Item name="details" valuePropName="checked"><Checkbox>补充交易详情</Checkbox></Form.Item>
              <Form.Item name="scanNative" valuePropName="checked"><Checkbox>扫描原生交易</Checkbox></Form.Item>
            </Space>
            <Button type="primary" htmlType="submit" icon={<CloudDownloadOutlined />} loading={loading} block>
              开始下载
            </Button>
          </Form>
        </Card>

        <div className="crypto-download-status">
          <Card>
            <Space direction="vertical" size={14} className="full">
              <div className="crypto-download-job-head">
                <div>
                  <Typography.Text type="secondary">当前任务</Typography.Text>
                  <h2>{currentJob?.id ?? '暂无任务'}</h2>
                </div>
                {currentJob && <Tag color={statusColor(currentJob.status)}>{statusText(currentJob.status)}</Tag>}
              </div>
              <Progress percent={Math.round(currentJob?.progress ?? 0)} status={currentJob?.status === 'failed' ? 'exception' : undefined} />
              <div className="crypto-download-stats">
                <Statistic title="完成地址" value={currentJob?.done ?? 0} suffix={`/ ${currentJob?.total ?? 0}`} />
                <Statistic title="队列运行" value={currentJob?.queueActive ?? 0} />
                <Statistic title="队列等待" value={currentJob?.queueWaiting ?? 0} />
              </div>
              <Alert type={currentJob?.status === 'failed' ? 'error' : 'info'} showIcon message={currentJob?.message || '等待任务'} />
            </Space>
          </Card>

          <Card title="地址进度">
            <Table
              size="small"
              rowKey={(item) => `${item.index}-${item.address}-${item.chain}`}
              columns={addressColumns}
              dataSource={[...(currentJob?.addresses ?? [])]}
              pagination={{ pageSize: 6 }}
              scroll={{ x: 760 }}
            />
          </Card>
        </div>
      </div>

      <div className="crypto-download-bottom">
        <Card title="结果文件">
          {resultFiles.length ? resultFiles.map((file) => (
            <div key={file} className="crypto-download-file">
              <Typography.Text copyable>{file}</Typography.Text>
              <Button size="small" icon={<DownloadOutlined />} onClick={() => openLocalPath(file)}>打开</Button>
            </div>
          )) : <Typography.Text type="secondary">暂无结果文件</Typography.Text>}
        </Card>
        <Card title="错误">
          {errors.length ? errors.map((error, index) => <Alert key={`${index}-${error}`} type="error" message={error} />) : <Typography.Text type="secondary">暂无错误</Typography.Text>}
        </Card>
        <Card title="最近任务">
          <Space direction="vertical" className="full">
            {jobs.slice(0, 8).map((job) => (
              <Button key={job.id} type={job.id === currentJob?.id ? 'primary' : 'default'} onClick={() => setCurrentJob(job)}>
                {job.id} · {statusText(job.status)} · {job.progress ?? 0}%
              </Button>
            ))}
          </Space>
        </Card>
      </div>
    </div>
  );
}

function parseAddressChains(raw: string, defaultChains: readonly string[] = []) {
  const chains = defaultChains.length ? defaultChains : ['ETH'];
  return raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .flatMap((line) => {
      const [address, chain] = line.split(/[\s,，|]+/).filter(Boolean);
      if (!address) return [];
      if (chain) return [{ address, chain: chain.toUpperCase() }];
      return chains.map((item) => ({ address, chain: item.toUpperCase() }));
    });
}

function statusText(status?: string) {
  switch (status) {
    case 'running': return '下载中';
    case 'done':
    case 'complete': return '完成';
    case 'failed': return '失败';
    case 'paused': return '已暂停';
    case 'cooling': return '冷却中';
    case 'queued': return '排队中';
    case 'cancelled': return '已取消';
    default: return '等待';
  }
}

function statusColor(status?: string) {
  switch (status) {
    case 'running': return 'blue';
    case 'done':
    case 'complete': return 'green';
    case 'failed': return 'red';
    case 'paused':
    case 'cooling': return 'orange';
    case 'cancelled': return 'default';
    default: return 'default';
  }
}

function terminalStatus(status?: string) {
  return status === 'done' || status === 'complete' || status === 'failed' || status === 'cancelled';
}

function formatCount(value?: number) {
  return Number(value ?? 0).toLocaleString();
}

function formatTotal(value?: number) {
  if (typeof value !== 'number' || value < 0) return '待统计';
  return value.toLocaleString();
}

function openLocalPath(path: string) {
  window.open(`file:///${path.split('\\').join('/')}`, '_blank', 'noopener,noreferrer');
}
