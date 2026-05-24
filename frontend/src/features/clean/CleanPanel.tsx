import {
  BarChartOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  PlusOutlined,
  TagsOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import { Button, Form, Space, Table, Tag, Upload } from 'antd';
import type { ProcessResponse } from '../../types';
export function CleanPanel({
  loading,
  onFinish,
  result,
  onOpenRules,
  onDownload,
}: {
  loading: boolean;
  onFinish: (values: any) => void;
  result: ProcessResponse | null;
  onOpenRules: () => void;
  onDownload: (result: ProcessResponse) => void;
}) {
  const previewRows = (result?.preview ?? []).map((row, index) => ({ ...row, __row_id: index + 1 }));
  const previewColumns = [
    {
      title: '序号',
      dataIndex: '__row_id',
      key: '__row_id',
      width: 70,
      fixed: 'left' as const,
    },
    ...(result?.columns ?? []).map((column) => ({
      title: column,
      dataIndex: column,
      key: column,
      width: Math.max(120, Math.min(220, column.length * 16 + 48)),
      ellipsis: true,
      render: (value: unknown) => <span title={formatPreviewCell(value)}>{formatPreviewCell(value)}</span>,
    })),
  ];

  return (
    <section className="panel import-panel">
      <div className="panel-head">
        <div>
          <h2>数据清洗</h2>
          <p>上传流水、账户信息和标签表，系统只执行清洗、合并、校验和导出。</p>
        </div>
        {result && (
          <Button icon={<DownloadOutlined />} onClick={() => onDownload(result)}>
            下载清洗结果
          </Button>
        )}
        <Button icon={<PlusOutlined />} onClick={onOpenRules}>
          规则扩充
        </Button>
      </div>
      <Form layout="vertical" onFinish={onFinish}>
        <div className="upload-grid">
          <Form.Item label="流水文件" name="transaction_files" valuePropName="fileList" getValueFromEvent={(event) => event.fileList} rules={[{ required: true, message: '请上传至少一个流水文件' }]}>
            <Upload.Dragger multiple beforeUpload={() => false} accept=".xlsx,.xls,.xlsm,.csv,.tsv">
              <UploadOutlined />
              <p>拖入支付宝、微信、银行卡流水</p>
            </Upload.Dragger>
          </Form.Item>
          <Form.Item label="账户信息表" name="account_files" valuePropName="fileList" getValueFromEvent={(event) => event.fileList}>
            <Upload.Dragger multiple beforeUpload={() => false} accept=".xlsx,.xls,.xlsm,.csv,.tsv">
              <DatabaseOutlined />
              <p>可选，用于补全户名、证件号、开户行</p>
            </Upload.Dragger>
          </Form.Item>
          <Form.Item label="标签表" name="label_file" valuePropName="fileList" getValueFromEvent={(event) => event.fileList}>
            <Upload.Dragger maxCount={1} beforeUpload={() => false} accept=".xlsx,.xls,.xlsm,.csv,.tsv">
              <TagsOutlined />
              <p>可选，字段建议包含卡号、标签</p>
            </Upload.Dragger>
          </Form.Item>
        </div>
        <Button type="primary" htmlType="submit" loading={loading} icon={<BarChartOutlined />}>
          开始清洗
        </Button>
      </Form>
      {result && (
        <div className="clean-preview">
          <div className="preview-head">
            <div>
              <h3>清洗结果预览</h3>
              <p>展示导出文件中的前 {previewRows.length} 行，完整数据请下载 Excel。</p>
            </div>
            <Space size="small" wrap>
              <Tag color="blue">{result.rows.toLocaleString('zh-CN')} 行</Tag>
              <Tag color="geekblue">{result.columns.length} 列</Tag>
              <Tag color="green">有效输出 {result.report.rows_out.toLocaleString('zh-CN')} 行</Tag>
              {!!result.report.removed_duplicates && <Tag color="orange">去重 {result.report.removed_duplicates.toLocaleString('zh-CN')} 行</Tag>}
            </Space>
          </div>
          <Table
            size="small"
            bordered
            rowKey="__row_id"
            dataSource={previewRows}
            columns={previewColumns}
            scroll={{ x: Math.max(960, previewColumns.reduce((sum, column) => sum + Number(column.width ?? 120), 0)), y: 420 }}
            pagination={{ pageSize: 20, showSizeChanger: true, pageSizeOptions: [10, 20, 50, 100] }}
          />
        </div>
      )}
    </section>
  );
}

function formatPreviewCell(value: unknown) {
  if (value === null || value === undefined || value === '') return '';
  if (typeof value === 'number') return Number.isFinite(value) ? value.toLocaleString('zh-CN') : '';
  if (typeof value === 'boolean') return value ? '是' : '否';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}
