import {
  BaseEdge,
  EdgeLabelRenderer,
  Handle,
  Position,
  useUpdateNodeInternals,
  type EdgeProps,
  type NodeProps,
} from '@xyflow/react';
import { useEffect, type CSSProperties, type ReactNode } from 'react';
import { FLOW_NODE_ICON_SIZE, FLOW_NODE_LABEL_WIDTH, type DynamicAnchor } from './flowTypes';

export function FlowEntityNode(props: NodeProps) {
  const dynamicHandles = dedupeDynamicAnchors((props.data.dynamicHandles as DynamicAnchor[] | undefined) ?? []);
  const canExpand = Boolean(props.data.penetrationCanExpand);
  const canCollapse = Boolean(props.data.penetrationCanCollapse);
  const onExpand = props.data.onPenetrationExpand as ((nodeId: string) => void) | undefined;
  const onCollapse = props.data.onPenetrationCollapse as ((nodeId: string) => void) | undefined;
  const updateNodeInternals = useUpdateNodeInternals();
  const dynamicHandleKey = dynamicHandles
    .map((anchor) => `${anchor.id}:${anchor.side}:${anchor.offset.toFixed(2)}:${anchor.x?.toFixed(2) ?? ''}:${anchor.y?.toFixed(2) ?? ''}`)
    .join('|');

  useEffect(() => {
    updateNodeInternals(props.id);
  }, [dynamicHandleKey, props.id, updateNodeInternals]);

  return (
    <>
      <Handle className="flow-handle base-anchor" id="top-target" type="target" position={Position.Top} style={styleForDynamicAnchor({ id: 'top', side: 'top', offset: 50 })} />
      <Handle className="flow-handle base-anchor" id="right-target" type="target" position={Position.Right} style={styleForDynamicAnchor({ id: 'right', side: 'right', offset: 50 })} />
      <Handle className="flow-handle base-anchor" id="bottom-target" type="target" position={Position.Bottom} style={styleForDynamicAnchor({ id: 'bottom', side: 'bottom', offset: 50 })} />
      <Handle className="flow-handle base-anchor" id="left-target" type="target" position={Position.Left} style={styleForDynamicAnchor({ id: 'left', side: 'left', offset: 50 })} />
      {dynamicHandles.map((anchor) => (
        <Handle
          key={`${anchor.id}-target`}
          className="flow-handle dynamic-anchor"
          id={`${anchor.id}-target`}
          type="target"
          position={positionForDynamicAnchor(anchor)}
          style={styleForDynamicAnchor(anchor)}
        />
      ))}
      <div className="flow-node-content">
        {props.data.label as ReactNode}
        {canExpand && (
          <button
            className="penetration-toggle penetration-expand"
            type="button"
            title="展开后续交易"
            onMouseDown={(event) => event.stopPropagation()}
            onClick={(event) => {
              event.stopPropagation();
              onExpand?.(props.id);
            }}
          >
            +
          </button>
        )}
        {canCollapse && (
          <button
            className="penetration-toggle penetration-collapse"
            type="button"
            title="折叠后续交易"
            onMouseDown={(event) => event.stopPropagation()}
            onClick={(event) => {
              event.stopPropagation();
              onCollapse?.(props.id);
            }}
          >
            -
          </button>
        )}
      </div>
      <Handle className="flow-handle base-anchor" id="top-source" type="source" position={Position.Top} style={styleForDynamicAnchor({ id: 'top', side: 'top', offset: 50 })} />
      <Handle className="flow-handle base-anchor" id="right-source" type="source" position={Position.Right} style={styleForDynamicAnchor({ id: 'right', side: 'right', offset: 50 })} />
      <Handle className="flow-handle base-anchor" id="bottom-source" type="source" position={Position.Bottom} style={styleForDynamicAnchor({ id: 'bottom', side: 'bottom', offset: 50 })} />
      <Handle className="flow-handle base-anchor" id="left-source" type="source" position={Position.Left} style={styleForDynamicAnchor({ id: 'left', side: 'left', offset: 50 })} />
      {dynamicHandles.map((anchor) => (
        <Handle
          key={`${anchor.id}-source`}
          className="flow-handle dynamic-anchor"
          id={`${anchor.id}-source`}
          type="source"
          position={positionForDynamicAnchor(anchor)}
          style={styleForDynamicAnchor(anchor)}
        />
      ))}
    </>
  );
}

