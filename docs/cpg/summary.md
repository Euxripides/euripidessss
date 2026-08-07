# Funds ETL — Code Property Graph

> Auto-generated. **47 packages, 3090 functions, 835 types.**

Use this as a **project map** before reading source code.

## 1. Architecture Layers

### Entry Point (1 pkgs, 1 funcs, 0 types)

- **`server`** — 1 fn, 0s/0i, uses: api, config, logger, rules

### API / HTTP (1 pkgs, 321 funcs, 48 types)

- **`api`** — 321 fn, 46s/0i, uses: analysis/duckdb, analyticsapi, chain, cloudruntime, used-by: 1 pkg(s)

### ETL Pipeline (47 pkgs, 3090 funcs, 835 types)

- **`cryptodownload`** — 655 fn, 132s/1i, uses: cryptodownload/useragent, used-by: 1 pkg(s)
- **`intelligence`** — 335 fn, 89s/4i, uses: analyticsapi, dynamicinvestigation, investigationstore, logger, used-by: 1 pkg(s)
- **`api`** — 321 fn, 46s/0i, uses: analysis/duckdb, analyticsapi, chain, cloudruntime, used-by: 1 pkg(s)
- **`parquetdownload`** — 200 fn, 33s/0i, uses: analysis/duckdb, chain, datasource, datasource/aws, used-by: 4 pkg(s)
- **`downloadengine`** — 166 fn, 67s/5i, used-by: 1 pkg(s)
- **`downloadscheduler`** — 161 fn, 34s/9i, uses: chain, cloudruntime, datasetsync, logger, used-by: 1 pkg(s)
- **`dbimport`** — 110 fn, 27s/1i, uses: model, parser, used-by: 1 pkg(s)
- **`rpcmanager`** — 110 fn, 21s/0i, uses: chain, used-by: 5 pkg(s)
- **`etl`** — 99 fn, 10s/0i, uses: model, parser, provider, rules, used-by: 1 pkg(s)
- **`dynamicinvestigation`** — 98 fn, 21s/2i, uses: analyticsapi, chain, logger, parquetdownload, used-by: 2 pkg(s)
- **`datasource/sqd`** — 93 fn, 25s/0i, uses: chain, used-by: 4 pkg(s)
- **`dunetools`** — 88 fn, 16s/3i, used-by: 1 pkg(s)
- **`parser`** — 85 fn, 7s/0i, used-by: 6 pkg(s)
- **`cloudruntime`** — 48 fn, 5s/0i, uses: logger, s3store, used-by: 2 pkg(s)
- **`datasourcemanager`** — 44 fn, 13s/0i, uses: chain, datasource/aws, datasource/sqd, rpcmanager, used-by: 2 pkg(s)
- **`downloadengine/provider`** — 44 fn, 12s/0i, uses: analysis/duckdb, chain, datasource, datasource/aws
- **`analyticsapi`** — 41 fn, 16s/0i, uses: analysis/duckdb, logger, used-by: 7 pkg(s)
- **`investigationstore`** — 37 fn, 13s/1i, used-by: 3 pkg(s)
- **`datasetsync`** — 31 fn, 7s/1i, uses: analysis/duckdb, logger, s3store, used-by: 3 pkg(s)
- **`flow`** — 29 fn, 11s/1i, uses: chain, investigationstore, normalize, rpcmanager, used-by: 1 pkg(s)
- **`rules`** — 27 fn, 1s/0i, uses: parser, used-by: 5 pkg(s)
- **`analysis/duckdb`** — 25 fn, 4s/0i, used-by: 10 pkg(s)
- **`provider`** — 25 fn, 5s/1i, uses: model, parser, rules, used-by: 1 pkg(s)
- **`s3store`** — 23 fn, 5s/1i, used-by: 3 pkg(s)
- **`casefile`** — 18 fn, 12s/0i, uses: analysis/duckdb, analyticsapi, balance, investigation
- **`datasource/rpc`** — 17 fn, 3s/0i, uses: chain, normalize, used-by: 1 pkg(s)
- **`downloader`** — 17 fn, 6s/0i, uses: writer, used-by: 1 pkg(s)
- **`scanner`** — 17 fn, 2s/0i, uses: parser, rules, used-by: 2 pkg(s)
- **`investigation`** — 13 fn, 8s/0i, uses: analysis/duckdb, analyticsapi, used-by: 1 pkg(s)
- **`datasetevents`** — 12 fn, 2s/0i, used-by: 1 pkg(s)
- **`datasource/sqd/scheduler`** — 12 fn, 4s/0i
- **`normalize`** — 12 fn, 12s/0i, uses: chain, datasource/sqd, used-by: 5 pkg(s)
- **`balance`** — 10 fn, 11s/0i, uses: analysis/duckdb, analyticsapi, used-by: 1 pkg(s)
- **`graphincrement`** — 10 fn, 3s/0i, uses: analysis/duckdb, datasetsync, used-by: 1 pkg(s)
- **`storage/control`** — 9 fn, 2s/0i, used-by: 1 pkg(s)
- **`graphintel`** — 8 fn, 5s/0i, uses: analysis/duckdb, analyticsapi
- **`storage`** — 8 fn, 2s/0i, used-by: 1 pkg(s)
- **`writer`** — 8 fn, 1s/1i, used-by: 3 pkg(s)
- **`logger`** — 6 fn, 1s/0i, used-by: 8 pkg(s)
- **`cryptodownload/browser_stealth`** — 5 fn, 1s/0i
- **`datasource/aws`** — 5 fn, 2s/0i, uses: chain, datasource, used-by: 3 pkg(s)
- **`config`** — 3 fn, 2s/0i, used-by: 2 pkg(s)
- **`chain`** — 2 fn, 1s/0i, used-by: 13 pkg(s)
- **`cryptodownload/useragent`** — 2 fn, 0s/0i, used-by: 1 pkg(s)
- **`server`** — 1 fn, 0s/0i, uses: api, config, logger, rules
- **`datasource`** — 0 fn, 1s/3i, uses: chain, normalize, used-by: 3 pkg(s)
- **`model`** — 0 fn, 13s/1i, used-by: 4 pkg(s)

