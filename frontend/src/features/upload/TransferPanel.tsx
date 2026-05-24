import { Progress } from 'antd';
import type { TransferStatus } from '../flow/flowTypes';

export function TransferPanel({ status }: { status: TransferStatus }) {
  if (!status.visible) return null;
  const statusType = status.phase === 'error' ? 'exception' : status.phase === 'done' ? 'success' : 'active';
  return (
    <div className={`transfer-panel transfer-${status.phase}`}>
      <div>
        <strong>{status.label || transferPhaseLabel(status.phase)}</strong>
        <span>
          {status.mode === 'external' ? '外网通道' : '本地/内网通道'} · {formatBytes(status.loaded)} / {status.total ? formatBytes(status.total) : '未知大小'}
        </span>
        <em>{buildTransferMeta(status)}</em>
      </div>
      <Progress percent={status.percent} status={statusType} size="small" />
    </div>
  );
}

function transferPhaseLabel(phase: TransferStatus['phase']) {
  return {
    idle: '等待传输',
    packing: '正在压缩文件',
    uploading: '正在上传',
    processing: '后端处理中',
    downloading: '正在下载',
    done: '传输完成',
    error: '传输失败',
  }[phase];
}

function buildTransferMeta(status: TransferStatus) {
  const parts = [];
  if (status.speed > 0) {
    parts.push(`${formatBytes(status.speed)}/s`);
    const remaining = estimateRemainingSeconds(status);
    if (remaining !== null) parts.push(`剩余 ${formatDuration(remaining)}`);
  }
  return parts.join(' · ');
}

function estimateRemainingSeconds(status: TransferStatus) {
  if (!status.total || status.speed <= 0 || status.loaded >= status.total) return null;
  return Math.max(1, Math.ceil((status.total - status.loaded) / status.speed));
}

function formatDuration(seconds: number) {
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  if (minutes < 60) return rest ? `${minutes} 分 ${rest} 秒` : `${minutes} 分`;
  const hours = Math.floor(minutes / 60);
  const minuteRest = minutes % 60;
  return minuteRest ? `${hours} 小时 ${minuteRest} 分` : `${hours} 小时`;
}

function formatBytes(value: number) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}
