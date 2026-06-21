import { DownloadOutlined, LoginOutlined, PlayCircleOutlined, SettingOutlined } from '@ant-design/icons';
import { Button, Checkbox, Collapse, Form, Input, InputNumber, Select, Space, Tag, message } from 'antd';
import type { TablePaginationConfig } from 'antd/es/table';
import { useEffect, useState } from 'react';
import { DuneAuthModal, type DuneAuthFormValues } from './DuneAuthModal';
import { DuneResultTable } from './DuneResultTable';
import {
  DuneAuthRequiredError,
  exportDuneExcel,
  loadDuneAuthStatus,
  loadDunePage,
  queryDuneSQL,
  saveDuneAuth,
  type DuneAuthStatus,
  type DuneQueryResponse,
  type DuneQueryValues,
} from './duneApi';

const { TextArea } = Input;

type DuneMode = 'api' | 'crawl';

export function DuneQueryPanel() {
  const [queryForm] = Form.useForm<DuneQueryValues>();
  const [authForm] = Form.useForm<DuneAuthFormValues>();
  const [auth, setAuth] = useState<DuneAuthStatus | null>(null);
  const [result, setResult] = useState<DuneQueryResponse | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(100);
  const [loading, setLoading] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [authOpen, setAuthOpen] = useState(false);
  const [mode, setMode] = useState<DuneMode>('api');

  useEffect(() => {
    void refreshAuthStatus();
  }, []);

  async function refreshAuthStatus() {
    try {
      setAuth(await loadDuneAuthStatus());
    } catch (error) {
      message.warning(error instanceof Error ? error.message : '读取 Dune 鉴权状态失败');
    }
  }

  async function submit(values: DuneQueryValues) {
    setLoading(true);
    try {
      const data = await queryDuneSQL({ ...values, webQuery: mode === 'crawl' });
      setResult(data);
      setPage(1);
      setPageSize(values.limit ?? 100);
      message.success(`查询完成，execution_id=${data.executionId}`);
      await refreshAuthStatus();
    } catch (error) {
      handleDuneError(error);
    } finally {
      setLoading(false);
    }
  }

  async function changePage(next: TablePaginationConfig) {
    if (!result?.executionId || !next.current || !next.pageSize) return;
    setLoading(true);
    try {
      const data = await loadDunePage({
        executionId: result.executionId,
        queryId: result.queryId || queryForm.getFieldValue('queryId') || 0,
        offset: (next.current - 1) * next.pageSize,
        limit: next.pageSize,
        allowPartialResults: queryForm.getFieldValue('allowPartialResults') ?? true,
      });
      setResult(data);
      setPage(next.current);
      setPageSize(next.pageSize);
    } catch (error) {
      handleDuneError(error);
    } finally {
      setLoading(false);
    }
  }

  async function exportExcel(scope: 'page' | 'all') {
    if (!result?.executionId) return;
    setExporting(true);
    try {
      const filename = await exportDuneExcel({
        executionId: result.executionId,
        queryId: result.queryId || queryForm.getFieldValue('queryId') || 0,
        scope,
        offset: scope === 'page' ? (page - 1) * pageSize : 0,
        limit: pageSize,
        allowPartialResults: queryForm.getFieldValue('allowPartialResults') ?? true,
      });
      message.success(`已导出 ${filename}`);
    } catch (error) {
      handleDuneError(error);
    } finally {
      setExporting(false);
    }
  }

  async function saveAuth(values: DuneAuthFormValues) {
    try {
      await saveDuneAuth(values);
      message.success('Dune Key/Cookie 已保存到本机后端');
      setAuthOpen(false);
      await refreshAuthStatus();
    } catch (error) {
      message.error(error instanceof Error ? error.message : '保存失败');
    }
  }

  function handleDuneError(error: unknown) {
    if (error instanceof DuneAuthRequiredError) {
      setAuthOpen(true);
      message.warning(error.message);
      return;
    }
    message.error(error instanceof Error ? error.message : 'Dune 请求失败');
  }

  return (
    <div className="download-shell dune-shell">
      <div className="panel-head">
        <div>
          <h2>Dune 数据查询</h2>
          <p>输入 SQL 后查询，结果在下方分页预览，并可导出当前页或全量 Excel。</p>
        </div>
        <Space>
          <Tag color={auth?.hasApiKey ? 'green' : 'orange'}>{auth?.hasApiKey ? `Key: ${auth.source}` : '未保存 Key'}</Tag>
          <Tag color={auth?.hasCookie ? 'green' : 'default'}>{auth?.hasCookie ? 'Cookie: 已保存' : 'Cookie: 未保存'}</Tag>
          <Tag color={auth?.hasWebAuth ? 'green' : 'default'}>{auth?.hasWebAuth ? '官网Token: 已保存' : '官网Token: 未保存'}</Tag>
          <Button icon={<LoginOutlined />} onClick={() => setAuthOpen(true)}>
            Key / Cookie / Token
          </Button>
        </Space>
      </div>

      <Form
        form={queryForm}
        layout="vertical"
        initialValues={{ performance: 'medium', timeoutSeconds: 900, pollIntervalSeconds: 2, allowPartialResults: true, limit: 100 }}
        onFinish={submit}
      >
        <Form.Item name="sql" label="SQL" rules={[{ required: true, message: '请输入 Dune SQL' }]}>
          <TextArea className="dune-sql-input" autoSize={{ minRows: 8, maxRows: 18 }} placeholder="SELECT * FROM dex.trades LIMIT 100" />
        </Form.Item>

        <Collapse
          ghost
          size="small"
          items={[{
            key: 'settings',
            label: <span><SettingOutlined /> 设置</span>,
            extra: (
              <Select
                size="small"
                value={mode}
                onChange={(v) => setMode(v)}
                style={{ width: 180 }}
                onClick={(e) => e.stopPropagation()}
                options={[
                  { value: 'api', label: 'API（需 Key）' },
                  { value: 'crawl', label: '爬取（需 Cookie）' },
                ]}
              />
            ),
            children: (
              <>
                <div className="download-grid dune-control-grid">
                  <Form.Item name="limit" label="每页行数">
                    <InputNumber min={10} max={1000} className="full" />
                  </Form.Item>
                  {mode === 'api' && (
                    <Form.Item name="performance" label="执行规格">
                      <Select options={[
                        { value: 'free', label: 'free' },
                        { value: 'small', label: 'small' },
                        { value: 'medium', label: 'medium' },
                        { value: 'large', label: 'large' },
                      ]} />
                    </Form.Item>
                  )}
                  {mode === 'crawl' && (
                    <>
                      <Form.Item name="queryId" label="query_id（复用已有查询）">
                        <InputNumber min={1} className="full" placeholder="填了就不自动创建" />
                      </Form.Item>
                      <Form.Item name="datasetId" label="dataset_id（可选覆盖）">
                        <InputNumber min={1} className="full" placeholder="默认 11" />
                      </Form.Item>
                    </>
                  )}
                  <Form.Item name="timeoutSeconds" label="超时秒数">
                    <InputNumber min={30} max={7200} className="full" />
                  </Form.Item>
                  <Form.Item name="pollIntervalSeconds" label="轮询间隔秒数">
                    <InputNumber min={1} max={30} className="full" />
                  </Form.Item>
                </div>
                <Form.Item name="allowPartialResults" valuePropName="checked">
                  <Checkbox>允许 partial result</Checkbox>
                </Form.Item>
              </>
            ),
          }]}
        />

        <div className="dune-action-row">
          <Space wrap>
            <Button type="primary" htmlType="submit" icon={<PlayCircleOutlined />} loading={loading}>
              查询
            </Button>
            <Button icon={<DownloadOutlined />} disabled={!result} loading={exporting} onClick={() => void exportExcel('page')}>
              下载当前页 Excel
            </Button>
            <Button icon={<DownloadOutlined />} disabled={!result} loading={exporting} onClick={() => void exportExcel('all')}>
              下载全量 Excel
            </Button>
          </Space>
        </div>
      </Form>

      {result && <DuneResultTable result={result} page={page} pageSize={pageSize} loading={loading} onChange={(pagination) => void changePage(pagination)} />}

      <DuneAuthModal open={authOpen} form={authForm} onClose={() => setAuthOpen(false)} onSave={saveAuth} authStatus={auth} />
    </div>
  );
}