### Storage & IO (5 pkgs, 152 funcs, 41 types)

- **`dbimport`** — 110 fn, 27s/1i, uses: model, parser, used-by: 1 pkg(s)
- **`downloader`** — 17 fn, 6s/0i, uses: writer, used-by: 1 pkg(s)
- **`storage/control`** — 9 fn, 2s/0i, used-by: 1 pkg(s)
- **`storage`** — 8 fn, 2s/0i, used-by: 1 pkg(s)
- **`writer`** — 8 fn, 1s/1i, used-by: 3 pkg(s)

### Crypto / Blockchain (13 pkgs, 1233 funcs, 286 types)

- **`cryptodownload`** — 655 fn, 132s/1i, uses: cryptodownload/useragent, used-by: 1 pkg(s)
- **`parquetdownload`** — 200 fn, 33s/0i, uses: analysis/duckdb, chain, datasource, datasource/aws, used-by: 4 pkg(s)
- **`rpcmanager`** — 110 fn, 21s/0i, uses: chain, used-by: 5 pkg(s)
- **`datasource/sqd`** — 93 fn, 25s/0i, uses: chain, used-by: 4 pkg(s)
- **`dunetools`** — 88 fn, 16s/3i, used-by: 1 pkg(s)
- **`datasourcemanager`** — 44 fn, 13s/0i, uses: chain, datasource/aws, datasource/sqd, rpcmanager, used-by: 2 pkg(s)
- **`datasource/rpc`** — 17 fn, 3s/0i, uses: chain, normalize, used-by: 1 pkg(s)
- **`datasource/sqd/scheduler`** — 12 fn, 4s/0i
- **`cryptodownload/browser_stealth`** — 5 fn, 1s/0i
- **`datasource/aws`** — 5 fn, 2s/0i, uses: chain, datasource, used-by: 3 pkg(s)
- **`chain`** — 2 fn, 1s/0i, used-by: 13 pkg(s)
- **`cryptodownload/useragent`** — 2 fn, 0s/0i, used-by: 1 pkg(s)
- **`datasource`** — 0 fn, 1s/3i, uses: chain, normalize, used-by: 3 pkg(s)

### Infrastructure (4 pkgs, 34 funcs, 22 types)

- **`analysis/duckdb`** — 25 fn, 4s/0i, used-by: 10 pkg(s)
- **`logger`** — 6 fn, 1s/0i, used-by: 8 pkg(s)
- **`config`** — 3 fn, 2s/0i, used-by: 2 pkg(s)
- **`model`** — 0 fn, 13s/1i, used-by: 4 pkg(s)

