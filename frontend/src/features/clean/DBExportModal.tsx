import { useEffect, useMemo, useRef, useState } from 'react';
import { DatabaseOutlined, PlusOutlined, SettingOutlined, StopOutlined } from '@ant-design/icons';
import {
  Alert,
  Button,
  Descriptions,
  Form,
  Input,
  InputNumber,
  message,
  Modal,
  Progress,
  Radio,
  Select,
  Space,
  Switch,
  Tag,
} from 'antd';
import {
  cancelDBExportTask,
  createDBExportTask,
  getDBExportTask,
  listDBConnections,
  loadDBDatabases,
  loadDBSchemas,
  saveDBConnection,
  testDBConnection,
  type DBConnection,
  type DBExportTask,
} from '../flow/dbImportApi';

type ExportFormValues = {
  connectionId: string;
  database: string;
  schema?: string;
  table: string;
  mode: 'append' | 'replace';
  columnNaming: 'snake_case' | 'original';
  duplicateMode: 'skip' | 'allow';
};

const defaultConnection: DBConnection = {
  name: '',
  type: 'postgresql',
  host: '127.0.0.1',
  port: 5432,
  defaultDatabase: '',
  username: '',
  password: '',
  savePassword: true,
  ssl: false,
  timeoutSeconds: 15,
};

