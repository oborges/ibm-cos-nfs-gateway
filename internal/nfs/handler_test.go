package nfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/oborges/cos-nfs-gateway/internal/cache"
	"github.com/oborges/cos-nfs-gateway/internal/config"
	"github.com/oborges/cos-nfs-gateway/internal/feature"
	"github.com/oborges/cos-nfs-gateway/internal/posix"
	"github.com/oborges/cos-nfs-gateway/internal/staging"
	"go.uber.org/zap"
)

func testStagingConfig(t *testing.T) *config.StagingConfig {
	t.Helper()

	return &config.StagingConfig{
		Enabled:          true,
		RootDir:          t.TempDir(),
		SyncInterval:     "30s",
		SyncThresholdMB:  1,
		MaxDirtyAge:      "5m",
		SyncOnClose:      false,
		MaxStagingSizeGB: 20,
		MaxDirtyFiles:    100,
		SyncWorkerCount:  2,
		SyncQueueSize:    10,
		MaxSyncRetries:   3,
		RetryBackoffInit: "1s",
		RetryBackoffMax:  "30s",
		CleanAfterSync:   true,
		StaleFileAge:     "24h",
	}
}

func TestCOSFilesystemChrootPreservesRecoveredStagingSessions(t *testing.T) {
	cfg := testStagingConfig(t)
	manager, err := staging.NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}

	path := "/crash-safe.bin"
	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("GetOrCreateSession() error = %v", err)
	}
	payload := []byte("crash recovery payload")
	if _, err := session.Write(payload, 0); err != nil {
		t.Fatalf("session.Write() error = %v", err)
	}
	if err := session.Sync(); err != nil {
		t.Fatalf("session.Sync() error = %v", err)
	}
	manager.MarkDirty(path, int64(len(payload)))
	if err := manager.Shutdown(); err != nil {
		t.Fatalf("manager.Shutdown() error = %v", err)
	}

	recoveredManager, err := staging.NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() after crash error = %v", err)
	}
	defer recoveredManager.Shutdown()
	recoveredSession, exists := recoveredManager.GetSession(path)
	if !exists {
		t.Fatalf("recovered staging session for %s not found", path)
	}
	if !recoveredSession.Dirty {
		t.Fatal("recovered staging session was not marked dirty")
	}

	perfConfig := &config.PerformanceConfig{
		WriteBufferKB:       4096,
		MaxBufferedWriteMB:  config.DefaultMaxBufferedWriteMB,
		MaxDirectoryEntries: config.DefaultMaxDirectoryEntries,
	}
	metadataCache := cache.NewMetadataCache(&config.MetadataCacheConfig{
		Enabled:    true,
		MaxEntries: 10,
		TTLSeconds: 60,
	})
	metadataCache.SetDirEntries("/", []os.FileInfo{})
	ops := posix.NewOperationsHandler(nil, metadataCache, nil, perfConfig)
	fs := NewCOSFilesystemWithConfig(
		ops,
		NewLogger(zap.NewNop()),
		"/",
		perfConfig,
		recoveredManager,
		nil,
		&feature.FeatureFlags{UseStagingPath: true},
	)

	chrooted, err := fs.Chroot("/")
	if err != nil {
		t.Fatalf("Chroot() error = %v", err)
	}

	chrootedFS, ok := chrooted.(*COSFilesystem)
	if !ok {
		t.Fatalf("Chroot() returned %T, want *COSFilesystem", chrooted)
	}
	if chrootedFS.stagingManager != recoveredManager {
		t.Fatal("Chroot() dropped staging manager")
	}
	if chrootedFS.featureFlags == nil || !chrootedFS.featureFlags.IsStagingEnabled() {
		t.Fatal("Chroot() dropped staging feature flags")
	}

	info, err := chrootedFS.Stat(filepath.Base(path))
	if err != nil {
		t.Fatalf("Stat() after chroot/recovery error = %v", err)
	}
	if info.Size() != int64(len(payload)) {
		t.Fatalf("Stat() size = %d, want %d", info.Size(), len(payload))
	}

	entries, err := chrootedFS.ReadDir("/")
	if err != nil {
		t.Fatalf("ReadDir() after chroot/recovery error = %v", err)
	}
	found := false
	for _, entry := range entries {
		if entry.Name() == filepath.Base(path) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ReadDir() did not include recovered staging file %s", path)
	}

	file, err := os.Open(recoveredSession.StagingPath)
	if err != nil {
		t.Fatalf("open recovered staging file: %v", err)
	}
	defer file.Close()
	recoveredPayload := make([]byte, len(payload))
	if _, err := file.ReadAt(recoveredPayload, 0); err != nil {
		t.Fatalf("read recovered staging payload: %v", err)
	}
	if string(recoveredPayload) != string(payload) {
		t.Fatalf("recovered payload = %q, want %q", recoveredPayload, payload)
	}
}

