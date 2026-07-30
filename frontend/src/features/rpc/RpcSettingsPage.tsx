import {
  ApiOutlined,
  CheckCircleFilled,
  CloudServerOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  EditOutlined,
  ExclamationCircleFilled,
  InfoCircleOutlined,
  PlayCircleOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  StopOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import {
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Progress,
  Row,
  Select,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from 'antd';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { rpcApi } from './rpcApi';
import type { EnrichmentJob, RpcEndpoint, RpcEndpointInput, RpcHealthResponse, RpcStatus } from './rpcTypes';
import './rpc-settings.css';

const CHAINS = [
  { value: 'bsc', label: 'BSC', color: '#f0b90b' },
  { value: 'eth', label: 'Ethereum', color: '#627eea' },
  { value: 'base', label: 'Base', color: '#0052ff' },
  { value: 'arbitrum', label: 'Arbitrum', color: '#28a0f0' },
];

const STATUS: Record<RpcStatus, { label: string; color: string }> = {
  HEALTHY: { label: '健康', color: 'success' },
  DEGRADED: { label: '降级', color: 'warning' },
  RATE_LIMITED: { label: '限流', color: 'orange' },
  UNAVAILABLE: { label: '不可用', color: 'error' },
  MISCONFIGURED: { label: '配置异常', color: 'magenta' },
  DISABLED: { label: '已停用', color: 'default' },
};

const EMPTY: RpcHealthResponse = {
  overview: {
    configured_endpoints: 0, healthy_endpoints: 0, degraded_endpoints: 0,
    today_requests: 0, cache_hit_rate: 0, rate_limited_count: 0,
  },
  endpoints: [],
  routing: {},
};

type RpcEndpointForm = RpcEndpointInput & { use_test_endpoint: boolean };

export function RpcSettingsPage() {
  const [data, setData] = useState<RpcHealthResponse>(EMPTY);
  const [jobs, setJobs] = useState<EnrichmentJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testingId, setTestingId] = useState('');
  const [editing, setEditing] = useState<RpcEndpoint | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [jobOpen, setJobOpen] = useState(false);
  const [form] = Form.useForm<RpcEndpointForm>();
  const [jobForm] = Form.useForm<{ job_type: string; chain_key: string; items: string }>();
  const useTestEndpoint = Form.useWatch('use_test_endpoint', form);

  const refresh = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true);
    try {
      const [health, enrichmentJobs] = await Promise.all([rpcApi.health(), rpcApi.jobs()]);
      setData(health);
      setJobs(enrichmentJobs);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取 RPC 状态失败');
    } finally {
      if (!quiet) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(true), 15_000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  const openCreate = () => {
    setEditing(null);
    form.setFieldsValue({
      provider: 'CHAINSTACK', chain_key: 'bsc', display_name: '', endpoint_url: '',
      test_endpoint_url: '', use_test_endpoint: false,
      priority: 10, enabled: true, max_rps: 3, max_concurrency: 2, request_timeout_ms: 8000,
    });
    setDialogOpen(true);
  };

  const openEdit = (item: RpcEndpoint) => {
    setEditing(item);
    form.setFieldsValue({
      provider: item.provider, chain_key: item.chain_key, display_name: item.display_name,
      endpoint_url: '', test_endpoint_url: '', use_test_endpoint: item.test_endpoint_configured,
      priority: item.priority, enabled: item.enabled, max_rps: item.max_rps,
      max_concurrency: item.max_concurrency, request_timeout_ms: item.request_timeout_ms,
    });
    setDialogOpen(true);
  };

  const save = async () => {
    const values = await form.validateFields();
    const { use_test_endpoint: useTest, ...endpointValues } = values;
    setSaving(true);
    try {
      if (editing) {
        const payload: Partial<RpcEndpointInput> = { ...endpointValues };
        if (!endpointValues.endpoint_url) delete payload.endpoint_url;
        if (useTest && !endpointValues.test_endpoint_url && editing.test_endpoint_configured) {
          delete payload.test_endpoint_url;
        }
        if (!useTest) payload.test_endpoint_url = '';
        await rpcApi.update(editing.endpoint_id, payload);
        message.success('节点配置已更新并通过连接校验');
      } else {
        await rpcApi.create({ ...endpointValues, test_endpoint_url: useTest ? endpointValues.test_endpoint_url : '' });
        message.success('节点已加密保存并加入路由');
      }
      setDialogOpen(false);
      await refresh(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '保存节点失败');
    } finally {
      setSaving(false);
    }
  };

  const test = async (item: RpcEndpoint) => {
    setTestingId(item.endpoint_id);
    try {
      const result = await rpcApi.test(item.endpoint_id);
      const endpointLabel = result.endpoint_role === 'TEST' ? '测试 Endpoint' : '正常 Endpoint';
      if (result.success) message.success(`${endpointLabel} 连接正常：区块 ${result.latest_block.toLocaleString()}，${result.latency_ms} ms`);
      else message.error(`${result.error_message || '连接失败'} ${result.suggestion || ''}`);
      await refresh(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '连接测试失败');
    } finally {
      setTestingId('');
    }
  };

  const move = async (chain: string, items: RpcEndpoint[], index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= items.length) return;
    const ids = items.map((item) => item.endpoint_id);
    [ids[index], ids[target]] = [ids[target], ids[index]];
    try {
      await rpcApi.routing(chain, ids);
      await refresh(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '更新路由失败');
    }
  };

  const startJob = async () => {
    const values = await jobForm.validateFields();
    const items = values.items.split(/\r?\n|,/).map((item) => item.trim()).filter(Boolean);
    try {
      await rpcApi.startJob({ job_type: values.job_type, chain_key: values.chain_key, items });
      message.success('富化任务已提交');
      setJobOpen(false);
      jobForm.resetFields();
      await refresh(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '提交任务失败');
    }
  };

  const metrics = useMemo(() => [
    { label: '已配置节点', value: data.overview.configured_endpoints, suffix: '个', icon: <CloudServerOutlined />, tone: 'blue', tip: '安全存储中已登记的 RPC Endpoint 数量' },
    { label: '健康节点', value: data.overview.healthy_endpoints, suffix: '个', icon: <SafetyCertificateOutlined />, tone: 'green', tip: '最近健康检查成功且未落后主节点的数量' },
    { label: '降级节点', value: data.overview.degraded_endpoints, suffix: '个', icon: <ExclamationCircleFilled />, tone: 'orange', tip: '延迟、失败率或限流导致自动降级的数量' },
    { label: '今日请求', value: data.overview.today_requests, suffix: '次', icon: <ThunderboltOutlined />, tone: 'indigo', tip: 'RPC 管理器今日发出的富化读取请求' },
    { label: '缓存命中率', value: data.overview.cache_hit_rate.toFixed(1), suffix: '%', icon: <DatabaseOutlined />, tone: 'cyan', tip: '地址状态和 Token Metadata 直接命中本地缓存的比例' },
    { label: '限流次数', value: data.overview.rate_limited_count, suffix: '次', icon: <StopOutlined />, tone: 'purple', tip: '上游返回 429 或配额错误的累计次数' },
  ], [data.overview]);

  return (
    <div className="rpc-page">
      <header className="rpc-hero">
        <div>
          <Typography.Text className="rpc-kicker">EVM RPC CONTROL PLANE</Typography.Text>
          <Typography.Title level={2}>RPC 节点管理</Typography.Title>
          <Typography.Paragraph>安全托管 Endpoint，自动限速、熔断和故障切换，为链上富化提供稳定通道。</Typography.Paragraph>
        </div>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={() => void refresh()}>刷新健康状态</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新增节点</Button>
        </Space>
      </header>

      <Row gutter={[14, 14]} className="rpc-metrics">
        {metrics.map((item) => (
          <Col xs={12} sm={8} xl={4} key={item.label}>
            <Card className={`rpc-metric rpc-metric-${item.tone}`}>
              <div className="rpc-metric-icon">{item.icon}</div>
              <div>
                <Tooltip title={item.tip}><span className="rpc-metric-label">{item.label} <InfoCircleOutlined /></span></Tooltip>
                <strong>{typeof item.value === 'number' ? item.value.toLocaleString() : item.value}<small>{item.suffix}</small></strong>
              </div>
            </Card>
          </Col>
        ))}
      </Row>

      <Spin spinning={loading}>
        <div className="rpc-workspace">
          <Card className="rpc-node-card" title={<span><ApiOutlined /> 节点列表</span>} extra={<Tag>{data.endpoints.length} 个 Endpoint</Tag>}>
            <Table<RpcEndpoint>
              rowKey="endpoint_id"
              size="middle"
              pagination={{ pageSize: 8, hideOnSinglePage: true }}
              scroll={{ x: 980 }}
              dataSource={data.endpoints}
              columns={[
                {
                  title: 'Provider / 链', width: 130,
                  render: (_, row) => <div className="rpc-provider"><strong>{row.provider}</strong><span>{row.chain_key.toUpperCase()} · {row.chain_id}</span></div>,
                },
                {
                  title: '节点名称 / Endpoint', width: 250,
                  render: (_, row) => <div className="rpc-endpoint"><strong>{row.display_name}</strong><Tooltip title={row.endpoint_masked}><code>{row.endpoint_masked}</code></Tooltip>{row.test_endpoint_configured ? <Tag color="purple">独立测试 Endpoint</Tag> : null}</div>,
                },
                { title: '优先级', dataIndex: 'priority', width: 74, align: 'center' },
                {
                  title: '状态', width: 105,
                  render: (_, row) => <Tooltip title={row.health.last_error_message_redacted || `熔断器：${row.health.circuit_state}`}><Tag color={STATUS[row.health.status].color}>{STATUS[row.health.status].label}</Tag></Tooltip>,
                },
                { title: 'P95', width: 78, render: (_, row) => `${Math.round(row.health.latency_p95_ms)} ms` },
                { title: '成功率', width: 84, render: (_, row) => `${row.health.success_rate_5m.toFixed(1)}%` },
                { title: '最新区块', width: 110, render: (_, row) => row.health.latest_block ? row.health.latest_block.toLocaleString() : '—' },
                { title: '落后', width: 64, render: (_, row) => row.health.block_lag || '0' },
                { title: '当前 RPS', width: 86, render: (_, row) => row.current_rps.toFixed(2) },
                {
                  title: '操作', fixed: 'right', width: 170,
                  render: (_, row) => <Space size={4}>
                    <Tooltip title="测试连接"><Button aria-label={`测试 ${row.display_name}`} type="text" icon={<PlayCircleOutlined />} loading={testingId === row.endpoint_id} onClick={() => void test(row)} /></Tooltip>
                    <Tooltip title="编辑"><Button aria-label={`编辑 ${row.display_name}`} type="text" icon={<EditOutlined />} onClick={() => openEdit(row)} /></Tooltip>
                    <Tooltip title={row.enabled ? '停用' : '启用'}><Switch size="small" checked={row.enabled} onChange={(enabled) => void rpcApi.update(row.endpoint_id, { enabled }).then(() => refresh(true)).catch((error: Error) => message.error(error.message))} /></Tooltip>
                    <Popconfirm title="确认删除这个节点？" description="节点配置和健康历史将被移除。" onConfirm={() => void rpcApi.remove(row.endpoint_id).then(() => refresh(true)).catch((error: Error) => message.error(error.message))}>
                      <Button aria-label={`删除 ${row.display_name}`} danger type="text" icon={<DeleteOutlined />} />
                    </Popconfirm>
                  </Space>,
                },
              ]}
            />
          </Card>

          <Card className="rpc-routing-card" title="路由配置（按链）" extra={<Tag color="green">自动故障转移已启用</Tag>}>
            <div className="rpc-routing-list">
              {CHAINS.map((chain) => {
                const endpoints = data.routing[chain.value] || [];
                return (
                  <section className="rpc-route" key={chain.value}>
                    <div className="rpc-route-head"><span style={{ background: chain.color }} /> <strong>{chain.label}</strong><small>{endpoints.length} 个节点</small></div>
                    {endpoints.length ? endpoints.map((endpoint, index) => (
                      <div className="rpc-route-item" key={endpoint.endpoint_id}>
                        <b>{index + 1}</b>
                        <span><strong>{endpoint.display_name}</strong><small>{endpoint.provider}</small></span>
                        <Tag color={STATUS[endpoint.health.status].color}>{STATUS[endpoint.health.status].label}</Tag>
                        <Space.Compact>
                          <Button size="small" disabled={index === 0} onClick={() => void move(chain.value, endpoints, index, -1)}>↑</Button>
                          <Button size="small" disabled={index === endpoints.length - 1} onClick={() => void move(chain.value, endpoints, index, 1)}>↓</Button>
                        </Space.Compact>
                      </div>
                    )) : <div className="rpc-route-empty">尚未配置节点</div>}
                  </section>
                );
              })}
            </div>
          </Card>
        </div>

        <Card
          className="rpc-jobs"
          title={<span><DatabaseOutlined /> 数据富化任务</span>}
          extra={<Button size="small" icon={<PlusOutlined />} onClick={() => setJobOpen(true)}>新建任务</Button>}
        >
          <Table<EnrichmentJob>
            rowKey="job_id"
            size="small"
            pagination={{ pageSize: 5, hideOnSinglePage: true }}
            scroll={{ x: 820 }}
            dataSource={jobs}
            locale={{ emptyText: '暂无富化任务；地址页面也会按需使用同一缓存。' }}
            columns={[
              { title: '任务', render: (_, row) => row.job_type === 'TOKEN_METADATA' ? 'Token Metadata' : '地址状态 / 余额' },
              { title: '链', dataIndex: 'chain_key', render: (value: string) => value.toUpperCase(), width: 90 },
              {
                title: '进度', width: 230,
                render: (_, row) => <Progress size="small" percent={row.total_items ? Math.round(row.completed_items / row.total_items * 100) : 0} status={row.status === 'FAILED' ? 'exception' : undefined} />,
              },
              { title: '成功', dataIndex: 'succeeded_items', width: 80, render: (value: number) => <span className="rpc-success">{value}</span> },
              { title: '失败', dataIndex: 'failed_items', width: 80, render: (value: number) => <span className="rpc-failure">{value}</span> },
              { title: '缓存命中', dataIndex: 'cache_hits', width: 100 },
              { title: '状态', dataIndex: 'status', width: 100, render: (value: string) => <Tag color={value === 'COMPLETED' ? 'success' : value === 'FAILED' ? 'error' : 'processing'}>{value}</Tag> },
              {
                title: '操作', width: 90,
                render: (_, row) => ['RUNNING', 'QUEUED'].includes(row.status)
                  ? <Button size="small" danger onClick={() => void rpcApi.cancelJob(row.job_id).then(() => refresh(true))}>取消</Button> : '—',
              },
            ]}
          />
        </Card>
      </Spin>

      <Modal className="rpc-endpoint-modal" title={editing ? '编辑 RPC 节点' : '新增 RPC 节点'} open={dialogOpen} onCancel={() => setDialogOpen(false)} onOk={() => void save()} okText={editing ? '校验并保存' : '测试并保存'} confirmLoading={saving} width={680} destroyOnHidden>
        <Form form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={12}><Form.Item name="provider" label="Provider" rules={[{ required: true }]}><Select options={['CHAINSTACK', 'ANKR', 'NODEREAL', 'CUSTOM'].map((value) => ({ value, label: value }))} /></Form.Item></Col>
            <Col span={12}><Form.Item name="chain_key" label="链" rules={[{ required: true }]}><Select options={CHAINS} /></Form.Item></Col>
          </Row>
          <Form.Item name="display_name" label="节点名称" rules={[{ required: true, message: '请输入便于识别的节点名称' }]}><Input placeholder="例如：Chainstack-BSC-Primary" /></Form.Item>
          <Form.Item name="endpoint_url" label={editing ? '新 Endpoint（留空保持原配置）' : '完整 Endpoint URL'} rules={editing ? [] : [{ required: true, message: '请输入供应商提供的 HTTPS Endpoint' }]}>
            <Input.Password autoComplete="new-password" placeholder="https://provider.example/v1/YOUR_API_KEY" />
          </Form.Item>
          <Form.Item name="use_test_endpoint" label="使用独立测试 Endpoint" valuePropName="checked">
            <Switch />
          </Form.Item>
          {useTestEndpoint ? (
            <Form.Item
              name="test_endpoint_url"
              label={editing && editing.test_endpoint_configured ? '新测试 Endpoint（留空保持原配置）' : '测试 Endpoint URL'}
              extra="仅用于手动测试连接；自动路由、定时健康检查和正式请求仍使用正常 Endpoint。"
              rules={editing && editing.test_endpoint_configured ? [] : [{ required: true, message: '请输入测试 Endpoint' }]}
            >
              <Input.Password autoComplete="new-password" placeholder="https://provider.example/test/YOUR_API_KEY" />
            </Form.Item>
          ) : null}
          <Row gutter={16}>
            <Col xs={12} sm={6}><Form.Item name="priority" label="优先级"><InputNumber min={1} max={999} /></Form.Item></Col>
            <Col xs={12} sm={6}><Form.Item name="max_rps" label="最大 RPS"><InputNumber min={0.25} max={100} step={0.25} /></Form.Item></Col>
            <Col xs={12} sm={6}><Form.Item name="max_concurrency" label="最大并发"><InputNumber min={1} max={32} /></Form.Item></Col>
            <Col xs={12} sm={6}><Form.Item name="request_timeout_ms" label="超时（ms）"><InputNumber min={1000} max={30000} step={500} /></Form.Item></Col>
          </Row>
          <Form.Item name="enabled" label="加入自动路由" valuePropName="checked"><Switch /></Form.Item>
        </Form>
      </Modal>

      <Modal title="新建数据富化任务" open={jobOpen} onCancel={() => setJobOpen(false)} onOk={() => void startJob()} okText="提交任务">
        <Form form={jobForm} layout="vertical" initialValues={{ job_type: 'TOKEN_METADATA', chain_key: 'bsc' }}>
          <Form.Item name="job_type" label="任务类型" rules={[{ required: true }]}><Select options={[{ value: 'TOKEN_METADATA', label: 'Token Metadata' }, { value: 'ADDRESS_STATE', label: '地址类型与原生币余额' }]} /></Form.Item>
          <Form.Item name="chain_key" label="链" rules={[{ required: true }]}><Select options={CHAINS} /></Form.Item>
          <Form.Item name="items" label="地址列表" extra="每行一个 EVM 地址，最多 10,000 条。" rules={[{ required: true }]}><Input.TextArea rows={8} placeholder="0x..." /></Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
