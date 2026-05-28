import { getJson, postJson, parseJsonResponse } from '../../api/client';

export type DBType = 'mysql' | 'postgresql' | 'pgsql';

export type DBConnection = {
  id?: string;
  name: string;
  type: DBType;
  host: string;
  port: number;
  defaultDatabase?: string;
  username: string;
  password?: string;
  savePassword?: boolean;
  hasPassword?: boolean;
  ssl?: boolean;
  timeoutSeconds?: number;
  remark?: string;
};

export type DBColumn = {
  name: string;
  dataType: string;
  nullable: boolean;
  default?: string;
  primaryKey?: boolean;
  indexed?: boolean;
  comment?: string;
  length?: string;
  precision?: string;
};

export type DBTableRef = {
  connectionId: string;
  database: string;
  schema?: string;
  table: string;
};

export type DBFieldMapping = {
  sourceColumn?: string;
  sourceType?: string;
  targetField: string;
  targetType?: string;
  transform?: string;
  required?: boolean;
  confidence?: number;
};

export type DBMappingRule = DBTableRef & {
  id?: string;
  connectionType?: DBType;
  sourceColumnsHash?: string;
  targetVersion?: string;
  mappings: DBFieldMapping[];
};

export type DBPreviewResponse = {
  columns: DBColumn[];
  rows: Record<string, unknown>[];
  page: number;
  pageSize: number;
  returnedRows: number;
  estimatedRows?: number;
  truncated?: boolean;
  elapsedMs?: number;
};

export type DBImportTask = {
  id: string;
  name: string;
  status: string;
  progress: {
    totalRows: number;
    processedRows: number;
    successRows: number;
    failedRows: number;
    skippedRows: number;
    speedRowsPerSecond: number;
  };
  errors?: Array<Record<string, unknown>>;
  session_id?: string;
  columns?: string[];
  files?: string[];
  sample?: Record<string, unknown>[];
};

export type DBEditPayload = DBTableRef & {
  values?: Record<string, unknown>;
  keys?: Record<string, unknown>;
};

async function request<T>(url: string, init: RequestInit, fallback: string): Promise<T> {
  const response = await fetch(url, init);
  const payload = await parseJsonResponse<T & { detail?: string }>(response, fallback);
  if (!response.ok) throw new Error(payload.detail || fallback);
  return payload;
}

export async function listDBConnections() {
  const { response, payload } = await getJson<{ items?: DBConnection[]; detail?: string }>('/api/db/connections', '读取数据库连接失败');
  if (!response.ok) throw new Error(payload.detail || '读取数据库连接失败');
  return payload.items ?? [];
}

export async function saveDBConnection(connection: DBConnection) {
  const method = connection.id ? 'PUT' : 'POST';
  const url = connection.id ? `/api/db/connections/${encodeURIComponent(connection.id)}` : '/api/db/connections';
  return request<DBConnection>(url, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(connection),
  }, '保存数据库连接失败');
}

export async function deleteDBConnection(id: string) {
  return request<{ ok: boolean }>(`/api/db/connections/${encodeURIComponent(id)}`, { method: 'DELETE' }, '删除数据库连接失败');
}

export async function testDBConnection(connection: DBConnection) {
  if (connection.id) {
    const { response, payload } = await postJson<{ ok?: boolean; detail?: string }>(`/api/db/connections/${encodeURIComponent(connection.id)}/test`, {}, '测试数据库连接失败');
    if (!response.ok) throw new Error(payload.detail || '测试数据库连接失败');
    return payload;
  }
  const { response, payload } = await postJson<{ ok?: boolean; detail?: string }>('/api/db/connections/test', connection, '测试数据库连接失败');
  if (!response.ok) throw new Error(payload.detail || '测试数据库连接失败');
  return payload;
}

export async function loadDBDatabases(connectionId: string) {
  const { response, payload } = await getJson<{ items?: string[]; detail?: string }>(`/api/db/connections/${encodeURIComponent(connectionId)}/databases`, '读取数据库列表失败');
  if (!response.ok) throw new Error(payload.detail || '读取数据库列表失败');
  return payload.items ?? [];
}

