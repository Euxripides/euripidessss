import { ReloadOutlined } from "@ant-design/icons";
import { Button } from "antd";

interface Props {
  label: string;
  loadedCount: number;
  totalCount: number;
  nextCursor?: string;
  loading?: boolean;
  error?: string;
  onLoadMore: () => void;
}

export function DatasetPagination({ label, loadedCount, totalCount, nextCursor, loading = false, error, onLoadMore }: Props) {
  if (totalCount === 0) return null;
  const complete = loadedCount >= totalCount;
  return <div className="txd-dataset-pagination">
    <span>已加载 <b>{loadedCount.toLocaleString("en-US")}</b> / 共 <b>{totalCount.toLocaleString("en-US")}</b> 条</span>
    {error ? <small>{error}</small> : null}
    {!complete && nextCursor ? <Button icon={error ? <ReloadOutlined /> : undefined} loading={loading} disabled={loading} onClick={onLoadMore}>{error ? "重试加载" : `加载更多${label}`}</Button> : null}
    {!complete && !nextCursor ? <small>服务未返回下一页游标，无法继续加载。</small> : null}
    {complete ? <em>已加载全部</em> : null}
  </div>;
}
