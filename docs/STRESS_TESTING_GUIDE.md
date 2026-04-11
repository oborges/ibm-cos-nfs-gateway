# Stress Testing Guide for COS NFS Gateway

## Overview

This guide provides comprehensive instructions for stress testing the COS NFS Gateway mountpoint to validate performance, stability, and correctness under various workload conditions.

## Prerequisites

### Required Tools

```bash
# Install fio (Flexible I/O Tester)
# Ubuntu/Debian
sudo apt-get install fio

# macOS
brew install fio

# RHEL/CentOS
sudo yum install fio

# Install iozone (optional, for additional testing)
sudo apt-get install iozone3  # Ubuntu/Debian
```

### Mount the NFS Share

```bash
# Create mount point
sudo mkdir -p /mnt/cos-nfs

# Mount the NFS share
sudo mount -t nfs -o vers=3,tcp,rsize=1048576,wsize=1048576 \
  localhost:/bucket /mnt/cos-nfs

# Verify mount
df -h /mnt/cos-nfs
mount | grep cos-nfs
```

## Test Categories

### 1. Sequential Read Performance

**Purpose**: Measure throughput for large sequential reads (streaming workloads)

```bash
# Test with fio - Sequential Read
fio --name=seq-read \
    --directory=/mnt/cos-nfs \
    --rw=read \
    --bs=1M \
    --size=100M \
    --numjobs=1 \
    --time_based \
    --runtime=60s \
    --group_reporting

# Expected Results:
# - Throughput: >50 MB/s (target: 100+ MB/s)
# - IOPS: >50 (with 1M blocks)
# - Latency: <20ms average
```

**Test with dd**:
```bash
# Create test file first (if not exists)
dd if=/dev/zero of=/mnt/cos-nfs/testfile bs=1M count=100

# Sequential read test
dd if=/mnt/cos-nfs/testfile of=/dev/null bs=1M count=100

# Expected: >50 MB/s
```

### 2. Sequential Write Performance

**Purpose**: Validate write buffering and flush efficiency

```bash
# Test with fio - Sequential Write
fio --name=seq-write \
    --directory=/mnt/cos-nfs \
    --rw=write \
    --bs=1M \
    --size=100M \
    --numjobs=1 \
    --time_based \
    --runtime=60s \
    --group_reporting \
    --fsync=1

# Expected Results:
# - Throughput: >20 MB/s (target: 50+ MB/s with write buffer)
# - IOPS: >20 (with 1M blocks)
# - Latency: <50ms average
```

**Test with dd**:
```bash
# Sequential write test
dd if=/dev/zero of=/mnt/cos-nfs/write-test bs=1M count=100 conv=fsync

# Expected: >20 MB/s
```

### 3. Random Read Performance

**Purpose**: Test chunk cache effectiveness and range request handling

```bash
# Random read test with fio
fio --name=rand-read \
    --directory=/mnt/cos-nfs \
    --rw=randread \
    --bs=4K \
    --size=100M \
    --numjobs=4 \
    --time_based \
    --runtime=60s \
    --group_reporting

# Expected Results:
# - IOPS: >100 (with cache hits)
# - Latency: <100ms average
# - Cache hit rate: >50% (check logs)
```

### 4. Random Write Performance

**Purpose**: Validate write buffer with random access patterns

```bash
# Random write test with fio
fio --name=rand-write \
    --directory=/mnt/cos-nfs \
    --rw=randwrite \
    --bs=4K \
    --size=100M \
    --numjobs=4 \
    --time_based \
    --runtime=60s \
    --group_reporting \
    --fsync=1

# Expected Results:
# - IOPS: >50 (buffered)
# - Latency: <200ms average
```

### 5. Mixed Read/Write Workload

**Purpose**: Simulate real-world application behavior

```bash
# 70% read, 30% write workload
fio --name=mixed-rw \
    --directory=/mnt/cos-nfs \
    --rw=randrw \
    --rwmixread=70 \
    --bs=64K \
    --size=100M \
    --numjobs=4 \
    --time_based \
    --runtime=120s \
    --group_reporting

# Expected Results:
# - Read IOPS: >80
# - Write IOPS: >30
# - Overall throughput: >10 MB/s
```

### 6. Small File Operations

**Purpose**: Test metadata operations and small file handling

