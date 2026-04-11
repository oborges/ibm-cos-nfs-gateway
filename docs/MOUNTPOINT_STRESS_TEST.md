# Mountpoint Stress Testing Guide

## Overview

This guide provides comprehensive stress testing procedures for the IBM Cloud COS NFS Gateway mountpoint. These tests establish baseline performance metrics and identify bottlenecks before architectural improvements.

## Prerequisites

### 1. Mounted NFS Share
```bash
# Verify mount is active
mount | grep nfs
df -h | grep nfs

# Example output:
# 192.168.1.100:/bucket on /mnt/cos-nfs type nfs4 (rw,relatime,...)
```

### 2. Required Tools
```bash
# Install testing tools
sudo apt-get update
sudo apt-get install -y \
    fio \
    iozone \
    sysbench \
    iotop \
    sysstat \
    bc

# Verify installations
fio --version
iozone -v
sysbench --version
```

### 3. Test Environment Setup
```bash
# Set mountpoint path
export MOUNT_PATH="/mnt/cos-nfs"
export TEST_DIR="${MOUNT_PATH}/stress-test"

# Create test directory
mkdir -p "${TEST_DIR}"

# Verify write access
touch "${TEST_DIR}/test.txt" && rm "${TEST_DIR}/test.txt"
```

## Test Categories

### 1. Sequential Write Performance

#### Test 1.1: Large File Sequential Write (FIO)
```bash
# 1GB sequential write, 1MB block size
fio --name=seq-write-1g \
    --directory="${TEST_DIR}" \
    --rw=write \
    --bs=1M \
    --size=1G \
    --numjobs=1 \
    --direct=1 \
    --group_reporting \
    --time_based=0 \
    --output-format=json \
    --output="${TEST_DIR}/seq-write-1g.json"

# Expected metrics:
# - Throughput (MB/s)
# - IOPS
# - Latency (avg, p50, p95, p99)
```

#### Test 1.2: Multiple File Sequential Write
```bash
# 4 concurrent 256MB writes
fio --name=seq-write-multi \
    --directory="${TEST_DIR}" \
    --rw=write \
    --bs=1M \
    --size=256M \
    --numjobs=4 \
    --direct=1 \
    --group_reporting \
    --output-format=json \
    --output="${TEST_DIR}/seq-write-multi.json"
```

#### Test 1.3: Variable Block Size Write
```bash
# Test different block sizes: 4K, 64K, 256K, 1M, 4M
for bs in 4k 64k 256k 1m 4m; do
    echo "Testing block size: ${bs}"
    fio --name=seq-write-${bs} \
        --directory="${TEST_DIR}" \
        --rw=write \
        --bs=${bs} \
        --size=512M \
        --numjobs=1 \
        --direct=1 \
        --group_reporting \
        --output-format=json \
        --output="${TEST_DIR}/seq-write-${bs}.json"
done
```

### 2. Sequential Read Performance

#### Test 2.1: Large File Sequential Read
```bash
# Create test file first
dd if=/dev/zero of="${TEST_DIR}/read-test-1g.dat" bs=1M count=1024

# Sequential read test
fio --name=seq-read-1g \
    --directory="${TEST_DIR}" \
    --rw=read \
    --bs=1M \
    --size=1G \
    --numjobs=1 \
    --direct=1 \
    --group_reporting \
    --output-format=json \
    --output="${TEST_DIR}/seq-read-1g.json"
```

#### Test 2.2: Cached vs Uncached Reads
```bash
# First read (cold cache)
echo 3 | sudo tee /proc/sys/vm/drop_caches
fio --name=seq-read-cold \
    --filename="${TEST_DIR}/read-test-1g.dat" \
    --rw=read \
    --bs=1M \
    --size=1G \
    --numjobs=1 \
    --direct=0 \
    --group_reporting \
    --output-format=json \
    --output="${TEST_DIR}/seq-read-cold.json"

# Second read (warm cache)
fio --name=seq-read-warm \
    --filename="${TEST_DIR}/read-test-1g.dat" \
    --rw=read \
    --bs=1M \
    --size=1G \
    --numjobs=1 \
    --direct=0 \
    --group_reporting \
    --output-format=json \
    --output="${TEST_DIR}/seq-read-warm.json"
```

