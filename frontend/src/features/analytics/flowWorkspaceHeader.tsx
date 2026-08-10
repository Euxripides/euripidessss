// 地址关系图 V2.0 UI 整改 — 顶部工具栏（设计 §5.2）
//
// 72px：品牌区 | 完整地址搜索（Enter=定位）| 方向分段 | 深度分段 | 操作。
// 方向/深度必须使用分段按钮，不得使用普通下拉框。

import { ArrowDownOutlined, ArrowUpOutlined, SearchOutlined } from "@ant-design/icons";
import { Button, Input, Segmented, Select } from "antd";
import type { ReactNode } from "react";
import type { FocusDepth, FocusDirection, FocusSelection } from "./graphUpgrade";
import type { GraphKind } from "./flowWorkspaceGraph";

export interface FlowWorkspaceHeaderProps {
  searchInput: string;
  onSearchInput: (value: string) => void;
  onLocate: () => void;
  focus: FocusSelection | null;
  direction: FocusDirection;
  depth: FocusDepth;
  onDirection: (direction: FocusDirection) => void;
  onDepth: (depth: FocusDepth) => void;
  kindFilter: GraphKind;
  onKindFilter: (kind: GraphKind) => void;
  onClearFocus: () => void;
}

const DIRECTION_OPTIONS: Array<{ label: React.ReactNode; value: FocusDirection }> = [
  { label: "全局视图", value: "all" },
  { label: <span><ArrowUpOutlined /> 上游</span>, value: "upstream" },
  { label: <span><ArrowDownOutlined /> 下游</span>, value: "downstream" },
  { label: <span><ArrowUpOutlined /><ArrowDownOutlined /> 前后</span>, value: "both" },
];

const DEPTH_OPTIONS: Array<{ label: string; value: FocusDepth }> = [
  { label: "1层", value: 1 },
  { label: "2层", value: 2 },
  { label: "全部", value: 0 },
];

export default function FlowWorkspaceHeader({
  searchInput,
  onSearchInput,
  onLocate,
  focus,
  direction,
  depth,
  onDirection,
  onDepth,
  kindFilter,
  onKindFilter,
  onClearFocus,
}: FlowWorkspaceHeaderProps) {
  return (
    <header className="flow-workspace-header">
      <div className="flow-workspace-brand">
        <strong>资金追踪</strong>
        <small>链上资金路径与地址关系分析</small>
      </div>

      <div className="flow-workspace-search">
        <Input
          aria-label="地址搜索"
          placeholder="输入完整 EVM 地址（Enter 定位）"
          prefix={<SearchOutlined />}
          value={searchInput}
          onChange={(event) => onSearchInput(event.target.value)}
          onPressEnter={onLocate}
          allowClear
        />
      </div>

      <div className="flow-workspace-segments" aria-label="方向与深度">
        <Segmented<FocusDirection>
          aria-label="方向"
          options={DIRECTION_OPTIONS}
          value={direction}
          onChange={onDirection}
        />
        <Segmented<FocusDepth>
          aria-label="深度"
          options={DEPTH_OPTIONS}
          value={depth}
          onChange={onDepth}
        />
      </div>

      <div className="flow-workspace-actions">
        <Select<GraphKind>
          aria-label="关系类型"
          value={kindFilter}
          onChange={onKindFilter}
          popupClassName="flow-dark-dropdown"
          options={[
            { value: "all", label: "全部关系" },
            { value: "TRANSFER", label: "Transfer" },
            { value: "INTERACTION", label: "Interaction" },
            { value: "COMMON_COUNTERPARTY", label: "Relation" },
          ]}
        />
        {focus ? (
          <Button onClick={onClearFocus} aria-label="返回全局">
            返回全局
          </Button>
        ) : null}
      </div>
    </header>
  );
}
