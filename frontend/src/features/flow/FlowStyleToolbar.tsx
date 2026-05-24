import { Collapse, ColorPicker, InputNumber, Select } from 'antd';
import type { ArrowMode, EdgeLabelMode, LineType, TimeWindow } from './flowTypes';

export function FlowStyleToolbar(props: {
  collapsed: boolean;
  onCollapsedChange: (collapsed: boolean) => void;
  edgeLabelMode: EdgeLabelMode;
  onEdgeLabelModeChange: (mode: EdgeLabelMode) => void;
  lineType: LineType;
  onLineTypeChange: (type: LineType) => void;
  arrowMode: ArrowMode;
  onArrowModeChange: (mode: ArrowMode) => void;
  optimizeAnchors: boolean;
  onOptimizeAnchorsChange: (enabled: boolean) => void;
  lineColor: string;
  onLineColorChange: (color: string) => void;
  lineWidth: number;
  onLineWidthChange: (width: number) => void;
  timeWindow: TimeWindow;
  onTimeWindowChange: (window: TimeWindow) => void;
  renderLimit: number;
  onRenderLimitChange: (limit: number) => void;
}) {
  return (
    <Collapse
      className={`graph-toolbar-collapse ${props.collapsed ? 'toolbar-collapsed' : ''}`}
      activeKey={props.collapsed ? [] : ['style']}
      onChange={(keys) => props.onCollapsedChange(!(Array.isArray(keys) ? keys : [keys]).includes('style'))}
      items={[
        {
          key: 'style',
          label: (
            <div className="graph-toolbar-title">
              <span>全局样式设置</span>
            </div>
          ),
          children: (
            <div className="graph-toolbar">
              <Select<EdgeLabelMode> value={props.edgeLabelMode} onChange={props.onEdgeLabelModeChange} options={[
                { label: '线条：金额 + 笔数', value: 'amount_count' },
                { label: '线条：金额', value: 'amount' },
                { label: '线条：笔数', value: 'count' },
                { label: '线条：不显示', value: 'none' },
              ]} />
              <Select<LineType> value={props.lineType} onChange={props.onLineTypeChange} options={[
                { label: '线条：直线', value: 'straight' },
                { label: '线条：曲线', value: 'smoothstep' },
                { label: '线条：折线', value: 'step' },
              ]} />
              <Select<ArrowMode> value={props.arrowMode} onChange={props.onArrowModeChange} options={[
                { label: '箭头：正向', value: 'forward' },
                { label: '箭头：反向', value: 'reverse' },
                { label: '箭头：双向', value: 'both' },
                { label: '箭头：无箭头', value: 'none' },
              ]} />
              <Select value={props.optimizeAnchors ? 'on' : 'off'} onChange={(value) => props.onOptimizeAnchorsChange(value === 'on')} options={[
                { label: '连接点优化：开启', value: 'on' },
                { label: '连接点优化：关闭', value: 'off' },
              ]} />
              <ColorPicker value={props.lineColor} onChange={(_, hex) => props.onLineColorChange(hex)} showText />
              <InputNumber min={0.5} max={8} step={0.5} value={props.lineWidth} addonBefore="线宽" onChange={(value) => props.onLineWidthChange(Number(value ?? 1.2))} />
              <Select<TimeWindow> value={props.timeWindow} onChange={props.onTimeWindowChange} options={[
                { label: '时间：全部', value: 'all' },
                { label: '近 30 天', value: '30' },
                { label: '近 90 天', value: '90' },
                { label: '近 180 天', value: '180' },
                { label: '近 1 年', value: '365' },
              ]} />
              <Select value={props.renderLimit} onChange={props.onRenderLimitChange} options={[
                { label: '渲染全部关系', value: 0 },
                { label: '渲染前 100 条', value: 100 },
                { label: '渲染前 200 条', value: 200 },
                { label: '渲染前 400 条', value: 400 },
                { label: '渲染前 600 条', value: 600 },
              ]} />
            </div>
          ),
        },
      ]}
    />
  );
}
