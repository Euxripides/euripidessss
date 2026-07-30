import { Badge, Tag, Tooltip } from 'antd';
import type { DataSourceStatus } from '../types/datasource';

const states: Record<DataSourceStatus, { label: string; color: string; badge: 'success' | 'processing' | 'warning' | 'error' | 'default' }> = {
  HEALTHY: { label: '健康', color: 'success', badge: 'success' },
  DEGRADED: { label: '降级', color: 'warning', badge: 'warning' },
  RATE_LIMITED: { label: '限流', color: 'orange', badge: 'warning' },
  UNAVAILABLE: { label: '不可用', color: 'error', badge: 'error' },
  MISCONFIGURED: { label: '配置异常', color: 'magenta', badge: 'error' },
  DISABLED: { label: '已停用', color: 'default', badge: 'default' },
  UNKNOWN: { label: '待检测', color: 'blue', badge: 'processing' },
};

export function SourceStatus({ status, detail, compact = false }: { status: DataSourceStatus; detail?: string; compact?: boolean }) {
  const state = states[status] || states.UNKNOWN;
  const content = compact
    ? <Badge status={state.badge} text={state.label} />
    : <Tag color={state.color}><Badge status={state.badge} /> {state.label}</Tag>;
  return detail ? <Tooltip title={detail}>{content}</Tooltip> : content;
}
