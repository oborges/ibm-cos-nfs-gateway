# MVP Implementation Plan: Staging-Based Write Path

**Version**: 1.0  
**Date**: 2026-04-11  
**Target**: First working vertical slice  
**Estimated Duration**: 2-3 weeks

---

## 1. MVP Scope

### What We're Building

A **minimal viable staging-based write path** that proves the architecture solves the root cause:
- Reopen/close churn no longer destroys buffering
- Writes accumulate in local staging across multiple file handles
- Background sync to object storage (simple, safe implementation)
- Read-after-write correctness for dirty files

### What We're NOT Building Yet

- Full production observability suite
- Advanced multipart optimization
- Complex failure recovery scenarios
- Performance tuning and optimization
- Distributed tracing
- Advanced caching strategies
- Rename/delete handling in staging

### Success Criteria

✅ A 16MB sequential write with reopen/close churn accumulates in staging  
✅ No full-object GET+PUT on every 1MB write  
✅ Dirty data readable before sync completes  
✅ Background sync eventually commits to COS  
✅ Feature flag allows switching between old/new paths  
✅ System remains stable during transition  

---

## 2. Code Impact Map

### New Packages/Files

```
internal/staging/
├── manager.go           # StagingManager - manages staging files
├── session.go           # WriteSession - path-scoped write state
├── sync_worker.go       # SyncWorker - background sync to COS
└── metadata.go          # DirtyFileIndex - tracks dirty files

internal/feature/
└── flags.go             # Feature flag system
```

### Modified Files

```
internal/nfs/handler.go  # Wire in staging path, feature flag
internal/config/config.go # Add staging configuration
configs/config.yaml      # Add staging config section
cmd/nfs-gateway/main.go  # Initialize staging components
```

### New Structs/Interfaces

```go
// Core components
type StagingManager struct { ... }
type WriteSession struct { ... }
type SyncWorker struct { ... }
type DirtyFileIndex struct { ... }

// Feature flag
type FeatureFlags struct {
    UseStagingPath bool
}
```

### Deprecated/Bypassed (for staging path only)

```go
// These remain for legacy path, bypassed when staging enabled
type WriteBuffer struct { ... }  // Replaced by WriteSession
// Direct COS writes in Write()    // Replaced by staging + async sync
```

---

## 3. Proposed New Components

### 3.1 StagingManager

**Responsibilities**:
- Manage staging directory lifecycle
- Create/retrieve staging files for paths
- Track active sessions
- Coordinate with sync worker

**Interface**:
```go
type StagingManager struct {
    stagingRoot   string
    sessions      map[string]*WriteSession  // path → session
    dirtyIndex    *DirtyFileIndex
    syncWorker    *SyncWorker
    mu            sync.RWMutex
}

func NewStagingManager(config *StagingConfig) *StagingManager
func (sm *StagingManager) GetOrCreateSession(path string) (*WriteSession, error)
func (sm *StagingManager) ReleaseSession(path string)
func (sm *StagingManager) MarkDirty(path string)
func (sm *StagingManager) Shutdown(ctx context.Context) error
```

**Lifecycle**:
1. Created at gateway startup
2. Lives for entire process lifetime
3. Shutdown flushes all dirty files

**Thread-Safety**: RWMutex protects sessions map

### 3.2 WriteSession

**Responsibilities**:
- Represent active write session for a file path
- Manage staging file handle
- Track dirty state and size
- Survive reopen/close cycles

**Interface**:
```go
type WriteSession struct {
    Path          string
    StagingPath   string
    File          *os.File        // Staging file handle
    Size          int64
    IsDirty       bool
    RefCount      int32           // Number of active handles
    LastAccess    time.Time
    CreatedAt     time.Time
    mu            sync.Mutex
}

func NewWriteSession(path string, stagingPath string) (*WriteSession, error)
func (ws *WriteSession) Write(offset int64, data []byte) (int, error)
func (ws *WriteSession) Read(offset int64, length int64) ([]byte, error)
func (ws *WriteSession) Sync() error
func (ws *WriteSession) Close() error
```

**Lifecycle**:
1. Created on first open of path
2. Reused on subsequent opens (RefCount++)
3. Persists after close if dirty
4. Cleaned up after successful sync + idle timeout

**Thread-Safety**: Mutex protects all operations

### 3.3 SyncWorker

