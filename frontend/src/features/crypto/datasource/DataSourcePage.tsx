import {
  ApiOutlined,
  CheckCircleOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  ExclamationCircleOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { Button, Drawer, Empty, Input, Segmented, Space, Spin, Tag, Tooltip, message } from 'antd';
import { startTransition, useCallback, useEffect, useMemo, useState } from 'react';
import { dataSourceApi } from './api/datasource-api';
import { MetricsCard } from './components/MetricsCard';
import { SourceCard } from './components/SourceCard';
import { SourceConfigDialog } from './components/SourceConfigDialog';
import { SourceStatus } from './components/SourceStatus';
import { TestConnectionModal } from './components/TestConnectionModal';
import type {
  DataSourceConfig,
  DataSourceEvent,
  DataSourceItem,
  DataSourceSnapshot,
  DataSourceTestResult,
  DataSourceType,
} from './types/datasource';
import './datasource.css';

const EMPTY: DataSourceSnapshot = {
  overview: { source_count: 0, healthy_count: 0, abnormal_count: 0, today_requests: 0, cache_hit_rate: 0 },
  sources: [],
  events: [],
};

export function DataSourcePage({ onOpenRpc }: { onOpenRpc: () => void }) {
  const [snapshot, setSnapshot] = useState(EMPTY);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [filter, setFilter] = useState<'ALL' | DataSourceType>('RPC');
  const [query, setQuery] = useState('');
  const [configOpen, setConfigOpen] = useState(false);
  const [editing, setEditing] = useState<DataSourceConfig | null>(null);
  const [testingSource, setTestingSource] = useState<DataSourceItem | null>(null);
  const [testResult, setTestResult] = useState<DataSourceTestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [logSource, setLogSource] = useState<DataSourceItem | null>(null);

  const refresh = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true);
    try {
      const next = await dataSourceApi.snapshot();
      startTransition(() => setSnapshot(next));
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取数据源失败');
    } finally {
      if (!quiet) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(true), 30_000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  const visibleSources = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return snapshot.sources.filter((source) =>
      (filter === 'ALL' || source.type === filter)
      && (!normalized || `${source.name} ${source.provider} ${source.chain_keys.join(' ')}`.toLowerCase().includes(normalized)),
    );
  }, [filter, query, snapshot.sources]);

  const sourceCounts = useMemo(() => snapshot.sources.reduce<Record<'ALL' | DataSourceType, number>>(
    (counts, source) => {
      counts.ALL += 1;
      counts[source.type] += 1;
      return counts;
    },
    { ALL: 0, STREAM: 0, DATASET: 0, RPC: 0 },
  ), [snapshot.sources]);

  const metrics = [
    { label: '数据源数量', value: snapshot.overview.source_count, suffix: '个', icon: <DatabaseOutlined />, tone: 'blue', help: 'SQD、AWS及各RPC Endpoint总数' },
    { label: '健康数据源', value: snapshot.overview.healthy_count, suffix: '个', icon: <CheckCircleOutlined />, tone: 'green', help: '最近一次健康检查正常的数据源' },
    { label: '异常数据源', value: snapshot.overview.abnormal_count, suffix: '个', icon: <ExclamationCircleOutlined />, tone: 'orange', help: '降级、限流、配置异常或不可用的数据源' },
    { label: '今日请求', value: snapshot.overview.today_requests.toLocaleString(), suffix: '次', icon: <ThunderboltOutlined />, tone: 'indigo', help: '统一管理器记录的当日探测与RPC请求' },
    { label: '缓存命中率', value: snapshot.overview.cache_hit_rate.toFixed(1), suffix: '%', icon: <ApiOutlined />, tone: 'cyan', help: 'RPC地址与Token富化缓存命中比例' },
  ];

  const editSource = async (source: DataSourceItem) => {
    if (source.type === 'RPC') {
      onOpenRpc();
      return;
    }
    try {
      setEditing(await dataSourceApi.config(source.source_id));
      setConfigOpen(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取配置失败');
    }
  };

  const saveSource = async (value: DataSourceConfig) => {
    setSaving(true);
    try {
      await dataSourceApi.save(value);
      message.success('数据源连接测试通过，配置已安全保存');
      setConfigOpen(false);
      await refresh(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '保存数据源失败');
    } finally {
      setSaving(false);
    }
  };

  const testSource = async (source: DataSourceItem) => {
    setTestingSource(source);
    setTestResult(null);
    setTesting(true);
    try {
      const result = await dataSourceApi.test(source.source_id);
      setTestResult(result);
      await refresh(true);
    } catch (error) {
      setTestResult({
        success: false, source_id: source.source_id, status: 'UNAVAILABLE',
        latency_ms: 0, checked_at: new Date().toISOString(),
        message: error instanceof Error ? error.message : '连接测试失败',
      });
      await refresh(true);
    } finally {
      setTesting(false);
    }
  };

  const removeSource = async (source: DataSourceItem) => {
    try {
      await dataSourceApi.remove(source.source_id);
      message.success(`${source.name} 已删除`);
      await refresh(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '删除数据源失败');
    }
  };

  const events = logSource
    ? snapshot.events.filter((event) => event.source_id === logSource.source_id)
    : snapshot.events;

  return (
    <div className="datasource-page">
      <header className="datasource-hero">
        <div>
          <h1>数据源管理中心</h1>
        </div>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={() => void refresh()}>刷新</Button>
          <Button icon={<CloudServerOutlined />} onClick={onOpenRpc}>新增RPC节点</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditing(null); setConfigOpen(true); }}>添加数据源</Button>
        </Space>
      </header>

      <div className="datasource-metrics">
        {metrics.map((metric) => <MetricsCard key={metric.label} {...metric} />)}
      </div>

      <div className="datasource-toolbar">
        <Segmented
          value={filter}
          onChange={(value) => setFilter(value as typeof filter)}
          options={[
            { value: 'RPC', label: `RPC (${sourceCounts.RPC})` },
            { value: 'STREAM', label: `SQD (${sourceCounts.STREAM})` },
            { value: 'DATASET', label: `AWS (${sourceCounts.DATASET})` },
            { value: 'ALL', label: `全部 (${sourceCounts.ALL})` },
          ]}
        />
        <Input allowClear prefix={<SearchOutlined />} placeholder="搜索名称、Provider或链" value={query} onChange={(event) => setQuery(event.target.value)} />
      </div>

      <Spin spinning={loading}>
        {visibleSources.length ? (
          <section className="datasource-grid">
            {visibleSources.map((source) => (
              <SourceCard
                key={source.source_id}
                source={source}
                wide={filter !== 'ALL' && source.type !== 'RPC'}
                testing={testingSource?.source_id === source.source_id && testing}
                onEdit={() => void editSource(source)}
                onTest={() => void testSource(source)}
                onLogs={() => setLogSource(source)}
                onDelete={() => void removeSource(source)}
              />
            ))}
          </section>
        ) : <Empty className="datasource-empty" description="没有符合条件的数据源" />}
      </Spin>

      <section className="datasource-events">
        <div className="datasource-section-title">
          <div><h2>最近健康事件</h2></div>
          <Tag>{snapshot.events.length} 条</Tag>
        </div>
        {snapshot.events.length ? snapshot.events.slice(0, 6).map((event) => <EventRow key={`${event.source_id}-${event.occurred_at}`} event={event} />)
          : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="健康检查完成后将在这里显示事件" />}
      </section>

      <SourceConfigDialog
        open={configOpen}
        initial={editing}
        saving={saving}
        onCancel={() => setConfigOpen(false)}
        onSave={saveSource}
      />
      <TestConnectionModal
        source={testingSource}
        result={testResult}
        open={Boolean(testingSource)}
        testing={testing}
        onClose={() => setTestingSource(null)}
      />
      <Drawer rootClassName="datasource-log-drawer" title={`${logSource?.name || ''} · 健康日志`} open={Boolean(logSource)} onClose={() => setLogSource(null)} width="min(520px, 92vw)">
        {events.length ? events.map((event) => <EventRow key={`${event.source_id}-${event.occurred_at}`} event={event} />)
          : <Empty description="该数据源暂无健康事件" />}
      </Drawer>
    </div>
  );
}

function EventRow({ event }: { event: DataSourceEvent }) {
  return (
    <div className="datasource-event-row">
      <SourceStatus status={event.status} compact />
      <span><strong>{event.source_name}</strong><Tooltip title={event.message} placement="topLeft"><small>{event.message}</small></Tooltip></span>
      <time>{new Date(event.occurred_at).toLocaleString('zh-CN', { hour12: false })}</time>
    </div>
  );
}
