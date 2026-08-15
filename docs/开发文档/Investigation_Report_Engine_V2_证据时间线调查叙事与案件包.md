# 下一阶段优化：Investigation Report Engine V2
## Evidence Timeline + Case Narrative + Exportable Case Package + Reproducible Investigation

> 前置能力：
>
> - Smart Download 统一数据供应层
> - Dataset Registry Coverage Index V2
> - Investigation Data Cache V2
> - Graph Expansion Cache / Smart Prefetch
> - Entity Intelligence Layer V1
> - Fund Flow Intelligence V2
> - Path Scoring
> - Profit Attribution
> - Settlement Detection
> - Cashout / Exchange Landing Detection
>
> 下一阶段目标：
>
> **把“数据、图、实体、路径、获利、沉淀、交易所落点”自动整理成一份可阅读、可复核、可追溯、可重复生成的调查成果。**
>
> 本阶段正式建设：
>
> ```text
> Investigation Report Engine V2
> +
> Evidence Timeline V1
> +
> Case Narrative Engine V1
> +
> Evidence Citation Layer V1
> +
> Exportable Case Package V1
> +
> Reproducible Investigation Snapshot V1
> ```
>
> 核心原则：
>
> > **每一个结论都必须能追溯到具体地址、交易、Dataset、时间范围、算法版本和证据。**

---

# 1. 为什么这是下一步

前面的系统已经能够回答：

```text
这个地址是谁？
资金经过哪里？
谁获利？
哪里沉淀？
是否进入交易所？
哪条路径最重要？
```

但如果最终用户还需要手工：

```text
复制地址
复制交易哈希
截图关系图
整理时间线
写资金路径
统计金额
写结论
```

整个调查流程仍然没有闭环。

因此下一步应该从：

```text
分析系统
```

升级成：

```text
调查成果生产系统
```

---

# 2. 最终输出结构

每个 Investigation 最终可生成：

```text
案件摘要
↓
调查目标
↓
数据范围
↓
关键地址 / 实体
↓
关键资金路径
↓
资金时间线
↓
获利归因
↓
资金沉淀
↓
交易所 / 服务落点
↓
风险与异常
↓
证据清单
↓
数据完整性说明
↓
附录
```

---

# 3. Report 不等于 LLM 自由写作

禁止：

```text
把所有分析结果扔给 Agent
→ 直接生成一大段报告
```

正确架构：

```text
Structured Findings
↓
Evidence Resolver
↓
Report Sections
↓
Narrative Renderer
↓
Citation Injection
↓
Final Report
```

Agent / LLM 只负责：

```text
组织语言
解释结构化结果
生成摘要
```

不能负责创造事实。

---

# 4. Report Data Model

建议：

```go
type InvestigationReport struct {
    ID              string
    InvestigationID string

    Title string

    Goal string

    ChainScope []int64
    TimeRange   TimeRange

    Summary ReportSummary

    Sections []ReportSection

    EvidenceIndex []EvidenceRef

    DataCertification ReportCertification

    SnapshotID string

    EngineVersion string
    TemplateVersion string

    CreatedAt time.Time
}
```

---

# 5. ReportSection

```go
type ReportSection struct {
    ID string

    Type string
    Title string

    Findings []Finding

    Narrative string

    EvidenceIDs []string

    Confidence float64
}
```

---

# 6. Finding

```go
type Finding struct {
    ID string

    FindingType string

    SubjectIDs []string

    Statement string

    Metrics map[string]string

    Confidence float64

    EvidenceIDs []string
}
```

---

# 7. Finding Type

建议统一：

```text
KEY_ADDRESS
KEY_ENTITY
HIGH_VALUE_PATH
PROFIT_ATTRIBUTION
SETTLEMENT
CASHOUT
EXCHANGE_DEPOSIT
BRIDGE_EXIT
COLLECTOR
DISTRIBUTOR
RETURN_FLOW
ROUND_TRIP
FLOW_CONSERVATION_ANOMALY
DATA_GAP
OTHER
```

---

# 8. Evidence Citation Layer

