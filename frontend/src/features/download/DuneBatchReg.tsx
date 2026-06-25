import { DeleteOutlined, DownloadOutlined, PlayCircleOutlined, ReloadOutlined, StopOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input, InputNumber, message, Popconfirm, Radio, Select, Space, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useMemo, useState } from 'react';
import {
  deleteDuneBatchAccounts,
  exportDuneBatchCSV,
  loadDuneBatchStatus,
  startDuneBatch,
  stopDuneBatch,
  type DuneBatchAccount,
  type DuneBatchStartValues,
  type DuneBatchTask,
} from './duneBatchApi';

const STATUS_LABELS: Record<DuneBatchAccount['status'], string> = {
  pending: '待开始',
  registering: '注册中',
  verifying: '邮件验证中',
  logging_in: '登录中',
  captcha: '需人机验证',
  done: '已完成',
  failed: '失败',
  wait_verify: '待验证登录',
  banned: '封禁',
};

const STATUS_COLORS: Record<DuneBatchAccount['status'], string> = {
  pending: 'default',
  registering: 'processing',
  verifying: 'processing',
  logging_in: 'processing',
  captcha: 'warning',
  done: 'success',
  failed: 'error',
  wait_verify: 'blue',
  banned: '#ff4d4f',
};

type BatchMode = 'full' | 'register' | 'verify_login';

const STORAGE_KEY_MODE = 'dune_batch_mode';
const STORAGE_KEY_FORM = 'dune_batch_form';

function loadSaved<T>(key: string, fallback: T): T {
  try { const raw = localStorage.getItem(key); return raw ? JSON.parse(raw) : fallback; } catch { return fallback; }
}
function save(key: string, value: unknown) { try { localStorage.setItem(key, JSON.stringify(value)); } catch {} }

const ALL_STATUS = '__all__';

