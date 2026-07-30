import type { ReactNode } from 'react';
import { Tooltip } from 'antd';
import { InfoCircleOutlined } from '@ant-design/icons';

export function MetricsCard({ label, value, suffix, icon, help, tone }: {
  label: string;
  value: string | number;
  suffix?: string;
  icon: ReactNode;
  help: string;
  tone: string;
}) {
  return (
    <div className={`datasource-metric datasource-metric-${tone}`}>
      <span className="datasource-metric-icon">{icon}</span>
      <div>
        <Tooltip title={help}><span className="datasource-metric-label">{label} <InfoCircleOutlined /></span></Tooltip>
        <strong>{value}<small>{suffix}</small></strong>
      </div>
    </div>
  );
}
