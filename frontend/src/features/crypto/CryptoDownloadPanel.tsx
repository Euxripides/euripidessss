import {
  CloudDownloadOutlined,
  DeleteOutlined,
  DownloadOutlined,
  ImportOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  SettingOutlined,
  StopOutlined,
} from '@ant-design/icons';
import { Alert, Button, Card, Checkbox, Collapse, Form, Input, InputNumber, Modal, Popconfirm, Progress, Radio, Select, Space, Statistic, Table, Tag, Typography, message, notification } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  cancelCryptoDownload,
  cryptoDownloadResultFileUrl,
  deleteCryptoDownloadHistory,
  importCryptoDownloadHistory,
  loadCryptoDownloadHistory,
  loadCryptoDownloadJob,
  loadCryptoDownloadJobs,
  loadCryptoDownloadSettings,
  resumeCryptoDownload,
  resumeCryptoDownloadHistory,
  saveCryptoDownloadSettings,
  startCryptoDownload,
  type CryptoDownloadAddressProgress,
  type CryptoDownloadAddressChain,
  type CryptoDownloadJob,
  type CryptoDownloadHistoryRecord,
  type CryptoCSVDeliveryMode,
  type CryptoDownloadSource,
  type CryptoDownloadStartValues,
} from './cryptoDownloadApi';
import './crypto-download.css';

