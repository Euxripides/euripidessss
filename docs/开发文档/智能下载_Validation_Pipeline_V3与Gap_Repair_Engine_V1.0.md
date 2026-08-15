# 智能下载下一阶段：Validation Pipeline V3 + Gap Repair Engine V1.0
## 数据完整性校验、缺口定位、自动补洞与最终可信结果层

> 前置阶段：
>
> - 四层任务模型：BatchJob → AddressJob → DatasetJob → RangeJob
> - Universal Checkpoint V3
> - Range Ledger
> - Provider Adapter
> - Discovery / Probe
> - Adaptive Range Planner
> - Scheduler V3
> - Runtime Feedback Loop
>
> 下一步最重要的目标：
>
> **让系统不只是“下载完”，而是能够证明“数据完整、没有漏、没有重复、Provider 切换后结果一致”，并在发现缺口时自动补洞。**
>
> 这是智能下载从“下载工具”升级为“可信数据系统”的关键阶段。

---

# 1. 本阶段核心结论

建议正式建设：

```text
Validation Pipeline V3
+
Gap Detection Engine V1
+
Gap Repair Orchestrator V1
+
Final Dataset Certification V1
```

完整链路：

```text
Download
↓
Normalize
↓
Merge
↓
Dedup
↓
Range Coverage
↓
Provider Count Reconcile
↓
Cross-Provider Sample Check
↓
Gap Detection
↓
Gap Repair
↓
Revalidate
↓
Certified Final Dataset
```

只有最终通过认证的数据，才允许：

```text
DatasetJob → COMPLETED
```

否则：

```text
PARTIAL
REPAIRING
VALIDATION_FAILED
```

---

# 2. 为什么现在必须做 Validation

智能调度越复杂，越容易遇到：

```text
CSV 下载一部分
↓
切 SQD
↓
SQD 503
↓
切 RPC
↓
RPC 补一部分
↓
Cloud 最终补洞
```

如果没有统一验证层，很难回答：

```text
到底有没有漏？
有没有重复？
有没有重叠区间？
某个 Provider 是否静默漏数据？
Provider 切换后字段有没有错？
Part 有没有重复写？
```

所以 Validation 必须成为任务状态机中的强制阶段，而不是可选工具。

---

# 3. Validation 分层

建议正式定义 6 层。

```text
L1 File Integrity
L2 Record Integrity
L3 Range Coverage
L4 Provider Reconciliation
L5 Gap Detection & Repair
L6 Cross-Provider Validation
```

---

# 4. L1：文件完整性

检查所有最终和 staging Part：

```text
文件存在
文件大小 > 0
Parquet footer 可读
Schema 与 Canonical Schema 匹配
SHA256 正确
Row Count 可读
Part 编号合法
```

必须检查：

```text
duplicate part sha == 0
```

允许同一个 SHA 被发现，但不能重复计入结果。

---

# 5. Part Registry 校验

最终：

```text
parts.json
```

必须满足：

```text
part_no unique
sha256 unique
range_id valid
dataset_job_id valid
rows > 0
committed = true
```

如果：

```text
part-000018.parquet
```

和：

```text
part-000019.parquet
```

SHA 相同：

```text
只保留一个有效提交
另一个进入 duplicate_part quarantine
```

---

# 6. L2：记录完整性

不同 Dataset 使用不同唯一键。

## Transactions

```text
chain_id + transaction_hash
```

## Token Transfer / Logs

```text
chain_id
+ transaction_hash
+ log_index
```

## Internal Transactions

```text
chain_id
+ transaction_hash
+ trace_address
```

## NFT

```text
chain_id
+ transaction_hash
+ log_index
+ token_id
```

---

# 7. Record Validation

检查：

```text
chain_id 合法
block_number 合法
transaction_hash 格式合法
address 格式合法
log_index 合法
trace_address 合法
token_address 合法
block_time 可解析
```

还需要：

```text
record belongs to requested address
```

例如 Token Transfer：

```text
from_address == target
OR
to_address == target
```

避免 Provider 返回了请求范围内其他地址数据。

---

# 8. L3：Range Coverage

这是当前最关键的完整性校验。

目标：

```text
Requested Range
=
Valid Completed Ranges
∪ Confirmed Empty Ranges
```

并且：

```text
Unknown Range = ∅
```

---

# 9. Range Coverage 示例

请求：

```text
40,000,000 → 40,500,000
```