**Responsibilities**:
- Background goroutine that syncs dirty files to COS
- Simple queue-based processing
- Retry on failure with backoff
- Update metadata on success

**Interface**:
```go
type SyncWorker struct {
    stagingMgr    *StagingManager
    dirtyIndex    *DirtyFileIndex
    cosClient     *cos.Client
    config        *SyncConfig
    queue         chan string     // Paths to sync
    stopCh        chan struct{}
    wg            sync.WaitGroup
}

func NewSyncWorker(config *SyncConfig, stagingMgr *StagingManager, cos *cos.Client) *SyncWorker
func (sw *SyncWorker) Start()
func (sw *SyncWorker) EnqueueSync(path string)
func (sw *SyncWorker) Shutdown(ctx context.Context) error
func (sw *SyncWorker) syncFile(path string) error
```

**Lifecycle**:
1. Started at gateway startup
2. Runs continuously in background
3. Shutdown drains queue and syncs all dirty files

**Thread-Safety**: Channel-based communication

### 3.4 DirtyFileIndex

**Responsibilities**:
- Track which files are dirty (not yet synced)
- Provide list of dirty files for sync
- Update state after successful sync
- Simple in-memory index for MVP

**Interface**:
```go
type DirtyFileIndex struct {
    dirty         map[string]*DirtyFileMetadata  // path → metadata
    mu            sync.RWMutex
}

type DirtyFileMetadata struct {
    Path          string
    Size          int64
    LastModified  time.Time
    SyncAttempts  int
}

func NewDirtyFileIndex() *DirtyFileIndex
func (dfi *DirtyFileIndex) MarkDirty(path string, size int64)
func (dfi *DirtyFileIndex) MarkClean(path string)
func (dfi *DirtyFileIndex) GetDirtyFiles() []*DirtyFileMetadata
func (dfi *DirtyFileIndex) IsDirty(path string) bool
```

**Lifecycle**:
1. Created at gateway startup
2. Lives in memory only (MVP - no persistence)
3. Rebuilt on restart by scanning staging directory

**Thread-Safety**: RWMutex protects dirty map

### 3.5 Feature Flag System

**Responsibilities**:
- Control whether staging path is enabled
- Allow runtime switching (via config reload)
- Default to legacy path for safety

**Interface**:
```go
type FeatureFlags struct {
    UseStagingPath bool
}

func LoadFeatureFlags(config *config.Config) *FeatureFlags
func (ff *FeatureFlags) IsStagingEnabled() bool
```

**Lifecycle**:
1. Loaded at startup from config
2. Can be reloaded without restart (future)

**Thread-Safety**: Read-only after initialization (MVP)

---

## 4. Execution Flow

### 4.1 Open Existing File (Write Mode)

```
1. Client: open("/bucket/file.txt", O_WRONLY)
2. NFS Handler: OpenFile()
3. Check feature flag: if !UseStagingPath → legacy path
4. StagingManager.GetOrCreateSession("/bucket/file.txt")
   a. Check if session exists in memory
   b. If yes: increment RefCount, return session
   c. If no: create new session
      - Generate staging path: /var/staging/active/<hash>.data
      - Check if staging file exists (from previous run)
      - If exists: open for append
      - If not: check if file exists in COS
        * If yes: download to staging (for read-modify-write)
        * If no: create empty staging file
      - Create WriteSession object
      - Add to sessions map
5. Create COSFile with session reference
6. Return file handle to client
```

### 4.2 Open New File (Create)

```
1. Client: open("/bucket/newfile.txt", O_WRONLY|O_CREAT)
2. NFS Handler: OpenFile()
3. Check feature flag: if !UseStagingPath → legacy path
4. StagingManager.GetOrCreateSession("/bucket/newfile.txt")
   a. Session doesn't exist
   b. Create new session
      - Generate staging path: /var/staging/active/<hash>.data
      - Create empty staging file
      - Create WriteSession object
      - Mark as new file (no COS download needed)
      - Add to sessions map
5. Create COSFile with session reference
6. Return file handle to client
```

### 4.3 Write 1 MiB

```
1. Client: write(fd, buffer, 1048576)
2. NFS Handler: COSFile.Write()
3. WriteSession.Write(offset, data)
   a. Seek to offset in staging file
   b. Write data to staging file
   c. Update session.Size
   d. Mark session.IsDirty = true
4. StagingManager.MarkDirty(path)
   a. DirtyFileIndex.MarkDirty(path, size)
5. Check sync trigger (config-driven):
   a. If size >= sync_threshold → EnqueueSync(path)
   b. If time >= max_dirty_age → EnqueueSync(path)
   c. Else: continue (sync later)
6. Return success to client immediately
```

