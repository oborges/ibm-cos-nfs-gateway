# Stress Test Execution Guide

## Overview

This guide provides step-by-step instructions for executing performance stress tests on your IBM Cloud COS NFS Gateway mountpoint. Use this to establish baseline metrics before implementing the staging architecture.

## Prerequisites Checklist

- [ ] NFS Gateway deployed and running
- [ ] NFS share mounted on test client
- [ ] Testing tools installed (fio, jq, bc)
- [ ] Write access to mount point verified
- [ ] Network connectivity confirmed
- [ ] Sufficient disk space available

## Step-by-Step Execution

### Step 1: Prepare Test Environment

```bash
# Set your mount path
export MOUNT_PATH="/mnt/cos-nfs"

# Verify mount is active
if mountpoint -q "$MOUNT_PATH"; then
    echo "✓ Mount point is active"
else
    echo "✗ Mount point not found - please mount first"
    exit 1
fi

# Check available space
df -h "$MOUNT_PATH"

# Verify write access
if touch "${MOUNT_PATH}/test.txt" 2>/dev/null; then
    rm "${MOUNT_PATH}/test.txt"
    echo "✓ Write access confirmed"
else
    echo "✗ No write access - check permissions"
    exit 1
fi
```

### Step 2: Install Required Tools

```bash
# Ubuntu/Debian
sudo apt-get update
sudo apt-get install -y fio jq bc sysstat iotop

# Verify installations
fio --version && echo "✓ FIO installed"
jq --version && echo "✓ jq installed"
bc --version && echo "✓ bc installed"
```

### Step 3: Run Quick Test (Recommended First)

```bash
# Navigate to project directory
cd /path/to/s3fsibmcloud

# Run quick test (5-10 minutes)
export QUICK_MODE=true
./scripts/mountpoint_stress_test.sh
```

**What happens:**
- Creates test directory: `${MOUNT_PATH}/stress-test-TIMESTAMP/`
- Runs 5 test categories
- Generates results in: `stress-test-results-TIMESTAMP/`
- Cleans up test files automatically

### Step 4: Monitor Test Execution

Open multiple terminal windows:

**Terminal 1: Test Execution**
```bash
./scripts/mountpoint_stress_test.sh
```

**Terminal 2: I/O Monitoring**
```bash
# Monitor disk I/O
iostat -x 5

# Look for:
# - %util (disk utilization)
# - r/s, w/s (read/write operations per second)
# - rMB/s, wMB/s (throughput)
```

**Terminal 3: Network Monitoring**
```bash
# Monitor network traffic
sudo iftop -i eth0

# Or use:
watch -n 1 'ifconfig eth0 | grep "RX\|TX"'
```

**Terminal 4: Gateway Logs**
```bash
# Monitor gateway logs
tail -f /var/log/cos-nfs-gateway/gateway.log

# Or via journalctl:
journalctl -u cos-nfs-gateway -f
```

### Step 5: Review Results

```bash
# Find results directory
RESULTS_DIR=$(ls -td stress-test-results-* | head -1)
echo "Results in: $RESULTS_DIR"

# View summary report
cat "${RESULTS_DIR}/summary_report.txt"

# View detailed FIO results
ls -lh "${RESULTS_DIR}"/*.json
```

### Step 6: Analyze Performance

```bash
# Extract key metrics
echo "=== Performance Summary ==="

# Sequential write
WRITE_BW=$(jq -r '.jobs[0].write.bw_bytes / 1048576' \
    "${RESULTS_DIR}/seq-write-large.json" 2>/dev/null)
echo "Sequential Write: ${WRITE_BW} MB/s"

# Sequential read (cold)
READ_BW=$(jq -r '.jobs[0].read.bw_bytes / 1048576' \
    "${RESULTS_DIR}/seq-read-cold.json" 2>/dev/null)
echo "Sequential Read (cold): ${READ_BW} MB/s"

# Random write IOPS
RAND_IOPS=$(jq -r '.jobs[0].write.iops' \
    "${RESULTS_DIR}/rand-write.json" 2>/dev/null)
echo "Random Write IOPS: ${RAND_IOPS}"
```

### Step 7: Save Baseline Results

```bash
# Create baseline directory
mkdir -p baseline-results

# Copy results
cp -r "${RESULTS_DIR}" baseline-results/

# Add metadata
cat > baseline-results/metadata.txt << EOF
Test Date: $(date)
Gateway Version: $(cat VERSION 2>/dev/null || echo "unknown")
Kernel: $(uname -r)
NFS Version: $(nfsstat -m | grep vers | head -1)
Mount Options: $(mount | grep "$MOUNT_PATH")
Network: $(ip addr show | grep "inet " | grep -v 127.0.0.1)
EOF

echo "✓ Baseline saved to: baseline-results/"
```

## Full Test Execution (Optional)

After quick test, run comprehensive test:

```bash
# Run full test (30-60 minutes)
unset QUICK_MODE
./scripts/mountpoint_stress_test.sh

# Save as full baseline
mkdir -p baseline-results-full
cp -r stress-test-results-* baseline-results-full/
```

## Expected Results (Current Architecture)

### Quick Test Results
```
Sequential Write (1GB):     1-5 MB/s
Sequential Read (cold):     10-50 MB/s
Sequential Read (warm):     100-500 MB/s
Random Write (4K):          10-50 IOPS
Random Read (4K):           100-500 IOPS
File Creation:              5-20 files/sec
File Deletion:              10-50 files/sec
```

### Performance Indicators

**Good Signs:**
- ✅ Sequential write > 3 MB/s
- ✅ Sequential read > 20 MB/s
- ✅ No errors in gateway logs
- ✅ Consistent performance across runs

