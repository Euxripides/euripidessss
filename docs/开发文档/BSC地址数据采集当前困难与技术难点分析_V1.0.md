# BSC 地址数据采集当前困难与技术难点分析 V1.0

## 一、当前目标

约 1,000 个 BSC 地址，包含普通钱包、普通合约、Token 合约、LP、Router 等，需要：

- 普通交易
- BNB 转账
- BEP-20 Token Transfer
- Input、Method Selector、方法名称与参数
- Receipt、Logs
- 部分重点交易的内部调用和 Trace
- 部分重点 LP / 合约的历史汇率和真实成交价

预计总量可能接近 1 亿行，预算上限约 500 元人民币。

## 二、已经验证的现状

### SQD

- 经常 503、No available workers
- 重试、退避、熔断只能改善恢复，不能保证持续可用
- 不适合作为生产主源

### AWS 公共数据

- 文件大，当前 Windows + 火绒环境存在 Range 下载 0 字节和连接中断
- 下载后还要自行解析和建立地址索引
- 现阶段不适合作为唯一主源

### Moralis

- 可以直接按地址查普通交易和 Token Transfer
- 字段接近 OKLink
- 免费层和 2M CU 套餐吞吐不足，无法短期处理接近 1 亿行

### Chainstack

- 适合 Transaction、Receipt、Logs、Trace
- 不提供按地址完整历史索引
- 只能作为已知交易哈希后的补全和 Trace 数据源

### Chainbase

已经实测：

- `bsc.token_transfers` 可以查到目标地址数据
- 2025 年 1 月查询返回 10,000 行
- 结果约 2.61 MB
- 耗时约 23.2 秒
- 全历史 SQL 出现查询池内存超限，内存达到约 23.45 GB 后被终止

结论：

```text
Chainbase 数据可用，但完整历史必须拆分查询。
```

## 三、当前最大困难

### 1. 没有一个 500 元以内的数据源同时满足全部需求

理想数据源需要同时提供：

```text
按地址查询
完整历史
高吞吐
稳定
低价格
Transactions
Token Transfers
Trace
方法解析
历史价格
批量 Parquet
```

目前不存在这样的单一低价数据源。

必须采用：

```text
Chainbase 批量 SQL / 地址 API
+ 本地解析
+ 重点交易 Trace
+ 选择性价格富化
```

### 2. LP、Router、Token 合约会造成数据爆炸

普通钱包的数据量通常有限，但一个热门 LP、Router 或 Token 合约可能包含：

- 数百万笔交易
- 数千万条 Transfer
- 大量 Swap、Mint、Burn、Sync
- 大量无关用户交易
- 大量内部调用

必须区分：

```text
目标地址相关数据
≠
该合约全部业务历史
```

否则一个热门 LP 就可能拖垮整个任务。

### 3. Chainbase 完整历史 SQL 会内存超限

当前已经验证，以下模式不可行：

```sql
SELECT *
FROM bsc.token_transfers
WHERE from_address IN (...)
   OR to_address IN (...);
```

即使加 `LIMIT 1000`，数据库仍可能扫描完整大表。

`LIMIT` 只限制最终返回行数，不限制底层扫描量。

必须拆成：

```text
单地址 × 单方向 × 单月/单周/单日
```

### 4. 返回行数正好等于 LIMIT 时，无法判断是否完整

当前结果正好返回 10,000 行，只能说明：

```text
至少有 10,000 行
```

不能说明完整结果只有 10,000 行。

系统必须实现：

```text
如果 row_count == LIMIT
→ 自动缩小时间范围
→ 月拆周
→ 周拆日
→ 日拆小时
→ 最后按区块二分
```

这是保证完整性的关键。

### 5. 多地址、多方向会产生重复记录

同一条 Transfer 可能同时命中：

```text
from_address = 目标地址 A
to_address   = 目标地址 B
```

使用 `UNION ALL` 时会出现两次。

必须按：

```text
chain_id + transaction_hash + log_index
```

去重，并单独保存：

```text
transaction_hash
target_address
direction
match_type
```

### 6. Transactions 和 Token Transfers 是两套不同数据

只查询 `token_transfers` 会漏掉：

- BNB 转账
- 合约调用
- 合约部署
- 失败交易
- 没有 Transfer 事件的交易

只查询 `transactions` 会漏掉：

- BEP-20 转账
- Mint / Burn
- Swap 中多条 Token Transfer
- LP Token 变化

至少要维护：

```text
transactions
token_transfers
logs
trace_calls
```

