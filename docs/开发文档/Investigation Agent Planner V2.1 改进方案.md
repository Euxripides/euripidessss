# Investigation Agent Planner V2.1 改进方案

## 目标

基于 V2 验收结果，升级为企业级链上调查 Agent。

V2 已完成：

-   Investigation Request
-   Intent Analyzer
-   Planner
-   Task Scheduler
-   执行器体系
-   动态任务追加
-   六维评分
-   调查报告 V2

V2.1 重点：

1.  Evidence Layer 证据体系
2.  Profit Detection 可信度
3.  Investigation Budget
4.  Stop Strategy
5.  Score Profile
6.  Investigation Memory

------------------------------------------------------------------------

# 1. Evidence Layer

当前：

    Task
     ↓
    Result
     ↓
    Score
     ↓
    Report

升级：

    Task
     ↓
    Finding
     ↓
    Evidence Extractor
     ↓
    Evidence Store
     ↓
    Report

新增 investigation_evidence：

    id
    investigation_id
    task_id
    address
    tx_hash
    block_number
    token
    amount
    evidence_type
    confidence
    created_at

目标：

让每个 AI 结论都有：

-   交易证据
-   地址证据
-   时间证据
-   可信度

------------------------------------------------------------------------

# 2. Profit Detection V2

当前限制：

无价格 Oracle 时，利润只能估算。

升级：

输出：

    Profit Estimate

    +

    Confidence

示例：

    估算利润:
    500000 USDT

    可信度:
    62%

    依据:
    ✓ Token流入
    ✓ Token流出
    ✓ 时间窗口匹配
    ? 缺少历史价格

------------------------------------------------------------------------

# 3. Investigation Budget

防止动态任务无限扩张。

新增：

``` json
{
 "max_tasks":50,
 "max_depth":8,
 "max_addresses":500,
 "max_runtime":1800
}
```

控制：

-   最大任务数量
-   图谱深度
-   地址数量
-   运行时间

------------------------------------------------------------------------

# 4. Stop Strategy

新增：

    TARGET_FOUND
    NO_VALUE
    LOW_CONFIDENCE
    BUDGET_LIMIT
    USER_CANCEL
    ERROR

例如：

发现交易所充值：

    STOP_REASON=TARGET_FOUND

------------------------------------------------------------------------

# 5. Score Profile

六维评分：

    Fund
    Behavior
    Risk
    Entity
    Graph
    Identity

根据调查模式动态调整权重。

资金追踪：

    Fund 40%
    Graph 30%
    Entity 20%
    Risk 10%

风险调查：

    Risk 40%
    Graph 30%
    Entity 20%
    Fund 10%

身份调查：

    Identity 40%
    Entity 30%
    Graph 20%
    Risk 10%

------------------------------------------------------------------------

# 6. Prompt Security

用户输入：

-   objective
-   expected_result

必须隔离。

结构：

    SYSTEM
    调查规则

    ↓

    CONTEXT
    链上事实

    ↓

    USER OBJECTIVE
    用户目标

    ↓

    CONSTRAINT
    不可覆盖系统规则

------------------------------------------------------------------------

# 7. Crash Recovery

增加测试：

    创建 Request

    ↓

    保存 Plan

    ↓

    服务停止

    ↓

    恢复

    ↓

    继续执行

验证：

    Request
    Plan
    Task
    Status

一致。

------------------------------------------------------------------------

# 8. Evidence Viewer

新增组件：

    components/Investigation/EvidenceViewer.tsx

展示：

    发现:

    500万 USDT资金流


    交易:

    0x123


    路径:

    A
     ↓
    B
     ↓
    C


    可信度:

    89%

------------------------------------------------------------------------

# 9. Investigation Memory

增加历史案件记忆。

支持：

-   地址关联
-   实体关联
-   案件关联
-   历史调查复用

示例：

历史：

    地址A
    关联实体X

新案件：

    地址B
    出现相同资金路径

提示：

    可能存在关联

------------------------------------------------------------------------

# 10. Knowledge Graph Memory

新增关系：

    Address

    Entity

    Case

    Transaction

支持：

-   地址属于实体
-   地址资金关联
-   地址共同控制
-   历史出现

------------------------------------------------------------------------

# 11. API升级

新增：

Evidence：

    GET /api/investigation/{id}/evidence

Memory：

    GET /api/investigation/memory/search

Budget：

    GET /api/investigation/{id}/budget

------------------------------------------------------------------------

# 12. 数据表

新增：

## investigation_evidence

保存证据链。

## investigation_memory

保存历史调查。

## score_profile

保存不同调查模式评分权重。

------------------------------------------------------------------------

# 13. 实施阶段

## Phase 1

Evidence Layer

## Phase 2

Profit Detection V2

## Phase 3

Budget + Stop Strategy

## Phase 4

Memory Layer

------------------------------------------------------------------------

# 最终目标

从：

    AI自动分析地址

升级：

    AI辅助链上调查系统

具备：

-   调查目标理解
-   自动计划
-   证据链
-   可信度评分
-   动态调查
-   历史知识复用
