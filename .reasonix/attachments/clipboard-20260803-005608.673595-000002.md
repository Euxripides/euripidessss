# Investigation Graph Expansion Engine V1.0 设计方案

## 目标

将 Smart Download Orchestrator、Investigation
Agent、地址关系图进行真正闭环。

目标流程：

用户调查目标 ↓ Investigation Agent ↓ Graph Expansion Engine ↓ Coverage
Resolver ↓ Smart Download Orchestrator ↓ Browser / SQD / RPC ↓ 数据分析
↓ Graph Patch ↓ 关系图增量更新 ↓ Agent继续调查

## 1. 系统职责

### Investigation Agent

负责：

-   理解调查目标
-   生成数据需求
-   判断调查方向

### Graph Expansion Engine

负责：

-   图节点扩展
-   图边扩展
-   调查上下文维护
-   增量图更新

### Smart Download Orchestrator

负责：

-   数据源选择
-   下载计划生成
-   Provider调度

### Downloader

负责执行：

-   Browser CSV/直链/爬取
-   SQD
-   RPC

## 2. 扩展请求模型

``` json
{
  "graph_session_id":"graph-001",
  "investigation_id":"inv-001",
  "center_address":"0xabc",
  "direction":"downstream",
  "depth":3,
  "objective":"寻找资金最终沉淀地址"
}
```

## 3. 扩展流程

    点击地址继续追踪

    ↓

    Graph Expansion Request

    ↓

    Coverage Resolver检查已有数据

    ↓

    Smart Download Orchestrator生成下载计划

    ↓

    自动选择SQD/RPC/Browser

    ↓

    分析数据

    ↓

    生成Graph Patch

    ↓

    关系图增加节点和边

## 4. Coverage Resolver

扩展前检查：

-   transactions
-   token transfers
-   logs
-   traces
-   balances
-   labels

避免：

-   重复下载
-   重复分析
-   浪费RPC

## 5. Graph Patch

禁止整图刷新。

采用：

``` json
{
 "add_nodes":[],
 "update_nodes":[],
 "add_edges":[],
 "update_edges":[]
}
```

要求：

-   保留viewport
-   保留节点位置
-   新节点动画加入
-   Patch幂等

## 6. Agent联动

发现：

-   大额资金
-   高风险地址
-   交易所入口
-   新路径

自动生成：

ADDRESS_DEEP_INVESTIGATION

重新进入：

Agent Planner

↓

Smart Scheduler

↓

Graph Expansion

## 7. 调查上下文

保存：

-   investigation_id
-   graph_session_id
-   expansion_history
-   download_jobs
-   assets
-   evidence
-   patches

实现：

为什么扩展这个地址？

↓

使用什么数据？

↓

哪个下载任务？

↓

哪些交易证据？

## 8. 预算控制

限制：

-   最大深度
-   最大地址数量
-   最大下载量
-   最大运行时间

超限：

BUDGET_LIMIT

## 9. 后端模块

    internal/graphexpansion/

    engine.go
    request.go
    planner.go
    coverage.go
    patch.go
    builder.go
    budget.go
    history.go

## 10. 前端模块

    GraphExpansionMenu.tsx

    ExpansionProgress.tsx

    CoverageIndicator.tsx

    GraphPatchUpdater.tsx

    InvestigationContextPanel.tsx

## 11. 测试

验证：

-   本地已有数据直接扩展
-   缺失数据自动下载
-   SQD失败自动切换
-   RPC补实时余额
-   Agent自动追加任务
-   下载恢复
-   Patch幂等
-   重复请求不产生重复节点

## 12. 完成目标

最终：

用户：

调查一个地址

↓

Agent理解目标

↓

自动获取数据

↓

自动生成关系图

↓

点击节点继续追踪

↓

自动补充数据

↓

自动更新调查报告

形成：

AI自治链上调查闭环。
