import { useState } from 'react';

import {

  Modal,

  Form,

  Input,

  Select,

  InputNumber,

  Checkbox,

  Button,

  Space,

} from 'antd';

import {

  LinkOutlined,

  DeleteOutlined,

  PlusOutlined,

} from '@ant-design/icons';

import type { Node } from '@xyflow/react';

import { nodeSelectOptions } from './flowSubject';

import type {

  EntityKind,

  ManualNodeFormValues,

} from './flowTypes';

import { ENTITY_KIND_OPTIONS } from './flowTypes';



interface FlowAddNodeModalProps {

  open: boolean;

  onClose: () => void;

  nodes: Node[];

  onFinish: (values: ManualNodeFormValues) => void;

}



export function FlowAddNodeModal({ open, onClose, nodes, onFinish }: FlowAddNodeModalProps) {

  const [form] = Form.useForm<ManualNodeFormValues>();

  const [outgoingEnabled, setOutgoingEnabled] = useState(false);

  const [incomingEnabled, setIncomingEnabled] = useState(false);



  function handleCancel() {

    form.resetFields();

    setOutgoingEnabled(false);

    setIncomingEnabled(false);

    onClose();

  }



  function handleFinish(values: ManualNodeFormValues) {

    onFinish(values);

    form.resetFields();

    setOutgoingEnabled(false);

    setIncomingEnabled(false);

  }



  return (

    <Modal

      title="新增主体"

      open={open}

      onCancel={handleCancel}

      onOk={() => form.submit()}

      okText="添加主体"

      cancelText="取消"

      afterClose={() => {

        form.resetFields();

        setOutgoingEnabled(false);

        setIncomingEnabled(false);

      }}

    >

      <Form<ManualNodeFormValues>

        form={form}

        layout="vertical"

        initialValues={{

          kind: 'unknown' as EntityKind,

          lineStyle: 'solid',

          lineWidth: 1.2,

          outgoingEnabled: false,

          incomingEnabled: false,

          outgoingLinks: [{}],

          incomingLinks: [{}],

        }}

        onFinish={handleFinish}

        onValuesChange={(changed) => {

          if ('outgoingEnabled' in changed) setOutgoingEnabled(changed.outgoingEnabled ?? false);

          if ('incomingEnabled' in changed) setIncomingEnabled(changed.incomingEnabled ?? false);

        }}

      >

        <Form.Item name="label" label="主体名称" rules={[{ required: true, message: '请输入主体名称' }]}>

          <Input placeholder="例如：张三、某公司、银行卡" />

        </Form.Item>

        <Form.Item name="kind" label="主体类型">

          <Select<EntityKind> options={ENTITY_KIND_OPTIONS} />

        </Form.Item>

        <div className="manual-edge-options">

          <Form.Item name="lineStyle" label="线条样式">

            <Select options={[

              { label: '实线', value: 'solid' },

              { label: '虚线', value: 'dashed' },

            ]} />

          </Form.Item>

          <Form.Item name="lineWidth" label="线条磅数">

            <InputNumber className="full" min={0.5} max={8} step={0.5} precision={1} />

          </Form.Item>

        </div>

        <Form.Item name="outgoingEnabled" valuePropName="checked">

          <Checkbox>连接到其他对象</Checkbox>

        </Form.Item>

        {outgoingEnabled && (

          <Form.List name="outgoingLinks">

            {(fields, { add, remove }) => (

              <div className="manual-link-list">

                {fields.map((field) => (

                  <Space key={field.key} align="start" className="manual-link-row">

                    <Form.Item {...field} name={[field.name, 'nodeId']} rules={[{ required: true, message: '请选择对象' }]}>

                      <Select

                        showSearch

                        optionFilterProp="label"

                        placeholder="新增主体 “?已有对象"

                        options={nodeSelectOptions(nodes)}

                      />

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

                <Button block icon={<PlusOutlined />} onClick={() => add({})}>添加链接对象</Button>

              </div>

            )}

          </Form.List>

        )}

        <Form.Item name="incomingEnabled" valuePropName="checked">

          <Checkbox>被其他对象链接</Checkbox>

        </Form.Item>

        {incomingEnabled && (

          <Form.List name="incomingLinks">

            {(fields, { add, remove }) => (

              <div className="manual-link-list">

                {fields.map((field) => (

                  <Space key={field.key} align="start" className="manual-link-row">

                    <Form.Item {...field} name={[field.name, 'nodeId']} rules={[{ required: true, message: '请选择对象' }]}>

                      <Select

                        showSearch

                        optionFilterProp="label"

                        placeholder="已有对象 “?新增主体"

                        options={nodeSelectOptions(nodes)}

                      />

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

                <Button block icon={<PlusOutlined />} onClick={() => add({})}>添加被链接对象</Button>

              </div>

            )}

          </Form.List>

        )}

      </Form>

    </Modal>

  );

}

