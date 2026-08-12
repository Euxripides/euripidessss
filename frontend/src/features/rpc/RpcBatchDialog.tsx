import { Alert, Col, Form, Input, InputNumber, Modal, Row, Segmented, Select, Space, Switch, Typography, message } from 'antd';
import { useEffect, useMemo, useState } from 'react';
import { rpcApi } from './rpcApi';
import type { RpcBatchFailure, RpcEndpointInput } from './rpcTypes';

const CHAINS = [
  { value: 'bsc', label: 'BSC' },
  { value: 'eth', label: 'Ethereum' },
  { value: 'base', label: 'Base' },
  { value: 'arbitrum', label: 'Arbitrum' },
];

interface BatchFormValue {
  provider: string;
  chain_key: string;
  endpoints: string;
  priority: number;
  enabled: boolean;
  max_rps: number;
  max_concurrency: number;
  request_timeout_ms: number;
}

type InputMode = 'API_KEY' | 'ENDPOINT';
const KEY_PROVIDERS = new Set(['ANKR', 'NODEREAL']);

export function endpointFromAPIKey(provider: string, chainKey: string, apiKey: string) {
  const normalizedProvider = provider.trim().toUpperCase();
  const normalizedChain = chainKey.trim().toLowerCase();
  const normalizedKey = apiKey.trim();
  if (!normalizedKey || /[\s|]/.test(normalizedKey) || normalizedKey.length > 512) {
    throw new Error('API 密钥不能为空、不能包含空白或竖线，且不能超过 512 个字符');
  }
  if (normalizedProvider === 'NODEREAL') {
    if (normalizedKey.length !== 32) throw new Error('NodeReal API 密钥应为 32 个字符');
    return `https://${normalizedChain}-mainnet.nodereal.io/v1/${encodeURIComponent(normalizedKey)}`;
  }
  if (normalizedProvider === 'ANKR') {
    return `https://rpc.ankr.com/${normalizedChain}/${encodeURIComponent(normalizedKey)}`;
  }
  throw new Error('该 Provider 无法仅凭 API 密钥推导 Endpoint，请选择完整 Endpoint');
}