### 4.4 Close

```
1. Client: close(fd)
2. NFS Handler: COSFile.Close()
3. WriteSession: decrement RefCount
4. If RefCount == 0 and config.sync_on_close:
   a. SyncWorker.EnqueueSync(path)
5. Session persists in memory (still dirty)
6. Return success to client immediately
```

### 4.5 Reopen Same File

```
1. Client: open("/bucket/file.txt", O_WRONLY)
2. NFS Handler: OpenFile()
3. StagingManager.GetOrCreateSession("/bucket/file.txt")
   a. Session exists in memory
   b. Increment RefCount
   c. Return existing session
4. Create new COSFile with same session reference
5. Return file handle to client

KEY: Session persists, buffered data not lost!
```

### 4.6 Read Dirty File

```
1. Client: read(fd, buffer, 4096)
2. NFS Handler: COSFile.Read()
3. Check if file is dirty:
   a. DirtyFileIndex.IsDirty(path) → true
4. Read from staging:
   a. WriteSession.Read(offset, length)
   b. Read from staging file
   c. Return data to client
5. If not dirty:
   a. Read from COS (legacy path)

KEY: Read-after-write correctness guaranteed!
```

### 4.7 Background Sync

```
1. SyncWorker goroutine running continuously
2. Periodic check (every sync_interval):
   a. DirtyFileIndex.GetDirtyFiles()
   b. For each dirty file:
      - Check if should sync (size/age thresholds)
      - If yes: EnqueueSync(path)
3. Process sync queue:
   a. Dequeue path
   b. Get WriteSession from StagingManager
   c. Lock session (prevent concurrent writes)
   d. Read data from staging file
   e. Upload to COS:
      - If size < multipart_threshold: single PUT
      - If size >= multipart_threshold: multipart upload
   f. On success:
      - DirtyFileIndex.MarkClean(path)
      - Update session state
      - Optionally remove staging file
   g. On failure:
      - Retry with exponential backoff
      - After max_retries: log error, keep dirty
   h. Unlock session
```

### 4.8 Crash Before Sync

```
1. Process crashes while file is dirty
2. Staging file remains on disk
3. DirtyFileIndex lost (in-memory only for MVP)
4. On restart:
   a. Scan staging directory
   b. For each staging file:
      - Parse filename to get path
      - Check if file exists in COS
      - If COS version is older or missing:
        * Rebuild DirtyFileIndex entry
        * EnqueueSync(path)
      - If COS version is newer:
        * Remove stale staging file
5. Resume normal operation

KEY: Data not lost, sync resumes automatically!
```

---

## 5. Persistence Model

### In-Memory Only (MVP)

```
- DirtyFileIndex (dirty file list)
- WriteSession objects (active sessions)
- Sync queue state
```

**Rationale**: Simplifies MVP, acceptable for proof-of-concept. Rebuilt on restart by scanning staging directory.

### Persisted on Disk

```
- Staging files (/var/staging/active/<hash>.data)
- Staging file metadata (embedded in filename or sidecar .meta file)
```

**Format**:
```
Filename: <sha256-of-path>.data
Sidecar: <sha256-of-path>.meta (JSON)
{
  "path": "/bucket/file.txt",
  "size": 1048576,
  "mtime": "2026-04-11T03:00:00Z",
  "created": "2026-04-11T02:55:00Z"
}
```

### Reconstructed on Startup

```
- DirtyFileIndex: rebuilt by scanning /var/staging/active/
- WriteSession: created on-demand when file is reopened
```

**Recovery Process**:
```go
func (sm *StagingManager) RecoverFromDisk() error {
    files, err := os.ReadDir(sm.stagingRoot + "/active")
    if err != nil {
        return err
    }
    
    for _, file := range files {
        if !strings.HasSuffix(file.Name(), ".data") {
            continue
        }
        
        // Read metadata
        meta := sm.readMetadata(file.Name())
        
        // Check if needs sync
        if sm.needsSync(meta) {
            sm.dirtyIndex.MarkDirty(meta.Path, meta.Size)
            sm.syncWorker.EnqueueSync(meta.Path)
        }
    }
    
    return nil
}
```

