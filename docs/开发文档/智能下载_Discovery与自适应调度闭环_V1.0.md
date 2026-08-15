# 智能下载下一阶段优化：Discovery + 自适应调度闭环 V1.0
## Smart Download Discovery & Adaptive Scheduling Feedback Loop

> 上一阶段已经明确：
> - SQD Cloud 只作为最终兜底；
> - Cloud S / L / XL 根据任务资源需求弹性选择；
> - 高性能服务器不能靠“地址大小”粗暴判断，而要根据 `Address × Dataset × Range` 实际工作量决策。
>
> 因此，下一个最值得做的优化方向不是继续增加 Provider，也不是继续堆 Cloud 服务器，而是：
>
> **把“任务规模探测 → Provider 选择 → 实际运行反馈 → 动态重调度”做成闭环。**
>
> 没有这一层，Smart Scheduler 仍然只是静态规则；有了这一层，系统才真正开始“智能”。

---

# 1. 下一优化方向结论

建议下一阶段正式建设：

```text
Smart Discovery Engine V2
+
Adaptive Scheduler V3
+
Execution Feedback Loop V1
```

核心目标：

```text
任务创建
↓
Discovery
↓
估算真实规模
↓
选择 Provider / Cloud Tier
↓
执行
↓
采集真实吞吐 / 失败率 / ETA
↓
修正估算
↓
重新调度
↓
形成历史画像
↓
下一次任务更准
```

重点不是“多一个调度算法”，而是让系统具备：

```text
预测
→ 执行
→ 观察
→ 修正
→ 再决策
```

能力。

---

# 2. 为什么这是当前最优先方向

目前已经有：

```text
Provider Router
Provider Health
Circuit Breaker
SQD / RPC / CSV
SQD Cloud
Cloud S/L/XL
Checkpoint V3 设计
Range Ledger 设计
Validation 设计
```

但仍缺一个关键中枢：

```text
这个地址的这个 Dataset 到底有多大？
```

如果不知道规模，后面很多决策都只能猜：

```text
是否 CSV？
是否 SQD？
是否 RPC？
是否进入 Cloud？
Cloud L 还是 XL？
Range 应该切多大？
并发多少？
ETA 多少？
是否应该提前限制时间范围？
```

因此 Discovery 是整个智能调度系统的“传感器”。

---

# 3. Discovery 的最终调度粒度

不要只给地址做一个总估算。

必须做到：

```text
Address × Dataset
```

必要时进一步：

```text
Address × Dataset × Time/Block Segment
```

例如：

```text
0xAAA

Transactions
  2022   8,200
  2023  11,000
  2024  90,000
  2025 420,000
  2026 810,000

Token Transfers
  2022   2,100
  2023   8,000
  2024 250,000
  2025 3,800,000
  2026 7,200,000
```

这样调度器才能发现：

```text
同一个地址早期很小
后期突然变成大地址
```

并做分段调度。

---

# 4. Discovery 必须输出什么

建议统一：

```go
type DiscoveryResult struct {
    ChainID   int64
    Address   string
    Dataset   DatasetType

    FirstBlock uint64
    LastBlock  uint64

    EstimatedRows  uint64
    EstimatedBytes uint64

    EstimatedTempBytes uint64

    EstimatedRuntimeByProvider map[string]time.Duration
    EstimatedCostByProvider    map[string]float64

    ActivityDensity float64

    SuggestedRangeSpan uint64

    SupportedProviders []ProviderCandidate

    Confidence float64

    CreatedAt time.Time
}
```

---

# 5. ProviderCandidate

```go
type ProviderCandidate struct {
    Provider string

    Supported bool

    EstimatedRowsPerSecond  float64
    EstimatedBytesPerSecond float64

    EstimatedRuntime time.Duration
    EstimatedCost    float64

    CoverageScore float64
    HealthScore   float64

    ResumeSupport bool
    RangeSupport  bool

    Score float64
}
```

这样 Discovery 结果可以直接进入 Scheduler。

---

# 6. Discovery 不要求精确到个位数

这一点很重要。

Discovery 不是正式数据下载。

目标：

```text
正确识别数量级
```

例如真实：

```text
8,243,211 rows
```

