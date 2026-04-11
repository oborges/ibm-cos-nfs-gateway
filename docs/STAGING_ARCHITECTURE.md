# Production-Grade Staging Architecture for NFS-to-Object-Storage Gateway

**Version**: 1.0  
**Date**: 2026-04-11  
**Status**: Design Proposal

---

## 1. Executive Summary

### Current Problem

The existing gateway architecture suffers from a fundamental structural flaw: write buffering is tied to file handle lifecycle. When NFS clients exhibit typical behavior (frequent reopen/close cycles), the write buffer is destroyed before reaching flush thresholds, forcing expensive full-object read-modify-write operations on every small update. This results in throughput degradation from potential 50+ MB/s to actual 1-2 MB/s.

### Proposed Solution

We propose a **staging-based architecture** conceptually aligned with AWS S3 Files, where:

1. **Active data lives in high-performance local staging** (filesystem or memory)
2. **Writes are decoupled from object storage operations** via asynchronous sync
3. **Reads are intelligently routed** between staging and object storage
4. **File lifecycle is path-scoped**, not handle-scoped
5. **Background workers** handle synchronization with configurable policies
6. **Crash recovery** ensures data safety without sacrificing performance

### Key Benefits

- **10-50x write performance improvement** by eliminating per-write object operations
- **Survives reopen/close churn** through path-scoped session management
- **Intelligent read routing** optimizes for access patterns
- **Configurable sync policies** balance performance vs durability
- **Production-grade observability** for monitoring and debugging

### Architecture Principles

1. **No hardcoded thresholds** - all operational parameters from configuration
2. **Eventual consistency** - object storage is authoritative but asynchronously updated
3. **Local durability first** - writes are safe locally before async sync
4. **Intelligent routing** - reads from fastest available source
5. **Graceful degradation** - system remains functional during object storage outages

---

## 2. Proposed Architecture

### 2.1 System Layers

```
┌─────────────────────────────────────────────────────────────┐
│                    NFS Protocol Layer                        │
│              (NFSv3 Server, Handle Management)               │
└────────────────────────┬────────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────────┐
│                   File Operations Layer                      │
│         (Open, Read, Write, Close, Fsync, Stat)             │
└────────────────────────┬────────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────────┐
│                    Metadata Manager                          │
│    (Path→Inode, Sync State, Dirty Tracking, Versions)      │
└─────┬──────────────────┴──────────────────────┬─────────────┘
      │                                          │
┌─────▼──────────────────┐          ┌───────────▼─────────────┐
│  Staging Layer         │          │   Read Router           │
│  (Local FS/Memory)     │          │   (Cache + Routing)     │
│  - Active files        │          │   - Hot data cache      │
│  - Dirty data          │          │   - Routing decisions   │
│  - Write sessions      │          │   - Prefetch logic      │
└─────┬──────────────────┘          └───────────┬─────────────┘
      │                                          │
┌─────▼──────────────────────────────────────────▼─────────────┐
│                    Sync Engine                                │
│  (Background Workers, Queue, Retry, Multipart)               │
└─────┬────────────────────────────────────────────────────────┘
      │
┌─────▼────────────────────────────────────────────────────────┐
│              Object Storage Integration Layer                 │
│         (IBM COS SDK, Multipart, Retry, Throttling)          │
└──────────────────────────────────────────────────────────────┘
```

### 2.2 Layer Responsibilities

#### NFS Protocol Layer
- **Responsibility**: Handle NFS protocol, file handles, RPC
- **State**: File handle → inode mapping (ephemeral)
- **Failure**: Client retry, handle invalidation
- **Consistency**: Stateless, delegates to lower layers

#### File Operations Layer
- **Responsibility**: POSIX semantics, path resolution, permission checks
- **State**: None (delegates to metadata manager)
- **Failure**: Return appropriate errno
- **Consistency**: Enforces POSIX ordering guarantees

#### Metadata Manager
- **Responsibility**: 
  - Path → inode mapping
  - File size, mtime, permissions
  - Sync state (clean/dirty/syncing)
  - Object storage version/ETag tracking
  - Multipart upload state
- **State**: In-memory index + persistent journal
- **Failure**: Recoverable from journal on restart
- **Consistency**: Single source of truth for file state

#### Staging Layer
- **Responsibility**:
  - Store active/dirty file data
  - Provide fast read/write access
  - Manage disk space
  - Handle write sessions
- **State**: Files on local filesystem or memory
- **Failure**: Data loss if not synced (mitigated by journal)
- **Consistency**: Eventually consistent with object storage

#### Read Router
- **Responsibility**:
  - Decide read source (staging vs object storage)
  - Manage read cache
  - Prefetch optimization
- **State**: Cache metadata, routing decisions
- **Failure**: Fallback to object storage
- **Consistency**: Read-after-write guaranteed via metadata

#### Sync Engine
- **Responsibility**:
  - Asynchronous export to object storage
  - Multipart upload management
  - Retry and error handling
  - Queue management
- **State**: Sync queue, in-flight uploads, retry state
- **Failure**: Retry with backoff, quarantine on permanent failure
- **Consistency**: Ensures object storage eventually matches staging

#### Object Storage Integration
- **Responsibility**:
  - COS API calls
  - Multipart upload protocol
  - Authentication and throttling
- **State**: None (stateless API client)
- **Failure**: Return errors to sync engine
- **Consistency**: Authoritative durable store

---

*[Document continues in next part...]*