import {
  FLOW_TEMPLATE_COLUMNS,
  FLOW_TEMPLATE_MAPPING,
  SOURCE_FILTER_FIELDS,
  TARGET_FILTER_FIELDS,
  type FlowFieldMapping,
  type SourceFilterField,
  type TargetFilterField,
} from './flowTypes';

export type ResolvedFlowMapping = Record<keyof typeof FLOW_TEMPLATE_MAPPING, string | undefined>;

export function flowTemplateMatches(columns: string[]) {
  const normalized = new Set(columns.map((column) => column.trim()));
  return FLOW_TEMPLATE_COLUMNS.every((column) => normalized.has(column));
}

export function autoFlowMapping(columns: string[]): FlowFieldMapping {
  const mapping: FlowFieldMapping = {};
  for (const [key, templateColumn] of Object.entries(FLOW_TEMPLATE_MAPPING) as Array<[keyof typeof FLOW_TEMPLATE_MAPPING, string]>) {
    const exact = columns.find((column) => column.trim() === templateColumn);
    if (exact) {
      mapping[key] = exact;
      continue;
    }
    const picked = pickColumn(columns, [templateColumn, templateColumn.replace(/^交易方/, '').replace(/^对手/, '').replace(/^交易对手/, '')]);
    if (picked) mapping[key] = picked;
  }
  for (const field of SOURCE_FILTER_FIELDS) {
    if (!mapping[field.value]) {
      const picked = pickColumn(columns, [...field.keywords]);
      if (picked) mapping[field.value] = picked;
    }
  }
  for (const field of TARGET_FILTER_FIELDS) {
    if (!mapping[field.value]) {
      const picked = pickColumn(columns, [...field.keywords]);
      if (picked) mapping[field.value] = picked;
    }
  }
  return mapping;
}

export function sanitizeFlowMapping(mapping: FlowFieldMapping, columns: string[]): FlowFieldMapping {
  const available = new Set(columns);
  const clean: FlowFieldMapping = {};
  for (const [key, value] of Object.entries(mapping) as Array<[keyof typeof FLOW_TEMPLATE_MAPPING, string | undefined]>) {
    if (value && available.has(value)) clean[key] = value;
  }
  return clean;
}

export function resolveEffectiveFlowMapping(mapping: FlowFieldMapping, columns: string[]): ResolvedFlowMapping {
  const auto = autoFlowMapping(columns);
  const merged = { ...auto, ...mapping } as FlowFieldMapping;
  const sourceColumn = merged.source_column || merged.source_name_column || merged.source_account_column || merged.source_id_column;
  const targetColumn = merged.target_column || merged.target_name_column || merged.target_card_column || merged.target_id_column;
  return {
    ...merged,
    source_column: sourceColumn,
    target_column: targetColumn,
  } as ResolvedFlowMapping;
}

export function resolveSourceFilterRawColumn(field: SourceFilterField, mapping: ResolvedFlowMapping, columns: string[]) {
  const mapped = mapping[field];
  if (mapped && columns.includes(mapped)) return mapped;
  const config = SOURCE_FILTER_FIELDS.find((item) => item.value === field);
  return config ? pickColumn(columns, [...config.keywords]) : undefined;
}

export function resolveTargetFilterRawColumn(field: TargetFilterField, mapping: ResolvedFlowMapping, columns: string[]) {
  const mapped = mapping[field];
  if (mapped && columns.includes(mapped)) return mapped;
  const config = TARGET_FILTER_FIELDS.find((item) => item.value === field);
  return config ? pickColumn(columns, [...config.keywords]) : undefined;
}

export function requiredFlowMappingMissing(mapping: FlowFieldMapping) {
  return [
    !mapping.source_column ? '交易方字段' : '',
    !mapping.target_column ? '交易对手字段' : '',
    !mapping.amount_column ? '交易金额字段' : '',
  ].filter(Boolean);
}

export function pickColumn(columns: string[], keywords: string[]) {
  return columns.find((column) => keywords.some((keyword) => column.toLowerCase().includes(keyword.toLowerCase())));
}
