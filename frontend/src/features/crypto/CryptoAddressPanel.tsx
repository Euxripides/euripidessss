import { CopyOutlined, SearchOutlined } from '@ant-design/icons';
import { Button, Card, Checkbox, Form, Input, Select, Space, Statistic, Table, Tag, Tooltip, message } from 'antd';
import { useEffect, useMemo, useState } from 'react';
import type { ColumnsType } from 'antd/es/table';
import { classifyCryptoAddresses, type CryptoAddressClassifyResponse, type CryptoAddressClassifyValues, type CryptoAddressItem } from './cryptoAddressApi';

const SETTINGS_KEY = 'etl.crypto.address.settings.v1';
const DEFAULT_BSC_RPC = 'BSC|https://bsc-rpc.publicnode.com';

const chainOptions = [
  { value: 'ETH', label: 'ETH' },
  { value: 'BSC', label: 'BSC' },
  { value: 'POLYGON', label: 'POLYGON' },
  { value: 'ARBITRUM', label: 'ARBITRUM' },
  { value: 'BASE', label: 'BASE' },
  { value: 'OP', label: 'OP' },
  { value: 'AVAXC', label: 'AVAXC' },
  { value: 'TRON', label: 'TRON' },
  { value: 'BTC', label: 'BTC' },
  { value: 'SOL', label: 'SOL' },
  { value: 'LTC', label: 'LTC' },
  { value: 'DOGE', label: 'DOGE' },
  { value: 'XRP', label: 'XRP' },
  { value: 'ADA', label: 'ADA' },
];

type CryptoAddressFormValues = {
  readonly addresses: string;
  readonly chains?: string[];
  readonly rpcNodesText?: string;
  readonly includeDuplicates?: boolean;
};