Discovery 得出：

```text
7.6M ～ 9.2M
```

已经足够用于：

```text
CSV 不适合
SQD 合适
Cloud L 可能不足
Cloud XL 候选
```

不应该为了“估算准确”先把所有数据扫一遍。

---

# 7. Discovery 分三层

建议分：

```text
L0 Metadata Discovery
L1 Lightweight Probe
L2 Adaptive Sample
```

---

# 8. L0：Metadata Discovery

优先使用无需扫描的数据源。

例如：

- Provider total count
- 首页分页 total
- explorer API total
- 已有 Dataset Registry
- 历史任务记录
- first-seen / last-seen
- block activity metadata

如果能直接获得：

```text
total = 8243211
```

则：

```text
confidence = 0.95+
```

无需继续深 probe。

---

# 9. L1：Lightweight Probe

如果没有 total：

选择：

```text
首段
中间段
尾段
```

例如：

```text
全范围 40M → 50M blocks

Sample:
40.0M → 40.05M
45.0M → 45.05M
49.95M → 50M
```

获取：

```text
rows / block
```

初步外推。

---

# 10. L2：Adaptive Sample

如果首中尾密度差异很大：

```text
variance > threshold
```

自动增加样本。

例如：

```text
3 samples
↓
variance high
↓
8 samples
↓
仍然高
↓
分段建模
```

最终得到：

```text
Segment 1: low activity
Segment 2: medium
Segment 3: high
```

而不是整个历史范围使用一个平均密度。

---

# 11. Activity Segmentation

建议新增：

```text
ActivitySegment
```

结构：

```go
type ActivitySegment struct {
    FromBlock uint64
    ToBlock   uint64

    EstimatedRows uint64

    Density float64

    Confidence float64
}
```

调度器可以：

```text
低密度区
→ 大 Range

高密度区
→ 小 Range
```

---

# 12. 动态 Range 大小

现在固定：

```text
50k blocks / Range
```

只能作为初始默认。

后续应该：

```text
TargetRowsPerRange
```

决定 Range。

例如目标：

```text
25k ~ 100k rows / Range
```

低活跃地址：

```text
500k blocks / Range
```

高活跃地址：

```text
5k blocks / Range
```

超级活跃地址：

```text
500 blocks / Range
```

这样每个 Range 的执行耗时更均衡。

---

# 13. Range 大小的意义

Range 太大：

- Provider 失败重试损失大
- 切换 Provider 时重拉数据多
- checkpoint 粒度太粗
- 单 Range 超时
- 内存压力大

Range 太小：

- 请求数量爆炸
- checkpoint 太频繁
- 文件 part 过多
- Provider API 开销高

因此需要动态目标。

---

# 14. 推荐 Range Planner

输入：

```text
EstimatedRowsPerBlock
ProviderMaxWindow
ProviderMaxRows
TargetRangeRuntime
TargetRowsPerRange
```

输出：

```text
SuggestedBlockSpan
```

推荐目标：

```text
单 Range 执行时间：
30s ～ 3min
```

这样：

- 失败成本低；
- Provider 切换快；
- checkpoint 可控；
- 并发调度容易。

---

# 15. Adaptive Scheduler V3

现有 Scheduler 评分保留，但加入 Discovery。

建议：

```text
ProviderScore =
  CapabilityScore
+ CoverageScore
+ HealthScore
+ HistoricalSuccessScore
+ EstimatedSpeedScore
+ ResumeScore
+ CostScore
+ LocalityScore
- FailurePenalty
- RateLimitPenalty
- EstimatedRuntimePenalty
```

---

# 16. LocalityScore

新增“数据本地性”。

如果 Dataset Registry 已有：

```text
80% Range
```

那么：

```text
Local Dataset
```

应该成为第一优先级。

例如：

```text
请求：
2024 → 2026

本地：
2024 → 2025

只下载：
2026
```

这是后续大规模重复调查最重要的性能优化之一。

---

# 17. Provider 不是一次性选择

不应该：

```text
创建任务时决定 SQD
然后永远 SQD
```

应该：

```text
Initial Select
↓
Runtime Monitor
↓
Reevaluate
↓
Keep / Retry / Switch / Scale
```

