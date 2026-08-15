# Data Source Manager 数据源管理中心 V1.0

## Codex 前后端接入开发方案

项目： E:`\codex`{=tex}`\bsc`{=tex}\_analytics

目标： 将 SQD、AWS Parquet、NodeReal、Chainstack、Ankr
等数据源统一接入现有虚拟币分析平台前端。

------------------------------------------------------------------------

# 一、当前系统状态

当前已经完成：

-   SQD finalized-stream
-   BSC / Ethereum / Base / Arbitrum
-   Transactions
-   Logs
-   Token Transfer
-   NFT Transfer
-   Trace
-   Internal Transaction
-   Address Activity V2
-   Address Summary
-   Parquet 下载和分析流程

当前架构：

    数据源

    SQD
    AWS
    RPC

        ↓

    标准化数据层

        ↓

    Parquet

        ↓

    DuckDB

        ↓

    链上地址分析

------------------------------------------------------------------------

# 二、建设目标

新增：

## 数据源管理中心

入口：

    虚拟币

     ├── 链上地址分析

     ├── Parquet下载

     └── 数据源管理

统一管理：

-   SQD
-   AWS Dataset
-   NodeReal
-   Chainstack
-   Ankr
-   Custom Provider

------------------------------------------------------------------------

# 三、总体架构

                     Data Source Manager


            ┌──────────┬──────────┬──────────┐

            SQD        AWS        RPC

            │          │          │

            └──────────┴──────────┘

                      ↓

              Provider Interface

                      ↓

              Health Monitor

                      ↓

              Metrics

                      ↓

              Frontend Dashboard

------------------------------------------------------------------------

# 四、前端目录设计

新增：

    frontend/src/features/crypto/datasource/

结构：

    datasource/

    ├── DataSourcePage.tsx

    ├── components/

    │   ├── SourceCard.tsx
    │   ├── SourceStatus.tsx
    │   ├── SourceConfigDialog.tsx
    │   ├── HealthChart.tsx
    │   ├── MetricsCard.tsx
    │   └── TestConnectionModal.tsx

    ├── api/

    │   └── datasource-api.ts

    ├── types/

    │   └── datasource.ts

    └── datasource.css

------------------------------------------------------------------------

# 五、前端页面设计

## 1. 数据源概览

顶部显示：

    数据源数量

    健康节点

    异常节点

    今日请求

    缓存命中率

示例：

    6 个数据源

    5 个健康

    1 个异常

    18231 次请求

    95%缓存

------------------------------------------------------------------------

# 六、数据源卡片

## SQD

展示：

    SQD Finalized Stream

    状态：
    ● Healthy

    链：

    BSC
    ETH
    Base
    Arbitrum

    延迟：

    120ms


    [配置]

    [测试]

    [日志]

------------------------------------------------------------------------

## RPC

展示：

    NodeReal

    BSC

    主节点


    状态：

    ● Healthy


    延迟：

    80ms


    成功率：

    99.8%


    [编辑]

    [测试]

------------------------------------------------------------------------

## AWS

展示：

    AWS Public Dataset

    状态：

    Healthy


    Bucket:

    xxx


    下载速度:

    120MB/s


    [配置]

------------------------------------------------------------------------

# 七、配置弹窗

## SQD配置

字段：

    名称

    Endpoint

    API Key

    Timeout

    最大并发

    Retry次数

    启用

------------------------------------------------------------------------

## RPC配置

字段：

    Provider

    Chain

    Endpoint

    API Key

    Priority

    最大RPS

    最大并发

    Timeout

    启用

支持：

    NodeReal

    Chainstack

    Ankr

    Custom

------------------------------------------------------------------------

## AWS配置

字段：

    Bucket

    Region

    Prefix

    下载线程

    缓存目录

    启用

------------------------------------------------------------------------

# 八、安全要求

## API Key处理

禁止：

    localStorage保存

    sessionStorage保存

    前端直接调用RPC

    console输出

------------------------------------------------------------------------

正确流程：

    前端输入

    ↓

    HTTPS提交

    ↓

    后端加密

    ↓

    数据库保存密文

    ↓

    前端只显示脱敏信息

