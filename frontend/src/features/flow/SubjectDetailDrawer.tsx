import { DeleteOutlined, LinkOutlined, PlusOutlined, TagsOutlined } from '@ant-design/icons';
import { Button, Checkbox, Form, Input, InputNumber, Select, Space, Tabs, Tag } from 'antd';
import type { FormInstance } from 'antd';
import type { Node } from '@xyflow/react';
import {
  ENTITY_KIND_OPTIONS,
  type EntityKind,
  type NodeConnectionFormValues,
  type SubjectDetailStats,
} from './flowTypes';

export function SubjectDetailDrawer(props: {
  node: Node;
  stats: SubjectDetailStats;
  connectionOptions: Array<{ label: string; value: string }>;
  tagInput: string;
  setTagInput: (value: string) => void;
  onSaveTag: () => void;
  featureInput: string;
  setFeatureInput: (value: string) => void;
  onSaveFeature: () => void;
  onDeleteFeature: (feature: string) => void;
  onDelete: () => void;
  onKindChange: (kind: EntityKind) => void;
  connectionForm: FormInstance<NodeConnectionFormValues>;
  outgoingEnabled?: boolean;
  incomingEnabled?: boolean;
  onCreateConnections: (values: NodeConnectionFormValues) => void;
}) {
  const label = String(props.node.data.entityLabel ?? props.node.id);
  const rawId = String(props.node.data.rawEntityId ?? props.node.data.rawEntityLabel ?? props.node.id);
  const layerId = props.node.data?.graphLayerId;
  const layerLabel = String(props.node.data?.graphLayerLabel ?? (layerId ? '当前画布' : '手工画布'));
  const stats = props.stats;

  return (
    <Space direction="vertical" size="middle" className="full subject-detail">
      <div className="subject-identity">
        <div className="subject-row">
          <span>主体</span>
          <strong>{label}</strong>
        </div>
        <div className="subject-row">
          <span>ID</span>
          <code>{rawId}</code>
        </div>
        <div className="subject-row">
          <span>画布</span>
          <Tag color="blue">{layerLabel}</Tag>
        </div>
        <div className="subject-row">
          <span>类型</span>
          <Select<EntityKind>
            className="subject-kind-select"
            value={(props.node.data.entityKind as EntityKind | undefined) ?? 'unknown'}
            options={ENTITY_KIND_OPTIONS}
            onChange={props.onKindChange}
          />
        </div>
        <div className="subject-row">
          <span>标签</span>
          <div className="subject-tags">
            {((props.node.data.tags as string[] | undefined) ?? []).map((tag) => <Tag color="green" key={tag}>{tag}</Tag>)}
            {!((props.node.data.tags as string[] | undefined) ?? []).length && <em>--</em>}
          </div>
        </div>
        <Space.Compact className="full">
          <Input value={props.tagInput} onChange={(event) => props.setTagInput(event.target.value)} placeholder="标注，如：嫌疑账户、卡商、商户" />
          <Button icon={<TagsOutlined />} onClick={props.onSaveTag}>添加</Button>
        </Space.Compact>
      </div>

      <Tabs
        className="subject-tabs"
        items={[
          {
            key: 'summary',
            label: '交易概要',
            children: (
              <div className="subject-summary">
                <div className="subject-time-grid">
                  <span>首次交易时间</span><strong>{stats.firstTime || '--'}</strong>
                  <span>最近交易时间</span><strong>{stats.lastTime || '--'}</strong>
                  <span>最近流出时间</span><strong>{stats.lastOutTime || '--'}</strong>
                </div>
                <SubjectMetricBar leftLabel="流入金额" rightLabel="流出金额" left={stats.amountIn} right={stats.amountOut} money />
                <SubjectMetricBar leftLabel="流入笔数" rightLabel="流出笔数" left={stats.inCount} right={stats.outCount} />
                <SubjectMetricBar leftLabel="流入对象数" rightLabel="流出对象数" left={stats.inPeers} right={stats.outPeers} />
                <div className="subject-feature-box">
                  <span>交易特征</span>
                  <div>
                    {stats.features.map((item) => (
                      <Tag key={item} color={stats.manualFeatures.includes(item) ? 'green' : 'purple'} closable={stats.manualFeatures.includes(item)} onClose={() => props.onDeleteFeature(item)}>
                        {item}
                      </Tag>
                    ))}
                  </div>
                  <Space.Compact className="full">
                    <Input value={props.featureInput} onChange={(event) => props.setFeatureInput(event.target.value)} placeholder="手动添加交易特征" />
                    <Button icon={<PlusOutlined />} onClick={props.onSaveFeature}>添加</Button>
                  </Space.Compact>
                </div>
              </div>
            ),
          },
          {
            key: 'connect',
            label: '连接管理',
            children: (
              <Form<NodeConnectionFormValues>
                form={props.connectionForm}
                layout="vertical"
                initialValues={{
                  lineStyle: 'solid',
                  lineWidth: 1.2,
                  outgoingEnabled: false,
                  incomingEnabled: false,
                  outgoingLinks: [{}],
                  incomingLinks: [{}],
                }}
                onFinish={props.onCreateConnections}
              >
                <div className="manual-edge-options">
                  <Form.Item name="lineStyle" label="线条样式">
                    <Select options={[{ label: '实线', value: 'solid' }, { label: '虚线', value: 'dashed' }]} />
                  </Form.Item>
                  <Form.Item name="lineWidth" label="线条磅数">
                    <InputNumber className="full" min={0.5} max={8} step={0.5} precision={1} />
                  </Form.Item>
                </div>
                <Form.Item name="outgoingEnabled" valuePropName="checked">
                  <Checkbox>添加连接：{label} → 其他主体</Checkbox>
                </Form.Item>
                {props.outgoingEnabled && (
                  <SubjectConnectionList name="outgoingLinks" options={props.connectionOptions} placeholder={`${label} → 选择主体`} buttonText="添加连接对象" />
                )}
                <Form.Item name="incomingEnabled" valuePropName="checked">
                  <Checkbox>添加被连接：其他主体 → {label}</Checkbox>
                </Form.Item>
                {props.incomingEnabled && (
                  <SubjectConnectionList name="incomingLinks" options={props.connectionOptions} placeholder={`选择主体 → ${label}`} buttonText="添加被链接对象" />
                )}
                {!props.connectionOptions.length && <div className="empty-mini">当前画布没有其他主体可连接。</div>}
                <Button block type="primary" icon={<LinkOutlined />} htmlType="submit" disabled={!props.connectionOptions.length}>
                  保存连接
                </Button>
              </Form>
            ),
          },
        ]}
      />

      <Button danger block icon={<DeleteOutlined />} onClick={props.onDelete}>
        删除该主体
      </Button>
    </Space>
  );
}