账本：

```text
40,000,000 → 40,099,999 VALID
40,100,000 → 40,199,999 VALID
40,200,000 → 40,299,999 UNKNOWN
40,300,000 → 40,399,999 EMPTY_CONFIRMED
40,400,000 → 40,500,000 VALID
```

结果：

```text
Coverage != 100%
```

Dataset 不能 COMPLETED。

系统应自动生成：

```text
Repair Range:
40,200,000 → 40,299,999
```

---

# 10. Interval Set

不要用简单数组直接比较。

实现：

```text
IntervalSet
```

支持：

```text
Add
Merge
Subtract
Intersect
FindGap
```

例如：

```go
type BlockInterval struct {
    From uint64
    To   uint64
}
```

---

# 11. Coverage 计算

```text
coverage_blocks =
sum(valid_ranges)
+
sum(confirmed_empty_ranges)

coverage_ratio =
coverage_blocks / requested_blocks
```

要求：

```text
coverage_ratio == 1.0
unknown_ranges == 0
```

---

# 12. Confirmed Empty Range

空结果不能直接认为下载失败。

如果 Provider 明确返回：

```text
0 records
```

且请求成功：

```text
ConfirmedEmpty = true
```

Range Ledger 记录：

```json
{
  "event": "RANGE_EMPTY_CONFIRMED",
  "range_id": "rng_xxx",
  "provider": "sqd"
}
```

这样覆盖率才能达到 100%。

---

# 13. 空 Range 的二次确认

对于高风险 Dataset：

```text
logs
token_transfers
internal_transactions
```

如果某 Range：

```text
前后 Range 都有大量数据
但中间突然 0
```

不能立即完全相信。

应该标记：

```text
SUSPICIOUS_EMPTY
```

交给第二 Provider 抽查。

---

# 14. L4：Provider Count 对账

如果 Provider 可以提供：

```text
total_count
```

则校验：

```text
expected_total
vs
actual_unique_total
```

---

# 15. Count Reconciliation

定义：

```text
CountDelta =
abs(expected - actual)

CountDeltaRatio =
CountDelta / expected
```

规则：

```text
delta = 0
→ PASS

delta <= configured tolerance
→ WARN

delta > tolerance
→ FAIL / REPAIR
```

对于链上交易数据，默认应尽量：

```text
tolerance = 0
```

除非 Provider 本身 total 有已知延迟。

---

# 16. 多 Provider Count

例如：

```text
SQD total       1,281,442
Explorer total  1,281,442
Final unique    1,281,442
```

高可信。

如果：

```text
SQD             1,281,442
Explorer        1,281,440
Final           1,281,442
```

记录差异，但不一定失败。

应结合：

```text
Range Coverage
Cross Check
```

判断。

---

# 17. L5：Gap Detection Engine

Gap 不只来自 Range Ledger。

还需要识别“数据内部缺口”。

---

# 18. Gap 类型

建议至少：

```text
RANGE_GAP
COUNT_GAP
SEQUENCE_GAP
SUSPICIOUS_EMPTY
PROVIDER_DIVERGENCE
PART_GAP
TIME_GAP
```

---

# 19. RANGE_GAP

最明确：

```text
requested range
中存在没有 VALID / EMPTY 的 block interval
```

直接生成 Repair Range。

---

# 20. COUNT_GAP

Provider 声称：

```text
100,000 records
```

Final：

```text
99,800
```

但 Range Coverage 100%。

说明：

```text
某些 Range 内部可能漏记录
```

需要定位。

---

# 21. COUNT_GAP 定位

采用二分：

```text
Whole Range
↓
Compare Count
↓
Left / Right
↓
继续二分
↓
找到异常 Range
```

最终缩小：

```text
40,221,000 → 40,223,000
```

然后用第二 Provider 重查。

---

# 22. SUSPICIOUS_EMPTY

例如：

```text
Range A: 12,000 rows
Range B: 0 rows
Range C: 11,500 rows
```

如果 B 长度相同且地址活跃度高：

```text
SuspicionScore ↑
```

超过阈值：

```text
Cross Check B
```

---

# 23. Provider Divergence

同一 Range：

```text
SQD: 12,000
RPC: 12,341
```

触发：

```text
PROVIDER_DIVERGENCE
```

比较：

```text
unique keys
```

而不是只比较数量。

---

# 24. Gap Repair Orchestrator

发现 Gap 后：

