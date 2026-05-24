import { Button, Select, Slider, Tag } from 'antd';
import type { GraphLayer } from './flowTypes';

type SubjectOption = { label: string; value: string };
type PathResult = { nodes: string[]; edges: string[] };

export function FlowGraphFilters(props: {
  subjectIds: string[];
  subjectOptions: SubjectOption[];
  onSubjectIdsChange: (values: string[]) => void;
  minAmount: number;
  maxAmount: number;
  onMinAmountChange: (value: number) => void;
  pathSource?: string;
  pathTarget?: string;
  pathResult: PathResult;
  nodeLabels: Map<string, string>;
  onPathSourceChange: (value?: string) => void;
  onPathTargetChange: (value?: string) => void;
  graphLayers: GraphLayer[];
  onCenterGraphLayer: (layerId: string) => void;
  formatMoney: (value: number) => string;
}) {
  return (
    <>
      <div className="inspector-title">
        <strong>主体筛选</strong>
      </div>
      <Select
        mode="multiple"
        allowClear
        showSearch
        maxTagCount="responsive"
        placeholder="选择主体查看连接"
        optionFilterProp="label"
        value={props.subjectIds}
        options={props.subjectOptions}
        onChange={props.onSubjectIdsChange}
      />
      <div className="inspector-title">
        <strong>关系过滤</strong>
        <Button size="small" type="link" onClick={() => props.onMinAmountChange(0)}>重置</Button>
      </div>
      <Slider
        min={0}
        max={props.maxAmount || 1}
        value={Math.min(props.minAmount, props.maxAmount || 1)}
        onChange={(value) => props.onMinAmountChange(Number(value))}
        tooltip={{ formatter: (value) => `>= ${props.formatMoney(Number(value ?? 0))}` }}
      />
      <div className="filter-value">最小金额：{props.formatMoney(props.minAmount)}</div>
      <div className="inspector-title">
        <strong>路径追踪</strong>
        <span>{props.pathResult.nodes.length ? `${props.pathResult.nodes.length} 个主体` : '选择两端主体'}</span>
      </div>
      <Select
        allowClear
        showSearch
        placeholder="起点主体"
        optionFilterProp="label"
        value={props.pathSource}
        options={props.subjectOptions}
        onChange={props.onPathSourceChange}
      />
      <Select
        allowClear
        showSearch
        placeholder="终点主体"
        optionFilterProp="label"
        value={props.pathTarget}
        options={props.subjectOptions}
        onChange={props.onPathTargetChange}
      />
      <div className="path-result">
        {props.pathResult.nodes.length
          ? props.pathResult.nodes.map((id) => props.nodeLabels.get(id) ?? id).join(' -> ')
          : props.pathSource && props.pathTarget
            ? '当前聚合图内未找到路径'
            : '用于快速查看两个主体之间的资金链路'}
      </div>
      {!!props.graphLayers.length && (
        <div className="graph-layer-list">
          <strong>当前画布</strong>
          {props.graphLayers.map((layer, index) => (
            <Tag key={layer.id} color={index % 2 ? 'purple' : 'blue'} onClick={() => props.onCenterGraphLayer(layer.id)} className="clickable-tag">
              {layer.label}
            </Tag>
          ))}
        </div>
      )}
    </>
  );
}
