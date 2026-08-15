# Explorer UI Rebuild V1.1
## 未达预期整改方案

> 结论：当前页面更像“后台 BI / 数据运维面板”，还不像成熟的链上 Explorer。
> 本阶段不动 ClickHouse、Writer、Graph、Investigation、Export 数据面，只重构 Explorer 的信息架构、地址详情页和交易列表。

# 1. 当前截图暴露的主要问题

1. 顶部有两套搜索框，主入口重复。
2. 左侧把 Parquet、浏览器下载、Dune、数据源等基础设施暴露得太多，产品继续像 ETL 后台。
3. Address Header 太空，`Unlabeled Address / UNKNOWN` 占据主视觉，但真正重要的余额、地址类型、首次/最后活动、Coverage、资金操作不够突出。
4. 六个统计块横向平铺，视觉像 BI Dashboard，不像 Explorer。
5. Overview 下方 8 个 Card 大量空白，数据为空时尤其显得“功能很多但没有内容”。
6. `Token Transfers` 与中文 Tab 混排，语言体系不统一。
7. `资金流向 / 调查 / 导出` 被做成 Tab，但它们本质上应当是主操作 Action。
8. 页面把内部实现细节直接暴露给用户，例如：
   - `0 price coverage`
   - `stored_historical_usd_only`
   - `missing_price_is_null`
   - `Logical unique`
   - `BUILD 20260808-1900`
9. `0001-01-01` 直接暴露 Go zero time，是必须修复的业务展示 Bug。
10. 当前地址是 Zero Address，却显示为 `Unlabeled Address / UNKNOWN`，说明 System Address 语义还没有进入 Explorer 展示。
11. NO_DATA 情况下仍显示 CEX/DEX/Bridge = 0，容易让用户把“未知”理解成“已确认没有”。
12. 页面缺少最重要的 Explorer 元素之一：Recent Activity / Recent Transactions。

# 2. 重新定义 Address Page

目标首屏：

```text
┌──────────────────────────────────────────────────────────────┐
│ [Icon] 0xd3ae39...ac735     EOA     Binance Deposit          │
│        [Copy] [QR]                                            │
│                                                              │
│ BNB 12.49   USDT 1.25M   Portfolio $1.31M                   │
│ First Seen 2024-03-11   Last Seen 2026-08-08                │
│ Coverage COMPLETE                         [资金流] [调查] [导出] │
└──────────────────────────────────────────────────────────────┘

[资产] [流入] [流出] [净流量]

概览 | 交易 | 代币转账 | 内部交易 | 资产 | 合约 | 分析 ▼

资金概览                 活动趋势

主要资金来源             主要资金去向

Recent Activity
```

# 3. 搜索框只保留一套

顶部统一：

```text
[Chain Selector] [Search Address / Tx / Block / Token / Entity]
```

Address 页面内部不再放第二个大搜索框。

`Explorer 首页` 按钮改为 Breadcrumb：

```text
Explorer / Address
```

# 4. 左侧导航重构

普通业务模式：

```text
Explorer
资金流向
智能调查
数据资产
案件

系统
├── 下载中心
├── 数据源
├── 数据质量
└── 设置
```

以下不要继续一级展示：

```text
Parquet 数据
浏览器下载
Dune 下载
智能下载实现细节
```

这些统一进入 `下载中心 > Advanced`。

# 5. Address Header 重构

未知地址不要把：

```text
Unlabeled Address
UNKNOWN
```

作为 H1。

正确：

```text
0x0000...0000
SYSTEM
Zero Address
```

普通未知 EOA：

```text
0xabc...123
EOA
No verified label
```

有实体：

```text
Binance Deposit
0xabc...123
EOA · HIGH confidence
```

# 6. Zero Address 必须内置识别

至少内置：

```text
0x0000000000000000000000000000000000000000
→ Zero Address / SYSTEM

0x000000000000000000000000000000000000dEaD
→ Dead Address / SYSTEM
```

不能继续显示成 UNKNOWN。

# 7. 0 与 Unknown 必须严格区分

统一规则：

```text
0
= 已有完整数据，结果确实为 0

--
= 未知 / 未覆盖 / 无价格 / 数据不足
```

所以 NO_DATA 时：

```text
CEX 交互 --
DEX 交互 --
Bridge 交互 --
```

而不是：

```text
0
```

# 8. 修复所有零时间

后端/DTO/BFF 统一处理：

```text
time.IsZero()
year <= 1970
invalid timestamp
```

业务 API 返回：

```text
null
```

前端显示：

```text
--
```

禁止任何：

```text
0001-01-01
1970-01-01
```

出现在 Explorer。

# 9. 去除内部技术文案

从普通 Explorer 删除：

```text
stored_historical_usd_only
missing_price_is_null
Logical unique
BUILD ...
```

这些移动到：

```text
Data Quality
Developer Detail
System Status
```

用户页面只显示：

```text
Price Coverage 98.2%
Coverage Complete
```