```text
Validation
↓
GapDetection
↓
RepairPlanner
↓
创建 RepairRangeJob
↓
选择不同 Provider
↓
下载
↓
Normalize
↓
Merge
↓
Revalidate
```

---

# 25. Repair Range 不新建 DatasetJob

保持：

```text
同一个 DatasetJob
```

只是 Range：

```text
purpose = REPAIR
```

例如：

```go
type RangePurpose string

const (
    RangePrimary RangePurpose = "PRIMARY"
    RangeRepair  RangePurpose = "REPAIR"
    RangeVerify  RangePurpose = "VERIFY"
)
```

---

# 26. Repair Provider 选择

Repair 不一定使用主 Provider。

优先：

```text
1. 未使用过的可靠 Provider
2. RPC 精确补洞
3. SQD
4. SQD Cloud
```

例如主任务：

```text
SQD
```

发现缺口：

```text
Repair → RPC
```

更加合理。

---

# 27. Repair 黑名单

如果 Gap 很可能由：

```text
SQD silent missing
```

造成：

```text
Repair Candidate
```

不应该第一时间继续选择同一个 SQD Endpoint。

将：

```text
provider + range
```

加入临时排除列表。

---

# 28. Repair Attempts

每个 Gap 限制：

```text
max_repair_attempts
```

例如：

```text
3
```

顺序：

```text
RPC A
↓ fail
RPC B
↓ fail
SQD Cloud
```

超过后：

```text
PARTIAL
```

而不是无限重试。

---

# 29. L6：Cross-Provider Validation

针对大地址和高价值任务进行抽样。

不建议全量双 Provider 下载，成本太高。

---

# 30. Sampling Strategy

按：

```text
Range Count
Estimated Rows
Risk
Provider Reliability
```

决定样本。

例如：

```text
小任务：
1～3 Range

中任务：
5 Range

大任务：
10～20 Range

超大任务：
0.5%～1% Range
```

---

# 31. 抽样不是纯随机

组合：

```text
随机 Range
+
高密度 Range
+
Provider 切换边界
+
曾经 Retry 的 Range
+
Suspicious Empty
```

这些地方最容易出问题。

---

# 32. Cross Check 内容

至少比较：

```text
row count
unique key set
min/max block
min/max timestamp
```

高价值 Dataset 再比较：

```text
from/to
token
value
```

---

# 33. Cross Check Score

例如：

```text
Sample Ranges: 10
Matched: 10

Key Match: 100%
Count Match: 100%
```

结果：

```text
PASS
```

如果：

```text
Key Match < 99.99%
```

触发：

```text
PROVIDER_DIVERGENCE
```

---

# 34. Validation Score

前端需要统一分数。

建议：

```text
ValidationScore =
  FileScore
+ RecordScore
+ CoverageScore
+ CountScore
+ GapScore
+ CrossCheckScore
```

但最终状态不能只靠总分。

---

# 35. Hard Gates

以下任何一个失败：

```text
Parquet unreadable
Schema invalid
Unknown Range > 0
duplicate unique keys unresolved
critical count mismatch
```

都不能：

```text
COMPLETED
```

---

# 36. Validation Status

统一：

```text
NOT_STARTED
RUNNING
PASS
WARN
REPAIRING
PARTIAL
FAILED
```

---

# 37. Dataset Certification

最终生成：

```text
validation-certificate.json
```

示例：

```json
{
  "dataset_job_id": "ds_xxx",
  "status": "PASS",

  "requested_range": {
    "from_block": 40000000,
    "to_block": 50000000
  },

  "coverage": 1.0,

  "rows": {
    "raw": 1289550,
    "normalized": 1289550,
    "unique": 1281442,
    "final": 1281442
  },

  "duplicates_removed": 8108,

  "parts": {
    "count": 52,
    "duplicate_sha": 0
  },

  "gaps": {
    "detected": 2,
    "repaired": 2,
    "remaining": 0
  },

  "cross_check": {
    "sample_ranges": 10,
    "matched": 10
  },

  "certified_at": "..."
}
```

---

# 38. Final Dataset 只有认证后发布

目录：

```text
raw/
staging/
canonical/
final/
```

流程：

```text
canonical
↓
validation pass
↓
atomic publish
↓
final
```

不要边下载边让下游读取 final。

---

# 39. Atomic Publish

建议：

```text
final.tmp/
↓
Validation PASS
↓
atomic rename
↓
final/
```