---

# 18. Execution Feedback Loop

运行过程中持续采集：

```go
type ExecutionMetrics struct {
    Provider string
    Dataset  DatasetType

    RowsPerSecond  float64
    BytesPerSecond float64
    BlocksPerSecond float64

    RequestSuccessRate float64

    HTTP429Rate float64
    HTTP503Rate float64
    TimeoutRate float64

    P50Latency time.Duration
    P95Latency time.Duration

    RetryCount int

    CPUUsage    float64
    MemoryUsage float64

    CurrentETA time.Duration
}
```

---

# 19. 动态重调度触发条件

建议满足任意：

```text
actual_speed < expected_speed × 0.35
持续 3～5 min
```

或：

```text
ETA > original ETA × 2
```

或：

```text
503 rate > 20%
```

或：

```text
429 rate > 10%
```

或：

```text
连续 timeout >= 3
```

或：

```text
Provider circuit OPEN
```

或：

```text
Validator 检测到 silent gap
```

触发：

```text
Reevaluate()
```

---

# 20. Reevaluate 不等于立刻切换

调度器先判断：

```text
1. 原 Provider Retry 是否更便宜？
2. 是否只需要降低并发？
3. 是否应该缩小 Range？
4. 是否应该切 Provider？
5. 是否应该进入 SQD Cloud？
6. Cloud 是否需要升级 Tier？
```

因此动作类型建议统一：

```text
KEEP
RETRY
THROTTLE
REDUCE_RANGE
SWITCH_PROVIDER
ENTER_CLOUD
SCALE_UP_CLOUD
SCALE_DOWN_CLOUD
FAIL
```

---

# 21. 例子：SQD 503

当前：

```text
SQD
30k rows/s
```

突然：

```text
503 rate 35%
```

不要马上 Cloud。

执行：

```text
并发 8
↓
降到 4
↓
降到 2
↓
进入 cooldown
```

如果恢复：

```text
继续 SQD
```

如果：

```text
circuit OPEN
```

才：

```text
Scheduler.SelectNext()
```

---

# 22. 例子：RPC 慢但稳定

RPC：

```text
100% success
但 ETA 11h
```

虽然没有失败，仍应该重调度。

因为：

```text
runtime penalty
```

过高。

可以切：

```text
SQD
```

或者最终：

```text
SQD Cloud
```

所以“失败”不是唯一切换条件。

---

# 23. 例子：Cloud L 不够快

```text
Cloud L
预计 25min

10min 后：
完成 9%
ETA 96min
```

Feedback Loop：

```text
Reevaluate
↓
Provider 没问题
↓
Resource 不足
↓
SCALE_UP_CLOUD
↓
Cloud XL
```

而不是切 Provider。

---

# 24. Provider Historical Profile

把每次实际运行结果保存。

按：

```text
Chain
+ Dataset
+ Provider
+ Scale Bucket
```

建画像。

例如：

```json
{
  "chain_id": 56,
  "dataset": "token_transfers",
  "provider": "sqd",
  "scale_bucket": "1M-5M",
  "jobs": 182,
  "success_rate": 0.94,
  "avg_rows_per_sec": 38422,
  "p95_runtime_sec": 912,
  "http_503_rate": 0.032
}
```

---

# 25. 为什么历史画像很重要

下一次 Scheduler 不需要继续依赖人工阈值。

例如系统经过 100 次任务后发现：

```text
BSC Token Transfer
1M～5M rows

SQD:
38k rows/s
94% success

RPC:
4k rows/s
99% success
```

那么：

```text
正常下载优先 SQD
缺口补洞优先 RPC
```

自然形成。

---

# 26. 文件系统持久化

项目不引入数据库。

建议：

```text
smart-download/
└── scheduler/
    ├── provider-profiles/
    │   ├── bsc_token_transfers_sqd.json
    │   └── ...
    ├── discovery-cache/
    ├── runtime-metrics/
    └── scheduler-events.ndjson
```

---

# 27. Discovery Cache

同一个地址短时间内重复任务，不要重复 Probe。

Key：

```text
chain
+ address
+ dataset
+ range
```

TTL：