---

## 6. Minimal On-Disk Layout

```
/var/staging/nfs-gateway/
├── active/                          # Active/dirty files
│   ├── a1b2c3d4.data               # Staging file (hash of path)
│   ├── a1b2c3d4.meta               # Metadata JSON
│   ├── e5f6g7h8.data
│   └── e5f6g7h8.meta
│
└── config/                          # Configuration
    └── feature-flags.json           # Feature flag state
```

### Filename Convention

```go
func stagingFilename(path string) string {
    hash := sha256.Sum256([]byte(path))
    return hex.EncodeToString(hash[:16]) + ".data"
}

// Example:
// path: "/bucket/documents/report.pdf"
// filename: "a1b2c3d4e5f6g7h8.data"
```

### Metadata File Format

```json
{
  "version": 1,
  "path": "/bucket/documents/report.pdf",
  "size": 1048576,
  "mtime": "2026-04-11T03:00:00Z",
  "created": "2026-04-11T02:55:00Z",
  "dirty": true,
  "sync_attempts": 0,
  "last_sync_attempt": null
}
```

### Directory Cleanup

```
- Clean files removed after successful sync (configurable)
- Stale files (>24h old, synced) removed on startup
- Failed files kept for manual inspection
```

---

## 7. Acceptance Criteria

### AC1: Reopen/Close Churn Doesn't Lose Buffer

**Test**:
```bash
# Write 16MB with reopen every 1MB
for i in {1..16}; do
  dd if=/dev/zero bs=1M count=1 >> /mnt/nfs/testfile
done
```

**Expected**:
- Single staging file accumulates all 16MB
- Single sync to COS after threshold
- No 16 separate COS uploads

**Measurement**: Check COS API call count (should be 1, not 16)

### AC2: No Full-Object GET+PUT Per Write

**Test**:
```bash
# Create 10MB file
dd if=/dev/zero of=/mnt/nfs/bigfile bs=1M count=10

# Append 1MB
dd if=/dev/zero bs=1M count=1 >> /mnt/nfs/bigfile
```

**Expected**:
- No GET request to download existing 10MB
- Append happens in staging file
- Single PUT of 11MB on sync

**Measurement**: Check COS API logs (no GET before PUT)

### AC3: Dirty Data Readable Before Sync

**Test**:
```bash
# Write data
echo "test data" > /mnt/nfs/testfile

# Read immediately (before sync completes)
cat /mnt/nfs/testfile
```

**Expected**:
- Read returns "test data"
- Data comes from staging, not COS

**Measurement**: Verify read happens before sync completes

### AC4: Background Sync Eventually Commits

**Test**:
```bash
# Write file
dd if=/dev/zero of=/mnt/nfs/testfile bs=1M count=5

# Wait for sync
sleep 10

# Verify in COS
aws s3 ls s3://bucket/testfile
```

**Expected**:
- File appears in COS within sync_interval
- File size matches staging file

**Measurement**: Check COS object exists and size correct

### AC5: Feature Flag Switches Paths

**Test**:
```yaml
# Config with staging disabled
staging:
  enabled: false
```

**Expected**:
- Writes use legacy path (direct to COS)
- No staging files created

**Test**:
```yaml
# Config with staging enabled
staging:
  enabled: true
```

**Expected**:
- Writes use staging path
- Staging files created

**Measurement**: Check which code path executes

### AC6: System Remains Stable

**Test**:
```bash
# Run for 1 hour with mixed workload
fio --name=stability --rw=randrw --bs=64K --size=100M \
    --numjobs=4 --runtime=3600s --directory=/mnt/nfs
```

**Expected**:
- No crashes or panics
- No memory leaks
- No goroutine leaks
- Consistent performance

**Measurement**: Monitor memory, goroutines, error logs

---

## 8. Test Plan

### 8.1 Unit Tests

```go
// internal/staging/manager_test.go
func TestStagingManager_GetOrCreateSession(t *testing.T)
func TestStagingManager_ReleaseSession(t *testing.T)
func TestStagingManager_MarkDirty(t *testing.T)

// internal/staging/session_test.go
func TestWriteSession_Write(t *testing.T)
func TestWriteSession_Read(t *testing.T)
func TestWriteSession_Sync(t *testing.T)
func TestWriteSession_RefCount(t *testing.T)

// internal/staging/sync_worker_test.go
func TestSyncWorker_EnqueueSync(t *testing.T)
func TestSyncWorker_SyncFile(t *testing.T)
func TestSyncWorker_RetryOnFailure(t *testing.T)

// internal/staging/metadata_test.go
func TestDirtyFileIndex_MarkDirty(t *testing.T)
func TestDirtyFileIndex_MarkClean(t *testing.T)
func TestDirtyFileIndex_GetDirtyFiles(t *testing.T)
```