export function DBExportModal({
  open,
  jobId,
  rowCount,
  onClose,
}: {
  open: boolean;
  jobId: string;
  rowCount: number;
  onClose: () => void;
}) {
  const [form] = Form.useForm<ExportFormValues>();
  const [connectionForm] = Form.useForm<DBConnection>();
  const [connections, setConnections] = useState<DBConnection[]>([]);
  const [databases, setDatabases] = useState<string[]>([]);
  const [schemas, setSchemas] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [connectionOpen, setConnectionOpen] = useState(false);
  const [editingConnection, setEditingConnection] = useState<DBConnection | null>(null);
  const [connectionLoading, setConnectionLoading] = useState(false);
  const [task, setTask] = useState<DBExportTask | null>(null);
  const pollTimer = useRef<number | null>(null);
  const selectedConnectionId = Form.useWatch('connectionId', form);
  const selectedDatabase = Form.useWatch('database', form);
  const selectedConnection = useMemo(
    () => connections.find((item) => item.id === selectedConnectionId),
    [connections, selectedConnectionId],
  );
  const taskActive = task?.status === 'pending' || task?.status === 'running';

  useEffect(() => {
    if (!open) return;
    setTask(null);
    form.setFieldsValue({
      table: `clean_transactions_${new Date().toISOString().slice(0, 10).replace(/-/g, '')}`,
      mode: 'append',
      columnNaming: 'snake_case',
      duplicateMode: 'skip',
    });
    void refreshConnections();
    return stopPolling;
  }, [open]);

  useEffect(() => {
    if (!selectedConnectionId) {
      setDatabases([]);
      return;
    }
    setLoading(true);
    loadDBDatabases(selectedConnectionId)
      .then((items) => {
        setDatabases(items);
        const preferred = selectedConnection?.defaultDatabase;
        if (preferred && items.includes(preferred)) form.setFieldValue('database', preferred);
        else if (!form.getFieldValue('database') && items.length) form.setFieldValue('database', items[0]);
      })
      .catch((error) => message.error(error instanceof Error ? error.message : '读取数据库列表失败'))
      .finally(() => setLoading(false));
  }, [selectedConnectionId, selectedConnection?.defaultDatabase]);

  useEffect(() => {
    if (selectedConnection?.type !== 'postgresql' && selectedConnection?.type !== 'pgsql') {
      setSchemas([]);
      form.setFieldValue('schema', undefined);
      return;
    }
    if (!selectedConnectionId || !selectedDatabase) return;
    setLoading(true);
    loadDBSchemas(selectedConnectionId, selectedDatabase)
      .then((items) => {
        setSchemas(items);
        form.setFieldValue('schema', items.includes('public') ? 'public' : items[0]);
      })
      .catch((error) => message.error(error instanceof Error ? error.message : '读取 Schema 失败'))
      .finally(() => setLoading(false));
  }, [selectedConnection?.type, selectedConnectionId, selectedDatabase]);

  function stopPolling() {
    if (pollTimer.current !== null) {
      window.clearTimeout(pollTimer.current);
      pollTimer.current = null;
    }
  }

  async function refreshConnections(preferredId?: string) {
    try {
      const items = await listDBConnections();
      setConnections(items);
      const nextId = preferredId ?? form.getFieldValue('connectionId') ?? items[0]?.id;
      if (nextId) form.setFieldValue('connectionId', nextId);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取数据库连接失败');
    }
  }

  async function startExport() {
    const values = await form.validateFields();
    setLoading(true);
    try {
      const nextTask = await createDBExportTask({ jobId, ...values });
      setTask(nextTask);
      message.success('数据库写入任务已启动');
      poll(nextTask.id);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '启动数据库写入失败');
    } finally {
      setLoading(false);
    }
  }

  function poll(id: string) {
    stopPolling();
    pollTimer.current = window.setTimeout(async () => {
      try {
        const nextTask = await getDBExportTask(id);
        setTask(nextTask);
        if (nextTask.status === 'pending' || nextTask.status === 'running') {
          poll(id);
        } else if (nextTask.status === 'done') {
          message.success(`数据库写入完成，共新增 ${nextTask.progress.insertedRows.toLocaleString('zh-CN')} 行`);
        } else if (nextTask.status === 'failed') {
          message.error(nextTask.error || '数据库写入失败');
        }
      } catch (error) {
        message.error(error instanceof Error ? error.message : '读取数据库写入进度失败');
      }
    }, 750);
  }

  async function cancelExport() {
    if (!task) return;
    try {
      const nextTask = await cancelDBExportTask(task.id);
      setTask(nextTask);
      poll(task.id);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '取消任务失败');
    }
  }

  function openConnectionEditor(connection?: DBConnection) {
    const next = connection ?? defaultConnection;
    setEditingConnection(connection ?? null);
    connectionForm.setFieldsValue({ ...next, password: '' });
    setConnectionOpen(true);
  }

  async function submitConnection(testOnly: boolean) {
    const values = await connectionForm.validateFields();
    setConnectionLoading(true);
    try {
      if (testOnly) {
        await testDBConnection({ ...editingConnection, ...values });
        message.success('数据库连接测试成功');
        return;
      }
      const saved = await saveDBConnection({ ...editingConnection, ...values });
      message.success('数据库连接已保存');
      setConnectionOpen(false);
      await refreshConnections(saved.id);
    } catch (error) {
      message.error(error instanceof Error ? error.message : testOnly ? '数据库连接测试失败' : '保存数据库连接失败');
    } finally {
      setConnectionLoading(false);
    }
  }

  const percent = task?.progress.totalRows
    ? Math.min(100, (task.progress.processedRows / task.progress.totalRows) * 100)
    : task?.status === 'done' ? 100 : 0;

  return (
    <>
      <Modal
        open={open}
        width={760}
        title={<Space><DatabaseOutlined />一键导入数据库</Space>}
        onCancel={taskActive ? undefined : onClose}
        maskClosable={!taskActive}
        footer={[
          taskActive && <Button key="cancel-task" danger icon={<StopOutlined />} onClick={cancelExport}>取消任务</Button>,
          <Button key="close" disabled={taskActive} onClick={onClose}>关闭</Button>,
          <Button key="start" type="primary" loading={loading} disabled={taskActive} onClick={startExport}>开始导入</Button>,
        ]}
      >
        <Alert
          type="info"
          showIcon
          message={`将清洗任务 ${jobId} 的统一结果（${rowCount.toLocaleString('zh-CN')} 行）流式写入 PostgreSQL 或 MySQL。`}
          description="默认使用英文 snake_case 字段和行指纹去重；再次导入同一批数据不会重复写入。源 CSV 和清洗阶段产物不会被修改。"
          style={{ marginBottom: 16 }}
        />
        <Form form={form} layout="vertical">
          <Form.Item label="数据库连接" required>
            <Space.Compact block>
              <Form.Item name="connectionId" noStyle rules={[{ required: true, message: '请选择数据库连接' }]}>
                <Select
                  loading={loading}
                  placeholder="选择已保存连接"
                  options={connections.map((item) => ({
                    value: item.id,
                    label: `${item.name} · ${item.type === 'mysql' ? 'MySQL' : 'PostgreSQL'} · ${item.host}:${item.port}`,
                  }))}
                />
              </Form.Item>
              <Button icon={<SettingOutlined />} disabled={!selectedConnection} onClick={() => openConnectionEditor(selectedConnection)}>编辑</Button>
              <Button icon={<PlusOutlined />} onClick={() => openConnectionEditor()}>新增</Button>
            </Space.Compact>
          </Form.Item>
          <Space align="start" size="middle" style={{ display: 'flex' }}>
            <Form.Item label="数据库" name="database" style={{ flex: 1 }} rules={[{ required: true, message: '请选择数据库' }]}>
              <Select showSearch loading={loading} options={databases.map((value) => ({ value }))} />
            </Form.Item>
            {(selectedConnection?.type === 'postgresql' || selectedConnection?.type === 'pgsql') && (
              <Form.Item label="Schema" name="schema" style={{ flex: 1 }} rules={[{ required: true, message: '请选择 Schema' }]}>
                <Select showSearch options={schemas.map((value) => ({ value }))} />
              </Form.Item>
            )}
            <Form.Item label="目标表" name="table" style={{ flex: 1.2 }} rules={[
              { required: true, message: '请输入目标表名' },
              { pattern: /^[A-Za-z_][A-Za-z0-9_]{0,62}$/, message: '建议使用 1-63 位英文、数字和下划线，且不能以数字开头' },
            ]}>
              <Input placeholder="clean_transactions" />
            </Form.Item>
          </Space>
          <Form.Item label="写入方式" name="mode">
            <Radio.Group>
              <Radio value="append">追加（表不存在则创建）</Radio>
              <Radio value="replace">清空重建表</Radio>
            </Radio.Group>
          </Form.Item>
          <Form.Item label="数据库字段名" name="columnNaming">
            <Radio.Group>
              <Radio value="snake_case">英文 snake_case（推荐）</Radio>
              <Radio value="original">保留中文标准字段</Radio>
            </Radio.Group>
          </Form.Item>
          <Form.Item label="重复数据" name="duplicateMode">
            <Radio.Group>
              <Radio value="skip">跳过重复行（推荐）</Radio>
              <Radio value="allow">允许重复写入</Radio>
            </Radio.Group>
          </Form.Item>
        </Form>
        {task && (
          <div className="db-export-progress">
            <Space style={{ width: '100%', justifyContent: 'space-between' }}>
              <strong>数据库写入进度</strong>
              <Tag color={task.status === 'done' ? 'success' : task.status === 'failed' ? 'error' : task.status === 'cancelled' ? 'default' : 'processing'}>
                {formatTaskStatus(task.status)}
              </Tag>
            </Space>
            <Progress
              percent={Number(percent.toFixed(1))}
              status={task.status === 'failed' ? 'exception' : task.status === 'done' ? 'success' : 'active'}
            />
            <Descriptions size="small" column={3}>
              <Descriptions.Item label="已处理">{formatCount(task.progress.processedRows)} / {formatCount(task.progress.totalRows)}</Descriptions.Item>
              <Descriptions.Item label="已写入">{formatCount(task.progress.insertedRows)}</Descriptions.Item>
              <Descriptions.Item label="跳过">{formatCount(task.progress.skippedRows)}</Descriptions.Item>
              <Descriptions.Item label="速度">{formatCount(Math.round(task.progress.speedRowsPerSecond))} 行/秒</Descriptions.Item>
              <Descriptions.Item label="已用">{formatDuration(task.progress.elapsedSeconds)}</Descriptions.Item>
              <Descriptions.Item label="预计剩余">{formatDuration(task.progress.etaSeconds)}</Descriptions.Item>
            </Descriptions>
            {task.error && <Alert type="error" showIcon message={task.error} />}
          </div>
        )}
      </Modal>
      <Modal
        open={connectionOpen}
        title={<Space><SettingOutlined />配置数据库连接</Space>}
        onCancel={() => setConnectionOpen(false)}
        footer={[
          <Button key="test" loading={connectionLoading} onClick={() => submitConnection(true)}>测试连接</Button>,
          <Button key="save" type="primary" loading={connectionLoading} onClick={() => submitConnection(false)}>保存连接</Button>,
        ]}
      >
        <Form form={connectionForm} layout="vertical" initialValues={defaultConnection}>
          <Form.Item label="连接名称" name="name" rules={[{ required: true, message: '请输入连接名称' }]}>
            <Input placeholder="本地调查数据库" />
          </Form.Item>
          <Space align="start" size="middle" style={{ display: 'flex' }}>
            <Form.Item label="数据库类型" name="type" style={{ flex: 1 }}>
              <Select
                options={[
                  { value: 'postgresql', label: 'PostgreSQL' },
                  { value: 'mysql', label: 'MySQL' },
                ]}
                onChange={(value) => connectionForm.setFieldValue('port', value === 'mysql' ? 3306 : 5432)}
              />
            </Form.Item>
            <Form.Item label="主机" name="host" style={{ flex: 1.4 }} rules={[{ required: true, message: '请输入主机' }]}>
              <Input />
            </Form.Item>
            <Form.Item label="端口" name="port" style={{ width: 110 }} rules={[{ required: true, message: '请输入端口' }]}>
              <InputNumber min={1} max={65535} style={{ width: '100%' }} />
            </Form.Item>
          </Space>
          <Space align="start" size="middle" style={{ display: 'flex' }}>
            <Form.Item label="默认数据库" name="defaultDatabase" style={{ flex: 1 }}>
              <Input />
            </Form.Item>
            <Form.Item label="用户名" name="username" style={{ flex: 1 }} rules={[{ required: true, message: '请输入用户名' }]}>
              <Input />
            </Form.Item>
          </Space>
          <Form.Item
            label="密码"
            name="password"
            extra={editingConnection?.hasPassword ? '留空则继续使用已加密保存的密码' : undefined}
            rules={[{ required: !editingConnection?.hasPassword, message: '请输入密码' }]}
          >
            <Input.Password placeholder={editingConnection?.hasPassword ? '已保存，留空不修改' : undefined} />
          </Form.Item>
          <Space size="large">
            <Form.Item label="保存加密密码" name="savePassword" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item label="启用 SSL" name="ssl" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item label="超时（秒）" name="timeoutSeconds">
              <InputNumber min={3} max={120} />
            </Form.Item>
          </Space>
        </Form>
      </Modal>
    </>
  );
}

function formatTaskStatus(status: DBExportTask['status']) {
  return {
    pending: '等待中',
    running: '导入中',
    done: '已完成',
    failed: '失败',
    cancelled: '已取消',
  }[status];
}

function formatCount(value: number) {
  return Number.isFinite(value) ? Math.max(0, value).toLocaleString('zh-CN') : '0';
}

function formatDuration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return '0秒';
  const rounded = Math.round(seconds);
  const hours = Math.floor(rounded / 3600);
  const minutes = Math.floor((rounded % 3600) / 60);
  const remainder = rounded % 60;
  return [hours ? `${hours}小时` : '', minutes ? `${minutes}分` : '', `${remainder}秒`].filter(Boolean).join('');
}
