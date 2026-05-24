import { useEffect, useMemo, useRef, useState } from 'react';



import message from 'antd/es/message';



import {



  pickColumn,



  resolveEffectiveFlowMapping,



  resolveSourceFilterRawColumn,



  resolveTargetFilterRawColumn,



} from './flowMapping';



import { loadFlowValues } from './flowApi';



import type {



  FlowFieldMapping,



  ImportedDataset,



  SourceFilterField,



  SourceFilterPayload,



  SourceFilterState,



  TargetFilterField,



  TargetFilterPayload,



  TargetFilterState,



} from './flowTypes';



import {



  SOURCE_FILTER_FIELDS,



  TARGET_FILTER_FIELDS,



} from './flowTypes';



import { normalizeDirectionFilterValues } from './flowAnalysis';







export function useFlowFilters(importedDataset: ImportedDataset | null, fieldMapping: FlowFieldMapping) {



  const [sourceFilters, setSourceFilters] = useState<SourceFilterState[]>([]);



  const [targetFilters, setTargetFilters] = useState<TargetFilterState[]>([]);



  const [amountColumn, setAmountColumn] = useState<string>();



  const [timeColumn, setTimeColumn] = useState<string>();



  const [directionColumn, setDirectionColumn] = useState<string>();



  const [directionValues, setDirectionValues] = useState<string[]>([]);



  const [sourceValueOptionsByField, setSourceValueOptionsByField] = useState<Record<string, Array<{ label: string; value: string }>>>({});



  const [targetValueOptionsByField, setTargetValueOptionsByField] = useState<Record<string, Array<{ label: string; value: string }>>>({});



  const [sourceLabelValues, setSourceLabelValues] = useState<string[]>([]);



  const [targetLabelValues, setTargetLabelValues] = useState<string[]>([]);



  const [sourceLabelOptions, setSourceLabelOptions] = useState<Array<{ label: string; value: string }>>([]);



  const [targetLabelOptions, setTargetLabelOptions] = useState<Array<{ label: string; value: string }>>([]);



  const [dateRange, setDateRange] = useState<any>(null);



  const [appendGraph, setAppendGraph] = useState(false);



  const [smartPrompt, setSmartPrompt] = useState('');







  const datasetSessionId = importedDataset?.session_id ?? '';



  const datasetSessionRef = useRef<string>('');







  useEffect(() => {



    datasetSessionRef.current = datasetSessionId;



  }, [datasetSessionId]);







  async function loadFieldValues(column: string | undefined, setter: (items: Array<{ label: string; value: string }>) => void, search = '') {



    if (!importedDataset || !column) return;



    const sessionId = importedDataset.session_id;



    const { response, payload } = await loadFlowValues(sessionId, column, search);



    if (datasetSessionRef.current !== sessionId) return;



    if (!response.ok) {



      message.error(payload.detail || '读取筛选值失败');



      return;



    }



    setter((payload.values ?? []).map((value: string) => ({ label: value, value })));



  }







  useEffect(() => {



    setSourceValueOptionsByField({});



    setTargetValueOptionsByField({});



    setSourceLabelOptions([]);



    setTargetLabelOptions([]);



    setSourceFilters([]);



    setTargetFilters([]);



    setDirectionValues([]);



    setSourceLabelValues([]);



    setTargetLabelValues([]);



    setDateRange(null);



    if (!importedDataset) {



      setAmountColumn(undefined);



      setTimeColumn(undefined);



      setDirectionColumn(undefined);



      return;



    }



    const columns = importedDataset.columns;



    setAmountColumn(pickColumn(columns, ['交易金额', '金额', 'amount', 'money', '交易额']));



    setTimeColumn(pickColumn(columns, ['交易时间', '时间', '日期', 'time', 'date']));



    setDirectionColumn(pickColumn(columns, ['收付标志', '进出标志', '借贷标志', '借贷方向', '方向', '收支类型', 'direction']));



  }, [datasetSessionId, importedDataset]);







  const effectiveMapping = useMemo(



    () => resolveEffectiveFlowMapping(fieldMapping, importedDataset?.columns ?? []),



    [fieldMapping, importedDataset],



  );



  const sourceLabelColumn = effectiveMapping.source_label_column;



  const targetLabelColumn = effectiveMapping.target_label_column;







  const importedColumnOptions = (importedDataset?.columns ?? []).map((column) => ({ label: column, value: column }));







  const sourceFilterPayload: SourceFilterPayload[] = sourceFilters



    .reduce<SourceFilterPayload[]>((items, filter) => {



      const config = SOURCE_FILTER_FIELDS.find((field) => field.value === filter.field);



      if (config && filter.values.length) items.push({ column: config.normalizedColumn, values: filter.values });



      return items;



    }, []);



  const targetFilterPayload: TargetFilterPayload[] = targetFilters



    .reduce<TargetFilterPayload[]>((items, filter) => {



      const config = TARGET_FILTER_FIELDS.find((field) => field.value === filter.field);



      if (config && filter.values.length) items.push({ column: config.normalizedColumn, values: filter.values });



      return items;



    }, []);







  const filterPayload = {



    source_column: effectiveMapping.source_column,



    source_account_column: effectiveMapping.source_account_column,



    source_name_column: effectiveMapping.source_name_column,



    source_id_column: effectiveMapping.source_id_column,



    source_label_column: sourceLabelColumn,



    target_column: effectiveMapping.target_column,



    target_card_column: effectiveMapping.target_card_column,



    target_name_column: effectiveMapping.target_name_column,



    target_id_column: effectiveMapping.target_id_column,



    target_label_column: targetLabelColumn,



    amount_column: effectiveMapping.amount_column ?? amountColumn,



    time_column: effectiveMapping.time_column ?? timeColumn,



    direction_column: effectiveMapping.direction_column ?? directionColumn,



    source_filters: sourceFilterPayload,



    target_filters: targetFilterPayload,



    source_values: sourceFilterPayload.flatMap((filter) => filter.values),



    target_values: targetFilterPayload.flatMap((filter) => filter.values),



    source_label_values: sourceLabelColumn ? sourceLabelValues : [],



    target_label_values: targetLabelColumn ? targetLabelValues : [],



    directions: normalizeDirectionFilterValues(directionValues),



    start_date: dateRange?.[0]?.format?.('YYYY-MM-DD'),



    end_date: dateRange?.[1]?.format?.('YYYY-MM-DD'),

    max_edges: sourceFilterPayload.length || targetFilterPayload.length ? 5000 : 600,



    append: appendGraph,



  };







  function addSourceFilter(field?: SourceFilterField) {



    if (!field) return;



    setSourceFilters((current) => current.some((item) => item.field === field) ? current : [...current, { field, values: [] }]);



  }







  function removeSourceFilter(field: SourceFilterField) {



    setSourceFilters((current) => current.filter((item) => item.field !== field));



    setSourceValueOptionsByField((current) => {



      const next = { ...current };



      delete next[field];



      return next;



    });



  }







  function updateSourceFilterValues(field: SourceFilterField, values: string[]) {



    setSourceFilters((current) => current.map((item) => item.field === field ? { ...item, values } : item));



  }







  function loadSourceFilterValues(field: SourceFilterField, search = '') {



    const rawColumn = resolveSourceFilterRawColumn(field, effectiveMapping, importedDataset?.columns ?? []);



    if (!rawColumn) {



      message.warning('请先在?字段映射 / 模板说明"里选择对应的交易方字段。');



      return;



    }



    loadFieldValues(rawColumn, (items) => setSourceValueOptionsByField((current) => ({ ...current, [field]: items })), search);



  }







  function addTargetFilter(field?: TargetFilterField) {



    if (!field) return;



    setTargetFilters((current) => current.some((item) => item.field === field) ? current : [...current, { field, values: [] }]);



  }







  function removeTargetFilter(field: TargetFilterField) {



    setTargetFilters((current) => current.filter((item) => item.field !== field));



    setTargetValueOptionsByField((current) => {



      const next = { ...current };



      delete next[field];



      return next;



    });



  }







  function updateTargetFilterValues(field: TargetFilterField, values: string[]) {



    setTargetFilters((current) => current.map((item) => item.field === field ? { ...item, values } : item));



  }







  function loadTargetFilterValues(field: TargetFilterField, search = '') {



    const rawColumn = resolveTargetFilterRawColumn(field, effectiveMapping, importedDataset?.columns ?? []);



    if (!rawColumn) {



      message.warning('请先在?字段映射 / 模板说明"里选择对应的交易对手字段。');



      return;



    }



    loadFieldValues(rawColumn, (items) => setTargetValueOptionsByField((current) => ({ ...current, [field]: items })), search);



  }







  return {


    sourceFilters,



    targetFilters,



    amountColumn,



    timeColumn,



    directionColumn,



    directionValues,



    setDirectionValues,



    sourceValueOptionsByField,



    targetValueOptionsByField,



    sourceLabelValues,



    setSourceLabelValues,



    targetLabelValues,



    setTargetLabelValues,



    sourceLabelOptions,
    setSourceLabelOptions,



    targetLabelOptions,
    setTargetLabelOptions,



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



    loadFieldValues,



  };;



}