## 2. Coupling Hotspots

Sorted by **instability** (outgoing deps / total deps). High = fragile.

| Package | Out | In | Instability | Funcs |
|---------|-----|----|-------------|-------|
| `api` | 28 | 1 | 0.97 █████████ | 321 |
| `parquetdownload` | 11 | 4 | 0.73 ███████ | 200 |
| `downloadengine/provider` | 9 | 0 | 1.00 ██████████ | 44 |
| `downloadscheduler` | 6 | 1 | 0.86 ████████ | 161 |
| `etl` | 5 | 1 | 0.83 ████████ | 99 |
| `server` | 4 | 0 | 1.00 ██████████ | 1 |
| `casefile` | 4 | 0 | 1.00 ██████████ | 18 |
| `datasourcemanager` | 4 | 2 | 0.67 ██████ | 44 |
| `dynamicinvestigation` | 4 | 2 | 0.67 ██████ | 98 |
| `flow` | 4 | 1 | 0.80 ████████ | 29 |
| `intelligence` | 4 | 1 | 0.80 ████████ | 335 |
| `datasetsync` | 3 | 3 | 0.50 █████ | 31 |
| `provider` | 3 | 1 | 0.75 ███████ | 25 |
| `analyticsapi` | 2 | 7 | 0.22 ██ | 41 |
| `balance` | 2 | 1 | 0.67 ██████ | 10 |

**Key**: `parquetdownload` (11 out) and `api` (15 out) are the most coupled.

## 3. Cycle Detection

✅ No circular dependencies.

## 4. Interface Inventory

**35 interfaces**:

| Interface | Package | Methods |
|-----------|---------|--------|
| `csvBrowserEmailRequester` | `cryptodownload` | Request |
| `ParquetValidator` | `datasetsync` | Validate |
| `LogsSource` | `datasource` | ProbeSchema |
| `TransactionSource` | `datasource` | DiscoverTransactions |
| `FirstSeenResolver` | `downloadengine` | ResolveFirstSeen |
| `CoverageSource` | `downloadscheduler` | AddressTxCount |
| `HealthSource` | `downloadscheduler` | SQDHealth |
| `RPCClient` | `downloadscheduler` | Call |
| `jobPoller` | `downloadscheduler` | JobProgress |
| `LinkVerifier` | `dunetools` | VerifyEmailLink |
| `Mailbox` | `dunetools` | WaitForVerificationLink |
| `AcquisitionExecutor` | `dynamicinvestigation` | Execute |
| `AssetStore` | `flow` | AddressAssets |
| `Expander` | `intelligence` | Expand |
| `FlowSource` | `intelligence` | Flows |
| `SQLExecutor` | `writer` | ExecSQLJSON |
| `ReceiptSource` | `datasource` | Probe, Receipts |
| `exportSchemaExecutor` | `dbimport` | ExecContext, QueryContext |
| `RecoveryWriter` | `downloadscheduler` | WriteTokenTransfers, MergeTokenTransfers |
| `SQDEngine` | `downloadscheduler` | Start, Get |
| `StateProvider` | `downloadscheduler` | State, StateReasons |
| `DiscoverySource` | `dynamicinvestigation` | Flows, Profile |
| `AIChatter` | `intelligence` | Chat, Configured |
| `Provider` | `downloadengine` | Name, Capabilities, Health |
| `BrowserClient` | `dunetools` | Register, VerifyEmail, LoginAndExtract |
| `Executor` | `intelligence` | Type, Execute, Validate |
| `Provider` | `provider` | Name, ProcessDirectory, ProcessFile |
| `LookupProvider` | `downloadengine` | Name, Capabilities, Health, ExecuteLookup |
| `CloudRuntime` | `downloadscheduler` | SubmitJob, JobStatus, CancelJob, Status |
| `ObjectProvider` | `downloadengine` | Name, Capabilities, Health, Estimate, ExecuteObject |
| `StreamingProvider` | `downloadengine` | Name, Capabilities, Health, Estimate, ExecuteStream |
| `Store` | `investigationstore` | Save, Get, List, Delete, Exists |
| `ObjectStore` | `s3store` | Get, Put, Exists, Delete, List |
| `Provider` | `downloadscheduler` | Kind, Name, Tier, CanHandle, Available, ManualOnly +2 |
| `Storage` | `model` | CreateSession, GetSession, ListSessions, SaveTransactions, LoadTransactions, SaveOutput +2 |

