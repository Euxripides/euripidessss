import { DatabaseOutlined, UploadOutlined } from '@ant-design/icons';
import { Button, Modal, Select, Upload } from 'antd';
import type { UploadFile } from 'antd';
import type { HistoryItem } from './flowTypes';
import { filterImportFiles } from './flowImportFiles';

export function FlowSourceModal(props: {
  open: boolean;
  loading: boolean;
  uploadFiles: UploadFile[];
  onUploadFilesChange: (files: UploadFile[]) => void;
  onOpenDatabaseImport: () => void;
  onImportData: (files: UploadFile[]) => Promise<boolean>;
  historyItems: HistoryItem[];
  selectedHistory?: string;
  onSelectedHistoryChange: (jobId?: string) => void;
  onRefreshHistory: () => void;
  onLoadHistory: (jobId: string) => Promise<void>;
  onClose: () => void;
}) {
  return (
    <Modal
      title="选择来源数据"
      open={props.open}
      onCancel={() => {
        if (!props.loading) props.onClose();
      }}
      footer={null}
      width={820}
      maskClosable={!props.loading}
    >
      <div className="source-modal-grid">
        <div className="source-card">
          <strong>上传文件</strong>
          <span>适合已整理好的资金流向表。</span>
          <div className="source-actions">
            <div className="stacked-actions">
              <Upload
                multiple
                beforeUpload={() => false}
                accept=".xlsx,.csv,.xls"
                fileList={props.uploadFiles}
                showUploadList={false}
                onChange={(event) => props.onUploadFilesChange(filterImportFiles(event.fileList))}
              >
                <Button icon={<UploadOutlined />}>选择文件</Button>
              </Upload>
              <Button
                type="primary"
                loading={props.loading}
                disabled={!props.uploadFiles.length}
                onClick={async () => {
                  const files = [...props.uploadFiles];
                  props.onUploadFilesChange([]);
                  props.onClose();
                  await props.onImportData(files);
                }}
              >
                导入数据
              </Button>
            </div>
            <div className="selected-files">
              {props.uploadFiles.length ? props.uploadFiles.map((file) => <span key={file.uid}>{file.name}</span>) : <span>尚未选择文件</span>}
            </div>
          </div>
        </div>
        <div className="source-card">
          <strong>数据库导入</strong>
          <span>连接 MySQL 或 PostgreSQL，预览表数据并确认字段映射后导入流向图。</span>
          <div className="source-actions">
            <div className="stacked-actions">
              <Button icon={<DatabaseOutlined />} type="primary" onClick={() => {
                props.onClose();
                props.onOpenDatabaseImport();
              }}>
                打开数据库导入
              </Button>
            </div>
          </div>
        </div>
        <div className="source-card">
          <strong>历史数据文件</strong>
          <span>从历史历史结果中选择数据生成图谱。</span>
          <div className="source-actions">
            <div className="stacked-actions">
              <Select
                allowClear
                showSearch
                placeholder="选择历史结果"
                optionFilterProp="label"
                value={props.selectedHistory}
                onDropdownVisibleChange={(open) => open && props.onRefreshHistory()}
                onChange={props.onSelectedHistoryChange}
                options={props.historyItems.map((item) => ({
                  value: item.job_id,
                  label: `${item.name} · ${new Date(item.updated_at * 1000).toLocaleString('zh-CN')}`,
                }))}
              />
              <Button loading={props.loading} disabled={!props.selectedHistory} onClick={() => props.selectedHistory && props.onLoadHistory(props.selectedHistory).then(() => props.onClose())}>
                载入历史图谱
              </Button>
            </div>
          </div>
        </div>
        <div className="format-hint">
          <strong>上传数据格式</strong>
          <span>直接图谱表：付款方账号/付款方/来源主体，收款方账号/收款方/目标主体，交易金额/金额；可选交易时间、收付标志。</span>
          <span>数据库导入表：字段需映射到交易方、对手方、交易时间、交易金额、收付标志等目标字段；必填字段未确认时不能导入。</span>
        </div>
      </div>
    </Modal>
  );
}
