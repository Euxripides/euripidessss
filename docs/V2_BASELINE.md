# V2 下载引擎 — 阶段0 基线报告

> 生成时间：2026-07-30  
> 项目根：`E:\codex\etl`

## 1. parquetdownload 模块清单

| 文件 | 行数 | 导出 |
|------|------|------|
| types.go | 204 | Settings, StartRequest, AddressSummary, Preview, Stage, FileTask, DatasetCoverage, Job, ManifestInfo, UploadAddressResponse; Status*(9), Coverage*(5) |
| handler.go | 300 | Handler, NewHandler |
| manager.go | 565 | Manager, NewManager |
| firstseen.go | 418 | FirstSeenStatus(5), FSCoverage*(3), FirstSeenResponse, EffectiveDateRange |
| sqd_checkpoint.go | 252 | SQDCheckpointStore, SQDCheckpoint, SQDBlockChunk, DefaultSQDChunkSize |
| process.go | 741 | (内部) |
| sqd_ingest.go | 938 | (内部) |
| analytics.go | 379 | (内部) |
| address_query.go | 321 | (内部) |
| download.go | 241 | (内部) |
| task_finalizer.go | 254 | (内部) |
| addresses.go | 125 | (内部) |
| coverage.go | 142 | (内部) |
| paths.go + paths_*.go | 158 | (内部) |
| s3.go | 65 | (内部) |
| dataset_writer.go | 40 | (内部) |

## 2. REST API 清单（与 V2 相关部分）

| 路径 | 方法 | Handler | 底层 |
|------|------|---------|------|
| `/api/crypto/parquet/*path` | ANY | HandleCryptoParquet | parquetdownload.Handler |
| `/api/crypto/addresses/:chain/:address/first-seen` | ANY | HandleFirstSeen | parquetdownload.Handler |
| `/api/address/*path` | GET | HandleAddressAnalytics | parquetdownload.Handler |
| `/api/crypto/rpc/*path` | ANY | HandleCryptoRPC | rpcmanager.Handler |
| `/api/crypto/enrichment/*path` | ANY | HandleCryptoRPC | rpcmanager.Handler (别名) |
| `/api/crypto/datasource/*path` | ANY | HandleCryptoDataSource | datasourcemanager.Handler |
| `/api/crypto/download/*path` | ANY | HandleCryptoDownload | cryptodownload.Handler |
| `/api/crypto/address-classify` | POST | HandleCryptoAddressClassify | 内置逻辑 |

## 3. DuckDB 引擎

| 方法 | 用途 |
|------|------|
| ExecSQL | 执行任意 SQL，返回 CombinedOutput |
| ExecSQLJSON | 执行 SQL + `-json`，返回 `[]map[string]interface{}` |
| CreateTableFromCSV/CSVFiles | 多 CSV 建表 |
| CreateTableFromXLSXFiles | Excel 建表 |
| DropTable / TableRowCount | 删除/统计 |

## 4. V1 状态码

| 常量 | 值 |
|------|-----|
| StatusQueued/Running/Pausing/Paused/Canceling/Done/Failed/Skipped/Canceled | queued~canceled (9个) |
| CoverageComplete/Partial/Downloading/Failed/NotSelected | COMPLETE~NOT_SELECTED (5个) |

## 5. 配置项

Settings: DataRoot, DownloadConcurrency, DuckDBThreads, MemoryLimit, MinimumFreeGB, KeepSourceFiles, ExportCSV, ReceiptBatchSize

## 6. Checkpoint / Manifest 格式

- Checkpoint: 未导出 `checkpoint` 结构体 (SourceURI, ETag, AddressHash, SizeBytes, OutputPath, CSVPath, SourceRows, Matched, Completed)
- Manifest: `ManifestInfo` (Path, Status, SchemaVersion, Consistent, FinishedAt)
- SQD Checkpoint: `SQDCheckpointStore` + `SQDCheckpoint` (DuckDB 存储)

## 7. 测试基线

```
go vet ./internal/...    ✅ 零警告
go test ./internal/...   ✅ 19/22包全部通过，3包无测试文件
parquetdownload 包       ✅ 1.257s，包含 17 个测试函数
```

## 8. 已知风险

1. **平铺状态**: Job.Status 是单个 string 字段，无 Stage 分离
2. **缺少 Migration Framework**: CREATE TABLE 散落在业务代码中
3. **Checkpoint 仅 SQD**：无通用 V2 Checkpoint
4. **Manifest 无原子写入**：直接写 JSON 文件
5. **无 Provider 抽象**：SQD/AWS 调用硬编码在 process.go
6. **无 Coverage Resolver**：仅 firstseen.go 有基础 coverage 检测
7. **Windows/386 不支持 -race**：无法运行竞态检测
