import { Alert, Button, Card, Col, Descriptions, Form, Input, InputNumber, Row, Select, Space, Statistic, Switch, Table, Tabs, Tag, message } from "antd";
import { useCallback, useEffect, useState } from "react";
import "./price-engine.css";

type Health = { status: string; clickhouse: string; root: string; providers: Array<{ name: string; status: string; detail?: string }> };
type Candle = { time: string; open: string; high: string; low: string; close: string; volume_usd: string; trade_count: number; source: string };
type Point = { token: string; timestamp: string; price_usd: string | null; source?: string; confidence?: string; price_type: string; age_seconds?: number; status?: string };
type BackfillJob = { id: string; symbol: string; month: string; status: string; error?: string; result?: { rows: number; checksum: string; reused: boolean } };
type Coverage = { chain_id: number; token_address: string; first_price_at: string; last_price_at: string; minute_count: number; trade_count: number; coverage_ratio: number };
type CoverageResponse = { covered: boolean; token?: string; coverage?: Coverage };
type PriceGap = { chain_id: number; token_id: string; resolution: string; gap_start: string; gap_end: string; missing_buckets: number };
type Pool = { protocol_id: string; dex: string; version: string; factory_address: string; pool_address: string; token0: string; token1: string; fee_bps: number; verified: boolean; liquidity_score: number };
type PoolDiscovery = { discovered: number; written: number; skipped_metadata: number; skipped_untrusted: number };
type DEXJob = { id: string; batch_id: string; token: string; status: string; error?: string; pools: string[]; result?: { pools: number; swaps: number; bars: number } };

const nowISO = () => new Date().toISOString();
const agoISO = (milliseconds: number) => new Date(Date.now() - milliseconds).toISOString();

async function json<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init);
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.detail || `HTTP ${response.status}`);
  return body as T;
}

