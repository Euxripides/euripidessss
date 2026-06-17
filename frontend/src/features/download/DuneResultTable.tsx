import { Table } from 'antd';
import type { ColumnsType, TablePaginationConfig } from 'antd/es/table';
import { useMemo } from 'react';
import type { DuneQueryResponse, DuneRow } from './duneApi';

type DuneTableRow = DuneRow & {
  readonly __rowKey: string;
};

type DuneResultTableProps = {
  readonly result: DuneQueryResponse;
  readonly page: number;
  readonly pageSize: number;
  readonly loading: boolean;
  readonly onChange: (pagination: TablePaginationConfig) => void;
};

export function DuneResultTable({ result, page, pageSize, loading, onChange }: DuneResultTableProps) {
  const tableRows = useMemo<readonly DuneTableRow[]>(() => {
    return result.rows.map((row, index) => ({
      ...row,
      __rowKey: `${result.executionId}-${page}-${index}`,
    }));
  }, [page, result]);

  const columns = useMemo<ColumnsType<DuneTableRow>>(() => {
    return result.columns.map((column) => ({
      title: (
        <div className="dune-column-title">
          <span>{result.columnLabels[column] || column}</span>
          <small>{column}</small>
        </div>
      ),
      dataIndex: column,
      key: column,
      ellipsis: true,
      width: 180,
      render: (value: DuneRow[string]) => formatCell(value),
    }));
  }, [result]);

  return (
    <div className="dune-result-panel">
      <div className="dune-result-meta">
        <strong>查询结果</strong>
        <span>{result.totalRowCount.toLocaleString()} 行 · {result.columns.length} 列 · {result.state}</span>
      </div>
      <Table<DuneTableRow>
        rowKey="__rowKey"
        loading={loading}
        columns={columns}
        dataSource={tableRows}
        scroll={{ x: 'max-content', y: 520 }}
        size="small"
        pagination={{
          current: page,
          pageSize,
          total: result.totalRowCount,
          showSizeChanger: true,
          pageSizeOptions: [50, 100, 200, 500, 1000],
        }}
        onChange={onChange}
      />
    </div>
  );
}

function formatCell(value: DuneRow[string]) {
  if (value === null || typeof value === 'undefined') return <span className="muted">NULL</span>;
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  return String(value);
}
