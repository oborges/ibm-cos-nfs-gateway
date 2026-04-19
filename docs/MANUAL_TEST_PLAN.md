# IBM COS NFS Gateway: Manual Test Plan

This comprehensive manual testing plan serves to validate all structural bounds bounding the Gateway Daemon natively. 
It covers local POSIX cache efficacy, aggressive background synchronization mechanisms, Staging quotas, and hardware-level catastrophe resilience mechanisms.

Before running these tests, ensure your Gateway is mounted according to the updated README specifications natively leveraging `soft` mounts:
```bash
sudo mount -t nfs -o vers=3,tcp,nolock,soft,timeo=30,retrans=2 localhost:/ /mnt/cos-nfs
```

---

## 1. Local Cache Efficacy & Read-After-Write Consistency

**Description**: Verify that the local Enterprise Staging Cache immediately serves files that were just written without being forced to wait for IBM COS synchronization speeds.  
**Steps**:
1. Run a rapid file write creation targeting the Gateway sequentially using `dd`:
   `dd if=/dev/urandom of=/mnt/cos-nfs/test_cache.bin bs=1M count=250`
2. **Immediately** read the exact file back to an internal pipe bypassing stdout rendering:
   `time cat /mnt/cos-nfs/test_cache.bin > /dev/null`
   
**Expected Result**: The `cat` command should complete practically instantly (verifying GB/s bounds mapped directly from `/tmp/nfs-staging` memory arrays instead of waiting multiple seconds streaming from IBM COS).

---

## 2. Progressive Multipart Synchronization Tracking

**Description**: Verify that S3 `MultipartUploads` actively kick in iteratively whenever files are written natively executing 20MB block splits asynchronously!
**Steps**:
1. Keep an active `ssh` terminal tracking the gateway logs live: 
   `tail -f /tmp/nfs.log | grep -i "multipart"`
2. Open a second native terminal explicitly writing an extremely large file traversing the buffer dynamically:
   `dd if=/dev/urandom of=/mnt/cos-nfs/multipart_test.blob bs=1M count=300`
   
**Expected Result**: While the `dd` command runs, the terminal polling `nfs.log` should emit continuous trace outputs declaring `Successfully uploaded part X for multipart payload` well before the `dd` payload natively finishes evaluating. It must finish with a final `Successfully completed S3 multipart payload` dynamically completing the array securely natively.

---

## 3. Quota Constraint Enforcements & System Stress Testing

**Description**: Verify the Staging Limit bounding constraints triggering Linux-native evaluations gracefully preserving disk systems from completely saturating safely!
**Steps**:
1. Ensure `NFS_GATEWAY_STAGING_ENABLED=true`
2. Temporarily set extreme constraints starting your binary mapping exactly using `MaxStagingSizeGB: 1` inside `configs/config.yaml`.
3. Stop caching sweeps gracefully mapping a manual flood dynamically pushing `2GB`!
   `dd if=/dev/zero of=/mnt/cos-nfs/quota_test.bin bs=1M count=2000`
   
**Expected Result**: The `dd` command should naturally run incredibly smoothly up until passing exactly `1GB` limit dynamically where `nfs-gateway` interceptor naturally trips emitting standard Linux-native `dd: error writing ... No space left on device` (ENOSPC), avoiding entirely crashing the actual Linux root partitions natively!

---

## 4. Staging Service Destruction & Data Resiliency

**Description**: Emulate severe `Out of Memory` memory panics killing the caching daemon dynamically testing whether its `.metadata` JSON tracking systems securely push orphaned buffered bytes up into IBM COS seamlessly on structural reboots!
**Steps**:
1. Write a massive logical evaluation file that stays active natively: 
   `dd if=/dev/urandom of=/mnt/cos-nfs/chaos.bin bs=1M count=80 &`
2. Freeze the Daemon using process signals halting chunk bounds mid streams: 
   `sudo pkill -STOP -f nfs-gateway`
3. Kill the runtime cleanly verifying destruction dynamically: 
   `sudo pkill -9 -f nfs-gateway`
4. Confirm `chaos.bin` does **not** exist exactly matched on IBM COS dynamically logging to the web IBM browser securely natively.
5. Boot Gateway Daemon runtime dynamically natively:
   `sudo env NFS_GATEWAY_STAGING_ENABLED=true ... ./bin/nfs-gateway --config configs/config.yaml`

**Expected Result**:
Upon reissuing the initial gateway, `tail -f nfs.log` should emit traces showcasing `Orphaned staging file recovered natively`. 
The `chaos.bin` byte streams should seamlessly continue uploading into IBM COS exactly executing across mapped JSON configurations without losing structural data dynamically organically.

---

## 5. Linux Kernel System Hanging Mitigation 

**Description**: Validate my `soft,timeo` OS mounting upgrades aggressively mitigating total VM destruction bounds!  
**Steps**:
1. Evaluate `mount` ensuring `soft,timeo=30,retrans=2` bounds natively populate the VM mounts smoothly validating the kernel strings.
2. Initialize an active process: e.g. `while true; do ls /mnt/cos-nfs; sleep 1; done &`
3. Terminate the gateway effectively: `sudo pkill -STOP -f nfs-gateway`

**Expected Result**:
Instead of completely permanently locking your SSH console into `D-state` uninterruptible kernel traps blocking your entire OS terminal natively perfectly indefinitely, the `ls` polling routine will smoothly yield `Input/output error` (EIO) organically after exactly a 6-second delay, cleanly decoupling the OS from the Gateway crashes successfully natively!
