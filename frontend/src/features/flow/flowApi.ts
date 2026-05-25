import { getJson, postJson } from '../../api/client';
import type { EdgeDetailPayload, FlowFieldMapping, GraphDetailContext, HistoryItem } from './flowTypes';

type ErrorDetail = {
  detail?: string;
};

export type UnknownDirectionPayload = {
  detail: {
    code: 'unknown_flow_directions';
    message: string;
    values: string[];
  };
};

export function isUnknownDirectionPayload(payload: any): payload is UnknownDirectionPayload {
  return payload?.detail?.code === 'unknown_flow_directions' && Array.isArray(payload.detail.values);
}

export function saveDirectionRules(aliases: Record<string, '进' | '出'>) {
  return postJson('/api/flow/direction-rules', { aliases }, '保存方向规则失败');
}

export function saveMappingRule(columns: string[], mapping: FlowFieldMapping) {
  return postJson('/api/flow/mapping-rules', { columns, mapping }, '保存字段映射规则失败');
}

export function buildFlowGraph(sessionId: string, values: Record<string, unknown>) {
  return postJson('/api/flow/build', { session_id: sessionId, ...values }, '生成流向图失败');
}

export function runFlowAnalysis(sessionId: string, values: Record<string, unknown>) {
  return postJson('/api/ai/analyze', { session_id: sessionId, ...values }, '智能分析失败');
}

export function loadHistoryGraph(jobId: string) {
  return getJson(`/api/flow/history/${encodeURIComponent(jobId)}`, '历史图谱载入失败');
}

export function loadHistoryItems() {
  return getJson<{ items?: HistoryItem[] }>('/api/flow/history', '读取历史记录失败');
}

export function loadFlowValues(sessionId: string, column: string, search = '') {
  return postJson<ErrorDetail & { values?: string[] }>('/api/flow/values', { session_id: sessionId, column, search, limit: 300 }, '读取筛选值失败');
}

export async function loadUnknownDirectionValues(sessionId: string, column: string) {
  const { response, payload } = await postJson<{ detail?: string; unknown_values?: string[] }>('/api/flow/direction-check', { session_id: sessionId, column }, '检查收付标志失败');
  if (!response.ok) throw new Error(payload.detail || '检查收付标志失败');
  return payload.unknown_values ?? [];
}

export function loadEdgeDetail(context: GraphDetailContext, source: string, target: string) {
  if (context.kind === 'cleaned' && context.jobId) {
    return getJson<ErrorDetail & EdgeDetailPayload>(
      `/api/flow/edge-detail?${new URLSearchParams({
        job_id: context.jobId,
        source,
        target,
        limit: '10000',
      }).toString()}`,
      '读取线条明细失败',
    );
  }
  return postJson<ErrorDetail & EdgeDetailPayload>(
    '/api/flow/edge-detail/imported',
    {
      session_id: context.sessionId,
      source_column: context.sourceColumn,
      source_account_column: context.sourceAccountColumn,
      source_name_column: context.sourceNameColumn,
      source_id_column: context.sourceIdColumn,
      source_label_column: context.sourceLabelColumn,
      target_column: context.targetColumn,
      target_card_column: context.targetCardColumn,
      target_name_column: context.targetNameColumn,
      target_id_column: context.targetIdColumn,
      target_label_column: context.targetLabelColumn,
      serial_column: context.serialColumn,
      summary_column: context.summaryColumn,
      remark_column: context.remarkColumn,
      amount_column: context.amountColumn,
      time_column: context.timeColumn,
      direction_column: context.directionColumn,
      source_filters: context.sourceFilters,
      source_values: context.sourceValues,
      target_filters: context.targetFilters,
      target_values: context.targetValues,
      detail_filters: context.detailFilters,
      directions: context.directionValues,
      start_date: context.startDate,
      end_date: context.endDate,
      source,
      target,
      limit: 10000,
    },
    '读取线条明细失败',
  );
}
