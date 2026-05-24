import { Modal } from 'antd';
import type { UploadFile } from 'antd';
import { FLOW_IMPORT_EXTENSIONS } from './flowTypes';

export function filterImportFiles(fileList: UploadFile[]) {
  const allowed = new Set(FLOW_IMPORT_EXTENSIONS);
  const accepted = fileList.filter((file) => allowed.has(fileExtension(file.name)));
  if (accepted.length !== fileList.length) {
    Modal.warning({
      title: '文件格式不符合模板导入要求',
      content: '请选择 xlsx、csv 或 xls 文件。若字段与模板不一致，导入后可在“字段映射 / 模板说明”里手动映射。',
    });
  }
  return accepted;
}

function fileExtension(name?: string) {
  const text = String(name ?? '').toLowerCase();
  const index = text.lastIndexOf('.');
  return index >= 0 ? text.slice(index) : '';
}