```bash
# Create many small files
mkdir -p /mnt/cos-nfs/small-files
cd /mnt/cos-nfs/small-files

# Create 1000 small files
time for i in {1..1000}; do
  echo "test data $i" > file_$i.txt
done

# Expected: <60 seconds for 1000 files

# Read all files
time for i in {1..1000}; do
  cat file_$i.txt > /dev/null
done

# Expected: <30 seconds for 1000 files

# Delete all files
time rm -f file_*.txt

# Expected: <30 seconds for 1000 files
```

### 7. Large File Operations

**Purpose**: Test multipart upload and large file handling

```bash
# Create 1GB file
dd if=/dev/zero of=/mnt/cos-nfs/large-file bs=1M count=1024

# Expected: >20 MB/s write speed

# Read 1GB file
dd if=/mnt/cos-nfs/large-file of=/dev/null bs=1M

# Expected: >50 MB/s read speed

# Append to large file
dd if=/dev/zero of=/mnt/cos-nfs/large-file bs=1M count=100 oflag=append conv=notrunc

# Expected: Successful append without corruption
```

### 8. Concurrent Access

**Purpose**: Test thread safety and concurrent operations

```bash
# Run multiple fio jobs concurrently
fio --name=concurrent \
    --directory=/mnt/cos-nfs \
    --rw=randrw \
    --bs=64K \
    --size=50M \
    --numjobs=8 \
    --time_based \
    --runtime=120s \
    --group_reporting

# Expected Results:
# - No errors or crashes
# - Aggregate throughput: >20 MB/s
# - No file corruption
```

### 9. Direct I/O Testing

**Purpose**: Validate direct I/O support (bypass page cache)

```bash
# Direct I/O read test
dd if=/mnt/cos-nfs/testfile of=/dev/null bs=16K count=1000 iflag=direct

# Expected: Successful reads, >10 MB/s

# Direct I/O write test
dd if=/dev/zero of=/mnt/cos-nfs/direct-test bs=16K count=1000 oflag=direct

# Expected: Successful writes, >5 MB/s
```

### 10. Stability and Endurance

**Purpose**: Long-running test to detect memory leaks and stability issues

```bash
# Run for 1 hour with mixed workload
fio --name=endurance \
    --directory=/mnt/cos-nfs \
    --rw=randrw \
    --bs=64K \
    --size=200M \
    --numjobs=4 \
    --time_based \
    --runtime=3600s \
    --group_reporting

# Monitor during test:
# - Memory usage (should be stable)
# - CPU usage (should be reasonable)
# - No error messages in logs
# - No file corruption
```

## Monitoring During Tests

### Check Gateway Logs

```bash
# Follow logs in real-time
tail -f /var/log/cos-nfs-gateway/gateway.log

# Look for:
# - Cache hit/miss rates
# - Error messages
# - Performance warnings
# - Buffer flush operations
```

### Monitor System Resources

```bash
# CPU and memory usage
top -p $(pgrep nfs-gateway)

# Network I/O
iftop -i eth0

# Disk I/O (if using local cache)
iostat -x 1
```

### Check NFS Statistics

```bash
# NFS client statistics
nfsstat -c

# NFS server statistics (on gateway host)
nfsstat -s
```

## Performance Baselines

### Expected Performance Targets

| Metric | Target | Acceptable | Poor |
|--------|--------|------------|------|
| Sequential Read | >100 MB/s | >50 MB/s | <20 MB/s |
| Sequential Write | >50 MB/s | >20 MB/s | <5 MB/s |
| Random Read IOPS (4K) | >200 | >100 | <50 |
| Random Write IOPS (4K) | >100 | >50 | <20 |
| Small File Create (1000 files) | <30s | <60s | >120s |
| Large File (1GB) Read | >100 MB/s | >50 MB/s | <20 MB/s |
| Large File (1GB) Write | >50 MB/s | >20 MB/s | <5 MB/s |

### Cache Performance Indicators

```bash
# Check cache hit rate in logs
grep "cache hit" /var/log/cos-nfs-gateway/gateway.log | wc -l
grep "cache miss" /var/log/cos-nfs-gateway/gateway.log | wc -l

# Target: >50% hit rate for repeated reads
```

## Troubleshooting

### Slow Performance

1. **Check network latency to COS**:
   ```bash
   ping -c 10 s3.us-south.cloud-object-storage.appdomain.cloud
   ```

2. **Verify cache is enabled**:
   ```bash
   grep -i cache /etc/cos-nfs-gateway/config.yaml
   ```

