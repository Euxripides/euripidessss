# Benchmark Report — V2.1-RC2

**Generated:** 2026-07-31T02:51:44Z  
**Environment:** windows/386 | 22 CPUs | GOMAXPROCS=22

## Results

| Test | Duration | Rows/s | MB/s | File | Pass |
|------|----------|--------|------|------|------|
| sim_L1_10K_count | 58ms | 173845 | 0.0 | 0.0 MB | ❌ |
| sim_L1_10K_group_from | 61ms | 163583 | 0.0 | 0.0 MB | ❌ |
| sim_L2_100K_count | 57ms | 1744960 | 0.0 | 0.0 MB | ✅ |
| sim_L2_100K_group_from | 94ms | 1068204 | 0.0 | 0.0 MB | ✅ |
| sim_L3_500K_count | 52ms | 9548107 | 0.0 | 0.0 MB | ✅ |
| sim_L3_500K_group_from | 199ms | 2515363 | 0.0 | 0.0 MB | ✅ |
| sim_parquet_500k | 73ms | 6878563 | 7.5 | 0.5 MB | ✅ |
| sim_crash_resume | 0s | 0 | 0.0 | 0.0 MB | ❌ |

## Summary

5/8 passed — NOT STABLE
