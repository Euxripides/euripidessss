export const FLOW_TEMPLATE_COLUMNS = ['交易方户名', '交易方账户', '交易方身份证号', '交易方标签', '交易时间', '交易金额', '收付标志', '交易余额', '交易对手账卡号', '对手户名', '对手身份证号', '对手标签', '交易流水号', '摘要说明', '备注'];

export const SOURCE_FILTER_FIELDS = [
  { label: '交易方户名', value: 'source_name_column', normalizedColumn: '交易户名', keywords: ['交易方户名', '交易户名', '主体户名', '主体名称', '户名', '姓名', '名称', 'name'] },
  { label: '交易方账户', value: 'source_account_column', normalizedColumn: '交易账号', keywords: ['交易方账户', '交易账号', '交易卡号', '主体账号', '主体账户', '主体卡号', '账号', '账户', '卡号', 'account', 'card'] },
  { label: '交易方身份证号', value: 'source_id_column', normalizedColumn: '交易方身份证号', keywords: ['交易方身份证号', '交易证件号码', '主体身份证号', '主体证件号', '身份证号', '证件号码', '证件号', 'id'] },
] as const;

export const TARGET_FILTER_FIELDS = [
  { label: '交易对手账卡号', value: 'target_card_column', normalizedColumn: '交易对手账卡号', keywords: ['交易对手账卡号', '对手账卡号', '对手账号', '对方账号', '对手卡号', '对方卡号', '收款方账号', '付款方账号', 'target', 'card', 'account'] },
  { label: '对手户名', value: 'target_name_column', normalizedColumn: '对手户名', keywords: ['对手户名', '交易对手户名', '对方户名', '对方姓名', '对手姓名', '对方名称', '交易对手', '户名', '姓名', '名称', 'name'] },
  { label: '对手身份证号', value: 'target_id_column', normalizedColumn: '对手身份证号', keywords: ['对手身份证号', '对手证件号', '对方身份证号', '对方证件号', '证件号码', '证件号', '身份证号', 'id'] },
] as const;

export const DETAIL_FILTER_FIELDS = [
  { label: '交易流水号', value: 'serial_column', normalizedColumn: '交易流水号', keywords: ['交易流水号', '流水号', '交易号', '订单号', '商户订单号', '微信支付订单号', '支付宝交易号', 'serial', 'transaction'] },
  { label: '摘要说明', value: 'summary_column', normalizedColumn: '摘要说明', keywords: ['摘要说明', '摘要', '交易摘要', '商品说明', '交易说明', '用途', 'description', 'summary'] },
  { label: '备注', value: 'remark_column', normalizedColumn: '备注', keywords: ['备注', '附言', '说明', 'remark', 'memo'] },
] as const;

export const DIRECTION_OPTIONS = [{ label: '进', value: '进' }, { label: '出', value: '出' }];
export const FLOW_IMPORT_EXTENSIONS = ['.xlsx', '.csv', '.xls'];
export const FLOW_NODE_ICON_SIZE = 28;
export const FLOW_NODE_LABEL_WIDTH = 220;
export const FLOW_NODE_LABEL_TOP = 32;
export const FLOW_NODE_LABEL_LINE_HEIGHT = 16.2;
export const FLOW_TEMPLATE_MAPPING = {
  source_column: '交易方户名',
  source_account_column: '交易方账户',
  source_name_column: '交易方户名',
  source_id_column: '交易方身份证号',
  source_label_column: '交易方标签',
  target_column: '对手户名',
  target_card_column: '交易对手账卡号',
  target_name_column: '对手户名',
  target_id_column: '对手身份证号',
  target_label_column: '对手标签',
  serial_column: '交易流水号',
  summary_column: '摘要说明',
  remark_column: '备注',
  amount_column: '交易金额',
  time_column: '交易时间',
  direction_column: '收付标志',
};

export type EntityKind = 'person' | 'group' | 'account' | 'company' | 'merchant' | 'unknown';

