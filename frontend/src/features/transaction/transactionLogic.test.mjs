import { test } from "node:test";
import assert from "node:assert/strict";
import { appendUniqueBy, compareTransferOrder, resourceTotal } from "./transactionLogic.ts";

test("Token Transfer 按 log_index 、batch_index 稳定升序", () => {
  const rows = [
    { log_index: 21, batch_index: 2 },
    { log_index: 20, batch_index: 9 },
    { log_index: 21, batch_index: 0 },
  ].sort(compareTransferOrder);
  assert.deepEqual(rows, [
    { log_index: 20, batch_index: 9 },
    { log_index: 21, batch_index: 0 },
    { log_index: 21, batch_index: 2 },
  ]);
});

test("Tab 计数优先 total_count，兼容 count/items", () => {
  assert.equal(resourceTotal(518, 500, 500), 518);
  assert.equal(resourceTotal(undefined, 3, 3), 3);
  assert.equal(resourceTotal(undefined, undefined, 2), 2);
});

test("追加分页按事件唯一键去重并保留顺序", () => {
  const current = [{ log_index: 20, batch_index: 0 }, { log_index: 21, batch_index: 0 }];
  const incoming = [{ log_index: 21, batch_index: 0 }, { log_index: 21, batch_index: 1 }, { log_index: 22, batch_index: 0 }];
  assert.deepEqual(
    appendUniqueBy(current, incoming, (item) => `${item.log_index}:${item.batch_index}`),
    [{ log_index: 20, batch_index: 0 }, { log_index: 21, batch_index: 0 }, { log_index: 21, batch_index: 1 }, { log_index: 22, batch_index: 0 }],
  );
});
