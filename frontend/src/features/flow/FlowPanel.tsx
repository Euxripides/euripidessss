import { useEffect, useState } from "react";
import { createPortal } from "react-dom";

import type { MenuProps, UploadFile } from "antd";

import message from "antd/es/message";

import {
  type Edge,
  type Node,
  type OnConnect,
  type OnEdgesChange,
  type OnNodesChange,
} from "@xyflow/react";

import { FlowGraphWorkspace } from "./FlowGraphWorkspace";

import { DBImportModal } from "./DBImportModal";

import { FlowSourceModal } from "./FlowSourceModal";
import { FlowStyleToolbar } from "./FlowStyleToolbar";

import type {
  EdgePatch,
  FlowBuildStatus,
  FlowEdgeRow,
  FlowFieldMapping,
  GraphLayer,
  HistoryItem,
  ImportedDataset,
} from "./flowTypes";

import { useFlowFilters } from "./useFlowFilters";

import { useFlowPanelState } from "./useFlowPanelState";

export function FlowPanel(props: {

  nodes: Node[];

  edges: Edge[];

  meta: Record<string, unknown>;

  graphLayers: GraphLayer[];

  importedDataset: ImportedDataset | null;

  fieldMapping: FlowFieldMapping;

  buildStatus: FlowBuildStatus;

  analysisReport: string;

  loading: boolean;

  onNodesChange: OnNodesChange;

  onEdgesChange: OnEdgesChange;

  onUpdateEdgeText: (edgeId: string, text: string) => void;

  onUpdateEdges: (edgeIds: string[], patch: EdgePatch) => void;

  onDeleteEdges: (edgeIds: string[]) => void;

  onDeleteLayer: (layerId: string) => void;

  onMoveLayer: (layerId: string, deltaX: number, deltaY: number, excludeNodeId?: string) => void;

  onConnect: OnConnect;

  onNodeClick: (event: React.MouseEvent, node: Node) => void;

  onAddNode: () => void;

  onUploadGraph: (files: UploadFile[]) => Promise<void>;

  onImportData: (files: UploadFile[]) => Promise<boolean>;

  onDatabaseImported: (dataset: ImportedDataset) => void;

  onOpenMapping: () => void;

  onBuildFilteredGraph: (values: Record<string, unknown> & { source_column?: string; target_column?: string; amount_column?: string; time_column?: string; direction_column?: string }) => Promise<void>;

  onSmartAnalyze: (values: Record<string, unknown> & { prompt: string; source_column?: string; target_column?: string; amount_column?: string; time_column?: string; direction_column?: string }) => Promise<void>;

  onLoadHistory: (jobId: string) => Promise<void>;

}) {

  const [dbImportOpen, setDbImportOpen] = useState(false);
  const [settingsHost, setSettingsHost] = useState<HTMLElement | null>(null);

  useEffect(() => {
    setSettingsHost(document.getElementById("graph-topbar-settings"));
  }, []);

  const {

    sourceFilters,

    targetFilters,

    detailFilters,

    amountColumn,

    timeColumn,

    directionColumn,

    directionValues,

    setDirectionValues,

    sourceValueOptionsByField,

    targetValueOptionsByField,

    detailValueOptionsByField,

    sourceLabelValues,

    setSourceLabelValues,

    targetLabelValues,

    setTargetLabelValues,

    sourceLabelOptions,

    targetLabelOptions,

    dateRange,

    setDateRange,

    appendGraph,

    setAppendGraph,

    smartPrompt,

    setSmartPrompt,

    datasetSessionId,

    effectiveMapping,

    sourceLabelColumn,

    targetLabelColumn,

    importedColumnOptions,

    filterPayload,

    addSourceFilter,

    removeSourceFilter,

    updateSourceFilterValues,

    loadSourceFilterValues,

    addTargetFilter,

    removeTargetFilter,

    updateTargetFilterValues,

    loadTargetFilterValues,

    addDetailFilter,

    removeDetailFilter,

    updateDetailFilterValues,

    loadDetailFilterValues,

    loadFieldValues,
    setSourceLabelOptions,
    setTargetLabelOptions,


  } = useFlowFilters(props.importedDataset, props.fieldMapping);



  const {

    subjectIds,

    setSubjectIds,

    edgeLabelMode,

    setEdgeLabelMode,

    timeWindow,

    setTimeWindow,

    renderLimit,

    setRenderLimit,

    minAmount,

    setMinAmount,

    pathSource,

    setPathSource,

    pathTarget,

    setPathTarget,

    uploadFiles,

    setUploadFiles,

    historyItems,

    setHistoryItems,

    selectedHistory,

    setSelectedHistory,

    sourceModalOpen,

    setSourceModalOpen,

    inspectorOpen,

    setInspectorOpen,

    lineType,

    setLineType,

    arrowMode,

    setArrowMode,

    lineColor,

    setLineColor,

    lineWidth,

    setLineWidth,

    optimizeAnchors,

    setOptimizeAnchors,

    selectedEdgeIds,

    setSelectedEdgeIds,

    edgeDetailOpen,

    edgeDetailLoading,

    edgeDetail,

    edgeDetailSearch,

    setEdgeDetailSearch,

    selectedGraphLayerIds,

    setSelectedGraphLayerIds,

    graphLayerPanelCollapsed,

    setGraphLayerPanelCollapsed,

    toolbarCollapsed,

    setToolbarCollapsed,

    miniMapCollapsed,

    setMiniMapCollapsed,

    subjectMultiSelect,

    setSubjectMultiSelect,

    dataPenetrationEnabled,

    setDataPenetrationEnabled,

    reactFlowInstance,

    setReactFlowInstance,

    flowCanvasRef,

    maxAmount,

    subjectOptions,

    nodeLabels,

    pathResult,

    visibleGraph,

    relationshipRows,

    subjectStats,

    visibleTotal,

    strongest,

    insightItems,

    selectedEdges,

    totalEdges,

    totalNodes,

    isTruncated,

    exportMenuItems,

    refreshHistory,

    exportCurrentGraph,

    handleEdgeClick,

    handleNodeClick,

    updateSelectedEdges,

    deleteSelectedEdges,

    toggleGraphLayerSelection,

    handleNodeDragStart,

    handleNodeDrag,

    centerGraphLayer,

    deleteGraphLayerFromPanel,

    openEdgeDetail,

    recalculateEdgeByDate,
    layerDragRef,
    setEdgeDetailOpen,

  } = useFlowPanelState({

    nodes: props.nodes,

    edges: props.edges,

    meta: props.meta,

    graphLayers: props.graphLayers,

    onNodeClick: props.onNodeClick,

    onUpdateEdges: props.onUpdateEdges,

    onDeleteEdges: props.onDeleteEdges,

    onMoveLayer: props.onMoveLayer,

    onDeleteLayer: props.onDeleteLayer,

  });



    return (
    <>
      {settingsHost && createPortal(
        <FlowStyleToolbar
          collapsed={false}
          onCollapsedChange={setToolbarCollapsed}
          edgeLabelMode={edgeLabelMode}
          onEdgeLabelModeChange={setEdgeLabelMode}
          lineType={lineType}
          onLineTypeChange={setLineType}
          arrowMode={arrowMode}
          onArrowModeChange={setArrowMode}
          optimizeAnchors={optimizeAnchors}
          onOptimizeAnchorsChange={setOptimizeAnchors}
          lineColor={lineColor}
          onLineColorChange={setLineColor}
          lineWidth={lineWidth}
          onLineWidthChange={setLineWidth}
          timeWindow={timeWindow}
          onTimeWindowChange={setTimeWindow}
          renderLimit={renderLimit}
          onRenderLimitChange={setRenderLimit}
          subjectMultiSelect={subjectMultiSelect}
          onSubjectMultiSelectChange={setSubjectMultiSelect}
          dataPenetrationEnabled={dataPenetrationEnabled}
          onDataPenetrationEnabledChange={setDataPenetrationEnabled}
        />,
        settingsHost,
      )}
      <FlowGraphWorkspace
        inspectorOpen={inspectorOpen}
        onInspectorOpenChange={setInspectorOpen}
        nodes={props.nodes}
        edges={props.edges}
        graphLayers={props.graphLayers}
        meta={props.meta}
        onNodesChange={props.onNodesChange}
        onEdgesChange={props.onEdgesChange}
        onConnect={props.onConnect}
        onNodeClick={props.onNodeClick}
        onAddNode={props.onAddNode}
        visibleGraph={visibleGraph}
        selectedEdges={selectedEdges}
        edgeLabelMode={edgeLabelMode}
        onEdgeLabelModeChange={setEdgeLabelMode}
        lineType={lineType}
        onLineTypeChange={setLineType}
        arrowMode={arrowMode}
        onArrowModeChange={setArrowMode}
        optimizeAnchors={optimizeAnchors}
        onOptimizeAnchorsChange={setOptimizeAnchors}
        lineColor={lineColor}
        onLineColorChange={setLineColor}
        lineWidth={lineWidth}
        onLineWidthChange={setLineWidth}
        timeWindow={timeWindow}
        onTimeWindowChange={setTimeWindow}
        renderLimit={renderLimit}
        onRenderLimitChange={setRenderLimit}
        subjectMultiSelect={subjectMultiSelect}
        toolbarCollapsed={toolbarCollapsed}
        onToolbarCollapsedChange={setToolbarCollapsed}
        miniMapCollapsed={miniMapCollapsed}
        onMiniMapCollapsedChange={setMiniMapCollapsed}
        graphLayerPanelCollapsed={graphLayerPanelCollapsed}
        onGraphLayerPanelCollapsedChange={setGraphLayerPanelCollapsed}
        selectedGraphLayerIds={selectedGraphLayerIds}
        onToggleGraphLayerSelection={toggleGraphLayerSelection}
        onSelectedGraphLayerIdsChange={setSelectedGraphLayerIds}
        onCenterGraphLayer={centerGraphLayer}
        onDeleteGraphLayerFromPanel={deleteGraphLayerFromPanel}
        selectedEdgeIds={selectedEdgeIds}
        onSelectedEdgeIdsChange={setSelectedEdgeIds}
        edgeDetailOpen={edgeDetailOpen}
        onEdgeDetailOpenChange={setEdgeDetailOpen}
        edgeDetail={edgeDetail}
        edgeDetailLoading={edgeDetailLoading}
        edgeDetailSearch={edgeDetailSearch}
        onEdgeDetailSearchChange={setEdgeDetailSearch}
        onUpdateEdges={props.onUpdateEdges}
        onDeleteEdges={props.onDeleteEdges}
        onUpdateSelectedEdges={updateSelectedEdges}
        onDeleteSelectedEdges={deleteSelectedEdges}
        onOpenEdgeDetail={openEdgeDetail}
        onRecalculateEdgeByDate={recalculateEdgeByDate}
        onEdgeClick={handleEdgeClick}
        onNodeDragStart={handleNodeDragStart}
        onNodeDrag={handleNodeDrag}
        exportMenuItems={exportMenuItems}
        onExportGraph={exportCurrentGraph}
        reactFlowInstance={reactFlowInstance}
        onReactFlowInit={setReactFlowInstance}
        flowCanvasRef={flowCanvasRef}
        onDeleteLayer={props.onDeleteLayer}
        onMoveLayer={props.onMoveLayer}
        importedDataset={props.importedDataset}
        totalNodes={totalNodes}
        onSelectSource={() => setSourceModalOpen(true)}
        onOpenMapping={props.onOpenMapping}
        datasetSessionId={datasetSessionId}
        columns={props.importedDataset?.columns ?? []}
        effectiveMapping={effectiveMapping}
        sourceFilters={sourceFilters}
        sourceValueOptionsByField={sourceValueOptionsByField}
        onAddSourceFilter={addSourceFilter}
        onLoadSourceFilterValues={loadSourceFilterValues}
        onUpdateSourceFilterValues={updateSourceFilterValues}
        onRemoveSourceFilter={removeSourceFilter}
        targetFilters={targetFilters}
        targetValueOptionsByField={targetValueOptionsByField}
        onAddTargetFilter={addTargetFilter}
        onLoadTargetFilterValues={loadTargetFilterValues}
        onUpdateTargetFilterValues={updateTargetFilterValues}
        onRemoveTargetFilter={removeTargetFilter}
        detailFilters={detailFilters}
        detailValueOptionsByField={detailValueOptionsByField}
        onAddDetailFilter={addDetailFilter}
        onLoadDetailFilterValues={loadDetailFilterValues}
        onUpdateDetailFilterValues={updateDetailFilterValues}
        onRemoveDetailFilter={removeDetailFilter}
        directionValues={directionValues}
        onDirectionValuesChange={setDirectionValues}
        dateRange={dateRange}
        onDateRangeChange={setDateRange}
        appendGraph={appendGraph}
        onAppendGraphChange={setAppendGraph}
        loading={props.loading}
        filterPayload={filterPayload}
        buildStatus={props.buildStatus}
        onBuildFilteredGraph={props.onBuildFilteredGraph}
        sourceLabelColumn={sourceLabelColumn}
        sourceLabelValues={sourceLabelValues}
        sourceLabelOptions={sourceLabelOptions}
        onLoadSourceLabelValues={(search) => sourceLabelColumn && loadFieldValues(sourceLabelColumn, setSourceLabelOptions, search)}
        onSourceLabelValuesChange={setSourceLabelValues}
        targetLabelColumn={targetLabelColumn}
        targetLabelValues={targetLabelValues}
        targetLabelOptions={targetLabelOptions}
        onLoadTargetLabelValues={(search) => targetLabelColumn && loadFieldValues(targetLabelColumn, setTargetLabelOptions, search)}
        onTargetLabelValuesChange={setTargetLabelValues}
        subjectIds={subjectIds}
        subjectOptions={subjectOptions}
        onSubjectIdsChange={setSubjectIds}
        minAmount={minAmount}
        maxAmount={maxAmount}
        onMinAmountChange={setMinAmount}
        pathSource={pathSource}
        pathTarget={pathTarget}
        pathResult={pathResult}
        onPathSourceChange={setPathSource}
        onPathTargetChange={setPathTarget}
        nodeLabels={nodeLabels}
        prompt={smartPrompt}
        onPromptChange={setSmartPrompt}
        onSmartAnalyze={props.onSmartAnalyze}
        analysisReport={props.analysisReport}
        visibleTotal={visibleTotal}
        strongest={strongest}
        relationshipRows={relationshipRows}
        insightItems={insightItems}
        subjectStats={subjectStats}
        isTruncated={isTruncated}
        edgeLimit={Number(props.meta.edge_limit ?? props.edges.length)}
      />
      <FlowSourceModal
        open={sourceModalOpen}
        loading={props.loading}
        uploadFiles={uploadFiles}
        onUploadFilesChange={setUploadFiles}
        onOpenDatabaseImport={() => setDbImportOpen(true)}
        onImportData={props.onImportData}
        historyItems={historyItems}
        selectedHistory={selectedHistory}
        onSelectedHistoryChange={setSelectedHistory}
        onRefreshHistory={refreshHistory}
        onLoadHistory={props.onLoadHistory}
        onClose={() => setSourceModalOpen(false)}
      />
      <DBImportModal
        open={dbImportOpen}
        onClose={() => setDbImportOpen(false)}
        onImported={props.onDatabaseImported}
      />
    </>
  );
}