### 3. Random I/O Performance

#### Test 3.1: Random Write
```bash
fio --name=rand-write \
    --directory="${TEST_DIR}" \
    --rw=randwrite \
    --bs=4k \
    --size=512M \
    --numjobs=4 \
    --direct=1 \
    --group_reporting \
    --runtime=60 \
    --time_based \
    --output-format=json \
    --output="${TEST_DIR}/rand-write.json"
```

#### Test 3.2: Random Read
```bash
fio --name=rand-read \
    --directory="${TEST_DIR}" \
    --rw=randread \
    --bs=4k \
    --size=512M \
    --numjobs=4 \
    --direct=1 \
    --group_reporting \
    --runtime=60 \
    --time_based \
    --output-format=json \
    --output="${TEST_DIR}/rand-read.json"
```

#### Test 3.3: Mixed Random I/O (70% read, 30% write)
```bash
fio --name=rand-mixed \
    --directory="${TEST_DIR}" \
    --rw=randrw \
    --rwmixread=70 \
    --bs=4k \
    --size=512M \
    --numjobs=4 \
    --direct=1 \
    --group_reporting \
    --runtime=60 \
    --time_based \
    --output-format=json \
    --output="${TEST_DIR}/rand-mixed.json"
```

### 4. Metadata Operations

#### Test 4.1: File Creation Rate
```bash
# Create 10,000 small files
time for i in {1..10000}; do
    touch "${TEST_DIR}/file_${i}.txt"
done

# Calculate files/second
# files_per_sec = 10000 / elapsed_seconds
```

#### Test 4.2: Directory Listing Performance
```bash
# Time directory listing
time ls -la "${TEST_DIR}" > /dev/null

# Time recursive listing
time find "${TEST_DIR}" -type f | wc -l
```

#### Test 4.3: File Deletion Rate
```bash
# Delete 10,000 files
time rm -f "${TEST_DIR}"/file_*.txt

# Calculate deletions/second
```

### 5. Real-World Workload Simulation

#### Test 5.1: Application Build Simulation
```bash
# Simulate git clone + build
cd "${TEST_DIR}"
time git clone https://github.com/torvalds/linux.git
cd linux
time make defconfig
time make -j4 modules_prepare
```

#### Test 5.2: Database-Like Workload
```bash
# Simulate database writes (append + fsync)
sysbench fileio \
    --file-test-mode=seqwr \
    --file-total-size=1G \
    --file-num=16 \
    --file-fsync-freq=100 \
    --file-fsync-mode=fsync \
    --threads=4 \
    --time=60 \
    --report-interval=10 \
    prepare

sysbench fileio \
    --file-test-mode=seqwr \
    --file-total-size=1G \
    --file-num=16 \
    --file-fsync-freq=100 \
    --file-fsync-mode=fsync \
    --threads=4 \
    --time=60 \
    --report-interval=10 \
    run

sysbench fileio cleanup
```

#### Test 5.3: Log File Append Pattern
```bash
# Simulate continuous log appends
for i in {1..1000}; do
    echo "Log entry ${i}: $(date) - Some log message data" >> "${TEST_DIR}/app.log"
    sleep 0.1
done

# Measure file size growth rate
ls -lh "${TEST_DIR}/app.log"
```

### 6. Stress and Endurance Tests

#### Test 6.1: Sustained Write Load (1 hour)
```bash
fio --name=sustained-write \
    --directory="${TEST_DIR}" \
    --rw=write \
    --bs=1M \
    --size=10G \
    --numjobs=2 \
    --direct=1 \
    --group_reporting \
    --runtime=3600 \
    --time_based \
    --output-format=json \
    --output="${TEST_DIR}/sustained-write.json"
```

