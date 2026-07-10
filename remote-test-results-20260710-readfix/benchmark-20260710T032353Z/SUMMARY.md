# COS NFS Gateway Benchmark Summary

- Run ID: `20260710T032353Z`
- Commit: `753e4f873147b83266631484c13b5f4825972eee`
- Profile: `standard`
- Mount: `/mnt/cos-nfs`

| Category | Benchmark | Status | MiB/s | IOPS | p50 ms | p95 ms | p99 ms | Error |
|---|---|---:|---:|---:|---:|---:|---:|---|
| read | cold-read-from-cos | pass | 57.143 | 57.143 | 5.931 | 115.868 | 160.432 |  |
| read | warm-read-local-cache | pass | 49.593 | 49.593 | 11.993 | 46.399 | 48.497 |  |
| read | partial-range-read | pass | 247.104 | 247.104 | 4.080 | 8.716 | 10.027 |  |
| read | random-4k-read | pass | 17.072 | 4370.588 | 0.202 | 0.338 | 0.717 |  |
| read | large-sequential-read | pass | 209.149 | 52.288 | 17.695 | 27.132 | 34.865 |  |
