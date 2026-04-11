package staging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oborges/cos-nfs-gateway/internal/config"
)

func createTestConfig(t *testing.T) *config.StagingConfig {
	tmpDir := t.TempDir()
	
	return &config.StagingConfig{
		Enabled:          true,
		RootDir:          tmpDir,
		SyncInterval:     "30s",
		SyncThresholdMB:  1, // 1MB
		MaxDirtyAge:      "5m",
		SyncOnClose:      false,
		MaxStagingSizeGB: 10,
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

func TestStagingManager_New(t *testing.T) {
	cfg := createTestConfig(t)

	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	// Verify active directory was created
	activeDir := filepath.Join(cfg.RootDir, "active")
	if _, err := os.Stat(activeDir); os.IsNotExist(err) {
		t.Error("Active directory was not created")
	}
}

func TestStagingManager_GetOrCreateSession(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	path := "/test/file.txt"

	// Create new session
	session1, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	if session1.Path != path {
		t.Errorf("Expected path %s, got %s", path, session1.Path)
	}

	if session1.GetRefCount() != 1 {
		t.Errorf("Expected RefCount 1, got %d", session1.GetRefCount())
	}

	// Get existing session
	session2, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	if session2 != session1 {
		t.Error("Should return same session instance")
	}

	if session2.GetRefCount() != 2 {
		t.Errorf("Expected RefCount 2, got %d", session2.GetRefCount())
	}
}

func TestStagingManager_ReleaseSession(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	path := "/test/file.txt"

	// Create session
	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Release session
	manager.ReleaseSession(path)

	if session.GetRefCount() != 0 {
		t.Errorf("Expected RefCount 0, got %d", session.GetRefCount())
	}

	// Session should still exist (not cleaned up yet)
	_, exists := manager.GetSession(path)
	if !exists {
		t.Error("Session should still exist after release")
	}
}

func TestStagingManager_MarkDirty(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	path := "/test/file.txt"
	size := int64(1024)

	manager.MarkDirty(path, size)

	if !manager.IsDirty(path) {
		t.Error("File should be marked as dirty")
	}

	dirtyFiles := manager.GetDirtyFiles()
	if len(dirtyFiles) != 1 {
		t.Errorf("Expected 1 dirty file, got %d", len(dirtyFiles))
	}

	if dirtyFiles[0].Path != path {
		t.Errorf("Expected path %s, got %s", path, dirtyFiles[0].Path)
	}

	if dirtyFiles[0].Size != size {
		t.Errorf("Expected size %d, got %d", size, dirtyFiles[0].Size)
	}
}

func TestStagingManager_MarkClean(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	path := "/test/file.txt"

	manager.MarkDirty(path, 1024)
	manager.MarkClean(path)

	if manager.IsDirty(path) {
		t.Error("File should not be dirty after marking clean")
	}

	dirtyFiles := manager.GetDirtyFiles()
	if len(dirtyFiles) != 0 {
		t.Errorf("Expected 0 dirty files, got %d", len(dirtyFiles))
	}
}

func TestStagingManager_GetSession(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	path := "/test/file.txt"

	// Get non-existent session
	_, exists := manager.GetSession(path)
	if exists {
		t.Error("Session should not exist")
	}

	// Create session
	session1, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Get existing session
	session2, exists := manager.GetSession(path)
	if !exists {
		t.Error("Session should exist")
	}

	if session2 != session1 {
		t.Error("Should return same session instance")
	}
}

func TestStagingManager_CleanupSession(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	path := "/test/file.txt"

	// Create session and write data
	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	data := []byte("Hello, World!")
	session.Write(data, 0)
	session.Sync()

	stagingPath := session.StagingPath

	// Cleanup with file deletion
	err = manager.CleanupSession(path, true)
	if err != nil {
		t.Fatalf("Failed to cleanup session: %v", err)
	}

	// Session should not exist
	_, exists := manager.GetSession(path)
	if exists {
		t.Error("Session should not exist after cleanup")
	}

	// Staging file should be deleted
	if _, err := os.Stat(stagingPath); !os.IsNotExist(err) {
		t.Error("Staging file should be deleted")
	}
}

func TestStagingManager_CleanupSessionKeepFile(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	path := "/test/file.txt"

	// Create session
	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	stagingPath := session.StagingPath

	// Cleanup without file deletion
	err = manager.CleanupSession(path, false)
	if err != nil {
		t.Fatalf("Failed to cleanup session: %v", err)
	}

	// Staging file should still exist
	if _, err := os.Stat(stagingPath); os.IsNotExist(err) {
		t.Error("Staging file should exist")
	}

	// Clean up
	os.Remove(stagingPath)
}

func TestStagingManager_Shutdown(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}

	// Create multiple sessions
	paths := []string{"/test/file1.txt", "/test/file2.txt", "/test/file3.txt"}
	for _, path := range paths {
		_, err := manager.GetOrCreateSession(path)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}
	}

	// Shutdown
	err = manager.Shutdown()
	if err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	// All sessions should be closed
	stats := manager.Stats()
	totalSessions := stats["total_sessions"].(int)
	if totalSessions != 0 {
		t.Errorf("Expected 0 sessions after shutdown, got %d", totalSessions)
	}
}