func TestCOSFileCloseReleasesStagingSessionOnce(t *testing.T) {
	cfg := testStagingConfig(t)
	manager, err := staging.NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	path := "/refcount-on-close.bin"
	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("GetOrCreateSession() error = %v", err)
	}
	session.IncrementRefCount()

	file := &COSFile{
		logger:         NewLogger(zap.NewNop()),
		path:           path,
		stagingManager: manager,
		stagingSession: session,
		featureFlags:   &feature.FeatureFlags{UseStagingPath: true},
	}

	if got := session.GetRefCount(); got != 2 {
		t.Fatalf("initial refcount = %d, want 2", got)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := session.GetRefCount(); got != 1 {
		t.Fatalf("refcount after Close() = %d, want 1", got)
	}
}

func TestCOSFilesystemRenameDirtyStagedFileIsBlocked(t *testing.T) {
	cfg := testStagingConfig(t)
	manager, err := staging.NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	path := "/dirty.txt"
	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("GetOrCreateSession() error = %v", err)
	}
	if _, err := session.Write([]byte("dirty data"), 0); err != nil {
		t.Fatalf("session.Write() error = %v", err)
	}
	manager.MarkDirty(path, session.Size)
	stagingPath := session.StagingPath

	fs := newDirtyStagingTestFilesystem(t, manager)
	err = fs.Rename("dirty.txt", "renamed.txt")
	if !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("Rename() error = %v, want EBUSY", err)
	}
	if !manager.IsDirty(path) {
		t.Fatal("dirty source path should remain dirty after blocked rename")
	}
	if _, exists := manager.GetSession("/renamed.txt"); exists {
		t.Fatal("blocked rename should not create a destination staging session")
	}
	if _, err := os.Stat(stagingPath); err != nil {
		t.Fatalf("dirty staging file should remain after blocked rename: %v", err)
	}
}

func TestCOSFilesystemRenameDirectoryWithDirtyChildIsBlocked(t *testing.T) {
	cfg := testStagingConfig(t)
	manager, err := staging.NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	path := "/dir/dirty.txt"
	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("GetOrCreateSession() error = %v", err)
	}
	if _, err := session.Write([]byte("dirty data"), 0); err != nil {
		t.Fatalf("session.Write() error = %v", err)
	}
	manager.MarkDirty(path, session.Size)

	fs := newDirtyStagingTestFilesystem(t, manager)
	err = fs.Rename("dir", "renamed-dir")
	if !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("Rename(directory) error = %v, want EBUSY", err)
	}
	if !manager.IsDirty(path) {
		t.Fatal("dirty child path should remain dirty after blocked directory rename")
	}
}

func TestCOSFilesystemRemoveDirtyStagedFileIsBlocked(t *testing.T) {
	cfg := testStagingConfig(t)
	manager, err := staging.NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	path := "/dirty-delete.txt"
	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("GetOrCreateSession() error = %v", err)
	}
	if _, err := session.Write([]byte("dirty data"), 0); err != nil {
		t.Fatalf("session.Write() error = %v", err)
	}
	manager.MarkDirty(path, session.Size)
	stagingPath := session.StagingPath

	fs := newDirtyStagingTestFilesystem(t, manager)
	err = fs.Remove("dirty-delete.txt")
	if !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("Remove() error = %v, want EBUSY", err)
	}
	if !manager.IsDirty(path) {
		t.Fatal("dirty path should remain dirty after blocked delete")
	}
	if _, err := os.Stat(stagingPath); err != nil {
		t.Fatalf("dirty staging file should remain after blocked delete: %v", err)
	}
}

func TestCOSFilesystemRemoveDirectoryWithDirtyChildIsBlocked(t *testing.T) {
	cfg := testStagingConfig(t)
	manager, err := staging.NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("NewStagingManager() error = %v", err)
	}
	defer manager.Shutdown()

	path := "/dir/dirty-delete.txt"
	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("GetOrCreateSession() error = %v", err)
	}
	if _, err := session.Write([]byte("dirty data"), 0); err != nil {
		t.Fatalf("session.Write() error = %v", err)
	}
	manager.MarkDirty(path, session.Size)

	fs := newDirtyStagingTestFilesystem(t, manager)
	err = fs.Remove("dir")
	if !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("Remove(directory) error = %v, want EBUSY", err)
	}
	if !manager.IsDirty(path) {
		t.Fatal("dirty child path should remain dirty after blocked directory remove")
	}
}

func newDirtyStagingTestFilesystem(t *testing.T, manager *staging.StagingManager) *COSFilesystem {
	t.Helper()

	perfConfig := &config.PerformanceConfig{
		WriteBufferKB:       4096,
		MaxBufferedWriteMB:  config.DefaultMaxBufferedWriteMB,
		MaxDirectoryEntries: config.DefaultMaxDirectoryEntries,
	}
	metadataCache := cache.NewMetadataCache(&config.MetadataCacheConfig{
		Enabled:    true,
		MaxEntries: 10,
		TTLSeconds: 60,
	})
	ops := posix.NewOperationsHandler(nil, metadataCache, nil, perfConfig)
	return NewCOSFilesystemWithConfig(
		ops,
		NewLogger(zap.NewNop()),
		"/",
		perfConfig,
		manager,
		nil,
		&feature.FeatureFlags{UseStagingPath: true},
	)
}
