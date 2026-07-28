import { useState } from 'react';
import {
  BarChartOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  PlusOutlined,
  TagsOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import { Button, Checkbox, Form, Progress, Space, Table, Tag, Upload } from 'antd';
import type { ProcessArtifact, ProcessProgress, ProcessResponse } from '../../types';
import { DBExportModal } from './DBExportModal';
export function CleanPanel({
  loading,
  onFinish,
  result,
  onOpenRules,
  onDownload,
  progress,
  onDownloadArtifact,
}: {
  loading: boolean;
  onFinish: (values: any) => void;
  result: ProcessResponse | null;
  onOpenRules: () => void;
  onDownload: (result: ProcessResponse) => void;
  progress: ProcessProgress | null;
  onDownloadArtifact: (artifact: ProcessArtifact) => void;
}) {
  const [dbExportOpen, setDBExportOpen] = useState(false);
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
          <>
            {result.merge_mode === 'unified' && (
              <Button type="primary" icon={<DatabaseOutlined />} onClick={() => setDBExportOpen(true)}>
                一键导入数据库
              </Button>
            )}
            <Button icon={<DownloadOutlined />} onClick={() => onDownload(result)}>
              下载清洗结果
            </Button>
          </>
        )}
        <Button icon={<PlusOutlined />} onClick={onOpenRules}>
          规则扩充
        </Button>
      </div>
      <Form layout="vertical" initialValues={{ unify_sources: true }} onFinish={onFinish}>
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
        <Form.Item
          name="unify_sources"
          valuePropName="checked"
          extra="所有模式都会先保留源文件，并按支付宝、微信、银行分别生成原字段大CSV。勾选后继续生成各类统一字段CSV，再清洗、去重并跨来源合并；所有阶段产物均保留。"
        >
          <Checkbox>统一字段名后合并不同来源</Checkbox>
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={loading} icon={<BarChartOutlined />}>
          开始清洗
        </Button>
      </Form>
      {progress && (
        <div className="clean-progress-panel">
          <div className="clean-progress-title">
            <div>
              <h3>处理进度</h3>
              <p>
                任务 {progress.job_id} · 总用时 {formatDuration(progress.elapsed_seconds)}
                {progress.status === 'failed' && progress.error ? ` · ${progress.error}` : ''}
              </p>
            </div>
            <Tag color={progress.status === 'done' ? 'success' : progress.status === 'failed' ? 'error' : 'processing'}>
              {progress.status === 'done' ? '已完成' : progress.status === 'failed' ? '失败' : '处理中'}
            </Tag>
          </div>
          <div className="clean-progress-grid">
            {progress.stages.map((stage) => (
              <div className={`clean-progress-stage is-${stage.status}`} key={stage.id}>
                <div className="clean-progress-stage-head">
                  <strong>{stage.name}</strong>
                  <span>{stage.message || formatStageStatus(stage.status)}</span>
                </div>
                <Progress
                  percent={Math.max(0, Math.min(100, Number(stage.percent.toFixed(1))))}
                  status={stage.status === 'failed' ? 'exception' : stage.status === 'done' ? 'success' : 'active'}
                  strokeColor={stage.status === 'pending' ? '#cbd5e1' : undefined}
                />
                <div className="clean-progress-metrics">
                  <span>{formatCount(stage.current)} / {formatCount(stage.total)} {stage.unit}</span>
                  <span>速度 {formatSpeed(stage.speed, stage.unit)}</span>
                  <span>已用 {formatDuration(stage.elapsed_seconds)}</span>
                  <span>剩余 {stage.status === 'pending' ? '--' : formatDuration(stage.eta_seconds)}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
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
              <Tag color={result.merge_mode === 'unified' ? 'green' : 'purple'}>
                {result.merge_mode === 'unified' ? '统一字段合并' : '按来源分 Sheet'}
              </Tag>
              <Tag color="green">有效输出 {result.report.rows_out.toLocaleString('zh-CN')} 行</Tag>
              {!!result.report.removed_duplicates && <Tag color="orange">去重 {result.report.removed_duplicates.toLocaleString('zh-CN')} 行</Tag>}
            </Space>
          </div>
          {result.merge_mode === 'separate' && !!result.source_sheets?.length && (
            <Space size="small" wrap className="source-sheet-summary">
              {result.source_sheets.map((sheet) => (
                <Tag key={sheet.sheet}>
                  {sheet.sheet}：{sheet.rows.toLocaleString('zh-CN')} 行 / {sheet.columns} 列
                </Tag>
              ))}
            </Space>
          )}
          <Table
            size="small"
            bordered
            rowKey="__row_id"
            dataSource={previewRows}
            columns={previewColumns}
            scroll={{ x: Math.max(960, previewColumns.reduce((sum, column) => sum + Number(column.width ?? 120), 0)), y: 420 }}
            pagination={{ pageSize: 20, showSizeChanger: true, pageSizeOptions: [10, 20, 50, 100] }}
          />
          {!!result.artifacts?.length && (
            <div className="clean-artifacts">
              <div className="preview-head">
                <div>
                  <h3>阶段产物</h3>
                  <p>源文件、分类原字段CSV、分类统一字段CSV和最终合并结果均可独立下载审计。</p>
                </div>
              </div>
              <Table
                size="small"
                rowKey="id"
                dataSource={result.artifacts}
                pagination={{ pageSize: 8, showSizeChanger: false }}
                columns={[
                  { title: '阶段', dataIndex: 'stage', width: 150 },
                  { title: '来源', dataIndex: 'provider', width: 90, render: (value: string) => value || '-' },
                  { title: '文件', dataIndex: 'name', ellipsis: true },
                  { title: '行数', dataIndex: 'rows', width: 120, render: (value: number) => value ? value.toLocaleString('zh-CN') : '-' },
                  { title: '大小', dataIndex: 'size', width: 110, render: (value: number) => formatBytes(value) },
                  {
                    title: '操作',
                    key: 'action',
                    width: 100,
                    render: (_: unknown, artifact: ProcessArtifact) => (
                      <Button size="small" icon={<DownloadOutlined />} onClick={() => onDownloadArtifact(artifact)}>下载</Button>
                    ),
                  },
                ]}
              />
            </div>
          )}
        </div>
      )}
      {result?.merge_mode === 'unified' && (
        <DBExportModal
          open={dbExportOpen}
          jobId={result.job_id}
          rowCount={result.rows}
          onClose={() => setDBExportOpen(false)}
        />
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

function formatSpeed(speed: number, unit: string) {
  if (!Number.isFinite(speed) || speed <= 0) return `0 ${unit}/秒`;
  return `${Math.round(speed).toLocaleString('zh-CN')} ${unit}/秒`;
}

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const index = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function formatStageStatus(status: 'pending' | 'running' | 'done' | 'failed') {
  return { pending: '等待中', running: '处理中', done: '已完成', failed: '失败' }[status];
}
