# Mountpoint Stress Testing

Complete performance testing suite for the IBM Cloud COS NFS Gateway mountpoint.

## Quick Start

```bash
# 1. Mount your NFS share
export MOUNT_PATH="/mnt/cos-nfs"
sudo mount -t nfs4 -o rw,hard,rsize=1048576,wsize=1048576 \
    <gateway-ip>:/<bucket> ${MOUNT_PATH}

# 2. Install tools
sudo apt-get install -y fio jq bc

# 3. Run quick test (5-10 minutes)
export QUICK_MODE=true
./scripts/mountpoint_stress_test.sh

# 4. View results
cat stress-test-results-*/summary_report.txt
```

## Documentation

### Quick Start Guide
📄 **[`docs/QUICK_MOUNTPOINT_TEST.md`](docs/QUICK_MOUNTPOINT_TEST.md)**
- Fast setup and execution
- Common troubleshooting
- Result interpretation
- **Start here for immediate testing**

### Comprehensive Guide
📄 **[`docs/MOUNTPOINT_STRESS_TEST.md`](docs/MOUNTPOINT_STRESS_TEST.md)**
- Detailed test descriptions
- Advanced configuration
- Performance tuning
- Monitoring strategies

## Test Categories

### 1. Sequential I/O
- **Write**: Large files, concurrent streams, variable block sizes
- **Read**: Cold cache, warm cache, streaming patterns
- **Metrics**: Bandwidth (MB/s), latency

### 2. Random I/O
- **Write**: 4K random writes
- **Read**: 4K random reads
- **Mixed**: 70% read, 30% write
- **Metrics**: IOPS, latency distribution

### 3. Metadata Operations
- **Creation**: File creation rate
- **Listing**: Directory traversal speed
- **Deletion**: File deletion rate
- **Metrics**: Operations per second

### 4. NFS-Specific
- **Reopen patterns**: Simulates typical NFS client behavior
- **Attribute caching**: stat() performance
- **Metrics**: Operation latency

## Test Modes

### Quick Mode (Default for First Run)
```bash
export QUICK_MODE=true
./scripts/mountpoint_stress_test.sh
```
- Duration: 5-10 minutes
- File sizes: 256MB
- Test duration: 30 seconds
- Files: 1,000

### Full Mode (Comprehensive)
```bash
./scripts/mountpoint_stress_test.sh
```
- Duration: 30-60 minutes
- File sizes: 1GB
- Test duration: 60 seconds
- Files: 10,000

## Expected Performance

### Current Baseline (Before Staging)
| Metric | Current | Target (MVP) | Ultimate Goal |
|--------|---------|--------------|---------------|
| Sequential Write | 1-5 MB/s | 20-100 MB/s | 100-500 MB/s |
| Sequential Read | 10-50 MB/s | 50-200 MB/s | 200-1000 MB/s |
| Random Write IOPS | 10-50 | 100-500 | 500-2000 |
| Random Read IOPS | 100-500 | 500-2000 | 2000-10000 |
| File Creation | 5-20/sec | 50-200/sec | 200-1000/sec |

### Performance Bottlenecks Identified
1. **Write Buffer Churn**: Handle-scoped buffers destroyed on reopen
2. **Full-Object Operations**: Every write triggers GET+PUT cycle
3. **Metadata Overhead**: High NFS round-trip latency
4. **Cache Inefficiency**: Poor hit rates on small files

## Results Analysis

### Output Structure
```
stress-test-results-YYYYMMDD-HHMMSS/
├── summary_report.txt          # Human-readable summary
├── seq-write-large.json        # FIO JSON output
├── seq-write-large.log         # FIO detailed log
├── seq-read-cold.json
├── rand-write.json
├── metadata-create.json
└── ...
```

### Key Metrics

**Bandwidth (MB/s)**
- Sequential operations
- Higher = better
- Network and disk limited

**IOPS (Operations/Second)**
- Random operations
- Higher = better
- Latency and overhead limited

**Latency (milliseconds)**
- Per-operation time
- Lower = better
- p50 (median), p95, p99 (tail latency)

## Monitoring

### During Tests
```bash
# Terminal 1: Run tests
./scripts/mountpoint_stress_test.sh

# Terminal 2: Monitor I/O
iostat -x 5

# Terminal 3: Monitor network
sudo iftop -i eth0

# Terminal 4: Monitor gateway
tail -f /var/log/cos-nfs-gateway/gateway.log
```

