# Stress Testing Resources Summary

This document provides an overview of all stress testing resources available for the COS NFS Gateway.

## 📚 Documentation

### 1. Comprehensive Guide
**File**: [`docs/STRESS_TESTING_GUIDE.md`](STRESS_TESTING_GUIDE.md)

**Purpose**: Complete reference for stress testing the gateway

**Contents**:
- Prerequisites and tool installation
- 10 different test categories (sequential, random, mixed, small files, large files, etc.)
- Performance baselines and targets
- Monitoring during tests
- Troubleshooting guide
- Validation checklist

**When to use**: For detailed understanding of testing methodology and comprehensive testing

---

### 2. Quick Start Guide
**File**: [`docs/QUICK_START_STRESS_TEST.md`](QUICK_START_STRESS_TEST.md)

**Purpose**: Fast-track guide to get started with stress testing

**Contents**:
- Quick installation steps
- 3 testing options (full suite, quick check, individual tests)
- Result interpretation guide
- Common troubleshooting
- Example test commands

**When to use**: When you want to quickly validate performance without reading extensive documentation

---

## 🔧 Testing Scripts

### 1. Comprehensive Test Suite
**File**: [`scripts/run_stress_tests.sh`](../scripts/run_stress_tests.sh)

**Purpose**: Automated comprehensive stress testing

**Features**:
- 9 different test scenarios
- Automated result collection
- JSON output for analysis
- Summary report generation
- Color-coded output
- Prerequisite checking

**Tests included**:
1. Sequential Write (100MB)
2. Sequential Read (100MB)
3. Random Read (4K blocks, 60s)
4. Random Write (4K blocks, 60s)
5. Mixed Read/Write (70/30, 60s)
6. Small File Operations (1000 files)
7. Large File Operations (500MB)
8. Direct I/O
9. Concurrent Access (8 jobs)

**Duration**: ~15-20 minutes

**Usage**:
```bash
./scripts/run_stress_tests.sh
```

**Output**: Results saved in `./test-results-YYYYMMDD-HHMMSS/`

---

### 2. Quick Test Script
**File**: [`scripts/quick_test.sh`](../scripts/quick_test.sh)

**Purpose**: Fast performance validation

**Features**:
- 4 essential tests
- Quick execution
- Simple output
- Minimal dependencies

**Tests included**:
1. Sequential Write (100MB)
2. Sequential Read (100MB)
3. Random IOPS (30s)
4. Small File Operations (100 files)

**Duration**: ~5 minutes

**Usage**:
```bash
./scripts/quick_test.sh
```

---

## 📊 Performance Targets

### Expected Performance

| Metric | Target | Acceptable | Poor |
|--------|--------|------------|------|
| **Sequential Read** | >100 MB/s | >50 MB/s | <20 MB/s |
| **Sequential Write** | >50 MB/s | >20 MB/s | <5 MB/s |
| **Random Read IOPS (4K)** | >200 | >100 | <50 |
| **Random Write IOPS (4K)** | >100 | >50 | <20 |
| **Small File Create (1000)** | <30s | <60s | >120s |
| **Large File (1GB) Read** | >100 MB/s | >50 MB/s | <20 MB/s |
| **Large File (1GB) Write** | >50 MB/s | >20 MB/s | <5 MB/s |

### Cache Performance

- **Cache Hit Rate**: >50% for repeated reads
- **Metadata Cache**: <10ms lookup time
- **Data Cache**: Significant reduction in COS API calls

---

## 🚀 Quick Start

### Step 1: Install Prerequisites

```bash
# Ubuntu/Debian
sudo apt-get install -y fio jq bc

# macOS
brew install fio jq bc
```

### Step 2: Mount NFS

```bash
sudo mkdir -p /mnt/cos-nfs
sudo mount -t nfs -o vers=3,tcp,rsize=1048576,wsize=1048576 \
  localhost:/bucket /mnt/cos-nfs
```

### Step 3: Run Tests

**Option A - Quick Test (5 minutes)**:
```bash
./scripts/quick_test.sh
```

**Option B - Full Suite (15-20 minutes)**:
```bash
./scripts/run_stress_tests.sh
```

