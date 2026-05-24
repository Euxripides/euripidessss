import type { EdgePatch } from './flowTypes';

export function aggregateRowsByDate(rows: Record<string, unknown>[], start?: number, end?: number): EdgePatch {
  const amountColumn = findColumnByKeywords(rows, ['交易金额', '金额', 'amount']);
  const timeColumn = findColumnByKeywords(rows, ['交易时间', '时间', '日期', 'time', 'date']);
  const matched = rows.filter((row) => {
    if (!timeColumn || (!start && !end)) return true;
    const time = parseRowTime(row[timeColumn]);
    if (!time) return true;
    if (start && time < start) return false;
    if (end && time > end) return false;
    return true;
  });
  const amounts = matched.map((row) => parseAmount(row[amountColumn ?? ''])).filter((value) => Number.isFinite(value));
  const times = timeColumn
    ? matched.map((row) => parseRowTime(row[timeColumn])).filter((value): value is number => Boolean(value)).sort((a, b) => a - b)
    : [];
  const amount = amounts.reduce((sum, value) => sum + value, 0);
  return {
    amount,
    tx_count: matched.length,
    avg_amount: matched.length ? amount / matched.length : 0,
    max_amount: amounts.length ? Math.max(...amounts) : 0,
    first_time: times[0] ? formatDateTime(times[0]) : '',
    last_time: times[times.length - 1] ? formatDateTime(times[times.length - 1]) : '',
  };
}

function findColumnByKeywords(rows: Record<string, unknown>[], keywords: string[]) {
  const columns = Object.keys(rows[0] ?? {});
  return columns.find((column) => keywords.some((keyword) => column.toLowerCase().includes(keyword.toLowerCase())));
}

function parseAmount(value: unknown) {
  return Number(String(value ?? '').replace(/,/g, '').replace(/[^\d.+-]/g, '')) || 0;
}

function parseRowTime(value: unknown) {
  const text = String(value ?? '').trim();
  if (!text) return 0;
  const normalized = /^\d{14}$/.test(text)
    ? `${text.slice(0, 4)}-${text.slice(4, 6)}-${text.slice(6, 8)} ${text.slice(8, 10)}:${text.slice(10, 12)}:${text.slice(12, 14)}`
    : /^\d{8}$/.test(text)
      ? `${text.slice(0, 4)}-${text.slice(4, 6)}-${text.slice(6, 8)} 00:00:00`
      : text.replace(/\//g, '-');
  const time = new Date(normalized).getTime();
  return Number.isFinite(time) ? time : 0;
}

function formatDateTime(time: number) {
  const date = new Date(time);
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}
