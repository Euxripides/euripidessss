# Funds ETL project overview

## Purpose

Funds ETL brings financial-record normalization and blockchain fund-flow analysis into one Go and React application. It is intended for analysts who need to reconcile heterogeneous files, inspect transaction relationships, and preserve the connection between a result and its supporting records.

The maintainer and copyright holder is LINDUJIAN. The repository is maintained under the GitHub account [Euxripides](https://github.com/Euxripides) and distributed under the [MIT License](../LICENSE).

## What the project provides

File-processing modules recognize bank, Alipay, and WeChat records, retain source-specific outputs, and map transactions into a 34-field schema. The pipeline handles dates, amounts, payment direction, duplicate records, and rejected rows. Results can be exported to CSV or Excel and imported into PostgreSQL or MySQL.

The visual workspace builds transaction relationships, supports filtering and path analysis, and allows analysts to add annotations and manage graph layers.

Blockchain modules provide address queries, transaction details, RPC enrichment, and resumable collection through provider adapters. Smart Download plans ranges and coordinates CSV, RPC, SQD, and SQD Cloud workflows. The Cloud path uses a separate Worker project and R2/S3 exchange, followed by local synchronization, validation, and ClickHouse ingestion.

## Engineering contribution

The reusable parts are the source-format parsers, transaction schema, mapping rules, provider adapters, range planning, bounded downstream queues, and audit outputs. Together they give contributors a concrete workflow for financial-data interoperability and fund-flow inspection.

The Go backend serves both APIs and built frontend assets. SQLite holds control metadata; ClickHouse is the default chain-query source; DuckDB CLI supports Parquet work. Windows and PowerShell are the current deployment focus. Some data paths remain machine-specific, and Cloud Worker deployment is separate.

## Maintenance

Repository history records work on data cleaning, mappings, transaction enrichment, download scheduling, tests, deployment scripts, and documentation. Contributions are reviewed against reproducible examples and the checks described in [CONTRIBUTING.md](../CONTRIBUTING.md).

Current maintenance includes dependency and credential-history review. The security audit identified open findings; [SECURITY.md](../SECURITY.md) describes the deployment model and reporting process.

## 中文项目说明

Funds ETL 将银行、支付宝、微信流水整理与链上地址查询、数据下载、资金追踪放在同一工作台。核心能力包括来源识别、34 列流水标准化、清洗去重、审计导出、数据库导入及资金关系图。

项目由 LINDUJIAN 维护，GitHub 账号为 Euxripides。可复用模块涵盖解析器、字段映射、Provider 适配器、Range 规划和有界数据流水线。当前主要部署环境为 Windows，链上下载验证主要围绕 BSC 展开，使用外部数据库与云端 Worker 的功能需要单独配置。

项目采用 MIT 许可证；第三方软件与数据继续遵循各自许可。安装、配置与运行方法见 [README](../README.md)。
