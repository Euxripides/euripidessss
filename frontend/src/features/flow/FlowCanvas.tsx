import type { MenuProps } from "antd";
import {
  Button,
  Dropdown,
} from "antd";

import {
  DownloadOutlined,
  PlusOutlined,
} from "@ant-design/icons";

import {
  Controls,
  MiniMap,
  ReactFlow,
  SelectionMode,
  type Edge,
  type Node,
  type OnConnect,
  type OnEdgesChange,
  type OnNodesChange,
  type ReactFlowInstance,
} from "@xyflow/react";

import { EdgeDetailModal } from "./EdgeDetailModal";
import { EdgeStylePanel } from "./EdgeStylePanel";
import { FlowLayerPanel } from "./FlowLayerPanel";
import { FlowStyleToolbar } from "./FlowStyleToolbar";
import { DirectionalFlowEdge, FlowEntityNode } from "./FlowGraphPrimitives";
import { miniMapNodeColor, miniMapNodeStrokeColor } from "./flowNodes";
import type { EdgeDetailPayload, EdgeLabelMode, EdgePatch, EdgeLinePattern, GraphExportFormat, GraphLayer, LineType, ArrowMode, TimeWindow } from "./flowTypes";

export interface FlowCanvasProps {
  // Graph data
  nodes: Node[];
  edges: Edge[];
  graphLayers: GraphLayer[];
  meta: Record<string, unknown>;

  // React Flow callbacks
  onNodesChange: OnNodesChange;
  onEdgesChange: OnEdgesChange;
  onConnect: OnConnect;
  onNodeClick: (event: React.MouseEvent, node: Node) => void;
  onAddNode: () => void;

  // Canvas computed data
  visibleGraph: { nodes: Node[]; edges: Edge[] };
  selectedEdges: Edge[];

  // Toolbar state
  edgeLabelMode: EdgeLabelMode;
  onEdgeLabelModeChange: (mode: EdgeLabelMode) => void;
  lineType: LineType;
  onLineTypeChange: (type: LineType) => void;
  arrowMode: ArrowMode;
  onArrowModeChange: (mode: ArrowMode) => void;
  optimizeAnchors: boolean;
  onOptimizeAnchorsChange: (v: boolean) => void;
  lineColor: string;
  onLineColorChange: (c: string) => void;
  lineWidth: number;
  onLineWidthChange: (w: number) => void;
  timeWindow: TimeWindow;
  onTimeWindowChange: (w: TimeWindow) => void;
  renderLimit: number;
  onRenderLimitChange: (l: number) => void;
  toolbarCollapsed: boolean;
  onToolbarCollapsedChange: (c: boolean) => void;
  miniMapCollapsed: boolean;
  onMiniMapCollapsedChange: (c: boolean) => void;
  graphLayerPanelCollapsed: boolean;
  onGraphLayerPanelCollapsedChange: (c: boolean) => void;

  // Layer panel
  selectedGraphLayerIds: string[];
  onToggleGraphLayerSelection: (layerId: string) => void;
  onSelectedGraphLayerIdsChange: (ids: string[]) => void;
  onCenterGraphLayer: (layerId: string) => void;
  onDeleteGraphLayerFromPanel: (layerId: string) => void;

  // Edge selection & detail
  selectedEdgeIds: string[];
  onSelectedEdgeIdsChange: (ids: string[]) => void;
  edgeDetailOpen: boolean;
  onEdgeDetailOpenChange: (v: boolean) => void;
  edgeDetail: EdgeDetailPayload | null;
  edgeDetailLoading: boolean;
  edgeDetailSearch: string;
  onEdgeDetailSearchChange: (s: string) => void;
  onUpdateEdges: (ids: string[], patch: EdgePatch) => void;
  onDeleteEdges: (ids: string[]) => void;
  onUpdateSelectedEdges: (patch: EdgePatch) => void;
  onDeleteSelectedEdges: () => void;
  onOpenEdgeDetail: (edge: Edge) => void;
  onRecalculateEdgeByDate: (edge: Edge, range: [string, string]) => void;

  // Event handlers
  onEdgeClick: (event: React.MouseEvent, edge: Edge) => void;
  onNodeDragStart: (event: React.MouseEvent, node: Node) => void;
  onNodeDrag: (event: React.MouseEvent, node: Node) => void;

  // Export
  exportMenuItems: MenuProps["items"];
  onExportGraph: (format: GraphExportFormat) => void;

  // Refs
  reactFlowInstance: ReactFlowInstance | null;
  onReactFlowInit: (instance: ReactFlowInstance) => void;
  flowCanvasRef: React.RefObject<HTMLDivElement | null>;

  // Layer actions (passed through for deleteGraphLayerFromPanel)
  onDeleteLayer: (layerId: string) => void;
  onMoveLayer: (layerId: string, deltaX: number, deltaY: number, excludeNodeId?: string) => void;
}

