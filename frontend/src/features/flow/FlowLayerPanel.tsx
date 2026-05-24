import { DeleteOutlined, RightOutlined } from '@ant-design/icons';
import { Button } from 'antd';
import type { GraphLayer } from './flowTypes';
import { graphLayerColor } from './flowNodes';

export function FlowLayerPanel(props: {
  layers: GraphLayer[];
  collapsed: boolean;
  selectedLayerIds: string[];
  onCollapsedChange: (collapsed: boolean) => void;
  onClearSelection: () => void;
  onToggleSelection: (layerId: string) => void;
  onCenterLayer: (layerId: string) => void;
  onDeleteLayer: (layerId: string) => void;
}) {
  if (!props.layers.length) return null;
  return (
    <div className={`graph-layer-floating ${props.collapsed ? 'collapsed' : ''}`}>
      {props.collapsed ? (
        <button className="graph-layer-toggle" type="button" aria-label="展开当前画布" onClick={() => props.onCollapsedChange(false)}>
          <RightOutlined />
        </button>
      ) : (
        <>
          <div className="graph-layer-floating-head">
            <button className="graph-layer-toggle" type="button" aria-label="折叠当前画布" onClick={() => props.onCollapsedChange(true)}>
              <RightOutlined />
            </button>
            <strong>当前画布</strong>
            <Button size="small" type="link" onClick={props.onClearSelection}>
              清空
            </Button>
          </div>
          <div className="graph-layer-floating-list">
            {props.layers.map((layer, index) => {
              const active = props.selectedLayerIds.includes(layer.id);
              return (
                <div key={layer.id} className="graph-layer-row">
                  <button
                    type="button"
                    className={`graph-layer-chip ${active ? 'active' : ''}`}
                    onClick={() => props.onToggleSelection(layer.id)}
                    onDoubleClick={() => props.onCenterLayer(layer.id)}
                    title={`${layer.label}。${active ? '已选中：参与同步移动' : '点击选中该画布参与同步移动；双击定位'}`}
                  >
                    <span style={{ background: graphLayerColor(index) }} />
                    <em>{layer.label}</em>
                  </button>
                  <Button
                    size="small"
                    danger
                    type="text"
                    icon={<DeleteOutlined />}
                    title="删除画布"
                    onClick={() => props.onDeleteLayer(layer.id)}
                  />
                </div>
              );
            })}
          </div>
          <p>{props.selectedLayerIds.length ? `已选中 ${props.selectedLayerIds.length} 个画布：拖动其中任一主体可同步移动。` : '未选中：主体可单独移动。'}</p>
        </>
      )}
    </div>
  );
}