```text
活跃地址：1h
普通地址：6h
历史闭合范围：长期缓存
```

历史已结束区间：

```text
2023
```

可以近似永久缓存。

---

# 28. Discovery 也要复用本地 Dataset Registry

优先：

```text
Dataset Registry
↓
已有 Range Coverage
↓
只 Probe 未覆盖范围
```

不要：

```text
本地已经下载了 95%
还去全量 Probe
```

---

# 29. Probe 成本守卫

Discovery 不能变成新的瓶颈。

要求：

```text
Probe Cost
<
Expected Download Cost × 1%
```

或者：

```text
Probe Runtime
<
min(30s, expected_download_runtime × 5%)
```

超出就停止继续采样，使用当前估算。

---

# 30. Confidence

Discovery 结果必须有：

```text
Confidence
```

建议：

```text
>= 0.9  High
0.7-0.9 Medium
< 0.7   Low
```

Low Confidence 时 Scheduler：

```text
使用保守 Range
降低初始并发
避免直接使用昂贵 Cloud XL
```

除非触发强制规则。

---

# 31. 调度解释

虽然用户不需要选 Provider，但系统应该可解释。

例如：

```text
当前 Provider：SQD

原因：
- 预计 8.2M rows
- CSV 超出合理规模
- RPC ETA 4h18m
- SQD 当前健康度 91
- SQD 历史同类任务成功率 94%
```

切换：

```text
SQD → RPC

原因：
- 连续 503
- Circuit OPEN
- RPC 当前健康度 88
- 剩余仅 3 个小 Range
```

---

# 32. 前端怎么展示

主界面只显示：

```text
智能调度中
```

详情可展开：

```text
数据规模：
预计 8.2M rows

当前：
SQD

预计：
18 min

调度置信度：
高
```

Provider 变化：

```text
SQD → RPC
已自动切换
已完成数据无需重新下载
```

---

# 33. 前端不展示 Score 数学细节

普通用户不需要：

```text
ProviderScore = 82.331
```

管理员/调试页可以看。

普通用户只看：

```text
为什么选择
为什么切换
是否需要重新下载
ETA
```

---

# 34. 目录建议

新增：

```text
internal/smartdownload/
├── discovery/
│   ├── service.go
│   ├── metadata.go
│   ├── sampler.go
│   ├── adaptive_sampler.go
│   ├── segmenter.go
│   ├── estimator.go
│   ├── cache.go
│   └── confidence.go
│
├── scheduler/
│   ├── scheduler.go
│   ├── scorer.go
│   ├── policy.go
│   ├── candidate.go
│   ├── reevaluate.go
│   └── history.go
│
├── rangeplanner/
│   ├── planner.go
│   └── adaptive_span.go
│
└── feedback/
    ├── collector.go
    ├── aggregator.go
    ├── trigger.go
    └── profile_writer.go
```

---

# 35. Phase A：先做 Discovery

实现：

```text
DiscoveryResult
Metadata Probe
Lightweight Probe
Adaptive Sample
Confidence
Discovery Cache
```

验收：

```text
10 个已知真实地址
```

比较：

```text
estimated rows
vs
actual rows
```

目标：

```text
80% 的任务估算误差 < ±30%
数量级判断准确率 > 95%
```

---

# 36. Phase B：Adaptive Range Planner

实现：

```text
根据 density
自动决定 block span
```

验收：

```text
低活跃地址
中活跃地址
超活跃地址
```

要求：

```text
单 Range 平均执行时间趋近目标区间
```

例如：

```text
30s～3min
```

---

# 37. Phase C：Scheduler V3

接入：

```text
Discovery
Provider Health
Historical Profile
Coverage
Cost
Resume Support
```

输出：

```text
Provider
Range Size
Concurrency
Cloud Tier Candidate
```

---

# 38. Phase D：Feedback Loop

实时采集：

```text
throughput
503
429
timeout
ETA
```

触发：

```text
THROTTLE
REDUCE_RANGE
SWITCH
CLOUD
SCALE
```

---

# 39. Phase E：历史自学习

初期不要上 ML。

只做：

```text
EWMA
Rolling Success Rate
P50
P95
Scale Bucket
```

已经足够。

