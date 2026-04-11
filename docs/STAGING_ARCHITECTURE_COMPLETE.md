# Production-Grade Staging Architecture for NFS-to-Object-Storage Gateway

**Version**: 2.0  
**Date**: 2026-04-11  
**Status**: Production Design Specification  
**Author**: Architecture Team  
**Alignment**: AWS S3 Files Architecture Principles

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Complete Write Path with Staging Layer](#2-complete-write-path-with-staging-layer)
3. [Read Routing Between Staging and Object Storage](#3-read-routing-between-staging-and-object-storage)
4. [Staging Directory Layout and File Management](#4-staging-directory-layout-and-file-management)
5. [Synchronization Strategy with Background Workers](#5-synchronization-strategy-with-background-workers)
6. [Metadata and State Model with Crash Recovery](#6-metadata-and-state-model-with-crash-recovery)
7. [Failure Scenarios and Recovery Procedures](#7-failure-scenarios-and-recovery-procedures)
8. [Concurrency Model](#8-concurrency-model)
9. [Performance Strategy](#9-performance-strategy)
10. [Complete Configuration Schema](#10-complete-configuration-schema)
11. [Observability Plan](#11-observability-plan)
12. [Phased Migration Plan](#12-phased-migration-plan)
13. [Open Design Tradeoffs](#13-open-design-tradeoffs)

---

## 1. Executive Summary

### 1.1 Problem Statement

The current NFS-to-Object-Storage gateway architecture exhibits critical performance limitations:

- **Write Performance**: 1-2 MB/s (target: 50+ MB/s)
- **Root Cause**: Handle-scoped write buffers destroyed on frequent reopen/close cycles
- **Impact**: Every small write triggers expensive read-modify-write operations on object storage
- **Business Impact**: Unusable for production workloads requiring high-throughput file operations

### 1.2 Proposed Solution

A **production-grade staging architecture** inspired by AWS S3 Files that fundamentally decouples NFS operations from object storage operations:

```
┌─────────────────────────────────────────────────────────────────┐
│                         NFS Clients                              │
└────────────────────────────┬────────────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────────────┐
│                    High-Performance Staging Layer                │
│  • Path-scoped (not handle-scoped)                              │
│  • Local filesystem or memory-backed                             │
│  • Immediate write acknowledgment                                │
│  • Survives handle lifecycle                                     │
└────────────────────────────┬────────────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────────────┐
│              Asynchronous Synchronization Engine                 │
│  • Background workers                                            │
│  • Configurable sync policies                                    │
│  • Intelligent batching                                          │
│  • Crash recovery                                                │
└────────────────────────────┬────────────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────────────┐
│                    Object Storage (IBM COS)                      │
│  • Authoritative durable store                                   │
│  • Eventually consistent                                         │
│  • Multipart upload support                                      │
└─────────────────────────────────────────────────────────────────┘
```

### 1.3 Key Benefits

| Metric | Current | Target | Improvement |
|--------|---------|--------|-------------|
| Sequential Write | 1-2 MB/s | 50-100 MB/s | 25-50x |
| Small Write IOPS | 1-2 | 1000+ | 500x |
| Write Latency | 500-1000ms | 1-5ms | 100-200x |
| Handle Churn Tolerance | None | Unlimited | ∞ |

### 1.4 Architecture Principles

1. **Zero Hardcoded Values**: All operational parameters configurable
2. **Path-Scoped Lifecycle**: File state survives handle open/close cycles
3. **Local Durability First**: Writes safe locally before async sync
4. **Intelligent Routing**: Reads from optimal source (staging vs object storage)
5. **Eventual Consistency**: Object storage updated asynchronously
6. **Graceful Degradation**: System functional during object storage outages
7. **Production Observability**: Comprehensive metrics, logs, and traces
8. **Crash Recovery**: No data loss on gateway restart

---

## 2. Complete Write Path with Staging Layer

### 2.1 Write Path Overview

```
NFS WRITE Request
       ↓
[1] NFS Protocol Layer
    • Validate handle
    • Check permissions
    • Extract: path, offset, data
       ↓
[2] Session Manager (Path-Scoped)
    • Get/Create session by PATH (not handle)
    • Session survives handle lifecycle
       ↓
[3] Staging Layer Write
    • Write to local file immediately
    • Update dirty ranges
    • Optional fsync
    • Return SUCCESS to client
       ↓
[4] Metadata Update
    • Update size, mtime
    • Mark dirty ranges
    • Persist to journal
       ↓
[5] Sync Trigger Check
    • Size threshold?
    • Time threshold?
    • Explicit fsync?
       ↓
[6] Background Sync (ASYNC)
    • Worker uploads to COS
    • Update metadata
    • Mark clean
```

### 2.2 Key Implementation Details

**Path-Scoped Sessions**: Unlike handle-scoped buffers, sessions are keyed by file path and persist across handle open/close cycles.

**Immediate Acknowledgment**: Writes return success as soon as data is in staging, not after COS upload.

**Dirty Range Tracking**: Efficiently track which byte ranges need syncing to avoid re-uploading entire files.

**Configurable Sync Triggers**:
- Size-based: Sync when dirty data >= threshold (e.g., 8MB)
- Time-based: Sync after interval (e.g., 30s)
- Explicit: Sync on fsync() call

### 2.3 Write Guarantees

| Guarantee | Implementation | Configuration |
|-----------|----------------|---------------|
| **Durability** | Local fsync | `staging.sync_on_write` |
| **Consistency** | Metadata journal | `metadata.journal_enabled` |
| **Atomicity** | Per-write atomic | Built-in |
| **Isolation** | File-level locks | Built-in |

---

## 3. Read Routing Between Staging and Object Storage

### 3.1 Read Decision Logic

```
NFS READ Request
       ↓
[1] Check if file in staging?
       ↓
    YES ──→ [2] Check if range is dirty?
                    ↓
                  YES ──→ [3a] Read from STAGING (authoritative)
                    ↓
                   NO ──→ [3b] Check read cache?
                              ↓
                            YES ──→ [3c] Return from CACHE
                              ↓
                             NO ──→ [3d] Fetch from COS
                                        • Download range
                                        • Populate cache
                                        • Return data
```

### 3.2 Read Sources Priority

1. **Staging (Dirty Data)**: Always read dirty data from staging
2. **Read Cache**: Check memory cache for clean data
3. **Object Storage**: Fallback to COS for cache misses

### 3.3 Read Performance Targets

| Source | Latency | Throughput |
|--------|---------|------------|
| Staging (memory) | 0.1-0.5ms | 1-5 GB/s |
| Staging (disk) | 1-5ms | 100-500 MB/s |
| Read Cache | 0.1-0.5ms | 1-5 GB/s |
| COS (cached) | 10-50ms | 50-200 MB/s |
| COS (uncached) | 50-200ms | 50-200 MB/s |

---

## 4. Staging Directory Layout and File Management

### 4.1 Directory Structure

```
/var/staging/nfs-gateway/
├── data/                      # Active file data
│   ├── by-hash/              # Content-addressed storage
│   │   ├── 00/
│   │   │   └── 00a1b2c3.dat
│   │   ├── 01/
│   │   └── ...
│   └── by-path/              # Path-based symlinks
│       └── bucket1/
│           └── file.txt -> ../by-hash/00/00a1b2c3.dat
├── metadata/                  # File metadata
│   ├── journal/              # Write-ahead log
│   │   ├── 000001.log
│   │   └── current -> 000001.log
│   ├── index/                # Metadata index
│   │   ├── paths.db
│   │   └── inodes.db
│   └── snapshots/            # Periodic snapshots
│       └── snapshot-20260411.db
├── sync/                      # Sync state
│   ├── queue/                # Pending syncs
│   ├── in-flight/            # Active uploads
│   └── failed/               # Failed syncs
└── temp/                      # Temporary files
    ├── uploads/              # Multipart staging
    └── downloads/            # Download buffers
```

### 4.2 Space Management

**Eviction Policy**:
1. Clean files (synced to COS) evicted first
2. LRU-based eviction for clean files
3. Dirty files NEVER evicted (must sync first)
4. Configurable watermarks trigger eviction

**Configuration**:
```yaml
staging:
  max_size_gb: 100
  low_watermark_percent: 70   # Start eviction at 70GB
  high_watermark_percent: 90  # Aggressive eviction at 90GB
```

---

## 5. Synchronization Strategy with Background Workers

### 5.1 Sync Architecture

```
┌──────────────────────────────────────────────────────────┐
│                    Sync Engine                            │
├──────────────────────────────────────────────────────────┤
│                                                           │
│  Trigger Logic ──→ Sync Queue ──→ Worker Pool           │
│  • Size          (Priority)      (Configurable)          │
│  • Time                                                   │
│  • Explicit      ┌─────────────────────────┐            │
│                  │  Upload Manager         │            │
│                  │  • Multipart            │            │
│                  │  • Retry                │            │
│                  │  • Rate Limit           │            │
│                  └─────────────────────────┘            │
└──────────────────────────────────────────────────────────┘
```

### 5.2 Sync Triggers

**1. Size-Based**: Sync when dirty data >= threshold
```yaml
sync:
  size_trigger_mb: 8  # Sync at 8MB dirty data
```

**2. Time-Based**: Sync after interval
```yaml
sync:
  time_trigger_seconds: 30  # Sync every 30s if dirty
```

**3. Explicit**: Sync on fsync() call
```yaml
sync:
  honor_fsync: true  # Respect fsync() calls
```

### 5.3 Worker Pool

**Configuration**:
```yaml
sync:
  workers: 50                    # Number of sync workers
  max_concurrent_uploads: 100    # Max parallel uploads
  queue_depth: 1000              # Max queued jobs
```

**Priority Levels**:
- **High**: Explicit fsync() calls
- **Medium**: Size threshold exceeded
- **Low**: Time-based periodic sync

### 5.4 Multipart Upload Strategy

For files > threshold (default 100MB):
1. Initialize multipart upload
2. Upload parts in parallel (configurable concurrency)
3. Complete multipart upload
4. Update metadata with ETag

**Configuration**:
```yaml
sync:
  multipart_threshold_mb: 100
  multipart_part_size_mb: 10
  multipart_max_concurrent_parts: 10
```

---

## 6. Metadata and State Model with Crash Recovery

### 6.1 Metadata Schema

```go
type FileMetadata struct {
    // Identity
    Path      string
    Inode     uint64
    
    // POSIX Attributes
    Size      int64
    Mode      uint32
    UID       uint32
    GID       uint32
    Atime     time.Time
    Mtime     time.Time
    Ctime     time.Time
    
    // Staging State
    StagingPath string
    InStaging   bool
    DirtyRanges []Range  // [start, end) byte ranges
    
    // Sync State
    SyncStatus  string   // clean, dirty, syncing, failed
    LastSync    time.Time
    SyncVersion int64
    
    // Object Storage State
    ObjectETag    string
    ObjectVersion string
    ObjectSize    int64
    
    // Multipart State (if applicable)
    MultipartUploadID string
    MultipartParts    []PartInfo
}
```

### 6.2 Journal Format

**Binary Format**:
```
Header (32 bytes):
  Magic:       0x4E465347 (NFSG)
  Version:     uint16
  EntryType:   uint16 (CREATE/UPDATE/DELETE/SYNC)
  Timestamp:   int64
  SequenceNum: uint64
  PayloadSize: uint32
  Checksum:    uint32 (CRC32)

Payload (variable):
  JSON-encoded metadata or operation data
```

### 6.3 Crash Recovery Procedure

```
Gateway Restart
       ↓
[1] Load Latest Snapshot
    • Restore metadata index
    • Get last sequence number
       ↓
[2] Replay Journal
    • Read entries since snapshot
    • Apply operations sequentially
    • Rebuild metadata state
       ↓
[3] Verify Staging Files
    • Check staging files exist
    • Validate checksums
    • Mark missing files for re-download
       ↓
[4] Resume Incomplete Syncs
    • Check in-flight uploads
    • Resume or abort multipart uploads
    • Re-queue failed syncs
       ↓
[5] Create New Snapshot
    • Snapshot current state
    • Compact old journal entries
       ↓
Gateway Ready
```

### 6.4 Recovery Guarantees

| Scenario | Recovery | Data Loss |
|----------|----------|-----------|
| Clean shutdown | None needed | None |
| Crash after write | Replay journal | None |
| Crash during sync | Resume sync | None |
| Corrupted journal | Use snapshot | Since snapshot |
| Missing staging file | Re-download from COS | None |

---

## 7. Failure Scenarios and Recovery Procedures

### 7.1 Network Timeout During Sync

**Detection**: Upload operation times out  
**Recovery**:
1. Mark sync job as failed
2. Increment retry counter
3. Apply exponential backoff
4. Re-enqueue if retries < max
5. Alert if max retries exceeded

**Configuration**:
```yaml
sync:
  retry_attempts: 3
  retry_backoff_initial: "1s"
  retry_backoff_max: "60s"
  retry_backoff_multiplier: 2.0
```

### 7.2 Gateway Crash

**Detection**: Process monitoring / health check  
**Recovery**:
1. Restart gateway
2. Run crash recovery (see Section 6.3)
3. Resume operations

**Impact**: 30-60 second unavailability, no data loss

### 7.3 Disk Full

**Detection**: Write returns ENOSPC  
**Recovery**:
1. Trigger aggressive eviction
2. Sync all clean files immediately
3. Remove old clean files
4. If still full: reject writes with ENOSPC
5. Alert operators

**Configuration**:
```yaml
staging:
  disk_full_action: "evict_and_alert"
  emergency_eviction_target_percent: 50
```

### 7.4 COS Bucket Deleted

**Detection**: Sync returns 404  
**Recovery**:
1. Mark all syncs as failed
2. Stop accepting new writes
3. Alert operators (CRITICAL)
4. Wait for bucket recreation
5. Resume after bucket available

### 7.5 Multipart Upload Failure

**Detection**: Part upload error  
**Recovery**:
1. Abort multipart upload
2. Clean up uploaded parts
3. Reset multipart state
4. Re-enqueue sync job
5. Retry with fresh upload

### 7.6 Journal Corruption

**Detection**: Checksum validation fails  
**Recovery**:
1. Stop writing to corrupted journal
2. Load last valid snapshot
3. Discard corrupted entries
4. Create new journal
5. Alert operators

**Impact**: Possible metadata loss since last snapshot

---

## 8. Concurrency Model

### 8.1 Locking Hierarchy

```
Level 1: SessionManager.mu (global)
   ↓
Level 2: FileSession.mu (per-file)
   ↓
Level 3: Metadata.mu (per-file metadata)
   ↓
Level 4: StagingFile.mu (per-file staging)
```

**Rule**: Always acquire locks in order to prevent deadlocks

### 8.2 Concurrent Operations

**Writes to Same File**: Serialized by FileSession.mu  
**Writes to Different Files**: Fully concurrent  
**Reads from Same File**: Concurrent (RLock)  
**Reads from Different Files**: Fully concurrent  
**Sync Operations**: Concurrent (worker pool)

### 8.3 Concurrency Limits

```yaml
concurrency:
  max_concurrent_requests: 1000
  max_sessions: 10000
  sync_workers: 50
  max_concurrent_uploads: 100
  max_concurrent_reads: 200
  session_lock_timeout: "5s"
  metadata_lock_timeout: "1s"
```

---

## 9. Performance Strategy

### 9.1 Performance Targets

| Operation | Target | Current |
|-----------|--------|---------|
| Sequential Write | 50-100 MB/s | 1-2 MB/s |
| Random Write | 20-50 MB/s | 1-2 MB/s |
| Small Write IOPS | 1000+ | 1-2 |
| Write Latency | 1-5ms | 500-1000ms |
| Sequential Read | 100-200 MB/s | 50-100 MB/s |
| Cache Hit Latency | 0.1-0.5ms | N/A |
| Stat Operations | <1ms | 10-50ms |

### 9.2 Optimization Strategies

**1. Write Path**:
- Path-scoped sessions (eliminate handle churn impact)
- Memory or SSD-backed staging
- Async sync decouples from critical path
- Batch small writes before sync

**2. Read Path**:
- Multi-tier caching (staging, memory cache, COS)
- Intelligent routing (dirty data from staging)
- Prefetching for sequential access
- Chunk-based caching

**3. Metadata**:
- In-memory index with persistent journal
- LRU caching for hot metadata
- Batch journal writes
- Periodic snapshots

**4. Sync**:
- Worker pool for parallelism
- Multipart uploads for large files
- Rate limiting to avoid COS throttling
- Priority queue for important syncs

### 9.3 Tuning Parameters

```yaml
performance:
  # Staging
  staging_backend: "disk"  # disk, memory, hybrid
  staging_sync_on_write: false  # fsync every write?
  
  # Caching
  read_cache_size_gb: 10
  read_cache_chunk_size_kb: 1024
  metadata_cache_size_mb: 256
  metadata_cache_ttl_seconds: 60
  
  # Sync
  sync_size_trigger_mb: 8
  sync_time_trigger_seconds: 30
  sync_workers: 50
  
  # Multipart
  multipart_threshold_mb: 100
  multipart_part_size_mb: 10
  multipart_max_concurrent_parts: 10
  
  # Rate Limiting
  cos_rate_limit_mbps: 1000
  cos_max_requests_per_second: 1000
```

---

## 10. Complete Configuration Schema

```yaml
# ============================================================
# NFS Gateway Staging Architecture Configuration
# ============================================================

server:
  nfs_port: 2049
  metrics_port: 8080
  health_port: 8081
  max_connections: 1000
  read_timeout: "30s"
  write_timeout: "30s"

cos:
  endpoint: "s3.us-south.cloud-object-storage.appdomain.cloud"
  bucket: "my-bucket"
  region: "us-south"
  auth_type: "iam"  # iam or hmac
  api_key: "${IBM_CLOUD_API_KEY}"
  service_id: "${IBM_CLOUD_SERVICE_ID}"
  max_retries: 3
  timeout: "30s"
  rate_limit_mbps: 1000
  max_requests_per_second: 1000

staging:
  # Storage backend
  backend: "disk"  # disk, memory, hybrid
  base_path: "/var/staging/nfs-gateway"
  
  # Space management
  max_size_gb: 100
  low_watermark_percent: 70
  high_watermark_percent: 90
  
  # Durability
  sync_on_write: false  # fsync after each write?
  journal_enabled: true
  journal_sync_interval: "1s"
  snapshot_interval: "5m"
  
  # Eviction
  eviction_policy: "lru"  # lru, lfu, fifo
  eviction_check_interval: "30s"
  clean_file_retention: "1h"
  
  # Lifecycle
  inactive_file_threshold: "30m"
  temp_file_max_age: "24h"

sync:
  # Workers
  workers: 50
  max_concurrent_uploads: 100
  queue_depth: 1000
  
  # Triggers
  size_trigger_mb: 8
  time_trigger_seconds: 30
  honor_fsync: true
  
  # Multipart
  multipart_threshold_mb: 100
  multipart_part_size_mb: 10
  multipart_max_concurrent_parts: 10
  
  # Retry
  retry_attempts: 3
  retry_backoff_initial: "1s"
  retry_backoff_max: "60s"
  retry_backoff_multiplier: 2.0
  
  # Timeouts
  upload_timeout: "5m"
  multipart_timeout: "30m"

cache:
  # Read cache
  read_cache_enabled: true
  read_cache_size_gb: 10
  read_cache_chunk_size_kb: 1024
  read_cache_eviction_policy: "lru"
  
  # Metadata cache
  metadata_cache_enabled: true
  metadata_cache_size_mb: 256
  metadata_cache_ttl_seconds: 60
  metadata_cache_max_entries: 10000
  
  # Pref
etching
  prefetch_enabled: true
  prefetch_multiplier: 2  # Prefetch 2x requested size

concurrency:
  max_concurrent_requests: 1000
  max_sessions: 10000
  sync_workers: 50
  max_concurrent_uploads: 100
  max_concurrent_reads: 200
  session_lock_timeout: "5s"
  metadata_lock_timeout: "1s"

logging:
  level: "info"  # debug, info, warn, error
  format: "json"  # json, text
  output: "stdout"  # stdout, stderr, file path
  file_path: "/var/log/nfs-gateway/gateway.log"
  max_size_mb: 100
  max_backups: 10
  max_age_days: 30
  compress: true

metrics:
  enabled: true
  port: 8080
  path: "/metrics"
  namespace: "nfs_gateway"
  
health:
  enabled: true
  port: 8081
  path: "/health"
  check_interval: "10s"

tracing:
  enabled: false
  provider: "jaeger"  # jaeger, zipkin, otlp
  endpoint: "http://localhost:14268/api/traces"
  sample_rate: 0.1  # 10% sampling
```

---

## 11. Observability Plan

### 11.1 Metrics (Prometheus)

#### 11.1.1 Write Path Metrics

```
# Write operations
nfs_gateway_write_requests_total{status="success|error"}
nfs_gateway_write_bytes_total
nfs_gateway_write_latency_seconds{quantile="0.5|0.9|0.99"}

# Staging metrics
nfs_gateway_staging_write_bytes_total
nfs_gateway_staging_write_latency_seconds{quantile="0.5|0.9|0.99"}
nfs_gateway_staging_size_bytes
nfs_gateway_staging_files_total
nfs_gateway_staging_dirty_bytes

# Session metrics
nfs_gateway_sessions_active
nfs_gateway_sessions_created_total
nfs_gateway_sessions_evicted_total
```

#### 11.1.2 Read Path Metrics

```
# Read operations
nfs_gateway_read_requests_total{status="success|error"}
nfs_gateway_read_bytes_total
nfs_gateway_read_latency_seconds{quantile="0.5|0.9|0.99"}

# Read routing
nfs_gateway_read_source_total{source="staging|cache|cos"}
nfs_gateway_cache_hit_ratio
nfs_gateway_cache_size_bytes
nfs_gateway_cache_evictions_total
```

#### 11.1.3 Sync Metrics

```
# Sync operations
nfs_gateway_sync_jobs_queued
nfs_gateway_sync_jobs_in_flight
nfs_gateway_sync_jobs_completed_total{status="success|error"}
nfs_gateway_sync_latency_seconds{quantile="0.5|0.9|0.99"}
nfs_gateway_sync_bytes_total

# Sync workers
nfs_gateway_sync_workers_active
nfs_gateway_sync_workers_idle

# Upload metrics
nfs_gateway_cos_upload_requests_total{status="success|error"}
nfs_gateway_cos_upload_bytes_total
nfs_gateway_cos_upload_latency_seconds{quantile="0.5|0.9|0.99"}
nfs_gateway_cos_multipart_uploads_active
```

#### 11.1.4 Metadata Metrics

```
# Metadata operations
nfs_gateway_metadata_operations_total{operation="create|update|delete|lookup"}
nfs_gateway_metadata_cache_hit_ratio
nfs_gateway_metadata_journal_size_bytes
nfs_gateway_metadata_journal_entries_total

# Recovery metrics
nfs_gateway_recovery_duration_seconds
nfs_gateway_recovery_journal_entries_replayed
```

#### 11.1.5 System Metrics

```
# Resource usage
nfs_gateway_cpu_usage_percent
nfs_gateway_memory_usage_bytes
nfs_gateway_disk_usage_bytes{path="/var/staging"}
nfs_gateway_disk_io_bytes_total{direction="read|write"}

# Errors
nfs_gateway_errors_total{component="nfs|staging|sync|cos|metadata"}
nfs_gateway_retries_total{component="sync|cos"}
```

### 11.2 Logging Strategy

#### 11.2.1 Log Levels

```
DEBUG: Detailed diagnostic information
  • Session lifecycle events
  • Cache operations
  • Sync trigger evaluations

INFO: General operational information
  • Gateway startup/shutdown
  • Configuration loaded
  • Sync job completions
  • Recovery operations

WARN: Warning conditions
  • Retry attempts
  • Cache evictions under pressure
  • Approaching resource limits

ERROR: Error conditions
  • Failed sync operations
  • COS API errors
  • Journal write failures
  • Metadata corruption detected

FATAL: Critical failures
  • Gateway initialization failure
  • Unrecoverable errors
```

#### 11.2.2 Structured Logging Format

```json
{
  "timestamp": "2026-04-11T03:00:00.000Z",
  "level": "INFO",
  "component": "sync_engine",
  "operation": "sync_complete",
  "path": "/bucket1/file.txt",
  "duration_ms": 150,
  "bytes": 8388608,
  "worker_id": 5,
  "trace_id": "abc123",
  "span_id": "def456"
}
```

#### 11.2.3 Key Log Events

```
# Write path
- session_created: New file session created
- staging_write: Data written to staging
- dirty_range_added: Dirty range tracked
- sync_triggered: Sync job enqueued

# Sync path
- sync_started: Sync job started
- sync_progress: Multipart upload progress
- sync_completed: Sync job completed
- sync_failed: Sync job failed

# Recovery
- recovery_started: Crash recovery initiated
- journal_replay: Journal entries replayed
- recovery_completed: Recovery finished

# Errors
- cos_error: COS API error
- disk_full: Staging disk full
- journal_corruption: Journal corruption detected
```

### 11.3 Distributed Tracing

#### 11.3.1 Trace Spans

```
Trace: NFS WRITE Operation
├─ Span: nfs_write_request
│  ├─ Span: session_lookup
│  ├─ Span: staging_write
│  │  └─ Span: disk_write
│  ├─ Span: metadata_update
│  │  └─ Span: journal_write
│  └─ Span: sync_trigger_check
│
└─ Span: background_sync (async)
   ├─ Span: read_staging_data
   ├─ Span: cos_upload
   │  ├─ Span: multipart_init
   │  ├─ Span: part_upload (parallel)
   │  └─ Span: multipart_complete
   └─ Span: metadata_update
```

#### 11.3.2 Trace Attributes

```
# Common attributes
trace.id: Unique trace identifier
span.id: Unique span identifier
service.name: "nfs-gateway"
service.version: "2.0.0"

# Operation attributes
operation.type: "write|read|sync|metadata"
file.path: "/bucket1/file.txt"
file.size: 8388608
offset: 0
length: 8388608

# Performance attributes
duration.ms: 150
bytes.transferred: 8388608
cache.hit: true|false

# Error attributes
error: true|false
error.type: "network|disk|cos|metadata"
error.message: "Connection timeout"
```

### 11.4 Alerting Rules

#### 11.4.1 Critical Alerts

```yaml
# Disk full
- alert: StagingDiskFull
  expr: nfs_gateway_staging_size_bytes / nfs_gateway_staging_max_size_bytes > 0.95
  for: 5m
  severity: critical
  description: "Staging disk >95% full"

# Sync failures
- alert: HighSyncFailureRate
  expr: rate(nfs_gateway_sync_jobs_completed_total{status="error"}[5m]) > 0.1
  for: 10m
  severity: critical
  description: "Sync failure rate >10%"

# COS connectivity
- alert: COSConnectivityLost
  expr: rate(nfs_gateway_cos_upload_requests_total{status="error"}[5m]) > 0.5
  for: 5m
  severity: critical
  description: "COS error rate >50%"
```

#### 11.4.2 Warning Alerts

```yaml
# High queue depth
- alert: HighSyncQueueDepth
  expr: nfs_gateway_sync_jobs_queued > 500
  for: 15m
  severity: warning
  description: "Sync queue depth >500"

# High latency
- alert: HighWriteLatency
  expr: histogram_quantile(0.99, nfs_gateway_write_latency_seconds) > 0.1
  for: 10m
  severity: warning
  description: "P99 write latency >100ms"

# Low cache hit rate
- alert: LowCacheHitRate
  expr: nfs_gateway_cache_hit_ratio < 0.5
  for: 30m
  severity: warning
  description: "Cache hit rate <50%"
```

### 11.5 Dashboards

#### 11.5.1 Overview Dashboard

```
┌─────────────────────────────────────────────────────────┐
│ NFS Gateway - Overview                                  │
├─────────────────────────────────────────────────────────┤
│                                                          │
│ [Write Throughput]  [Read Throughput]  [Sync Queue]    │
│   50 MB/s             100 MB/s           25 jobs        │
│                                                          │
│ [Write Latency P99] [Read Latency P99] [Cache Hit]     │
│   5 ms                2 ms               85%            │
│                                                          │
│ [Staging Usage]     [Active Sessions]  [Sync Workers]  │
│   45 GB / 100 GB      1,234              48 / 50        │
│                                                          │
│ [Error Rate]        [COS Errors]       [Disk I/O]      │
│   0.1%                0.05%              250 MB/s       │
└─────────────────────────────────────────────────────────┘
```

#### 11.5.2 Write Path Dashboard

- Write request rate (req/s)
- Write throughput (MB/s)
- Write latency distribution (P50, P90, P99)
- Staging write latency
- Session creation rate
- Dirty data size over time

#### 11.5.3 Sync Dashboard

- Sync queue depth over time
- Sync job completion rate
- Sync latency distribution
- Worker utilization
- COS upload throughput
- Multipart upload progress
- Failed sync jobs

#### 11.5.4 Resource Dashboard

- CPU usage
- Memory usage
- Disk usage (staging)
- Disk I/O (read/write)
- Network I/O (COS)
- Goroutine count
- File descriptor count

---

## 12. Phased Migration Plan

### 12.1 Phase 1: Foundation (Weeks 1-2)

**Objective**: Build core staging infrastructure

**Tasks**:
1. Implement staging directory structure
2. Create path-scoped session manager
3. Build metadata journal system
4. Implement basic crash recovery
5. Add configuration schema

**Deliverables**:
- Staging file manager
- Session manager with path-scoped sessions
- Metadata journal with replay capability
- Configuration validation

**Success Criteria**:
- Sessions survive handle lifecycle
- Metadata persists across restarts
- Journal replay works correctly

**Risk**: Low - foundational work, no production impact

### 12.2 Phase 2: Write Path (Weeks 3-4)

**Objective**: Implement staging-based write path

**Tasks**:
1. Integrate session manager with NFS handler
2. Implement staging layer writes
3. Add dirty range tracking
4. Build sync trigger logic
5. Add write path metrics

**Deliverables**:
- Path-scoped write operations
- Dirty range tracking
- Sync trigger evaluation
- Write path observability

**Success Criteria**:
- Writes complete in <5ms
- Sessions persist across reopen
- Dirty ranges tracked accurately

**Risk**: Medium - changes critical write path

**Rollback**: Feature flag to disable staging, fall back to direct COS writes

### 12.3 Phase 3: Sync Engine (Weeks 5-6)

**Objective**: Build background synchronization

**Tasks**:
1. Implement sync worker pool
2. Build upload manager with multipart support
3. Add retry logic with exponential backoff
4. Implement sync queue with priority
5. Add sync metrics and logging

**Deliverables**:
- Worker pool with configurable size
- Multipart upload support
- Retry mechanism
- Sync observability

**Success Criteria**:
- Sync completes within configured interval
- Multipart uploads work for large files
- Failed syncs retry appropriately

**Risk**: Medium - async operations, need robust error handling

**Rollback**: Disable async sync, sync on every write (performance degradation)

### 12.4 Phase 4: Read Routing (Weeks 7-8)

**Objective**: Implement intelligent read routing

**Tasks**:
1. Build read router with source selection
2. Implement read cache
3. Add prefetching logic
4. Integrate with staging layer
5. Add read path metrics

**Deliverables**:
- Read router with dirty data detection
- Memory-based read cache
- Prefetch for sequential reads
- Read path observability

**Success Criteria**:
- Dirty data always read from staging
- Cache hit rate >70%
- Read latency <1ms for cached data

**Risk**: Low - read path changes, no data loss risk

**Rollback**: Disable read cache, always read from COS

### 12.5 Phase 5: Space Management (Weeks 9-10)

**Objective**: Implement staging space management

**Tasks**:
1. Build eviction manager
2. Implement watermark-based eviction
3. Add lifecycle management
4. Implement emergency eviction
5. Add space management metrics

**Deliverables**:
- LRU-based eviction
- Configurable watermarks
- Lifecycle policies
- Space management observability

**Success Criteria**:
- Eviction maintains space below high watermark
- Clean files evicted before dirty files
- Emergency eviction prevents disk full

**Risk**: Medium - incorrect eviction could lose data

**Rollback**: Disable eviction, alert on disk full

### 12.6 Phase 6: Production Hardening (Weeks 11-12)

**Objective**: Production readiness

**Tasks**:
1. Comprehensive error handling
2. Enhanced crash recovery
3. Performance tuning
4. Load testing
5. Documentation

**Deliverables**:
- Failure scenario handling
- Optimized configuration
- Load test results
- Operations runbook

**Success Criteria**:
- Handles all failure scenarios gracefully
- Meets performance targets under load
- Complete operational documentation

**Risk**: Low - hardening existing functionality

### 12.7 Phase 7: Gradual Rollout (Weeks 13-14)

**Objective**: Safe production deployment

**Strategy**:
1. **Week 13**: Deploy to 10% of traffic
   - Monitor metrics closely
   - Validate performance improvements
   - Check for errors

2. **Week 14**: Expand to 50% of traffic
   - Continue monitoring
   - Tune configuration based on real traffic
   - Address any issues

3. **Week 15**: Full rollout to 100%
   - Complete migration
   - Remove old code paths
   - Declare production ready

**Rollback Plan**:
- Feature flag to disable staging
- Automated rollback on error rate spike
- Manual rollback procedure documented

---

## 13. Open Design Tradeoffs

### 13.1 Staging Backend: Disk vs Memory

**Disk-Based Staging**:
- ✅ Survives gateway restarts
- ✅ Larger capacity (100s of GB)
- ✅ Lower memory pressure
- ❌ Higher latency (1-5ms)
- ❌ Disk I/O bottleneck

**Memory-Based Staging**:
- ✅ Ultra-low latency (0.1-0.5ms)
- ✅ No disk I/O bottleneck
- ✅ Simpler implementation
- ❌ Lost on restart (need journal replay)
- ❌ Limited capacity (10s of GB)
- ❌ High memory usage

**Hybrid Approach**:
- ✅ Best of both worlds
- ✅ Hot data in memory, cold on disk
- ❌ Complex implementation
- ❌ Cache coherency challenges

**Recommendation**: Start with disk-based, add memory tier later if needed

### 13.2 Sync Trigger: Aggressive vs Conservative

**Aggressive Sync** (small threshold, short interval):
- ✅ Lower data loss window
- ✅ Staging disk usage lower
- ✅ Faster eventual consistency
- ❌ Higher COS API costs
- ❌ More network bandwidth
- ❌ Potential COS rate limiting

**Conservative Sync** (large threshold, long interval):
- ✅ Lower COS API costs
- ✅ Better batching efficiency
- ✅ Less network usage
- ❌ Larger data loss window
- ❌ Higher staging disk usage
- ❌ Slower eventual consistency

**Recommendation**: Configurable with sensible defaults (8MB, 30s)

### 13.3 Eviction Policy: LRU vs LFU

**LRU (Least Recently Used)**:
- ✅ Simple implementation
- ✅ Works well for temporal locality
- ✅ Predictable behavior
- ❌ Can evict frequently accessed files

**LFU (Least Frequently Used)**:
- ✅ Better for frequency-based access
- ✅ Keeps hot files longer
- ❌ Complex implementation
- ❌ Slow to adapt to pattern changes

**Recommendation**: LRU for simplicity, add LFU option later

### 13.4 Consistency Model: Strong vs Eventual

**Strong Consistency** (sync on every write):
- ✅ No data loss window
- ✅ Simpler reasoning
- ✅ Immediate COS consistency
- ❌ Terrible performance (back to 1-2 MB/s)
- ❌ Defeats purpose of staging

**Eventual Consistency** (async sync):
- ✅ High performance (50-100 MB/s)
- ✅ Decoupled from COS latency
- ✅ Survives COS outages
- ❌ Data loss window (mitigated by journal)
- ❌ COS may be stale

**Recommendation**: Eventual consistency with configurable sync triggers

### 13.5 Multipart Threshold: Small vs Large

**Small Threshold** (e.g., 10MB):
- ✅ More files use multipart
- ✅ Better parallelism
- ✅ Faster uploads
- ❌ More COS API calls
- ❌ Higher overhead

**Large Threshold** (e.g., 100MB):
- ✅ Fewer COS API calls
- ✅ Lower overhead
- ❌ Less parallelism
- ❌ Slower for large files

**Recommendation**: 100MB threshold, configurable

### 13.6 Journal Format: Binary vs Text

**Binary Format**:
- ✅ Compact size
- ✅ Faster parsing
- ✅ Built-in checksums
- ❌ Not human-readable
- ❌ Harder to debug

**Text Format** (JSON):
- ✅ Human-readable
- ✅ Easy to debug
- ✅ Flexible schema
- ❌ Larger size
- ❌ Slower parsing

**Recommendation**: Binary format with JSON payload (hybrid)

### 13.7 Recovery Strategy: Snapshot vs Full Replay

**Snapshot-Based**:
- ✅ Fast recovery
- ✅ Bounded replay time
- ✅ Compact journal
- ❌ Periodic overhead
- ❌ Potential data loss (since snapshot)

**Full Replay**:
- ✅ No data loss
- ✅ No snapshot overhead
- ❌ Slow recovery (proportional to journal size)
- ❌ Unbounded replay time

**Recommendation**: Snapshot-based with configurable interval (5 minutes)

### 13.8 Read Cache: Shared vs Per-Session

**Shared Read Cache**:
- ✅ Better cache utilization
- ✅ Shared across all clients
- ✅ Higher hit rate
- ❌ Cache coherency complexity
- ❌ Locking overhead

**Per-Session Cache**:
- ✅ Simple implementation
- ✅ No coherency issues
- ✅ No locking overhead
- ❌ Lower cache utilization
- ❌ Duplicate cached data

**Recommendation**: Shared cache with fine-grained locking

### 13.9 Worker Pool: Fixed vs Dynamic

**Fixed Worker Pool**:
- ✅ Predictable resource usage
- ✅ Simple implementation
- ✅ No scaling overhead
- ❌ May be underutilized
- ❌ May be overwhelmed

**Dynamic Worker Pool**:
- ✅ Adapts to load
- ✅ Better resource utilization
- ❌ Complex implementation
- ❌ Scaling overhead

**Recommendation**: Fixed pool with configurable size

### 13.10 Failure Handling: Retry vs Quarantine

**Aggressive Retry**:
- ✅ Maximizes success rate
- ✅ Handles transient failures
- ❌ Can amplify problems
- ❌ Wastes resources on permanent failures

**Quick Quarantine**:
- ✅ Avoids wasting resources
- ✅ Isolates problematic files
- ❌ May give up too early
- ❌ Requires manual intervention

**Recommendation**: Retry with exponential backoff, quarantine after max attempts

---

## Appendix A: Glossary

**Staging**: Local high-performance storage layer for active file data

**Session**: Path-scoped file state that survives handle lifecycle

**Dirty Range**: Byte range modified in staging but not yet synced to COS

**Sync Job**: Background task to upload dirty data to object storage

**Journal**: Write-ahead log for metadata operations

**Snapshot**: Point-in-time copy of metadata state

**Eviction**: Removal of clean files from staging to free space

**Multipart Upload**: COS feature for uploading large files in parts

**Read Router**: Component that decides where to read data from

**Worker Pool**: Fixed set of goroutines processing sync jobs

---

## Appendix B: References

1. **AWS S3 Files**: https://aws.amazon.com/s3/features/file-gateway/
2. **IBM Cloud Object Storage**: https://cloud.ibm.com/docs/cloud-object-storage
3. **NFSv3 RFC 1813**: https://tools.ietf.org/html/rfc1813
4. **Write-Ahead Logging**: https://en.wikipedia.org/wiki/Write-ahead_logging
5. **LRU Cache**: https://en.wikipedia.org/wiki/Cache_replacement_policies#LRU

---

## Appendix C: Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-04-11 | Path-scoped sessions | Survive handle churn, key to performance |
| 2026-04-11 | Disk-based staging | Balance performance and durability |
| 2026-04-11 | Eventual consistency | Required for performance targets |
| 2026-04-11 | Binary journal format | Compact and fast, JSON payload for flexibility |
| 2026-04-11 | Snapshot-based recovery | Fast recovery, acceptable data loss window |
| 2026-04-11 | Fixed worker pool | Predictable, simple, configurable |
| 2026-04-11 | LRU eviction | Simple, effective, well-understood |

---

**Document Version**: 2.0  
**Last Updated**: 2026-04-11  
**Status**: Production Design Specification  
**Next Review**: 2026-05-11

---

*Made with Bob - Production-Grade Architecture*
