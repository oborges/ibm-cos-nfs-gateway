# READDIR Root Cause Analysis

## Executive Summary

**Root Cause**: Abstraction mismatch between NFS READDIR protocol semantics and `billy.Filesystem.ReadDir()` interface, combined with verifier cache eviction causing repeated full directory reads.

**NOT a pure go-nfs library bug** - the library implements NFS pagination correctly, but the `billy.Filesystem` abstraction doesn't expose the necessary state for efficient pagination.

## Call Chain Analysis

### 1. NFS READDIR Protocol Flow

```
Client sends READDIR request
  ↓
go-nfs: nfs_onreaddir.go:onReadDir()
  ↓
getDirListingWithVerifier() - checks verifier cache
  ↓
billy.Filesystem.ReadDir(path) - returns ALL entries
  ↓
go-nfs paginates the full list based on cookie + count
  ↓
Returns subset to client with EOF flag
```

### 2. Key Code in `nfs_onreaddir.go`

```go
func getDirListingWithVerifier(userHandle Handler, fsHandle []byte, verifier uint64) ([]fs.FileInfo, uint64, error) {
    // Check verifier cache first
    if vh, ok := userHandle.(CachingHandler); verifier != 0 && ok {
        entries := vh.DataForVerifier(path, verifier)
        if entries != nil {
            return entries, verifier, nil  // ← Cache hit: reuse previous results
        }
    }
    
    // Cache miss: call ReadDir to get ALL entries
    contents, err := fs.ReadDir(path)  // ← Returns complete directory
    
    // Store in verifier cache
    if vh, ok := userHandle.(CachingHandler); ok {
        v := vh.VerifierFor(path, contents)
        return contents, v, nil
    }
}
```

### 3. Pagination Logic in `onReadDir()`

```go
func onReadDir(ctx context.Context, w *response, userHandle Handler) error {
    // Get full directory listing (from cache or ReadDir)
    contents, verifier, err := getDirListingWithVerifier(userHandle, obj.Handle, obj.CookieVerif)
    
    // Paginate based on cookie
    started := obj.Cookie == 0
    for i, c := range contents {
        cookie := uint64(i + 2)
        if started {
            // Add entries until count limit reached
            if maxBytes > obj.Count || len(entities) > maxEntities {
                eof = false  // ← More entries available
                break
            }
            entities = append(entities, ...)
        } else if cookie == obj.Cookie {
            started = true  // ← Resume from cookie position
        }
    }
    
    // Return subset with EOF flag
    xdr.Write(writer, eof)
}
```

## The Problem

### What We Observed

```json
{
  "total_calls": 66549,
  "result_count_histogram": { "100": 66549 },
  "diagnosis": "PAGINATION LOOP DETECTED"
}
```

Every `ReadDir()` call returns all 100 entries.

### Why This Happens

**The verifier cache is being evicted**, causing repeated calls to `billy.Filesystem.ReadDir()`:

1. **Client sends READDIR** with cookie=0, verifier=0
2. **go-nfs calls** `getDirListingWithVerifier()`
3. **Cache miss** → calls `fs.ReadDir()` → returns 100 entries
4. **Stores in verifier cache** with verifier=12345
5. **Returns first batch** (e.g., 20 entries) with EOF=false, verifier=12345

6. **Client sends READDIR** with cookie=20, verifier=12345
7. **go-nfs calls** `getDirListingWithVerifier()`
8. **Cache hit** → reuses cached 100 entries
9. **Returns next batch** (entries 20-40) with EOF=false

10. **Verifier cache evicted** (LRU, only 1000 entries)
11. **Client sends READDIR** with cookie=40, verifier=12345
12. **go-nfs calls** `getDirListingWithVerifier()`
13. **Cache miss** (verifier evicted) → calls `fs.ReadDir()` again
14. **Stores NEW verifier** 67890
15. **Returns batch** with NEW verifier=67890

16. **Client sends READDIR** with cookie=60, verifier=67890
17. **Verifier mismatch** → returns NFSStatusBadCookie
18. **Client retries** from cookie=0 → **LOOP RESTARTS**

### The Abstraction Mismatch

**NFS READDIR expects**:
- Stateful iteration with cookies/offsets
- Ability to resume from arbitrary position
- Verifier to detect directory changes

**billy.Filesystem.ReadDir() provides**:
- Stateless operation
- Returns complete directory every time
- No cookie/offset support
- No built-in change detection

**The gap**:
- go-nfs bridges this with verifier caching
- But cache eviction breaks the bridge
- Causing repeated full directory reads

## Why 66,549 Calls for 100 Files?

With verifier cache eviction:
1. Client requests entries in batches (e.g., 20 at a time)
2. After 5 requests, verifier gets evicted
3. Next request fails with BadCookie
4. Client restarts from beginning
5. **Loop repeats ~665 times** (66549 ÷ 100 = 665)

## This Is NOT a Pure Library Bug

The go-nfs library:
- ✅ Implements NFS pagination correctly
- ✅ Uses verifiers properly
- ✅ Returns EOF flags correctly
- ✅ Handles cookies correctly

The problem is:
- ❌ Verifier cache too small (1000 entries for all paths)
- ❌ `billy.Filesystem` abstraction doesn't support stateful iteration
- ❌ No way to implement efficient pagination without caching full results

## The Fix

### Option 1: Increase Verifier Cache Size (Workaround)
Increase cache from 1000 to much larger value to prevent eviction during active directory reads.

**Pros**: Simple, no code changes
**Cons**: Doesn't scale, wastes memory

### Option 2: Per-Handle Directory State (Proper Fix)
Implement stateful directory iteration that survives across READDIR calls for the same handle.

**Pros**: Efficient, scalable
**Cons**: Requires new abstraction layer

### Option 3: Single-Shot READDIR Response
For directories that fit in one response, return everything immediately and set EOF=true.

**Pros**: Simple, works for small directories
**Cons**: Doesn't help with large directories

## Recommended Solution

Implement **Option 2** with a minimal per-handle directory state cache that:
1. Caches full directory listing per handle (not per verifier)
2. Survives for the duration of the READDIR sequence
3. Clears when EOF is reached or handle is invalidated
4. Has separate LRU from verifier cache

This provides efficient pagination without the abstraction mismatch.