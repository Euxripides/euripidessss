import { Progress, Tooltip } from 'antd';

export function HealthChart({ score, successRate }: { score: number; successRate: number }) {
  const normalized = Math.max(0, Math.min(100, score));
  const status = normalized >= 85 ? 'success' : normalized >= 60 ? 'normal' : 'exception';
  return (
    <Tooltip title={`健康评分 ${normalized.toFixed(1)}；成功率 ${successRate.toFixed(1)}%`}>
      <div className="datasource-health-line">
        <Progress percent={Math.round(normalized)} status={status} showInfo={false} strokeWidth={6} />
        <strong>{normalized.toFixed(0)}</strong>
      </div>
    </Tooltip>
  );
}
