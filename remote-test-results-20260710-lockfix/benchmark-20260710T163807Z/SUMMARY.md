# COS NFS Gateway Benchmark Summary

- Run ID: `20260710T163807Z`
- Commit: `753e4f873147b83266631484c13b5f4825972eee`
- Profile: `standard`
- Mount: `/mnt/cos-nfs`

| Category | Benchmark | Status | MiB/s | IOPS | p50 ms | p95 ms | p99 ms | Error |
|---|---|---:|---:|---:|---:|---:|---:|---|
| read | cold-read-from-cos | pass | 47.284 | 47.285 | 6.062 | 141.558 | 214.958 |  |
| read | warm-read-local-cache | pass | 49.902 | 49.903 | 11.469 | 45.351 | 46.924 |  |
| read | partial-range-read | pass | 217.687 | 217.687 | 5.341 | 8.585 | 11.993 |  |
| read | random-4k-read | pass | 17.010 | 4354.522 | 0.204 | 0.334 | 0.676 |  |
| read | large-sequential-read | pass | 208.809 | 52.202 | 18.743 | 23.462 | 32.899 |  |
| small-files | create-read-delete-100 | pass |  |  |  |  |  |  |
| small-files | create-read-delete-1000 | pass |  |  |  |  |  |  |
| mixed | concurrent-readers-writers | pass |  |  |  |  |  |  |
| mixed | large-writes-plus-small-reads | pass |  |  |  |  |  |  |
| mixed | dirty-file-reads-during-sync | pass |  |  |  |  |  |  |
