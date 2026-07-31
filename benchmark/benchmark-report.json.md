# Benchmark Report — V2.1-RC2

**Generated:** 2026-07-31T02:57:33Z  
**Environment:** windows/386 | 22 CPUs | GOMAXPROCS=22

## Results

| Test | Duration | Rows/s | MB/s | File | Pass |
|------|----------|--------|------|------|------|
| parquet_write | 87ms | 11506646 | 12.1 | 1.1 MB | ✅ |
| duckdb_count_all | 53ms | 18961588 | 0.0 | 0.0 MB | ✅ |
| duckdb_group_by | 64ms | 15554107 | 0.0 | 0.0 MB | ✅ |
| pipeline_addresses | 19ms | 5306194 | 0.0 | 0.0 MB | ✅ |
| sim_L1_10K_count | 53ms | 190386 | 0.0 | 0.0 MB | ❌ |
| sim_L1_10K_group_from | 62ms | 161628 | 0.0 | 0.0 MB | ❌ |
| sim_L2_100K_count | 55ms | 1818542 | 0.0 | 0.0 MB | ✅ |
| sim_L2_100K_group_from | 87ms | 1149674 | 0.0 | 0.0 MB | ✅ |
| sim_L3_500K_count | 55ms | 1819720 | 0.0 | 0.0 MB | ✅ |
| sim_L3_500K_group_from | 88ms | 1130172 | 0.0 | 0.0 MB | ✅ |
| sim_parquet_500k | 74ms | 6735767 | 7.4 | 0.5 MB | ✅ |
| sim_crash_resume | 0s | 0 | 0.0 | 0.0 MB | ❌ |

## Summary

9/12 passed — NOT STABLE