export function RpcBatchDialog({ open, onClose, onCreated }: {
  open: boolean;
  onClose: () => void;
  onCreated: () => void | Promise<void>;
}) {
  const [form] = Form.useForm<BatchFormValue>();
  const [saving, setSaving] = useState(false);
  const [failures, setFailures] = useState<RpcBatchFailure[]>([]);
  const [inputMode, setInputMode] = useState<InputMode>('ENDPOINT');
  const [showSecrets, setShowSecrets] = useState(false);
  const provider = Form.useWatch('provider', form) || 'CHAINSTACK';
  const endpointText = Form.useWatch('endpoints', form) || '';
  const parsedCount = useMemo(() => endpointText.split(/\r?\n/).filter((line) => line.trim()).length, [endpointText]);

  useEffect(() => {
    if (open) {
      form.setFieldsValue({
        provider: 'CHAINSTACK', chain_key: 'bsc', endpoints: '', priority: 10,
        enabled: true, max_rps: 3, max_concurrency: 2, request_timeout_ms: 8000,
      });
      setFailures([]);
      setInputMode('ENDPOINT');
      setShowSecrets(false);
    }
  }, [form, open]);

  const submit = async () => {
    const values = await form.validateFields();
    const lines = values.endpoints.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
    if (lines.length > 50) {
      message.error('单次最多批量添加 50 个 RPC 账号');
      return;
    }
    let items: RpcEndpointInput[];
    try {
      items = lines.map((line, index) => {
        const separator = line.indexOf('|');
        return {
          provider: values.provider,
          chain_key: values.chain_key,
          display_name: separator >= 0 ? line.slice(0, separator).trim() : `${values.provider}-${values.chain_key.toUpperCase()}-${index + 1}`,
          endpoint_url: inputMode === 'API_KEY'
            ? endpointFromAPIKey(values.provider, values.chain_key, separator >= 0 ? line.slice(separator + 1) : line)
            : (separator >= 0 ? line.slice(separator + 1).trim() : line),
          priority: values.priority + index,
          enabled: values.enabled,
          max_rps: values.max_rps,
          max_concurrency: values.max_concurrency,
          request_timeout_ms: values.request_timeout_ms,
        };
      });
    } catch (error) {
      message.error(error instanceof Error ? error.message : 'API 密钥格式错误');
      return;
    }
    setSaving(true);
    setFailures([]);
    try {
      const result = await rpcApi.createBatch(items);
      const resultFailures = Array.isArray(result.failures) ? result.failures : [];
      setFailures(resultFailures);
      if (result.created_count) {
        message.success(`已安全保存 ${result.created_count} 个 RPC 账号`);
        await onCreated();
      }
      if (!result.failure_count) onClose();
      else {
        form.setFieldValue('endpoints', resultFailures.map((failure) => lines[failure.index]).join('\n'));
        message.warning(`${result.failure_count} 条未添加；已从输入框移除成功项，可修正后重试`);
      }
    } catch (error) {
      message.error(error instanceof Error ? error.message : '批量添加 RPC 失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title="批量添加 RPC API 账号"
      open={open}
      onCancel={onClose}
      onOk={() => void submit()}
      okText={`校验并添加${parsedCount ? `（${parsedCount}）` : ''}`}
      confirmLoading={saving}
      width={760}
      destroyOnHidden
    >
      <Form form={form} layout="vertical">
        <Row gutter={16}>
          <Col span={12}><Form.Item name="provider" label="RPC 类型 / Provider" rules={[{ required: true }]}><Select onChange={(value) => { setInputMode(KEY_PROVIDERS.has(value) ? 'API_KEY' : 'ENDPOINT'); setFailures([]); }} options={['CHAINSTACK', 'ANKR', 'NODEREAL', 'CUSTOM'].map((value) => ({ value, label: value }))} /></Form.Item></Col>
          <Col span={12}><Form.Item name="chain_key" label="链" rules={[{ required: true }]}><Select options={CHAINS} /></Form.Item></Col>
        </Row>
        <Form.Item label="输入方式">
          <Space wrap>
            <Segmented
              value={inputMode}
              onChange={(value) => { setInputMode(value as InputMode); setFailures([]); }}
              options={[
                { value: 'API_KEY', label: '直接填写 API 密钥', disabled: !KEY_PROVIDERS.has(provider) },
                { value: 'ENDPOINT', label: '填写完整 Endpoint' },
              ]}
            />
            {inputMode === 'API_KEY' ? <span className="rpc-secret-visibility"><Switch size="small" checked={showSecrets} onChange={setShowSecrets} /> 显示密钥</span> : null}
          </Space>
        </Form.Item>
        <Form.Item
          name="endpoints"
          label={`${inputMode === 'API_KEY' ? 'API 密钥' : 'API Endpoint'}（每行一个，最多 50 个）`}
          extra={inputMode === 'API_KEY'
            ? `格式：账号名称 | API 密钥，也可只填密钥。系统将按 ${provider} 和所选链生成 HTTPS Endpoint；密钥仅用于本次加密保存，不会在页面或响应中返回。`
            : '格式：账号名称 | 完整 HTTPS Endpoint。也可只填 URL，系统会自动命名。完整 URL 仅用于本次加密保存，不会在页面或响应中明文返回。'}
          rules={[{ required: true, message: '请至少输入一个 API 账号' }]}
        >
          <Input.TextArea
            autoComplete="off"
            className={inputMode === 'API_KEY' && !showSecrets ? 'rpc-secret-textarea' : undefined}
            rows={9}
            placeholder={inputMode === 'API_KEY' ? '主账号 | API_KEY_1\n备用账号 | API_KEY_2' : '主账号 | https://provider.example/v1/API_KEY_1\n备用账号 | https://provider.example/v1/API_KEY_2'}
          />
        </Form.Item>
        <Row gutter={12}>
          <Col span={6}><Form.Item name="priority" label="起始优先级"><InputNumber min={1} max={949} /></Form.Item></Col>
          <Col span={6}><Form.Item name="max_rps" label="每账号最大 RPS"><InputNumber min={0.25} max={100} step={0.25} /></Form.Item></Col>
          <Col span={6}><Form.Item name="max_concurrency" label="每账号最大并发"><InputNumber min={1} max={32} /></Form.Item></Col>
          <Col span={6}><Form.Item name="request_timeout_ms" label="超时（ms）"><InputNumber min={1000} max={30000} step={500} /></Form.Item></Col>
        </Row>
        <Form.Item name="enabled" label="添加后加入自动路由" valuePropName="checked"><Switch /></Form.Item>
      </Form>
      {failures.length ? (
        <Alert
          type="error"
          showIcon
          message={`${failures.length} 条添加失败`}
          description={failures.map((failure) => <Typography.Paragraph key={failure.index} style={{ marginBottom: 4 }}>第 {failure.index + 1} 行 {failure.display_name || '未命名'}：{failure.detail}</Typography.Paragraph>)}
        />
      ) : null}
    </Modal>
  );
}
