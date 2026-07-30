import {
  CloudServerOutlined,
  DeleteOutlined,
  EditOutlined,
  FileZipOutlined,
  HistoryOutlined,
  PlayCircleOutlined,
  RadarChartOutlined,
} from '@ant-design/icons';
import { Button, Popconfirm, Space, Tag, Tooltip } from 'antd';
import type { DataSourceItem } from '../types/datasource';
import { HealthChart } from './HealthChart';
import { SourceStatus } from './SourceStatus';

const chainLabels: Record<string, string> = { bsc: 'BSC', eth: 'ETH', base: 'Base', arbitrum: 'Arbitrum' };

export function SourceCard({ source, wide, testing, onEdit, onTest, onLogs, onDelete }: {
  source: DataSourceItem;
  wide: boolean;
  testing: boolean;
  onEdit: () => void;
  onTest: () => void;
  onLogs: () => void;
  onDelete: () => void;
}) {
  const icon = source.type === 'STREAM'
    ? <RadarChartOutlined />
    : source.type === 'DATASET' ? <FileZipOutlined /> : <CloudServerOutlined />;
  return (
    <article className={`datasource-card ${wide ? 'datasource-card-wide' : ''}`}>
      <header>
        <span className={`datasource-source-icon datasource-source-${source.type.toLowerCase()}`}>{icon}</span>
        <div>
          <h3>{source.name}</h3>
          <small>{source.description}</small>
        </div>
        <SourceStatus status={source.status} detail={source.last_error} />
      </header>

      <div className="datasource-card-endpoint">
        <Tooltip title={source.endpoint_masked}><code>{source.endpoint_masked || '由 RPC 节点管理器托管'}</code></Tooltip>
        {source.secret_configured ? <Tag color="blue">密钥已加密</Tag> : null}
      </div>

      <div className="datasource-card-stats">
        <div>
          <span>支持链</span>
          <Space size={[4, 4]} wrap>{source.chain_keys.map((chain) => <Tag key={chain}>{chainLabels[chain] || chain}</Tag>)}</Space>
        </div>
        <div><span>P95 延迟</span><strong>{source.latency_p95_ms ? `${Math.round(source.latency_p95_ms)} ms` : '待检测'}</strong></div>
        <div><span>成功率</span><strong>{source.success_rate ? `${source.success_rate.toFixed(2)}%` : '待检测'}</strong></div>
      </div>

      <HealthChart score={source.health_score} successRate={source.success_rate} />

      <footer>
        <span className="datasource-card-time"><HistoryOutlined /> 最近成功：{formatTime(source.last_success_at)}</span>
        <div className="datasource-card-actions">
          <Button size="small" icon={<EditOutlined />} onClick={onEdit}>配置</Button>
          <Button size="small" type="primary" icon={<PlayCircleOutlined />} loading={testing} onClick={onTest}>测试</Button>
          <Button size="small" icon={<HistoryOutlined />} onClick={onLogs}>日志</Button>
          <Popconfirm title="确认删除该数据源？" description={source.type === 'RPC' ? '对应RPC节点配置也会被删除。' : '删除后可重新添加。'} onConfirm={onDelete}>
            <Button size="small" type="text" danger aria-label={`删除 ${source.name}`} icon={<DeleteOutlined />} />
          </Popconfirm>
        </div>
      </footer>
    </article>
  );
}

function formatTime(value?: string) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false });
}
