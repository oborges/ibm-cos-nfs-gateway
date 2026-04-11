# Stress Test Summary

## Quick Reference

### Run Tests
```bash
# Quick test (5-10 min)
export MOUNT_PATH="/mnt/cos-nfs"
export QUICK_MODE=true
./scripts/mountpoint_stress_test.sh

# Full test (30-60 min)
export MOUNT_PATH="/mnt/cos-nfs"
./scripts/mountpoint_stress_test.sh
```

### View Results
```bash
cat stress-test-results-*/summary_report.txt
```

## Documentation Structure

### 📚 Complete Documentation Set

1. **[`README_STRESS_TEST.md`](../README_STRESS_TEST.md)** - Main overview
   - Test categories and modes
   - Expected performance metrics
   - Results analysis
   - Best practices

2. **[`docs/QUICK_MOUNTPOINT_TEST.md`](./QUICK_MOUNTPOINT_TEST.md)** - Quick start
   - TL;DR commands
   - Prerequisites
   - Common troubleshooting
   - FAQ

3. **[`docs/MOUNTPOINT_STRESS_TEST.md`](./MOUNTPOINT_STRESS_TEST.md)** - Comprehensive guide
   - Detailed test descriptions
   - All test categories
   - Advanced configuration
   - Monitoring strategies

4. **[`docs/STRESS_TEST_EXECUTION_GUIDE.md`](./STRESS_TEST_EXECUTION_GUIDE.md)** - Step-by-step
   - Execution checklist
   - Monitoring setup
   - Result analysis
   - Post-test actions

5. **[`scripts/mountpoint_stress_test.sh`](../scripts/mountpoint_stress_test.sh)** - Automated script
   - All tests automated
   - Quick and full modes
   - Automatic cleanup
   - JSON result output

## Test Categories

### 1. Sequential I/O
**Tests:** Large file writes, concurrent writes, variable block sizes, cold/warm reads

**Current Performance:**
- Write: 1-5 MB/s
- Read (cold): 10-50 MB/s
- Read (warm): 100-500 MB/s

**Target (MVP):**
- Write: 20-100 MB/s (10-50x improvement)
- Read: 50-200 MB/s

### 2. Random I/O
**Tests:** 4K random writes, reads, mixed workload

**Current Performance:**
- Write: 10-50 IOPS
- Read: 100-500 IOPS

**Target (MVP):**
- Write: 100-500 IOPS (10x improvement)
- Read: 500-2000 IOPS

### 3. Metadata Operations
**Tests:** File creation, directory listing, file deletion

**Current Performance:**
- Creation: 5-20 files/sec
- Listing: 100-1000 files/sec
- Deletion: 10-50 files/sec

**Target (MVP):**
- Creation: 50-200 files/sec (10x improvement)

### 4. NFS-Specific
**Tests:** File reopen patterns, attribute caching

**Current Performance:**
- Reopen cycles: High latency due to buffer churn

**Target (MVP):**
- Reopen cycles: Low latency with path-scoped sessions

## Key Bottlenecks Identified

### 1. Write Buffer Churn ⚠️ CRITICAL
**Problem:** Handle-scoped buffers destroyed on file reopen
**Impact:** Every small write triggers full-object GET+PUT
**Solution:** Path-scoped write sessions (staging architecture)

### 2. Full-Object Operations ⚠️ HIGH
**Problem:** No partial object updates
**Impact:** 16MB write requires downloading entire object first
**Solution:** Local staging layer with async sync

### 3. Metadata Overhead ⚠️ MEDIUM
**Problem:** High NFS round-trip latency
**Impact:** Slow file creation/deletion rates
**Solution:** Batch operations, attribute caching

### 4. Cache Inefficiency ⚠️ MEDIUM
**Problem:** Poor hit rates on small files
**Impact:** Repeated downloads of same data
**Solution:** Improved chunk-based caching (already implemented)

## Performance Improvement Roadmap

### Phase 1: Baseline (Current) ✅
- [x] Implement streaming reads with range requests
- [x] Add chunk-based caching
- [x] Fix critical bugs (truncate, stale handles, etc.)
- [x] Create stress testing framework
- **Result:** 1-5 MB/s write, 10-50 MB/s read

### Phase 2: MVP Staging (In Progress) 🔄
- [x] Design staging architecture
- [x] Create MVP implementation plan
- [x] Implement foundation (feature flags, config)
- [ ] Implement StagingManager and WriteSession
- [ ] Wire into NFS handler
- [ ] Test and validate
- **Target:** 20-100 MB/s write (10-50x improvement)

### Phase 3: Production Staging (Future) ⏳
- [ ] Add metadata persistence
- [ ] Implement crash recovery
- [ ] Add advanced sync strategies
- [ ] Optimize worker pools
- [ ] Production hardening
- **Target:** 100-500 MB/s write (network limited)

### Phase 4: Advanced Optimization (Future) ⏳
- [ ] Parallel multipart uploads
- [ ] Intelligent prefetching
- [ ] Advanced caching strategies
- [ ] Performance monitoring dashboard
- **Target:** 200-1000 MB/s read, 500-2000 IOPS

## Usage Workflow

### 1. Initial Baseline
```bash
# Run tests on current architecture
./scripts/mountpoint_stress_test.sh
mv stress-test-results-* baseline-current/
```

