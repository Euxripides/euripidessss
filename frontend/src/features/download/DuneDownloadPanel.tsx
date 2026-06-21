import { Tabs } from 'antd';
import { DuneBatchReg } from './DuneBatchReg';
import { DuneQueryPanel } from './DuneQueryPanel';

export function DuneDownloadPanel() {
  return (
    <Tabs
      className="dune-tabs"
      items={[
        { key: 'query', label: '数据查询', children: <DuneQueryPanel /> },
        { key: 'batch', label: '批量注册', children: <DuneBatchReg /> },
      ]}
    />
  );
}