报告里的结论不能只有：

```text
地址 A 获利 1.2M USDT
```

必须链接：

```text
Evidence IDs
```

例如：

```json
{
  "finding": "profit_001",
  "statement": "地址 0xAAA 的净获利约为 1.2M USDT",
  "evidence_ids": [
    "tx_001",
    "tx_002",
    "profit_attr_001",
    "dataset_cert_012"
  ]
}
```

---

# 9. Evidence 类型

统一：

```text
TRANSACTION
TOKEN_TRANSFER
INTERNAL_TRANSACTION
LOG
BALANCE_SNAPSHOT
ADDRESS_PROFILE
ENTITY_LABEL
ENTITY_EVIDENCE
FLOW_PATH
PROFIT_ATTRIBUTION
SETTLEMENT_SCORE
CASHOUT_RESULT
DATASET_CERTIFICATE
SCREENSHOT
USER_NOTE
EXTERNAL_DOCUMENT
```

---

# 10. EvidenceRef

```go
type EvidenceRef struct {
    ID string

    Type string

    ChainID int64

    Address string
    TxHash  string

    DatasetID string

    BlockNumber uint64
    Timestamp   *time.Time

    SourcePath string

    SourceProvider string

    Certification string

    EvidenceHash string
}
```

---

# 11. Evidence Hash

为了保证报告之后可复核：

```text
EvidenceHash
```

建议基于：

```text
Canonical Record
+
Dataset Version
+
Schema Version
```

生成 SHA256。

这样以后数据文件变动时可以检测：

```text
报告引用的数据是否仍然一致
```

---

# 12. Evidence Timeline

资金调查非常适合时间线。

新增：

```text
Evidence Timeline
```

例如：

```text
2026-03-18
地址 A 首次收到 300K USDT

2026-03-19
A 将 280K 转至 B

2026-03-19
B 在 12 分钟内拆分至 C/D/E

2026-03-20
C 向 Exchange X Deposit 转入 120K

2026-03-27
E 保留 410K，之后无明显流出
```

---

# 13. TimelineEvent

```go
type TimelineEvent struct {
    ID string

    Timestamp time.Time

    Type string

    SubjectIDs []string

    Summary string

    Amount string
    Token  string

    TxHash string

    EvidenceIDs []string

    ImportanceScore float64
}
```

---

# 14. Timeline 不要放所有交易

如果一个地址有：

```text
100 万笔交易
```

不能全放。

必须先进行：

```text
Importance Scoring
```

只保留：

```text
关键事件
```

---

# 15. Timeline Importance Score

建议：

```text
TimelineScore =
  ValueScore
+ PathImportance
+ EntityImportance
+ ProfitImpact
+ SettlementImpact
+ InvestigationGoalRelevance
```

---

# 16. Timeline Event Type

例如：

```text
FIRST_SEEN
LARGE_INFLOW
LARGE_OUTFLOW
SWEEP
DISTRIBUTION
COLLECTION
EXCHANGE_DEPOSIT
BRIDGE
PROFIT_REALIZATION
SETTLEMENT
BALANCE_CHANGE
ENTITY_RESOLUTION
```

---

# 17. Case Narrative Engine

Narrative Engine 负责把结构化 Findings 转成：

```text
人能直接阅读的调查说明
```

例如：

```text
目标地址 A 在 2026-03-18 至 2026-03-27 期间累计收到约 2.1M USDT，
其中约 1.18M USDT 通过地址 B、C 两级中转后进入 Exchange X 的已识别入金地址；
另有约 410K USDT 在地址 E 形成持续沉淀。
```

---

# 18. Narrative 必须受到结构化约束

Agent 输入只允许：

```text
Findings
Metrics
Evidence
Confidence
```

不能给它：

```text
自由浏览所有原始数据
```

否则容易：

```text
遗漏
幻觉
数字不一致
```

---

# 19. Narrative Generation Pipeline

```text
Structured Finding
↓
Template Skeleton
↓
Metric Binding
↓
Evidence Binding
↓
LLM Language Rendering
↓
Fact Consistency Check
↓
Final Narrative
```