**Warning Signs:**
- ⚠️ Sequential write < 1 MB/s
- ⚠️ High latency (>1000ms)
- ⚠️ Errors in gateway logs
- ⚠️ Tests timing out

**Critical Issues:**
- ❌ Tests failing to complete
- ❌ Mount becoming unresponsive
- ❌ Gateway crashes
- ❌ Data corruption

## Troubleshooting

### Issue: Tests Hang

```bash
# Check NFS mount status
nfsstat -c

# Check for stale file handles
ls -la "$MOUNT_PATH"

# Check gateway status
systemctl status cos-nfs-gateway

# If needed, remount
sudo umount -f "$MOUNT_PATH"
sudo mount -t nfs4 -o rw,hard,rsize=1048576,wsize=1048576 \
    <gateway-ip>:/<bucket> "$MOUNT_PATH"
```

### Issue: Permission Denied

```bash
# Check NFS export configuration
# On gateway host:
cat /etc/exports

# Should include your client IP
# Example: /bucket 192.168.1.0/24(rw,sync,no_subtree_check)

# Restart NFS server if changed
sudo systemctl restart nfs-server
```

### Issue: Poor Performance

```bash
# Check mount options
mount | grep "$MOUNT_PATH"

# Should have:
# - rsize=1048576 (1MB read buffer)
# - wsize=1048576 (1MB write buffer)
# - hard (don't timeout)
# - vers=4.1 (NFS v4.1)

# Remount with optimal options
sudo umount "$MOUNT_PATH"
sudo mount -t nfs4 -o rw,hard,rsize=1048576,wsize=1048576,vers=4.1 \
    <gateway-ip>:/<bucket> "$MOUNT_PATH"
```

### Issue: Out of Space

```bash
# Check available space
df -h "$MOUNT_PATH"

# Clean up old test files
rm -rf "${MOUNT_PATH}"/stress-test-*

# Check COS bucket quota
# (via IBM Cloud Console or CLI)
```

## Post-Test Actions

### 1. Document Results

Create a summary document:

```bash
cat > test-summary-$(date +%Y%m%d).md << EOF
# Stress Test Results - $(date)

## Environment
- Gateway Version: $(cat VERSION 2>/dev/null || echo "unknown")
- Kernel: $(uname -r)
- NFS Version: $(nfsstat -m | grep vers | head -1)

## Performance Metrics
- Sequential Write: [VALUE] MB/s
- Sequential Read: [VALUE] MB/s
- Random Write: [VALUE] IOPS
- Random Read: [VALUE] IOPS

## Observations
- [Add notes about performance]
- [Any errors or warnings]
- [Bottlenecks identified]

## Next Steps
- [Actions to take]
EOF
```

### 2. Share Results

```bash
# Create shareable archive
tar -czf stress-test-results-$(date +%Y%m%d).tar.gz \
    stress-test-results-*/ \
    baseline-results/ \
    test-summary-*.md

# Upload to shared location or attach to issue
```

### 3. Plan Improvements

Based on results, prioritize:

1. **Critical Issues** (< 1 MB/s write)
   - Enable staging architecture
   - Tune write buffer configuration
   - Check network/gateway resources

2. **Performance Optimization** (1-5 MB/s write)
   - Implement staging MVP
   - Tune cache configuration
   - Optimize sync thresholds

3. **Fine Tuning** (> 5 MB/s write)
   - Adjust worker pool sizes
   - Tune retry policies
   - Optimize chunk sizes

## Comparison Testing

After implementing improvements:

```bash
# 1. Save current results as baseline
mv stress-test-results-* baseline-before/

# 2. Make changes (enable staging, tune config, etc.)

# 3. Run tests again
./scripts/mountpoint_stress_test.sh

# 4. Compare results
echo "=== Performance Comparison ==="
echo "Before:"
cat baseline-before/*/summary_report.txt | grep "Write Bandwidth"
echo "After:"
cat stress-test-results-*/summary_report.txt | grep "Write Bandwidth"

# 5. Calculate improvement
# (Use spreadsheet or script to calculate percentage gains)
```

## Continuous Testing

Set up periodic testing:

```bash
# Create cron job for weekly tests
cat > /etc/cron.weekly/nfs-stress-test << 'EOF'
#!/bin/bash
export MOUNT_PATH="/mnt/cos-nfs"
export QUICK_MODE=true
cd /path/to/s3fsibmcloud
./scripts/mountpoint_stress_test.sh
# Email or upload results
EOF

chmod +x /etc/cron.weekly/nfs-stress-test
```

## Next Steps

1. ✅ Complete stress test execution
2. ✅ Document baseline performance
3. ✅ Identify bottlenecks
4. ⏳ Implement staging architecture (MVP)
5. ⏳ Re-test with staging enabled
6. ⏳ Measure performance improvement
7. ⏳ Tune configuration based on results
8. ⏳ Deploy to production

## References

- **Quick Start**: [`docs/QUICK_MOUNTPOINT_TEST.md`](./QUICK_MOUNTPOINT_TEST.md)
- **Detailed Guide**: [`docs/MOUNTPOINT_STRESS_TEST.md`](./MOUNTPOINT_STRESS_TEST.md)
- **Main README**: [`README_STRESS_TEST.md`](../README_STRESS_TEST.md)
- **Staging Architecture**: [`docs/STAGING_ARCHITECTURE_COMPLETE.md`](./STAGING_ARCHITECTURE_COMPLETE.md)
- **MVP Plan**: [`docs/MVP_IMPLEMENTATION_PLAN.md`](./MVP_IMPLEMENTATION_PLAN.md)

## Support

For issues or questions:
1. Check troubleshooting section above
2. Review gateway logs
3. Verify mount and network status
4. Check project documentation
5. Open GitHub issue with test results

---

**Ready to start?** Follow Step 1 above to begin testing!