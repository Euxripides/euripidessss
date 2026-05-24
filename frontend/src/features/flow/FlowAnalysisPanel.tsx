import { Button, Collapse, Input, Space, Table } from 'antd';
import type { FlowEdgeRow, SubjectStat } from './flowTypes';

type InsightItem = {
  key: string;
  title: string;
  detail: string;
  subjects: string[];
};

export function FlowAnalysisPanel(props: {
  prompt: string;
  onPromptChange: (value: string) => void;
  loading: boolean;
  filterPayload: Record<string, unknown>;
  onSmartAnalyze: (values: Record<string, unknown> & { prompt: string }) => Promise<void>;
  analysisReport: string;
  visibleTotal: number;
  strongest?: { amount: number } | null;
  relationshipRows: FlowEdgeRow[];
  insightItems: InsightItem[];
  subjectStats: SubjectStat[];
  onSubjectIdsChange: (ids: string[]) => void;
}) {
  return (
    <Collapse
      className="inspector-collapse"
      defaultActiveKey={['analysis']}
      items={[
        {
          key: 'analysis',
          label: '数据分析',
          children: (
            <Space direction="vertical" size="middle" className="full">
              <Input.TextArea
                rows={4}
                value={props.prompt}
                onChange={(event) => props.onPromptChange(event.target.value)}
                placeholder="智能分析，例如：分析张三在近三年转给王五的交易流水；或：根据资金流向图生成资金分析报告"
              />
              <Button
                type="primary"
                loading={props.loading}
                onClick={() =>
                  props.onSmartAnalyze({
                    prompt: props.prompt,
                    ...props.filterPayload,
                  })
                }
              >
                智能分析
              </Button>
              {props.analysisReport && <pre className="analysis-report">{props.analysisReport}</pre>}
              <div className="insight-strip">
                <div>
                  <span>过滤后金额</span>
                  <strong>{formatMoney(props.visibleTotal)}</strong>
                </div>
                <div>
                  <span>最大关系</span>
                  <strong>{props.strongest ? formatMoney(props.strongest.amount) : '-'}</strong>
                </div>
              </div>

              <div className="inspector-section">
                <div className="inspector-title">
                  <strong>关系清单</strong>
                  <span>点击聚焦</span>
                </div>
                <Table
                  size="small"
                  rowKey="id"
                  dataSource={props.relationshipRows.slice(0, 12)}
                  pagination={false}
                  onRow={(record) => ({
                    onClick: () => props.onSubjectIdsChange([record.source, record.target]),
                  })}
                  columns={[
                    {
                      title: '流向',
                      dataIndex: 'sourceLabel',
                      ellipsis: true,
                      render: (_, record) => (
                        <span className="flow-route">
                          {record.sourceLabel}
                          <span>→</span>
                          {record.targetLabel}
                        </span>
                      ),
                    },
                    { title: '金额', dataIndex: 'amount', width: 92, align: 'right', render: (value) => formatMoney(Number(value)) },
                    { title: '笔', dataIndex: 'tx_count', width: 54, align: 'right' },
                  ]}
                />
              </div>

              <div className="inspector-section">
                <div className="inspector-title">
                  <strong>异常线索</strong>
                  <span>自动从可见关系提取</span>
                </div>
                <div className="insight-list">
                  {props.insightItems.map((item) => (
                    <button key={item.key} type="button" onClick={() => props.onSubjectIdsChange(item.subjects)}>
                      <strong>{item.title}</strong>
                      <span>{item.detail}</span>
                    </button>
                  ))}
                  {!props.insightItems.length && <div className="empty-mini">当前筛选下暂无明显线索</div>}
                </div>
              </div>

              <div className="inspector-section">
                <div className="inspector-title">
                  <strong>重点主体</strong>
                  <span>按关联金额</span>
                </div>
                <div className="subject-rank">
                  {props.subjectStats.map((item) => (
                    <button key={item.id} type="button" onClick={() => props.onSubjectIdsChange([item.id])}>
                      <span>{item.label}</span>
                      <strong>{formatMoney(item.amount)}</strong>
                      <em>{item.degree} 关系 · {item.tx_count} 笔</em>
                    </button>
                  ))}
                </div>
              </div>
            </Space>
          ),
        },
      ]}
    />
  );
}

function formatMoney(value: number) {
  return Number(value || 0).toLocaleString('zh-CN', { maximumFractionDigits: 0 });
}