export function DirectionalFlowEdge(props: EdgeProps) {
  const offset = Number(props.data?.parallelOffset ?? 0);
  const label = props.data?.displayLabel ? String(props.data.displayLabel) : '';
  const dx = props.targetX - props.sourceX;
  const dy = props.targetY - props.sourceY;
  const length = Math.hypot(dx, dy) || 1;
  const normalX = (-dy / length) * offset;
  const normalY = (dx / length) * offset;
  const sourceX = props.sourceX + normalX;
  const sourceY = props.sourceY + normalY;
  const targetX = props.targetX + normalX;
  const targetY = props.targetY + normalY;
  const unitX = dx / length;
  const unitY = dy / length;
  const hitTrim = Math.min(34, length / 3);
  const hitSourceX = sourceX + unitX * hitTrim;
  const hitSourceY = sourceY + unitY * hitTrim;
  const hitTargetX = targetX - unitX * hitTrim;
  const hitTargetY = targetY - unitY * hitTrim;
  const labelOffset = Math.sign(offset || 1) * Math.max(Math.abs(offset) * 1.25, 16);
  const labelX = (props.sourceX + props.targetX) / 2 + (-dy / length) * labelOffset;
  const labelY = (props.sourceY + props.targetY) / 2 + (dx / length) * labelOffset;
  const path = `M ${sourceX},${sourceY} L ${targetX},${targetY}`;
  const hitPath = `M ${hitSourceX},${hitSourceY} L ${hitTargetX},${hitTargetY}`;
  const style = props.selected
    ? {
        ...(props.style ?? {}),
        strokeWidth: Math.max(3.5, Number((props.style as CSSProperties | undefined)?.strokeWidth ?? 1.2)),
      }
    : props.style;

  return (
    <>
      <BaseEdge
        id={props.id}
        path={path}
        markerStart={props.markerStart}
        markerEnd={props.markerEnd}
        style={{ ...(style ?? {}), pointerEvents: 'none' }}
        interactionWidth={0}
      />
      <path
        d={hitPath}
        className="react-flow__edge-interaction directional-edge-hit"
        fill="none"
        stroke="rgba(17, 24, 39, 0.001)"
        strokeWidth={props.interactionWidth ?? 32}
        strokeLinecap="round"
        pointerEvents="stroke"
      />
      {label && (
        <EdgeLabelRenderer>
          <div
            className="directional-edge-label"
            style={{
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
            }}
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

function dedupeDynamicAnchors(anchors: DynamicAnchor[]) {
  const seen = new Set<string>();
  return anchors.filter((anchor) => {
    if (seen.has(anchor.id)) return false;
    seen.add(anchor.id);
    return true;
  });
}

function positionForDynamicAnchor(anchor: DynamicAnchor) {
  return {
    top: Position.Top,
    right: Position.Right,
    bottom: Position.Bottom,
    left: Position.Left,
  }[anchor.side];
}

function styleForDynamicAnchor(anchor: DynamicAnchor): CSSProperties {
  if (anchor.x !== undefined && anchor.y !== undefined) {
    return {
      left: anchor.x,
      top: anchor.y,
      right: 'auto',
      bottom: 'auto',
      transform: 'translate(-50%, -50%)',
    };
  }
  const iconLeft = (FLOW_NODE_LABEL_WIDTH - FLOW_NODE_ICON_SIZE) / 2;
  const iconOffset = (clamp(anchor.offset, 0, 100) / 100) * FLOW_NODE_ICON_SIZE;
  const base: CSSProperties = {
    right: 'auto',
    bottom: 'auto',
    transform: 'translate(-50%, -50%)',
  };
  if (anchor.side === 'top') return { ...base, left: iconLeft + iconOffset, top: 0 };
  if (anchor.side === 'bottom') return { ...base, left: iconLeft + iconOffset, top: FLOW_NODE_ICON_SIZE };
  if (anchor.side === 'left') return { ...base, left: iconLeft, top: iconOffset };
  return { ...base, left: iconLeft + FLOW_NODE_ICON_SIZE, top: iconOffset };
}

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(max, value));
}
