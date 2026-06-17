import { SaveOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input, Modal, Space } from 'antd';
import type { FormInstance } from 'antd';

const { TextArea, Password } = Input;

export type DuneAuthFormValues = {
  readonly apiKey?: string;
  readonly cookie?: string;
  readonly authorization?: string;
  readonly accessToken?: string;
};

type DuneAuthModalProps = {
  readonly open: boolean;
  readonly form: FormInstance<DuneAuthFormValues>;
  readonly onClose: () => void;
  readonly onSave: (values: DuneAuthFormValues) => Promise<void>;
};

export function DuneAuthModal({ open, form, onClose, onSave }: DuneAuthModalProps) {
  return (
    <Modal title="Dune 手动鉴权保存" open={open} onCancel={onClose} footer={null} width={620}>
      <Alert
        className="download-alert"
        type="info"
        showIcon
        message="手动填写 Dune API Key、Cookie 或官网 Token"
        description="API Key 用于官方查询接口；Cookie、Authorization 和 X-Dune-Access-Token 用于官网 UpdateQuery / ExecuteQuery 链路。系统不会启动浏览器或自动抓取登录信息。"
      />
      <Form form={form} layout="vertical" onFinish={onSave}>
        <Form.Item name="apiKey" label="Dune API Key">
          <Password autoComplete="off" placeholder="粘贴 Dune API Key" />
        </Form.Item>
        <Form.Item name="cookie" label="Dune Cookie（可选）">
          <TextArea autoSize={{ minRows: 3, maxRows: 6 }} placeholder="可选：从已登录浏览器手动复制 Cookie" />
        </Form.Item>
        <Form.Item name="authorization" label="Authorization（官网查询可选）">
          <Password autoComplete="off" placeholder="Bearer ...；不填时会尝试从 Cookie 的 auth-id-token 提取" />
        </Form.Item>
        <Form.Item name="accessToken" label="X-Dune-Access-Token（官网查询必填）">
          <Password autoComplete="off" placeholder="粘贴请求头里的 x-dune-access-token" />
        </Form.Item>
        <Space>
          <Button type="primary" htmlType="submit" icon={<SaveOutlined />}>
            保存
          </Button>
        </Space>
      </Form>
    </Modal>
  );
}