3. **Check for errors in logs**:
   ```bash
   grep -i error /var/log/cos-nfs-gateway/gateway.log
   ```

4. **Verify NFS mount options**:
   ```bash
   mount | grep cos-nfs
   # Should see: rsize=1048576,wsize=1048576
   ```

### File Corruption

1. **Verify checksums**:
   ```bash
   # Create file with known content
   echo "test" > /mnt/cos-nfs/checksum-test
   md5sum /mnt/cos-nfs/checksum-test
   
   # Unmount and remount
   sudo umount /mnt/cos-nfs
   sudo mount -t nfs localhost:/bucket /mnt/cos-nfs
   
   # Verify checksum matches
   md5sum /mnt/cos-nfs/checksum-test
   ```

2. **Check for partial writes**:
   ```bash
   # Look for flush errors in logs
   grep -i "flush\|write.*error" /var/log/cos-nfs-gateway/gateway.log
   ```

### High Latency

1. **Check COS region**:
   - Ensure gateway is in same region as COS bucket

2. **Verify chunk size configuration**:
   ```bash
   grep chunk_size /etc/cos-nfs-gateway/config.yaml
   # Should be 1MB-4MB for optimal performance
   ```

3. **Monitor concurrent requests**:
   ```bash
   # Check for request queuing
   grep "concurrent" /var/log/cos-nfs-gateway/gateway.log
   ```

## Automated Test Script

Save as `stress-test.sh`:

```bash
#!/bin/bash

MOUNT_POINT="/mnt/cos-nfs"
TEST_DIR="$MOUNT_POINT/stress-test-$(date +%Y%m%d-%H%M%S)"

echo "=== COS NFS Gateway Stress Test ==="
echo "Mount point: $MOUNT_POINT"
echo "Test directory: $TEST_DIR"
echo

# Create test directory
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

# Test 1: Sequential Write
echo "Test 1: Sequential Write (100MB)"
fio --name=seq-write --rw=write --bs=1M --size=100M --numjobs=1 \
    --output=seq-write.log --output-format=json

# Test 2: Sequential Read
echo "Test 2: Sequential Read (100MB)"
fio --name=seq-read --rw=read --bs=1M --size=100M --numjobs=1 \
    --output=seq-read.log --output-format=json

# Test 3: Random Read
echo "Test 3: Random Read (4K blocks, 60s)"
fio --name=rand-read --rw=randread --bs=4K --size=100M --numjobs=4 \
    --time_based --runtime=60s --output=rand-read.log --output-format=json

# Test 4: Random Write
echo "Test 4: Random Write (4K blocks, 60s)"
fio --name=rand-write --rw=randwrite --bs=4K --size=100M --numjobs=4 \
    --time_based --runtime=60s --output=rand-write.log --output-format=json

# Test 5: Mixed Workload
echo "Test 5: Mixed Read/Write (70/30, 60s)"
fio --name=mixed --rw=randrw --rwmixread=70 --bs=64K --size=100M --numjobs=4 \
    --time_based --runtime=60s --output=mixed.log --output-format=json

echo
echo "=== Test Complete ==="
echo "Results saved in: $TEST_DIR"
echo
echo "Summary:"
grep -A 5 "READ:" *.log | grep -E "bw=|iops="
grep -A 5 "WRITE:" *.log | grep -E "bw=|iops="
```

## Validation Checklist

- [ ] Sequential read >50 MB/s
- [ ] Sequential write >20 MB/s
- [ ] Random read IOPS >100
- [ ] Random write IOPS >50
- [ ] No file corruption detected
- [ ] No memory leaks during 1-hour test
- [ ] Cache hit rate >50% for repeated reads
- [ ] Direct I/O works correctly
- [ ] Concurrent access works without errors
- [ ] Large files (>1GB) handled correctly

## Next Steps

After completing stress tests:

1. **Analyze Results**: Compare against baselines
2. **Identify Bottlenecks**: Use profiling if needed
3. **Tune Configuration**: Adjust cache sizes, chunk sizes, buffer thresholds
4. **Iterate**: Re-test after configuration changes
5. **Document**: Record final performance characteristics

## References

- [fio Documentation](https://fio.readthedocs.io/)
- [NFS Performance Tuning](https://access.redhat.com/documentation/en-us/red_hat_enterprise_linux/7/html/storage_administration_guide/ch-nfs)
- [IBM Cloud Object Storage Performance](https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-performance)