---

# 20. Fact Consistency Check

生成文字后重新解析：

```text
金额
地址
时间
实体
数量
```

与 Finding 比较。

如果不一致：

```text
Reject
Regenerate
```

---

# 21. 数字禁止由 LLM 自己算

例如：

```text
累计流入
净获利
沉淀金额
```

必须由：

```text
Fund Flow Engine
DuckDB
```

提前计算。

LLM 只能使用结果。

---

# 22. Report Confidence

报告每个 Section 都带：

```text
Confidence
```

例如：

```text
获利归因：高
实体归属：高
沉淀判断：中
交易所落点：已确认
```

---

# 23. 数据完整性必须进入报告

报告顶部或附录必须明确：

```text
Dataset Certification
Coverage
Remaining Gaps
Provider Sources
Validation Status
```

例如：

```text
Token Transfers:
Coverage 100%
CERTIFIED

Internal Transactions:
Coverage 99.98%
PARTIAL
缺失 2 个区间
```

---

# 24. 不允许隐藏 PARTIAL

如果：

```text
数据不完整
```

报告不能写：

```text
已确认所有资金路径
```

必须：

```text
在当前可用数据范围内...
```

---

# 25. Report Certification

建议：

```go
type ReportCertification struct {
    OverallStatus string

    DatasetStatuses []DatasetCertification

    CoverageScore float64

    HasKnownGaps bool

    KnownGapCount int
}
```

---

# 26. Report Status

统一：

```text
DRAFT
GENERATING
READY
PARTIAL
REVIEWED
LOCKED
SUPERSEDED
```

---

# 27. Investigation Snapshot

报告生成时，保存：

```text
Snapshot
```

表示：

```text
报告是基于哪一版数据和分析结果生成的
```

---

# 28. Snapshot 内容

```text
Dataset IDs
Dataset Manifest Hash
Coverage
Entity Resolver Version
Fund Flow Version
Path Scoring Version
Profit Attribution Version
Report Template Version
```

---

# 29. 为什么 Snapshot 很重要

如果一周后：

```text
数据更新
实体标签变化
新增交易
```

旧报告仍然应该能解释：

```text
当时为什么得出这个结论
```

因此不能只保存“最新状态”。

---

# 30. Reproducible Report

任何已锁定报告都应该可以：

```text
从 Snapshot 重建
```

要求：

```text
同样输入
+
同样版本
→
同样核心 Findings
```

---

# 31. Report Versioning

每次重生成：

```text
v1
v2
v3
```

保留差异。

---

# 32. Report Diff

可以显示：

```text
新增 2 个交易所落点
净获利从 1.2M 修正为 1.35M
新增 1 个沉淀地址
1 个 Entity Label 由中可信升级为高可信
```

---

# 33. Manual Review

报告生成后允许：

```text
人工审阅
```

但人工修改必须区分：

```text
System Generated
User Edited
```

---

# 34. Manual Note

用户可以加入：

```text
案件备注
判断
调证反馈
外部信息
```

必须标记：

```text
USER_NOTE
```

不能伪装成系统自动结论。

---

# 35. Case Package

最终不仅导出一个 PDF / Word。

建议生成：

```text
Case Package
```

目录：

```text
case-package/
├── report/
├── evidence/
├── graphs/
├── tables/
├── manifests/
├── certificates/
└── index.json
```

---

# 36. Export Formats

建议支持：

```text
PDF
DOCX
XLSX
CSV
JSON
ZIP Case Package
```

---

# 37. PDF

用于：

```text
阅读
汇报
归档
```

---

# 38. DOCX

用于：

```text
人工编辑
正式文书二次加工
```

---

# 39. XLSX

用于：

```text
关键地址
关键路径
获利
沉淀
交易所落点
证据清单
```

---

# 40. JSON

用于：

```text
系统间交换
```

---

# 41. ZIP Case Package

用于完整留档：

```text
报告
证据
图
表
Manifest
Hash
```

---

# 42. Evidence Table

报告附录建议：

