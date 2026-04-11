# Quick Mountpoint Stress Test Guide

## TL;DR - Run Tests Now

```bash
# Quick test (5-10 minutes)
export MOUNT_PATH="/mnt/cos-nfs"
export QUICK_MODE=true
./scripts/mountpoint_stress_test.sh

# Full test (30-60 minutes)
export MOUNT_PATH="/mnt/cos-nfs"
./scripts/mountpoint_stress_test.sh
```

## Prerequisites

### 1. Mount the NFS Share

```bash
# Set your gateway IP and bucket name
GATEWAY_IP="192.168.1.100"
BUCKET_NAME="my-bucket"
MOUNT_PATH="/mnt/cos-nfs"

# Create mount point
sudo mkdir -p $MOUNT_PATH

# Mount with optimal options
sudo mount -t nfs4 -o rw,hard,rsize=1048576,wsize=1048576,timeo=600 \
    ${GATEWAY_IP}:/${BUCKET_NAME} ${MOUNT_PATH}

# Verify mount
mount | grep nfs
df -h $MOUNT_PATH
```

### 2. Install Testing Tools

```bash
# Ubuntu/Debian
sudo apt-get update
sudo apt-get install -y fio jq bc

# RHEL/CentOS
sudo yum install -y fio jq bc

# macOS
brew install fio jq
```

### 3. Verify Access

```bash
# Test write access
touch ${MOUNT_PATH}/test.txt && rm ${MOUNT_PATH}/test.txt

# If permission denied, check NFS export settings on gateway
```

## Running Tests

### Quick Mode (Recommended First)

Fast baseline test (~5-10 minutes):

```bash
export MOUNT_PATH="/mnt/cos-nfs"
export QUICK_MODE=true
./scripts/mountpoint_stress_test.sh
```

**Quick mode uses:**
- Smaller file sizes (256MB vs 1GB)
- Shorter test duration (30s vs 60s)
- Fewer files (1,000 vs 10,000)

### Full Mode

Comprehensive test (~30-60 minutes):

```bash
export MOUNT_PATH="/mnt/cos-nfs"
./scripts/mountpoint_stress_test.sh
```

### Custom Configuration

```bash
# Custom mount path
export MOUNT_PATH="/custom/mount/path"

# Quick mode
export QUICK_MODE=true

# Run tests
./scripts/mountpoint_stress_test.sh
```

## What Gets Tested

### 1. Sequential Write Performance
- Large file writes (1GB single file)
- Concurrent writes (4 parallel streams)
- Variable block sizes (4K to 4M)

**Expected Current Performance:**
- Large files: 1-5 MB/s
- Small blocks: 0.5-2 MB/s

### 2. Sequential Read Performance
- Cold cache reads (first read)
- Warm cache reads (cached data)

**Expected Current Performance:**
- Cold: 10-50 MB/s
- Warm: 100-500 MB/s

### 3. Random I/O
- Random writes (4K blocks)
- Random reads (4K blocks)
- Mixed workload (70% read, 30% write)

**Expected Current Performance:**
- Random write: 10-50 IOPS
- Random read: 100-500 IOPS

### 4. Metadata Operations
- File creation rate
- Directory listing speed
- File deletion rate

**Expected Current Performance:**
- Creation: 5-20 files/sec
- Listing: 100-1000 files/sec
- Deletion: 10-50 files/sec

### 5. NFS-Specific Patterns
- File reopen cycles (simulates typical NFS behavior)

## Understanding Results

### Results Location

```bash
# Results saved to timestamped directory
ls -la stress-test-results-*/

# View summary report
cat stress-test-results-*/summary_report.txt
```

### Key Metrics

**Bandwidth (MB/s):**
- Higher is better
- Sequential > Random
- Reads typically faster than writes

**IOPS (Operations/Second):**
- Higher is better
- Important for small file workloads
- Random I/O metric

**Latency (milliseconds):**
- Lower is better
- p50 = median latency
- p99 = 99th percentile (worst case)

### Sample Output

```
=== Mountpoint Stress Test Report ===
Generated: 2026-04-11 00:35:00

== Performance Results ==

### seq-write-large
  Write Bandwidth: 2.34 MB/s
  Write IOPS: 2.34

### seq-write-concurrent
  Write Bandwidth: 4.12 MB/s
  Write IOPS: 4.12

### seq-read-cold
  Read Bandwidth: 28.45 MB/s
  Read IOPS: 28.45

### seq-read-warm
  Read Bandwidth: 312.67 MB/s
  Read IOPS: 312.67

### rand-write
  Write Bandwidth: 0.18 MB/s
  Write IOPS: 45.23

### rand-read
  Read Bandwidth: 1.23 MB/s
  Read IOPS: 315.67
```

## Interpreting Results

### Good Performance Indicators
✅ Sequential write > 5 MB/s
✅ Sequential read (cold) > 20 MB/s
✅ Random write > 50 IOPS
✅ File creation > 20 files/sec