type CryptoDownloadFormValues = {
  readonly source: CryptoDownloadSource;
  readonly addresses: string;
  readonly chains: string[];
  readonly rpcUrl?: string;
  readonly rpcConfig?: string;
  readonly nativeSymbol?: string;
  readonly csvEmail?: string;
  readonly csvImapHost?: string;
  readonly csvImapPort?: number;
  readonly csvImapUser?: string;
  readonly csvImapPassword?: string;
  readonly csvMailPool?: string;
  readonly csvProxyPool?: string;
  readonly csvImapProxyPool?: string;
  readonly csvProxyPin?: number;
  readonly csvDeliveryMode?: CryptoCSVDeliveryMode;
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
  readonly amlKey?: string;
  readonly amlLabels?: boolean;
  readonly amlRps?: number;
  readonly filterExchange?: boolean;
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

// simplifyPauseMessage strips the "已暂停：" prefix and everything after a
// "原因：" marker, keeping only the actionable guidance for the toast
// notification.  Full details remain visible in the task status area.
function simplifyPauseMessage(message: string): string {
  const trimmed = message.replace(/^已暂停[：:]\s*/, '');
  const reasonIndex = trimmed.indexOf('原因：');
  return reasonIndex >= 0 ? trimmed.slice(0, reasonIndex).trim() : trimmed;
}

export function CryptoDownloadPanel() {
  const [form] = Form.useForm<CryptoDownloadFormValues>();
  const [notificationApi, notificationHolder] = notification.useNotification({ placement: 'topRight', maxCount: 4 });
  const [loading, setLoading] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [currentJob, setCurrentJob] = useState<CryptoDownloadJob | null>(null);
  const [jobs, setJobs] = useState<readonly CryptoDownloadJob[]>([]);
  const [history, setHistory] = useState<readonly CryptoDownloadHistoryRecord[]>([]);
  const [selectedHistoryIds, setSelectedHistoryIds] = useState<readonly string[]>([]);
  const [pendingHistoryImportIds, setPendingHistoryImportIds] = useState<readonly string[]>([]);
  const [historyActionLoading, setHistoryActionLoading] = useState(false);
  const [source, setSource] = useState<CryptoDownloadSource>('rpc');
  const [settingsModalOpen, setSettingsModalOpen] = useState(false);
  const [addressChainModalOpen, setAddressChainModalOpen] = useState(false);
  const [pendingAddressChains, setPendingAddressChains] = useState<readonly CryptoDownloadAddressChain[]>([]);
  const timerRef = useRef<number | null>(null);
  const pausedNotifiedRef = useRef<string | null>(null);
  const pendingStartValuesRef = useRef<CryptoDownloadFormValues | null>(null);

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

  useEffect(() => {
    const status = currentJob?.status;
    if (!currentJob?.id || (status !== 'paused' && status !== 'cooling')) {
      pausedNotifiedRef.current = null;
      return;
    }
    const signature = `${currentJob.id}|${status}|${currentJob.message ?? ''}`;
    if (pausedNotifiedRef.current === signature) return;
    pausedNotifiedRef.current = signature;
    notificationApi.warning({
      key: `crypto-paused-${currentJob.id}`,
      message: status === 'paused' ? '任务已暂停' : '任务冷却中',
      description: simplifyPauseMessage(currentJob.message ?? '') || '请检查下载配置和网络后重试',
      duration: 10,
    });
  }, [currentJob?.id, currentJob?.status, currentJob?.message]);

  useEffect(() => {
    if (!currentJob?.id) return;
    const files = expandResultFiles(currentJob.results ?? []);
    const jobErrors = (currentJob.errors ?? []).filter(Boolean);
    if (files.length) {
      const fileNotification = {
        key: `crypto-results-${currentJob.id}`,
        message: jobErrors.length
          ? `下载失败，已保留诊断文件（${files.length}）`
          : `结果文件已生成（${files.length}）`,
        description: (
          <div className="crypto-download-notification-list">
            {files.slice(0, 5).map((file) => (
              <div className="crypto-download-notification-row" key={file}>
                <Typography.Text ellipsis={{ tooltip: file }}>{localFileName(file)}</Typography.Text>
                <Button
                  size="small"
                  icon={<DownloadOutlined />}
                  href={cryptoDownloadResultFileUrl(currentJob.id, file)}
                  target="_blank"
                >
                  下载
                </Button>
              </div>
            ))}
            {files.length > 5 && <Typography.Text type="secondary">另有 {files.length - 5} 个文件</Typography.Text>}
          </div>
        ),
        duration: 0,
      };
      if (jobErrors.length) notificationApi.warning(fileNotification);
      else notificationApi.success(fileNotification);
    }
    if (jobErrors.length) {
      notificationApi.error({
        key: `crypto-errors-${currentJob.id}`,
        message: `任务错误（${jobErrors.length}）`,
        description: (
          <div className="crypto-download-notification-errors">
            {jobErrors.slice(0, 3).map((error, index) => (
              <Typography.Paragraph key={`${index}-${error}`} ellipsis={{ rows: 3, expandable: true }}>
                {error}
              </Typography.Paragraph>
            ))}
            {jobErrors.length > 3 && <Typography.Text type="secondary">另有 {jobErrors.length - 3} 条错误</Typography.Text>}
          </div>
        ),
        duration: 0,
      });
    }
  }, [currentJob?.id, currentJob?.results?.length, currentJob?.errors?.length, notificationApi]);

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
      const [settings, list, historyList] = await Promise.all([
        loadCryptoDownloadSettings(),
        loadCryptoDownloadJobs(),
        loadCryptoDownloadHistory(),
      ]);
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
        startBlock: 0,
        endBlock: -1,
        cutoffBlock: 0,
        outputPrefix: 'wallet_export',
        traceMode: 'auto',
        blockBatch: 100,
        logBatch: 50,
        riskCooldownSecs: 1800,
        amlLabels: true,
        amlRps: 2,
        filterExchange: true,
        details: true,
        scanNative: true,
        ...settings,
        rawDir,
        outputDir,
      });
      setJobs(list);
      setHistory(historyList);
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
    if (addressChains.length > 1 && !hasExplicitChainForEveryAddress(values.addresses)) {
      pendingStartValuesRef.current = values;
      setPendingAddressChains(addressChains);
      setAddressChainModalOpen(true);
      return;
    }
    await startDownload(values, addressChains);
  }

  async function startDownload(values: CryptoDownloadFormValues, addressChains: readonly CryptoDownloadAddressChain[]) {
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
        csvMailPool: values.csvMailPool,
        csvProxyPool: values.csvProxyPool,
        csvImapProxyPool: values.csvImapProxyPool,
        csvDeliveryMode: values.csvDeliveryMode,
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
      await Promise.all([refreshJobs(), refreshHistory()]);
      message.success('虚拟币下载任务已启动');
      return true;
    } catch (error) {
      message.error(error instanceof Error ? error.message : '启动虚拟币下载失败');
      return false;
    } finally {
      setLoading(false);
    }
  }

  async function saveSettingsAndClose() {
    setSettingsSaving(true);
    try {
      if (source === 'csv') {
        await form.validateFields(['csvEmail', 'csvImapHost', 'csvImapPort', 'csvImapUser']);
      }
      const values = form.getFieldsValue();
      await saveCryptoDownloadSettings({
        source: values.source,
        csvEmail: values.csvEmail,
        csvImapHost: values.csvImapHost,
        csvImapPort: values.csvImapPort,
        csvImapUser: values.csvImapUser,
        csvImapPassword: values.csvImapPassword,
        csvMailPool: values.csvMailPool,
        csvProxyPool: values.csvProxyPool,
        csvImapProxyPool: values.csvImapProxyPool,
        csvProxyPin: values.csvProxyPin ?? 0,
        csvDeliveryMode: values.csvDeliveryMode,
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
      setSettingsModalOpen(false);
      message.success('下载设置已安全保存');
    } catch (error) {
      if (error instanceof Error) {
        message.error(error.message || '保存下载设置失败');
      }
    } finally {
      setSettingsSaving(false);
    }
  }

  async function confirmAddressChains() {
    const values = pendingStartValuesRef.current;
    if (!pendingAddressChains.length) return;
    if (!values) {
      form.setFieldValue('addresses', pendingAddressChains.map((item) => `${item.address},${item.chain}`).join('\n'));
      closeAddressChainModal();
      message.success('已确认每个地址的链，请点击开始下载');
      return;
    }
    if (await startDownload(values, pendingAddressChains)) {
      closeAddressChainModal();
    }
  }

  function openAddressChainConfirmation() {
    const raw = form.getFieldValue('addresses') ?? '';
    const rows = parseAddressChains(raw, form.getFieldValue('chains') ?? []);
    if (rows.length < 2) {
      message.info(rows.length ? '当前只有一个地址，无需逐一确认链' : '请先输入地址');
      return;
    }
    pendingStartValuesRef.current = null;
    setPendingAddressChains(rows);
    setAddressChainModalOpen(true);
  }

  function handleAddressPaste() {
    window.setTimeout(() => {
      const raw = form.getFieldValue('addresses') ?? '';
      if (parseAddressChains(raw, form.getFieldValue('chains') ?? []).length > 1) {
        openAddressChainConfirmation();
      }
    }, 0);
  }

  function closeAddressChainModal() {
    if (loading) return;
    pendingStartValuesRef.current = null;
    setPendingAddressChains([]);
    setAddressChainModalOpen(false);
  }

  function updatePendingAddressChain(index: number, chain: string) {
    setPendingAddressChains((current) => current.map((item, itemIndex) => (
      itemIndex === index ? { ...item, chain } : item
    )));
  }

  async function refreshJob(showMessage = true) {
    if (!currentJob?.id) return;
    try {
      const job = await loadCryptoDownloadJob(currentJob.id);
      setCurrentJob(job);
      if (showMessage) message.success('已刷新任务状态');
      if (!job.running) await Promise.all([refreshJobs(), refreshHistory()]);
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

  async function refreshHistory() {
    try {
      const records = await loadCryptoDownloadHistory();
      setHistory(records);
      setSelectedHistoryIds((current) => current.filter((id) => records.some((record) => record.id === id)));
    } catch {
      // keep current list
    }
  }

  function toggleHistorySelection(id: string, checked: boolean) {
    setSelectedHistoryIds((current) => (
      checked ? [...new Set([...current, id])] : current.filter((item) => item !== id)
    ));
  }

  function requestHistoryImport(ids: readonly string[]) {
    if (!ids.length) return;
    setPendingHistoryImportIds(ids);
  }

  async function confirmHistoryImport() {
    const ids = pendingHistoryImportIds;
    if (!ids.length) return;
    setHistoryActionLoading(true);
    try {
      const importedJobs = await importCryptoDownloadHistory(ids);
      const latestJob = importedJobs[importedJobs.length - 1];
      if (latestJob) setCurrentJob(latestJob);
      setSelectedHistoryIds([]);
      setPendingHistoryImportIds([]);
      await Promise.all([refreshJobs(), refreshHistory()]);
      message.success(`已重新导入 ${importedJobs.length} 条历史任务`);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '重新导入历史任务失败');
    } finally {
      setHistoryActionLoading(false);
    }
  }

  async function resumeHistory(id: string) {
    setHistoryActionLoading(true);
    try {
      const job = await resumeCryptoDownloadHistory(id);
      setCurrentJob(job);
      await Promise.all([refreshJobs(), refreshHistory()]);
      message.success('已从历史断点继续下载');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '继续历史任务失败');
    } finally {
      setHistoryActionLoading(false);
    }
  }

  async function deleteHistory(id: string) {
    setHistoryActionLoading(true);
    try {
      await deleteCryptoDownloadHistory(id);
      setSelectedHistoryIds((current) => current.filter((item) => item !== id));
      await refreshHistory();
      message.success('历史记录已删除，导出的数据文件已保留');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '删除历史记录失败');
    } finally {
      setHistoryActionLoading(false);
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
  return (
    <div className="crypto-download-shell">
      {notificationHolder}
      <section className="topbar">
        <div>
          <div className="topbar-title-row">
            <h1>虚拟币数据下载</h1>
          </div>
        </div>
        <Space>
          <Button icon={<SettingOutlined />} onClick={() => setSettingsModalOpen(true)}>下载设置</Button>
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
            <Form.Item name="addresses" label="地址列表" rules={[{ required: true, message: '请输入地址' }]}>
              <Input.TextArea
                rows={8}
                placeholder="每行一个地址；粘贴多个地址会弹窗逐一选链；也支持：地址,链 或 地址 链"
                onPaste={handleAddressPaste}
              />
            </Form.Item>
            <Button className="crypto-confirm-address-chain-button" onClick={openAddressChainConfirmation}>
              确认多地址链
            </Button>

            <Modal
              title="下载设置"
              open={settingsModalOpen}
              width={960}
              footer={<Button type="primary" loading={settingsSaving} onClick={() => void saveSettingsAndClose()}>保存设置</Button>}
              onCancel={() => setSettingsModalOpen(false)}
              styles={{ body: { maxHeight: '72vh', overflowY: 'auto' } }}
            >
              <div className="crypto-download-settings-categories">
                <Card size="small" title="下载方式与默认链">
                  <div className="crypto-download-grid">
                    <Form.Item name="source" label="数据源">
                      <Radio.Group optionType="button" buttonStyle="solid" options={sourceOptions} />
                    </Form.Item>
                    <Form.Item name="chains" label="默认链" extra="多个地址确认时使用第一个默认链作为初始值">
                      <Select mode="multiple" options={chainOptions} />
                    </Form.Item>
                  </div>
                </Card>

                {source === 'rpc' && (
                  <Card size="small" title="RPC 与区块范围">
                    <div className="crypto-download-grid">
                      <Form.Item name="rpcUrl" label="RPC URL">
                        <Input placeholder="留空则使用内置公共 RPC；也可填写单链 RPC" />
                      </Form.Item>
                      <Form.Item name="rpcConfig" label="多链 RPC 配置 JSON">
                        <Input placeholder="可选，填写 JSON 配置文件路径" />
                      </Form.Item>
                      <Form.Item name="nativeSymbol" label="原生币符号">
                        <Input placeholder="例如 ETH、BNB、MATIC" />
                      </Form.Item>
                      <Form.Item name="startBlock" label="起始区块">
                        <InputNumber min={0} className="full" />
                      </Form.Item>
                      <Form.Item name="endBlock" label="结束区块（-1 最新）">
                        <InputNumber min={-1} className="full" />
                      </Form.Item>
                      <Form.Item name="cutoffBlock" label="截止区块（不包含，0 关闭）">
                        <InputNumber min={0} className="full" />
                      </Form.Item>
                      <Form.Item name="blockBatch" label="区块批次">
                        <InputNumber min={1} className="full" />
                      </Form.Item>
                      <Form.Item name="logBatch" label="日志批次">
                        <InputNumber min={1} className="full" />
                      </Form.Item>
                    </div>
                  </Card>
                )}

                {source === 'csv' && (
                  <Card size="small" title="OKLink CSV 与接收邮箱">
                    <div className="crypto-download-grid">
                      <Form.Item name="csvDeliveryMode" label="CSV 获取方式" initialValue="auto">
                        <Select options={[
                          { value: 'auto', label: '自动（优先直链，必要时邮箱）' },
                          { value: 'direct', label: '仅直链 CSV' },
                          { value: 'email', label: '仅邮箱 CSV' },
                        ]} />
                      </Form.Item>
                      <Form.Item name="csvEmail" label="接收邮箱" rules={[{ required: true, type: 'email', message: '请输入有效接收邮箱' }]}>
                        <Input placeholder="name@gmail.com" />
                      </Form.Item>
                      <Form.Item
                        name="csvImapHost"
                        label="IMAP Host"
                        rules={[
                          { required: true, message: '请输入 IMAP 主机' },
                          { validator: (_, value) => String(value ?? '').includes('@') ? Promise.reject(new Error('这里应填写 imap.gmail.com，不能填写邮箱地址')) : Promise.resolve() },
                        ]}
                      >
                        <Input placeholder="imap.gmail.com" />
                      </Form.Item>
                      <Form.Item name="csvImapPort" label="IMAP 端口" rules={[{ required: true, message: '请输入 IMAP 端口' }]}>
                        <InputNumber min={1} className="full" />
                      </Form.Item>
                      <Form.Item name="csvImapUser" label="IMAP 用户" rules={[{ required: true, type: 'email', message: '请输入完整邮箱地址作为 IMAP 用户名' }]}>
                        <Input autoComplete="email" placeholder="Gmail 必须与接收邮箱一致" />
                      </Form.Item>
                      <Form.Item name="csvImapPassword" label="IMAP 密码" extra="Gmail 请使用应用专用密码；已保存时可留空">
                        <Input.Password autoComplete="new-password" />
                      </Form.Item>
                      <Form.Item name="csvRequestHar" label="OKLink HAR"><Input placeholder="可选，复用签名请求" /></Form.Item>
                      <Form.Item name="csvMailPool" label="邮箱池（绕限流）" extra="每行：邮箱|IMAP主机|IMAP端口|IMAP用户|IMAP密码；等待邮件超时或 IMAP 登录失败自动切换下一账号（Gmail 叠加 +alias）。已保存的池不回显，留空保留旧池">
                        <Input.TextArea rows={3} placeholder="account1@gmail.com|imap.gmail.com|993|account1@gmail.com|应用专用密码" />
                      </Form.Item>
                      <Form.Item name="csvProxyPool" label="HTTP 代理池" extra="每行一个 http/https/socks5 代理，CSV 请求按连接轮换；留空使用环境变量代理">
                        <Input.TextArea rows={2} placeholder="http://127.0.0.1:7890" />
                      </Form.Item>
                      <Form.Item name="csvImapProxyPool" label="IMAP 代理池" extra="每行一个 socks5:// 代理，IMAP 收信轮换；留空使用 OKLINK_IMAP_PROXY / ALL_PROXY">
                        <Input.TextArea rows={2} placeholder="socks5://127.0.0.1:1080" />
                      </Form.Item>
                      <Form.Item name="csvProxyPin" label="IP 锁定" extra="固定使用代理池中某个 IP（每 IP 独立指纹+域名邮箱）。此值为新任务默认，每次启动任务时仍可各自选择——开 N 个任务锁不同 IP 即 N 个独立用户并发；0=自动轮换。锁定超出池数量会报错">
                        <Select options={[
                          { value: 0, label: '自动轮换' },
                          ...Array.from({ length: 20 }, (_, index) => ({ value: index + 1, label: `IP ${index + 1}` })),
                        ]} />
                      </Form.Item>
                      <Form.Item name="csvStartTime" label="CSV 开始时间（Unix 秒）"><InputNumber min={0} className="full" /></Form.Item>
                      <Form.Item name="csvEndTime" label="CSV 结束时间（Unix 秒）"><InputNumber min={0} className="full" /></Form.Item>
                    </div>
                  </Card>
                )}

                <Card size="small" title="性能、重试与风控">
                  <div className="crypto-download-grid compact">
                    <Form.Item name="workers" label="并发"><InputNumber min={1} max={64} className="full" /></Form.Item>
                    <Form.Item name="rps" label="RPS"><InputNumber min={0} step={0.5} className="full" /></Form.Item>
                    <Form.Item name="timeoutSeconds" label="超时秒"><InputNumber min={1} className="full" /></Form.Item>
                    <Form.Item name="retries" label="重试"><InputNumber min={0} className="full" /></Form.Item>
                    <Form.Item name="pageSize" label="页大小"><InputNumber min={1} max={100} className="full" /></Form.Item>
                    <Form.Item name="riskCooldownSecs" label="429 冷却秒"><InputNumber min={1} className="full" /></Form.Item>
                  </div>
                </Card>

                <Card size="small" title="输出与数据处理">
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
                </Card>

                {source !== 'csv' && (
                  <Card size="small" title="DeepAML 与地址过滤">
                    <div className="crypto-download-grid">
                      <Form.Item name="amlKey" label="DeepAML API Key"><Input.Password autoComplete="off" /></Form.Item>
                      <Form.Item name="amlRps" label="DeepAML RPS"><InputNumber min={0} step={0.5} className="full" /></Form.Item>
                    </div>
                    <Space size={[16, 8]} wrap className="crypto-download-checks">
                      <Form.Item name="amlLabels" valuePropName="checked"><Checkbox>DeepAML 标签</Checkbox></Form.Item>
                      <Form.Item name="filterExchange" valuePropName="checked"><Checkbox>过滤交易所大地址</Checkbox></Form.Item>
                    </Space>
                  </Card>
                )}
              </div>
            </Modal>

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
                <Statistic title="已处理地址" value={currentJob?.done ?? 0} suffix={`/ ${currentJob?.total ?? 0}`} />
                <Statistic title="队列运行" value={currentJob?.queueActive ?? 0} />
                <Statistic title="队列等待" value={currentJob?.queueWaiting ?? 0} />
              </div>
              {currentJob?.status !== 'paused' && currentJob?.status !== 'cooling' && (
                <Alert type={currentJob?.status === 'failed' ? 'error' : 'info'} showIcon message={currentJob?.message || '等待任务'} />
              )}
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

          <Collapse
            className="crypto-download-recent-collapse"
            items={[{
              key: 'history-jobs',
              label: `历史任务（${history.length}）`,
              children: history.length ? (
                <div className="crypto-download-history">
                  <div className="crypto-download-history-toolbar">
                    <Space>
                      <Checkbox
                        checked={selectedHistoryIds.length === history.length}
                        indeterminate={selectedHistoryIds.length > 0 && selectedHistoryIds.length < history.length}
                        onChange={(event) => setSelectedHistoryIds(event.target.checked ? history.map((record) => record.id) : [])}
                      >
                        全选
                      </Checkbox>
                      <Typography.Text type="secondary">已选 {selectedHistoryIds.length} 条</Typography.Text>
                    </Space>
                    <Space>
                      <Button size="small" icon={<ReloadOutlined />} onClick={() => void refreshHistory()}>刷新</Button>
                      <Button
                        size="small"
                        type="primary"
                        icon={<ImportOutlined />}
                        loading={historyActionLoading}
                        disabled={!selectedHistoryIds.length}
                        onClick={() => requestHistoryImport(selectedHistoryIds)}
                      >
                        导入所选
                      </Button>
                    </Space>
                  </div>
                  <div className="crypto-download-recent-list">
                    {history.map((record) => (
                      <div className="crypto-download-recent-item" key={record.id}>
                        <Checkbox
                          checked={selectedHistoryIds.includes(record.id)}
                          aria-label={`选择历史任务 ${record.id}`}
                          onChange={(event) => toggleHistorySelection(record.id, event.target.checked)}
                        />
                        <span className="crypto-download-history-summary">
                          <strong>{record.id}</strong>
                          <small>{record.startedAt || record.finishedAt || '时间未知'} · {record.entries?.length ?? record.addresses?.length ?? record.total ?? 0} 个地址</small>
                          <small>{record.message || '等待任务'}</small>
                        </span>
                        <span className="crypto-download-recent-meta">
                          <Tag color={statusColor(record.status)}>{statusText(record.status)}</Tag>
                          <b>{Math.round(record.progress ?? 0)}%</b>
                        </span>
                        <Space size={4} className="crypto-download-history-actions">
                          <Button
                            size="small"
                            icon={<ImportOutlined />}
                            disabled={historyActionLoading}
                            onClick={() => requestHistoryImport([record.id])}
                          >
                            重新导入
                          </Button>
                          {(record.status === 'paused' || record.status === 'cooling') && (
                            <Button
                              size="small"
                              icon={<PlayCircleOutlined />}
                              disabled={historyActionLoading}
                              onClick={() => void resumeHistory(record.id)}
                            >
                              断点继续
                            </Button>
                          )}
                          <Popconfirm
                            title="删除这条历史记录？"
                            description="导出的数据文件不会被删除。"
                            okText="删除记录"
                            cancelText="取消"
                            okButtonProps={{ danger: true }}
                            onConfirm={() => void deleteHistory(record.id)}
                          >
                            <Button size="small" danger icon={<DeleteOutlined />} disabled={historyActionLoading}>删除记录</Button>
                          </Popconfirm>
                        </Space>
                      </div>
                    ))}
                  </div>
                </div>
              ) : <Typography.Text type="secondary">暂无历史任务</Typography.Text>,
            }]}
          />
        </div>
      </div>

      <Modal
        title="确认重新导入历史任务"
        open={pendingHistoryImportIds.length > 0}
        okText="确认并开始下载"
        cancelText="取消"
        confirmLoading={historyActionLoading}
        closable={!historyActionLoading}
        maskClosable={!historyActionLoading}
        onOk={() => void confirmHistoryImport()}
        onCancel={() => setPendingHistoryImportIds([])}
      >
        <Alert
          type="warning"
          showIcon
          message={`即将重新导入 ${pendingHistoryImportIds.length} 条历史任务`}
          description="只有点击“确认并开始下载”后才会创建新任务。请先核对地址、链和原任务状态。"
        />
        <div className="crypto-download-history-confirm-list">
          {pendingHistoryImportIds.map((id) => {
            const record = history.find((item) => item.id === id);
            return (
              <div key={id}>
                <Typography.Text strong>{id}</Typography.Text>
                <Typography.Text type="secondary">
                  {(record?.entries ?? []).map((entry) => `${entry.address} [${entry.chain}]`).join('，') || '无地址信息'}
                </Typography.Text>
              </div>
            );
          })}
        </div>
      </Modal>

      <Modal
        title="确认地址和链"
        open={addressChainModalOpen}
        okText={pendingStartValuesRef.current ? '确认并开始下载' : '确认并写回'}
        cancelText="取消"
        confirmLoading={loading}
        closable={!loading}
        maskClosable={!loading}
        onOk={() => void confirmAddressChains()}
        onCancel={closeAddressChainModal}
      >
        <Alert
          type="info"
          showIcon
          message={pendingStartValuesRef.current
            ? '请逐一确认每个地址所属的链；确认后立即开始下载。'
            : '请逐一确认每个地址所属的链；确认后写回地址列表，再点击开始下载。'}
        />
        <Space direction="vertical" size="middle" className="crypto-address-chain-confirm-list">
          {pendingAddressChains.map((item, index) => (
            <div className="crypto-address-chain-confirm-row" key={`${index}-${item.address}`}>
              <Typography.Text code ellipsis={{ tooltip: item.address }}>{item.address}</Typography.Text>
              <Select
                value={item.chain}
                options={chainOptions}
                onChange={(chain) => updatePendingAddressChain(index, chain)}
              />
            </div>
          ))}
        </Space>
      </Modal>
    </div>
  );
}

function parseAddressChains(raw: string, defaultChains: readonly string[] = []) {
  const defaultChain = (defaultChains[0] || 'ETH').toUpperCase();
  const seen = new Set<string>();
  return raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .flatMap<CryptoDownloadAddressChain>((line) => {
      const [address, chain] = line.split(/[\s,，|]+/).filter(Boolean);
      if (!address) return [];
      const key = address.toLowerCase();
      if (seen.has(key)) return [];
      seen.add(key);
      return [{ address, chain: (chain || defaultChain).toUpperCase() }];
    });
}

function hasExplicitChainForEveryAddress(raw: string) {
  const lines = raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  return lines.length > 0 && lines.every((line) => line.split(/[\s,，|]+/).filter(Boolean).length >= 2);
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

function localFileName(path: string) {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || path;
}

function expandResultFiles(results: readonly string[]) {
  return results.flatMap((result) => result.split(';').map((part) => part.trim()).filter(Boolean));
}