## 5. Key Call Paths

HTTP handlers down to data layer:

- **BuildFlowGraph** ← `api`.HandleProcess ← `etl`.BuildFlowGraph
- **StartTask** ← `dbimport`.StartTask
- **CollectAddress** ← `cryptodownload`.CollectAddress
- **FetchTokenTransfersByTimeWindow** ← `cryptodownload`.FetchTokenTransfersByTimeWindow

## 6. Package Risk Matrix

Size × coupling = maintenance risk.

| Package | Size | Coupling | Risk | Level |
|---------|------|----------|------|-------|
| `api` | 369 | 29 | 11070 | 🔴 HIGH |
| `parquetdownload` | 236 | 15 | 3776 | 🔴 HIGH |
| `intelligence` | 443 | 5 | 2658 | 🔴 HIGH |
| `cryptodownload` | 805 | 2 | 2415 | 🔴 HIGH |
| `downloadscheduler` | 210 | 7 | 1680 | 🔴 HIGH |
| `rpcmanager` | 131 | 6 | 917 | 🔴 HIGH |
| `dynamicinvestigation` | 126 | 6 | 882 | 🔴 HIGH |
| `etl` | 109 | 6 | 763 | 🔴 HIGH |
| `datasource/sqd` | 121 | 5 | 726 | 🔴 HIGH |
| `parser` | 92 | 6 | 644 | 🔴 HIGH |
| `analyticsapi` | 57 | 9 | 570 | 🔴 HIGH |
| `downloadengine/provider` | 56 | 9 | 560 | 🔴 HIGH |
| `dbimport` | 139 | 3 | 556 | 🔴 HIGH |
| `downloadengine` | 254 | 1 | 508 | 🔴 HIGH |
| `datasourcemanager` | 57 | 6 | 399 | 🟡 MED |

## 7. Dependency Diagram

```mermaid
graph TB
  subgraph Entry["Entry"]
    server["server"]
  end
  subgraph API["API Layer"]
    api["api (279 fn)"]
  end
  subgraph ETL["ETL Pipeline"]
    etl["etl"]
    scanner["scanner"]
    parser["parser"]
    provider["provider"]
    rules["rules"]
  end
  subgraph Data["Data Layer"]
    dbimport["dbimport"]
    storage["storage"]
    duckdb_["duckdb"]
  end
  subgraph Crypto["Crypto/Scraping"]
    cryptodw["cryptodownload<br/>(655 fn)"]
    parquet["parquetdownload"]
    rpc["rpcmanager"]
    normal["normalize"]
    chain_["chain"]
  end
  subgraph Infra["Infrastructure"]
    model["model"]
    config["config"]
    logger_["logger"]
  end
  server --> api
  api --> etl
  api --> cryptodw
  api --> parquet
  api --> dbimport
  api --> rpc
  etl --> provider
  etl --> parser
  etl --> scanner
  etl --> rules
  etl --> model
  provider --> parser
  provider --> rules
  provider --> model
  cryptodw --> chain_
  parquet --> chain_
  parquet --> normal
  parquet --> rpc
  rpc --> chain_
  normal --> chain_
  dbimport --> parser
  dbimport --> model
  scanner --> parser
  scanner --> rules
  rules --> parser
```

## 8. Agent Quick-Start

**Before modifying code**, check:

1. **Which layer?** See Section 1.
2. **Who depends on it?** See Section 2 coupling table.
3. **Any cycles?** See Section 3.
4. **Which interface?** See Section 4.
5. **Full data**: `cpg.json` has per-function CFG stats, param types, exact call sites.

**Common tasks & where to look**:

| Task | Primary Package(s) |
|------|-------------------|
| Add new data source format | `parser`, `provider`, `rules` |
| Add API endpoint | `api/router.go`, `api/handlers.go` |
| Modify ETL pipeline | `etl/etl.go` |
| Database import logic | `dbimport/` |
| Crypto download / scraping | `cryptodownload/` (655 fn!) |
| Flow graph visualization | `etl/flow_graph.go` + `frontend/` |
| Config / env vars | `config/config.go` |

**Biggest files**: `api/handlers.go`, `etl/etl.go`, all of `cryptodownload/`, `parquetdownload/`.

---
*Generated by `tools/cpg/` (Go) + `tools/cpg/enhance_summary.py`.*
