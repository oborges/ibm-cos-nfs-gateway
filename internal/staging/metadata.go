package staging

import (
	"sync"
	"time"
)

// DirtyFileIndex tracks which files are dirty (not yet synced to COS)
type DirtyFileIndex struct {
	dirty     map[string]*DirtyFileMetadata
	syncing   map[string]bool
	conflicts map[string]*ConflictMetadata
	mu        sync.RWMutex
}

// DirtyFileMetadata contains metadata about a dirty file
type DirtyFileMetadata struct {
	Path                 string
	ObjectKey            string
	ObservedETag         string
	ObservedSize         int64
	ObservedLastModified time.Time
	LocalDirtyGeneration int64
	StagedPath           string
	ConflictStatus       string
	Size                 int64
	DirtySince           time.Time
	LastModified         time.Time
	SyncAttempts         int
	LastSyncError        error
}

// ConflictMetadata records a dirty staged path whose COS object changed before
// the local staged bytes could be synced.
type ConflictMetadata struct {
	Path                  string
	ObjectKey             string
	Size                  int64
	PreservedPath         string
	PreservedMetadataPath string
	DetectedAt            time.Time
	Reason                string
	LocalDirtyGeneration  int64
	StagedPath            string
	ConflictStatus        string
	ObservedETag          string
	ObservedSize          int64
	ObservedLastModified  time.Time
	ExternalSize          int64
	ExternalETag          string
	ExternalLastModified  time.Time
	ExternalDeleted       bool
}

// NewDirtyFileIndex creates a new dirty file index
func NewDirtyFileIndex() *DirtyFileIndex {
	return &DirtyFileIndex{
		dirty:     make(map[string]*DirtyFileMetadata),
		syncing:   make(map[string]bool),
		conflicts: make(map[string]*ConflictMetadata),
	}
}

// MarkDirty marks a file as dirty (needs sync)
func (dfi *DirtyFileIndex) MarkDirty(path string, size int64) {
	dfi.MarkDirtyWithState(path, size, nil)
}

// MarkDirtyWithState marks a file as dirty and attaches any durable state known
// about the staged write.
func (dfi *DirtyFileIndex) MarkDirtyWithState(path string, size int64, state *PathMetadataState) {
	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	if _, conflicted := dfi.conflicts[path]; conflicted {
		return
	}

	now := time.Now()
	if meta, exists := dfi.dirty[path]; exists {
		meta.Size = size
		meta.LastModified = now
		meta.LocalDirtyGeneration++
		applyPathStateToDirtyMetadata(meta, state)
	} else {
		generation := int64(1)
		if state != nil && state.LocalDirtyGeneration > 0 {
			generation = state.LocalDirtyGeneration
		}
		meta := &DirtyFileMetadata{
			Path:                 path,
			ObjectKey:            objectKeyFromPath(path),
			LocalDirtyGeneration: generation,
			ConflictStatus:       ConflictStatusNone,
			Size:                 size,
			DirtySince:           now,
			LastModified:         now,
			SyncAttempts:         0,
		}
		applyPathStateToDirtyMetadata(meta, state)
		dfi.dirty[path] = meta
	}
}

// MarkClean marks a file as clean (synced to COS)
func (dfi *DirtyFileIndex) MarkClean(path string) {
	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	delete(dfi.dirty, path)
	delete(dfi.syncing, path)
}

// MarkConflicted records a conflict and removes the path from the upload queue.
func (dfi *DirtyFileIndex) MarkConflicted(meta *ConflictMetadata) {
	if meta == nil || meta.Path == "" {
		return
	}

	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	metaCopy := *meta
	delete(dfi.dirty, meta.Path)
	delete(dfi.syncing, meta.Path)
	dfi.conflicts[meta.Path] = &metaCopy
}

