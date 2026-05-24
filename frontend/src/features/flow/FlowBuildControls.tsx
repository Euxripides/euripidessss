import { Button, Checkbox, DatePicker, Select } from 'antd';
import { DIRECTION_OPTIONS, type FlowBuildStatus } from './flowTypes';

type FlowBuildPayload = Record<string, unknown> & {
  source_column?: string;
  target_column?: string;
  amount_column?: string;
  time_column?: string;
  direction_column?: string;
};

export function FlowBuildControls(props: {
  directionValues: string[];
  onDirectionValuesChange: (values: string[]) => void;
  dateRange: any;
  onDateRangeChange: (value: any) => void;
  appendGraph: boolean;
  onAppendGraphChange: (value: boolean) => void;
  canAppend: boolean;
  loading: boolean;
  filterPayload: FlowBuildPayload;
  buildStatus: FlowBuildStatus;
  onBuildFilteredGraph: (values: FlowBuildPayload) => Promise<void>;
}) {
  return (
    <>
      <div className="combo-filter">
        <span className="combo-label">进出标志</span>
        <Select
          mode="multiple"
          allowClear
          placeholder="进/出"
          value={props.directionValues}
          options={DIRECTION_OPTIONS}
          onChange={props.onDirectionValuesChange}
        />
      </div>
      <DatePicker.RangePicker className="full" value={props.dateRange} onChange={props.onDateRangeChange} />
      <Checkbox checked={props.appendGraph} disabled={!props.canAppend} onChange={(event) => props.onAppendGraphChange(event.target.checked)}>
        追加到当前画布
      </Checkbox>
      <Button type="primary" loading={props.loading} onClick={() => props.onBuildFilteredGraph(props.filterPayload)}>
        {props.loading ? '正在生成图...' : props.appendGraph ? '计算并追加图' : '计算并生成图'}
      </Button>
      {props.buildStatus.visible && (
        <div className={`build-status status-${props.buildStatus.status}`}>
          {props.buildStatus.text}
        </div>
      )}
    </>
  );
}
