# Explorer Interaction Layer V1.2
## 高密度交易表、筛选器、详情抽屉与跨模块上下文联动

> 目标：把 Explorer 从“页面展示”升级成“真正可操作的链上调查工作台”。
> 本阶段不动 ClickHouse Data Plane，不增加新的底层数据能力，只优化前端交互效率。

# 1. 核心方向

下一步不要继续加卡片，而要把重心转到：

- Transaction / Token Transfer 高密度表格
- Quick Filter + Advanced Filter
- URL State
- Transaction / Token Transfer Drawer
- Column Manager
- Saved Views
- AnalysisContext
- Explorer ↔ Fund Flow / Investigation / Data Assets / Export 上下文继承

# 2. 为什么先做这一层

真正决定 Explorer 是否专业的不是“卡片多不多”，而是：

```text
找一笔交易要多快
筛一类资金要多快
从一笔交易继续追踪要多快
```

目标流程：

```text
输入地址
→ 筛 USDT
→ 筛 OUT
→ >= $100K
→ 只看 CEX
→ 点一条交易
→ 右侧直接看完整详情
→ 一键加入资金流
→ 一键继续调查
→ 不丢任何筛选上下文
```

# 3. Transaction Table V2

核心列：

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

可选列：

```text
From Entity
To Entity
Token Movement
Gas Used
Gas Price
Contract
Protocol
```

# 4. Token Transfer Table V2

核心列：

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
Entity
```

# 5. 高密度规范

建议：

```text
Row Height: 44–48px
Header: 40px
Font: 13–14px
```

金额右对齐，地址左对齐，时间使用 Absolute Time。

# 6. Quick Filters

表格顶部：

```text
[全部]
[转入]
[转出]
[大额]
[CEX]
[DEX]
[Bridge]
[USDT]
[高级筛选]
```

# 7. Advanced Filter Drawer

右侧 Drawer 分组：

```text
时间
地址
Token
金额
交易
实体
协议
```

支持：

```text
24H / 7D / 30D / 90D / 1Y / ALL / Custom
From / To / Counterparty
Token / Token Contract
USD Min / Max
Entity Type / Entity / Role
Method / Status
```

# 8. Filter Chips

筛选后固定展示：

```text
[OUT ×]
[USDT ×]
[>= $100K ×]
[Entity: CEX ×]
[30D ×]

Clear All
```

# 9. URL State

所有筛选写入 URL，例如：

```text
?tab=token-transfers
&direction=out
&token=0x...
&min_usd=100000
&entity_type=cex
&range=30d
```

保证：

```text
刷新不丢
复制可复现
案件可引用
分享可复现
```

# 10. Saved Views

允许保存：

```text
USDT 大额转出
CEX 入金
未知地址大额出金
Bridge 流量
案件目标地址
```

保存：

```text
Filters
Columns
Sort
Time Range
Tab
```

# 11. Column Manager

支持：

```text
显示 / 隐藏
拖动顺序
Pin Left
Pin Right
Reset
```

内置 Preset：

```text
默认
调查
资金
合约
紧凑
```

# 12. Transaction Detail Drawer

点击表格行时，先打开右侧 Drawer，不立即跳整页。

显示：

```text
Tx Hash
Status
Block
Time
From
To
Method
Native Value
Token Movement
Fee
Internal Tx Count
Event Count
```

顶部操作：

```text
[完整详情]
[资金流]
[调查]
[导出]
```

# 13. Token Transfer Drawer

显示：

```text
Token
Amount
Historical USD
From
To
Entity
Role
Tx Hash
Method
Time
```

# 14. Row Context Menu

每行 `⋯`：

```text
查看交易
查看 From 地址
查看 To 地址
加入资金流
向上追踪
向下追踪
启动调查
导出此交易
复制 Tx Hash
```

# 15. Multi-select

支持多选：

```text
Add to Fund Flow
Export Selected
Create Investigation Evidence
Tag Addresses
```

# 16. AnalysisContext

新增统一前端上下文：

```text
chain
rootAddress
timeRange
tokenFilters
direction
minUSD
maxUSD
entityFilters
protocolFilters
selectedRows
caseID
```

# 17. 跨模块上下文继承

当前条件：

```text
USDT
OUT
>= $100K
30D
CEX
```

点击 Fund Flow / Investigation / Data Assets / Export 后，必须完整继承。

禁止跳过去后变成：

```text
ALL TOKEN
ALL TIME
```

# 18. 分页

继续使用 Cursor。

前端提供：

```text
上一页
下一页
Page indicator
```

Page Size：

```text
50
100
200
```

默认 100。

# 19. Sticky

表格 Header Sticky。

筛选栏 Sticky。

长列表滚动时用户仍可随时改条件。

# 20. Empty State

必须严格区分：

```text
筛选结果 = 0
```

显示：

```text
没有符合当前筛选条件的数据
```

与：

```text
Coverage 不完整
```

显示：

```text
数据覆盖不完整
```

两者不能混淆。

# 21. Loading

首次加载：

```text
Skeleton Rows
```

翻页：

```text
保留旧数据
+ 局部 Loading
```

不能整表白屏。

# 22. Query Cache

建议：

```text
TanStack Query
```

Cache Key：

```text
chain
address
tab
filters
cursor
```

# 23. Tab 状态保留

切换：

```text
交易
→ 代币转账
→ 返回交易
```

必须保留：

```text
筛选
Cursor
滚动位置
列设置
```

# 24. P0

必须优先完成：

```text
Transaction Table V2
Token Transfer Table V2
Quick Filters
Advanced Filter Drawer
Filter Chips
URL State
Transaction Drawer
Token Transfer Drawer
Row Context Menu
AnalysisContext
Explorer → Fund Flow
Explorer → Investigation
Explorer → Data Assets
Explorer → Export
```

# 25. P1

```text
Column Manager
Column Presets
Saved Views
Multi-select
Bulk Actions
Address Hover Card
Sticky Header
Prefetch
Cache
```

# 26. P2

```text
Keyboard Navigation
Right-click Actions
Compare Addresses
Workspace Persistence
Mobile Table Mode
```

# 27. 验收 Case A

地址有 100,001 Activity。

筛选：

```text
USDT
OUT
>= $100K
```

连续翻页：

```text
0 duplicate
0 missing
```

筛选不丢。

# 28. 验收 Case B

点击交易：

```text
先开 Transaction Drawer
```

可继续：

```text
资金流
调查
完整详情
```

# 29. 验收 Case C

当前：

```text
USDT
OUT
>= $100K
30D
```

点击资金流：

```text
所有条件完整继承
```

# 30. 验收 Case D

刷新浏览器：

```text
筛选完全恢复
```

复制 URL 到新窗口：

```text
得到相同结果
```

# 31. 最终完成标准

用户无需反复跳页面，就能在一个 Address Explorer 页面完成：

```text
查看
筛选
定位
展开
追踪
调查
导出
```

这才是：

```text
Professional Explorer Interaction
```

而不是：

```text
一个有很多功能的 Dashboard
```

# 32. 下一阶段

完成 V1.2 后进入：

```text
Explorer Visual Intelligence V1.3
```

重点再做：

```text
资金趋势图
Token Distribution
Entity Distribution
Counterparty Sankey
Retention Curve
Pass-through Timeline
Address Comparison
```

先把“操作效率”做好，再做高级可视化。
