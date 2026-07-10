# COS NFS Gateway Benchmark Summary

- Run ID: `20260710T005733Z`
- Commit: `753e4f873147b83266631484c13b5f4825972eee`
- Profile: `standard`
- Mount: `/mnt/cos-nfs`

| Category | Benchmark | Status | MiB/s | IOPS | p50 ms | p95 ms | p99 ms | Error |
|---|---|---:|---:|---:|---:|---:|---:|---|
| small-files | create-read-delete-100 | pass |  |  |  |  |  |  |
| small-files | create-read-delete-1000 | pass |  |  |  |  |  |  |
| mixed | concurrent-readers-writers | pass |  |  |  |  |  |  |
| mixed | large-writes-plus-small-reads | pass |  |  |  |  |  |  |
| mixed | dirty-file-reads-during-sync | pass |  |  |  |  |  |  |
