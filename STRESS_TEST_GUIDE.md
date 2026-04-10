# Stress Test Guide with Progress Monitoring

## Overview

This guide helps you run stress tests on the NFS gateway with real-time progress monitoring to diagnose issues with large file operations.

## Prerequisites

1. NFS gateway rebuilt with latest fixes: `make build`
2. Gateway running: `./bin/nfs-gateway`
3. NFS mount active: `/mnt/nfs`

## Progress Monitoring

### Option 1: Real-time Progress Monitor (Recommended)

In a separate terminal, run the progress monitor:

```bash
# Monitor the gateway logs with formatted output
./scripts/monitor_progress.sh /var/log/nfs-gateway/gateway.log

# Or if logs go to stdout:
journalctl -u nfs-gateway -f | ./scripts/monitor_progress.sh
```

**What you'll see:**
- `[START]` - When download begins with file size
- `[PROGRESS]` - Every 10MB or 5 seconds showing:
  - Downloaded MB
  - Progress percentage
  - Current throughput (MB/s)
- `[COMPLETE]` - When download finishes
- `[RETRY]` - If network issues trigger retry
- `[ERROR]` - Any errors encountered
- `[CACHE HIT/MISS]` - Cache performance

### Option 2: Raw Log Monitoring

```bash
# Watch all logs
tail -f /var/log/nfs-gateway/gateway.log

# Filter for progress only
tail -f /var/log/nfs-gateway/gateway.log | grep -E "(progress|Starting|complete|retry)"

# Watch for errors
tail -f /var/log/nfs-gateway/gateway.log | grep -i error
```

## Stress Tests

### Test 1: Small File Baseline (Warm-up)

```bash
# Create 100MB test file
dd if=/dev/urandom of=/mnt/nfs/test_100mb.dat bs=1M count=100

# Sequential read test
fio --name=small_seq_read --rw=read --bs=1M --size=100M \
    --numjobs=1 --directory=/mnt/nfs --direct=1 \
    --group_reporting --runtime=30s

# Expected: Should complete without issues
```

**Expected Progress Output:**
```
[START] Downloading: test_100mb.dat (100.00 MB)
[PROGRESS] Downloaded: 10.00 MB | Progress: 10.0% | Speed: 15.23 MB/s
[PROGRESS] Downloaded: 20.00 MB | Progress: 20.0% | Speed: 16.45 MB/s
...
[COMPLETE] Downloaded: test_100mb.dat (100.00 MB)
```

### Test 2: Large File Sequential Read (The Failing Test)

```bash
# Create 1GB test file (if not exists)
dd if=/dev/urandom of=/mnt/nfs/test_1gb.dat bs=1M count=1024

# Run the test with monitoring
fio --name=seq_read --rw=read --bs=1M --size=1G \
    --numjobs=1 --directory=/mnt/nfs --direct=1 \
    --group_reporting --time_based --runtime=60s
```

**What to Watch For:**

1. **Progress Updates**: Should see updates every 10MB or 5 seconds
   - If stuck without progress for >30 seconds → Network/COS issue
   - If progress stops at specific percentage → Possible timeout

2. **Throughput**: Should be consistent
   - Good: 10-50 MB/s (depends on network)
   - Warning: <5 MB/s (slow network)
   - Problem: Drops to 0 MB/s (connection issue)

3. **Retries**: May see retry attempts
   - Normal: 1-2 retries on large files
   - Problem: 3+ retries or continuous retries

4. **Errors**: Should not see "unexpected EOF" anymore
   - If you do, note at what progress % it fails

### Test 3: Concurrent Small Files

```bash
# Multiple concurrent readers
fio --name=concurrent_read --rw=read --bs=1M --size=100M \
    --numjobs=5 --directory=/mnt/nfs --direct=1 \
    --group_reporting --runtime=60s
```

**Expected**: Multiple `[START]` and `[PROGRESS]` lines for different files

### Test 4: Cache Performance Test

```bash
# First read (cache miss)
time cat /mnt/nfs/test_1gb.dat > /dev/null

# Second read (should be cache hit)
time cat /mnt/nfs/test_1gb.dat > /dev/null
```

**Expected Progress Output:**
```
First read:
[CACHE MISS] Fetching from COS
[START] Downloading: test_1gb.dat (1024.00 MB)
[PROGRESS] ...

Second read:
[CACHE HIT] Data served from cache
(No download progress - instant)
```

## Diagnostic Scenarios

### Scenario 1: Download Starts But Stalls