### 8.2 Integration Tests

```go
// test/integration/staging_test.go

func TestStagingPath_WriteAndSync(t *testing.T) {
    // 1. Write file to staging
    // 2. Verify staging file created
    // 3. Wait for sync
    // 4. Verify COS object created
    // 5. Verify staging file cleaned up
}

func TestStagingPath_ReopenChurn(t *testing.T) {
    // 1. Open file
    // 2. Write 1MB
    // 3. Close file
    // 4. Repeat 16 times
    // 5. Verify single staging file
    // 6. Verify single COS upload
}

func TestStagingPath_ReadAfterWrite(t *testing.T) {
    // 1. Write data to file
    // 2. Read data immediately
    // 3. Verify data matches
    // 4. Verify read from staging (not COS)
}

func TestStagingPath_ConcurrentWrites(t *testing.T) {
    // 1. Open file in 2 goroutines
    // 2. Write from both concurrently
    // 3. Verify no corruption
    // 4. Verify correct final size
}
```

### 8.3 Benchmark Test

```go
// test/benchmark/staging_bench_test.go

func BenchmarkStagingPath_SequentialWrite(b *testing.B) {
    // Measure throughput of sequential writes
    // Compare staging path vs legacy path
    // Target: >20 MB/s with staging
}

func BenchmarkStagingPath_ReopenChurn(b *testing.B) {
    // Measure performance with reopen/close churn
    // Compare staging path vs legacy path
    // Target: 10x improvement with staging
}
```

### 8.4 Failure/Recovery Test

```go
// test/integration/recovery_test.go

func TestRecovery_CrashBeforeSync(t *testing.T) {
    // 1. Write file to staging
    // 2. Kill process (simulate crash)
    // 3. Restart process
    // 4. Verify staging file recovered
    // 5. Verify sync resumes
    // 6. Verify COS object created
}

func TestRecovery_ScanStagingOnStartup(t *testing.T) {
    // 1. Create staging files manually
    // 2. Start process
    // 3. Verify DirtyFileIndex rebuilt
    // 4. Verify syncs enqueued
}
```

---

## 9. Risk List

### Risk 1: Staging Disk Space Exhaustion

**Description**: Staging directory fills up, no space for new writes

**Mitigation**:
- Config: `max_staging_size_gb` (default: 10GB)
- Monitor staging directory size
- Evict clean files when approaching limit
- Fail writes gracefully if full

**Detection**: Disk space monitoring, error logs

### Risk 2: Sync Worker Falls Behind

**Description**: Writes faster than sync can keep up, dirty files accumulate

**Mitigation**:
- Config: `sync_worker_count` (default: 4)
- Config: `max_dirty_files` (default: 1000)
- Monitor sync queue depth
- Apply backpressure if queue too deep

**Detection**: Metric: `staging_dirty_files_count`

### Risk 3: Crash Loses In-Memory State

**Description**: DirtyFileIndex lost on crash, some files not synced

**Mitigation**:
- Scan staging directory on startup
- Rebuild DirtyFileIndex from disk
- Resume syncs automatically
- Accept temporary inconsistency (MVP)

**Detection**: Startup logs show recovery

### Risk 4: Concurrent Access Corruption

**Description**: Multiple handles writing to same staging file cause corruption

**Mitigation**:
- Mutex in WriteSession protects all operations
- RefCount tracks active handles
- Test concurrent access thoroughly

**Detection**: Integration tests, corruption detection

### Risk 5: Feature Flag Confusion

**Description**: Unclear which path is active, mixed behavior

**Mitigation**:
- Clear logging at startup: "Staging path: ENABLED/DISABLED"
- Metric: `staging_path_enabled` (0 or 1)
- Config validation prevents invalid states
- Default to legacy path for safety

**Detection**: Startup logs, metrics dashboard

---

## 10. Step-by-Step Implementation Order

### Milestone 1: Foundation (Week 1, Days 1-2)