### 2. Enable Staging (After MVP)
```yaml
# configs/config.yaml
staging:
  enabled: true
  root_dir: "/var/lib/cos-nfs-gateway/staging"
  sync_threshold: 16777216  # 16MB
  sync_interval: 30s
```

### 3. Re-test with Staging
```bash
# Restart gateway with staging enabled
sudo systemctl restart cos-nfs-gateway

# Run tests again
./scripts/mountpoint_stress_test.sh
mv stress-test-results-* baseline-staging/
```

### 4. Compare Results
```bash
# Compare write performance
echo "Before Staging:"
jq -r '.jobs[0].write.bw_bytes / 1048576' \
    baseline-current/seq-write-large.json

echo "After Staging:"
jq -r '.jobs[0].write.bw_bytes / 1048576' \
    baseline-staging/seq-write-large.json

# Calculate improvement
# Expected: 10-50x improvement in write throughput
```

## Monitoring During Tests

### Essential Metrics

**Gateway Side:**
- CPU usage (should be < 80%)
- Memory usage (watch for leaks)
- Network throughput
- COS API calls (rate limits)

**Client Side:**
- NFS operations/sec
- Network utilization
- I/O wait time
- Cache hit ratio

**COS Side:**
- Request rate
- Bandwidth usage
- Error rate
- Latency

### Monitoring Commands

```bash
# Gateway monitoring
ssh gateway-host 'top -bn1 | head -20'
journalctl -u cos-nfs-gateway -f

# Client monitoring
iostat -x 5
nfsstat -c 5
iftop -i eth0

# Results monitoring
watch -n 5 'tail -20 stress-test-results-*/seq-write-large.log'
```

## Troubleshooting Quick Reference

| Issue | Check | Solution |
|-------|-------|----------|
| Tests hang | `nfsstat -c` | Remount NFS |
| Permission denied | NFS exports | Add client IP |
| Poor performance | Mount options | Use rsize/wsize=1048576 |
| Out of space | `df -h` | Clean up test files |
| Gateway errors | Logs | Check config, restart |
| Network issues | `ping gateway` | Check connectivity |

## Expected Test Duration

| Mode | Duration | File Sizes | Tests |
|------|----------|------------|-------|
| Quick | 5-10 min | 256MB | Basic coverage |
| Full | 30-60 min | 1GB | Comprehensive |
| Custom | Variable | Configurable | Selective |

## Result Files

```
stress-test-results-YYYYMMDD-HHMMSS/
├── summary_report.txt          # Human-readable summary
├── seq-write-large.json        # Sequential write (large file)
├── seq-write-concurrent.json   # Concurrent writes
├── seq-write-4k.json          # 4K block writes
├── seq-write-64k.json         # 64K block writes
├── seq-write-256k.json        # 256K block writes
├── seq-write-1m.json          # 1M block writes
├── seq-read-cold.json         # Cold cache reads
├── seq-read-warm.json         # Warm cache reads
├── rand-write.json            # Random writes
├── rand-read.json             # Random reads
├── rand-mixed.json            # Mixed random I/O
├── metadata-create.json       # File creation
├── metadata-list.json         # Directory listing
├── metadata-delete.json       # File deletion
└── nfs-reopen.json           # Reopen patterns
```

## Success Criteria

### Baseline Tests (Current)
- ✅ All tests complete without errors
- ✅ Results consistent across multiple runs
- ✅ No gateway crashes or hangs
- ✅ Performance matches expected baseline

### MVP Staging Tests (Target)
- ✅ 10-50x improvement in write throughput
- ✅ No data loss or corruption
- ✅ Read-after-write consistency maintained
- ✅ Graceful handling of failures

### Production Readiness
- ✅ Sustained performance under load
- ✅ Crash recovery without data loss
- ✅ Monitoring and alerting functional
- ✅ Documentation complete

## Next Steps

1. **Run Baseline Tests** ← START HERE
   - Execute quick test
   - Document results
   - Identify bottlenecks

2. **Complete MVP Implementation**
   - Finish StagingManager
   - Wire into NFS handler
   - Add unit tests

3. **Test MVP**
   - Enable staging
   - Run stress tests
   - Measure improvement

4. **Iterate and Optimize**
   - Tune configuration
   - Address issues
   - Re-test

5. **Production Deployment**
   - Gradual rollout
   - Monitor metrics
   - Validate performance

## Resources

- **Main README:** [`README_STRESS_TEST.md`](../README_STRESS_TEST.md)
- **Quick Start:** [`docs/QUICK_MOUNTPOINT_TEST.md`](./QUICK_MOUNTPOINT_TEST.md)
- **Full Guide:** [`docs/MOUNTPOINT_STRESS_TEST.md`](./MOUNTPOINT_STRESS_TEST.md)
- **Execution Guide:** [`docs/STRESS_TEST_EXECUTION_GUIDE.md`](./STRESS_TEST_EXECUTION_GUIDE.md)
- **Staging Architecture:** [`docs/STAGING_ARCHITECTURE_COMPLETE.md`](./STAGING_ARCHITECTURE_COMPLETE.md)
- **MVP Plan:** [`docs/MVP_IMPLEMENTATION_PLAN.md`](./MVP_IMPLEMENTATION_PLAN.md)

---

**Ready to test?** Run: `export MOUNT_PATH="/mnt/cos-nfs" && export QUICK_MODE=true && ./scripts/mountpoint_stress_test.sh`