// RestoreDirty restores dirty metadata from a durable sidecar without bumping
// the local dirty generation.
func (dfi *DirtyFileIndex) RestoreDirty(meta DirtyFileMetadata) {
	if meta.Path == "" {
		return
	}

	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	if _, conflicted := dfi.conflicts[meta.Path]; conflicted {
		return
	}
	metaCopy := meta
	if metaCopy.ObjectKey == "" {
		metaCopy.ObjectKey = objectKeyFromPath(metaCopy.Path)
	}
	if metaCopy.LocalDirtyGeneration <= 0 {
		metaCopy.LocalDirtyGeneration = 1
	}
	if metaCopy.ConflictStatus == "" {
		metaCopy.ConflictStatus = ConflictStatusNone
	}
	dfi.dirty[meta.Path] = &metaCopy
}

// DropEntry removes the dirty entry without touching sync claims, unlike
// MarkClean which also releases the path's syncing lock.
func (dfi *DirtyFileIndex) DropEntry(path string) {
	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	delete(dfi.dirty, path)
}

// Rekey moves the dirty entry for oldPath to newPath, replacing any existing
// destination entry (rename-over semantics). The syncing map is intentionally
// untouched: sync claims belong to the workers holding them, and the bumped
// LastModified invalidates any in-flight snapshot of either path.
func (dfi *DirtyFileIndex) Rekey(oldPath, newPath, stagedPath string) {
	dfi.mu.Lock()
	defer dfi.mu.Unlock()

	now := time.Now()
	moved := DirtyFileMetadata{
		DirtySince:     now,
		ConflictStatus: ConflictStatusNone,
	}
	if meta, exists := dfi.dirty[oldPath]; exists {
		moved = *meta
		delete(dfi.dirty, oldPath)
	}
	moved.Path = newPath
	moved.ObjectKey = objectKeyFromPath(newPath)
	moved.StagedPath = stagedPath
	// The destination object's remote state is unknown; do not carry the
	// source's observed COS state across the rename.
	moved.ObservedETag = ""
	moved.ObservedSize = 0
	moved.ObservedLastModified = time.Time{}
	moved.LocalDirtyGeneration++
	if moved.LocalDirtyGeneration <= 0 {
		moved.LocalDirtyGeneration = 1
	}
	moved.LastModified = now
	moved.SyncAttempts = 0
	moved.LastSyncError = nil
	dfi.dirty[newPath] = &moved
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

// IsConflicted returns true when the path has an unresolved staging conflict.
func (dfi *DirtyFileIndex) IsConflicted(path string) bool {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	_, exists := dfi.conflicts[path]
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

// GetConflicts returns copies of all unresolved conflict records.
func (dfi *DirtyFileIndex) GetConflicts() []*ConflictMetadata {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	conflicts := make([]*ConflictMetadata, 0, len(dfi.conflicts))
	for _, meta := range dfi.conflicts {
		metaCopy := *meta
		conflicts = append(conflicts, &metaCopy)
	}

	return conflicts
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

func applyPathStateToDirtyMetadata(meta *DirtyFileMetadata, state *PathMetadataState) {
	if meta == nil || state == nil {
		return
	}
	if state.ObjectKey != "" {
		meta.ObjectKey = state.ObjectKey
	}
	meta.ObservedETag = state.ObservedETag
	meta.ObservedSize = state.ObservedSize
	meta.ObservedLastModified = state.ObservedLastModified
	if state.LocalDirtyGeneration > 0 {
		meta.LocalDirtyGeneration = state.LocalDirtyGeneration
	}
	if state.StagedFilePath != "" {
		meta.StagedPath = state.StagedFilePath
	}
	if state.ConflictStatus != "" {
		meta.ConflictStatus = state.ConflictStatus
	}
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

// ConflictCount returns the number of unresolved conflict records.
func (dfi *DirtyFileIndex) ConflictCount() int {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	return len(dfi.conflicts)
}

// LastConflictTime returns the newest conflict timestamp.
func (dfi *DirtyFileIndex) LastConflictTime() time.Time {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	var last time.Time
	for _, meta := range dfi.conflicts {
		if meta.DetectedAt.After(last) {
			last = meta.DetectedAt
		}
	}
	return last
}

// SyncingCount returns the number of files currently claimed by sync workers.
func (dfi *DirtyFileIndex) SyncingCount() int {
	dfi.mu.RLock()
	defer dfi.mu.RUnlock()

	return len(dfi.syncing)
}

// Made with Bob