export type ManualNodeFormValues = {
  label: string;
  kind?: EntityKind;
  outgoingEnabled?: boolean;
  incomingEnabled?: boolean;
  outgoingLinks?: ManualNodeLink[];
  incomingLinks?: ManualNodeLink[];
  lineStyle?: 'solid' | 'dashed';
  lineWidth?: number;
};

export type ManualNodeLink = {
  nodeId?: string;
  amount?: number;
  count?: number;
};

export type NodeConnectionFormValues = {
  outgoingEnabled?: boolean;
  incomingEnabled?: boolean;
  outgoingLinks?: ManualNodeLink[];
  incomingLinks?: ManualNodeLink[];
  lineStyle?: 'solid' | 'dashed';
  lineWidth?: number;
};

export type FlowFieldMapping = Partial<Record<keyof typeof FLOW_TEMPLATE_MAPPING, string>>;
export type SourceFilterField = typeof SOURCE_FILTER_FIELDS[number]['value'];
export type SourceFilterState = { field: SourceFilterField; values: string[] };
export type SourceFilterPayload = { column: string; values: string[] };
export type TargetFilterField = typeof TARGET_FILTER_FIELDS[number]['value'];
export type TargetFilterState = { field: TargetFilterField; values: string[] };
export type TargetFilterPayload = { column: string; values: string[] };
export type DetailFilterField = typeof DETAIL_FILTER_FIELDS[number]['value'];
export type DetailFilterState = { field: DetailFilterField; values: string[] };
export type DetailFilterPayload = { column: string; values: string[] };

export type GraphDetailContext = {
  kind: 'cleaned' | 'imported' | 'none';
  jobId?: string;
  sessionId?: string;
  sourceColumn?: string;
  sourceAccountColumn?: string;
  sourceNameColumn?: string;
  sourceIdColumn?: string;
  sourceLabelColumn?: string;
  targetColumn?: string;
  targetCardColumn?: string;
  targetNameColumn?: string;
  targetIdColumn?: string;
  targetLabelColumn?: string;
  serialColumn?: string;
  summaryColumn?: string;
  remarkColumn?: string;
  amountColumn?: string;
  timeColumn?: string;
  directionColumn?: string;
  sourceValues?: string[];
  sourceFilters?: SourceFilterPayload[];
  sourceLabelValues?: string[];
  targetValues?: string[];
  targetFilters?: TargetFilterPayload[];
  targetLabelValues?: string[];
  detailFilters?: DetailFilterPayload[];
  directionValues?: string[];
  startDate?: string;
  endDate?: string;
};

export type GraphLayer = {
  id: string;
  label: string;
  rows: number;
};

export type DirectionRulePending = {
  values: string[];
  source: 'build' | 'mapping';
  payload: Record<string, unknown> & {
    source_column?: string;
    target_column?: string;
    amount_column?: string;
    time_column?: string;
    direction_column?: string;
  };
  mapping?: FlowFieldMapping;
};

export type NetworkMode = 'local' | 'external';

export type TransferStatus = {
  visible: boolean;
  phase: 'idle' | 'packing' | 'uploading' | 'processing' | 'downloading' | 'done' | 'error';
  mode: NetworkMode;
  label: string;
  percent: number;
  speed: number;
  loaded: number;
  total: number;
};

export type EdgeLabelMode = 'amount_count' | 'amount' | 'count' | 'none';
export type TimeWindow = 'all' | '30' | '90' | '180' | '365';

export type FlowEdgeRow = {
  id: string;
  source: string;
  target: string;
  sourceLabel: string;
  targetLabel: string;
  amount: number;
  tx_count: number;
};

export type EdgeDetailPayload = {
  job_id: string;
  source: string;
  target: string;
  total_rows: number;
  returned_rows: number;
  amount: number;
  columns: string[];
  rows: Record<string, unknown>[];
  truncated: boolean;
};

export type SubjectStat = {
  id: string;
  label: string;
  amount: number;
  tx_count: number;
  degree: number;
};

