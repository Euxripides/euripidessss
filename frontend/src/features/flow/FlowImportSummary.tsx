import { DatabaseOutlined, SettingOutlined } from '@ant-design/icons';
import { Button } from 'antd';
import type { ImportedDataset } from './flowTypes';

export function FlowImportSummary(props: {
  importedDataset: ImportedDataset | null;
  visibleNodes: number;
  totalNodes: number;
  onSelectSource: () => void;
  onOpenMapping: () => void;
}) {
  return (
    <>
      <div className="data-import-actions">
        <Button type="primary" block icon={<DatabaseOutlined />} onClick={props.onSelectSource}>
          选择来源数据
        </Button>
        <Button block icon={<SettingOutlined />} onClick={props.onOpenMapping}>
          字段映射 / 模板说明
        </Button>
      </div>
      <div className="graph-mini-stat">
        <span>{props.importedDataset ? `${props.importedDataset.rows} 行已导入` : '未导入数据'}</span>
        <span>{props.visibleNodes}/{props.totalNodes} 主体</span>
      </div>
    </>
  );
}