Dataset Registry 只有在：

```text
final/
```

发布后更新为：

```text
CERTIFIED
```

---

# 40. Dataset Registry 增加状态

建议：

```text
DOWNLOADING
VALIDATING
REPAIRING
CERTIFIED
PARTIAL
FAILED
```

只有：

```text
CERTIFIED
```

可以被：

```text
地址画像
关系图
智能调查
```

默认直接复用。

---

# 41. PARTIAL 数据怎么处理

如果最终：

```text
99.98% coverage
2 ranges 无法获取
```

不要假装完整。

状态：

```text
PARTIAL
```

前端显示：

```text
完整性：99.98%
缺失区间：2
```

允许：

```text
查看已有数据
```

但下游要携带：

```text
data_completeness = PARTIAL
```

---

# 42. 下游必须知道完整性

关系图和智能调查不能把 PARTIAL 当成全量。

结果 API 返回：

```json
{
  "certification": "PARTIAL",
  "coverage": 0.9998
}
```

下游可以显示：

```text
数据可能存在少量缺口
```

---

# 43. Validation Event

后续 SSE：

```text
validation.started
validation.progress
gap.detected
repair.started
repair.completed
validation.completed
```

---

# 44. 前端任务详情

建议显示：

```text
完整性校验

文件        PASS
唯一键      PASS
区间覆盖    100%
Provider对账 PASS
缺口        2 → 已修复
交叉验证    PASS

最终：
已验证
```

---

# 45. 修复过程前端

例如：

```text
检测到缺口

44,200,000 → 44,210,000

原 Provider：
SQD

补洞 Provider：
RPC

状态：
Repairing 68%
```

---

# 46. Validation Worker

不要占用下载 Worker。

建议独立：

```text
Download Pool
Validation Pool
Repair Pool
```

例如：

```text
download workers: 32
validation workers: 4
repair workers: 8
```

---

# 47. DuckDB 在 Validation 中的作用

DuckDB 非常适合：

```text
COUNT
COUNT DISTINCT
MIN/MAX
GROUP BY block
GROUP BY tx hash
Anti Join
Diff
```

例如：

```sql
SELECT
  COUNT(*) AS rows,
  COUNT(DISTINCT unique_key) AS unique_rows
FROM read_parquet(...);
```

---

# 48. Provider Diff

将第二 Provider 的样本写到 temporary table：

```text
sample_provider_a
sample_provider_b
```

通过：

```text
ANTI JOIN
```

找差异。

---

# 49. Gap Detector 代码结构

建议：

```text
internal/smartdownload/validation/
├── pipeline.go
├── certificate.go
├── file_validator.go
├── record_validator.go
├── coverage_validator.go
├── count_validator.go
├── crosscheck_validator.go
│
├── gap/
│   ├── detector.go
│   ├── interval.go
│   ├── suspicious_empty.go
│   ├── count_bisect.go
│   └── divergence.go
│
└── repair/
    ├── orchestrator.go
    ├── planner.go
    ├── selector.go
    └── retry_policy.go
```

---

# 50. 文件系统持久化

每 Dataset：

```text
validation/
├── validation-state.json
├── validation-certificate.json
├── gap-ledger.ndjson
├── repair-attempts.ndjson
├── crosscheck/
└── quarantine/
```

---

# 51. Gap Ledger

示例：

```json
{
  "gap_id": "gap_xxx",
  "type": "RANGE_GAP",
  "from_block": 44200000,
  "to_block": 44210000,
  "status": "DETECTED"
}
```

修复：

```json
{
  "gap_id": "gap_xxx",
  "event": "REPAIR_COMPLETED",
  "provider": "rpc",
  "rows": 2811
}
```

---

# 52. Validation 与 Checkpoint V3

Validation 不修改已完成 Range 的原始事实。

如果发现问题：

```text
Range VALID
↓
CrossCheck mismatch
```

追加：

```text
VALIDATION_REVOKED
```

然后：

```text
REPAIRING
```

保留审计历史。

---

# 53. Provider Reliability 回写

Validation 结果必须反馈给 Scheduler V3。

例如：

```text
SQD HTTP success = 100%
但 cross check 发现缺漏
```

记录：

```text
TransportSuccess = true
ValidationSuccess = false
FinalSuccess = false
```

Provider 历史评分下降。

---

# 54. 最终 Provider 成功率

建议保存：

