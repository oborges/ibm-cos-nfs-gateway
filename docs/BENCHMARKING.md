# Enterprise Gateway Benchmarking

The IBM COS NFS Gateway includes a highly unified validation dashboard scripting suite (`scripts/run_stress_tests.sh`) that natively tracks Hardware CPU variables and Local Memory usage dynamically scaling across execution profiles.

## Running the Validations
Execute the primary benchmark sequence locally across your RedHat machine checking out limits natively:
```bash
sudo ./scripts/run_stress_tests.sh
```

## Tracked Metrics
The Dashboard analyzes cache capabilities comprehensively bypassing typical FIO string abstractions:
- **Sequential OS Cache Scaling**: `cache_throughput`
- **Multi-Gigabyte Progressive S3 Streaming**: `massive_s3_chunk_burst`
- **Native Memory Mapped Concurrent Profiling**: `mmap_concurrent_randrw`

At execution bounds finish, it will drop natively directly tracking a compiled `DASHBOARD_STRESS.md` detailing Hardware Constraints (Max RAM/Active CPU limits) evaluating Gateway capacities accurately.
