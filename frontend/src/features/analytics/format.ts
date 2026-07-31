// 数字格式化工具。
export function formatNumber(n: number): string {
  return n.toLocaleString("en-US");
}

export function shortAddr(addr: string, head = 8, tail = 6): string {
  if (!addr || addr.length <= head + tail + 3) return addr || "";
  return `${addr.slice(0, head)}…${addr.slice(-tail)}`;
}
