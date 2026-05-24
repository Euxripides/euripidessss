import {
  ApartmentOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  ExportOutlined,
  FolderOpenOutlined,
  FunctionOutlined,
  ImportOutlined,
  PlusCircleOutlined,
  PlusOutlined,
  ReloadOutlined,
  TableOutlined,
} from '@ant-design/icons';
import { Alert, Button, Checkbox, Empty, Form, Input, InputNumber, Modal, Progress, Select, Space, Table, Tabs, Tag, Tooltip, Tree, message, notification } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { DataNode } from 'antd/es/tree';
import { useEffect, useMemo, useState } from 'react';
import type { ImportedDataset } from './flowTypes';
import {
  autoDBMapping,
  confirmDBMapping,
  createDBImportTask,
  deleteDBConnection,
  editDBTable,
  executeDBQuery,
  listDBConnections,
  loadDBColumns,
  loadDBDatabases,
  loadDBSchemas,
  loadDBTables,
  previewDBTable,
  saveDBConnection,
  startDBImportTask,
  testDBConnection,
  type DBColumn,
  type DBConnection,
  type DBFieldMapping,
  type DBImportTask,
  type DBMappingRule,
  type DBTableRef,
} from './dbImportApi';
import './db-import.css';

type TableItem = { name: string; type: string };

const objectGroups = [
  { key: 'tables', label: '表', icon: <TableOutlined />, disabled: false },
  { key: 'views', label: '视图', icon: <ApartmentOutlined />, disabled: true },
  { key: 'materialized_views', label: '实体化视图', icon: <DatabaseOutlined />, disabled: true },
  { key: 'functions', label: '函数', icon: <FunctionOutlined />, disabled: true },
  { key: 'queries', label: '查询', icon: <FolderOpenOutlined />, disabled: true },
  { key: 'backups', label: '备份', icon: <DatabaseOutlined />, disabled: true },
];

const defaultConnection: DBConnection = {
  name: '',
  type: 'mysql',
  host: 'localhost',
  port: 3306,
  username: '',
  password: '',
  savePassword: false,
  ssl: false,
  timeoutSeconds: 10,
};