**Option C - Manual Test**:
```bash
# Sequential write
fio --name=test --directory=/mnt/cos-nfs \
    --rw=write --bs=1M --size=100M --numjobs=1

# Sequential read
fio --name=test --directory=/mnt/cos-nfs \
    --rw=read --bs=1M --size=100M --numjobs=1
```

---

## 📈 Monitoring

### During Tests

**Watch Gateway Logs**:
```bash
tail -f /var/log/cos-nfs-gateway/gateway.log
```

**Monitor Resources**:
```bash
# CPU and memory
top -p $(pgrep nfs-gateway)

# Network I/O
iftop -i eth0

# NFS statistics
nfsstat -c
```

### After Tests

**Check Results**:
```bash
# View summary
cat ./test-results-*/SUMMARY.txt

# Analyze JSON results
jq '.jobs[0].read.bw' ./test-results-*/02-seq-read.json
```

---

## 🔍 Interpreting Results

### Good Performance Example

```
Sequential Write: 45.2 MB/s ✓
Sequential Read:  78.5 MB/s ✓
Random Read:      156 IOPS  ✓
Random Write:     89 IOPS   ✓
```

### Poor Performance Example (Needs Investigation)

```
Sequential Write: 2.1 MB/s  ✗ (Target: >20 MB/s)
Sequential Read:  15.3 MB/s ✗ (Target: >50 MB/s)
Random Read:      23 IOPS   ✗ (Target: >100 IOPS)
Random Write:     12 IOPS   ✗ (Target: >50 IOPS)
```

**Action**: Check logs, verify cache configuration, test network latency to COS

---

## 🐛 Common Issues

### Issue: Slow Performance

**Symptoms**: All metrics below targets

**Checks**:
1. Network latency to COS: `ping s3.us-south.cloud-object-storage.appdomain.cloud`
2. Cache enabled: `grep cache /etc/cos-nfs-gateway/config.yaml`
3. Mount options: `mount | grep cos-nfs` (should see rsize/wsize=1048576)
4. Gateway errors: `grep -i error /var/log/cos-nfs-gateway/gateway.log`

### Issue: Test Script Fails

**Symptoms**: Script exits with error

**Checks**:
1. fio installed: `which fio`
2. NFS mounted: `mountpoint /mnt/cos-nfs`
3. Write permissions: `touch /mnt/cos-nfs/test && rm /mnt/cos-nfs/test`
4. Disk space: `df -h /mnt/cos-nfs`

### Issue: Inconsistent Results

**Symptoms**: Performance varies significantly between runs

**Possible causes**:
- Network congestion
- COS throttling
- Cache warming (first run slower)
- Background processes

**Solution**: Run tests multiple times, use median values

---

## 📋 Testing Checklist

Before running stress tests:
- [ ] Gateway is running and healthy
- [ ] NFS is mounted with correct options
- [ ] fio, jq, bc are installed
- [ ] Sufficient disk space available
- [ ] No other heavy processes running

After running stress tests:
- [ ] All tests completed without errors
- [ ] Performance meets acceptable targets
- [ ] No errors in gateway logs
- [ ] Cache hit rate is reasonable (>50%)
- [ ] Memory usage is stable
- [ ] Results documented

---

## 🎯 Next Steps

After completing stress tests:

1. **Analyze Results**: Compare against baselines
2. **Identify Bottlenecks**: Use profiling if needed
3. **Tune Configuration**: Adjust cache sizes, chunk sizes
4. **Re-test**: Validate improvements
5. **Document**: Record final performance characteristics
6. **Deploy**: Move to production if targets are met

---

## 📞 Getting Help

If you encounter issues:

1. Check the [Comprehensive Guide](STRESS_TESTING_GUIDE.md) for detailed troubleshooting
2. Review gateway logs for errors
3. Verify network connectivity to IBM Cloud COS
4. Check system resources (CPU, memory, network)
5. Open an issue on GitHub with test results and logs

---

## 📝 Additional Resources

- [Main README](../README.md) - Project overview
- [Architecture](../ARCHITECTURE.md) - System design
- [Performance Improvements](PERFORMANCE_IMPROVEMENTS.md) - Optimization history
- [Deployment Guide](DEPLOYMENT_AND_TESTING.md) - Production deployment

---

**Last Updated**: 2026-04-11

**Version**: 1.0