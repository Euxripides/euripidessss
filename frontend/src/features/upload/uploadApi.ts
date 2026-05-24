import type { UploadFile } from 'antd';
import JSZip from 'jszip';
import type { FlowImportProgress, ImportedDataset, NetworkMode, TransferStatus } from '../flow/flowTypes';

type UploadGroup = {
  field: string;
  files: UploadFile[];
  archiveName: string;
  single?: boolean;
};

export function detectNetworkMode(): NetworkMode {
  const hostname = window.location.hostname;
  if (hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '::1') return 'local';
  if (/^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[0-1])\.)/.test(hostname)) return 'local';
  return 'external';
}

export async function buildUploadForm(
  groups: UploadGroup[],
  mode: NetworkMode,
  onTransfer: (status: TransferStatus) => void,
) {
  const form = new FormData();
  for (const group of groups) {
    const files = group.files
      .map((file) => file.originFileObj ?? (file as unknown as File))
      .filter(Boolean) as File[];
    if (!files.length) continue;
    if (mode === 'external') {
      onTransfer({
        visible: true,
        phase: 'packing',
        mode,
        label: `正在压缩 ${group.archiveName}`,
        percent: 0,
        speed: 0,
        loaded: 0,
        total: files.reduce((sum, file) => sum + file.size, 0),
      });
      const zipped = await zipFiles(files, group.archiveName, onTransfer, mode);
      form.append(group.field, zipped, group.archiveName);
    } else {
      for (const file of files) form.append(group.field, file);
    }
  }
  return form;
}

export function requestJsonWithProgress(url: string, data: FormData, mode: NetworkMode, label: string, onTransfer: (status: TransferStatus) => void): Promise<any> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    const started = performance.now();
    request.open('POST', url);
    request.timeout = 30 * 60 * 1000;
    request.upload.onprogress = (event) => {
      updateTransfer(onTransfer, mode, 'uploading', label, event.loaded, event.lengthComputable ? event.total : 0, calculateSpeed(event.loaded, started));
    };
    request.upload.onload = () => updateTransfer(onTransfer, mode, 'processing', '上传完成，后端处理中', 1, 1, 0);
    request.onload = () => {
      let payload: any = {};
      try {
        payload = request.responseText ? JSON.parse(request.responseText) : {};
      } catch {
        reject(new Error('后端返回内容无法解析'));
        return;
      }
      payload.status = request.status;
      if (request.status >= 200 && request.status < 300) {
        doneTransfer(onTransfer, mode, '处理完成');
      }
      resolve(payload);
    };
    request.onerror = () => reject(new Error('网络连接失败'));
    request.ontimeout = () => reject(new Error('传输超时'));
    request.send(data);
  });
}

export function downloadWithProgress(url: string, mode: NetworkMode, fallbackName: string, onTransfer: (status: TransferStatus) => void) {
  return new Promise<void>((resolve, reject) => {
    const request = new XMLHttpRequest();
    const started = performance.now();
    const downloadUrl = mode === 'external' ? `${url}${url.includes('?') ? '&' : '?'}package=1` : url;
    request.open('GET', downloadUrl);
    request.responseType = 'blob';
    request.timeout = 30 * 60 * 1000;
    request.onprogress = (event) => {
      updateTransfer(onTransfer, mode, 'downloading', '正在下载清洗结果', event.loaded, event.lengthComputable ? event.total : 0, calculateSpeed(event.loaded, started));
    };
    request.onload = () => {
      if (request.status < 200 || request.status >= 300) {
        reject(new Error(`下载失败（HTTP ${request.status}）`));
        return;
      }
      const filename = parseDownloadFilename(request.getResponseHeader('Content-Disposition')) || `${fallbackName}${mode === 'external' ? '.zip' : ''}`;
      const blobUrl = URL.createObjectURL(request.response);
      const link = document.createElement('a');
      link.href = blobUrl;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(blobUrl);
      doneTransfer(onTransfer, mode, '下载完成');
      resolve();
    };
    request.onerror = () => reject(new Error('下载连接失败'));
    request.ontimeout = () => reject(new Error('下载超时'));
    request.send();
  });
}