func TestStagingManager_Stats(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	// Create sessions
	session1, _ := manager.GetOrCreateSession("/test/file1.txt")
	session2, _ := manager.GetOrCreateSession("/test/file2.txt")
	manager.GetOrCreateSession("/test/file3.txt")

	// Release one session
	manager.ReleaseSession("/test/file1.txt")

	// Mark files as dirty
	manager.MarkDirty("/test/file1.txt", 1024)
	manager.MarkDirty("/test/file2.txt", 2048)

	stats := manager.Stats()

	totalSessions := stats["total_sessions"].(int)
	if totalSessions != 3 {
		t.Errorf("Expected 3 total sessions, got %d", totalSessions)
	}

	activeSessions := stats["active_sessions"].(int)
	if activeSessions != 2 { // file2 and file3 have RefCount > 0
		t.Errorf("Expected 2 active sessions, got %d", activeSessions)
	}

	dirtyFiles := stats["dirty_files"].(int)
	if dirtyFiles != 2 {
		t.Errorf("Expected 2 dirty files, got %d", dirtyFiles)
	}

	// Clean up refs
	session1.DecrementRefCount()
	session2.DecrementRefCount()
}

func TestStagingManager_MultipleSessions(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	// Create multiple sessions
	numSessions := 10
	for i := 0; i < numSessions; i++ {
		path := "/test/file" + string(rune(i)) + ".txt"
		_, err := manager.GetOrCreateSession(path)
		if err != nil {
			t.Fatalf("Failed to create session %d: %v", i, err)
		}
	}

	stats := manager.Stats()
	totalSessions := stats["total_sessions"].(int)
	if totalSessions != numSessions {
		t.Errorf("Expected %d sessions, got %d", numSessions, totalSessions)
	}
}

func TestStagingManager_StagingFilePath(t *testing.T) {
	cfg := createTestConfig(t)
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager.Shutdown()

	// Create two sessions with different paths
	session1, _ := manager.GetOrCreateSession("/test/file1.txt")
	session2, _ := manager.GetOrCreateSession("/test/file2.txt")

	// Staging paths should be different
	if session1.StagingPath == session2.StagingPath {
		t.Error("Different logical paths should have different staging paths")
	}

	// Staging paths should be in active directory
	activeDir := filepath.Join(cfg.RootDir, "active")
	if filepath.Dir(session1.StagingPath) != activeDir {
		t.Errorf("Staging path should be in active directory: %s", session1.StagingPath)
	}

	// Same path should get same staging path
	session3, _ := manager.GetOrCreateSession("/test/file1.txt")
	if session3.StagingPath != session1.StagingPath {
		t.Error("Same logical path should have same staging path")
	}
}

func TestStagingManager_RecoverFromDisk(t *testing.T) {
	cfg := createTestConfig(t)

	// Create first manager and add some files
	manager1, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}

	session, _ := manager1.GetOrCreateSession("/test/file.txt")
	session.Write([]byte("test data"), 0)
	session.Sync()

	manager1.Shutdown()

	// Create second manager (should recover)
	manager2, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("Failed to create staging manager: %v", err)
	}
	defer manager2.Shutdown()

	// Recovery should not fail (though it may not fully restore state in MVP)
	// This test mainly ensures RecoverFromDisk doesn't panic
}

// Made with Bob
