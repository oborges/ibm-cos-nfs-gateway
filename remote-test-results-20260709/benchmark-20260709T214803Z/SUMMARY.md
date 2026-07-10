# COS NFS Gateway Benchmark Summary

- Run ID: `20260709T214803Z`
- Commit: `753e4f873147b83266631484c13b5f4825972eee`
- Profile: `quick`
- Mount: `/mnt/cos-nfs`

| Category | Benchmark | Status | MiB/s | IOPS | p50 ms | p95 ms | p99 ms | Error |
|---|---|---:|---:|---:|---:|---:|---:|---|
| backpressure | below-high-watermark | pass | 26.423 |  |  |  |  |  |
| backpressure | above-high-watermark | pass | 225.840 |  |  |  |  |  |
| backpressure | above-critical-watermark | pass | 77.607 |  |  |  |  |  |
| backpressure | sync-drain-releases-pressure | pass |  |  |  |  |  |  |
| backpressure | block-mode | skip |  |  |  |  |  | mode-specific validation requires restarting gateway with block mode |
| backpressure | fail-fast-mode | skip |  |  |  |  |  | mode-specific validation requires restarting gateway with fail_fast mode |