```text
transport_success_rate
validation_success_rate
final_success_rate
gap_rate
repair_rate
```

Scheduler 主要使用：

```text
final_success_rate
```

---

# 55. 真实验收 Case A

## Provider 切换后无重复

```text
CSV → SQD
```

要求：

```text
raw > unique
final == unique
duplicate unique key == 0
duplicate part sha == 0
coverage == 100%
```

---

# 56. 真实验收 Case B

## SQD silent gap

模拟：

```text
SQD 少返回一个 Range 的部分数据
但 HTTP 200
```

要求：

```text
L4/L6 发现
↓
Gap Detector 定位
↓
RPC Repair
↓
最终 PASS
```

---

# 57. 真实验收 Case C

## 空 Range

真实地址某 Range 无交易。

要求：

```text
Confirmed Empty
```

而不是：

```text
FAILED
```

---

# 58. 真实验收 Case D

## Provider 数量不一致

```text
Provider A: 100000
Final:      99800
```

要求：

```text
二分定位异常区间
↓
第二 Provider 查询
↓
补齐
```

---

# 59. 真实验收 Case E

## Repair 仍失败

3 个 Provider 都无法获取同一 Range。

最终：

```text
PARTIAL
```

而不是：

```text
COMPLETED
```

并明确：

```text
coverage < 100%
remaining gaps > 0
```

---

# 60. 性能要求

Validation 不能重新全量读写多次。

原则：

```text
一次 Parquet Scan
尽量完成多个统计
```

例如同一 DuckDB SQL 获取：

```text
COUNT
COUNT DISTINCT
MIN BLOCK
MAX BLOCK
```

减少 IO。

---

# 61. 增量 Validation

每个 Range 下载完成后就可以执行：

```text
L1
L2
```

最终才执行：

```text
L3
L4
L6
```

这样失败可以更早发现。

---

# 62. 大 Dataset Validation

千万级行数：

```text
不要全部加载内存
```

使用：

```text
DuckDB
Parquet predicate pushdown
streaming hash
external sort
```

---

# 63. SHA 策略

保留：

```text
Part SHA256
```

最终 Dataset 还可以生成：

```text
Manifest Hash
```

不是对整个大文件重新算一个巨大 SHA。

---

# 64. Final Manifest

最终：

```text
manifest-v3.json
```

包含：

```text
Dataset
Range
Parts
Rows
Unique Rows
Providers Used
Provider Switches
Gaps
Repairs
Validation Certificate
```

---

# 65. 这一阶段完成后意味着什么

系统状态从：

```text
下载器说成功
= 成功
```

升级为：

```text
Range 全覆盖
+
Canonical Schema 正确
+
唯一键正确
+
Part 正确
+
Count 对账
+
Gap 已补
+
交叉验证
= Certified
```

这才可以作为分析系统的数据基础。

---

# 66. 下一阶段之后再做什么

Validation 完成后，后面的最佳顺序是：

```text
1. Progress Aggregator + EWMA ETA + SSE
2. 新“智能下载”前端
3. Result Data Grid
4. Dataset Registry 增量复用
5. 地址画像 / 关系图 / 智能调查联动
```

不建议在 Validation 之前先做最终前端，因为前端会再次建立在不可靠的数据完成状态上。

---

# 67. 本阶段 P0

必须完成：

```text
L1 File
L2 Record
L3 Coverage
Gap Detector
Gap Repair
Validation Certificate
PARTIAL 状态
Final Atomic Publish
```

---

# 68. P1

随后补：

```text
L4 Provider Count Reconcile
L6 Cross Provider Sampling
Suspicious Empty
Count Bisect
Provider Reliability Feedback
```

---

# 69. Codex 开发顺序

建议：

```text
Commit 1
IntervalSet + Coverage Validator

Commit 2
File / Record Validator

Commit 3
Gap Ledger + Gap Detector

Commit 4
Repair RangeJob + Repair Orchestrator

Commit 5
Validation State Machine

Commit 6
Validation Certificate

Commit 7
Atomic Final Publish

Commit 8
Provider Count Reconcile

Commit 9
Cross Provider Validation

Commit 10
Real BSC Gap Repair Tests
```

---

# 70. 一句话目标

> **下一步不是让系统“下载得更多”，而是让系统能够证明自己下载的数据是完整的；如果不完整，就自己找出缺口、换 Provider 补齐，直到生成可以被分析系统信任的 Certified Dataset。**