export type SubjectDetailStats = {
  amountIn: number;
  amountOut: number;
  inCount: number;
  outCount: number;
  inPeers: number;
  outPeers: number;
  firstTime: string;
  lastTime: string;
  lastOutTime: string;
  features: string[];
  manualFeatures: string[];
};

export type HistoryItem = {
  job_id: string;
  name: string;
  size: number;
  updated_at: number;
};

export type ImportedDataset = {
  session_id: string;
  rows: number;
  columns: string[];
  files: string[];
  sample: Record<string, unknown>[];
  signature?: string;
  mapping_rule?: {
    signature: string;
    mapping: FlowFieldMapping;
  } | null;
};

export type FlowImportProgress = {
  visible: boolean;
  percent: number;
  status: 'normal' | 'active' | 'success' | 'exception';
  text: string;
};

export type FlowBuildStatus = {
  visible: boolean;
  status: 'normal' | 'active' | 'success' | 'exception';
  text: string;
};

export type LineType = 'straight' | 'smoothstep' | 'step';
export type ArrowMode = 'forward' | 'reverse' | 'both' | 'none';
export type EdgeLinePattern = 'solid' | 'dashed';
export type EdgePatch = {
  customLabel?: string;
  lineWidth?: number;
  lineColor?: string;
  arrowMode?: ArrowMode;
  linePattern?: EdgeLinePattern;
  amount?: number;
  tx_count?: number;
  avg_amount?: number;
  max_amount?: number;
  first_time?: string;
  last_time?: string;
};

export type GraphExportFormat = 'png' | 'jpeg' | 'webp' | 'svg' | 'json' | 'csv' | 'graphml' | 'dot' | 'mermaid' | 'drawio' | 'xmind' | 'zip';
export type CanvasImageExportFormat = Extract<GraphExportFormat, 'png' | 'jpeg' | 'webp' | 'svg'>;
export type GraphExportNode = {
  id: string;
  label: string;
  kind: string;
  layer: string;
  x: number;
  y: number;
  tags: string[];
};
export type GraphExportEdge = {
  id: string;
  source: string;
  sourceLabel: string;
  target: string;
  targetLabel: string;
  label: string;
  amount: number;
  tx_count: number;
  avg_amount: number;
  max_amount: number;
  first_time: string;
  last_time: string;
  layer: string;
};
export type GraphExportPayload = {
  exported_at: string;
  meta: Record<string, unknown>;
  layers: GraphLayer[];
  nodes: GraphExportNode[];
  edges: GraphExportEdge[];
};
export type DynamicAnchor = {
  id: string;
  side: 'top' | 'right' | 'bottom' | 'left';
  offset: number;
  x?: number;
  y?: number;
};

export type NodeGeometry = {
  originX: number;
  originY: number;
  x: number;
  y: number;
  width: number;
  height: number;
  centerX: number;
  centerY: number;
  rects: GeometryRect[];
};

export type GeometryRect = {
  x: number;
  y: number;
  width: number;
  height: number;
};

export type LayoutTreeEdge = {
  source: string;
  target: string;
  amount: number;
};

export const ENTITY_KIND_OPTIONS: Array<{ value: EntityKind; label: string }> = [
  { value: 'person', label: '人员' },
  { value: 'group', label: '群体' },
  { value: 'account', label: '银行卡/账户' },
  { value: 'company', label: '公司' },
  { value: 'merchant', label: '商户/支付' },
  { value: 'unknown', label: '未知主体' },
];

export const GRAPH_LAYER_COLORS = ['#2563eb', '#7c3aed', '#059669', '#d97706', '#dc2626', '#0891b2', '#be185d', '#4f46e5'];

export const DIRECTION_FILTER_MAP: Record<string, '进' | '出'> = {
  支出: '出',
  转出: '出',
  付款: '出',
  出账: '出',
  借: '出',
  借方: '出',
  D: '出',
  收入: '进',
  转入: '进',
  收款: '进',
  入账: '进',
  贷: '进',
  贷方: '进',
  C: '进',
};
