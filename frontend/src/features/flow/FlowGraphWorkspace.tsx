import { RightOutlined } from "@ant-design/icons";
import { type Edge } from "@xyflow/react";
import { FlowCanvas, type FlowCanvasProps } from "./FlowCanvas";
import { FlowInspectorPanel, type FlowInspectorPanelProps } from "./FlowInspectorPanel";

type FlowGraphWorkspaceProps = FlowCanvasProps &
  Omit<FlowInspectorPanelProps, "graphLayers" | "onCenterGraphLayer" | "visibleNodeCount" | "canAppend" | "formatMoney"> & {
    inspectorOpen: boolean;
    onInspectorOpenChange: (open: boolean) => void;
  };

export function FlowGraphWorkspace(props: FlowGraphWorkspaceProps) {
  return (
    <section className={`panel graph-panel ${props.inspectorOpen ? "" : "inspector-collapsed"}`}>
      <button
        className="inspector-edge-toggle"
        type="button"
        aria-label={props.inspectorOpen ? "折叠侧边栏" : "展开侧边栏"}
        onClick={() => props.onInspectorOpenChange(!props.inspectorOpen)}
      >
        <RightOutlined />
      </button>
      <div className="graph-workspace">
        <div className="graph-main">
          <FlowCanvas
            nodes={props.nodes}
            edges={props.edges}
            graphLayers={props.graphLayers}
            meta={props.meta}
            onNodesChange={props.onNodesChange}
            onEdgesChange={props.onEdgesChange}
            onConnect={props.onConnect}
            onNodeClick={props.onNodeClick}
            onAddNode={props.onAddNode}
            visibleGraph={props.visibleGraph}
            selectedEdges={props.selectedEdges}
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
            subjectMultiSelect={props.subjectMultiSelect}
            toolbarCollapsed={props.toolbarCollapsed}
            onToolbarCollapsedChange={props.onToolbarCollapsedChange}
            miniMapCollapsed={props.miniMapCollapsed}
            onMiniMapCollapsedChange={props.onMiniMapCollapsedChange}
            graphLayerPanelCollapsed={props.graphLayerPanelCollapsed}
            onGraphLayerPanelCollapsedChange={props.onGraphLayerPanelCollapsedChange}
            selectedGraphLayerIds={props.selectedGraphLayerIds}
            onToggleGraphLayerSelection={props.onToggleGraphLayerSelection}
            onSelectedGraphLayerIdsChange={props.onSelectedGraphLayerIdsChange}
            onCenterGraphLayer={props.onCenterGraphLayer}
            onDeleteGraphLayerFromPanel={props.onDeleteGraphLayerFromPanel}
            selectedEdgeIds={props.selectedEdgeIds}
            onSelectedEdgeIdsChange={props.onSelectedEdgeIdsChange}
            edgeDetailOpen={props.edgeDetailOpen}
            onEdgeDetailOpenChange={props.onEdgeDetailOpenChange}
            edgeDetail={props.edgeDetail}
            edgeDetailLoading={props.edgeDetailLoading}
            edgeDetailSearch={props.edgeDetailSearch}
            onEdgeDetailSearchChange={props.onEdgeDetailSearchChange}
            onUpdateEdges={props.onUpdateEdges}
            onDeleteEdges={props.onDeleteEdges}
            onUpdateSelectedEdges={props.onUpdateSelectedEdges}
            onDeleteSelectedEdges={props.onDeleteSelectedEdges}
            onOpenEdgeDetail={props.onOpenEdgeDetail}
            onRecalculateEdgeByDate={props.onRecalculateEdgeByDate}
            onEdgeClick={props.onEdgeClick}
            onNodeDragStart={props.onNodeDragStart}
            onNodeDrag={props.onNodeDrag}
            exportMenuItems={props.exportMenuItems}
            onExportGraph={props.onExportGraph}
            reactFlowInstance={props.reactFlowInstance}
            onReactFlowInit={props.onReactFlowInit}
            flowCanvasRef={props.flowCanvasRef}
            onDeleteLayer={props.onDeleteLayer}
            onMoveLayer={props.onMoveLayer}
          />
        </div>
        {props.inspectorOpen && (
          <FlowInspectorPanel
            importedDataset={props.importedDataset}
            visibleNodeCount={props.visibleGraph.nodes.length}
            totalNodes={props.totalNodes}
            onSelectSource={props.onSelectSource}
            onOpenMapping={props.onOpenMapping}
            datasetSessionId={props.datasetSessionId}
            columns={props.columns}
            effectiveMapping={props.effectiveMapping}
            sourceFilters={props.sourceFilters}
            sourceValueOptionsByField={props.sourceValueOptionsByField}
            onAddSourceFilter={props.onAddSourceFilter}
            onLoadSourceFilterValues={props.onLoadSourceFilterValues}
            onUpdateSourceFilterValues={props.onUpdateSourceFilterValues}
            onRemoveSourceFilter={props.onRemoveSourceFilter}
            targetFilters={props.targetFilters}
            targetValueOptionsByField={props.targetValueOptionsByField}
            onAddTargetFilter={props.onAddTargetFilter}
            onLoadTargetFilterValues={props.onLoadTargetFilterValues}
            onUpdateTargetFilterValues={props.onUpdateTargetFilterValues}
            onRemoveTargetFilter={props.onRemoveTargetFilter}
            detailFilters={props.detailFilters}
            detailValueOptionsByField={props.detailValueOptionsByField}
            onAddDetailFilter={props.onAddDetailFilter}
            onLoadDetailFilterValues={props.onLoadDetailFilterValues}
            onUpdateDetailFilterValues={props.onUpdateDetailFilterValues}
            onRemoveDetailFilter={props.onRemoveDetailFilter}
            directionValues={props.directionValues}
            onDirectionValuesChange={props.onDirectionValuesChange}
            dateRange={props.dateRange}
            onDateRangeChange={props.onDateRangeChange}
            appendGraph={props.appendGraph}
            onAppendGraphChange={props.onAppendGraphChange}
            canAppend={props.edges.length > 0}
            loading={props.loading}
            filterPayload={props.filterPayload}
            buildStatus={props.buildStatus}
            onBuildFilteredGraph={props.onBuildFilteredGraph}
            sourceLabelColumn={props.sourceLabelColumn}
            sourceLabelValues={props.sourceLabelValues}
            sourceLabelOptions={props.sourceLabelOptions}
            onLoadSourceLabelValues={props.onLoadSourceLabelValues}
            onSourceLabelValuesChange={props.onSourceLabelValuesChange}
            targetLabelColumn={props.targetLabelColumn}
            targetLabelValues={props.targetLabelValues}
            targetLabelOptions={props.targetLabelOptions}
            onLoadTargetLabelValues={props.onLoadTargetLabelValues}
            onTargetLabelValuesChange={props.onTargetLabelValuesChange}
            subjectIds={props.subjectIds}
            subjectOptions={props.subjectOptions}
            onSubjectIdsChange={props.onSubjectIdsChange}
            minAmount={props.minAmount}
            maxAmount={props.maxAmount}
            onMinAmountChange={props.onMinAmountChange}
            pathSource={props.pathSource}
            pathTarget={props.pathTarget}
            pathResult={props.pathResult}
            onPathSourceChange={props.onPathSourceChange}
            onPathTargetChange={props.onPathTargetChange}
            nodeLabels={props.nodeLabels}
            graphLayers={props.graphLayers}
            onCenterGraphLayer={props.onCenterGraphLayer}
            formatMoney={formatMoney}
            prompt={props.prompt}
            onPromptChange={props.onPromptChange}
            onSmartAnalyze={props.onSmartAnalyze}
            analysisReport={props.analysisReport}
            visibleTotal={props.visibleTotal}
            strongest={props.strongest}
            relationshipRows={props.relationshipRows}
            insightItems={props.insightItems}
            subjectStats={props.subjectStats}
            isTruncated={props.isTruncated}
            edgeLimit={props.edgeLimit}
          />
        )}
      </div>
    </section>
  );
}

function formatMoney(value: number) {
  return Number(value || 0).toLocaleString("zh-CN", { maximumFractionDigits: 0 });
}
