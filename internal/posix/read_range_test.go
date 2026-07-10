package posix

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// Test harness: chunk size is 1024 (see newRefreshTestOps), object is 8 KiB.
func newRangeReadFixture(t *testing.T) (*fakeObjectStore, *OperationsHandler, []byte) {
	t.Helper()

	payload := make([]byte, 8*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	store := newFakeObjectStore()
	store.put("range.bin", payload, time.Unix(100, 0))

	ops, _ := newRefreshTestOps(t, store)
	// Populate the metadata cache the way a real NFS client does (GETATTR
	// before READ); this also lets read-ahead clamp to the object size.
	if _, err := ops.Stat(context.Background(), "/range.bin"); err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	return store, ops, payload
}

func TestReadRangeWarmChunksSkipCOSAndReadAhead(t *testing.T) {
	ctx := context.Background()
	store, ops, payload := newRangeReadFixture(t)

	// Cold read: fetches the required chunk plus read-ahead within the file.
	data, err := ops.ReadFile(ctx, "/range.bin", 0, 1024)
	if err != nil {
		t.Fatalf("ReadFile(cold) error = %v", err)
	}
	if !bytes.Equal(data, payload[:1024]) {
		t.Fatal("ReadFile(cold) returned wrong bytes")
	}
	coldCalls := store.rangeCallCount()
	if coldCalls == 0 {
		t.Fatal("cold read should fetch from COS")
	}

	// Warm reads across the whole file must be served purely from cache:
	// no COS range calls, including for read-ahead.
	for off := int64(0); off < int64(len(payload)); off += 1024 {
		data, err := ops.ReadFile(ctx, "/range.bin", off, 1024)
		if err != nil {
			t.Fatalf("ReadFile(warm off=%d) error = %v", off, err)
		}
		if !bytes.Equal(data, payload[off:off+1024]) {
			t.Fatalf("ReadFile(warm off=%d) returned wrong bytes", off)
		}
	}
	// Unaligned warm reads spanning chunk boundaries (the shape NFS clients
	// actually issue) must also stay cache-only.
	for _, off := range []int64{512, 1000, 3585} {
		data, err := ops.ReadFile(ctx, "/range.bin", off, 1024)
		if err != nil {
			t.Fatalf("ReadFile(warm unaligned off=%d) error = %v", off, err)
		}
		if !bytes.Equal(data, payload[off:off+1024]) {
			t.Fatalf("ReadFile(warm unaligned off=%d) returned wrong bytes", off)
		}
	}
	if got := store.rangeCallCount(); got != coldCalls {
		t.Fatalf("warm reads made %d extra COS range calls, want 0", got-coldCalls)
	}
}

func TestReadRangeSmallReadWithinCachedChunk(t *testing.T) {
	ctx := context.Background()
	store, ops, payload := newRangeReadFixture(t)

	// Warm the first chunk.
	if _, err := ops.ReadFile(ctx, "/range.bin", 0, 1024); err != nil {
		t.Fatalf("ReadFile(warmup) error = %v", err)
	}
	warmCalls := store.rangeCallCount()

	// A small unaligned read inside a cached chunk: exact bytes, no COS.
	data, err := ops.ReadFile(ctx, "/range.bin", 100, 4)
	if err != nil {
		t.Fatalf("ReadFile(small) error = %v", err)
	}
	if !bytes.Equal(data, payload[100:104]) {
		t.Fatalf("ReadFile(small) = %v, want %v", data, payload[100:104])
	}
	if got := store.rangeCallCount(); got != warmCalls {
		t.Fatalf("small warm read made %d extra COS range calls, want 0", got-warmCalls)
	}
}

func TestReadRangeCrossChunkAndEOF(t *testing.T) {
	ctx := context.Background()
	_, ops, payload := newRangeReadFixture(t)

	// Unaligned read spanning two chunks.
	data, err := ops.ReadFile(ctx, "/range.bin", 1000, 100)
	if err != nil {
		t.Fatalf("ReadFile(cross-chunk) error = %v", err)
	}
	if !bytes.Equal(data, payload[1000:1100]) {
		t.Fatal("ReadFile(cross-chunk) returned wrong bytes")
	}

	// Read crossing EOF returns only the available bytes.
	data, err = ops.ReadFile(ctx, "/range.bin", int64(len(payload))-100, 1024)
	if err != nil {
		t.Fatalf("ReadFile(EOF) error = %v", err)
	}
	if !bytes.Equal(data, payload[len(payload)-100:]) {
		t.Fatalf("ReadFile(EOF) returned %d wrong bytes, want final 100", len(data))
	}
}

func TestReadRangeReadAheadClampedToObjectSize(t *testing.T) {
	ctx := context.Background()
	store, ops, _ := newRangeReadFixture(t)

	// Cold read of the first chunk. Read-ahead (default 8 MiB) must clamp to
	// the 8 KiB object: at most 8 chunk fetches, no doomed past-EOF ranges.
	if _, err := ops.ReadFile(ctx, "/range.bin", 0, 1024); err != nil {
		t.Fatalf("ReadFile(cold) error = %v", err)
	}
	if got := store.rangeCallCount(); got > 8 {
		t.Fatalf("cold read issued %d COS range calls, want <= 8 (read-ahead not clamped to object size)", got)
	}
}

// Made with Bob
