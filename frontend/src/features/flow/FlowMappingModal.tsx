import { DownloadOutlined } from '@ant-design/icons';
import { Button, Modal, Select, Space, message } from 'antd';
import { useState, type MouseEvent } from 'react';
import { FLOW_TEMPLATE_COLUMNS, FLOW_TEMPLATE_MAPPING, type FlowFieldMapping } from './flowTypes';
import { resolveEffectiveFlowMapping } from './flowMapping';

export function FlowMappingModal(props: {
  open: boolean;
  columns: string[];
  mapping: FlowFieldMapping;
  onChange: (mapping: FlowFieldMapping) => void;
  onSave: (mapping: FlowFieldMapping) => Promise<boolean>;
  onClose: () => void;
}) {
  const [saving, setSaving] = useState(false);
  const columnOptions = props.columns.map((column) => ({ label: column, value: column }));
  const missingTemplateColumns = FLOW_TEMPLATE_COLUMNS.filter((column) => !props.columns.includes(column));
  const rows = [
    ['source_name_column', '交易方户名'],
    ['source_account_column', '交易方账户'],
    ['source_id_column', '交易方身份证号'],
    ['source_label_column', '交易方标签'],
    ['target_card_column', '交易对手账卡号'],
    ['target_name_column', '对手户名'],
    ['target_id_column', '对手身份证号'],
    ['target_label_column', '对手标签'],
    ['serial_column', '交易流水号'],
    ['summary_column', '摘要说明'],
    ['remark_column', '备注'],
    ['amount_column', '交易金额'],
    ['time_column', '交易时间'],
    ['direction_column', '收付标志'],
  ] as Array<[keyof typeof FLOW_TEMPLATE_MAPPING, string]>;

  function update(key: keyof typeof FLOW_TEMPLATE_MAPPING, value?: string) {
    props.onChange({ ...props.mapping, [key]: value ?? '' });
  }

  async function confirm() {
    setSaving(true);
    try {
      const resolved = resolveEffectiveFlowMapping(props.mapping, props.columns);
      const saved = await props.onSave(resolved);
      if (saved) props.onClose();
    } catch (error) {
      message.error(error instanceof Error ? error.message : '保存字段映射规则失败');
    } finally {
      setSaving(false);
    }
  }

  function closeOpenSelectOnBlankClick(event: MouseEvent<HTMLDivElement>) {
    const target = event.target as HTMLElement;
    if (target.closest('.ant-select') || target.closest('.ant-select-dropdown')) return;
    const active = document.activeElement as HTMLElement | null;
    active?.blur?.();
  }

  return (
    <Modal
      title="字段映射 / 模板说明"
      open={props.open}
      onCancel={props.onClose}
      onOk={confirm}
      okText="确认"
      cancelText="关闭"
      confirmLoading={saving}
      maskClosable={false}
      width={760}
    >
      <div onMouseDown={closeOpenSelectOnBlankClick}>
        <Space direction="vertical" size="middle" className="full">
          {!!missingTemplateColumns.length && (
            <div className="build-status status-exception">
              当前文件与模板不完全匹配，请按模板重新上传，或在下方选择字段映射后继续生成图。
            </div>
          )}
          {!missingTemplateColumns.length && (
            <div className="build-status status-success">当前文件字段符合模板，可直接生成图。</div>
          )}
          <Button icon={<DownloadOutlined />} onClick={() => window.open('/api/flow/template', '_blank')}>
            下载数据模板
          </Button>
          <div className="mapping-grid">
            {rows.map(([key, label]) => (
              <div key={key} className="mapping-row">
                <span>{label}</span>
                <Select
                  allowClear
                  showSearch
                  optionFilterProp="label"
                  placeholder={`选择${label}字段`}
                  value={props.mapping[key] || undefined}
                  options={columnOptions}
                  onChange={(value) => update(key, value)}
                />
              </div>
            ))}
          </div>
          <div className="format-hint">
            <strong>模板字段</strong>
            <span>{FLOW_TEMPLATE_COLUMNS.join('、')}</span>
          </div>
        </Space>
      </div>
    </Modal>
  );
}
