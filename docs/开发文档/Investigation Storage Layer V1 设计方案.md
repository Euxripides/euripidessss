# Investigation Storage Layer V1 设计方案

## 1. 背景

Investigation Agent Planner V2.1 新增：

-   investigation_evidence
-   investigation_memory
-   score_profile

这些数据属于调查系统状态，而不是链上分析数据。

当前平台：

    SQD
     |
    Parquet
     |
    DuckDB
     |
    Analysis Engine

DuckDB 负责：

-   链上数据查询
-   聚合分析
-   地址行为分析

Storage Layer 负责：

-   调查请求
-   调查计划
-   任务状态
-   证据链
-   AI记忆
-   配置

因此采用：

    JSON File Storage V1

后续可平滑迁移 DuckDB / PostgreSQL。

------------------------------------------------------------------------

# 2. 设计目标

要求：

1.  不引入数据库依赖
2.  与 RequestStore 保持一致
3.  原子写
4.  支持崩溃恢复
5.  支持未来迁移
6.  数据结构稳定

------------------------------------------------------------------------

# 3. 总体目录

推荐：

    data/

    └── investigation/

        ├── requests/

        ├── plans/

        ├── tasks/

        ├── evidence/

        ├── memory/

        ├── score-profile/

        └── indexes/

------------------------------------------------------------------------

# 4. Storage Interface

禁止业务代码直接操作文件。

统一接口：

``` go
type Store interface {

 Save()

 Get()

 List()

 Delete()

 Exists()

}
```

------------------------------------------------------------------------

# 5. Request Storage

目录：

    requests/

    inv-001.json

示例：

``` json
{
"id":"inv-001",

"address":"0xabc",

"chain":"bsc",

"objective":"寻找资金沉淀",

"mode":"fund_trace",

"status":"RUNNING"
}
```

------------------------------------------------------------------------

# 6. Plan Storage

目录：

    plans/

    plan-001.json

保存：

-   Agent规划结果
-   Task列表
-   优先级

示例：

``` json
{
"id":"plan-001",

"request_id":"inv-001",

"tasks":[

 {
  "type":"FORWARD_TRACE",
  "priority":1
 }

]
}
```

------------------------------------------------------------------------

# 7. Task Storage

目录：

    tasks/

    task-001.json

保存：

-   执行状态
-   输入
-   输出
-   错误

示例：

``` json
{
"id":"task-001",

"type":"FLOW_GRAPH",

"status":"COMPLETED",

"result_ref":"result-001"
}
```

------------------------------------------------------------------------

# 8. Evidence Storage

目录：

    evidence/

    inv-001/

        evidence-001.json

        evidence-002.json

原因：

避免单文件无限增长。

Evidence结构：

``` json
{
"id":"evidence-001",

"type":"profit_behavior",

"address":"0xabc",

"tx_hash":"0x123",

"amount":"500000",

"token":"USDT",

"confidence":0.89
}
```

------------------------------------------------------------------------

# 9. Memory Storage

目录：

    memory/

    address/

    0xabc.json

    entity/

    entity-001.json

    case/

    case-001.json

用途：

保存：

-   地址历史
-   实体关系
-   案件关联

示例：

``` json
{
"address":"0xabc",

"labels":[
 "profit_address"
],

"cases":[
 "inv-001"
],

"relations":[

 {
  "address":"0xdef",
  "type":"fund_transfer"
 }

]
}
```

------------------------------------------------------------------------

# 10. Score Profile Storage

目录：

    score-profile/

    profiles.json

保存：

不同调查模式评分权重。

示例：

``` json
{
"fund_trace":{

"Fund":0.4,

"Graph":0.3,

"Entity":0.2,

"Risk":0.1

}
}
```

------------------------------------------------------------------------

# 11. 原子写机制

所有写入：

禁止：

    直接覆盖文件

采用：

    write temp

    ↓

    fsync

    ↓

    rename

保证：

-   进程崩溃不损坏
-   状态可恢复

------------------------------------------------------------------------

# 12. Index设计

增加：

    indexes/

    evidence-index.json

    memory-index.json

用途：

快速定位。

例如：

``` json
{
"0xabc":[

"evidence-001",

"evidence-002"

]
}
```

------------------------------------------------------------------------

# 13. 生命周期管理

## Active

运行中的调查：

    requests/

    tasks/

## History

完成调查：

    archive/

建议：

    active <=5

    history <=200

保持内存和查询稳定。

------------------------------------------------------------------------

# 14. 并发控制

要求：

-   单文件锁
-   避免锁嵌套
-   避免顺序反转

推荐：

    Request Lock

    Task Lock

    Evidence Lock

------------------------------------------------------------------------

# 15. 数据校验

读取JSON后：

必须验证：

-   ID
-   Request关联
-   Plan关联
-   Task状态
-   Schema版本

增加：

    schema_version

例如：

``` json
{
"schema_version":1
}
```

------------------------------------------------------------------------

# 16. 未来迁移方案

未来规模：

    Evidence > 1000万

    Memory > 百万地址

    跨案件分析需求

迁移：

    JSON Storage

          ↓

    DuckDB Investigation Warehouse

          ↓

    PostgreSQL / Distributed DB

------------------------------------------------------------------------

# 17. DuckDB迁移设计

保持接口不变：

    EvidenceStore

    JSONEvidenceStore

    DuckDBEvidenceStore

业务层无需修改。

------------------------------------------------------------------------

# 18. 与现有系统关系

最终架构：

                     Investigation Agent


                             |

                     Storage Layer


            +----------------+----------------+

            |                                 |

     JSON Investigation              DuckDB Analytics


            |                                 |

     Request/Plan/Task              Blockchain Data


            |

     Evidence/Memory

------------------------------------------------------------------------

# 19. 实施阶段

## Phase 1

完成：

-   Store Interface
-   JSON Storage
-   Atomic Write

## Phase 2

完成：

-   Evidence Store
-   Memory Store

## Phase 3

完成：

-   Index
-   Recovery

## Phase 4

完成：

-   DuckDB Adapter

------------------------------------------------------------------------

# 最终目标

建立一个：

稳定、可迁移、可扩展的 Investigation Storage Layer。

支持：

-   AI调查计划
-   证据链
-   历史记忆
-   案件关联
-   后续数据仓库升级