export function CryptoAddressPanel() {
  const [form] = Form.useForm<CryptoAddressFormValues>();
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<CryptoAddressClassifyResponse | null>(null);

  useEffect(() => {
    const saved = loadSettings();
    form.setFieldsValue({
      chains: ['BSC'],
      rpcNodesText: DEFAULT_BSC_RPC,
      includeDuplicates: false,
      ...saved,
    });
  }, [form]);

  const columns = useMemo<ColumnsType<CryptoAddressItem>>(
    () => [
      {
        title: '地址',
        dataIndex: 'address',
        width: 340,
        render: (address: string, item) => (
          <div className="crypto-address-cell">
            <code>{address || item.input}</code>
            {item.warnings?.map((warning) => <Tag key={warning} color="orange">{warning}</Tag>)}
          </div>
        ),
      },
      {
        title: '类型',
        dataIndex: 'kind',
        width: 130,
        render: (_, item) => item.valid
          ? <Tag color={item.kind === 'CONTRACT' ? 'purple' : item.kind === 'EOA' ? 'blue' : 'default'}>{item.kind || item.family || item.network}</Tag>
          : <Tag color="red">{item.kind || 'INVALID'}</Tag>,
      },
      {
        title: '状态',
        dataIndex: 'status',
        width: 170,
        render: (status: string) => <Tag color={status.startsWith('OK') ? 'green' : status.includes('失败') || status.includes('错误') ? 'red' : 'default'}>{status || '-'}</Tag>,
      },
      {
        title: '重试次数',
        dataIndex: 'retry_count',
        width: 100,
      },
      {
        title: '候选链',
        dataIndex: 'candidates',
        render: (_, item) => (
          <Space size={[6, 6]} wrap>
            {item.candidates.length
              ? item.candidates.slice(0, 8).map((candidate) => (
                  <Tooltip key={`${item.address}-${candidate.chain}`} title={candidate.detail || `${candidate.name} · ${(candidate.confidence * 100).toFixed(0)}%`}>
                    <Tag color={candidate.status === 'verified' ? 'green' : candidate.source === 'api' ? 'cyan' : 'default'}>
                      {candidate.chain}
                    </Tag>
                  </Tooltip>
                ))
              : <span className="muted">-</span>}
          </Space>
        ),
      },
      {
        title: '置信度',
        dataIndex: 'confidence',
        width: 110,
        render: (confidence: number) => confidence ? `${Math.round(confidence * 100)}%` : '-',
      },
      {
        title: '错误信息 / 说明',
        dataIndex: 'error',
        render: (_, item) => <span className="crypto-reason">{item.error || item.reason}</span>,
      },
    ],
    [],
  );

  async function submit(values: CryptoAddressFormValues) {
    setLoading(true);
    try {
      const payload: CryptoAddressClassifyValues = {
        addresses: values.addresses,
        chains: values.chains,
        rpcNodes: parseRpcNodes(values.rpcNodesText ?? ''),
        includeDuplicates: values.includeDuplicates,
      };
      saveSettings(values);
      const next = await classifyCryptoAddresses(payload);
      setResult(next);
      message.success(`完成区分：有效 ${next.summary.valid} 个，未识别 ${next.summary.invalid} 个`);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '地址区分失败');
    } finally {
      setLoading(false);
    }
  }

  function copyResult() {
    if (!result) return;
    const lines = result.items.map((item) => [
      item.address,
      item.kind || (item.valid ? item.family : 'INVALID'),
      item.status,
      item.retry_count,
      item.error,
    ].join('\t'));
    navigator.clipboard.writeText(['地址\t类型\t状态\t重试次数\t错误信息', ...lines].join('\n'))
      .then(() => message.success('已复制结果'))
      .catch(() => message.error('复制失败'));
  }

  return (
    <div className="crypto-address-shell">
      <section className="topbar">
        <div>
          <div className="topbar-title-row">
            <h1>地址区分</h1>
          </div>
          <p>按原地址区分逻辑在线判断 EVM 外部账户（EOA）和合约（CONTRACT）；默认使用 BSC，也可选择其他链或配置多个 RPC 节点。</p>
        </div>
        <Space>
          <Button icon={<CopyOutlined />} disabled={!result?.items.length} onClick={copyResult}>复制结果</Button>
        </Space>
      </section>

      <Card className="panel crypto-address-card">
        <Form form={form} layout="vertical" onFinish={submit}>
          <div className="crypto-address-grid">
            <Form.Item
              name="addresses"
              label="地址列表"
              rules={[{ required: true, message: '请输入地址' }]}
            >
              <Input.TextArea
                className="crypto-address-input"
                rows={12}
                placeholder="每行一个地址，也可以用空格、逗号、分号分隔"
              />
            </Form.Item>
            <div className="crypto-settings-stack">
              <Form.Item name="chains" label="限定链范围">
                <Select mode="multiple" allowClear options={chainOptions} placeholder="不选则使用全部内置规则" />
              </Form.Item>
              <Form.Item
                name="rpcNodesText"
                label="RPC 节点"
                extra="每行一个节点；支持 `CHAIN|URL`、`CHAIN=URL` 或直接填 URL。某条节点额度耗尽或限流后会自动切换下一条。"
              >
                <Input.TextArea
                  rows={7}
                  className="crypto-rpc-input"
                  placeholder={"ETH|https://eth-rpc.example\nBSC|https://bsc-rpc.example\nhttps://generic-rpc.example"}
                />
              </Form.Item>
              <Form.Item name="includeDuplicates" valuePropName="checked">
                <Checkbox>保留重复地址</Checkbox>
              </Form.Item>
              <Button type="primary" htmlType="submit" icon={<SearchOutlined />} loading={loading} block>
                开始区分
              </Button>
            </div>
          </div>
        </Form>
      </Card>

      {result && (
        <>
          <div className="crypto-stats">
            <Card><Statistic title="总数" value={result.summary.total} /></Card>
            <Card><Statistic title="有效地址" value={result.summary.valid} /></Card>
            <Card><Statistic title="未识别" value={result.summary.invalid} /></Card>
            <Card><Statistic title="重复" value={result.summary.duplicates} /></Card>
          </div>
          <Card className="crypto-result-card">
            <Table
              rowKey={(item) => `${item.address}-${item.input}`}
              columns={columns}
              dataSource={[...result.items]}
              pagination={{ pageSize: 20, showSizeChanger: true }}
              scroll={{ x: 1280 }}
            />
          </Card>
        </>
      )}
    </div>
  );
}

function loadSettings(): Partial<CryptoAddressFormValues> {
  try {
    const raw = window.localStorage.getItem(SETTINGS_KEY);
    if (!raw) return {};
    const value = JSON.parse(raw) as Partial<CryptoAddressFormValues>;
    return {
      chains: Array.isArray(value.chains) ? value.chains.filter((item): item is string => typeof item === 'string') : undefined,
      rpcNodesText: typeof value.rpcNodesText === 'string' ? value.rpcNodesText : undefined,
      includeDuplicates: value.includeDuplicates === true,
    };
  } catch {
    return {};
  }
}

function saveSettings(values: CryptoAddressFormValues): void {
  const { addresses: _addresses, ...settings } = values;
  window.localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings));
}

function parseRpcNodes(raw: string): string[] {
  return raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}
