# Funds ETL — Code Property Graph

> Auto-generated. **37 packages, 2230 functions, 573 types.**

Use this as a **project map** before reading source code.

## 1. Architecture Layers

### Entry Point (1 pkgs, 1 funcs, 0 types)

- **`server`** — 1 fn, 0s/0i, uses: api, config, logger, rules

### API / HTTP (1 pkgs, 281 funcs, 43 types)

- **`api`** — 281 fn, 41s/0i, uses: analysis/duckdb, analyticsapi, config, cryptodownload, used-by: 1 pkg(s)

### ETL Pipeline (37 pkgs, 2230 funcs, 573 types)

- **`cryptodownload`** — 655 fn, 132s/1i, uses: cryptodownload/useragent, used-by: 1 pkg(s)
- **`api`** — 281 fn, 41s/0i, uses: analysis/duckdb, analyticsapi, config, cryptodownload, used-by: 1 pkg(s)
- **`parquetdownload`** — 189 fn, 29s/0i, uses: analysis/duckdb, chain, datasource, datasource/aws, used-by: 2 pkg(s)
- **`downloadengine`** — 166 fn, 67s/5i, used-by: 1 pkg(s)
- **`dbimport`** — 110 fn, 27s/1i, uses: model, parser, used-by: 1 pkg(s)
- **`rpcmanager`** — 110 fn, 21s/0i, uses: chain, used-by: 4 pkg(s)
- **`etl`** — 99 fn, 10s/0i, uses: model, parser, provider, rules, used-by: 1 pkg(s)
- **`datasource/sqd`** — 90 fn, 25s/0i, uses: chain, used-by: 4 pkg(s)
- **`dunetools`** — 88 fn, 16s/3i, used-by: 1 pkg(s)
- **`parser`** — 85 fn, 7s/0i, used-by: 6 pkg(s)
- **`datasourcemanager`** — 44 fn, 13s/0i, uses: chain, datasource/aws, datasource/sqd, rpcmanager, used-by: 2 pkg(s)
- **`downloadengine/provider`** — 44 fn, 12s/0i, uses: analysis/duckdb, chain, datasource, datasource/aws
- **`rules`** — 27 fn, 1s/0i, uses: parser, used-by: 5 pkg(s)
- **`provider`** — 25 fn, 5s/1i, uses: model, parser, rules, used-by: 1 pkg(s)
- **`analyticsapi`** — 23 fn, 9s/0i, uses: analysis/duckdb, used-by: 5 pkg(s)
- **`analysis/duckdb`** — 21 fn, 4s/0i, used-by: 8 pkg(s)
- **`casefile`** — 18 fn, 12s/0i, uses: analysis/duckdb, analyticsapi, balance, investigation
- **`datasource/rpc`** — 17 fn, 3s/0i, uses: chain, normalize, used-by: 1 pkg(s)
- **`downloader`** — 17 fn, 6s/0i, uses: writer, used-by: 1 pkg(s)
- **`scanner`** — 17 fn, 2s/0i, uses: parser, rules, used-by: 2 pkg(s)
- **`investigation`** — 13 fn, 8s/0i, uses: analysis/duckdb, analyticsapi, used-by: 1 pkg(s)
- **`datasource/sqd/scheduler`** — 12 fn, 4s/0i
- **`normalize`** — 12 fn, 12s/0i, uses: chain, datasource/sqd, used-by: 3 pkg(s)
- **`balance`** — 10 fn, 11s/0i, uses: analysis/duckdb, analyticsapi, used-by: 1 pkg(s)
- **`storage/control`** — 9 fn, 2s/0i, used-by: 1 pkg(s)
- **`graphintel`** — 8 fn, 5s/0i, uses: analysis/duckdb, analyticsapi
- **`storage`** — 8 fn, 2s/0i, used-by: 1 pkg(s)
- **`writer`** — 8 fn, 1s/1i, used-by: 3 pkg(s)
- **`logger`** — 6 fn, 1s/0i, used-by: 1 pkg(s)
- **`cryptodownload/browser_stealth`** — 5 fn, 1s/0i
- **`datasource/aws`** — 5 fn, 2s/0i, uses: chain, datasource, used-by: 3 pkg(s)
- **`config`** — 3 fn, 2s/0i, used-by: 2 pkg(s)
- **`chain`** — 2 fn, 1s/0i, used-by: 9 pkg(s)
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

### Crypto / Blockchain (13 pkgs, 1219 funcs, 282 types)

- **`cryptodownload`** — 655 fn, 132s/1i, uses: cryptodownload/useragent, used-by: 1 pkg(s)
- **`parquetdownload`** — 189 fn, 29s/0i, uses: analysis/duckdb, chain, datasource, datasource/aws, used-by: 2 pkg(s)
- **`rpcmanager`** — 110 fn, 21s/0i, uses: chain, used-by: 4 pkg(s)
- **`datasource/sqd`** — 90 fn, 25s/0i, uses: chain, used-by: 4 pkg(s)
- **`dunetools`** — 88 fn, 16s/3i, used-by: 1 pkg(s)
- **`datasourcemanager`** — 44 fn, 13s/0i, uses: chain, datasource/aws, datasource/sqd, rpcmanager, used-by: 2 pkg(s)
- **`datasource/rpc`** — 17 fn, 3s/0i, uses: chain, normalize, used-by: 1 pkg(s)
- **`datasource/sqd/scheduler`** — 12 fn, 4s/0i
- **`cryptodownload/browser_stealth`** — 5 fn, 1s/0i
- **`datasource/aws`** — 5 fn, 2s/0i, uses: chain, datasource, used-by: 3 pkg(s)
- **`chain`** — 2 fn, 1s/0i, used-by: 9 pkg(s)
- **`cryptodownload/useragent`** — 2 fn, 0s/0i, used-by: 1 pkg(s)
- **`datasource`** — 0 fn, 1s/3i, uses: chain, normalize, used-by: 3 pkg(s)