------------------------------------------------------------------------

显示：

    https://rpc.xxx.com/****

    API Key:

    xxxx****91ab

------------------------------------------------------------------------

# 九、后端目录设计

新增：

    internal/datasource/

    ├── manager.go

    ├── provider.go

    ├── health.go

    ├── metrics.go

    ├── sqd/

    ├── aws/

    └── rpc/

        ├── nodereal.go

        ├── chainstack.go

        └── ankr.go

------------------------------------------------------------------------

# 十、统一 Provider 接口

Go:

``` go
type DataSource interface {

 Name() string

 Type() string

 Test(ctx context.Context) error

 Health(ctx context.Context) HealthStatus

 Metrics(ctx context.Context) Metrics

}
```

------------------------------------------------------------------------

# 十一、API设计

## 获取数据源列表

GET

    /api/crypto/datasource/list

返回：

``` json
[
 {
  "name":"SQD",
  "type":"stream",
  "status":"healthy",
  "latency":120
 }
]
```

------------------------------------------------------------------------

## 测试连接

POST

    /api/crypto/datasource/test

返回：

    success

    latency

    chain_id

    latest_block

------------------------------------------------------------------------

## 保存配置

POST

    /api/crypto/datasource/save

------------------------------------------------------------------------

## 健康状态

GET

    /api/crypto/datasource/health

------------------------------------------------------------------------

## 指标

GET

    /api/crypto/datasource/metrics

------------------------------------------------------------------------

# 十二、健康检测

所有数据源支持：

-   连通性检测
-   延迟检测
-   错误率统计
-   最近成功时间
-   最近失败原因

状态：

    HEALTHY

    DEGRADED

    RATE_LIMITED

    UNAVAILABLE

    DISABLED

------------------------------------------------------------------------

# 十三、RPC特殊要求

支持：

NodeReal

Chainstack

Ankr

功能：

-   主备切换
-   限速
-   Retry
-   熔断
-   健康评分

规则：

RPC只负责：

-   Metadata
-   Balance
-   Receipt补漏
-   地址类型

禁止：

-   历史交易下载
-   全量Logs扫描
-   全量Trace扫描

历史数据继续：

SQD / Parquet

------------------------------------------------------------------------

# 十四、数据源指标

统一展示：

## 请求

    今日请求数量

    成功数量

    失败数量

## 性能

    P50延迟

    P95延迟

    平均速度

## 错误

    429次数

    Timeout次数

    认证失败

------------------------------------------------------------------------

# 十五、前端UI要求

保持当前虚拟币页面风格：

-   白色卡片
-   深蓝标题
-   青色状态
-   圆角
-   阴影
-   响应式布局

禁止：

-   横向滚动
-   密集后台表格
-   原生alert

必须支持：

桌面

手机

平板

------------------------------------------------------------------------

# 十六、测试要求

## 后端

必须通过：

    go test ./...

    go vet ./...

    go build

------------------------------------------------------------------------

## 前端

必须通过：

    npm run build

    Playwright

测试：

-   添加数据源
-   删除数据源
-   修改配置
-   测试连接
-   移动端显示

------------------------------------------------------------------------

# 十七、开发阶段

## Phase 1

数据源列表

完成：

-   Provider模型
-   API
-   前端卡片

## Phase 2

配置管理

完成：

-   添加
-   修改
-   删除
-   测试

## Phase 3

健康中心

完成：

-   状态
-   延迟
-   错误统计

## Phase 4

RPC接入

完成：

-   NodeReal
-   Chainstack
-   Ankr

## Phase 5

SQD/AWS接入

完成：

-   Stream状态
-   下载状态
-   数据统计

------------------------------------------------------------------------

# 十八、完成标准

完成后：

系统具备：

-   统一数据源管理
-   SQD配置管理
-   AWS配置管理
-   RPC节点管理
-   健康监控
-   API Key安全保存
-   数据源故障发现
-   前端可视化管理

未来新增：

Bitquery

Allium

Goldsky

QuickNode

无需修改核心架构。