| Evidence ID | Type | Address | Tx Hash | Time | Dataset | Certification |
|---|---|---|---|---|---|---|

---

# 43. Key Address Table

字段：

```text
Address
Entity
Role
Inflow
Outflow
Net
Profit
Settlement
Confidence
```

---

# 44. Key Path Table

字段：

```text
Path ID
From
Via
To
Amount
Token
Hop Count
Path Score
Terminal Type
Confidence
```

---

# 45. Cashout Table

字段：

```text
Exchange / Service
Deposit Address
Amount
Token
Timestamp
Tx Hash
Path ID
Confidence
```

---

# 46. Settlement Table

字段：

```text
Address / Entity
Retained Value
Holding Duration
Last Outflow
Settlement Score
Evidence
```

---

# 47. Graph Export

每个关键路径可以输出：

```text
PNG / SVG
```

报告里使用：

```text
关键路径小图
```

而不是整张巨大关系图截图。

---

# 48. Graph Snapshot

Graph Snapshot 要记录：

```text
filter
token
time range
depth
selected path
aggregation version
```

---

# 49. Evidence Link

在报告前端查看时：

```text
点击 Tx Hash
→ 打开证据 Drawer
```

点击地址：

```text
→ 地址画像
```

点击路径：

```text
→ 关系图高亮
```

---

# 50. 前端：报告中心

新增：

```text
智能调查
└── 调查报告
```

或者 Investigation 内：

```text
[概览]
[资金路径]
[实体]
[时间线]
[证据]
[报告]
```

---

# 51. Report Builder UI

用户可以选择：

```text
报告类型
```

例如：

```text
资金追踪报告
获利分析报告
沉淀地址报告
交易所落点报告
综合调查报告
```

---

# 52. Report Template

模板只决定：

```text
章节顺序
展示字段
语言风格
```

不改变底层 Findings。

---

# 53. 自动报告建议

默认：

```text
综合调查报告
```

系统自动包含：

```text
案件摘要
关键地址
关键路径
获利
沉淀
交易所落点
证据
完整性
```

---

# 54. Evidence Timeline UI

前端：

```text
纵向时间线
```

支持：

```text
按金额
按事件类型
按实体
按 Token
```

筛选。

---

# 55. 时间线与关系图联动

点击时间线事件：

```text
关系图跳到对应节点 / 边
```

---

# 56. 报告与关系图联动

点击：

```text
Path #3
```

自动：

```text
Graph Focus = Path #3
```

---

# 57. 报告与智能下载联动

报告发现：

```text
Internal Transactions 数据 PARTIAL
```

可以：

```text
[补全数据]
```

调用 Smart Download Gap Repair。

完成后：

```text
Report → OUTDATED
```

提示重新生成。

---

# 58. Report Staleness

如果底层：

```text
Dataset
Entity
Fund Flow
```

发生变化，报告标记：

```text
OUTDATED
```

不自动覆盖旧版本。

---

# 59. 审计事件

记录：

```text
report.created
report.generated
report.reviewed
report.exported
report.locked
report.superseded
```

---

# 60. Report Lock

重要报告可以：

```text
LOCK
```

锁定后：

```text
内容不可变
```

新的更新生成：

```text
新版本
```

---

# 61. 文件系统结构

```text
investigations/
└── {investigation_id}/
    └── reports/
        ├── report_v1/
        │   ├── report.json
        │   ├── snapshot.json
        │   ├── findings.json
        │   ├── timeline.json
        │   ├── evidence-index.json
        │   ├── graphs/
        │   └── exports/
        └── report_v2/
```

---

# 62. Report Engine 模块

```text
internal/reportengine/
├── model/
├── findings/
├── timeline/
├── narrative/
├── evidence/
├── snapshot/
├── template/
├── renderer/
├── export/
├── diff/
└── api/
```

---

# 63. Finding Builder

不同系统结果转成标准 Finding：

```text
Entity Intelligence
→ EntityFindingBuilder

Fund Flow
→ PathFindingBuilder

Profit Attribution
→ ProfitFindingBuilder

Settlement
→ SettlementFindingBuilder
```

