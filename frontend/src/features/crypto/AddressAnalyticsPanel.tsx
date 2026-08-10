import {
  ApartmentOutlined,
  ArrowDownOutlined,
  ArrowUpOutlined,
  BlockOutlined,
  ClockCircleOutlined,
  ExportOutlined,
  InfoCircleOutlined,
  SearchOutlined,
  SwapOutlined,
  WalletOutlined,
} from '@ant-design/icons';
import {
  Alert,
  Button,
  DatePicker,
  Empty,
  Form,
  Input,
  Select,
  Skeleton,
  Space,
  Spin,
  Statistic,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
  Tooltip,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { ReactNode } from 'react';
import { useCallback, useMemo, useRef, useState } from 'react';
import dayjs from 'dayjs';
import {
  loadAddressActivity,
  loadAddressCounterparties,
  loadAddressNFTs,
  loadAddressSummary,
  loadAddressTokens,
  loadFirstSeen,
  type AddressActivity,
  type AddressAsset,
  type AddressCounterparty,
  type AddressQueryParams,
  type AddressSummary,
  type EVMChainKey,
  type FirstSeen,
  type FirstSeenStatus,
  type PageResult,
} from './addressAnalyticsApi';
import './address-analytics.css';

type SearchValues = {
  chain_key: EVMChainKey;
  address: string;
  use_first_seen: boolean;
  start_time?: dayjs.Dayjs | null;
  end_time?: dayjs.Dayjs | null;
};

const chainOptions = [
  { value: 'bsc', label: 'BNB Smart Chain' },
  { value: 'eth', label: 'Ethereum' },
  { value: 'base', label: 'Base' },
  { value: 'arbitrum', label: 'Arbitrum One' },
];

export function AddressAnalyticsPanel() {
  const [form] = Form.useForm<SearchValues>();
  const [loading, setLoading] = useState(false);
  const [searched, setSearched] = useState(false);
  const [summary, setSummary] = useState<AddressSummary | null>(null);
  const [activity, setActivity] = useState<PageResult<AddressActivity> | null>(null);
  const [tokens, setTokens] = useState<PageResult<AddressAsset> | null>(null);
  const [nfts, setNFTs] = useState<PageResult<AddressAsset> | null>(null);
  const [counterparties, setCounterparties] = useState<PageResult<AddressCounterparty> | null>(null);
  const chainKey = summary?.chain_key ?? 'bsc';
  const [useFirstSeen, setUseFirstSeen] = useState(true);
  const [firstSeen, setFirstSeen] = useState<FirstSeen | null>(null);
  const [firstSeenLoading, setFirstSeenLoading] = useState(false);
  const firstSeenFetchedRef = useRef<string>('');
  const activityColumns = useMemo(() => buildActivityColumns(chainKey), [chainKey]);
  const assetColumns = useMemo(() => buildAssetColumns(chainKey), [chainKey]);
  const counterpartyColumns = useMemo(() => buildCounterpartyColumns(chainKey), [chainKey]);

  const submitDisabled = useMemo(() => {
    if (useFirstSeen) {
      if (firstSeenLoading) return true;
      if (firstSeen && (firstSeen.status === 'not_found' || firstSeen.status === 'temporarily_unavailable')) return true;
    }
    if (!useFirstSeen) {
      const startVal = form.getFieldValue('start_time');
      if (!startVal) return true;
    }
    return false;
  }, [useFirstSeen, firstSeenLoading, firstSeen, form]);

  const fetchFirstSeen = useCallback(async (chain: EVMChainKey, addr: string) => {
    const cacheKey = `${chain}:${addr}`;
    if (firstSeenFetchedRef.current === cacheKey) return;
    firstSeenFetchedRef.current = cacheKey;
    setFirstSeenLoading(true);
    setFirstSeen(null);
    try {
      const result = await loadFirstSeen(chain, addr);
      setFirstSeen(result);
    } catch {
      setFirstSeen({ status: 'failed' });
    } finally {
      setFirstSeenLoading(false);
    }
  }, []);

  async function searchAddress(values: SearchValues) {
    const address = values.address.trim().toLowerCase();
    setLoading(true);
    setSearched(true);

    const params: AddressQueryParams = {
      chain_key: values.chain_key,
      address,
      use_first_seen: values.use_first_seen,
    };
    if (!values.use_first_seen) {
      params.start_time = values.start_time ? values.start_time.toISOString() : null;
      params.end_time = values.end_time ? values.end_time.toISOString() : null;
    }

    try {
      const [nextSummary, nextActivity, nextTokens, nextNFTs, nextCounterparties] = await Promise.all([
        loadAddressSummary(params),
        loadAddressActivity(params),
        loadAddressTokens(params),
        loadAddressNFTs(params),
        loadAddressCounterparties(params),
      ]);
      setSummary(nextSummary);
      setActivity(nextActivity);
      setTokens(nextTokens);
      setNFTs(nextNFTs);
      setCounterparties(nextCounterparties);
    } catch (error) {
      setSummary(null);
      setActivity(null);
      setTokens(null);
      setNFTs(null);
      setCounterparties(null);
      message.error(error instanceof Error ? error.message : '读取地址分析失败');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="address-analytics-shell">
      <header className="address-analytics-hero">
        <div>
          <span className="address-analytics-kicker">EVM ADDRESS INTELLIGENCE</span>
          <h1>链上地址分析</h1>
        </div>
        <BlockOutlined />
      </header>

      <Form<SearchValues>
        form={form}
        initialValues={{ chain_key: 'bsc', address: '', use_first_seen: true }}
        onFinish={searchAddress}
      >
        <div className="address-analytics-search">
          <Form.Item name="chain_key" noStyle>
            <Select options={chainOptions} aria-label="EVM 网络"
              onChange={(value: EVMChainKey) => {
                setFirstSeen(null);
                const addr = form.getFieldValue('address')?.trim().toLowerCase() ?? '';
                if (/^0x[0-9a-fA-F]{40}$/.test(addr)) {
                  void fetchFirstSeen(value, addr);
                }
              }}
            />
          </Form.Item>
          <Form.Item
            name="address"
            noStyle
            rules={[
              { required: true, message: '请输入 EVM 地址' },
              { pattern: /^0x[0-9a-fA-F]{40}$/, message: '地址必须是 0x 开头的 40 位十六进制地址' },
            ]}
          >
            <Input placeholder="输入 0x 地址" spellCheck={false} allowClear
              onChange={(e) => {
                const v = e.target.value.trim().toLowerCase();
                if (/^0x[0-9a-fA-F]{40}$/.test(v)) {
                  const chain = form.getFieldValue('chain_key') ?? 'bsc';
                  void fetchFirstSeen(chain, v);
                }
              }}
            />
          </Form.Item>
          <Button type="primary" htmlType="submit" icon={<SearchOutlined />} loading={loading} disabled={submitDisabled}>
            分析地址
          </Button>
        </div>

        <div className="address-analytics-time-row">
          <Form.Item name="use_first_seen" valuePropName="checked" noStyle>
            <Switch onChange={(checked) => setUseFirstSeen(checked)} />
          </Form.Item>
          <span className="address-analytics-time-label">从地址首次出现开始分析</span>
          <Form.Item name="start_time" noStyle
            rules={useFirstSeen ? [] : [{ required: true, message: '关闭首次时间后必须选择开始时间' }]}
          >
            <DatePicker
              showTime
              placeholder="开始时间"
              disabled={useFirstSeen}
              allowClear={false}
              className="address-analytics-time-picker"
            />
          </Form.Item>
          <Form.Item name="end_time" noStyle>
            <DatePicker
              showTime
              placeholder="结束时间"
              allowClear
              className="address-analytics-time-picker"
            />
          </Form.Item>
        </div>

        {useFirstSeen && firstSeen && (
          <div className="address-analytics-first-seen">
            {firstSeenLoading ? (
              <span><Spin size="small" /> 查询首次出现时间…</span>
            ) : firstSeen.status === 'found' || firstSeen.status === 'partial' ? (
              <>
                <Tag color={firstSeen.status === 'found' ? 'green' : 'orange'}>
                  {firstSeen.status === 'found' ? '已确认' : '部分覆盖'}
                </Tag>
                <span>
                  首次出现：{firstSeen.first_seen_time
                    ? dayjs(firstSeen.first_seen_time).format('YYYY-MM-DD HH:mm UTC')
                    : '未知'}
                </span>
                {firstSeen.first_seen_source && (
                  <Tag>{firstSeen.first_seen_source}</Tag>
                )}
              </>
            ) : firstSeen.status === 'not_found' ? (
              <Tag color="default">未找到历史活动</Tag>
            ) : firstSeen.status === 'temporarily_unavailable' ? (
              <Tag color="warning">数据源暂不可用</Tag>
            ) : (
              <Tag color="error">查询失败</Tag>
            )}
          </div>
        )}
      </Form>

      {loading ? (
        <div className="address-analytics-loading"><Skeleton active paragraph={{ rows: 10 }} /></div>
      ) : summary ? (
        <>
          {!summary.rpc_configured && (
            <Alert
              className="address-analytics-rpc-alert"
              type="warning"
              showIcon
              message="RPC 尚未配置，实时状态检测未执行"
              description={`${summary.rpc_env} 未配置：地址类型显示“未检测”，Token Metadata 不推测，余额快照暂不可用。`}
            />
          )}
          <section className="address-analytics-identity">
            <div>
              <div className="address-analytics-avatar"><WalletOutlined /></div>
              <span>
                <ExplorerIdentifier value={summary.address} chainKey={summary.chain_key} kind="address" />
                <small>{summary.chain_key.toUpperCase()} · Chain ID {summary.chain_id}</small>
              </span>
            </div>
            <span className="address-analytics-address-status">
              <Tag color={summary.address_type === 'CONTRACT' ? 'purple' : summary.address_type === 'EOA' ? 'blue' : 'default'}>
                {addressTypeLabel(summary.address_type)}
              </Tag>
              <small>{summary.address_type_reason}</small>
            </span>
          </section>

          <section className="address-analytics-metrics">
            <Metric title="交易数" description="该地址参与的去重交易哈希数量。" value={summary.tx_count} icon={<SwapOutlined />} />
            <Metric title="Token" description="该地址产生过标准 Token 活动的不同合约数量。" value={summary.token_count} icon={<WalletOutlined />} />
            <Metric title="NFT" description="该地址产生过 ERC-721/ERC-1155 活动的不同合约数量。" value={summary.nft_count} icon={<BlockOutlined />} />
            <Metric title="交易对手" description="与该地址发生过资金活动的不同地址数量。" value={summary.unique_counterparty_count} icon={<ApartmentOutlined />} />
            <Metric title="原生币流入" description="当前任务覆盖范围内原生币流入金额合计。" value={summary.total_native_in} icon={<ArrowDownOutlined />} />
            <Metric title="原生币流出" description="当前任务覆盖范围内原生币流出金额合计。" value={summary.total_native_out} icon={<ArrowUpOutlined />} />
            <Metric title="当前原生币余额" description="由高可用 RPC 读取的最新区块余额；与任务历史范围统计相互独立。" value={summary.native_balance ?? '未检测'} icon={<WalletOutlined />} />
          </section>

          <Tabs
            className="address-analytics-tabs"
            items={[
              {
                key: 'activity',
                label: `流水 ${activity?.total ?? 0}`,
                children: <Table rowKey={(row) => `${row.tx_hash}-${row.activity_type}-${row.direction}-${row.asset_address ?? ''}-${row.amount_raw}`} columns={activityColumns} dataSource={[...(activity?.rows ?? [])]} pagination={{ pageSize: 20 }} scroll={{ x: 1420 }} />,
              },
              {
                key: 'tokens',
                label: `Token资产 ${tokens?.total ?? 0}`,
                children: <Table rowKey="asset_address" columns={assetColumns} dataSource={[...(tokens?.rows ?? [])]} pagination={false} scroll={{ x: 900 }} />,
              },
              {
                key: 'nfts',
                label: `NFT资产 ${nfts?.total ?? 0}`,
                children: <Table rowKey="asset_address" columns={assetColumns} dataSource={[...(nfts?.rows ?? [])]} pagination={false} scroll={{ x: 900 }} />,
              },
              {
                key: 'counterparties',
                label: `资金关系 ${counterparties?.total ?? 0}`,
                children: <Table rowKey="counterparty" columns={counterpartyColumns} dataSource={[...(counterparties?.rows ?? [])]} pagination={false} scroll={{ x: 1050 }} />,
              },
            ]}
          />

          <div className="address-analytics-range">
            <ClockCircleOutlined />
            数据活跃区间：{formatTime(summary.first_active_time)} 至 {formatTime(summary.last_active_time)}
          </div>
        </>
      ) : searched ? (
        <Alert type="info" showIcon message="该链地址暂无 V1.3 分析数据，请先在 Parquet 下载页面完成对应地址任务。" />
      ) : (
        <Empty className="address-analytics-empty" description="选择网络并输入地址，查看统一链上画像" />
      )}
    </div>
  );
}

function Metric({ title, description, value, icon }: { title: string; description: string; value: string | number; icon: ReactNode }) {
  return (
    <div className="address-analytics-metric">
      <span>{icon}</span>
      <Statistic
        title={<Tooltip title={description}><span className="address-analytics-metric-title">{title}<InfoCircleOutlined /></span></Tooltip>}
        value={compactDecimal(value)}
      />
    </div>
  );
}

function compactDecimal(value: string | number) {
  if (typeof value !== 'string' || !/^-?\d+\.\d+$/.test(value)) return value;
  return value.replace(/0+$/, '').replace(/\.$/, '') || '0';
}

function buildActivityColumns(chainKey: EVMChainKey): ColumnsType<AddressActivity> {
  return [
    { title: '时间', dataIndex: 'block_time', width: 170, render: formatTime },
    { title: '交易哈希', dataIndex: 'tx_hash', width: 220, render: (value) => <ExplorerIdentifier value={value} chainKey={chainKey} kind="tx" /> },
    { title: '类型', dataIndex: 'activity_type', width: 150, render: (value) => <Tag>{activityTypeLabel(value)}</Tag> },
    { title: '方向', dataIndex: 'direction', width: 82, render: directionTag },
    { title: '资产', key: 'asset', width: 145, render: (_, row) => row.symbol || row.asset_type },
    { title: '金额', key: 'amount', width: 180, render: (_, row) => compactDecimal(row.amount || row.amount_raw) },
    { title: '状态', dataIndex: 'status', width: 105, render: statusTag },
    { title: '交易对手', dataIndex: 'counterparty', width: 210, render: (value) => value ? <ExplorerIdentifier value={value} chainKey={chainKey} kind="address" /> : '—' },
    { title: '来源', dataIndex: 'source', width: 130 },
  ];
}

function buildAssetColumns(chainKey: EVMChainKey): ColumnsType<AddressAsset> {
  return [
    { title: '资产', key: 'name', width: 180, render: (_, row) => <span className="address-analytics-asset"><strong>{metadataDisplay(row.symbol)}</strong><small>{knownMetadata(row.name) || row.standard || row.asset_type}</small></span> },
    { title: '合约地址', dataIndex: 'asset_address', width: 240, render: (value) => <ExplorerIdentifier value={value} chainKey={chainKey} kind="address" /> },
    { title: '余额', key: 'balance', width: 170, render: (_, row) => row.balance ?? row.balance_raw ?? '未快照' },
    { title: '活动数', dataIndex: 'activity_count', width: 90 },
    { title: '最后活动', dataIndex: 'last_active_time', width: 180, render: formatTime },
    { title: 'Metadata 状态', dataIndex: 'source', width: 140, render: metadataStatusTag },
  ];
}

function buildCounterpartyColumns(chainKey: EVMChainKey): ColumnsType<AddressCounterparty> {
  return [
    { title: '交易对手', dataIndex: 'counterparty', width: 235, render: (value) => <ExplorerIdentifier value={value} chainKey={chainKey} kind="address" /> },
    { title: '方向', dataIndex: 'direction', width: 90, render: directionTag },
    { title: '原生币活动', key: 'native', width: 150, render: (_, row) => <DirectionSplit incoming={row.native_in_count} outgoing={row.native_out_count} /> },
    { title: 'Token 活动', key: 'token', width: 150, render: (_, row) => <DirectionSplit incoming={row.token_in_count} outgoing={row.token_out_count} /> },
    { title: '交互次数', dataIndex: 'activity_count', width: 90 },
    { title: '交易数', dataIndex: 'tx_count', width: 80 },
    { title: '首次交互', dataIndex: 'first_active_time', width: 175, render: formatTime },
    { title: '最后交互', dataIndex: 'last_active_time', width: 175, render: formatTime },
  ];
}

function ExplorerIdentifier({
  value,
  chainKey,
  kind,
}: {
  value: string;
  chainKey: EVMChainKey;
  kind: 'address' | 'tx';
}) {
  const label = kind === 'tx' ? compactHash(value) : compactAddress(value);
  return (
    <Space size={3} className="address-analytics-identifier">
      <Typography.Text copyable={{ text: value }}>{label}</Typography.Text>
      <Tooltip title={kind === 'tx' ? '在区块浏览器查看交易' : '在区块浏览器查看地址'}>
        <Button
          type="text"
          size="small"
          icon={<ExportOutlined />}
          href={explorerURL(chainKey, kind, value)}
          target="_blank"
          rel="noreferrer"
          aria-label={kind === 'tx' ? '打开交易浏览器' : '打开地址浏览器'}
        />
      </Tooltip>
    </Space>
  );
}

function DirectionSplit({ incoming, outgoing }: { incoming: number; outgoing: number }) {
  return (
    <Space size={4}>
      <Tag color="green">入 {incoming}</Tag>
      <Tag color="orange">出 {outgoing}</Tag>
    </Space>
  );
}

function directionTag(value?: string) {
  if (value === 'IN') return <Tag color="green">流入</Tag>;
  if (value === 'OUT') return <Tag color="orange">流出</Tag>;
  if (value === 'BOTH') return <Tag color="blue">双向</Tag>;
  return <Tag>未检测</Tag>;
}

function statusTag(value?: string) {
  if (value === 'SUCCESS') return <Tag color="green">成功</Tag>;
  if (value === 'FAILED') return <Tag color="red">失败</Tag>;
  return <Tooltip title="数据源未提供交易执行状态，或未启用 Receipt/RPC 富化"><Tag>未检测</Tag></Tooltip>;
}

function metadataStatusTag(value?: string) {
  if (value === 'RPC') return <Tag color="green">已检测</Tag>;
  if (value === 'RPC_PARTIAL') return <Tooltip title="RPC 仅返回了部分 Metadata 字段"><Tag color="gold">部分检测</Tag></Tooltip>;
  return <Tooltip title="未配置对应链 RPC，Metadata 字段未推测"><Tag>未检测</Tag></Tooltip>;
}

function addressTypeLabel(value?: string) {
  if (value === 'EOA') return '外部账户';
  if (value === 'CONTRACT') return '合约地址';
  return '未检测';
}

function activityTypeLabel(value?: string) {
  return {
    NATIVE_TRANSFER: '原生币转账',
    TOKEN_TRANSFER: 'Token 转账',
    NFT_TRANSFER: 'NFT 转账',
    INTERNAL_TRANSFER: '内部转账',
    CONTRACT_CALL: '合约调用',
    CONTRACT_CREATE: '合约创建',
    APPROVE: '授权',
    SWAP: '兑换',
  }[value ?? ''] ?? value ?? '未检测';
}

function metadataDisplay(value?: string) {
  return knownMetadata(value) || '未检测';
}

function knownMetadata(value?: string) {
  return value && value !== 'UNKNOWN' ? value : '';
}

function explorerURL(chainKey: EVMChainKey, kind: 'address' | 'tx', value: string) {
  const base = {
    bsc: 'https://bscscan.com',
    eth: 'https://etherscan.io',
    base: 'https://basescan.org',
    arbitrum: 'https://arbiscan.io',
  }[chainKey];
  return `${base}/${kind}/${encodeURIComponent(value)}`;
}

function compactAddress(value?: string) {
  if (!value) return '—';
  return value.length > 18 ? `${value.slice(0, 10)}…${value.slice(-8)}` : value;
}

function compactHash(value?: string) {
  if (!value) return '—';
  return `${value.slice(0, 12)}…${value.slice(-8)}`;
}

function formatTime(value?: string) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false });
}
