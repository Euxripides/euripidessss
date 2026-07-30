import { Alert, Col, Form, Input, InputNumber, Modal, Row, Select, Switch } from 'antd';
import { useEffect } from 'react';
import type { DataSourceConfig } from '../types/datasource';

const defaults: DataSourceConfig = {
  type: 'STREAM', name: '', endpoint: '', api_key: '', timeout_ms: 8000,
  max_concurrency: 4, retry_count: 2, enabled: true,
};

export function SourceConfigDialog({ open, initial, saving, onCancel, onSave }: {
  open: boolean;
  initial: DataSourceConfig | null;
  saving: boolean;
  onCancel: () => void;
  onSave: (value: DataSourceConfig) => Promise<void>;
}) {
  const [form] = Form.useForm<DataSourceConfig>();
  const type = Form.useWatch('type', form);
  useEffect(() => {
    if (!open) return;
    form.resetFields();
    form.setFieldsValue(initial || defaults);
  }, [form, initial, open]);
  return (
    <Modal
      title={initial?.source_id ? '修改数据源配置' : '添加数据源'}
      open={open}
      width={680}
      okText="测试并保存"
      confirmLoading={saving}
      onCancel={onCancel}
      onOk={() => void form.validateFields().then(onSave)}
      destroyOnHidden
    >
      <Alert className="datasource-config-alert" type="info" showIcon message="API Key仅提交到后端加密保存，页面不会回显明文。" />
      <Form form={form} layout="vertical" initialValues={defaults}>
        <Form.Item name="source_id" hidden><Input /></Form.Item>
        <Row gutter={16}>
          <Col span={12}>
            <Form.Item name="type" label="数据源类型" rules={[{ required: true }]}>
              <Select disabled={Boolean(initial?.source_id)} options={[
                { value: 'STREAM', label: 'SQD Finalized Stream' },
                { value: 'DATASET', label: 'AWS Public Dataset' },
              ]} />
            </Form.Item>
          </Col>
          <Col span={12}><Form.Item name="name" label="名称" rules={[{ required: true }]}><Input placeholder="数据源显示名称" /></Form.Item></Col>
        </Row>
        <Form.Item name="endpoint" label="Endpoint" rules={[{ required: true }, { type: 'url', message: '请输入完整Endpoint URL' }]}><Input placeholder="https://..." /></Form.Item>
        <Form.Item name="api_key" label="API Key（可选）" extra={initial?.source_id ? '留空表示保持原密钥。' : undefined}><Input.Password autoComplete="new-password" /></Form.Item>
        {type === 'DATASET' ? (
          <>
            <Row gutter={16}>
              <Col span={12}><Form.Item name="bucket" label="Bucket" rules={[{ required: true }]}><Input /></Form.Item></Col>
              <Col span={12}><Form.Item name="region" label="Region" rules={[{ required: true }]}><Input /></Form.Item></Col>
            </Row>
            <Form.Item name="prefix" label="Prefix" rules={[{ required: true }]}><Input /></Form.Item>
            <Form.Item name="cache_directory" label="缓存目录"><Input placeholder="E:\\codex\\bsc_analytics\\cache" /></Form.Item>
          </>
        ) : null}
        <Row gutter={16}>
          <Col span={8}><Form.Item name="timeout_ms" label="Timeout（ms）"><InputNumber min={1000} max={60000} step={1000} /></Form.Item></Col>
          <Col span={8}><Form.Item name="max_concurrency" label="最大并发"><InputNumber min={1} max={64} /></Form.Item></Col>
          <Col span={8}><Form.Item name="retry_count" label="Retry次数"><InputNumber min={0} max={5} /></Form.Item></Col>
        </Row>
        <Form.Item name="enabled" label="启用" valuePropName="checked"><Switch /></Form.Item>
      </Form>
    </Modal>
  );
}
