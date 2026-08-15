# EVM多链链上分析平台 V1.4.1 稳定性修复方案（Codex部署版）

项目路径：

`E:\codex\bsc_analytics`

## 目标

修复当前测试发现的问题：

-   API状态与Manifest状态不一致
-   SQD export_csv=true未生成CSV
-   Cancel任务状态异常
-   大文件下载速度和恢复能力不足
-   数据覆盖状态不透明

------------------------------------------------------------------------

# 1. Task Finalizer

新增最终化流程：

    Stage全部完成
    ↓
    Output检查
    ↓
    Checksum计算
    ↓
    更新任务状态
    ↓
    生成最终Manifest
    ↓
    原子替换

要求：

-   Worker禁止直接修改最终Manifest
-   使用临时文件写入
-   rename保证原子提交

------------------------------------------------------------------------

# 2. 任务状态机

Job：

    CREATED
    RUNNING
    PAUSING
    PAUSED
    CANCELING
    CANCELED
    SUCCESS
    FAILED

Stage：

    WAITING
    RUNNING
    SUCCESS
    FAILED
    SKIPPED
    CANCELED

禁止：

    Job=CANCELED
    Stage=RUNNING

------------------------------------------------------------------------

# 3. Cancel机制修复

流程：

    用户取消
    ↓
    cancellation_requested=true
    ↓
    Worker检测
    ↓
    停止IO
    ↓
    释放goroutine
    ↓
    Stage=CANCELED
    ↓
    生成最终Manifest

------------------------------------------------------------------------

# 4. Dataset Writer统一

解决SQD CSV输出问题。

新增：

    internal/writer/

    parquet_writer.go
    csv_writer.go
    manifest_writer.go

统一：

    SQD
    AWS
    其他数据源

    ↓

    Dataset Writer

    ↓

    Parquet
    CSV
    Manifest

------------------------------------------------------------------------

# 5. 大文件并行下载

新增：

    internal/downloader/

    chunk_manager.go
    range_worker.go
    merge.go
    checkpoint.go

支持：

-   多Range并行
-   Chunk断点
-   ETag校验
-   SHA256验证
-   自动恢复

------------------------------------------------------------------------

# 6. 数据覆盖状态

新增：

    dataset_coverage

字段：

    job_id
    chain_id
    transactions_status
    logs_status
    trace_status
    coverage_percent
    updated_at

状态：

    COMPLETE
    PARTIAL
    DOWNLOADING
    FAILED

地址页面不能显示：

    交易=0

而应该显示：

    交易数据未完整加载
    Coverage: 40%

------------------------------------------------------------------------

# 7. Manifest升级

包含：

-   source
-   chain
-   coverage
-   checksum
-   finished_at
-   schema_version

------------------------------------------------------------------------

# 8. 任务审计

新增：

    task_events

记录：

-   下载开始
-   Chunk完成
-   用户取消
-   Worker异常
-   完成时间

------------------------------------------------------------------------

# 9. 前端修改

增加：

-   任务真实状态
-   数据覆盖进度
-   Manifest状态
-   Checksum信息
-   取消后的最终状态

------------------------------------------------------------------------

# 10. 测试要求

必须通过：

## Manifest一致性

API状态必须等于Manifest状态。

## Cancel测试

取消后：

-   所有Stage停止
-   资源释放
-   状态一致

## CSV测试

SQD：

    export_csv=true

必须生成CSV。

## 断点测试

流程：

    下载50%
    停止服务
    恢复下载

## 大文件测试

测试：

    5GB+
    10GB+

------------------------------------------------------------------------

# 完成标准

V1.4.1完成后：

-   任务状态可靠
-   Manifest可信
-   下载可恢复
-   CSV输出统一
-   大文件支持并行
-   数据覆盖透明
-   支持千万级数据长期运行

架构：

    Data Source

    ↓

    Task Engine

    ↓

    Dataset Writer

    ↓

    Parquet Warehouse

    ↓

    DuckDB Analytics
