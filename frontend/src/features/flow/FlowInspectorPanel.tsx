import { Collapse, Space } from 'antd';

import { SettingOutlined } from '@ant-design/icons';

import { FlowAnalysisPanel } from './FlowAnalysisPanel';

import { FlowBuildControls } from './FlowBuildControls';

import { FlowFieldFilters } from './FlowFieldFilters';

import { FlowGraphFilters } from './FlowGraphFilters';

import { FlowImportSummary } from './FlowImportSummary';

import { FlowLabelFilters } from './FlowLabelFilters';

import { ResolvedFlowMapping } from './flowMapping';

import type {

  FlowBuildStatus,

  FlowEdgeRow,

  FlowFieldMapping,

  GraphLayer,

  ImportedDataset,

  SourceFilterField,

  SourceFilterPayload,

  SourceFilterState,

  SubjectStat,

  TargetFilterField,

  TargetFilterPayload,

  TargetFilterState,

} from './flowTypes';



export interface FlowInspectorPanelProps {

  importedDataset: ImportedDataset | null;

  visibleNodeCount: number;

  totalNodes: number;

  onSelectSource: () => void;

  onOpenMapping: () => void;

  datasetSessionId: string;

  columns: string[];

  effectiveMapping: ResolvedFlowMapping;

  sourceFilters: SourceFilterState[];

  sourceValueOptionsByField: Record<string, Array<{ label: string; value: string }>>;

  onAddSourceFilter: (field?: SourceFilterField) => void;

  onLoadSourceFilterValues: (field: SourceFilterField, search?: string) => void;

  onUpdateSourceFilterValues: (field: SourceFilterField, values: string[]) => void;

  onRemoveSourceFilter: (field: SourceFilterField) => void;

  targetFilters: TargetFilterState[];

  targetValueOptionsByField: Record<string, Array<{ label: string; value: string }>>;

  onAddTargetFilter: (field?: TargetFilterField) => void;

  onLoadTargetFilterValues: (field: TargetFilterField, search?: string) => void;

  onUpdateTargetFilterValues: (field: TargetFilterField, values: string[]) => void;

  onRemoveTargetFilter: (field: TargetFilterField) => void;

  directionValues: string[];

  onDirectionValuesChange: (values: string[]) => void;

  dateRange: any;

  onDateRangeChange: (range: any) => void;

  appendGraph: boolean;

  onAppendGraphChange: (append: boolean) => void;

  canAppend: boolean;

  loading: boolean;

  filterPayload: Record<string, unknown>;

  buildStatus: FlowBuildStatus;

  onBuildFilteredGraph: (values: Record<string, unknown> & { source_column?: string; target_column?: string; amount_column?: string; time_column?: string; direction_column?: string }) => Promise<void>;

  sourceLabelColumn: string | undefined;

  sourceLabelValues: string[];

  sourceLabelOptions: Array<{ label: string; value: string }>;

  onLoadSourceLabelValues: (search?: string) => void;

  onSourceLabelValuesChange: (values: string[]) => void;

  targetLabelColumn: string | undefined;

  targetLabelValues: string[];

  targetLabelOptions: Array<{ label: string; value: string }>;

  onLoadTargetLabelValues: (search?: string) => void;

  onTargetLabelValuesChange: (values: string[]) => void;

  subjectIds: string[];

  subjectOptions: Array<{ label: string; value: string }>;

  onSubjectIdsChange: (ids: string[]) => void;

  minAmount: number;

  maxAmount: number;

  onMinAmountChange: (amount: number) => void;

  pathSource: string | undefined;

  pathTarget: string | undefined;

  pathResult: { nodes: string[]; edges: string[] };

  nodeLabels: Map<string, string>;

  onPathSourceChange: (value: string | undefined) => void;

  onPathTargetChange: (value: string | undefined) => void;

  graphLayers: GraphLayer[];

  onCenterGraphLayer: (layerId: string) => void;

  formatMoney: (value: number) => string;

  prompt: string;

  onPromptChange: (prompt: string) => void;

  onSmartAnalyze: (values: Record<string, unknown> & { prompt: string; source_column?: string; target_column?: string; amount_column?: string; time_column?: string; direction_column?: string }) => Promise<void>;

  analysisReport: string;

  visibleTotal: number;

  strongest: FlowEdgeRow | undefined;

  relationshipRows: FlowEdgeRow[];

  insightItems: Array<{ key: string; title: string; detail: string; subjects: string[] }>;

  subjectStats: SubjectStat[];

  isTruncated: boolean;

  edgeLimit: number;

}



