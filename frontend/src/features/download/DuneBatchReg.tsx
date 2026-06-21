import { DownloadOutlined, PlayCircleOutlined, ReloadOutlined, StopOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input, InputNumber, Space, Table, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useMemo, useState } from 'react';
import {
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
  verifying: '等验证',
  logging_in: '登录中',
  captcha: '需验证',
  done: '完成',
  failed: '失败',
};

const STATUS_COLORS: Record<DuneBatchAccount['status'], string> = {
  pending: 'default',
  registering: 'processing',
  verifying: 'processing',
  logging_in: 'processing',
  captcha: 'warning',
  done: 'success',
  failed: 'error',
};

export function DuneBatchReg() {
  const [form] = Form.useForm<DuneBatchStartValues>();
  const [task, setTask] = useState<DuneBatchTask | null>(null);
  const [loading, setLoading] = useState(false);
  const [exporting, setExporting] = useState(false);

  useEffect(() => {
    void refreshStatus(false);
  }, []);

  useEffect(() => {
    if (task?.status !== 'running') return;
    const timer = window.setInterval(() => void refreshStatus(false), 2000);
    return () => window.clearInterval(timer);
  }, [task?.status]);

  const columns = useMemo<ColumnsType<DuneBatchAccount>>(() => [
    { title: '#', width: 56, render: (_value, _record, index) => index + 1 },
    { title: '邮箱', dataIndex: 'email', minWidth: 260 },
    { title: '密码', dataIndex: 'password', minWidth: 180 },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (status: DuneBatchAccount['status']) => <Tag color={STATUS_COLORS[status]}>{STATUS_LABELS[status]}</Tag>,
    },
    { title: '错误', dataIndex: 'error', ellipsis: true },
  ], []);

  async function submit(values: DuneBatchStartValues) {
    setLoading(true);
    try {
      const next = await startDuneBatch(values);
      setTask(next);
      message.success('批量注册任务已启动');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '启动失败');
    } finally {
      setLoading(false);
    }
  }

  async function stopTask() {
    setLoading(true);
    try {
      setTask(await stopDuneBatch());
    } catch (error) {
      message.error(error instanceof Error ? error.message : '停止失败');
    } finally {
      setLoading(false);
    }
  }

  async function refreshStatus(showMessage = true) {
    try {
      const next = await loadDuneBatchStatus();
      setTask(next);
      if (showMessage) message.success('状态已刷新');
    } catch (error) {
      if (showMessage) message.error(error instanceof Error ? error.message : '刷新失败');
    }
  }

  async function exportCSV() {
    setExporting(true);
    try {
      const filename = await exportDuneBatchCSV();
      message.success(`已导出 ${filename}`);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '导出失败');
    } finally {
      setExporting(false);
    }
  }

  return (
    <div className="download-shell dune-shell">
      <div className="panel-head">
        <div>
          <h2>Dune 批量注册</h2>
          <p>邮箱配置和已注册账户列表。</p>
        </div>
        <Space wrap>
          <Tag color={task?.status === 'running' ? 'processing' : task?.status === 'done' ? 'success' : 'default'}>
            {task?.status ?? 'idle'} · {task?.completed ?? 0}/{task?.total ?? 0}
          </Tag>
          <Button icon={<ReloadOutlined />} onClick={() => void refreshStatus()} />
          <Button icon={<DownloadOutlined />} loading={exporting} disabled={!task?.accounts.length} onClick={() => void exportCSV()}>
            导出 CSV
          </Button>
        </Space>
      </div>

      <Form
        form={form}
        layout="vertical"
        className="dune-batch-form"
        initialValues={{
          total: 1,
          domain: 'aurore.online',
          intervalSeconds: 60,
          imapHost: 'imap.gmail.com:993',
          imapUser: 'ldj1009538134@gmail.com',
        }}
        onFinish={submit}
      >
        <div className="download-grid dune-batch-grid">
          <Form.Item name="domain" label="域名" rules={[{ required: true, message: '请输入域名' }]}>
            <Input />
          </Form.Item>
          <Form.Item name="imapUser" label="接收邮箱" rules={[{ required: true, message: '请输入接收邮箱' }]}>
            <Input />
          </Form.Item>
          <Form.Item name="imapPassword" label="Gmail App Password" rules={[{ required: true, message: '请输入 Gmail App Password' }]}>
            <Input.Password autoComplete="off" />
          </Form.Item>
          <Form.Item name="imapHost" label="IMAP">
            <Input />
          </Form.Item>
          <Form.Item name="total" label="注册数量">
            <InputNumber min={1} max={100} className="full" />
          </Form.Item>
          <Form.Item name="intervalSeconds" label="间隔秒数">
            <InputNumber min={0} max={86400} className="full" />
          </Form.Item>
        </div>
        <div className="dune-action-row">
          <Space wrap>
            <Button type="primary" htmlType="submit" icon={<PlayCircleOutlined />} loading={loading} disabled={task?.status === 'running'}>
              开始注册
            </Button>
            <Button icon={<StopOutlined />} loading={loading} disabled={task?.status !== 'running'} onClick={() => void stopTask()}>
              停止
            </Button>
          </Space>
        </div>
      </Form>

      <Alert className="download-alert" type="warning" showIcon message="密码仅当前会话可用；Cookie/Auth/Token 仅在 CSV 中导出。" />

      <Table<DuneBatchAccount>
        rowKey="email"
        columns={columns}
        dataSource={task?.accounts ?? []}
        pagination={false}
        scroll={{ x: 920 }}
        locale={{ emptyText: '暂无账户' }}
      />
    </div>
  );
}