export default function PriceEnginePage() {
  const [health, setHealth] = useState<Health>();
  const [point, setPoint] = useState<Point>();
  const [candles, setCandles] = useState<Candle[]>([]);
  const [job, setJob] = useState<BackfillJob>();
  const [coverage, setCoverage] = useState<CoverageResponse>();
  const [gaps, setGaps] = useState<PriceGap[]>([]);
  const [discovery, setDiscovery] = useState<PoolDiscovery>();
  const [pools, setPools] = useState<Pool[]>([]);
  const [dexJob, setDEXJob] = useState<DEXJob>();
  const [loading, setLoading] = useState(false);

  const refreshHealth = useCallback(async () => {
    try { setHealth(await json<Health>("/api/price/health")); } catch (error) { message.error((error as Error).message); }
  }, []);
  useEffect(() => { void refreshHealth(); }, [refreshHealth]);
  useEffect(() => {
    if (!job || job.status !== "RUNNING") return;
    const timer = window.setInterval(async () => {
      try { setJob(await json<BackfillJob>(`/api/price/backfill/jobs/${job.id}`)); } catch { /* keep the last durable status */ }
    }, 1500);
    return () => window.clearInterval(timer);
  }, [job]);
  useEffect(() => {
    if (!dexJob || ["COMPLETED", "FAILED"].includes(dexJob.status)) return;
    const timer = window.setInterval(async () => {
      try { setDEXJob(await json<DEXJob>(`/api/price/backfill/dex/jobs/${dexJob.id}`)); } catch { /* retain the last auditable state */ }
    }, 2000);
    return () => window.clearInterval(timer);
  }, [dexJob]);

  const queryPoint = async (values: { token: string; timestamp: string }) => {
    setLoading(true);
    try { setPoint(await json<Point>(`/api/price/history/point?chain=bsc&token=${encodeURIComponent(values.token)}&timestamp=${encodeURIComponent(values.timestamp)}`)); }
    catch (error) { message.error((error as Error).message); } finally { setLoading(false); }
  };
  const queryCandles = async (values: { token: string; start: string; end: string; interval: string }) => {
    setLoading(true);
    try { const data = await json<{ candles: Candle[] }>(`/api/price/history/candles?chain=bsc&token=${encodeURIComponent(values.token)}&start=${encodeURIComponent(values.start)}&end=${encodeURIComponent(values.end)}&interval=${values.interval}`); setCandles(data.candles); }
    catch (error) { message.error((error as Error).message); } finally { setLoading(false); }
  };
  const startBackfill = async (values: { symbol: string; month: string }) => {
    setLoading(true);
    try { setJob(await json<BackfillJob>("/api/price/backfill/anchor", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(values) })); }
    catch (error) { message.error((error as Error).message); } finally { setLoading(false); }
  };
  const queryCoverage = async (values: { token: string; from: string; to: string; resolution: string }) => {
    setLoading(true);
    try {
      const token = encodeURIComponent(values.token.trim().toLowerCase());
      const [coverageResult, gapResult] = await Promise.all([
        json<CoverageResponse>(`/api/price/coverage?chain=bsc&token=${token}`),
        json<PriceGap[]>(`/api/v2/pricing/56/token/${token}/gaps?from=${encodeURIComponent(values.from)}&to=${encodeURIComponent(values.to)}&resolution=${values.resolution}`),
      ]);
      setCoverage(coverageResult);
      setGaps(gapResult);
    } catch (error) { message.error((error as Error).message); } finally { setLoading(false); }
  };
  const discoverPools = async (values: { from: string; to: string }) => {
    setLoading(true);
    try {
      setDiscovery(await json<PoolDiscovery>("/api/price/pools/discover", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(values) }));
      message.success("Pool Registry 已刷新");
    } catch (error) { message.error((error as Error).message); } finally { setLoading(false); }
  };
  const startGapRepair = async (values: { token: string; from_block: number; to_block: number; from: string; to: string; refresh: boolean }) => {
    setLoading(true);
    try {
      const token = values.token.trim().toLowerCase();
      const registered = await json<{ pools: Pool[] }>(`/api/price/pools?token=${encodeURIComponent(token)}`);
      setPools(registered.pools);
      setDEXJob(await json<DEXJob>("/api/price/gaps/repair", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ...values, token }) }));
      message.success("价格缺口修复已进入 Turbo 队列");
    } catch (error) { message.error((error as Error).message); } finally { setLoading(false); }
  };

  return <div className="price-engine-page">
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <div><h2 style={{ margin: 0 }}>历史价格</h2><div style={{ color: "#667085", marginTop: 4 }}>BSC 分钟级价格、覆盖回填与可审计来源</div></div>
      {health?.status === "degraded" && <Alert type="warning" showIcon message="价格服务降级" description="ClickHouse 暂不可用；外部数据源故障不会被伪装为价格 0。" />}
      <Row gutter={[12, 12]} className="price-engine-health">
        <Col xs={12} sm={6}><Card size="small"><Statistic title="服务" value={health?.status || "检查中"} /></Card></Col>
        <Col xs={12} sm={6}><Card size="small"><Statistic title="ClickHouse" value={health?.clickhouse || "检查中"} /></Card></Col>
        <Col xs={24} sm={12}><Card size="small"><Statistic title="数据根目录" value={health?.root || "--"} valueStyle={{ fontSize: 15 }} /></Card></Col>
      </Row>
      <Tabs items={[
        { key: "search", label: "Token 查询", children: <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <Card size="small" title="指定时间价格"><Form layout="inline" onFinish={queryPoint} initialValues={{ token: "native:56", timestamp: new Date().toISOString() }}><Form.Item name="token" style={{ width: 360, maxWidth: "100%" }} rules={[{ required: true }]}><Input placeholder="Token 合约或 native:56" /></Form.Item><Form.Item name="timestamp" style={{ width: 250, maxWidth: "100%" }} rules={[{ required: true }]}><Input placeholder="RFC3339 UTC" /></Form.Item><Button htmlType="submit" type="primary" loading={loading}>查询</Button></Form>{point && <Descriptions size="small" column={{ xs: 1, sm: 2, lg: 3 }} style={{ marginTop: 16 }} items={[{ key:"price",label:"USD 价格",children:point.price_usd ?? "不可定价"},{key:"source",label:"来源",children:point.source || "--"},{key:"confidence",label:"置信度",children:point.confidence || "--"},{key:"type",label:"价格类型",children:point.price_type},{key:"age",label:"价格年龄",children:point.age_seconds == null ? "--" : `${point.age_seconds}s`},{key:"time",label:"查询时间",children:point.timestamp}]} />}</Card>
          <Card size="small" title="历史 K 线"><Form layout="inline" onFinish={queryCandles} initialValues={{ token:"BNBUSDT", start:new Date(Date.now()-86400000).toISOString(), end:new Date().toISOString(), interval:"1h" }}><Form.Item name="token" style={{ width: 180, maxWidth: "100%" }} rules={[{required:true}]}><Input /></Form.Item><Form.Item name="start" style={{ width: 240, maxWidth: "100%" }} rules={[{required:true}]}><Input /></Form.Item><Form.Item name="end" style={{ width: 240, maxWidth: "100%" }} rules={[{required:true}]}><Input /></Form.Item><Form.Item name="interval" style={{ width: 90, maxWidth: "100%" }}><Select options={["1m","5m","15m","1h","4h","1d"].map(value=>({value,label:value}))} /></Form.Item><Button htmlType="submit" loading={loading}>加载</Button></Form><Table size="small" rowKey="time" pagination={{pageSize:50}} dataSource={candles} scroll={{ x: 900 }} style={{marginTop:16}} columns={[{title:"UTC",dataIndex:"time"},{title:"Open",dataIndex:"open",align:"right"},{title:"High",dataIndex:"high",align:"right"},{title:"Low",dataIndex:"low",align:"right"},{title:"Close",dataIndex:"close",align:"right"},{title:"USD Volume",dataIndex:"volume_usd",align:"right"},{title:"Trades",dataIndex:"trade_count",align:"right"},{title:"Source",dataIndex:"source"}]} /></Card>
        </Space> },
        { key: "quality", label: "覆盖与缺口", children: <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <Card size="small" title="Price Coverage"><Form layout="inline" onFinish={queryCoverage} initialValues={{ token: "0x55d398326f99059ff775485246999027b3197955", from: agoISO(24 * 60 * 60 * 1000), to: nowISO(), resolution: "1m" }}><Form.Item name="token" style={{ width: 360, maxWidth: "100%" }} rules={[{ required: true }]}><Input placeholder="Token 合约" /></Form.Item><Form.Item name="from" style={{ width: 240, maxWidth: "100%" }} rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="to" style={{ width: 240, maxWidth: "100%" }} rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="resolution" style={{ width: 90 }}><Select options={["1m","5m","15m","1h","4h","1d"].map(value=>({value,label:value}))} /></Form.Item><Button htmlType="submit" type="primary" loading={loading}>审计覆盖</Button></Form>
            <Row gutter={[12, 12]} style={{ marginTop: 16 }}><Col xs={12} sm={6}><Statistic title="覆盖状态" value={coverage?.covered ? "已覆盖" : "无价格"} /></Col><Col xs={12} sm={6}><Statistic title="Coverage" value={(coverage?.coverage?.coverage_ratio || 0) * 100} precision={2} suffix="%" /></Col><Col xs={12} sm={6}><Statistic title="分钟数" value={coverage?.coverage?.minute_count ?? 0} /></Col><Col xs={12} sm={6}><Statistic title="Swap 数" value={coverage?.coverage?.trade_count ?? 0} /></Col></Row>
          </Card>
          <Card size="small" title={`连续价格缺口 (${gaps.length})`}><Table size="small" rowKey={(row) => `${row.gap_start}-${row.gap_end}`} pagination={{ pageSize: 20 }} dataSource={gaps} scroll={{ x: 760 }} columns={[{title:"开始 UTC",dataIndex:"gap_start"},{title:"结束 UTC",dataIndex:"gap_end"},{title:"粒度",dataIndex:"resolution"},{title:"缺失桶",dataIndex:"missing_buckets",align:"right"}]} /></Card>
        </Space> },
        { key: "repair", label: "Pool 与缺口修复", children: <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <Card size="small" title="可信 Pool Discovery"><Form layout="inline" onFinish={discoverPools} initialValues={{ from: agoISO(7 * 24 * 60 * 60 * 1000), to: nowISO() }}><Form.Item name="from" style={{ width: 280, maxWidth: "100%" }} rules={[{ required: true }]}><Input placeholder="RFC3339 开始" /></Form.Item><Form.Item name="to" style={{ width: 280, maxWidth: "100%" }} rules={[{ required: true }]}><Input placeholder="RFC3339 结束" /></Form.Item><Button htmlType="submit" loading={loading}>刷新 Pool Registry</Button></Form>{discovery && <Descriptions size="small" column={{ xs: 2, sm: 4 }} style={{ marginTop: 16 }} items={[{key:"found",label:"发现事件",children:discovery.discovered},{key:"written",label:"可信入库",children:discovery.written},{key:"metadata",label:"缺 Metadata",children:discovery.skipped_metadata},{key:"untrusted",label:"非白名单",children:discovery.skipped_untrusted}]} />}</Card>
          <Card size="small" title="⚡ 极速补价格"><Form layout="inline" onFinish={startGapRepair} initialValues={{ token: "0xc988f6d9a589da550c2e5a0bdc7820f0c0bbbc6a", from: agoISO(24 * 60 * 60 * 1000), to: nowISO(), refresh: false }}><Form.Item name="token" style={{ width: 360, maxWidth: "100%" }} rules={[{ required: true }]}><Input placeholder="当前 Token 合约" /></Form.Item><Form.Item name="from_block" label="From Block" rules={[{ required: true }]}><InputNumber min={0} precision={0} placeholder="开始区块" /></Form.Item><Form.Item name="to_block" label="To Block" rules={[{ required: true }]}><InputNumber min={0} precision={0} placeholder="结束区块" /></Form.Item><Form.Item name="from" style={{ width: 250, maxWidth: "100%" }} rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="to" style={{ width: 250, maxWidth: "100%" }} rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="refresh" label="强制重拉" valuePropName="checked"><Switch /></Form.Item><Button htmlType="submit" type="primary" loading={loading}>启动 PRICE_GAP_REPAIR</Button></Form>
            {dexJob && <Descriptions size="small" column={{ xs: 1, sm: 2, lg: 4 }} style={{ marginTop: 16 }} items={[{key:"id",label:"Job ID",children:dexJob.id},{key:"batch",label:"Turbo Batch",children:dexJob.batch_id},{key:"status",label:"状态",children:<Tag>{dexJob.status}</Tag>},{key:"pools",label:"可信 Pool",children:dexJob.result?.pools ?? dexJob.pools.length},{key:"swaps",label:"Canonical Swap",children:dexJob.result?.swaps ?? "--"},{key:"bars",label:"1m Price",children:dexJob.result?.bars ?? "--"},{key:"error",label:"错误",children:dexJob.error || "--"}]} />}
          </Card>
          <Card size="small" title={`本次主池 (${pools.length})`}><Table size="small" rowKey="pool_address" pagination={false} dataSource={pools} scroll={{ x: 980 }} columns={[{title:"Protocol",dataIndex:"protocol_id"},{title:"版本",dataIndex:"version"},{title:"Pool",dataIndex:"pool_address",ellipsis:true},{title:"Token0",dataIndex:"token0",ellipsis:true},{title:"Token1",dataIndex:"token1",ellipsis:true},{title:"Fee bps",dataIndex:"fee_bps",align:"right"},{title:"可信",dataIndex:"verified",render:(value:boolean)=><Tag color={value?"green":"default"}>{value?"VERIFIED":"UNVERIFIED"}</Tag>}]} /></Card>
        </Space> },
        { key: "backfill", label: "Anchor 回填", children: <Card size="small" title="Binance Public Data 1m"><Form layout="inline" onFinish={startBackfill} initialValues={{symbol:"BNBUSDT",month:"2025-01"}}><Form.Item name="symbol" style={{ width: 150, maxWidth: "100%" }}><Select options={["BNBUSDT","ETHUSDT","BTCUSDT","USDCUSDT"].map(value=>({value,label:value}))} /></Form.Item><Form.Item name="month" style={{ width: 120, maxWidth: "100%" }} rules={[{pattern:/^\d{4}-\d{2}$/,message:"YYYY-MM"}]}><Input /></Form.Item><Button htmlType="submit" type="primary" loading={loading}>开始回填</Button></Form>{job && <Descriptions size="small" column={{ xs: 1, sm: 2, lg: 3 }} style={{marginTop:16}} items={[{key:"id",label:"Job ID",children:job.id},{key:"status",label:"状态",children:<Tag>{job.status}</Tag>},{key:"rows",label:"写入行",children:job.result?.rows ?? "--"},{key:"checksum",label:"Checksum",children:job.result?.checksum || "--"},{key:"reused",label:"覆盖复用",children:job.result?.reused ? "是" : "否"},{key:"error",label:"错误",children:job.error || "--"}]} />}</Card> },
        { key: "providers", label: "Provider 健康", children: <Table size="small" rowKey="name" pagination={false} dataSource={health?.providers || []} columns={[{title:"Provider",dataIndex:"name"},{title:"状态",dataIndex:"status",render:(value:string)=><Tag>{value}</Tag>},{title:"说明",dataIndex:"detail",render:(value?:string)=>value||"--"}]} /> },
      ]} />
    </Space>
  </div>;
}
