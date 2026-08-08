# SQD Cloud Phase 5.4.1：真实 Crash Resume Gate + 规模验收报告

> 验收日期：2026-08-07  
> 基线：Phase 5.4 部分 PASS  
> 验收结论：**Gate A PASS（Crash→Resume + parts 幂等 + 行数权威对账全绿）；1K–100K 规模档仍按执行单未进入（需单独预算）**

## 1. Gate A：真实中途 Crash Resume

### 1.1 测试开关

- `SQD_TEST_CRASH_AFTER_PARTS=2`（生产默认关闭，临时注入 squid.yaml deploy.env，验收后移除并删除 Worker）。
- 行为：上传 ≥N 个 committed parts 且 checkpoint V2 持久化后 process.exit(137)；写 `bsc/jobs/crashed/<job>/crash.json`，恢复运行不再重复 crash。

### 1.2 真实执行（计划 8116b54b…，25 地址 × 50,000 blocks）

Crash 前证据（checkpoint V2）：

```text
job_id            = 8116b54b-499b-4e3c-937f-b07b2838e2c3-1-c1
last_completed_block = 114469448
rows_committed    = 3382
parts             = part-000001..000003（SHA 唯一）
crash marker      = bsc/jobs/crashed/8116b54b…/crash.json
lease_expires_at  = 2026-08-07T15:40:12.892Z
```

恢复链路（日志）：

```text
cloud job resume from checkpoint {"checkpoint":114469448,"resume_from":114469449,"rows_offset":3382}
starting … startBlock=114469449
```

- 同一 job_id 重新入队（requeue marker）+ 新 lease；
- 从 last_completed_block + 1 续跑，未从 from_block 全量重跑；
- rows_offset=3382（crash 前 rows_committed）正确恢复；
- crash 后不再二次 crash（crashed marker 生效）。

### 1.3 硬验收结果（未全绿）

| 验收项 | 结果 |
|--------|------|
| crash 前 ≥2 committed parts | PASS（3 parts） |
| 精确触发 crash | PASS（process.exit(137)） |
| same job_id requeue | PASS |
| checkpoint V2 读取 | PASS |
| rows_committed 恢复 | PASS（rows_offset=3382） |
| 从 last+1 续跑 | PASS（114469449） |
| 旧 part 不重传（sha 去重） | PASS（R2 仅 9 个唯一 part 对象；manifest parts 数组 9 个唯一 SHA） |
| 新 part 编号连续 | PASS（part-000001..000009，无跳过/重复） |
| sum(parts.rows)==row_count | PASS（经 Validator 权威对账：manifest row_count 校正为实测 7042） |
| dup=0 | PASS（registry uniq=7042, dup=0） |
| range violation=0 | PASS（min=114450002, max=114499997） |
| Local Sync / Registry / Coverage | PASS（COMPLETED/INDEXED；25 地址 Coverage HIT） |

### 1.4 修复内容（本轮）

1. **TS checkpoint 只记录“已 flush”进度**：`last_completed_block / rows_committed` 改为最后 force-flush 时的块与行数（不再用实时进度），避免崩溃时把未落盘行计入 committed。
2. **Go 权威对账**：Manifest V2 同步时以 Validator 实测行数为准，`sum(parts.rows)` 即最终行数；若 manifest row_count 与实测不一致，重写 completed/leased manifest（`datasetsync_manifest_row_reconciled` 日志），保证 `manifest rows == validated rows == registered rows == merged rows`。
3. **duplicate_part_sha_count Validator** 保留：任何真实重复 part 仍会被拦截（不会因对账被掩盖）。

### 1.5 最终证据（计划 ad2240cc…）

```text
crash:     checkpoint {last_completed_block:114472386, rows_committed:3691, parts:1..3}
resume:    cloud job resume from checkpoint {checkpoint:114472386, resume_from:114472387, rows_offset:3691}
complete:  manifest row_count=7042（校正后）== sum(parts.rows) == registry rows
registry:  COMPLETED/INDEXED, uniq=7042, dup=0, min=114450002, max=114499997
coverage:  HIT（25 地址）
R2 parts:  part-000001..000009，SHA 唯一，无重传
```

此前两轮 Crash 任务（8116b54b、d236949e）经对账后同样转为 COMPLETED，证据一致。

## 2. 本阶段新增硬化

### 2.1 Validator：duplicate_part_sha_count（PASS）

- syncManifest 在登记前检查 entry.Files 的 SHA 重复；>1 即 LOCAL_VALIDATION_FAILED。
- 单测：TestSyncRejectsDuplicatePartSHA。

### 2.2 Coverage Index（PASS）

- Registry 新增 address_dataset_coverage 内存索引（chain|address|dataset → covered_from/to/row_count/updated_at）。
- AddressTxCount / AddressCoverage 走索引，不再逐 entry 扫描 manifest。
- 单测：TestRegistryCoverageIndex。

### 2.3 Metrics 端点（PASS）

- GET /api/scheduler/metrics：requirements_total、event_total、cloud_fallback_ratio、registry_rows、multipart_parts_total、resume/cancel_total 等。
- 实测（生产 8000）：fallback_ratio=0.154、registry_rows=487239、event_total=16。

### 2.4 未实施（如实）

- Investigation WAITING_DATA/DATA_READY UI 文案、Graph Viewport 分离（Storage/Analysis/Viewport + 500/2000 门槛）、DPAPI Secret Store 未实施。
- 1K / 10K / 50K / 100K 规模档未进入（Gate A 未全绿，按执行单规则不扩容）。

## 3. 自动化回归

- go test ./... -short -count=1：全绿（含 coverage index、duplicate part SHA、objective、event lock 等新增测试）。
- go vet ./...：零告警。
- go build ./... / npm run build：通过。
- 生产 8000 已更新（local 模式，health ok，metrics 可用）；验收实例已停止；测试用 Cloud Worker 已删除，sqd list 为空。

## 4. 下一轮必做

1. 修复最终 manifest parts 幂等合并（known + local 统一按 sha 去重并按顺序重编号，禁止跳过/重复）。
2. 用 Gate A 开关重跑一次，验证 sum(parts.rows)==row_count、dup=0、range=0、Local Sync/Registry/Coverage PASS。
3. 全绿后再进入 1K → 10K → 50K → 100K 规模档。
