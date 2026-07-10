package staging

import (
	"os"
	"testing"
)

func readStagedBytes(t *testing.T, manager *StagingManager, path string, size int) string {
	t.Helper()

	session, exists := manager.GetSession(path)
	if !exists {
		t.Fatalf("no session for %s", path)
	}
	buf := make([]byte, size)
	if _, err := session.Read(buf, 0); err != nil {
		t.Fatalf("read staged bytes for %s: %v", path, err)
	}
	return string(buf)
}

func TestRenameStagedPathMovesStateAndTombstonesSource(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	oldPath := "/rename-src.txt"
	newPath := "/rename-dst.txt"
	payload := "rename payload"
	writeDirtyTestFile(t, manager, oldPath, payload)
	oldStaging := manager.stagingFilePath(oldPath)
	newStaging := manager.stagingFilePath(newPath)

	if err := manager.RenameStagedPath(oldPath, newPath); err != nil {
		t.Fatalf("RenameStagedPath() error = %v", err)
	}

	if manager.IsDirty(oldPath) {
		t.Fatal("source must not stay dirty")
	}
	if !manager.IsDirty(newPath) {
		t.Fatal("destination must be dirty")
	}
	if !manager.HasPendingDelete(oldPath) {
		t.Fatal("source must have a pending delete tombstone")
	}
	if manager.HasPendingDelete(newPath) {
		t.Fatal("destination must not have a pending delete")
	}
	if _, err := os.Stat(oldStaging); !os.IsNotExist(err) {
		t.Fatalf("source staging file should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(manager.pathMetadataPath(oldStaging)); !os.IsNotExist(err) {
		t.Fatalf("source sidecar should be gone, stat err = %v", err)
	}
	state, err := readPathMetadataState(manager.pathMetadataPath(newStaging))
	if err != nil {
		t.Fatalf("read destination sidecar: %v", err)
	}
	if state.OriginalPath != newPath {
		t.Fatalf("destination sidecar original_path = %q, want %q", state.OriginalPath, newPath)
	}
	if got := readStagedBytes(t, manager, newPath, len(payload)); got != payload {
		t.Fatalf("moved staged bytes = %q, want %q", got, payload)
	}
	if meta := manager.dirtyIndex.GetMetadata(newPath); meta == nil || meta.ObjectKey != "rename-dst.txt" {
		t.Fatalf("destination dirty metadata = %+v, want object key rename-dst.txt", meta)
	}
}

func TestRenameStagedPathWhileSourceSyncKeepsClaimAndDefers(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	oldPath := "/rename-syncing-src.txt"
	newPath := "/rename-syncing-dst.txt"
	writeDirtyTestFile(t, manager, oldPath, "in-flight payload")

	// Simulate a sync worker mid-upload of the source.
	if !manager.dirtyIndex.LockFile(oldPath) {
		t.Fatal("failed to claim sync lock for test")
	}

	if err := manager.RenameStagedPath(oldPath, newPath); err != nil {
		t.Fatalf("RenameStagedPath() during source sync error = %v", err)
	}

	// The worker's claim must be intact so the tombstone processor cannot
	// delete the old object before the in-flight upload lands.
	if !manager.dirtyIndex.IsSyncing(oldPath) {
		t.Fatal("in-flight sync claim on the source was released by the rename")
	}
	if !manager.HasPendingDelete(oldPath) {
		t.Fatal("source must have a pending delete tombstone")
	}
	if !manager.IsDirty(newPath) {
		t.Fatal("destination must be dirty")
	}

	// While the upload is in flight, tombstone processing must skip the path.
	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)
	worker.processPendingDeletes(0)
	if len(cosClient.GetDeletes()) != 0 {
		t.Fatalf("COS deletes = %v, want none while sync claim is held", cosClient.GetDeletes())
	}
	if !manager.HasPendingDelete(oldPath) {
		t.Fatal("tombstone must survive while the sync claim is held")
	}

	// Once the upload finishes (claim released), the tombstone completes.
	manager.dirtyIndex.UnlockFile(oldPath)
	worker.processPendingDeletes(0)
	deletes := cosClient.GetDeletes()
	if len(deletes) != 1 || deletes[0] != "rename-syncing-src.txt" {
		t.Fatalf("COS deletes = %v, want [rename-syncing-src.txt]", deletes)
	}
	if manager.HasPendingDelete(oldPath) {
		t.Fatal("tombstone should be resolved after the deferred delete")
	}
}

