import { Select } from 'antd';

type ValueOption = { label: string; value: string };

export function FlowLabelFilters(props: {
  sourceLabelColumn?: string;
  sourceLabelValues: string[];
  sourceLabelOptions: ValueOption[];
  onLoadSourceLabelValues: (search?: string) => void;
  onSourceLabelValuesChange: (values: string[]) => void;
  targetLabelColumn?: string;
  targetLabelValues: string[];
  targetLabelOptions: ValueOption[];
  onLoadTargetLabelValues: (search?: string) => void;
  onTargetLabelValuesChange: (values: string[]) => void;
}) {
  return (
    <>
      {props.sourceLabelColumn && (
        <Select
          mode="multiple"
          allowClear
          showSearch
          maxTagCount="responsive"
          className="no-wrap-select"
          placeholder="交易方标签筛选（留空为全部）"
          value={props.sourceLabelValues}
          options={[{ label: '全选', value: '__ALL__' }, ...props.sourceLabelOptions]}
          onFocus={() => props.onLoadSourceLabelValues()}
          onSearch={(value) => props.onLoadSourceLabelValues(value)}
          onChange={(values) => props.onSourceLabelValuesChange(resolveAllSelection(values, props.sourceLabelOptions))}
          filterOption={false}
        />
      )}
      {props.targetLabelColumn && (
        <Select
          mode="multiple"
          allowClear
          showSearch
          maxTagCount="responsive"
          className="no-wrap-select"
          placeholder="对手方标签筛选（留空为全部）"
          value={props.targetLabelValues}
          options={[{ label: '全选', value: '__ALL__' }, ...props.targetLabelOptions]}
          onFocus={() => props.onLoadTargetLabelValues()}
          onSearch={(value) => props.onLoadTargetLabelValues(value)}
          onChange={(values) => props.onTargetLabelValuesChange(resolveAllSelection(values, props.targetLabelOptions))}
          filterOption={false}
        />
      )}
    </>
  );
}

function resolveAllSelection(values: string[], options: ValueOption[]) {
  if (!values.includes('__ALL__')) return values;
  return options.map((option) => option.value);
}
