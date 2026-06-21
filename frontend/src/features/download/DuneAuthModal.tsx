import { SaveOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input, Modal, Space, Tag } from 'antd';
import type { FormInstance } from 'antd';
import { useEffect, useState } from 'react';
import type { DuneAuthStatus } from './duneApi';

const { TextArea } = Input;

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
  readonly authStatus?: DuneAuthStatus | null;
};

type DetectedFields = {
  readonly apiKey?: string;
  readonly cookie?: string;
  readonly authorization?: string;
  readonly accessToken?: string;
  readonly detectedFrom: string;
};

function parseCredentials(raw: string): DetectedFields {
  const text = raw.trim();
  const result: Record<string, string> = {};

  // Try JSON
  try {
    const json = JSON.parse(text);
    const m: Record<string, string> = {
      DUNE_API_KEY: 'apiKey',
      DUNE_COOKIE: 'cookie',
      AUTHORIZATION: 'authorization',
      X_DUNE_ACCESS_TOKEN: 'accessToken',
    };
    for (const [srcKey, destKey] of Object.entries(m)) {
      if (typeof json[srcKey] === 'string' && json[srcKey]) {
        result[destKey] = json[srcKey];
      }
    }
    if (Object.keys(result).length > 0) {
      return { detectedFrom: 'JSON', ...result };
    }
  } catch {
    // Not JSON, try .env
  }

  // Try .env format
  const envMap: Record<string, string> = {
    DUNE_API_KEY: 'apiKey',
    DUNE_COOKIE: 'cookie',
    AUTHORIZATION: 'authorization',
    X_DUNE_ACCESS_TOKEN: 'accessToken',
    x_dune_access_token: 'accessToken',
  };
  const keyRe = /^(DUNE_API_KEY|DUNE_COOKIE|AUTHORIZATION|X_DUNE_ACCESS_TOKEN|x_dune_access_token)\s*=\s*(.+)$/im;
  let match;
  while ((match = keyRe.exec(text)) !== null) {
    const envKey = match[1];
    const envValue = match[2].trim().replace(/^["']|["']$/g, '');
    if (envValue) {
      const destKey = envMap[envKey];
      result[destKey] = envValue;
    }
    keyRe.lastIndex = match.index + 1;
  }
  if (Object.keys(result).length > 0) {
    return { detectedFrom: '.env', ...result };
  }

  return { detectedFrom: '未知' };
}

export function DuneAuthModal({ open, form, onClose, onSave, authStatus }: DuneAuthModalProps) {
  const [pasteText, setPasteText] = useState('');
  const [detected, setDetected] = useState<DetectedFields | null>(null);

  // Pre-fill form from stored credentials when modal opens
  useEffect(() => {
    if (open && authStatus) {
      form.setFieldsValue({
        apiKey: undefined,
        cookie: authStatus.cookie || undefined,
        authorization: authStatus.authorization || undefined,
        accessToken: authStatus.access_token || undefined,
      });
    }
  }, [open, authStatus, form]);

  function handlePasteChange(raw: string) {
    setPasteText(raw);
    if (!raw.trim()) {
      setDetected(null);
      return;
    }
    const fields = parseCredentials(raw);
    setDetected(fields);
    // Auto-fill form
    if (fields.apiKey || fields.cookie || fields.authorization || fields.accessToken) {
      form.setFieldsValue({
        apiKey: fields.apiKey || undefined,
        cookie: fields.cookie || undefined,
        authorization: fields.authorization || undefined,
        accessToken: fields.accessToken || undefined,
      });
    }
  }

  const detectedCount = detected
    ? [detected.apiKey, detected.cookie, detected.authorization, detected.accessToken].filter(Boolean).length
    : 0;

  return (
    <Modal title="Dune 鉴权保存" open={open} onCancel={onClose} footer={null} width={640}>
      <Alert
        className="download-alert"
        type="info"
        showIcon
        message="粘贴 Dune 参数"
        description="支持浏览器扩展复制的 JSON 或 .env 格式，自动识别并填入下方字段。也可手动编辑。"
        style={{ marginBottom: 12 }}
      />

      <TextArea
        autoSize={{ minRows: 4, maxRows: 10 }}
        placeholder={`粘贴 JSON 或 .env 参数，自动解析：

JSON 示例：
{"DUNE_COOKIE":"csrf=...","AUTHORIZATION":"Bearer ...","X_DUNE_ACCESS_TOKEN":"eyJ..."}

.env 示例：
DUNE_COOKIE=csrf=...
AUTHORIZATION=Bearer eyJ...
X_DUNE_ACCESS_TOKEN=eyJ...`}
        value={pasteText}
        onChange={(e) => handlePasteChange(e.target.value)}
        style={{ marginBottom: 12, fontFamily: 'monospace', fontSize: 12 }}
      />

      {detected && (
        <div style={{ marginBottom: 12 }}>
          <Tag color="green">{detected.detectedFrom} 检测到 {detectedCount} 项</Tag>
          {detected.apiKey && <Tag>API Key ✓</Tag>}
          {detected.cookie && <Tag>Cookie ✓ ({detected.cookie.length} 字符)</Tag>}
          {detected.authorization && <Tag>Authorization ✓</Tag>}
          {detected.accessToken && <Tag>AccessToken ✓</Tag>}
        </div>
      )}

      <Form form={form} layout="vertical" onFinish={onSave}>
        <Form.Item name="apiKey" label="Dune API Key（可选）">
          <Input.Password autoComplete="off" placeholder="不需手动填，粘贴上方自动解析" />
        </Form.Item>
        <Form.Item name="cookie" label="Dune Cookie">
          <TextArea autoSize={{ minRows: 2, maxRows: 4 }} placeholder="不需手动填，粘贴上方自动解析" />
        </Form.Item>
        <Form.Item name="authorization" label="Authorization">
          <Input.Password autoComplete="off" placeholder="不需手动填，粘贴上方自动解析" />
        </Form.Item>
        <Form.Item name="accessToken" label="X-Dune-Access-Token">
          <Input.Password autoComplete="off" placeholder="不需手动填，粘贴上方自动解析" />
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
