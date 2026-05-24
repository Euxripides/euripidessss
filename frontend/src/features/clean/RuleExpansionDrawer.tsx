import { UploadOutlined } from '@ant-design/icons';
import { Button, Descriptions, Drawer, Form, Select, Space, Table, Tag, Upload, message } from 'antd';
import type { UploadFile } from 'antd';
import { useEffect, useState } from 'react';
import type { RuleAnalysis } from '../../types';
import { analyzeRules, confirmRuleExpansion, loadCurrentFiles } from './cleanApi';
export function RuleExpansionDrawer({ open, onClose, initialAnalysis }: { open: boolean; onClose: () => void; initialAnalysis?: RuleAnalysis | null }) {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [analysis, setAnalysis] = useState<any>(null);
  const [selectedRule, setSelectedRule] = useState<any>(null);
  const [visibleFiles, setVisibleFiles] = useState<Array<{ name: string; path: string; size: number }>>([]);

  function refreshVisibleFiles() {
    loadCurrentFiles()
      .then(({ payload }) => setVisibleFiles([...(payload.uploads ?? []), ...(payload.rule_samples ?? [])]))
      .catch(() => setVisibleFiles([]));
  }

  useEffect(() => {
    if (open) refreshVisibleFiles();
  }, [open]);

  useEffect(() => {
    if (!initialAnalysis) return;
    setAnalysis(initialAnalysis);
    setSelectedRule(initialAnalysis.suggestions?.[0] ?? null);
    form.setFieldsValue({ provider: initialAnalysis.provider });
  }, [initialAnalysis, form]);

  async function analyze(values: { provider: string; sample_files?: UploadFile[]; max_rows?: number }) {
    const data = new FormData();
    data.append('provider', values.provider);
    data.append('max_rows', String(values.max_rows ?? 30));
    for (const file of values.sample_files ?? []) {
      data.append('sample_files', file.originFileObj as File);
    }
    setLoading(true);
    try {
      const { response, payload } = await analyzeRules(data);
      if (!response.ok) throw new Error(payload.detail || '规则分析失败');
      setAnalysis(payload);
      setSelectedRule(payload.suggestions?.[0] ?? null);
      refreshVisibleFiles();
      message.success('表头分析完成');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '规则分析失败');
    } finally {
      setLoading(false);
    }
  }

  async function confirm() {
    if (!analysis?.provider || !selectedRule) return;
    setLoading(true);
    try {
      const { response, payload } = await confirmRuleExpansion(analysis.provider, selectedRule);
      if (!response.ok) throw new Error(payload.detail || '保存规则失败');
      message.success('规则已扩充到规则库');
      onClose();
    } catch (error) {
      message.error(error instanceof Error ? error.message : '保存规则失败');
    } finally {
      setLoading(false);
    }
  }

  return (
    <Drawer title="规则扩充" width={760} open={open} onClose={onClose}>
      <Space direction="vertical" size="large" className="full">
        <Form form={form} layout="vertical" initialValues={{ provider: 'bank', max_rows: 30 }} onFinish={analyze}>
          <Form.Item label="数据类型" name="provider" rules={[{ required: true }]}>
            <Select
              options={[
                { label: '银行卡', value: 'bank' },
                { label: '微信', value: 'wechat' },
                { label: '支付宝', value: 'alipay' },
              ]}
            />
          </Form.Item>
          <Form.Item label="读取行数" name="max_rows">
            <Select
              options={[
                { label: '前 20 行', value: 20 },
                { label: '前 30 行', value: 30 },
              ]}
            />
          </Form.Item>
          <Form.Item label="样本文件或文件夹" name="sample_files" valuePropName="fileList" getValueFromEvent={(event) => event.fileList} rules={[{ required: true, message: '请上传样本文件' }]}>
            <Upload.Dragger multiple directory beforeUpload={() => false} accept=".xlsx,.xls,.xlsm,.csv,.tsv">
              <UploadOutlined />
              <p>上传新类型样本，支持 xlsx、xls、xlsm、csv、tsv；可直接选择文件夹</p>
            </Upload.Dragger>
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={loading}>
            提取表头并分析
          </Button>
        </Form>

        <Table
          size="small"
          rowKey="path"
          dataSource={visibleFiles}
          pagination={{ pageSize: 6 }}
          columns={[
            { title: '后端可见文件', dataIndex: 'path', ellipsis: true },
            { title: '大小', dataIndex: 'size', width: 110, render: (value) => `${Math.ceil(Number(value || 0) / 1024)} KB` },
          ]}
        />

        {analysis && (
          <>
            <Descriptions bordered size="small" column={2}>
              <Descriptions.Item label="类型">{analysis.provider_label}</Descriptions.Item>
              <Descriptions.Item label="候选表头">{analysis.candidates?.length ?? 0}</Descriptions.Item>
              <Descriptions.Item label="新类型建议">{analysis.suggestions?.length ?? 0}</Descriptions.Item>
              <Descriptions.Item label="模型状态">{selectedRule?.model_status ?? '-'}</Descriptions.Item>
            </Descriptions>
            <Table
              size="small"
              rowKey="signature"
              dataSource={(analysis.candidates ?? []) as Array<{ signature: string; columns?: string[] }>}
              columns={[
                { title: '文件', dataIndex: 'filename', ellipsis: true },
                { title: 'Sheet', dataIndex: 'sheet_name', width: 120 },
                { title: '表头行', dataIndex: 'header_row', width: 90 },
                { title: '已知匹配', dataIndex: 'known_match', width: 140 },
                { title: '分数', dataIndex: 'score', width: 80 },
                { title: '新类型', dataIndex: 'is_new_type', width: 90, render: (value) => (value ? <Tag color="orange">是</Tag> : <Tag color="blue">否</Tag>) },
              ]}
              expandable={{
                expandedRowRender: (record: { columns?: string[] }) => <pre className="mapping-preview">{record.columns?.join('\n')}</pre>,
              }}
            />
            {selectedRule && (
              <div>
                <h3>候选规则</h3>
                <pre className="mapping-preview">{JSON.stringify(selectedRule, null, 2)}</pre>
                <Button type="primary" loading={loading} onClick={confirm}>
                  确认并写入规则库
                </Button>
              </div>
            )}
          </>
        )}
      </Space>
    </Drawer>
  );
}
