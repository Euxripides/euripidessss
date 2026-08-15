import { DatabaseOutlined, SearchOutlined } from "@ant-design/icons";
import { AutoComplete, Input, Tag } from "antd";
import { useCallback, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { listAddressLibrary, type AddressLibraryItem } from "./addressLibraryApi";

const STATE_LABEL: Record<string, { color: string; text: string }> = {
  AVAILABLE: { color: "success", text: "可分析" },
  CERTIFIED: { color: "processing", text: "已认证" },
  DOWNLOADING: { color: "blue", text: "下载中" },
  PARTIAL: { color: "warning", text: "部分完成" },
  FAILED: { color: "error", text: "下载失败" },
  IMPORTED: { color: "default", text: "仅已导入" },
};

export interface AddressLibraryInputProps {
  value: string;
  onChange: (value: string) => void;
  onSelect?: (value: string, item?: AddressLibraryItem) => void;
  onPressEnter?: () => void;
  chainKey?: string;
  placeholder?: string;
  className?: string;
  style?: CSSProperties;
  allowClear?: boolean;
  prefix?: ReactNode;
  ariaLabel?: string;
}

export default function AddressLibraryInput({
  value,
  onChange,
  onSelect,
  onPressEnter,
  chainKey,
  placeholder = "搜索已导入地址",
  className,
  style,
  allowClear = true,
  prefix = <SearchOutlined />,
  ariaLabel = "地址资产搜索",
}: AddressLibraryInputProps) {
  const [items, setItems] = useState<AddressLibraryItem[]>([]);
  const [open, setOpen] = useState(false);
  const requestSequence = useRef(0);
  const queryRef = useRef(value);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const load = useCallback(async (query: string) => {
    const sequence = ++requestSequence.current;
    queryRef.current = query;
    try {
      const result = await listAddressLibrary(chainKey, query, 30, true);
      if (!mountedRef.current) return;
      // 响应写入前核对序号与查询词：慢返回的旧请求不得覆盖新结果。
      if (sequence === requestSequence.current && queryRef.current === query) {
        setItems(result.items);
      }
    } catch {
      if (!mountedRef.current) return;
      if (sequence === requestSequence.current) {
        setItems([]);
        setOpen(false);
      }
    }
  }, [chainKey]);

  // 输入值或链变化时立即失效在途旧请求（不等 debounce 窗口）。
  useEffect(() => {
    requestSequence.current += 1;
    queryRef.current = value;
  }, [value, chainKey]);

  useEffect(() => {
    const timer = window.setTimeout(() => void load(value), 180);
    return () => window.clearTimeout(timer);
  }, [load, value]);

  const options = items.map((item) => {
    const status = STATE_LABEL[item.state] ?? STATE_LABEL.IMPORTED;
    // 唯一键与唯一 value：同一地址可存在于多条链，裸地址作 value 会导致
    // rc-select 按 value 查找 option 时命中错误链的项（点击 BSC 项进入 ETH）。
    const optionValue = `${item.chain_key}:${item.address}`;
    return {
      key: optionValue,
      value: optionValue,
      item,
      label: (
        <div className="address-library-option">
          <DatabaseOutlined />
          <code>{item.address}</code>
          <Tag color={status.color}>{status.text}</Tag>
          <small>{item.chain_key.toUpperCase()}{item.activity_rows > 0 ? ` · ${item.activity_rows.toLocaleString()} 行` : ""}</small>
        </div>
      ),
    };
  });

  return (
    <AutoComplete
      className={className}
      style={style}
      value={value}
      options={options}
      open={open && options.length > 0}
      onDropdownVisibleChange={setOpen}
      onFocus={() => { setOpen(true); void load(value); }}
      onSearch={onChange}
      onChange={onChange}
      onSelect={(selected, option) => {
        setOpen(false);
        const item = (option as { item?: AddressLibraryItem }).item;
        if (item) {
          // 回写真实地址（组合值仅用于 option 唯一标识，不得进入业务输入）
          onChange(item.address);
          onSelect?.(item.address, item);
        } else {
          onSelect?.(selected, item);
        }
      }}
    >
      <Input
        allowClear={allowClear}
        prefix={prefix}
        placeholder={placeholder}
        aria-label={ariaLabel}
        onPressEnter={onPressEnter}
      />
    </AutoComplete>
  );
}