export function FlowCanvas(props: FlowCanvasProps) {
  return (
    <div className="flow-canvas" ref={props.flowCanvasRef}>
      <FlowStyleToolbar
        collapsed={props.toolbarCollapsed}
        onCollapsedChange={props.onToolbarCollapsedChange}
        edgeLabelMode={props.edgeLabelMode}
        onEdgeLabelModeChange={props.onEdgeLabelModeChange}
        lineType={props.lineType}
        onLineTypeChange={props.onLineTypeChange}
        arrowMode={props.arrowMode}
        onArrowModeChange={props.onArrowModeChange}
        optimizeAnchors={props.optimizeAnchors}
        onOptimizeAnchorsChange={props.onOptimizeAnchorsChange}
        lineColor={props.lineColor}
        onLineColorChange={props.onLineColorChange}
        lineWidth={props.lineWidth}
        onLineWidthChange={props.onLineWidthChange}
        timeWindow={props.timeWindow}
        onTimeWindowChange={props.onTimeWindowChange}
        renderLimit={props.renderLimit}
        onRenderLimitChange={props.onRenderLimitChange}
      />
      <div className="graph-canvas-actions">
        <Button type="primary" icon={<PlusOutlined />} onClick={props.onAddNode}>
          新建主体
        </Button>
        <Dropdown
          trigger={["click"]}
          menu={{
            items: props.exportMenuItems,
            onClick: ({ key }) => {
              void props.onExportGraph(key as GraphExportFormat);
            },
          }}
        >
          <Button icon={<DownloadOutlined />} disabled={!props.visibleGraph.nodes.length}>
            导出图谱
          </Button>
        </Dropdown>
      </div>
      <ReactFlow
        fitView
        minZoom={0.02}
        maxZoom={4}
        nodes={props.visibleGraph.nodes}
        edges={props.visibleGraph.edges}
        nodeTypes={flowNodeTypes}
        edgeTypes={flowEdgeTypes}
        selectionOnDrag
        selectionMode={SelectionMode.Partial}
        panOnDrag={[1, 2]}
        nodesDraggable
        selectNodesOnDrag={false}
        elevateEdgesOnSelect
        onInit={props.onReactFlowInit}
        onNodesChange={props.onNodesChange}
        onEdgesChange={props.onEdgesChange}
        onConnect={props.onConnect}
        onNodeClick={props.onNodeClick}
        onNodeDragStart={props.onNodeDragStart}
        onNodeDrag={props.onNodeDrag}
        onNodeDragStop={() => {
          layerDragRef.current = null;
        }}
        onEdgeClick={props.onEdgeClick}
        onPaneClick={() => props.onSelectedEdgeIdsChange([])}
      >
        <Controls />
        {!!props.visibleGraph.nodes.length && (
          <>
            {!props.miniMapCollapsed && (
              <MiniMap
                className="flow-minimap"
                position="bottom-right"
                pannable
                zoomable
                maskColor="transparent"
                nodeBorderRadius={3}
                nodeStrokeWidth={2}
                nodeColor={(node) => miniMapNodeColor(String(node.data?.entityKind ?? "unknown"))}
                nodeStrokeColor={(node) => miniMapNodeStrokeColor(String(node.data?.entityKind ?? "unknown"))}
              />
            )}
            <button
              className={"minimap-toggle " + (props.miniMapCollapsed ? "collapsed" : "")}
              type="button"
              onClick={() => props.onMiniMapCollapsedChange(!props.miniMapCollapsed)}
              aria-label={props.miniMapCollapsed ? "展开小地图" : "折叠小地图"}
            >
              {props.miniMapCollapsed ? "\u25c9" : "\u25ce"}
            </button>
          </>
        )}
      </ReactFlow>
      <FlowLayerPanel
        layers={props.graphLayers}
        collapsed={props.graphLayerPanelCollapsed}
        selectedLayerIds={props.selectedGraphLayerIds}
        onCollapsedChange={props.onGraphLayerPanelCollapsedChange}
        onClearSelection={() => props.onSelectedGraphLayerIdsChange([])}
        onToggleSelection={props.onToggleGraphLayerSelection}
        onCenterLayer={props.onCenterGraphLayer}
        onDeleteLayer={props.onDeleteGraphLayerFromPanel}
      />
      {props.selectedEdges.length > 0 && (
        <EdgeStylePanel
          edges={props.selectedEdges}
          defaultLineWidth={props.lineWidth}
          defaultLineColor={props.lineColor}
          defaultArrowMode={props.arrowMode}
          onUpdate={props.onUpdateSelectedEdges}
          onRecalculateDateRange={(range) => props.selectedEdges[0] && props.onRecalculateEdgeByDate(props.selectedEdges[0], range)}
          onOpenDetail={() => props.selectedEdges[0] && props.onOpenEdgeDetail(props.selectedEdges[0])}
          onDelete={props.onDeleteSelectedEdges}
          onClose={() => props.onSelectedEdgeIdsChange([])}
        />
      )}
      <EdgeDetailModal
        open={props.edgeDetailOpen}
        loading={props.edgeDetailLoading}
        detail={props.edgeDetail}
        search={props.edgeDetailSearch}
        onSearch={props.onEdgeDetailSearchChange}
        onClose={() => props.onEdgeDetailOpenChange(false)}
      />
    </div>
  );
}

const flowNodeTypes = {
  flowEntity: FlowEntityNode,
};

const flowEdgeTypes = {
  directional: DirectionalFlowEdge,
};

const layerDragRef = { current: null as string | null };
