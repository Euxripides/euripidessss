import {
  ApartmentOutlined,
  BankOutlined,
  DatabaseOutlined,
  TeamOutlined,
  UserOutlined,
  WalletOutlined,
} from '@ant-design/icons';
import { Tag } from 'antd';
import { GRAPH_LAYER_COLORS, type EntityKind } from './flowTypes';

export function renderFlowNodeLabel(label: string, tags: string[], kind: EntityKind) {
  const Icon = iconForEntityKind(kind);
  return (
    <div className={`flow-entity entity-${kind}`}>
      <span className="entity-icon"><Icon /></span>
      <strong title={label}>{label}</strong>
      {!!tags.length && <div className="node-tags">{tags.map((tag) => <Tag key={tag}>{tag}</Tag>)}</div>}
    </div>
  );
}

export function detectEntityKind(role: string, label: string): EntityKind {
  const text = String(label);
  if (text.includes('传销') || text.includes('人员') || text.includes('团伙') || text.includes('群')) return 'group';
  if (/公司|有限|科技|管理|咨询|服务|信息|网络|文化|传媒|贸易/.test(text)) return 'company';
  if (/商户|支付|结算|收款|店|平台/.test(text)) return 'merchant';
  if (/^\d{8,}$/.test(text) || /银行|卡|账户|账号/.test(text) || role === 'source') return 'account';
  if (/^[\u4e00-\u9fa5]{2,4}$/.test(text) || role === 'self') return 'person';
  return 'unknown';
}

export function miniMapNodeColor(kind: string) {
  return {
    person: '#2563eb',
    group: '#7c3aed',
    account: '#f59e0b',
    company: '#059669',
    merchant: '#dc2626',
    unknown: '#64748b',
  }[kind] ?? '#64748b';
}

export function miniMapNodeStrokeColor(kind: string) {
  return {
    person: '#1d4ed8',
    group: '#6d28d9',
    account: '#b45309',
    company: '#047857',
    merchant: '#b91c1c',
    unknown: '#334155',
  }[kind] ?? '#334155';
}

export function graphLayerColor(index: number) {
  return GRAPH_LAYER_COLORS[index % GRAPH_LAYER_COLORS.length];
}

function iconForEntityKind(kind: EntityKind) {
  return {
    person: UserOutlined,
    group: TeamOutlined,
    account: BankOutlined,
    company: ApartmentOutlined,
    merchant: WalletOutlined,
    unknown: DatabaseOutlined,
  }[kind];
}