### Infrastructure (4 pkgs, 30 funcs, 22 types)

- **`analysis/duckdb`** — 21 fn, 4s/0i, used-by: 8 pkg(s)
- **`logger`** — 6 fn, 1s/0i, used-by: 1 pkg(s)
- **`config`** — 3 fn, 2s/0i, used-by: 2 pkg(s)
- **`model`** — 0 fn, 13s/1i, used-by: 4 pkg(s)

## 2. Coupling Hotspots

Sorted by **instability** (outgoing deps / total deps). High = fragile.

| Package | Out | In | Instability | Funcs |
|---------|-----|----|-------------|-------|
| `api` | 16 | 1 | 0.94 █████████ | 281 |
| `parquetdownload` | 11 | 2 | 0.85 ████████ | 189 |
| `downloadengine/provider` | 9 | 0 | 1.00 ██████████ | 44 |
| `etl` | 5 | 1 | 0.83 ████████ | 99 |
| `server` | 4 | 0 | 1.00 ██████████ | 1 |
| `casefile` | 4 | 0 | 1.00 ██████████ | 18 |
| `datasourcemanager` | 4 | 2 | 0.67 ██████ | 44 |
| `provider` | 3 | 1 | 0.75 ███████ | 25 |
| `balance` | 2 | 1 | 0.67 ██████ | 10 |
| `datasource` | 2 | 3 | 0.40 ████ | 0 |
| `datasource/aws` | 2 | 3 | 0.40 ████ | 5 |
| `datasource/rpc` | 2 | 1 | 0.67 ██████ | 17 |
| `dbimport` | 2 | 1 | 0.67 ██████ | 110 |
| `graphintel` | 2 | 0 | 1.00 ██████████ | 8 |
| `investigation` | 2 | 1 | 0.67 ██████ | 13 |

**Key**: `parquetdownload` (11 out) and `api` (15 out) are the most coupled.

## 3. Cycle Detection

✅ No circular dependencies.

## 4. Interface Inventory

**16 interfaces**:

| Interface | Package | Methods |
|-----------|---------|--------|
| `csvBrowserEmailRequester` | `cryptodownload` | Request |
| `LogsSource` | `datasource` | ProbeSchema |
| `TransactionSource` | `datasource` | DiscoverTransactions |
| `FirstSeenResolver` | `downloadengine` | ResolveFirstSeen |
| `LinkVerifier` | `dunetools` | VerifyEmailLink |
| `Mailbox` | `dunetools` | WaitForVerificationLink |
| `SQLExecutor` | `writer` | ExecSQLJSON |
| `ReceiptSource` | `datasource` | Probe, Receipts |
| `exportSchemaExecutor` | `dbimport` | ExecContext, QueryContext |
| `Provider` | `downloadengine` | Name, Capabilities, Health |
| `BrowserClient` | `dunetools` | Register, VerifyEmail, LoginAndExtract |
| `Provider` | `provider` | Name, ProcessDirectory, ProcessFile |
| `LookupProvider` | `downloadengine` | Name, Capabilities, Health, ExecuteLookup |
| `ObjectProvider` | `downloadengine` | Name, Capabilities, Health, Estimate, ExecuteObject |
| `StreamingProvider` | `downloadengine` | Name, Capabilities, Health, Estimate, ExecuteStream |
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
| `api` | 324 | 17 | 5832 | 🔴 HIGH |
| `parquetdownload` | 221 | 13 | 3094 | 🔴 HIGH |
| `cryptodownload` | 805 | 2 | 2415 | 🔴 HIGH |
| `rpcmanager` | 131 | 5 | 786 | 🔴 HIGH |
| `etl` | 109 | 6 | 763 | 🔴 HIGH |
| `datasource/sqd` | 118 | 5 | 708 | 🔴 HIGH |
| `parser` | 92 | 6 | 644 | 🔴 HIGH |
| `downloadengine/provider` | 56 | 9 | 560 | 🔴 HIGH |
| `dbimport` | 139 | 3 | 556 | 🔴 HIGH |
| `downloadengine` | 254 | 1 | 508 | 🔴 HIGH |
| `datasourcemanager` | 57 | 6 | 399 | 🟡 MED |
| `analysis/duckdb` | 25 | 8 | 225 | 🟡 MED |
| `analyticsapi` | 32 | 6 | 224 | 🟡 MED |
| `dunetools` | 109 | 1 | 218 | 🟡 MED |
| `rules` | 28 | 6 | 196 | 🟢 LOW |

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