export function DBImportModal(props: {
  open: boolean;
  onClose: () => void;
  onImported: (dataset: ImportedDataset) => void;
}) {
  const [form] = Form.useForm<DBConnection>();
  const [connections, setConnections] = useState<DBConnection[]>([]);
  const [editing, setEditing] = useState<DBConnection | null>(null);
  const [connectionOpen, setConnectionOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [selectedConnection, setSelectedConnection] = useState<DBConnection | null>(null);
  const [databases, setDatabases] = useState<string[]>([]);
  const [schemas, setSchemas] = useState<string[]>([]);
  const [tables, setTables] = useState<TableItem[]>([]);
  const [selectedDatabase, setSelectedDatabase] = useState('');
  const [selectedSchema, setSelectedSchema] = useState('');
  const [selectedTable, setSelectedTable] = useState('');
  const [columns, setColumns] = useState<DBColumn[]>([]);
  const [preview, setPreview] = useState<Record<string, unknown>[]>([]);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(100);
  const [search, setSearch] = useState('');
  const [mappingRule, setMappingRule] = useState<DBMappingRule | null>(null);
  const [mappingConfirmed, setMappingConfirmed] = useState(false);
  const [task, setTask] = useState<DBImportTask | null>(null);
  const [querySql, setQuerySql] = useState('select * from ');
  const [queryRows, setQueryRows] = useState<Record<string, unknown>[]>([]);
  const [queryColumns, setQueryColumns] = useState<DBColumn[]>([]);
  const [editOperation, setEditOperation] = useState<'insert' | 'update' | 'delete'>('insert');
  const [editValues, setEditValues] = useState('{\n  "字段名": "值"\n}');
  const [editKeys, setEditKeys] = useState('{\n  "主键字段": "值"\n}');
  const [activeTab, setActiveTab] = useState('objects');
  const [expandedTreeKeys, setExpandedTreeKeys] = useState<React.Key[]>([]);
  const [objectGroup, setObjectGroup] = useState('tables');

  useEffect(() => {
    if (props.open) refreshConnections();
  }, [props.open]);

  const tableRef = useMemo<DBTableRef | null>(() => {
    if (!selectedConnection?.id || !selectedDatabase || !selectedTable) return null;
    return {
      connectionId: selectedConnection.id,
      database: selectedDatabase,
      schema: selectedSchema,
      table: selectedTable,
    };
  }, [selectedConnection?.id, selectedDatabase, selectedSchema, selectedTable]);

  const requiredMissing = (mappingRule?.mappings ?? []).some((item) => item.required && !item.sourceColumn);
  const lowConfidence = (mappingRule?.mappings ?? []).some((item) => item.required && (item.confidence ?? 0) < 70);
  const canImport = !!tableRef && !!mappingRule && mappingConfirmed && !requiredMissing;
  const treeSelectedKey = selectedTable
    ? `table:${selectedDatabase}:${selectedSchema}:${selectedTable}`
    : selectedSchema
      ? `schema:${selectedDatabase}:${selectedSchema}`
      : selectedDatabase
        ? `db:${selectedDatabase}`
        : selectedConnection?.id
          ? `conn:${selectedConnection.id}`
          : undefined;
  const selectedContext = selectedConnection
    ? [selectedConnection.name, selectedDatabase, selectedSchema, selectedTable].filter(Boolean).join(' / ')
    : '未选择连接';
  const treeData = useMemo<DataNode[]>(() => connections.map((conn) => ({
    key: `conn:${conn.id}`,
    title: conn.name,
    icon: <DatabaseOutlined />,
    children: conn.id === selectedConnection?.id ? databases.map((db) => ({
      key: `db:${db}`,
      title: db,
      icon: <DatabaseOutlined />,
      children: db === selectedDatabase ? schemas.map((schema) => ({
        key: `schema:${db}:${schema}`,
        title: schema,
        icon: <DatabaseOutlined />,
        children: schema === selectedSchema ? tables.map((table) => ({
          key: `table:${db}:${schema}:${table.name}`,
          title: table.name,
          icon: <TableOutlined />,
          isLeaf: true,
        })) : undefined,
      })) : undefined,
    })) : undefined,
  })), [connections, databases, schemas, selectedConnection?.id, selectedDatabase, selectedSchema, tables]);
  const objectRows = tables.map((table) => ({
    key: table.name,
    name: table.name,
    type: table.type,
    rows: '',
    comment: '',
  }));

  async function refreshConnections() {
    setLoading(true);
    try {
      const items = await listDBConnections();
      setConnections(items);
      if (!selectedConnection && items[0]) setSelectedConnection(items[0]);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取数据库连接失败');
    } finally {
      setLoading(false);
    }
  }

  async function handleConnectionSelect(id: string) {
    const conn = connections.find((item) => item.id === id) ?? null;
    setSelectedConnection(conn);
    setDatabases([]);
    setSchemas([]);
    setTables([]);
    setSelectedDatabase('');
    setSelectedSchema('');
    setSelectedTable('');
    setPreview([]);
    setColumns([]);
    setMappingRule(null);
    setMappingConfirmed(false);
    if (!conn?.id) return;
    setExpandedTreeKeys((keys) => [...new Set([...keys, `conn:${conn.id}`])]);
    await loadDatabases(conn.id);
  }

  async function loadDatabases(connectionId = selectedConnection?.id) {
    if (!connectionId) return;
    setLoading(true);
    try {
      setDatabases(await loadDBDatabases(connectionId));
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取数据库失败');
    } finally {
      setLoading(false);
    }
  }

  async function loadSchemas(database: string) {
    if (!selectedConnection?.id) return;
    setSelectedDatabase(database);
    setSelectedSchema('');
    setSelectedTable('');
    setTables([]);
    setPreview([]);
    setColumns([]);
    setMappingRule(null);
    setLoading(true);
    try {
      const items = await loadDBSchemas(selectedConnection.id, database);
      setSchemas(items);
      const schema = selectedConnection.type === 'mysql' ? database : items.includes('public') ? 'public' : items[0] ?? '';
      setSelectedSchema(schema);
      setActiveTab('objects');
      setObjectGroup('tables');
      setExpandedTreeKeys((keys) => [...new Set([...keys, `conn:${selectedConnection.id}`, `db:${database}`, `schema:${database}:${schema}`])]);
      await loadTables(database, schema);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取 schema 失败');
    } finally {
      setLoading(false);
    }
  }

  async function loadTables(database = selectedDatabase, schema = selectedSchema) {
    if (!selectedConnection?.id || !database) return;
    setSelectedDatabase(database);
    setSelectedSchema(schema);
    setSelectedTable('');
    setPreview([]);
    setColumns([]);
    setMappingRule(null);
    setMappingConfirmed(false);
    setActiveTab('objects');
    setObjectGroup('tables');
    setLoading(true);
    try {
      setTables(await loadDBTables({ connectionId: selectedConnection.id, database, schema }));
    } catch (error) {
      message.error(error instanceof Error ? error.message : '读取数据表失败');
    } finally {
      setLoading(false);
    }
  }

  async function openTable(table: string, nextPage = 1, nextPageSize = pageSize, nextSearch = search) {
    if (!selectedConnection?.id || !selectedDatabase) return;
    const ref = { connectionId: selectedConnection.id, database: selectedDatabase, schema: selectedSchema, table };
    setSelectedTable(table);
    setPage(nextPage);
    setPageSize(nextPageSize);
    setLoading(true);
    try {
      const [tableColumns, previewPayload, mappingPayload] = await Promise.all([
        loadDBColumns(ref),
        previewDBTable(ref, nextPage, nextPageSize, nextSearch),
        autoDBMapping(ref),
      ]);
      setColumns(tableColumns);
      setPreview(previewPayload.rows ?? []);
      setMappingRule(mappingPayload.rule);
      setMappingConfirmed(mappingPayload.reused && !requiredMappingsMissing(mappingPayload.rule.mappings));
      setActiveTab('data');
      setExpandedTreeKeys((keys) => [...new Set([...keys, `conn:${selectedConnection.id}`, `db:${selectedDatabase}`, `schema:${selectedDatabase}:${selectedSchema}`])]);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '预览数据表失败');
    } finally {
      setLoading(false);
    }
  }

  function openConnectionEditor(connection?: DBConnection) {
    const next = connection ?? defaultConnection;
    setEditing(connection ?? null);
    form.setFieldsValue(next);
    setConnectionOpen(true);
  }

  async function saveConnectionFromForm(testOnly = false) {
    const values = await form.validateFields();
    setLoading(true);
    try {
      if (testOnly) {
        const result = await testDBConnection({ ...editing, ...values });
        if (result.ok === false) throw new Error(result.detail || '连接测试失败');
        notification.success({
          message: '连接测试成功',
          description: `${values.type} ${values.host}:${values.port}${values.defaultDatabase ? ` / ${values.defaultDatabase}` : ''}`,
          placement: 'topRight',
        });
        return;
      }
      const saved = await saveDBConnection({ ...editing, ...values });
      message.success('连接已保存');
      setConnectionOpen(false);
      await refreshConnections();
      setSelectedConnection(saved);
    } catch (error) {
      if (testOnly) {
        notification.error({
          message: '连接测试失败',
          description: error instanceof Error ? error.message : '连接测试失败',
          placement: 'topRight',
        });
      } else {
        message.error(error instanceof Error ? error.message : '保存连接失败');
      }
    } finally {
      setLoading(false);
    }
  }

  async function removeConnection(id?: string) {
    if (!id) return;
    setLoading(true);
    try {
      await deleteDBConnection(id);
      message.success('连接已删除');
      setSelectedConnection(null);
      await refreshConnections();
    } catch (error) {
      message.error(error instanceof Error ? error.message : '删除连接失败');
    } finally {
      setLoading(false);
    }
  }

  async function saveMapping() {
    if (!mappingRule) return;
    setLoading(true);
    try {
      const saved = await confirmDBMapping(mappingRule);
      setMappingRule(saved);
      setMappingConfirmed(true);
      message.success('字段映射已确认');
    } catch (error) {
      message.error(error instanceof Error ? error.message : '保存字段映射失败');
    } finally {
      setLoading(false);
    }
  }

  async function startImport() {
    if (!tableRef || !mappingRule) return;
    setLoading(true);
    try {
      const created = await createDBImportTask(`数据库导入_${tableRef.table}`, tableRef, mappingRule.mappings);
      setTask(created);
      const completed = await startDBImportTask(created.id);
      setTask(completed);
      if (!completed.session_id) throw new Error('导入任务未返回会话 ID');
      props.onImported({
        session_id: completed.session_id,
        rows: completed.progress?.successRows ?? completed.sample?.length ?? 0,
        columns: completed.columns ?? [],
        files: completed.files ?? [],
        sample: completed.sample ?? [],
      });
      message.success('数据库数据已导入，请确认字段后生成图谱');
      props.onClose();
    } catch (error) {
      message.error(error instanceof Error ? error.message : '数据库导入失败');
    } finally {
      setLoading(false);
    }
  }

  async function runQuery() {
    if (!selectedConnection?.id || !selectedDatabase) return;
    setLoading(true);
    try {
      const result = await executeDBQuery(selectedConnection.id, selectedDatabase, selectedSchema, querySql, 1, 100);
      setQueryColumns(result.columns ?? []);
      setQueryRows(result.rows ?? []);
    } catch (error) {
      message.error(error instanceof Error ? error.message : '执行 SQL 查询失败');
    } finally {
      setLoading(false);
    }
  }

  async function submitEdit() {
    if (!tableRef) return;
    let values: Record<string, unknown> = {};
    let keys: Record<string, unknown> = {};
    try {
      if (editOperation !== 'delete') values = JSON.parse(editValues || '{}');
      if (editOperation !== 'insert') keys = JSON.parse(editKeys || '{}');
    } catch {
      message.error('JSON 格式不正确');
      return;
    }
    const execute = async () => {
      setLoading(true);
      try {
        const result = await editDBTable(editOperation, { ...tableRef, values, keys });
        message.success(`已影响 ${result.affectedRows ?? 0} 行`);
        await openTable(tableRef.table, page, pageSize, search);
      } catch (error) {
        message.error(error instanceof Error ? error.message : '提交表数据变更失败');
      } finally {
        setLoading(false);
      }
    };
    if (editOperation === 'delete') {
      Modal.confirm({ title: '确认删除数据', content: '删除必须带主键或唯一条件，提交后将直接修改数据库。', onOk: execute });
    } else {
      await execute();
    }
  }

  const previewColumns: ColumnsType<Record<string, unknown>> = (columns.length ? columns : Object.keys(preview[0] ?? {}).map((name) => ({ name, dataType: '' }))).map((col) => ({
    title: <Tooltip title={col.dataType || col.name}><span>{col.name}</span></Tooltip>,
    dataIndex: col.name,
    key: col.name,
    width: 160,
    ellipsis: true,
    render: (value) => value == null ? <span className="db-null">NULL</span> : String(value),
  }));
  const queryTableColumns: ColumnsType<Record<string, unknown>> = (queryColumns.length ? queryColumns : Object.keys(queryRows[0] ?? {}).map((name) => ({ name, dataType: '' }))).map((col) => ({
    title: col.name,
    dataIndex: col.name,
    key: col.name,
    width: 160,
    ellipsis: true,
    render: (value) => value == null ? <span className="db-null">NULL</span> : String(value),
  }));
  const objectColumns: ColumnsType<(typeof objectRows)[number]> = [
    {
      title: '名',
      dataIndex: 'name',
      key: 'name',
      ellipsis: true,
      render: (value) => <span className="db-object-name"><TableOutlined />{value}</span>,
    },
    { title: '行', dataIndex: 'rows', key: 'rows', width: 120, align: 'right', render: (value) => value || '-' },
    { title: '注释', dataIndex: 'comment', key: 'comment', ellipsis: true, render: (value) => value || '' },
  ];

  return (
    <Modal
      title="数据库导入"
      open={props.open}
      onCancel={props.onClose}
      footer={null}
      width={1260}
      className="db-import-modal"
      maskClosable={!loading}
    >
      <div className="db-import-shell">
        <aside className="db-import-side">
          <div className="db-import-toolbar">
            <Button icon={<PlusOutlined />} type="primary" onClick={() => openConnectionEditor()}>新建</Button>
            <Button icon={<ReloadOutlined />} onClick={refreshConnections} />
          </div>
          <Input.Search placeholder="搜索连接" allowClear />
          {selectedConnection ? (
            <div className="connection-actions">
              <Button onClick={() => selectedConnection.id && loadDatabases(selectedConnection.id)}>打开连接</Button>
              <Button onClick={() => openConnectionEditor(selectedConnection)}>编辑</Button>
              <Button danger icon={<DeleteOutlined />} onClick={() => Modal.confirm({ title: '删除连接', content: '删除后会同步移除本地配置。', onOk: () => removeConnection(selectedConnection.id) })} />
            </div>
          ) : null}
          <Tree
            showIcon
            blockNode
            className="db-browser-tree"
            treeData={treeData}
            selectedKeys={treeSelectedKey ? [treeSelectedKey] : []}
            expandedKeys={expandedTreeKeys}
            onExpand={setExpandedTreeKeys}
            onSelect={async (keys) => {
              const key = String(keys[0] ?? '');
              const [kind, database, schema, table] = key.split(':');
              if (kind === 'conn') await handleConnectionSelect(database);
              if (kind === 'db') await loadSchemas(database);
              if (kind === 'schema') await loadTables(database, schema);
              if (kind === 'table') await openTable(table);
            }}
          />
        </aside>

        <main className="db-import-main">
          <div className="db-object-bar">
            <div className="db-object-tab">对象</div>
            <Space size={2} wrap>
              <Tooltip title="打开当前表数据">
                <Button size="small" icon={<FolderOpenOutlined />} onClick={() => tableRef && openTable(tableRef.table)} disabled={!tableRef} loading={loading}>打开表</Button>
              </Tooltip>
              <Tooltip title="查看当前表结构">
                <Button size="small" icon={<TableOutlined />} disabled={!tableRef} onClick={() => setActiveTab('columns')}>设计表</Button>
              </Tooltip>
              <Tooltip title="当前版本不支持新建物理表">
                <Button size="small" icon={<PlusCircleOutlined />} disabled>新建表</Button>
              </Tooltip>
              <Tooltip title="当前版本不支持删除物理表">
                <Button size="small" icon={<DeleteOutlined />} disabled>删除表</Button>
              </Tooltip>
              <Button size="small" icon={<ImportOutlined />} type="primary" disabled={!canImport} loading={loading} onClick={startImport}>导入向导</Button>
              <Tooltip title="请使用下方查询或导入后的图谱导出能力">
                <Button size="small" icon={<ExportOutlined />} disabled>导出向导</Button>
              </Tooltip>
              <Input.Search
                placeholder="搜索当前表"
                allowClear
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                onSearch={(value) => tableRef && openTable(tableRef.table, 1, pageSize, value)}
                className="db-search"
              />
            </Space>
          </div>
          <div className="db-current-path">{selectedContext}</div>
          <div className="db-main-toolbar">
            <Space>
              <Tag color={selectedConnection ? 'blue' : 'default'}>{selectedConnection?.type ?? '未连接'}</Tag>
              {selectedTable ? <Tag color="green">{selectedTable}</Tag> : <Tag>请选择模式或表</Tag>}
            </Space>
          </div>

          {requiredMissing || lowConfidence ? (
            <Alert
              type="warning"
              showIcon
              message="字段映射需要确认"
              description="必填字段缺失、低置信度或新表结构会阻止直接导入，请在字段映射页确认并保存。"
            />
          ) : null}

          <Tabs
            activeKey={activeTab}
            onChange={setActiveTab}
            items={[
              {
                key: 'objects',
                label: '对象',
                children: selectedSchema ? (
                  <div className="db-object-panel">
                    <div className="db-object-groups">
                      {objectGroups.map((group) => (
                        <Button
                          key={group.key}
                          size="small"
                          type={objectGroup === group.key ? 'primary' : 'default'}
                          icon={group.icon}
                          disabled={group.disabled}
                          onClick={() => setObjectGroup(group.key)}
                        >
                          {group.label}
                        </Button>
                      ))}
                    </div>
                    <Table
                      rowKey="key"
                      size="small"
                      loading={loading}
                      columns={objectColumns}
                      dataSource={objectGroup === 'tables' ? objectRows : []}
                      pagination={false}
                      scroll={{ y: 405 }}
                      onRow={(record) => ({
                        onDoubleClick: () => openTable(record.name),
                        onClick: () => setSelectedTable(record.name),
                      })}
                    />
                  </div>
                ) : <Empty description="请选择左侧连接、数据库和模式" />,
              },
              {
                key: 'data',
                label: '表数据',
                children: tableRef ? (
                  <Table
                    rowKey={(_, index) => String(index)}
                    size="small"
                    loading={loading}
                    columns={previewColumns}
                    dataSource={preview}
                    scroll={{ x: Math.max(previewColumns.length * 160, 900), y: 360 }}
                    virtual
                    pagination={{
                      current: page,
                      pageSize,
                      pageSizeOptions: [50, 100, 500, 1000],
                      showSizeChanger: true,
                      total: page * pageSize + (preview.length === pageSize ? pageSize : 0),
                      onChange: (nextPage, nextSize) => openTable(tableRef.table, nextPage, nextSize, search),
                    }}
                  />
                ) : <Empty description="请选择左侧连接和数据表" />,
              },
              {
                key: 'columns',
                label: '表结构',
                children: (
                  <Table
                    rowKey="name"
                    size="small"
                    dataSource={columns}
                    pagination={false}
                    columns={[
                      { title: '字段名', dataIndex: 'name', ellipsis: true },
                      { title: '类型', dataIndex: 'dataType', width: 140 },
                      { title: '允许空', dataIndex: 'nullable', width: 80, render: (value) => value ? '是' : '否' },
                      { title: '主键', dataIndex: 'primaryKey', width: 80, render: (value) => value ? '是' : '' },
                      { title: '默认值', dataIndex: 'default', ellipsis: true },
                      { title: '注释', dataIndex: 'comment', ellipsis: true },
                    ]}
                  />
                ),
              },
              {
                key: 'query',
                label: '查询',
                children: (
                  <div className="db-query-panel">
                    <Input.TextArea
                      value={querySql}
                      onChange={(event) => setQuerySql(event.target.value)}
                      rows={5}
                      placeholder="默认仅允许 SELECT / WITH 查询"
                    />
                    <div><Button type="primary" onClick={runQuery} loading={loading} disabled={!selectedConnection?.id || !selectedDatabase}>执行查询</Button></div>
                    <Table
                      rowKey={(_, index) => String(index)}
                      size="small"
                      columns={queryTableColumns}
                      dataSource={queryRows}
                      scroll={{ x: Math.max(queryTableColumns.length * 160, 900), y: 280 }}
                      pagination={false}
                      virtual
                    />
                  </div>
                ),
              },
              {
                key: 'edit',
                label: '数据编辑',
                children: (
                  <div className="db-edit-panel">
                    <Alert type="info" showIcon message="写操作需要数据库账号具备权限；修改和删除必须提供主键或唯一条件，后端会拒绝无条件 update/delete。" />
                    <Select value={editOperation} onChange={setEditOperation} options={[
                      { value: 'insert', label: '新增' },
                      { value: 'update', label: '修改' },
                      { value: 'delete', label: '删除' },
                    ]} />
                    {editOperation !== 'delete' ? <Input.TextArea value={editValues} onChange={(event) => setEditValues(event.target.value)} rows={6} placeholder="变更字段 JSON" /> : null}
                    {editOperation !== 'insert' ? <Input.TextArea value={editKeys} onChange={(event) => setEditKeys(event.target.value)} rows={4} placeholder="主键/唯一条件 JSON" /> : null}
                    <div><Button danger={editOperation === 'delete'} type="primary" disabled={!tableRef} loading={loading} onClick={submitEdit}>提交变更</Button></div>
                  </div>
                ),
              },
              {
                key: 'mapping',
                label: '字段映射',
                children: (
                  <div className="db-mapping-panel">
                    <Table
                      rowKey={(row) => row.targetField}
                      size="small"
                      pagination={false}
                      dataSource={mappingRule?.mappings ?? []}
                      columns={[
                        { title: '源字段', dataIndex: 'sourceColumn', render: (value, row) => (
                          <Select
                            allowClear
                            value={value}
                            placeholder="选择源字段"
                            options={columns.map((col) => ({ value: col.name, label: `${col.name} · ${col.dataType}` }))}
                            onChange={(next) => updateMapping(row.targetField, { sourceColumn: next })}
                          />
                        ) },
                        { title: '目标字段', dataIndex: 'targetField' },
                        { title: '目标类型', dataIndex: 'targetType', width: 110 },
                        { title: '必填', dataIndex: 'required', width: 70, render: (value) => value ? '是' : '否' },
                        { title: '置信度', dataIndex: 'confidence', width: 90, render: (value) => value ? `${value}%` : '-' },
                        { title: '状态', width: 110, render: (_, row) => row.required && !row.sourceColumn ? <Tag color="red">缺失</Tag> : (row.confidence ?? 0) < 70 ? <Tag color="gold">需确认</Tag> : <Tag color="green">已匹配</Tag> },
                      ]}
                    />
                    <div className="mapping-actions">
                      <Checkbox checked={mappingConfirmed} onChange={(event) => setMappingConfirmed(event.target.checked)} disabled={requiredMissing}>
                        我已确认字段映射、时间和金额转换规则
                      </Checkbox>
                      <Button type="primary" onClick={saveMapping} disabled={!mappingRule || requiredMissing} loading={loading}>保存映射规则</Button>
                    </div>
                  </div>
                ),
              },
              {
                key: 'task',
                label: '导入任务',
                children: task ? (
                  <div className="db-task-panel">
                    <strong>{task.name}</strong>
                    <Tag>{task.status}</Tag>
                    <Progress percent={task.progress?.processedRows ? Math.min(100, Math.round((task.progress.successRows / Math.max(task.progress.processedRows, 1)) * 100)) : 0} />
                    <span>已处理 {task.progress?.processedRows ?? 0} 行，成功 {task.progress?.successRows ?? 0} 行，失败 {task.progress?.failedRows ?? 0} 行</span>
                  </div>
                ) : <Empty description="暂无导入任务" />,
              },
            ]}
          />
        </main>
      </div>

      <Modal
        title={editing ? '编辑连接' : '新建连接'}
        open={connectionOpen}
        onCancel={() => setConnectionOpen(false)}
        footer={[
          <Button key="test" onClick={() => saveConnectionFromForm(true)} loading={loading}>测试连接</Button>,
          <Button key="save" type="primary" onClick={() => saveConnectionFromForm(false)} loading={loading}>保存</Button>,
        ]}
      >
        <Form form={form} layout="vertical" initialValues={defaultConnection}>
          <Form.Item name="name" label="连接名称" rules={[{ required: true, message: '请输入连接名称' }]}><Input /></Form.Item>
          <Form.Item name="type" label="数据库类型" rules={[{ required: true }]}><Select options={[{ value: 'mysql', label: 'MySQL' }, { value: 'postgresql', label: 'PostgreSQL' }]} onChange={(value) => form.setFieldValue('port', value === 'mysql' ? 3306 : 5432)} /></Form.Item>
          <Form.Item name="host" label="主机" rules={[{ required: true, message: '请输入主机' }]}><Input /></Form.Item>
          <Form.Item name="port" label="端口" rules={[{ required: true, message: '请输入端口' }]}><InputNumber min={1} max={65535} /></Form.Item>
          <Form.Item name="defaultDatabase" label="初始数据库"><Input /></Form.Item>
          <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}><Input /></Form.Item>
          <Form.Item name="password" label="密码"><Input.Password autoComplete="new-password" /></Form.Item>
          <Form.Item name="savePassword" valuePropName="checked"><Checkbox>保存密码</Checkbox></Form.Item>
          <Form.Item name="ssl" valuePropName="checked"><Checkbox>启用 SSL</Checkbox></Form.Item>
          <Form.Item name="timeoutSeconds" label="连接超时（秒）"><InputNumber min={1} max={120} /></Form.Item>
          <Form.Item name="remark" label="备注"><Input.TextArea rows={2} /></Form.Item>
        </Form>
      </Modal>
    </Modal>
  );

  function updateMapping(targetField: string, patch: Partial<DBFieldMapping>) {
    if (!mappingRule) return;
    setMappingConfirmed(false);
    setMappingRule({
      ...mappingRule,
      mappings: mappingRule.mappings.map((item) => item.targetField === targetField ? { ...item, ...patch } : item),
    });
  }
}

function requiredMappingsMissing(mappings: DBFieldMapping[]) {
  return mappings.some((item) => item.required && !item.sourceColumn);
}