### 7. 合约方法分析不能只看顶层 Input

顶层交易只能识别第一层方法。

DeFi 常见调用链：

```text
EOA → Router → LP → Token → Fee Contract
```

完整方法分析需要：

- 顶层 Input
- Trace 内部调用
- ABI
- Proxy 实现合约
- DelegateCall
- 4byte 签名
- 调用层级
- 成功/失败状态

### 8. 不能给全部交易执行 Trace

Trace 比普通交易和 Receipt 更慢、更贵。

必须分级：

```text
P0：必须 Trace
- 大额交易
- Swap
- 加减流动性
- Mint/Burn
- 代理升级
- 复杂归集
- 失败交易

P1：只取 Receipt + Logs
- 普通 Token Transfer
- 简单合约调用

P2：只保存摘要
- 垃圾币
- 空投
- 无关 LP 交易
```

### 9. 历史价格不能逐笔查询

只有部分 LP 和合约需要历史汇率，这是当前有利条件。

必须区分：

```text
execution_price_usd
```

真实 Swap 成交价。

```text
market_price_usd
```

市场参考价。

真实成交价优先从同一交易的 Swap 输入输出计算；市场价按分钟或小时缓存。

不能给每条 Transfer 单独请求价格 API。

### 10. 预算与规模冲突

500 元无法购买：

- Bitquery 完整历史包
- Allium Datashare
- 企业级 Parquet 定向交付
- 大规模 Moralis 套餐
- 自建 BSC Archive 节点

因此必须接受：

- 下载周期更长
- 调度器更复杂
- 本地解析更多
- 必须严格控制 LP、Router 和 Trace 范围

## 四、当前真正需要开发的核心能力

### 1. 自适应分区调度器

输入：

```text
地址
数据集
方向
开始时间
结束时间
```

自动执行：

```text
先按月
→ 命中 LIMIT 或超时则拆周
→ 仍超限则拆日
→ 再拆小时
→ 最后按区块二分
```

### 2. 完整性控制

每个分区保存：

```text
address
dataset
direction
start_time
end_time
row_count
limit
is_truncated
status
retry_count
checksum
```

判断规则：

```text
row_count < limit
→ 分区可能完整

row_count == limit
→ 必须继续拆分
```

### 3. Checkpoint 和断点续传

必须支持：

- 成功落盘后才标记完成
- 失败后继续
- 不重复跑已完成分区
- 错误不能写成空数据
- 可暂停、恢复、重试单分区

### 4. 多表统一索引

本地至少需要：

```text
transactions
token_transfers
logs
trace_calls
transaction_address_relation
address_classification
method_registry
price_enrichment
```

### 5. 地址类型识别

必须自动识别：

```text
EOA
Contract
Token
LP
Router
Proxy
Exchange
Bridge
Unknown
```

不同类型走不同下载策略。

## 五、风险等级

### 高风险

- 热门 LP / Router 全历史
- 全交易 Trace
- 无时间范围 SQL
- 返回行数正好等于 LIMIT
- 将 5xx 写成空数据
- 多地址重复统计
- 历史价格逐笔请求

### 中风险

- ABI 不完整
- Proxy 识别错误
- Chainbase 免费资源池波动
- 大分区下载中断
- SQL 查询结果不稳定

### 低风险

- 普通 EOA 交易
- 普通 Token Transfer
- Method Selector 提取
- DuckDB 去重
- 已知交易哈希查询 Receipt

## 六、当前可行架构

```text
Chainbase Data Cloud SQL
→ 按地址、方向、时间自动分区
→ 下载 Transactions 和 Token Transfers
→ 本地 Parquet + DuckDB 去重
→ 地址分类
→ 本地 ABI 和方法解析
→ 仅重点交易补 Trace
→ 仅重点 LP / 合约补历史价格
```

数据源定位：

```text
Chainbase：当前主批量源
Chainstack：重点 Trace 补全
Moralis：少量地址补采和交叉验证
SQD：后台备用
AWS：长期整链数据资产方案
```

## 七、下一阶段最优先事项

下一步应开发：

```text
Chainbase Adaptive Partition Downloader V1.0
```

必须包含：

1. 地址批量导入
2. 地址类型识别
3. 月/周/日/小时/区块自动拆分
4. IN / OUT 分开查询
5. LIMIT 截断识别
6. SQL 自动生成
7. 查询状态轮询
8. 结果下载
9. Parquet 落盘
10. Checkpoint
11. 唯一键去重
12. 失败重试和熔断
13. 完整性报告

这是当前真正的技术核心。