function SubjectConnectionList(props: { name: 'outgoingLinks' | 'incomingLinks'; options: Array<{ label: string; value: string }>; placeholder: string; buttonText: string }) {
  return (
    <Form.List name={props.name}>
      {(fields, { add, remove }) => (
        <div className="manual-link-list compact">
          {fields.map((field) => (
            <Space key={field.key} align="start" className="manual-link-row">
              <Form.Item {...field} name={[field.name, 'nodeId']} rules={[{ required: true, message: '请选择对象' }]}>
                <Select showSearch optionFilterProp="label" placeholder={props.placeholder} options={props.options} />
              </Form.Item>
              <Form.Item {...field} name={[field.name, 'amount']}>
                <InputNumber min={0} precision={2} placeholder="金额" addonBefore={<LinkOutlined />} />
              </Form.Item>
              <Form.Item {...field} name={[field.name, 'count']}>
                <InputNumber min={0} precision={0} placeholder="笔数" />
              </Form.Item>
              <Button danger icon={<DeleteOutlined />} onClick={() => remove(field.name)} />
            </Space>
          ))}
          <Button block icon={<PlusOutlined />} onClick={() => add({})}>{props.buttonText}</Button>
        </div>
      )}
    </Form.List>
  );
}

function SubjectMetricBar(props: { leftLabel: string; rightLabel: string; left: number; right: number; money?: boolean }) {
  const total = props.left + props.right;
  const leftPercent = total ? (props.left / total) * 100 : 50;
  return (
    <div className="subject-metric">
      <div className="subject-meter">
        <span style={{ width: `${leftPercent}%` }} />
        <b />
      </div>
      <div className="subject-metric-values">
        <div><span>{props.leftLabel}</span><strong>{props.money ? formatMoney(props.left) : props.left}</strong></div>
        <div><span>{props.rightLabel}</span><strong>{props.money ? formatMoney(props.right) : props.right}</strong></div>
      </div>
    </div>
  );
}

function formatMoney(value: number) {
  return Number(value || 0).toLocaleString('zh-CN', { maximumFractionDigits: 0 });
}
