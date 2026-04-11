# Write Buffer Design

## Problem Statement
Current write performance: ~1 MB/s, 1-2 IOPS
Root cause: Each write operation immediately calls COS PutObject
Goal: Buffer writes in memory, flush in larger chunks

## Architecture

### 1. Write Buffer Structure
```go
type WriteBuffer struct {
    mu          sync.RWMutex
    segments    map[int64]*BufferSegment  // offset -> segment
    totalSize   int64
    maxSize     int64  // Flush threshold (default: 8MB)
    dirty       bool
    lastWrite   time.Time
}

type BufferSegment struct {
    offset int64
    data   []byte
}
```

### 2. Write Flow
```
Write() → Buffer.Append() → Check threshold → Auto-flush if needed
Close() → Flush remaining data → PutObject/MultipartUpload
```

### 3. Flush Strategies

**Trigger conditions:**
- Buffer size >= threshold (8MB default)
- File close
- Explicit fsync
- Timeout (optional: 30s of inactivity)

**Flush behavior:**
- Merge overlapping segments
- Sort by offset
- Upload as single object or multipart

### 4. Data Model

**Option B (Chunked Segments):**
- Maintain map of offset → data segments
- Merge contiguous segments on flush
- Support random writes
- Memory efficient for sparse writes

**Benefits:**
- Handles random writes
- Doesn't require full file in memory
- Can flush incrementally
- Supports multipart upload

### 5. Consistency Semantics

**Write guarantees:**
- Data buffered in memory until flush
- Close() guarantees all data flushed to COS
- Crash before flush = data loss (standard POSIX behavior)
- After successful close = durable in COS

**Read-after-write:**
- Reads check write buffer first
- Return buffered data if available
- Ensures consistency within same file handle

### 6. Multipart Upload Integration

**For files > 100MB:**
- Use multipart upload
- Each flush creates a part
- Complete multipart on close
- Track part numbers and ETags

**Structure:**
```go
type MultipartState struct {
    uploadID string
    parts    []CompletedPart
    nextPart int
}
```

### 7. Error Handling

**Flush failures:**
- Return error to caller
- Keep data in buffer
- Retry on next flush attempt
- Fail close() if flush fails

**Partial writes:**
- Buffer partial data
- Return bytes written
- Flush on threshold

### 8. Concurrency

**Per-file handle buffers:**
- Each COSFile has own WriteBuffer
- No global locks
- Thread-safe within file handle
- Independent flush operations

### 9. Configuration

```yaml
performance:
  write_buffer_size_mb: 8      # Auto-flush threshold
  write_buffer_timeout_ms: 30000  # Flush after inactivity
  multipart_threshold_mb: 100  # Use multipart for large files
  multipart_part_size_mb: 10   # Part size for multipart
```

### 10. Observability

**Metrics:**
- `write_buffer_size_bytes` - Current buffer size
- `write_buffer_flushes_total` - Number of flushes
- `write_buffer_flush_duration_seconds` - Flush latency
- `write_buffer_bytes_flushed` - Total bytes flushed
- `write_multipart_uploads_total` - Multipart upload count

**Logs:**
- Buffer size on write
- Flush trigger (threshold/close/timeout)
- Flush duration and size
- Multipart upload progress

## Implementation Plan

### Phase 1: Basic Write Buffer
1. Create WriteBuffer struct
2. Implement Append() and Flush()
3. Integrate with COSFile.Write()
4. Auto-flush on threshold
5. Flush on Close()

### Phase 2: Segment Management
1. Implement segment merging
2. Handle overlapping writes
3. Support random writes
4. Optimize memory usage

### Phase 3: Multipart Upload
1. Detect large files
2. Initialize multipart upload
3. Upload parts on flush
4. Complete multipart on close

### Phase 4: Advanced Features
1. Timeout-based flush
2. Background flush worker
3. Retry logic
4. Performance tuning

## Expected Performance

**Before:**
- Sequential write: ~1 MB/s
- Small writes: 1-2 IOPS

**After:**
- Sequential write: >20 MB/s (20x improvement)
- Small writes: Buffered, amortized cost
- Large files: Multipart upload efficiency

## Testing Strategy

1. **Unit tests:**
   - Buffer append/merge
   - Flush logic
   - Segment management

2. **Integration tests:**
   - Sequential writes
   - Random writes
   - Large file writes
   - Concurrent writes

3. **Performance tests:**
   - dd sequential write
   - fio random write
   - Large file upload (>1GB)