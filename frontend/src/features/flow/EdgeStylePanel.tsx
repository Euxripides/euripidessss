import { DeleteOutlined } from '@ant-design/icons';
import { Button, ColorPicker, DatePicker, Input, InputNumber, Select, Space } from 'antd';
import { useState } from 'react';
import type { Edge } from '@xyflow/react';
import type { ArrowMode, EdgeLinePattern, EdgePatch } from './flowTypes';

export function EdgeStylePanel(props: {
  edges: Edge[];
  defaultLineWidth: number;
  defaultLineColor: string;
  defaultArrowMode: ArrowMode;
  onUpdate: (patch: EdgePatch) => void;
  onRecalculateDateRange: (range: any) => void;
  onOpenDetail: () => void;
  onDelete: () => void;
  onClose: () => void;
}) {
  const isBatch = props.edges.length > 1;
  const first = props.edges[0];
  const label = String(first.data?.customLabel ?? first.label ?? '');
  const lineWidth = getEdgeLineWidth(first, props.defaultLineWidth);
  const lineColor = getEdgeLineColor(first, props.defaultLineColor);
  const arrowMode = getEdgeArrowMode(first, props.defaultArrowMode);
  const linePattern = getEdgeLinePattern(first);
  const firstTime = first.data?.first_time ? String(first.data.first_time) : '';
  const lastTime = first.data?.last_time ? String(first.data.last_time) : '';
  const [dateRange, setDateRange] = useState<any>(null);

  return (
    <div className="edge-floating-panel">
      <div className="edge-floating-header">
        <strong>{isBatch ? `批量编辑 ${props.edges.length} 条线条` : '线条样式'}</strong>
        <Button size="small" onClick={props.onClose}>关闭</Button>
      </div>
      <Space direction="vertical" size="middle" className="full">
        {!isBatch && (
          <div className="edge-summary">
            <span>金额：{formatMoney(getEdgeAmount(first))}</span>
            <span>笔数：{getEdgeCount(first)} 笔</span>
            {(firstTime || lastTime) && <span>时间：{firstTime || '-'} 至 {lastTime || '-'}</span>}
          </div>
        )}
        <Button block type="primary" onClick={props.onOpenDetail} disabled={isBatch}>
          详细数据
        </Button>
        {!isBatch && (
          <Space.Compact className="full">
            <DatePicker.RangePicker
              className="full"
              value={dateRange}
              onChange={setDateRange}
              placeholder={['开始时间', '结束时间']}
            />
            <Button onClick={() => props.onRecalculateDateRange(dateRange)}>重算</Button>
          </Space.Compact>
        )}
        {!isBatch && (
          <Input
            value={label}
            addonBefore="显示数据"
            onChange={(event) => props.onUpdate({ customLabel: event.target.value })}
          />
        )}
        <InputNumber
          min={0.5}
          max={12}
          step={0.5}
          value={lineWidth}
          addonBefore="线条磅数"
          className="full"
          onChange={(value) => props.onUpdate({ lineWidth: Number(value ?? props.defaultLineWidth) })}
        />
        <ColorPicker value={lineColor} onChange={(_, hex) => props.onUpdate({ lineColor: hex })} showText />
        <Select<ArrowMode>
          value={arrowMode}
          className="full"
          onChange={(value) => props.onUpdate({ arrowMode: value })}
          options={[
            { label: '箭头：正向', value: 'forward' },
            { label: '箭头：反向', value: 'reverse' },
            { label: '箭头：双向', value: 'both' },
            { label: '箭头：无', value: 'none' },
          ]}
        />
        <Select<EdgeLinePattern>
          value={linePattern}
          className="full"
          onChange={(value) => props.onUpdate({ linePattern: value })}
          options={[
            { label: '线条：实线', value: 'solid' },
            { label: '线条：虚线', value: 'dashed' },
          ]}
        />
        <Button danger block icon={<DeleteOutlined />} onClick={props.onDelete}>
          {isBatch ? '删除选中线条' : '删除线条'}
        </Button>
      </Space>
    </div>
  );
}

function getEdgeAmount(edge: Edge) {
  return Number(edge.data?.amount ?? 0);
}

function getEdgeCount(edge: Edge) {
  return Number(edge.data?.tx_count ?? 0);
}

function getEdgeLineWidth(edge: Edge, fallback: number) {
  return Number(edge.data?.lineWidth ?? fallback);
}

function getEdgeLineColor(edge: Edge, fallback: string) {
  return String(edge.data?.lineColor ?? fallback);
}

function getEdgeArrowMode(edge: Edge, fallback: ArrowMode) {
  const value = edge.data?.arrowMode;
  return value === 'forward' || value === 'reverse' || value === 'both' || value === 'none' ? value : fallback;
}

function getEdgeLinePattern(edge: Edge): EdgeLinePattern {
  return edge.data?.linePattern === 'dashed' ? 'dashed' : 'solid';
}

function formatMoney(value: number) {
  return Number(value || 0).toLocaleString('zh-CN', { maximumFractionDigits: 0 });
}
