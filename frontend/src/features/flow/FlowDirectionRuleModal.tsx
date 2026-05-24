import { Modal, Select, Space, Tag } from 'antd';
import type { DirectionRulePending } from './flowTypes';

type DirectionValue = '进' | '出';

export function FlowDirectionRuleModal(props: {
  pending: DirectionRulePending | null;
  values: Record<string, DirectionValue>;
  loading: boolean;
  onChange: (values: Record<string, DirectionValue>) => void;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <Modal
      title="确认收付标志规则"
      open={Boolean(props.pending)}
      okText={props.pending?.source === 'mapping' ? '确认' : '写入规则库并重试'}
      cancelText="取消"
      confirmLoading={props.loading}
      onOk={props.onConfirm}
      onCancel={props.onCancel}
    >
      <Space direction="vertical" className="full">
        <div>发现新的收付标志写法，请确认每个值属于进账还是出账。</div>
        {(props.pending?.values ?? []).map((value) => (
          <Space key={value} className="full" align="center">
            <Tag>{value}</Tag>
            <Select<DirectionValue>
              value={props.values[value] ?? '出'}
              options={[
                { label: '进', value: '进' },
                { label: '出', value: '出' },
              ]}
              onChange={(direction) => props.onChange({ ...props.values, [value]: direction })}
            />
          </Space>
        ))}
      </Space>
    </Modal>
  );
}