### Gateway Metrics
```bash
# If Prometheus enabled
curl http://localhost:9090/metrics | grep nfs

# Key metrics:
# - nfs_operations_total
# - nfs_operation_duration_seconds
# - cos_requests_total
# - cache_hit_ratio
```

## Troubleshooting

### Tests Fail to Start
```bash
# Check mount
mountpoint -q $MOUNT_PATH || echo "Not mounted"

# Check tools
fio --version
jq --version

# Check permissions
touch ${MOUNT_PATH}/test.txt && rm ${MOUNT_PATH}/test.txt
```

### Poor Performance
```bash
# Check mount options
mount | grep $MOUNT_PATH

# Should have: rsize=1048576,wsize=1048576

# Check network latency
ping -c 10 <gateway-ip>

# Check gateway resources
ssh <gateway-host> 'top -bn1'
```

### Tests Hang
```bash
# Check NFS stats
nfsstat -c

# Check for stale handles
ls -la $MOUNT_PATH

# Remount if needed
sudo umount -f $MOUNT_PATH
sudo mount -t nfs4 ... $MOUNT_PATH
```

## Comparison Testing

### Before/After Staging Implementation

```bash
# 1. Run baseline tests (current architecture)
./scripts/mountpoint_stress_test.sh
mv stress-test-results-* baseline-results/

# 2. Enable staging in gateway config
# Edit configs/config.yaml:
#   staging:
#     enabled: true

# 3. Restart gateway
sudo systemctl restart cos-nfs-gateway

# 4. Run tests again
./scripts/mountpoint_stress_test.sh
mv stress-test-results-* staging-results/

# 5. Compare results
diff baseline-results/summary_report.txt \
     staging-results/summary_report.txt
```

## Integration with CI/CD

### Automated Testing
```yaml
# .github/workflows/performance-test.yml
name: Performance Test
on: [push, pull_request]

jobs:
  stress-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      - name: Setup test environment
        run: |
          sudo apt-get install -y fio jq bc
          # Mount NFS, etc.
      - name: Run stress tests
        run: |
          export QUICK_MODE=true
          ./scripts/mountpoint_stress_test.sh
      - name: Upload results
        uses: actions/upload-artifact@v2
        with:
          name: stress-test-results
          path: stress-test-results-*/
```

## Best Practices

### 1. Establish Baseline
- Run tests on clean system
- Document environment (kernel, NFS version, network)
- Save results for comparison

### 2. Consistent Environment
- Same hardware/VM specs
- Same network conditions
- Same gateway configuration

### 3. Multiple Runs
- Run tests 3-5 times
- Calculate average and standard deviation
- Identify outliers

### 4. Incremental Changes
- Change one variable at a time
- Test after each change
- Document what changed

### 5. Monitor Resources
- Gateway CPU/memory
- Network utilization
- COS API rate limits

## Advanced Usage

### Custom Test Configuration
```bash
# Override test parameters
export MOUNT_PATH="/custom/path"
export QUICK_MODE=false
export LARGE_FILE_SIZE="2G"
export TEST_DURATION="120"

./scripts/mountpoint_stress_test.sh
```

### Selective Tests
```bash
# Edit script to comment out unwanted tests
# Or create custom test script based on template
```

### Continuous Monitoring
```bash
# Run tests periodically
while true; do
    ./scripts/mountpoint_stress_test.sh
    sleep 3600  # Every hour
done
```

## Contributing

### Adding New Tests
1. Add test function to `scripts/mountpoint_stress_test.sh`
2. Follow naming convention: `run_<category>_tests()`
3. Save results to `$RESULTS_DIR/<test-name>.json`
4. Update documentation

### Reporting Issues
Include:
- Test mode (quick/full)
- Environment details (OS, kernel, NFS version)
- Gateway version and configuration
- Complete test output
- Gateway logs during test

## References

- **FIO**: https://fio.readthedocs.io/
- **NFS Performance**: https://wiki.linux-nfs.org/wiki/index.php/Performance
- **IBM Cloud COS**: https://cloud.ibm.com/docs/cloud-object-storage
- **Project Documentation**: [`docs/`](docs/)

## Support

For questions or issues:
1. Check [`docs/QUICK_MOUNTPOINT_TEST.md`](docs/QUICK_MOUNTPOINT_TEST.md) FAQ
2. Review gateway logs
3. Check network connectivity
4. Verify mount options
5. Open GitHub issue with details

---

**Ready to test?** Start with the [Quick Start Guide](docs/QUICK_MOUNTPOINT_TEST.md)!