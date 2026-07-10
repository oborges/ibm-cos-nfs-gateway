# COS NFS Gateway Benchmark Summary

- Run ID: `20260709T213749Z`
- Commit: `753e4f873147b83266631484c13b5f4825972eee`
- Profile: `standard`
- Mount: `/mnt/cos-nfs`

| Category | Benchmark | Status | MiB/s | IOPS | p50 ms | p95 ms | p99 ms | Error |
|---|---|---:|---:|---:|---:|---:|---:|---|
| frontend-write | seq-write-100MiB-direct0 | pass | 2083.333 | 2083.333 | 0.449 | 0.510 | 0.518 |  |
| frontend-write | seq-write-100MiB-direct1 | pass | 45.998 | 45.998 | 21.365 | 23.462 | 25.035 |  |
| frontend-write | seq-write-1GiB-direct0 | pass | 823.150 | 823.151 | 1.188 | 2.146 | 5.014 |  |
| frontend-write | seq-write-1GiB-direct1 | pass | 40.539 | 40.540 | 23.724 | 28.180 | 32.375 |  |
| sync | sync-durable-100MiB | pass |  |  |  |  |  |  |
| read | cold-read-from-cos | pass | 14.770 | 14.770 | 64.225 | 115.868 | 166.724 |  |
| read | warm-read-local-cache | pass | 45.182 | 45.182 | 21.103 | 34.341 | 41.681 |  |
| read | partial-range-read | pass | 53.511 | 53.512 | 16.908 | 28.180 | 30.015 |  |
| read | random-4k-read | pass | 0.390 | 99.897 | 9.765 | 13.435 | 16.581 |  |
| read | large-sequential-read | pass | 72.893 | 18.223 | 54.264 | 64.750 | 77.070 |  |
| backpressure | below-high-watermark | skip |  |  |  |  |  | requires --allow-backpressure and a gateway configured with test watermarks |
| backpressure | above-high-watermark | skip |  |  |  |  |  | requires --allow-backpressure and a gateway configured with test watermarks |
| backpressure | above-critical-watermark | skip |  |  |  |  |  | requires --allow-backpressure and a gateway configured with test watermarks |
| backpressure | block-mode | skip |  |  |  |  |  | requires --allow-backpressure and a gateway configured with test watermarks |
| backpressure | fail-fast-mode | skip |  |  |  |  |  | requires --allow-backpressure and a gateway configured with test watermarks |
| backpressure | sync-drain-releases-pressure | skip |  |  |  |  |  | requires --allow-backpressure and a gateway configured with test watermarks |
| small-files | create-read-delete-100 | fail |  |  |  |  |  | [Errno 5] Input/output error: '/mnt/cos-nfs/.benchmark-20260709T213749Z/small-files-100/file-000084.txt' |
| small-files | create-read-delete-1000 | pass |  |  |  |  |  |  |
| crash-safety | crash-before-sync | skip |  |  |  |  |  | requires --allow-crash and --gateway-command |
| crash-safety | crash-during-sync | skip |  |  |  |  |  | requires --allow-crash and --gateway-command |
| crash-safety | crash-during-multipart | skip |  |  |  |  |  | requires --allow-crash and --gateway-command |
| mixed | concurrent-readers-writers | pass |  |  |  |  |  |  |
| mixed | large-writes-plus-small-reads | pass |  |  |  |  |  |  |
| mixed | dirty-file-reads-during-sync | pass |  |  |  |  |  |  |
