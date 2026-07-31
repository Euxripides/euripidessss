import { useState, useCallback, useEffect } from 'react';
import {
  Card, Form, Input, Button, Select, Switch, Table, Tag, Progress, Space, Typography, message, Modal, Descriptions, Empty
} from 'antd';
import {
  PauseCircleOutlined, PlayCircleOutlined,
  StopOutlined, ReloadOutlined, DatabaseOutlined, CloudServerOutlined
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';

// ── Types ──
// REVIEW-FIX: useEffect mount + resp.ok guard + Record<JobStatus> + unused imports removed

type JobStatus = 'CREATED' | 'VALIDATING' | 'QUEUED' | 'RUNNING' | 'PAUSED' | 'COMPLETED' | 'FAILED' | 'CANCELLED';
type JobStage = 'IDLE' | 'DISCOVERING' | 'RESOLVING_RANGE' | 'PLANNING' | 'AWAITING_SCHEDULE' | 'DOWNLOADING' | 'WRITING' | 'INDEXING' | 'VALIDATING_OUTPUT' | 'FINALIZING';

interface JobRecord {
  job_id: string;
  job_type: string;
  chain_id: string;
  status: JobStatus;
  stage: JobStage;
  progress: number;
  created_at: string;
  error_message?: string;
}

const statusColor: Record<JobStatus, string> = {
  CREATED: 'default', VALIDATING: 'processing', QUEUED: 'blue', RUNNING: 'processing',
  PAUSED: 'warning', COMPLETED: 'success', FAILED: 'error', CANCELLED: 'default'
};

const chainOptions = [
  { value: 'bsc', label: 'BNB Smart Chain (灰度中)' },
  { value: 'eth', label: 'Ethereum (待灰度)' },
  { value: 'base', label: 'Base (待灰度)' },
  { value: 'arbitrum', label: 'Arbitrum One (待灰度)' },
];

// ── Component ──

export function DownloadCenterPage() {
  const [form] = Form.useForm();
  const [jobs, setJobs] = useState<JobRecord[]>([]);
  const [selectedJob, setSelectedJob] = useState<JobRecord | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);

  const loadJobs = useCallback(async () => {
    try {
      const resp = await fetch('/api/download-engine/jobs');
      if (!resp.ok) throw new Error((await resp.json()).detail);
      const data = await resp.json();
      setJobs(Array.isArray(data) ? data : []);
    } catch {
      message.error('加载任务列表失败');
    }
  }, []);

  useEffect(() => { loadJobs(); }, [loadJobs]);

  const createJob = useCallback(async (values: any) => {
    try {
      const resp = await fetch('/api/download-engine/jobs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          job_type: 'ADDRESS_SINGLE',
          chain_id: values.chain_id,
          addresses: values.addresses.split(/[\n,]+/).filter(Boolean),
          datasets: values.datasets ?? ['transactions'],
          range_mode: values.use_first_seen ? 'AUTO_FIRST_SEEN' : 'TIME_RANGE',
          priority: 2,
        }),
      });
      if (!resp.ok) throw new Error((await resp.json()).detail);
      message.success('任务已创建');
      form.resetFields();
      await loadJobs();
    } catch (e: any) {
      message.error(e.message || '创建失败');
    }
  }, [form, loadJobs]);

  const jobAction = useCallback(async (jobId: string, action: string) => {
    try {
      const resp = await fetch(`/api/download-engine/jobs/${jobId}/${action}`, { method: 'POST' });
      if (!resp.ok) throw new Error((await resp.json()).detail);
      message.success(`${action} 成功`);
      await loadJobs();
    } catch (e: any) {
      message.error(e.message || `${action} 失败`);
    }
  }, [loadJobs]);

  const columns: ColumnsType<JobRecord> = [
    { title: '任务ID', dataIndex: 'job_id', width: 160, ellipsis: true },
    { title: '链', dataIndex: 'chain_id', width: 60, render: (v) => v?.toUpperCase() },
    { title: '状态', dataIndex: 'status', width: 90, render: (v) => <Tag color={statusColor[v as JobStatus] || 'default'}>{v}</Tag> },
    { title: '阶段', dataIndex: 'stage', width: 130, render: (v) => v || '—' },
    { title: '进度', dataIndex: 'progress', width: 120, render: (v) => <Progress percent={v ?? 0} size="small" /> },
    { title: '操作', width: 180, render: (_, record) => (
      <Space size={4}>
        <Button size="small" icon={<PlayCircleOutlined />} disabled={record.status !== 'PAUSED'} onClick={() => jobAction(record.job_id, 'resume')} />
        <Button size="small" icon={<PauseCircleOutlined />} disabled={record.status !== 'RUNNING'} onClick={() => jobAction(record.job_id, 'pause')} />
        <Button size="small" icon={<StopOutlined />} danger disabled={record.status === 'COMPLETED' || record.status === 'CANCELLED'} onClick={() => jobAction(record.job_id, 'cancel')} />
        <Button size="small" icon={<ReloadOutlined />} disabled={record.status !== 'FAILED'} onClick={() => jobAction(record.job_id, 'retry-failed')} />
      </Space>
    )},
  ];

  return (
    <div style={{ padding: 24 }}>
      <Typography.Title level={4}>
        <DatabaseOutlined /> 下载中心
      </Typography.Title>

      <Card title="创建下载任务" style={{ marginBottom: 16 }}>
        <Form form={form} layout="inline" onFinish={createJob} initialValues={{ chain_id: 'bsc', use_first_seen: true, datasets: ['transactions'] }}>
          <Form.Item name="chain_id" label="链" rules={[{ required: true }]}>
            <Select options={chainOptions} style={{ width: 200 }} />
          </Form.Item>
          <Form.Item name="addresses" label="地址" rules={[{ required: true }]}>
            <Input.TextArea rows={2} placeholder="每行一个地址" style={{ width: 320 }} />
          </Form.Item>
          <Form.Item name="use_first_seen" valuePropName="checked" label="自动首次时间">
            <Switch />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" icon={<CloudServerOutlined />}>创建任务</Button>
          </Form.Item>
        </Form>
      </Card>

      <Card title={`任务列表 (${jobs.length})`} extra={<Button icon={<ReloadOutlined />} onClick={loadJobs}>刷新</Button>}>
        {jobs.length ? (
          <Table rowKey="job_id" columns={columns} dataSource={jobs} pagination={{ pageSize: 10 }} size="small"
            onRow={(record) => ({ onClick: () => { setSelectedJob(record); setDetailOpen(true); }, style: { cursor: 'pointer' } })} />
        ) : (
          <Empty description="暂无任务" />
        )}
      </Card>

      <Modal open={detailOpen} onCancel={() => setDetailOpen(false)} title="任务详情" footer={null} width={640}>
        {selectedJob && (
          <Descriptions column={2} size="small" bordered>
            <Descriptions.Item label="ID">{selectedJob.job_id}</Descriptions.Item>
            <Descriptions.Item label="状态"><Tag color={statusColor[selectedJob.status]}>{selectedJob.status}</Tag></Descriptions.Item>
            <Descriptions.Item label="阶段">{selectedJob.stage}</Descriptions.Item>
            <Descriptions.Item label="进度"><Progress percent={selectedJob.progress ?? 0} size="small" /></Descriptions.Item>
            <Descriptions.Item label="创建时间">{selectedJob.created_at}</Descriptions.Item>
            {selectedJob.error_message && <Descriptions.Item label="错误" span={2}>{selectedJob.error_message}</Descriptions.Item>}
          </Descriptions>
        )}
      </Modal>
    </div>
  );
}