---

# 64. Narrative Renderer

建议：

```text
Rule-based skeleton
+
LLM language polish
```

而不是纯 LLM。

---

# 65. Report API

创建：

```http
POST /api/investigations/{id}/reports
```

获取：

```http
GET /api/investigations/{id}/reports/{report_id}
```

重新生成：

```http
POST /api/investigations/{id}/reports/{report_id}/regenerate
```

---

# 66. Export API

```http
POST /api/investigations/{id}/reports/{report_id}/export
```

参数：

```text
pdf
docx
xlsx
json
case_package
```

---

# 67. Report Diff API

```http
GET /api/investigations/{id}/reports/{a}/diff/{b}
```

---

# 68. Evidence API

```http
GET /api/investigations/{id}/evidence/{evidence_id}
```

---

# 69. P0 实施范围

必须完成：

```text
Structured Findings
Evidence Citation Layer
Evidence Timeline
Report Snapshot
综合调查报告模板
Narrative Renderer
PDF / DOCX / XLSX 导出
Report Versioning
```

---

# 70. P1

随后：

```text
Report Diff
Report Staleness
Case Package ZIP
Interactive Evidence Links
Graph Snapshot Export
Manual Review
Report Lock
```

---

# 71. P2

高级：

```text
多语言报告
不同机构模板
自动摘要层级
章节自定义
电子签名 / Hash 归档
```

---

# 72. Case A：完整报告

调查已完成：

```text
Entity
Path
Profit
Settlement
Cashout
```

要求：

```text
一键生成综合报告
```

每一项结论都可以点击回证据。

---

# 73. Case B：PARTIAL 数据

Internal Tx Coverage：

```text
99.9%
```

报告必须明确：

```text
存在数据缺口
```

不得输出：

```text
完整确认
```

---

# 74. Case C：报告更新

v1 生成后新增数据。

要求：

```text
v1 保留
报告标记 OUTDATED
生成 v2
可查看 diff
```

---

# 75. Case D：数字一致性

Finding：

```text
Net Profit = 1,234,567.89
```

Narrative 中必须完全一致。

不允许 LLM 写成：

```text
约 1.4M
```

除非模板允许明确的格式化规则。

---

# 76. Case E：证据追溯

用户点击：

```text
交易所落点
```

必须能定位：

```text
Tx Hash
Address
Dataset
Entity Evidence
Path
```

---

# 77. Case F：Snapshot 重现

报告锁定后：

```text
系统数据更新
```

仍能从 Snapshot 重现原核心 Findings。

---

# 78. 核心指标

至少监控：

```text
Report Generation Time
Evidence Coverage
Finding Citation Coverage
Narrative Consistency Pass Rate
Report Rebuild Success Rate
Export Success Rate
Stale Report Count
```

---

# 79. 强制质量指标

必须：

```text
100% Findings 带 Evidence
100% 金额来自结构化计算
100% Entity 结论带 Confidence
100% PARTIAL Dataset 有披露
100% Locked Report 可重现
```

---

# 80. 与整个系统的关系

最终结构：

```text
Smart Download
→ 提供可信数据

Dataset Registry
→ 提供数据资产

Investigation Cache
→ 提供调查上下文

Entity Intelligence
→ 解释地址 / 实体

Fund Flow Intelligence
→ 解释资金路径 / 获利 / 沉淀

Report Engine
→ 生成最终调查成果
```

---

# 81. 下一阶段之后

Report Engine 完成后，下一步最值得做的是：

```text
Case Workflow & Collaboration V1
+
Review / Approval
+
Investigation Task Management
+
Evidence Chain of Custody
```

也就是把系统从：

```text
单人分析工具
```

升级成：

```text
多人协作调查平台
```

支持：

```text
调查分工
审核
版本
证据交接
审批
任务状态
```

---

# 82. 一句话目标

> **下一阶段要让系统不再停留在“分析结果很多”，而是把所有结果组织成一份可复核、可引用、可重复生成、可正式导出的调查报告，并确保每一个重要结论都能回到具体链上证据。**