**Goal**: Basic structure in place, compiles

**Tasks**:
1. Create `internal/staging/` package structure
2. Add staging config to `internal/config/config.go`:
   ```yaml
   staging:
     enabled: false  # Default off
     root_dir: /var/staging/nfs-gateway
     sync_interval: 30s
     sync_threshold_mb: 10
     max_staging_size_gb: 10
   ```
3. Create `internal/feature/flags.go` with `UseStagingPath` flag
4. Wire feature flag into `cmd/nfs-gateway/main.go`
5. Add feature flag check in `internal/nfs/handler.go:OpenFile()`
6. **Commit**: "Add staging config and feature flag foundation"

### Milestone 2: Staging Manager (Week 1, Days 3-4)

**Goal**: StagingManager can create/manage sessions

**Tasks**:
1. Implement `internal/staging/manager.go`:
   - `NewStagingManager()`
   - `GetOrCreateSession()`
   - `ReleaseSession()`
   - `Shutdown()`
2. Implement `internal/staging/session.go`:
   - `NewWriteSession()`
   - `Write()`
   - `Read()`
   - `Close()`
3. Create staging directory on startup
4. Wire StagingManager into `cmd/nfs-gateway/main.go`
5. Unit tests for StagingManager and WriteSession
6. **Commit**: "Implement StagingManager and WriteSession"

### Milestone 3: Write Path Integration (Week 1, Day 5)

**Goal**: Writes go to staging when flag enabled

**Tasks**:
1. Modify `internal/nfs/handler.go:OpenFile()`:
   - Check feature flag
   - If enabled: call `StagingManager.GetOrCreateSession()`
   - Store session reference in COSFile
2. Modify `internal/nfs/handler.go:Write()`:
   - Check if session exists
   - If yes: call `session.Write()`
   - If no: legacy path
3. Modify `internal/nfs/handler.go:Close()`:
   - Call `StagingManager.ReleaseSession()`
4. Integration test: write to staging
5. **Commit**: "Integrate staging path into write operations"

### Milestone 4: Read Path Integration (Week 2, Days 1-2)

**Goal**: Reads of dirty files come from staging

**Tasks**:
1. Implement `internal/staging/metadata.go`:
   - `NewDirtyFileIndex()`
   - `MarkDirty()`
   - `MarkClean()`
   - `IsDirty()`
2. Wire DirtyFileIndex into StagingManager
3. Modify `internal/nfs/handler.go:Read()`:
   - Check if file is dirty
   - If yes: read from session
   - If no: legacy path (COS)
4. Integration test: read-after-write
5. **Commit**: "Implement read routing for dirty files"

### Milestone 5: Sync Worker (Week 2, Days 3-4)

**Goal**: Background sync to COS works

**Tasks**:
1. Implement `internal/staging/sync_worker.go`:
   - `NewSyncWorker()`
   - `Start()` (goroutine)
   - `EnqueueSync()`
   - `syncFile()` (upload to COS)
   - `Shutdown()`
2. Wire SyncWorker into `cmd/nfs-gateway/main.go`
3. Add sync triggers in `Write()`:
   - Size threshold
   - Time threshold (periodic check)
4. Update DirtyFileIndex on sync success
5. Integration test: write + wait + verify COS
6. **Commit**: "Implement background sync worker"

### Milestone 6: Crash Recovery (Week 2, Day 5)

**Goal**: System recovers from crash

**Tasks**:
1. Implement `StagingManager.RecoverFromDisk()`:
   - Scan staging directory
   - Rebuild DirtyFileIndex
   - Enqueue syncs
2. Call recovery on startup
3. Add metadata sidecar files (.meta)
4. Integration test: crash + restart + verify recovery
5. **Commit**: "Implement crash recovery from staging directory"

### Milestone 7: Testing & Validation (Week 3, Days 1-3)

**Goal**: All acceptance criteria pass

**Tasks**:
1. Run all unit tests
2. Run all integration tests
3. Run benchmark tests
4. Run failure/recovery tests
5. Verify all 6 acceptance criteria
6. Fix any issues found
7. **Commit**: "Complete MVP testing and validation"

### Milestone 8: Documentation & Deployment (Week 3, Days 4-5)

**Goal**: Ready for production trial