export async function loadDBSchemas(connectionId: string, database: string) {
  const { response, payload } = await getJson<{ items?: string[]; detail?: string }>(`/api/db/connections/${encodeURIComponent(connectionId)}/schemas?database=${encodeURIComponent(database)}`, '读取 schema 失败');
  if (!response.ok) throw new Error(payload.detail || '读取 schema 失败');
  return payload.items ?? [];
}

export async function loadDBTables(ref: Omit<DBTableRef, 'table'>) {
  const query = new URLSearchParams({ database: ref.database });
  if (ref.schema) query.set('schema', ref.schema);
  const { response, payload } = await getJson<{ items?: Array<{ name: string; type: string }>; detail?: string }>(`/api/db/connections/${encodeURIComponent(ref.connectionId)}/tables?${query}`, '读取数据表失败');
  if (!response.ok) throw new Error(payload.detail || '读取数据表失败');
  return payload.items ?? [];
}

export async function loadDBColumns(ref: DBTableRef) {
  const query = new URLSearchParams({ database: ref.database, table: ref.table });
  if (ref.schema) query.set('schema', ref.schema);
  const { response, payload } = await getJson<{ items?: DBColumn[]; detail?: string }>(`/api/db/connections/${encodeURIComponent(ref.connectionId)}/columns?${query}`, '读取表结构失败');
  if (!response.ok) throw new Error(payload.detail || '读取表结构失败');
  return payload.items ?? [];
}

export async function previewDBTable(ref: DBTableRef, page: number, pageSize: number, search = '') {
  const body = { ...ref, page, pageSize, search };
  const { response, payload } = await postJson<DBPreviewResponse & { detail?: string }>(search ? '/api/db/search' : '/api/db/preview', body, '预览表数据失败');
  if (!response.ok) throw new Error(payload.detail || '预览表数据失败');
  return payload;
}

export async function executeDBQuery(connectionId: string, database: string, schema: string | undefined, sql: string, page: number, pageSize: number) {
  const { response, payload } = await postJson<DBPreviewResponse & { detail?: string }>('/api/db/query', {
    connectionId,
    database,
    schema,
    sql,
    page,
    pageSize,
    allowWrite: false,
  }, '执行 SQL 查询失败');
  if (!response.ok) throw new Error(payload.detail || '执行 SQL 查询失败');
  return payload;
}

export async function editDBTable(operation: 'insert' | 'update' | 'delete', payload: DBEditPayload) {
  const method = operation === 'update' ? 'PUT' : operation === 'delete' ? 'DELETE' : 'POST';
  return request<{ affectedRows: number }>(`/api/db/table/${operation}`, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  }, '提交表数据变更失败');
}

export async function autoDBMapping(ref: DBTableRef) {
  const { response, payload } = await postJson<{ rule: DBMappingRule; reused: boolean; targetFields: DBFieldMapping[]; detail?: string }>('/api/db/mappings/auto', ref, '自动字段映射失败');
  if (!response.ok) throw new Error(payload.detail || '自动字段映射失败');
  return payload;
}

export async function confirmDBMapping(rule: DBMappingRule) {
  const { response, payload } = await postJson<DBMappingRule & { detail?: string }>('/api/db/mappings/confirm', rule, '保存字段映射失败');
  if (!response.ok) throw new Error(payload.detail || '保存字段映射失败');
  return payload;
}

export async function createDBImportTask(name: string, ref: DBTableRef, mappings: DBFieldMapping[]) {
  const { response, payload } = await postJson<DBImportTask & { detail?: string }>('/api/db/import/tasks', {
    name,
    tables: [{ ...ref, mappings }],
  }, '创建数据库导入任务失败');
  if (!response.ok) throw new Error(payload.detail || '创建数据库导入任务失败');
  return payload;
}

export async function startDBImportTask(id: string) {
  const { response, payload } = await postJson<DBImportTask & { detail?: string }>(`/api/db/import/tasks/${encodeURIComponent(id)}/start`, {}, '启动数据库导入任务失败');
  if (!response.ok) throw new Error(payload.detail || '启动数据库导入任务失败');
  return payload;
}

export async function getDBImportTask(id: string) {
  const { response, payload } = await getJson<DBImportTask & { detail?: string }>(`/api/db/import/tasks/${encodeURIComponent(id)}`, '获取导入任务状态失败');
  if (!response.ok) throw new Error(payload.detail || '获取导入任务状态失败');
  return payload;
}