**Symptoms:**
```
[START] Downloading: seq_read.0.0 (1024.00 MB)
[PROGRESS] Downloaded: 50.00 MB | Progress: 4.9% | Speed: 12.34 MB/s
[PROGRESS] Downloaded: 100.00 MB | Progress: 9.8% | Speed: 11.23 MB/s
... (no more progress for 60+ seconds)
[ERROR] failed to read object body: unexpected EOF
```

**Diagnosis**: Network timeout or COS connection issue
**Action**: 
- Check network connectivity to IBM COS
- Verify COS endpoint is correct
- Check if firewall is blocking long connections

### Scenario 2: Immediate Failure

**Symptoms:**
```
[START] Downloading: seq_read.0.0 (1024.00 MB)
[ERROR] failed to get object: NoSuchKey
```

**Diagnosis**: File doesn't exist in COS
**Action**: Verify file was created successfully

### Scenario 3: Slow But Steady Progress

**Symptoms:**
```
[PROGRESS] Downloaded: 10.00 MB | Progress: 1.0% | Speed: 2.34 MB/s
[PROGRESS] Downloaded: 20.00 MB | Progress: 2.0% | Speed: 2.12 MB/s
```

**Diagnosis**: Slow network or COS throttling
**Action**: 
- Check network bandwidth
- Verify COS region is optimal
- Consider using private endpoint if available

### Scenario 4: Successful With Retries

**Symptoms:**
```
[PROGRESS] Downloaded: 500.00 MB | Progress: 48.8% | Speed: 15.23 MB/s
[RETRY] Attempt 1 after 1s
[PROGRESS] Downloaded: 510.00 MB | Progress: 49.8% | Speed: 14.56 MB/s
[SUCCESS] Retry succeeded on attempt 1
[COMPLETE] Downloaded: seq_read.0.0 (1024.00 MB)
```

**Diagnosis**: Transient network issue, but retry worked
**Action**: This is normal and expected behavior

## Metrics to Collect

While running tests, collect these metrics:

```bash
# In another terminal
watch -n 5 'curl -s http://localhost:8080/metrics | grep -E "(nfs_request|cache_hit|cache_miss|bytes_read|bytes_written)"'
```

**Key Metrics:**
- `nfs_request_duration_seconds` - Request latency
- `cache_hit_total` - Cache effectiveness
- `bytes_read_total` - Total data transferred
- `nfs_request_errors_total` - Error count

## Troubleshooting Commands

```bash
# Check if gateway is running
ps aux | grep nfs-gateway

# Check NFS mount
mount | grep nfs

# Check network connectivity to COS
ping s3.us-south.cloud-object-storage.appdomain.cloud

# Check gateway resource usage
top -p $(pgrep nfs-gateway)

# Check for file descriptor leaks
lsof -p $(pgrep nfs-gateway) | wc -l

# View recent errors
tail -100 /var/log/nfs-gateway/gateway.log | grep -i error
```

## Success Criteria

✅ **Test Passes If:**
- Progress updates appear regularly (every 5-10 seconds)
- Throughput is consistent (>5 MB/s)
- Download completes within expected time
- No "unexpected EOF" errors
- Retries (if any) succeed
- Clean shutdown with no panics

❌ **Test Fails If:**
- Progress stalls for >60 seconds
- "unexpected EOF" errors persist
- Gateway crashes or panics
- Throughput drops to 0
- All retry attempts fail

## Next Steps Based on Results

### If Test Passes
1. Gradually increase file size (2GB, 5GB)
2. Increase concurrent jobs
3. Run longer duration tests (5+ minutes)
4. Test with mixed read/write operations

### If Test Still Fails
1. **Capture full logs** during failure
2. **Note exact progress** where it fails (MB, %)
3. **Check COS dashboard** for API errors
4. **Verify network path** to COS endpoint
5. **Test with smaller chunks** (500MB files)
6. **Try different COS region** if available

## Report Template

When reporting issues, include:

```
Test: [Test name]
File Size: [Size in MB/GB]
Progress Before Failure: [XX.X%]
Last Throughput: [XX.XX MB/s]
Error Message: [Full error]
Retry Attempts: [Number]
Gateway Version: [Version]
COS Region: [Region]
Network Type: [Public/Private]

Logs:
[Paste relevant log lines]

Metrics:
[Paste metrics output]
```

## Additional Resources

- Full fix documentation: `STRESS_TEST_FIX.md`
- Architecture: `ARCHITECTURE.md`
- Configuration: `configs/config.example.yaml`