**Tasks**:
1. Update README with staging path documentation
2. Add configuration examples
3. Add troubleshooting guide
4. Create deployment checklist
5. Deploy to test environment
6. Run stress tests
7. Monitor for 24 hours
8. **Commit**: "MVP ready for production trial"

---

## 11. Configuration Example

```yaml
# configs/config.yaml

# Feature flags
features:
  staging_path: false  # Set to true to enable staging path

# Staging configuration
staging:
  # Root directory for staging files
  root_dir: /var/staging/nfs-gateway
  
  # Sync triggers
  sync_interval: 30s           # Check for dirty files every 30s
  sync_threshold_mb: 10        # Sync files when they reach 10MB
  max_dirty_age: 5m            # Sync files dirty for >5 minutes
  sync_on_close: false         # Don't sync immediately on close
  
  # Resource limits
  max_staging_size_gb: 10      # Max staging directory size
  max_dirty_files: 1000        # Max number of dirty files
  
  # Sync worker
  sync_worker_count: 4         # Number of background sync workers
  sync_queue_size: 100         # Max sync queue depth
  
  # Retry policy
  max_sync_retries: 3          # Max retry attempts per file
  retry_backoff_initial: 1s    # Initial retry backoff
  retry_backoff_max: 60s       # Max retry backoff
  
  # Cleanup
  clean_after_sync: true       # Remove staging file after successful sync
  stale_file_age: 24h          # Remove stale files older than 24h
```

---

## 12. Success Metrics

### Performance Metrics

- **Write Throughput**: >20 MB/s (vs current 1-2 MB/s)
- **Sync Latency**: <5s for 10MB file
- **COS API Calls**: 90% reduction for sequential writes

### Reliability Metrics

- **Crash Recovery**: 100% success rate
- **Data Loss**: 0% (all fsynced data survives)
- **Sync Success Rate**: >99%

### Operational Metrics

- **Staging Disk Usage**: <10GB
- **Dirty File Count**: <100 (steady state)
- **Sync Queue Depth**: <10 (steady state)

---

## 13. Rollout Plan

### Phase 1: Internal Testing (Week 3)
- Enable staging path in test environment
- Run stress tests for 24 hours
- Monitor metrics and logs
- Fix any issues found

### Phase 2: Canary Deployment (Week 4)
- Enable staging path for 10% of traffic
- Monitor for 48 hours
- Compare metrics with legacy path
- Rollback if issues found

### Phase 3: Gradual Rollout (Week 5-6)
- Increase to 50% of traffic
- Monitor for 1 week
- Increase to 100% if stable
- Keep legacy path as fallback

### Phase 4: Deprecate Legacy Path (Week 7+)
- After 2 weeks of stable operation
- Remove legacy code path
- Staging path becomes default

---

## 14. Next Steps After MVP

Once MVP is validated:

1. **Add Persistence**: Journal for DirtyFileIndex
2. **Add Observability**: Prometheus metrics, structured logging
3. **Optimize Multipart**: Concurrent part uploads
4. **Add Rename/Delete**: Handle in staging layer
5. **Add Eviction**: LRU eviction when disk full
6. **Add Monitoring**: Dashboards and alerts
7. **Performance Tuning**: Based on production data

---

## Appendix A: Code Skeleton

### internal/staging/manager.go