# 10. 顶部统计缩减

当前 6 个横向块改为 4 个主指标：

```text
Estimated Portfolio
Total In
Total Out
Net Flow
```

以下移入 Header 或 Overview：

```text
Counterparties
Active Days
First Seen
Last Seen
```

# 11. Overview 不再放 8 个空 Card

保留四个主要区域：

```text
Financial Overview
Activity Trend
Top Sources
Top Destinations
```

然后必须增加：

```text
Recent Activity
```

显示最近 10~20 条交易 / Token Transfer。

如果整个地址 NO_DATA：

不要渲染八个空模块。

直接：

```text
暂无本地数据
正在准备数据 / 开始补齐
```

# 12. Tabs 重新整理

改为：

```text
概览
交易
代币转账
内部交易
资产
合约
分析 ▼
```

`分析 ▼`：

```text
资金分析
交易对手
关联钱包
Retention
Pass-through
PnL
```

右侧固定主操作：

```text
[资金流向] [调查] [导出]
```

不要继续把这三个做成 Tab。

# 13. 语言统一

不要：

```text
概览
交易
Token Transfers
内部交易
```

统一中文：

```text
概览
交易
代币转账
内部交易
资产
合约
```

或统一英文，但不能混排。

# 14. 页面从 Card-centric 改成 Explorer-centric

当前：

```text
大量 Card
大量浅灰边框
大量空白
```

整改：

```text
减少 40%~60% Card
增加表格
增加 Inline Metadata
增加紧凑统计
增加地址身份
```

链上 Explorer 的视觉核心必须是：

```text
Identity
Transactions
Token Transfers
Counterparty
Fund Flow
```

# 15. Transaction Table 作为核心组件

列：

```text
Tx Hash
Method
Block
Time
From
Direction
To
Value
Historical USD
Fee
Status
```

建议：

```text
Row Height 44~48px
金额右对齐
地址两行显示 Entity + Short Address
时间默认 Absolute
Direction 用文字 + 箭头
```

# 16. Token Transfer Table

列：

```text
Tx Hash
Method
Block
Time
From
Direction
To
Amount
Token
Historical USD
```

Token：

```text
[Logo] USDT
```

未知 Token：

```text
[Identicon] SYMBOL
```

不能仅按 symbol 匹配 Logo。

# 17. Quick Filter

表格顶部：

```text
[全部] [转入] [转出] [大额] [CEX] [USDT] [高级筛选]
```

筛选后展示：

```text
[OUT ×]
[USDT ×]
[>= $100K ×]
[30D ×]
```

# 18. Address 主操作优先级

地址页右上：

```text
Primary: 资金流向
Secondary: 调查
Tertiary: 导出
```

`保存视图 / 分享 / 添加标签` 收进更多菜单：

```text
⋯
```

# 19. Data Coverage 降低视觉噪音

当前 `无覆盖证明` 改成：

```text
Coverage
COMPLETE
```

点击后展开详细区间。

普通首屏不要展示底层 Coverage 证明文本。

# 20. 数据质量移出主视觉

当前 `数据边界` Card 不应该占据 Overview 主区域。

Address 页面最多显示：

```text
Coverage: Complete
Price Coverage: 98%
```

详细信息通过：

```text
查看数据质量
```

进入后台。

# 21. Sidebar 与内容宽度

建议：

```text
Sidebar 208px
可折叠到 64px

Main max-width:
1600~1800px 或 none
```

1920 屏必须充分利用宽度，尤其交易表。

# 22. 1080p 首屏验收

1920×1080 不滚动必须至少看到：

```text
Address Header
4 个核心金额指标
Tabs
Overview 两个主模块
Recent Activity 前 5~8 行
```

如果首屏仍然只有：

```text
Header
Stats
Tabs
空 Card
```

则不通过。

# 23. P0 整改清单

必须先完成：

```text
删除第二搜索框
重构 Sidebar
重构 Address Header
Zero Address 识别
修复 0001-01-01
删除 Debug 技术文案
0 / Unknown 分离
统计块从 6 缩到 4
Tabs 重构
Fund Flow / Investigation / Export 改为 Action
Overview 加 Recent Activity
NO_DATA 不渲染一堆空 Card
```

# 24. P1

```text
Transaction Table 重构
Token Transfer Table 重构
Advanced Filter
Filter Chips
Column Manager
URL State
Coverage UX
```

# 25. P2

```text
Financial Overview
Counterparty
Entity Analytics
Retention
Pass-through
Related Wallets
PnL
```

# 26. 最终判断

当前页面不是“稍微润色一下”就能达到目标。

需要的是：

```text
UI Information Architecture Rebuild
```

而不是：

```text
UI Polish
```

保留现有后端和功能能力，直接重做：

```text
Address Page Shell
Navigation
Overview
Transaction Table
Token Transfer Table
Primary Actions
```

不要继续在当前页面上加 Card、加 Tab、加统计，否则只会越来越像后台管理系统。
