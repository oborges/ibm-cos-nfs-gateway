# COS NFS Gateway Benchmark Summary

- Run ID: `20260710T032522Z`
- Commit: `753e4f873147b83266631484c13b5f4825972eee`
- Profile: `standard`
- Mount: `/mnt/cos-nfs`

| Category | Benchmark | Status | MiB/s | IOPS | p50 ms | p95 ms | p99 ms | Error |
|---|---|---:|---:|---:|---:|---:|---:|---|
| frontend-write | seq-write-100MiB-direct0 | pass | 1960.784 | 1960.784 | 0.481 | 0.537 | 0.569 |  |
| frontend-write | seq-write-100MiB-direct1 | pass | 42.937 | 42.937 | 22.413 | 25.821 | 43.778 |  |
| frontend-write | seq-write-1GiB-direct0 | pass | 743.646 | 743.646 | 0.569 | 7.832 | 12.255 |  |
| frontend-write | seq-write-1GiB-direct1 | pass | 32.077 | 32.077 | 22.151 | 28.705 | 35.389 |  |
| sync | sync-durable-100MiB | pass |  |  |  |  |  |  |
| small-files | create-read-delete-100 | pass |  |  |  |  |  |  |
| small-files | create-read-delete-1000 | pass |  |  |  |  |  |  |
| mixed | concurrent-readers-writers | pass |  |  |  |  |  |  |
| mixed | large-writes-plus-small-reads | pass |  |  |  |  |  |  |
| mixed | dirty-file-reads-during-sync | pass |  |  |  |  |  |  |