export function DuneBatchReg() {
  const [form] = Form.useForm<DuneBatchStartValues>();
  const [task, setTask] = useState<DuneBatchTask | null>(null);
  const [loading, setLoading] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [mode, setMode] = useState<BatchMode>(() => loadSaved(STORAGE_KEY_MODE, 'full'));
  const [filterStatus, setFilterStatus] = useState<string>(ALL_STATUS);
  const [selectedKeys, setSelectedKeys] = useState<React.Key[]>([]);

  useEffect(() => { void refreshStatus(false); const s = loadSaved(STORAGE_KEY_FORM, {}); form.setFieldsValue(s); }, []);

  useEffect(() => {
    if (task?.status !== 'running') return;
    const timer = window.setInterval(() => void refreshStatus(false), 2000);
    return () => window.clearInterval(timer);
  }, [task?.status]);

  const filteredAccounts = useMemo(() => {
    const list = task?.accounts ?? [];
    if (filterStatus === ALL_STATUS) return list;
    return list.filter(a => a.status === filterStatus);
  }, [task?.accounts, filterStatus]);

  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const a of (task?.accounts ?? [])) { counts[a.status] = (counts[a.status] ?? 0) + 1; }
    return counts;
  }, [task?.accounts]);

  const columns = useMemo<ColumnsType<DuneBatchAccount>>(() => [
    { title: '#', width: 50, render: (_v, _r, idx) => idx + 1 },
    { title: '邮箱', dataIndex: 'email', minWidth: 260 },
    { title: '密码', dataIndex: 'password', minWidth: 170 },
    { title: '状态', dataIndex: 'status', width: 110, render: (s: DuneBatchAccount['status']) => <Tag color={STATUS_COLORS[s]}>{STATUS_LABELS[s]}</Tag> },
    { title: '错误', dataIndex: 'error', ellipsis: true, width: 180 },
    {
      title: '操作', width: 70,
      render: (_v, record) => (
        <Popconfirm title="确认删除？" onConfirm={() => doDelete([record.email])} okText="删除" cancelText="取消">
          <Button size="small" danger icon={<DeleteOutlined />} />
        </Popconfirm>
      ),
    },
  ], []);

  async function submit(values: DuneBatchStartValues) {
    setLoading(true);
    save(STORAGE_KEY_FORM, values);
    try {
      const next = await startDuneBatch({ ...values, mode });
      setTask(next);
      if (next.redirected_from) {
        message.info('未找到 auth.json，已自动切换为手动抓取模式，请在打开的浏览器中登录 Dune');
      } else {
        message.success(mode === 'verify_login' ? '批量验证登录已启动' : '批量注册已启动');
      }
    } catch (e) { message.error(e instanceof Error ? e.message : '启动失败'); }
    finally { setLoading(false); }
  }

  async function stopTask() {
    setLoading(true);
    try { setTask(await stopDuneBatch()); } catch (e) { message.error(e instanceof Error ? e.message : '停止失败'); }
    finally { setLoading(false); }
  }

  async function refreshStatus(show = true) {
    try { setTask(await loadDuneBatchStatus()); if (show) message.success('已刷新'); } catch {}
  }

  async function doDelete(emails: readonly string[]) {
    setDeleting(true);
    try {
      const n = await deleteDuneBatchAccounts(emails);
      message.success(`已删除 ${n} 个`);
      setSelectedKeys([]);
      await refreshStatus(false);
    } catch (e) { message.error(e instanceof Error ? e.message : '删除失败'); }
    finally { setDeleting(false); }
  }

  async function exportCSV() {
    setExporting(true);
    try { message.success(`已导出 ${await exportDuneBatchCSV()}`); } catch (e) { message.error(e instanceof Error ? e.message : '导出失败'); }
    finally { setExporting(false); }
  }

  const isRegister = mode !== 'verify_login';
  const waitCount = statusCounts['wait_verify'] ?? 0;
  const total = task?.accounts?.length ?? 0;

  return (
    <div className="download-shell dune-shell">
      <div className="panel-head">
        <h2>Dune 批量注册</h2>
        <Space wrap>
          <Tag color={task?.status === 'running' ? 'processing' : task?.status === 'done' ? 'success' : 'default'}>{task?.status ?? 'idle'} · {task?.completed ?? 0}/{task?.total ?? 0}</Tag>
          <Button icon={<ReloadOutlined />} onClick={() => refreshStatus()} />
          <Button icon={<DownloadOutlined />} loading={exporting} disabled={!total} onClick={() => exportCSV()}>导出</Button>
        </Space>
      </div>

      <Form form={form} layout="vertical" className="dune-batch-form" onFinish={submit}>
        <Form.Item label="模式">
          <Radio.Group value={mode} onChange={e => { setMode(e.target.value); save(STORAGE_KEY_MODE, e.target.value); }} optionType="button" buttonStyle="solid">
            <Radio.Button value="full">完整流程</Radio.Button>
            <Radio.Button value="register">仅注册</Radio.Button>
            <Radio.Button value="verify_login">仅验证登录</Radio.Button>
          </Radio.Group>
        </Form.Item>

        {isRegister && (
          <div className="download-grid dune-batch-grid">
            <Form.Item name="domain" label="域名" rules={[{ required: true }]}><Input /></Form.Item>
            <Form.Item name="imapUser" label="接收邮箱" rules={[{ required: true }]}><Input /></Form.Item>
            <Form.Item name="imapPassword" label="App Password" rules={[{ required: true }]}><Input.Password autoComplete="off" /></Form.Item>
            <Form.Item name="imapHost" label="IMAP"><Input /></Form.Item>
            <Form.Item name="total" label="数量"><InputNumber min={1} max={100} className="full" /></Form.Item>
            <Form.Item name="intervalSeconds" label="间隔(秒)"><InputNumber min={0} max={86400} className="full" /></Form.Item>
          </div>
        )}

        {!isRegister && waitCount > 0 && <Alert type="info" showIcon style={{ marginBottom: 12 }} message={`${waitCount} 个账户待验证登录`} />}
        {!isRegister && waitCount === 0 && <Alert type="warning" showIcon style={{ marginBottom: 12 }} message="没有待验证账户" />}

        <Space wrap style={{ marginBottom: 12 }}>
          <Button type="primary" htmlType="submit" icon={<PlayCircleOutlined />} loading={loading}
            disabled={task?.status === 'running' || (!isRegister && waitCount === 0)}>
            {isRegister ? '开始注册' : '开始验证登录'}
          </Button>
          <Button icon={<StopOutlined />} loading={loading} disabled={task?.status !== 'running'} onClick={stopTask}>停止</Button>
        </Space>
      </Form>

      <Space wrap style={{ marginBottom: 12 }}>
        <Select value={filterStatus} onChange={v => { setFilterStatus(v); setSelectedKeys([]); }} style={{ width: 160 }}
          options={[
            { value: ALL_STATUS, label: `全部 (${total})` },
            ...Object.entries(STATUS_LABELS).map(([k, v]) => ({ value: k, label: `${v} (${statusCounts[k] ?? 0})` })),
          ]}
        />
        {selectedKeys.length > 0 && (
          <Popconfirm title={`确认删除选中的 ${selectedKeys.length} 个？`} onConfirm={() => doDelete(selectedKeys as string[])} okText="删除" cancelText="取消">
            <Button danger icon={<DeleteOutlined />} loading={deleting}>删除选中</Button>
          </Popconfirm>
        )}
      </Space>

      <Table<DuneBatchAccount>
        rowKey="email"
        columns={columns}
        dataSource={filteredAccounts}
        rowSelection={{ selectedRowKeys: selectedKeys, onChange: keys => setSelectedKeys(keys) }}
        pagination={false}
        scroll={{ x: 920 }}
        locale={{ emptyText: '暂无账户' }}
        size="small"
      />
    </div>
  );
}