### Performance Issues
⚠️ Sequential write < 2 MB/s → Write buffering problem
⚠️ Sequential read < 10 MB/s → Network or cache issue
⚠️ Random write < 20 IOPS → Metadata overhead
⚠️ File creation < 10 files/sec → NFS round-trip latency

## Monitoring During Tests

### Terminal 1: Run Tests
```bash
./scripts/mountpoint_stress_test.sh
```

### Terminal 2: Monitor I/O
```bash
# Install if needed: sudo apt-get install sysstat
iostat -x 5
```

### Terminal 3: Monitor Network
```bash
# Install if needed: sudo apt-get install iftop
sudo iftop -i eth0
```

### Terminal 4: Monitor Gateway
```bash
# Check gateway logs
tail -f /var/log/cos-nfs-gateway/gateway.log

# Or via journalctl
journalctl -u cos-nfs-gateway -f
```

## Troubleshooting

### Test Hangs or Fails

```bash
# Check mount status
mountpoint -q $MOUNT_PATH && echo "Mounted" || echo "Not mounted"

# Check network connectivity
ping <gateway-ip>

# Check NFS stats
nfsstat -c

# Remount if needed
sudo umount $MOUNT_PATH
sudo mount -t nfs4 -o rw,hard,rsize=1048576,wsize=1048576 \
    <gateway-ip>:/<bucket> $MOUNT_PATH
```

### Permission Denied

```bash
# Check NFS export on gateway
# Ensure client IP is allowed in gateway config

# Check mount options
mount | grep $MOUNT_PATH

# Try with different mount options
sudo mount -t nfs4 -o rw,hard,vers=4.1 \
    <gateway-ip>:/<bucket> $MOUNT_PATH
```

### Slow Performance

```bash
# Check mount options (should have large rsize/wsize)
mount | grep $MOUNT_PATH | grep -o 'rsize=[0-9]*'
mount | grep $MOUNT_PATH | grep -o 'wsize=[0-9]*'

# Should show: rsize=1048576, wsize=1048576

# Check network latency
ping -c 10 <gateway-ip>

# Check gateway resource usage
ssh <gateway-host> 'top -bn1 | head -20'
```

### Out of Space

```bash
# Check available space
df -h $MOUNT_PATH

# Clean up test files
rm -rf ${MOUNT_PATH}/stress-test-*

# Check COS bucket quota (via IBM Cloud Console)
```

## Next Steps After Testing

### 1. Analyze Bottlenecks
- Identify slowest operations
- Compare with expected performance
- Check gateway logs for errors

### 2. Tune Configuration
```yaml
# configs/config.yaml
write_buffer:
  size: 16777216        # 16MB
  flush_threshold: 8388608  # 8MB
  
cache:
  chunk_size: 1048576   # 1MB
  max_size: 1073741824  # 1GB
```

### 3. Enable Staging (After MVP Implementation)
```yaml
staging:
  enabled: true
  root_dir: "/var/lib/cos-nfs-gateway/staging"
  sync_threshold: 16777216  # 16MB
```

### 4. Re-test and Compare
```bash
# Run tests again after changes
./scripts/mountpoint_stress_test.sh

# Compare results
diff stress-test-results-before/summary_report.txt \
     stress-test-results-after/summary_report.txt
```

## Performance Targets

### Current (Before Staging)
- Sequential write: 1-5 MB/s
- Random write: 10-50 IOPS
- File creation: 5-20 files/sec

### Target (After Staging MVP)
- Sequential write: 20-100 MB/s (10-50x improvement)
- Random write: 100-500 IOPS (10x improvement)
- File creation: 50-200 files/sec (10x improvement)

### Ultimate Goal (Full Staging)
- Sequential write: 100-500 MB/s (network limited)
- Random write: 500-2000 IOPS
- File creation: 200-1000 files/sec

## FAQ

**Q: How long do tests take?**
A: Quick mode: 5-10 minutes, Full mode: 30-60 minutes

**Q: Will tests affect production data?**
A: Tests create files in a separate directory (`stress-test-*`) and clean up after completion

**Q: Can I run tests on a production mount?**
A: Yes, but tests will consume bandwidth and IOPS. Run during low-traffic periods.

**Q: What if I don't have sudo access?**
A: You can skip cache dropping (affects cold read test only). Other tests work without sudo.

**Q: How do I stop tests mid-run?**
A: Press Ctrl+C. The script will clean up test files automatically.

**Q: Can I run multiple tests in parallel?**
A: Not recommended - results will interfere with each other. Run sequentially.

## Support

For issues or questions:
1. Check gateway logs: `journalctl -u cos-nfs-gateway -n 100`
2. Review test logs in results directory
3. Check network connectivity and mount status
4. Verify gateway configuration

## References

- Full documentation: [`docs/MOUNTPOINT_STRESS_TEST.md`](./MOUNTPOINT_STRESS_TEST.md)
- FIO documentation: https://fio.readthedocs.io/
- NFS tuning: https://wiki.linux-nfs.org/wiki/index.php/Performance