```go
package staging

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "time"
)

type StagingManager struct {
    config      *StagingConfig
    stagingRoot string
    sessions    map[string]*WriteSession
    dirtyIndex  *DirtyFileIndex
    syncWorker  *SyncWorker
    mu          sync.RWMutex
}

type StagingConfig struct {
    RootDir           string
    SyncInterval      time.Duration
    SyncThresholdMB   int64
    MaxDirtyAge       time.Duration
    SyncOnClose       bool
    MaxStagingSizeGB  int64
    MaxDirtyFiles     int
    SyncWorkerCount   int
    SyncQueueSize     int
}

func NewStagingManager(config *StagingConfig, cosClient *cos.Client) (*StagingManager, error) {
    // Create staging directory
    if err := os.MkdirAll(filepath.Join(config.RootDir, "active"), 0755); err != nil {
        return nil, err
    }
    
    sm := &StagingManager{
        config:      config,
        stagingRoot: config.RootDir,
        sessions:    make(map[string]*WriteSession),
        dirtyIndex:  NewDirtyFileIndex(),
    }
    
    // Create sync worker
    sm.syncWorker = NewSyncWorker(config, sm, cosClient)
    
    // Recover from disk
    if err := sm.RecoverFromDisk(); err != nil {
        return nil, err
    }
    
    // Start sync worker
    sm.syncWorker.Start()
    
    return sm, nil
}

func (sm *StagingManager) GetOrCreateSession(path string) (*WriteSession, error) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    
    // Check if session exists
    if session, exists := sm.sessions[path]; exists {
        session.RefCount++
        session.LastAccess = time.Now()
        return session, nil
    }
    
    // Create new session
    stagingPath := sm.stagingFilePath(path)
    session, err := NewWriteSession(path, stagingPath)
    if err != nil {
        return nil, err
    }
    
    sm.sessions[path] = session
    return session, nil
}

func (sm *StagingManager) ReleaseSession(path string) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    
    session, exists := sm.sessions[path]
    if !exists {
        return
    }
    
    session.RefCount--
    session.LastAccess = time.Now()
    
    // Keep session alive even if RefCount == 0 (for reopen)
    // Cleanup happens after sync + idle timeout
}

func (sm *StagingManager) MarkDirty(path string, size int64) {
    sm.dirtyIndex.MarkDirty(path, size)
    
    // Check if should sync now
    if size >= sm.config.SyncThresholdMB*1024*1024 {
        sm.syncWorker.EnqueueSync(path)
    }
}

func (sm *StagingManager) stagingFilePath(path string) string {
    hash := sha256.Sum256([]byte(path))
    filename := hex.EncodeToString(hash[:16]) + ".data"
    return filepath.Join(sm.stagingRoot, "active", filename)
}

func (sm *StagingManager) RecoverFromDisk() error {
    // Scan staging directory
    // Rebuild DirtyFileIndex
    // Enqueue syncs
    // TODO: Implement
    return nil
}

func (sm *StagingManager) Shutdown(ctx context.Context) error {
    // Stop sync worker
    return sm.syncWorker.Shutdown(ctx)
}
```

### internal/staging/session.go

```go
package staging

import (
    "os"
    "sync"
    "time"
)

type WriteSession struct {
    Path        string
    StagingPath string
    File        *os.File
    Size        int64
    IsDirty     bool
    RefCount    int32
    LastAccess  time.Time
    CreatedAt   time.Time
    mu          sync.Mutex
}

func NewWriteSession(path string, stagingPath string) (*WriteSession, error) {
    // Open or create staging file
    file, err := os.OpenFile(stagingPath, os.O_RDWR|os.O_CREATE, 0644)
    if err != nil {
        return nil, err
    }
    
    // Get current size
    stat, err := file.Stat()
    if err != nil {
        file.Close()
        return nil, err
    }
    
    return &WriteSession{
        Path:        path,
        StagingPath: stagingPath,
        File:        file,
        Size:        stat.Size(),
        IsDirty:     false,
        RefCount:    1,
        LastAccess:  time.Now(),
        CreatedAt:   time.Now(),
    }, nil
}

func (ws *WriteSession) Write(offset int64, data []byte) (int, error) {
    ws.mu.Lock()
    defer ws.mu.Unlock()
    
    // Seek to offset
    if _, err := ws.File.Seek(offset, 0); err != nil {
        return 0, err
    }
    
    // Write data
    n, err := ws.File.Write(data)
    if err != nil {
        return n, err
    }
    
    // Update size
    newSize := offset + int64(n)
    if newSize > ws.Size {
        ws.Size = newSize
    }
    
    ws.IsDirty = true
    ws.LastAccess = time.Now()
    
    return n, nil
}

func (ws *WriteSession) Read(offset int64, length int64) ([]byte, error) {
    ws.mu.Lock()
    defer ws.mu.Unlock()
    
    // Seek to offset
    if _, err := ws.File.Seek(offset, 0); err != nil {
        return nil, err
    }
    
    // Read data
    buf := make([]byte, length)
    n, err := ws.File.Read(buf)
    if err != nil {
        return nil, err
    }
    
    ws.LastAccess = time.Now()
    
    return buf[:n], nil
}

func (ws *WriteSession) Sync() error {
    ws.mu.Lock()
    defer ws.mu.Unlock()
    
    return ws.File.Sync()
}

func (ws *WriteSession) Close() error {
    ws.mu.Lock()
    defer ws.mu.Unlock()
    
    return ws.File.Close()
}
```

---

**End of MVP Implementation Plan**