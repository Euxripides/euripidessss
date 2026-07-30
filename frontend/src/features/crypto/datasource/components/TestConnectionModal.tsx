import { CheckCircleFilled, CloseCircleFilled } from '@ant-design/icons';
import { Modal, Progress, Result, Spin } from 'antd';
import type { DataSourceItem, DataSourceTestResult } from '../types/datasource';

export function TestConnectionModal({ source, result, open, testing, onClose }: {
  source: DataSourceItem | null;
  result: DataSourceTestResult | null;
  open: boolean;
  testing: boolean;
  onClose: () => void;
}) {
  return (
    <Modal title={`连接测试 · ${source?.name || ''}`} open={open} footer={null} onCancel={onClose} width={520}>
      {testing ? (
        <div className="datasource-test-running">
          <Spin size="large" />
          <strong>正在校验连通性与响应内容</strong>
          <Progress percent={65} status="active" showInfo={false} />
          <span>测试由后端执行，浏览器不会直接访问数据源。</span>
        </div>
      ) : result ? (
        <Result
          status={result.success ? 'success' : 'error'}
          icon={result.success ? <CheckCircleFilled /> : <CloseCircleFilled />}
          title={result.success ? '连接测试通过' : '连接测试失败'}
          subTitle={`${result.message} · ${result.latency_ms} ms${result.dataset ? ` · ${result.dataset}` : ''}`}
        />
      ) : null}
    </Modal>
  );
}