export function FlowInspectorPanel(props: FlowInspectorPanelProps) {

  const {

    importedDataset,

    visibleNodeCount,

    totalNodes,

    onSelectSource,

    onOpenMapping,

    datasetSessionId,

    columns,

    effectiveMapping,

    sourceFilters,

    sourceValueOptionsByField,

    onAddSourceFilter,

    onLoadSourceFilterValues,

    onUpdateSourceFilterValues,

    onRemoveSourceFilter,

    targetFilters,

    targetValueOptionsByField,

    onAddTargetFilter,

    onLoadTargetFilterValues,

    onUpdateTargetFilterValues,

    onRemoveTargetFilter,

    directionValues,

    onDirectionValuesChange,

    dateRange,

    onDateRangeChange,

    appendGraph,

    onAppendGraphChange,

    canAppend,

    loading,

    filterPayload,

    buildStatus,

    onBuildFilteredGraph,

    sourceLabelColumn,

    sourceLabelValues,

    sourceLabelOptions,

    onLoadSourceLabelValues,

    onSourceLabelValuesChange,

    targetLabelColumn,

    targetLabelValues,

    targetLabelOptions,

    onLoadTargetLabelValues,

    onTargetLabelValuesChange,

    subjectIds,

    subjectOptions,

    onSubjectIdsChange,

    minAmount,

    maxAmount,

    onMinAmountChange,

    pathSource,

    pathTarget,

    pathResult,

    nodeLabels,

    onPathSourceChange,

    onPathTargetChange,

    graphLayers,

    onCenterGraphLayer,

    formatMoney,

    prompt,

    onPromptChange,

    onSmartAnalyze,

    analysisReport,

    visibleTotal,

    strongest,

    relationshipRows,

    insightItems,

    subjectStats,

    isTruncated,

    edgeLimit,

  } = props;



  return (

        <aside className="graph-inspector">

          <div className="inspector-header">

            <Space>

              <SettingOutlined />

              <strong>功能</strong>

            </Space>

          </div>

          {isTruncated && (

            <div className="graph-alert">

              后端已保留金额最高的 {String(edgeLimit ?? 0)} 条聚合关系，适合大数据快速研判；全量明细仍在导出文件中。
            </div>

          )}

          <Collapse

            className="inspector-collapse"

            defaultActiveKey={['data']}

            items={[

              {

                key: 'data',

                label: '数据导入',

                children: (

                  <Space direction="vertical" size="middle" className="full">

                    <FlowImportSummary

                      importedDataset={importedDataset}

                      visibleNodes={visibleNodeCount}

                      totalNodes={totalNodes}

                      onSelectSource={onSelectSource}

                      onOpenMapping={onOpenMapping}

                    />

                    <FlowFieldFilters

                      datasetSessionId={datasetSessionId}

                      columns={importedDataset?.columns ?? []}

                      effectiveMapping={effectiveMapping}

                      sourceFilters={sourceFilters}

                      sourceValueOptionsByField={sourceValueOptionsByField}

                      onAddSourceFilter={onAddSourceFilter}

                      onLoadSourceFilterValues={onLoadSourceFilterValues}

                      onUpdateSourceFilterValues={onUpdateSourceFilterValues}

                      onRemoveSourceFilter={onRemoveSourceFilter}

                      targetFilters={targetFilters}

                      targetValueOptionsByField={targetValueOptionsByField}

                      onAddTargetFilter={onAddTargetFilter}

                      onLoadTargetFilterValues={onLoadTargetFilterValues}

                      onUpdateTargetFilterValues={onUpdateTargetFilterValues}

                      onRemoveTargetFilter={onRemoveTargetFilter}

                    />

                    <FlowBuildControls

                      directionValues={directionValues}

                      onDirectionValuesChange={onDirectionValuesChange}

                      dateRange={dateRange}

                      onDateRangeChange={onDateRangeChange}

                      appendGraph={appendGraph}

                      onAppendGraphChange={onAppendGraphChange}

                      canAppend={Boolean(0)}

                      loading={loading}

                      filterPayload={filterPayload}

                      buildStatus={buildStatus}

                      onBuildFilteredGraph={onBuildFilteredGraph}

                    />

                    <FlowLabelFilters

                      sourceLabelColumn={sourceLabelColumn}

                      sourceLabelValues={sourceLabelValues}

                      sourceLabelOptions={sourceLabelOptions}

                      onLoadSourceLabelValues={onLoadSourceLabelValues}

                      onSourceLabelValuesChange={onSourceLabelValuesChange}

                      targetLabelColumn={targetLabelColumn}

                      targetLabelValues={targetLabelValues}

                      targetLabelOptions={targetLabelOptions}

                      onLoadTargetLabelValues={onLoadTargetLabelValues}

                      onTargetLabelValuesChange={onTargetLabelValuesChange}

                    />

                    <FlowGraphFilters

                      subjectIds={subjectIds}

                      subjectOptions={subjectOptions}

                      onSubjectIdsChange={onSubjectIdsChange}

                      minAmount={minAmount}

                      maxAmount={maxAmount}

                      onMinAmountChange={onMinAmountChange}

                      pathSource={pathSource}

                      pathTarget={pathTarget}

                      pathResult={pathResult}

                      nodeLabels={nodeLabels}

                      onPathSourceChange={onPathSourceChange}

                      onPathTargetChange={onPathTargetChange}

                      graphLayers={graphLayers}

                      onCenterGraphLayer={onCenterGraphLayer}

                      formatMoney={formatMoney}

                    />

                  </Space>

                ),

              },

            ]}

          />

          <FlowAnalysisPanel

            prompt={prompt}

            onPromptChange={onPromptChange}

            loading={loading}

            filterPayload={filterPayload}

            onSmartAnalyze={onSmartAnalyze}

            analysisReport={analysisReport}

            visibleTotal={visibleTotal}

            strongest={strongest}

            relationshipRows={relationshipRows}

            insightItems={insightItems}

            subjectStats={subjectStats}

            onSubjectIdsChange={onSubjectIdsChange}

          />        </aside>

  );

}

