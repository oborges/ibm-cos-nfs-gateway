# Quick Start: Stress Testing the COS NFS Gateway

This guide will help you quickly set up and run stress tests on your COS NFS Gateway mountpoint.

## Prerequisites

### 1. Install Required Tools

```bash
# Ubuntu/Debian
sudo apt-get update
sudo apt-get install -y fio jq bc

# macOS
brew install fio jq bc

# RHEL/CentOS
sudo yum install -y fio jq bc
```

### 2. Verify NFS Mount

```bash
# Check if NFS is mounted
df -h | grep cos-nfs
mount | grep cos-nfs

# If not mounted, mount it:
sudo mkdir -p /mnt/cos-nfs
sudo mount -t nfs -o vers=3,tcp,rsize=1048576,wsize=1048576 \
  localhost:/bucket /mnt/cos-nfs
```

## Running the Automated Stress Test

### Option 1: Full Test Suite (Recommended)

Run the comprehensive automated test suite:

```bash
cd /path/to/cos-nfs-gateway
./scripts/run_stress_tests.sh
```

This will run:
- Sequential read/write tests
- Random read/write tests
- Mixed workload tests
- Small file operations
- Large file operations
- Direct I/O tests
- Concurrent access tests

**Duration**: ~15-20 minutes

**Output**: Results saved in `./test-results-YYYYMMDD-HHMMSS/`

### Option 2: Quick Performance Check

For a quick performance check (2-3 minutes):

```bash
# Sequential write test
fio --name=quick-write --directory=/mnt/cos-nfs \
    --rw=write --bs=1M --size=100M --numjobs=1

# Sequential read test
fio --name=quick-read --directory=/mnt/cos-nfs \
    --rw=read --bs=1M --size=100M --numjobs=1
```

### Option 3: Individual Tests

Run specific tests as needed:

#### Sequential Write Performance
```bash
fio --name=seq-write --directory=/mnt/cos-nfs \
    --rw=write --bs=1M --size=100M --numjobs=1 \
    --output=seq-write.json --output-format=json
```

#### Sequential Read Performance
```bash
fio --name=seq-read --directory=/mnt/cos-nfs \
    --rw=read --bs=1M --size=100M --numjobs=1 \
    --output=seq-read.json --output-format=json
```

#### Random Read IOPS
```bash
fio --name=rand-read --directory=/mnt/cos-nfs \
    --rw=randread --bs=4K --size=100M --numjobs=4 \
    --time_based --runtime=60s
```

#### Random Write IOPS
```bash
fio --name=rand-write --directory=/mnt/cos-nfs \
    --rw=randwrite --bs=4K --size=100M --numjobs=4 \
    --time_based --runtime=60s
```

## Interpreting Results

### Performance Targets

| Metric | Target | Acceptable | Poor |
|--------|--------|------------|------|
| Sequential Read | >100 MB/s | >50 MB/s | <20 MB/s |
| Sequential Write | >50 MB/s | >20 MB/s | <5 MB/s |
| Random Read IOPS | >200 | >100 | <50 |
| Random Write IOPS | >100 | >50 | <20 |

### Reading fio Output

Key metrics to look for:

```
READ: bw=52.3MiB/s (54.8MB/s), 52.3MiB/s-52.3MiB/s (54.8MB/s-54.8MB/s), io=3136MiB (3289MB), run=60001-60001msec
```

- **bw**: Bandwidth (throughput) - higher is better
- **IOPS**: I/O operations per second - higher is better
- **lat**: Latency - lower is better

### Example Good Results

```
Sequential Write: bw=45.2MB/s, IOPS=45
Sequential Read:  bw=78.5MB/s, IOPS=78
Random Read:      IOPS=156, lat=25.6ms
Random Write:     IOPS=89, lat=44.8ms
```

### Example Poor Results (Needs Investigation)

```
Sequential Write: bw=2.1MB/s, IOPS=2    ← Too slow
Sequential Read:  bw=15.3MB/s, IOPS=15  ← Below target
Random Read:      IOPS=23, lat=174ms    ← Low IOPS, high latency
Random Write:     IOPS=12, lat=333ms    ← Very poor
```

## Monitoring During Tests

### Watch Gateway Logs

```bash
# In another terminal
tail -f /var/log/cos-nfs-gateway/gateway.log

# Or if using systemd
journalctl -u cos-nfs-gateway -f
```

