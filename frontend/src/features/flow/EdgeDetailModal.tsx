import { Input, Modal, Table } from 'antd';
import { useMemo, type Key } from 'react';
import type { EdgeDetailPayload } from './flowTypes';

export function EdgeDetailModal(props: {
  open: boolean;
  loading: boolean;
  detail: EdgeDetailPayload | null;
  search: string;
  onSearch: (value: string) => void;
  onClose: () => void;
}) {
  const keyword = props.search.trim().toLowerCase();
  const rows = useMemo(
    () =>
      (props.detail?.rows ?? [])
        .map((row, index) => ({ ...row, __row_id: index + 1 }))
        .filter((row) => {
          if (!keyword) return true;
          return Object.values(row).some((value) => formatPreviewCell(value).toLowerCase().includes(keyword));
        }),
    [keyword, props.detail],
  );
  const columns = useMemo(() => {
    const sourceColumns = props.detail?.columns ?? [];
    return [
      {
        title: '序号',
        dataIndex: '__row_id',
        key: '__row_id',
        width: 70,
        fixed: 'left' as const,
      },
      ...sourceColumns.map((column) => {
        const filters = buildColumnFilters(props.detail?.rows ?? [], column);
        return {
          title: column,
          dataIndex: column,
          key: column,
          width: Math.max(96, Math.min(180, column.length * 14 + 42)),
          ellipsis: false,
          filters,
          filterSearch: true,
          onFilter: (value: boolean | Key, record: Record<string, unknown>) => formatPreviewCell(record[column]) === String(value),
          render: (value: unknown) => {
            const text = formatPreviewCell(value);
            return <span className="excel-cell-text" title={text}>{text || '\u00A0'}</span>;
          },
        };
      }),
    ];
  }, [props.detail]);
  const tableWidth = Math.max(1200, columns.reduce((sum, column) => sum + Number(column.width ?? 140), 0));

  return (
    <Modal
      title="线条详细数据"
      open={props.open}
      onCancel={props.onClose}
      footer={null}
      width="96vw"
      centered
      destroyOnHidden
      className="edge-detail-modal-shell"
    >
      <div className="edge-detail-modal">
        <div className="edge-detail-toolbar">
          <div>
            <strong>{props.detail ? `${props.detail.source} -> ${props.detail.target}` : '流水明细'}</strong>
            <span>
              {props.detail
                ? `${props.detail.total_rows.toLocaleString('zh-CN')} 笔，金额合计 ${formatMoney(props.detail.amount)}${props.detail.truncated ? `，当前返回 ${props.detail.returned_rows.toLocaleString('zh-CN')} 笔` : ''}`
                : '正在读取线条对应流水'}
            </span>
          </div>
          <Input.Search
            allowClear
            placeholder="搜索过滤全部字段"
            value={props.search}
            onChange={(event) => props.onSearch(event.target.value)}
            className="edge-detail-search"
          />
        </div>
        <Table
          size="small"
          bordered
          loading={props.loading}
          rowKey="__row_id"
          dataSource={rows}
          columns={columns}
          tableLayout="fixed"
          scroll={{ x: tableWidth, y: '62vh' }}
          pagination={{ pageSize: 50, showSizeChanger: true, pageSizeOptions: [20, 50, 100, 200], showTotal: (total) => `共 ${total} 条` }}
        />
      </div>
    </Modal>
  );
}

function buildColumnFilters(rows: Record<string, unknown>[], column: string) {
  const values = Array.from(new Set(rows.map((row) => formatPreviewCell(row[column])).filter(Boolean))).slice(0, 80);
  return values.map((value) => ({ text: value, value }));
}

function formatPreviewCell(value: unknown) {
  if (value === null || value === undefined || value === '') return '';
  if (typeof value === 'number') return Number.isFinite(value) ? value.toLocaleString('zh-CN') : '';
  if (typeof value === 'boolean') return value ? '是' : '否';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

function formatMoney(value: number) {
  return Number(value || 0).toLocaleString('zh-CN', { maximumFractionDigits: 0 });
}