func TestRenameStagedPathReplacesDirtyDestination(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	oldPath := "/rename-replace-src.txt"
	newPath := "/rename-replace-dst.txt"
	writeDirtyTestFile(t, manager, newPath, "doomed destination bytes")
	writeDirtyTestFile(t, manager, oldPath, "winning source bytes!!")

	// A pending delete on the destination is superseded by the rename.
	if _, err := manager.RegisterPendingDelete(newPath); err != nil {
		t.Fatalf("RegisterPendingDelete() error = %v", err)
	}
	// Re-stage destination bytes after the delete to model dirty state.
	writeDirtyTestFile(t, manager, newPath, "doomed destination bytes")

	if err := manager.RenameStagedPath(oldPath, newPath); err != nil {
		t.Fatalf("RenameStagedPath() error = %v", err)
	}

	if manager.HasPendingDelete(newPath) {
		t.Fatal("destination pending delete must be canceled by the rename")
	}
	payload := "winning source bytes!!"
	if got := readStagedBytes(t, manager, newPath, len(payload)); got != payload {
		t.Fatalf("destination staged bytes = %q, want %q", got, payload)
	}
	if !manager.IsDirty(newPath) {
		t.Fatal("destination must be dirty with the moved bytes")
	}
	if manager.IsDirty(oldPath) {
		t.Fatal("source must not stay dirty")
	}
}

func TestRenameStagedPathSurvivesRestart(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}

	oldPath := "/rename-crash-src.txt"
	newPath := "/rename-crash-dst.txt"
	payload := "crash-safe rename bytes"
	writeDirtyTestFile(t, manager, oldPath, payload)

	if err := manager.RenameStagedPath(oldPath, newPath); err != nil {
		t.Fatalf("RenameStagedPath() error = %v", err)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	recovered, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() after crash error = %v", err)
	}
	defer recovered.Shutdown()

	if recovered.IsDirty(oldPath) {
		t.Fatal("source must not be restored as dirty after restart")
	}
	if !recovered.IsDirty(newPath) {
		t.Fatal("destination must be restored as dirty after restart")
	}
	if !recovered.HasPendingDelete(oldPath) {
		t.Fatal("source tombstone must be recovered after restart")
	}
	if got := readStagedBytes(t, recovered, newPath, len(payload)); got != payload {
		t.Fatalf("recovered staged bytes = %q, want %q", got, payload)
	}
}

func TestRenameStagedPathWithoutStagedBytesFallsBack(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	oldPath := "/rename-stale-src.txt"
	// Dirty entry without staged bytes on disk (stale bookkeeping).
	manager.dirtyIndex.MarkDirty(oldPath, 42)

	err = manager.RenameStagedPath(oldPath, "/rename-stale-dst.txt")
	if !os.IsNotExist(err) {
		t.Fatalf("RenameStagedPath() error = %v, want not-exist for stale dirty entry", err)
	}
	if manager.IsDirty(oldPath) {
		t.Fatal("stale dirty entry should be dropped")
	}
	if manager.HasPendingDelete(oldPath) {
		t.Fatal("no tombstone should be registered for a failed rename")
	}
}

func TestRenameStagedPathMovedBytesSyncToNewKey(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	oldPath := "/rename-sync-src.txt"
	newPath := "/rename-sync-dst.txt"
	payload := "bytes that sync to the new key"
	writeDirtyTestFile(t, manager, oldPath, payload)

	if err := manager.RenameStagedPath(oldPath, newPath); err != nil {
		t.Fatalf("RenameStagedPath() error = %v", err)
	}

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)
	if err := worker.syncFile(newPath); err != nil {
		t.Fatalf("syncFile() error = %v", err)
	}

	uploaded, ok := cosClient.GetUpload(newPath)
	if !ok {
		t.Fatal("moved bytes were not uploaded to the destination key")
	}
	if string(uploaded) != payload {
		t.Fatalf("uploaded bytes = %q, want %q", uploaded, payload)
	}
	if _, ok := cosClient.GetUpload(oldPath); ok {
		t.Fatal("nothing must be uploaded to the source key")
	}
	if manager.IsDirty(newPath) {
		t.Fatal("destination should be clean after sync")
	}
}

func TestSyncWorkerNotifiesObjectMutated(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)
	var mutated []string
	worker.SetObjectMutatedCallback(func(path string) {
		mutated = append(mutated, path)
	})

	// A successful sync upload must notify, so caches poisoned during the
	// dirty window (e.g. a zero-byte truncate object) get invalidated.
	syncPath := "/mutate-sync.txt"
	writeDirtyTestFile(t, manager, syncPath, "sync payload")
	if err := worker.syncFile(syncPath); err != nil {
		t.Fatalf("syncFile() error = %v", err)
	}
	if len(mutated) != 1 || mutated[0] != syncPath {
		t.Fatalf("mutated after sync = %v, want [%s]", mutated, syncPath)
	}

	// A deferred delete must notify as well.
	deletePath := "/mutate-delete.txt"
	writeDirtyTestFile(t, manager, deletePath, "delete payload")
	if _, err := manager.RegisterPendingDelete(deletePath); err != nil {
		t.Fatalf("RegisterPendingDelete() error = %v", err)
	}
	worker.processPendingDeletes(0)
	if len(mutated) != 2 || mutated[1] != deletePath {
		t.Fatalf("mutated after deferred delete = %v, want second entry %s", mutated, deletePath)
	}
}

// Made with Bob