#### Test 6.2: Concurrent Mixed Workload
```bash
# Run multiple workloads simultaneously
fio --name=mixed-stress \
    --directory="${TEST_DIR}" \
    --rw=randrw \
    --rwmixread=60 \
    --bs=64k \
    --size=2G \
    --numjobs=8 \
    --direct=1 \
    --group_reporting \
    --runtime=1800 \
    --time_based \
    --output-format=json \
    --output="${TEST_DIR}/mixed-stress.json"
```

### 7. NFS-Specific Tests

#### Test 7.1: File Reopen Pattern (NFS churn)
```bash
# Simulate repeated open/close cycles
cat > "${TEST_DIR}/reopen_test.sh" << 'EOF'
#!/bin/bash
FILE="$1"
ITERATIONS="${2:-1000}"

for i in $(seq 1 $ITERATIONS); do
    echo "Iteration $i" >> "$FILE"
done
EOF

chmod +x "${TEST_DIR}/reopen_test.sh"
time "${TEST_DIR}/reopen_test.sh" "${TEST_DIR}/reopen.log" 1000
```

#### Test 7.2: Attribute Cache Test
```bash
# Test stat() performance
time for i in {1..10000}; do
    stat "${TEST_DIR}/read-test-1g.dat" > /dev/null
done
```

## Monitoring During Tests

### System Monitoring
```bash
# Terminal 1: Monitor I/O
iostat -x 5

# Terminal 2: Monitor network
iftop -i eth0

# Terminal 3: Monitor NFS stats
nfsstat -c 5

# Terminal 4: Monitor processes
top -d 5
```

### Gateway Monitoring
```bash
# Check gateway logs
tail -f /var/log/cos-nfs-gateway/gateway.log

# Monitor gateway metrics (if Prometheus enabled)
curl http://localhost:9090/metrics | grep nfs
```

## Results Analysis

### Parse FIO JSON Results
```bash
# Extract key metrics from FIO JSON
cat > parse_fio.sh << 'EOF'
#!/bin/bash
JSON_FILE="$1"

echo "=== FIO Test Results: $(basename $JSON_FILE) ==="
echo ""

# Read bandwidth (MB/s)
READ_BW=$(jq -r '.jobs[0].read.bw_bytes / 1048576' "$JSON_FILE" 2>/dev/null)
if [ "$READ_BW" != "null" ] && [ -n "$READ_BW" ]; then
    echo "Read Bandwidth: ${READ_BW} MB/s"
fi

# Write bandwidth (MB/s)
WRITE_BW=$(jq -r '.jobs[0].write.bw_bytes / 1048576' "$JSON_FILE" 2>/dev/null)
if [ "$WRITE_BW" != "null" ] && [ -n "$WRITE_BW" ]; then
    echo "Write Bandwidth: ${WRITE_BW} MB/s"
fi

# IOPS
READ_IOPS=$(jq -r '.jobs[0].read.iops' "$JSON_FILE" 2>/dev/null)
if [ "$READ_IOPS" != "null" ] && [ -n "$READ_IOPS" ]; then
    echo "Read IOPS: ${READ_IOPS}"
fi

WRITE_IOPS=$(jq -r '.jobs[0].write.iops' "$JSON_FILE" 2>/dev/null)
if [ "$WRITE_IOPS" != "null" ] && [ -n "$WRITE_IOPS" ]; then
    echo "Write IOPS: ${WRITE_IOPS}"
fi

# Latency
LAT_AVG=$(jq -r '.jobs[0].write.lat_ns.mean / 1000000' "$JSON_FILE" 2>/dev/null)
if [ "$LAT_AVG" != "null" ] && [ -n "$LAT_AVG" ]; then
    echo "Average Latency: ${LAT_AVG} ms"
fi

echo ""
EOF

chmod +x parse_fio.sh

# Parse all results
for json in "${TEST_DIR}"/*.json; do
    ./parse_fio.sh "$json"
done
```

