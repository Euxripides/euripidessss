import { DeleteOutlined } from '@ant-design/icons';
import { Button, Select } from 'antd';
import {
  DETAIL_FILTER_FIELDS,
  SOURCE_FILTER_FIELDS,
  TARGET_FILTER_FIELDS,
  type DetailFilterField,
  type DetailFilterState,
  type SourceFilterField,
  type SourceFilterState,
  type TargetFilterField,
  type TargetFilterState,
} from './flowTypes';
import {
  resolveDetailFilterRawColumn,
  resolveSourceFilterRawColumn,
  resolveTargetFilterRawColumn,
  type ResolvedFlowMapping,
} from './flowMapping';

type ValueOption = { label: string; value: string };

export function FlowFieldFilters(props: {
  datasetSessionId: string;
  columns: string[];
  effectiveMapping: ResolvedFlowMapping;
  sourceFilters: SourceFilterState[];
  sourceValueOptionsByField: Record<string, ValueOption[]>;
  onAddSourceFilter: (field?: SourceFilterField) => void;
  onLoadSourceFilterValues: (field: SourceFilterField, search?: string) => void;
  onUpdateSourceFilterValues: (field: SourceFilterField, values: string[]) => void;
  onRemoveSourceFilter: (field: SourceFilterField) => void;
  targetFilters: TargetFilterState[];
  targetValueOptionsByField: Record<string, ValueOption[]>;
  onAddTargetFilter: (field?: TargetFilterField) => void;
  onLoadTargetFilterValues: (field: TargetFilterField, search?: string) => void;
  onUpdateTargetFilterValues: (field: TargetFilterField, values: string[]) => void;
  onRemoveTargetFilter: (field: TargetFilterField) => void;
  detailFilters: DetailFilterState[];
  detailValueOptionsByField: Record<string, ValueOption[]>;
  onAddDetailFilter: (field?: DetailFilterField) => void;
  onLoadDetailFilterValues: (field: DetailFilterField, search?: string) => void;
  onUpdateDetailFilterValues: (field: DetailFilterField, values: string[]) => void;
  onRemoveDetailFilter: (field: DetailFilterField) => void;
}) {
  const activeSourceFields = new Set(props.sourceFilters.map((filter) => filter.field));
  const availableSourceFields = SOURCE_FILTER_FIELDS.filter((field) => !activeSourceFields.has(field.value));
  const activeTargetFields = new Set(props.targetFilters.map((filter) => filter.field));
  const availableTargetFields = TARGET_FILTER_FIELDS.filter((field) => !activeTargetFields.has(field.value));
  const activeDetailFields = new Set(props.detailFilters.map((filter) => filter.field));
  const availableDetailFields = DETAIL_FILTER_FIELDS.filter((field) => {
    if (activeDetailFields.has(field.value)) return false;
    return Boolean(resolveDetailFilterRawColumn(field.value, props.effectiveMapping, props.columns));
  });

  return (
    <>
      <div className="multi-field-filter">
        {!!availableSourceFields.length && (
          <Select
            allowClear
            placeholder="请选择交易方筛选字段"
            value={undefined}
            options={availableSourceFields}
            onChange={(value) => value && props.onAddSourceFilter(value as SourceFilterField)}
          />
        )}
        {props.sourceFilters.map((filter) => {
          const config = SOURCE_FILTER_FIELDS.find((field) => field.value === filter.field);
          const options = props.sourceValueOptionsByField[filter.field] ?? [];
          const rawColumn = resolveSourceFilterRawColumn(filter.field, props.effectiveMapping, props.columns);
          return config ? (
            <div key={filter.field} className="filter-row">
              <span>{config.label}</span>
              <Select
                key={`source-values-${props.datasetSessionId}-${filter.field}`}
                mode="multiple"
                allowClear
                showSearch
                maxTagCount="responsive"
                className="no-wrap-select"
                placeholder={`${config.label}筛选（留空为全部）`}
                value={filter.values}
                options={options.length ? [{ label: '全选', value: '__ALL__' }, ...options] : options}
                notFoundContent={rawColumn ? '暂无待选项' : '请先完成字段映射'}
                onFocus={() => props.onLoadSourceFilterValues(filter.field)}
                onSearch={(value) => props.onLoadSourceFilterValues(filter.field, value)}
                onChange={(values) => props.onUpdateSourceFilterValues(filter.field, resolveAllSelection(values, options))}
                filterOption={false}
              />
              <Button danger size="small" icon={<DeleteOutlined />} onClick={() => props.onRemoveSourceFilter(filter.field)} />
            </div>
          ) : null;
        })}
      </div>
      <div className="multi-field-filter">
        {!!availableTargetFields.length && (
          <Select
            allowClear
            placeholder="请选择交易对手筛选字段"
            value={undefined}
            options={availableTargetFields}
            onChange={(value) => value && props.onAddTargetFilter(value as TargetFilterField)}
          />
        )}
        {props.targetFilters.map((filter) => {
          const config = TARGET_FILTER_FIELDS.find((field) => field.value === filter.field);
          const options = props.targetValueOptionsByField[filter.field] ?? [];
          const rawColumn = resolveTargetFilterRawColumn(filter.field, props.effectiveMapping, props.columns);
          return config ? (
            <div key={filter.field} className="filter-row">
              <span>{config.label}</span>
              <Select
                key={`target-values-${props.datasetSessionId}-${filter.field}`}
                mode="multiple"
                allowClear
                showSearch
                maxTagCount="responsive"
                className="no-wrap-select"
                placeholder={`${config.label}筛选（留空为全部）`}
                value={filter.values}
                options={options.length ? [{ label: '全选', value: '__ALL__' }, ...options] : options}
                notFoundContent={rawColumn ? '暂无待选项' : '请先完成字段映射'}
                onFocus={() => props.onLoadTargetFilterValues(filter.field)}
                onSearch={(value) => props.onLoadTargetFilterValues(filter.field, value)}
                onChange={(values) => props.onUpdateTargetFilterValues(filter.field, resolveAllSelection(values, options))}
                filterOption={false}
              />
              <Button danger size="small" icon={<DeleteOutlined />} onClick={() => props.onRemoveTargetFilter(filter.field)} />
            </div>
          ) : null;
        })}
      </div>
      <div className="multi-field-filter">
        {!!availableDetailFields.length && (
          <Select
            allowClear
            placeholder="请选择明细筛选字段"
            value={undefined}
            options={availableDetailFields}
            onChange={(value) => value && props.onAddDetailFilter(value as DetailFilterField)}
          />
        )}
        {props.detailFilters.map((filter) => {
          const config = DETAIL_FILTER_FIELDS.find((field) => field.value === filter.field);
          const options = props.detailValueOptionsByField[filter.field] ?? [];
          const rawColumn = resolveDetailFilterRawColumn(filter.field, props.effectiveMapping, props.columns);
          return config && rawColumn ? (
            <div key={filter.field} className="filter-row">
              <span>{config.label}</span>
              <Select
                key={`detail-values-${props.datasetSessionId}-${filter.field}`}
                mode="multiple"
                allowClear
                showSearch
                maxTagCount="responsive"
                className="no-wrap-select"
                placeholder={`${config.label}筛选（留空为全部）`}
                value={filter.values}
                options={options.length ? [{ label: '全选', value: '__ALL__' }, ...options] : options}
                notFoundContent="暂无待选项"
                onFocus={() => props.onLoadDetailFilterValues(filter.field)}
                onSearch={(value) => props.onLoadDetailFilterValues(filter.field, value)}
                onChange={(values) => props.onUpdateDetailFilterValues(filter.field, resolveAllSelection(values, options))}
                filterOption={false}
              />
              <Button danger size="small" icon={<DeleteOutlined />} onClick={() => props.onRemoveDetailFilter(filter.field)} />
            </div>
          ) : null;
        })}
      </div>
    </>
  );
}

function resolveAllSelection(values: string[], options: ValueOption[]) {
  if (!values.includes('__ALL__')) return values;
  return options.map((option) => option.value);
}