例如：

```text
rows_per_sec_ewma
success_rate_30d
503_rate_7d
p95_runtime
```

---

# 40. 为什么暂时不要上 Agent

Provider 选择大多数是结构化问题。

输入：

```text
rows
bytes
health
coverage
latency
cost
resume
```

规则/评分更：

```text
稳定
可解释
可测试
```

Agent 只用于：

```text
Provider 返回异常结构
无法识别的网页下载
特殊站点
规则无法分类的新数据源
```

不应该作为主调度器。

---

# 41. 最关键的验收 Case

## Case 1：小地址

```text
estimated 8k
actual 9k
→ CSV
```

## Case 2：中地址

```text
estimated 850k
→ SQD
```

## Case 3：超大地址

```text
estimated 12M
→ SQD / Cloud XL Candidate
```

## Case 4：SQD 运行退化

```text
初始 SQD
↓
503 rate 30%
↓
throttle
↓
circuit open
↓
RPC
```

## Case 5：RPC 无错误但过慢

```text
ETA 8h
↓
reevaluate
↓
SQD Cloud
```

## Case 6：Cloud L 估错

```text
ETA 25min
↓
实际 100min
↓
Cloud XL
```

---

# 42. 和上一阶段 Cloud Planner 的衔接

上一阶段：

```text
CloudResourcePlanner
```

不独立做估算。

直接消费：

```text
DiscoveryResult
```

例如：

```text
Discovery
↓
EstimatedRows
EstimatedBytes
DatasetComplexity
EstimatedRuntime
TempSpace
↓
Cloud Resource Planner
↓
S / L / XL
```

这样避免两套估算逻辑。

---

# 43. 和 Validation 的衔接

Validation 结果也反向影响 Scheduler。

例如：

```text
SQD 多次出现 silent gap
```

虽然 HTTP 成功率很高，但：

```text
Coverage Reliability
```

应该下降。

因此 Historical Profile 不只记录：

```text
请求成功
```

还记录：

```text
最终验证成功
```

真正的 Provider Success 应定义为：

```text
Download Success
+
Validation Success
```

---

# 44. 新 Provider 成功率定义

建议：

```text
TransportSuccessRate
ValidationSuccessRate
FinalSuccessRate
```

例如：

```text
Transport:   99.2%
Validation:  92.1%
Final:       91.4%
```

Scheduler 应主要看：

```text
FinalSuccessRate
```

而不是 HTTP 200。

---

# 45. 最终形成完整闭环

```text
                Discovery
                    ↓
              Estimate Scale
                    ↓
             Adaptive Range
                    ↓
             Smart Scheduler
                    ↓
            Provider / Cloud
                    ↓
                 Execute
                    ↓
              Runtime Metrics
                    ↓
                Reevaluate
              ↙      ↓      ↘
          Throttle  Switch  Scale
              ↘      ↓      ↙
                 Continue
                    ↓
                Validate
                    ↓
              Final Outcome
                    ↓
            Historical Profile
                    ↓
                Discovery
```

这才是完整的“智能下载”。

---

# 46. 推荐优先级

当前建议优先级：

```text
P0
Discovery Engine
Adaptive Range Planner

P0
Scheduler V3
Runtime Feedback

P1
Historical Provider Profile

P1
Dataset Registry 增量复用

P1
Cloud Tier 动态升降级

P2
Agent 特殊决策
```

---

# 47. 当前最应该做的第一件事

如果下一步马上让 Codex 实施：

```text
先不要继续修改 SQD Cloud Worker。
```

应该先实现：

```text
DiscoveryResult
+
Lightweight Probe
+
Adaptive Sampling
+
Confidence
+
Dynamic Range Planner
```

因为：

```text
Smart Scheduler
Cloud S/L/XL
ETA
Cost
Range Size
```

后面全部依赖这份估算。

---

# 48. 一句话总结

> **下一阶段最重要的优化，是让智能下载系统从“按照固定规则选择下载器”，升级成“先探测真实数据规模、执行中持续观察、发现估算错误后自动重新调度”的闭环系统。**

这样后面无论增加新的 Provider、Cloud 服务器还是新的链，都不需要重新写一套判断逻辑。