Look for:
- Cache hit/miss rates
- Error messages
- Performance warnings
- Buffer flush operations

### Monitor System Resources

```bash
# CPU and memory
top -p $(pgrep nfs-gateway)

# Network I/O
iftop -i eth0

# NFS statistics
nfsstat -c
```

## Troubleshooting

### Slow Performance

1. **Check network latency**:
   ```bash
   ping -c 10 s3.us-south.cloud-object-storage.appdomain.cloud
   ```
   - Should be <50ms for same region

2. **Verify cache is enabled**:
   ```bash
   grep -A 5 "cache:" /etc/cos-nfs-gateway/config.yaml
   ```

3. **Check for errors**:
   ```bash
   grep -i error /var/log/cos-nfs-gateway/gateway.log | tail -20
   ```

4. **Verify mount options**:
   ```bash
   mount | grep cos-nfs
   ```
   - Should see: `rsize=1048576,wsize=1048576`

### Test Failures

1. **Permission denied**:
   ```bash
   # Check mount point permissions
   ls -ld /mnt/cos-nfs
   
   # Should be writable by your user or group
   sudo chmod 777 /mnt/cos-nfs  # For testing only
   ```

2. **fio not found**:
   ```bash
   # Install fio
   sudo apt-get install fio  # Ubuntu/Debian
   brew install fio          # macOS
   ```

3. **Mount point not found**:
   ```bash
   # Verify NFS is mounted
   mountpoint /mnt/cos-nfs
   
   # If not, mount it
   sudo mount -t nfs -o vers=3,tcp,rsize=1048576,wsize=1048576 \
     localhost:/bucket /mnt/cos-nfs
   ```

## Advanced Testing

### Long-Running Stability Test

Test for memory leaks and stability issues:

```bash
fio --name=endurance --directory=/mnt/cos-nfs \
    --rw=randrw --bs=64K --size=200M --numjobs=4 \
    --time_based --runtime=3600s --group_reporting
```

**Duration**: 1 hour

Monitor memory usage during the test:
```bash
watch -n 5 'ps aux | grep nfs-gateway | grep -v grep'
```

### Concurrent Client Test

Simulate multiple clients:

```bash
# Run 8 concurrent fio jobs
fio --name=multi-client --directory=/mnt/cos-nfs \
    --rw=randrw --bs=64K --size=50M --numjobs=8 \
    --time_based --runtime=300s --group_reporting
```

### Large File Test

Test with files >1GB:

```bash
# Write 2GB file
dd if=/dev/zero of=/mnt/cos-nfs/large-2gb bs=1M count=2048

# Read 2GB file
dd if=/mnt/cos-nfs/large-2gb of=/dev/null bs=1M
```

## Next Steps

After running stress tests:

1. **Review Results**: Check if performance meets targets
2. **Analyze Logs**: Look for errors or warnings
3. **Tune Configuration**: Adjust cache sizes, chunk sizes if needed
4. **Re-test**: Validate improvements after tuning
5. **Document**: Record final performance characteristics

## Getting Help

If you encounter issues:

1. Check the full documentation: [`docs/STRESS_TESTING_GUIDE.md`](STRESS_TESTING_GUIDE.md)
2. Review gateway logs for errors
3. Verify network connectivity to IBM Cloud COS
4. Check system resources (CPU, memory, network)

## Example: Complete Quick Test

```bash
#!/bin/bash

# Quick 5-minute stress test
echo "=== Quick COS NFS Gateway Stress Test ==="

# 1. Sequential write
echo "Test 1: Sequential Write..."
fio --name=write --directory=/mnt/cos-nfs --rw=write \
    --bs=1M --size=100M --numjobs=1 | grep -E "bw=|IOPS="

# 2. Sequential read
echo "Test 2: Sequential Read..."
fio --name=read --directory=/mnt/cos-nfs --rw=read \
    --bs=1M --size=100M --numjobs=1 | grep -E "bw=|IOPS="

# 3. Random IOPS
echo "Test 3: Random IOPS..."
fio --name=random --directory=/mnt/cos-nfs --rw=randrw \
    --bs=4K --size=50M --numjobs=4 --runtime=30s \
    --time_based | grep -E "IOPS="

echo "=== Test Complete ==="
```

Save as `quick-test.sh`, make executable, and run:
```bash
chmod +x quick-test.sh
./quick-test.sh