export function uploadFlowImport(
  data: FormData,
  onProgress: (progress: FlowImportProgress) => void,
  mode: NetworkMode,
  onTransfer: (status: TransferStatus) => void,
): Promise<ImportedDataset> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open('POST', '/api/flow/import');
    request.timeout = 10 * 60 * 1000;
    const started = performance.now();

    request.upload.onprogress = (event) => {
      const speed = calculateSpeed(event.loaded, started);
      updateTransfer(onTransfer, mode, 'uploading', '上传导入数据', event.loaded, event.lengthComputable ? event.total : 0, speed);
      if (!event.lengthComputable) {
        onProgress({ visible: true, percent: 8, status: 'active', text: '正在上传数据文件...' });
        return;
      }
      const uploadPercent = Math.round((event.loaded / event.total) * 85);
      onProgress({
        visible: true,
        percent: Math.max(1, Math.min(85, uploadPercent)),
        status: 'active',
        text: `正在上传数据文件 ${Math.min(100, Math.round((event.loaded / event.total) * 100))}%`,
      });
    };

    request.upload.onload = () => {
      updateTransfer(onTransfer, mode, 'processing', '后端解析导入数据', 1, 1, 0);
      onProgress({ visible: true, percent: 90, status: 'active', text: '上传完成，正在解析数据...' });
    };

    request.onload = () => {
      onProgress({ visible: true, percent: 95, status: 'active', text: '上传完成，正在解析数据...' });
      let payload: any = {};
      try {
        payload = request.responseText ? JSON.parse(request.responseText) : {};
      } catch {
        reject(new Error('导入数据失败：后端返回内容无法解析'));
        return;
      }
      if (request.status >= 200 && request.status < 300) {
        doneTransfer(onTransfer, mode, '导入完成');
        resolve(payload as ImportedDataset);
        return;
      }
      reject(new Error(payload.detail || `导入数据失败（HTTP ${request.status}）`));
    };

    request.onerror = () => reject(new Error('导入数据失败：无法连接后端服务'));
    request.ontimeout = () => reject(new Error('导入数据超时，请检查文件大小或后端服务状态'));

    onProgress({ visible: true, percent: 1, status: 'active', text: '正在连接后端服务...' });
    request.send(data);
  });
}

export function failTransfer(mode: NetworkMode, label: string, onTransfer: (status: TransferStatus) => void) {
  onTransfer({ visible: true, phase: 'error', mode, label, percent: 100, speed: 0, loaded: 0, total: 0 });
}

async function zipFiles(files: File[], archiveName: string, onTransfer: (status: TransferStatus) => void, mode: NetworkMode) {
  const zip = new JSZip();
  for (const file of files) zip.file(file.name, file);
  const blob = await zip.generateAsync(
    { type: 'blob', compression: 'DEFLATE', compressionOptions: { level: 6 } },
    (meta) => {
      onTransfer({
        visible: true,
        phase: 'packing',
        mode,
        label: `正在压缩 ${archiveName}`,
        percent: Math.round(meta.percent),
        speed: 0,
        loaded: Math.round(meta.percent),
        total: 100,
      });
    },
  );
  return new File([blob], archiveName, { type: 'application/zip' });
}

function updateTransfer(onTransfer: (status: TransferStatus) => void, mode: NetworkMode, phase: TransferStatus['phase'], label: string, loaded: number, total: number, speed: number) {
  onTransfer({
    visible: true,
    phase,
    mode,
    label,
    percent: total ? Math.min(100, Math.round((loaded / total) * 100)) : phase === 'processing' ? 95 : 0,
    speed,
    loaded,
    total,
  });
}

function doneTransfer(onTransfer: (status: TransferStatus) => void, mode: NetworkMode, label: string) {
  onTransfer({ visible: true, phase: 'done', mode, label, percent: 100, speed: 0, loaded: 1, total: 1 });
}

function calculateSpeed(loaded: number, started: number) {
  const seconds = Math.max((performance.now() - started) / 1000, 0.1);
  return loaded / seconds;
}

function parseDownloadFilename(header: string | null) {
  if (!header) return '';
  const utf8Match = /filename\*=UTF-8''([^;]+)/i.exec(header);
  if (utf8Match) return decodeURIComponent(utf8Match[1]);
  const plainMatch = /filename="?([^"]+)"?/i.exec(header);
  return plainMatch?.[1] ?? '';
}
