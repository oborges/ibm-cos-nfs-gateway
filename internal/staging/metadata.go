package staging

import (
	"sync"
	"time"
)

// DirtyFileIndex tracks which files are dirty (not yet synced to COS)
type DirtyFileIndex struct {
	dirty   map[string]*DirtyFileMetadata
	syncing map[string]bool
	mu      sync.RWMutex
}

// DirtyFileMetadata contains metadata about a dirty file
type DirtyFileMetadata struct {
	Path          string
	Size          int64
	DirtySince    time.Time
	LastModified  time.Time
	SyncAttempts  int
	LastSyncError error
}

// NewDirtyFileIndex creates a new dirty file index
func NewDirtyFileIndex() *DirtyFileIndex {
	return &DirtyFileIndex{
		dirty:   make(map[string]*DirtyFileMetadata),
		syncing: make(map[string]bool),
	}
}

// MarkDirty marks a file as dirty (needs sync)
func (dfi *DirtyFileIndex) MarkDirty(path string, size int64) {
	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	now := time.Now()
	if meta, exists := dfi.dirty[path]; exists {
		meta.Size = size
		meta.LastModified = now
	} else {
		dfi.dirty[path] = &DirtyFileMetadata{
			Path:         path,
			Size:         size,
			DirtySince:   now,
			LastModified: now,
			SyncAttempts: 0,
		}
	}
}

// MarkClean marks a file as clean (synced to COS)
func (dfi *DirtyFileIndex) MarkClean(path string) {
	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	delete(dfi.dirty, path)
	delete(dfi.syncing, path)
}

// LockFile securely claims the file for syncing by a background worker natively isolating multiple loops
func (dfi *DirtyFileIndex) LockFile(path string) bool {
	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	if dfi.syncing[path] {
		return false // Activity bound globally to another worker
	}
	dfi.syncing[path] = true
	return true
}

// UnlockFile securely releases the file sync bounds natively out of IBM pipelines
func (dfi *DirtyFileIndex) UnlockFile(path string) {
	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	delete(dfi.syncing, path)
}

// IsSyncing returns true if a file is currently claimed by a sync worker.
func (dfi *DirtyFileIndex) IsSyncing(path string) bool {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	return dfi.syncing[path]
}

// IsDirty returns true if the file is dirty
func (dfi *DirtyFileIndex) IsDirty(path string) bool {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	_, exists := dfi.dirty[path]
	return exists
}

// GetDirtyFiles returns a list of all dirty files
func (dfi *DirtyFileIndex) GetDirtyFiles() []*DirtyFileMetadata {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	files := make([]*DirtyFileMetadata, 0, len(dfi.dirty))
	for _, meta := range dfi.dirty {
		// Create a copy to avoid race conditions
		metaCopy := *meta
		files = append(files, &metaCopy)
	}

	return files
}

// GetMetadata returns metadata for a specific file
func (dfi *DirtyFileIndex) GetMetadata(path string) *DirtyFileMetadata {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	if meta, exists := dfi.dirty[path]; exists {
		metaCopy := *meta
		return &metaCopy
	}

	return nil
}

// IncrementSyncAttempts increments the sync attempt counter for a file
func (dfi *DirtyFileIndex) IncrementSyncAttempts(path string, err error) {
	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	if meta, exists := dfi.dirty[path]; exists {
		meta.SyncAttempts++
		meta.LastSyncError = err
	}
}

// Count returns the number of dirty files
func (dfi *DirtyFileIndex) Count() int {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	return len(dfi.dirty)
}

// SyncingCount returns the number of files currently claimed by sync workers.
func (dfi *DirtyFileIndex) SyncingCount() int {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	return len(dfi.syncing)
}

// Made with Bob