### Generate Summary Report
```bash
cat > generate_report.sh << 'EOF'
#!/bin/bash
TEST_DIR="$1"
REPORT_FILE="${TEST_DIR}/stress_test_report.txt"

echo "=== Mountpoint Stress Test Report ===" > "$REPORT_FILE"
echo "Generated: $(date)" >> "$REPORT_FILE"
echo "" >> "$REPORT_FILE"

echo "## Test Environment" >> "$REPORT_FILE"
echo "Mount Point: ${MOUNT_PATH}" >> "$REPORT_FILE"
echo "Kernel: $(uname -r)" >> "$REPORT_FILE"
echo "NFS Version: $(nfsstat -m | grep vers)" >> "$REPORT_FILE"
echo "" >> "$REPORT_FILE"

echo "## Performance Summary" >> "$REPORT_FILE"
echo "" >> "$REPORT_FILE"

# Parse all JSON results
for json in "${TEST_DIR}"/*.json; do
    echo "### $(basename $json .json)" >> "$REPORT_FILE"
    ./parse_fio.sh "$json" >> "$REPORT_FILE"
done

echo "Report generated: $REPORT_FILE"
cat "$REPORT_FILE"
EOF

chmod +x generate_report.sh
./generate_report.sh "${TEST_DIR}"
```

## Expected Baseline Metrics

Based on current architecture (before staging optimization):

### Sequential Write
- **Large files (>16MB)**: 1-5 MB/s
- **Small files (<1MB)**: 0.5-2 MB/s
- **Latency**: 200-1000ms per operation

### Sequential Read
- **Cold cache**: 10-50 MB/s (network limited)
- **Warm cache**: 100-500 MB/s (memory cache)

### Random I/O
- **4K random write**: 10-50 IOPS
- **4K random read**: 100-500 IOPS (cached)

### Metadata Operations
- **File creation**: 5-20 files/sec
- **Directory listing**: 100-1000 files/sec
- **File deletion**: 10-50 files/sec

## Cleanup

```bash
# Remove test files
rm -rf "${TEST_DIR}"

# Verify cleanup
ls -la "${MOUNT_PATH}"
```

## Troubleshooting

### Test Hangs
```bash
# Check NFS mount status
nfsstat -m

# Check network connectivity
ping <gateway-ip>

# Check gateway logs
journalctl -u cos-nfs-gateway -f
```

### Poor Performance
```bash
# Check NFS mount options
mount | grep nfs

# Recommended options:
# rw,relatime,vers=4.1,rsize=1048576,wsize=1048576,hard,timeo=600

# Remount with optimal options
sudo umount "${MOUNT_PATH}"
sudo mount -t nfs4 -o rw,hard,rsize=1048576,wsize=1048576 \
    <gateway-ip>:/bucket "${MOUNT_PATH}"
```

### Disk Space Issues
```bash
# Check available space
df -h "${MOUNT_PATH}"

# Check COS bucket usage
# (via IBM Cloud Console or CLI)
```

## Next Steps

After establishing baseline metrics:

1. **Analyze Results**: Identify primary bottlenecks
2. **Compare with Targets**: Determine gap to desired performance
3. **Implement Staging**: Deploy staging architecture (MVP)
4. **Re-test**: Run same tests with staging enabled
5. **Measure Improvement**: Calculate performance gains
6. **Iterate**: Tune configuration based on results

## References

- FIO Documentation: https://fio.readthedocs.io/
- IOzone Documentation: http://www.iozone.org/docs/IOzone_msword_98.pdf
- NFS Performance Tuning: https://wiki.linux-nfs.org/wiki/index.php/Performance