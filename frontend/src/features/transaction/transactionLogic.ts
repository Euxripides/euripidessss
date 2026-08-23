export interface TransferOrderKey { log_index: number; batch_index: number }

export function compareTransferOrder(a: TransferOrderKey, b: TransferOrderKey): number {
  return a.log_index - b.log_index || a.batch_index - b.batch_index;
}

export function resourceTotal(totalCount: unknown, count: unknown, itemCount: number): number {
  const total = Number(totalCount);
  if (Number.isFinite(total) && total >= 0) return total;
  const pageCount = Number(count);
  return Number.isFinite(pageCount) && pageCount >= 0 ? pageCount : itemCount;
}

export function appendUniqueBy<T>(current: readonly T[], incoming: readonly T[], keyOf: (item: T) => string): T[] {
  const seen = new Set(current.map(keyOf));
  const merged = [...current];
  for (const item of incoming) {
    const key = keyOf(item);
    if (seen.has(key)) continue;
    seen.add(key);
    merged.push(item);
  }
  return merged;
}
