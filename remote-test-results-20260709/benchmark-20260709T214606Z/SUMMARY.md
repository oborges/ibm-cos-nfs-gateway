# COS NFS Gateway Benchmark Summary

- Run ID: `20260709T214606Z`
- Commit: `753e4f873147b83266631484c13b5f4825972eee`
- Profile: `quick`
- Mount: `/mnt/cos-nfs`

| Category | Benchmark | Status | MiB/s | IOPS | p50 ms | p95 ms | p99 ms | Error |
|---|---|---:|---:|---:|---:|---:|---:|---|
| small-files | create-read-delete-100 | fail |  |  |  |  |  | [Errno 5] Input/output error: '/mnt/cos-nfs/.benchmark-20260709T214606Z/small-files-100/file-000076.txt' |
