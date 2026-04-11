package staging

import (
	"context"
	"strings"
	"testing"
	"time"
)

// MockCOSClient for testing
type MockCOSClient struct {
	uploads      map[string][]byte
	errors       map[string]error
	putObjectFn  func(ctx context.Context, path string, data []byte, metadata map[string]string) error
}

func NewMockCOSClient() *MockCOSClient {
	m := &MockCOSClient{
		uploads: make(map[string][]byte),
		errors:  make(map[string]error),
	}
	m.putObjectFn = m.defaultPutObject
	return m
}

func (m *MockCOSClient) defaultPutObject(ctx context.Context, path string, data []byte, metadata map[string]string) error {
	if err, exists := m.errors[path]; exists {
		return err
	}
	m.uploads[path] = data
	return nil
}

func (m *MockCOSClient) PutObject(ctx context.Context, path string, data []byte, metadata map[string]string) error {
	return m.putObjectFn(ctx, path, data, metadata)
}

func (m *MockCOSClient) SetError(path string, err error) {
	m.errors[path] = err
}

func (m *MockCOSClient) GetUpload(path string) ([]byte, bool) {
	data, exists := m.uploads[path]
	return data, exists
}

func TestSyncWorker_New(t *testing.T) {
	cfg := createTestConfig(t)
	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	if worker == nil {
		t.Fatal("Failed to create sync worker")
	}

	if worker.manager != manager {
		t.Error("Manager not set correctly")
	}

	if worker.config != cfg {
		t.Error("Config not set correctly")
	}
}

func TestSyncWorker_StartStop(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.SyncInterval = "100ms"

	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	// Start worker
	worker.Start()

	// Let it run briefly
	time.Sleep(200 * time.Millisecond)

	// Stop worker
	worker.Stop()

	// Should not panic
}

func TestSyncWorker_ShouldSync_SizeThreshold(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.SyncThresholdMB = 0 // Will use bytes calculation in test

	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	// Set threshold to 1KB (in MB)
	cfg.SyncThresholdMB = 1
	threshold := cfg.SyncThresholdMB * 1024 * 1024

	// File below threshold
	metadata1 := &DirtyFileMetadata{
		Path:       "/test/small.txt",
		Size:       threshold - 1,
		DirtySince: time.Now(),
	}

	if worker.shouldSync(metadata1) {
		t.Error("Should not sync file below threshold")
	}

	// File at threshold
	metadata2 := &DirtyFileMetadata{
		Path:       "/test/exact.txt",
		Size:       threshold,
		DirtySince: time.Now(),
	}

	if !worker.shouldSync(metadata2) {
		t.Error("Should sync file at threshold")
	}

	// File above threshold
	metadata3 := &DirtyFileMetadata{
		Path:       "/test/large.txt",
		Size:       threshold + 1,
		DirtySince: time.Now(),
	}

	if !worker.shouldSync(metadata3) {
		t.Error("Should sync file above threshold")
	}
}

func TestSyncWorker_ShouldSync_AgeThreshold(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.MaxDirtyAge = "1s"

	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	// Recent file
	metadata1 := &DirtyFileMetadata{
		Path:       "/test/recent.txt",
		Size:       512,
		DirtySince: time.Now(),
	}

	if worker.shouldSync(metadata1) {
		t.Error("Should not sync recent file")
	}

	// Old file
	metadata2 := &DirtyFileMetadata{
		Path:       "/test/old.txt",
		Size:       512,
		DirtySince: time.Now().Add(-2 * time.Second),
	}

	if !worker.shouldSync(metadata2) {
		t.Error("Should sync old file")
	}
}

func TestSyncWorker_ShouldSync_IdleSession(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.SyncThresholdMB = 10 // High threshold (10MB)

	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	path := "/test/file.txt"

	// Create session and write data
	session, _ := manager.GetOrCreateSession(path)
	session.Write([]byte("test"), 0)
	manager.MarkDirty(path, 4)

	// Release session (make it idle)
	manager.ReleaseSession(path)

	// Wait for idle threshold (5 seconds + buffer)
	time.Sleep(5100 * time.Millisecond)

	metadata := &DirtyFileMetadata{
		Path:       path,
		Size:       4,
		DirtySince: time.Now().Add(-6 * time.Second), // Ensure it's been dirty long enough
	}

	// Should sync idle session even if below size threshold
	if !worker.shouldSync(metadata) {
		t.Error("Should sync idle session")
	}
}

func TestSyncWorker_SyncFile_Success(t *testing.T) {
	cfg := createTestConfig(t)
	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	path := "/test/file.txt"
	data := []byte("Hello, World!")

	// Create session and write data
	session, _ := manager.GetOrCreateSession(path)
	session.Write(data, 0)
	session.Sync()
	manager.MarkDirty(path, int64(len(data)))

	// Sync file
	err := worker.syncFile(path)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Verify upload
	uploaded, exists := cosClient.GetUpload(path)
	if !exists {
		t.Fatal("File was not uploaded")
	}

	if string(uploaded) != string(data) {
		t.Errorf("Expected %s, got %s", data, uploaded)
	}

	// File should be marked clean
	if manager.IsDirty(path) {
		t.Error("File should be marked clean after sync")
	}
}

func TestSyncWorker_SyncFile_Retry(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.MaxSyncRetries = 4 // Need 4 to allow 3 failures + 1 success
	cfg.RetryBackoffInit = "10ms"

	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	path := "/test/file.txt"
	data := []byte("test data")

	// Create session
	session, _ := manager.GetOrCreateSession(path)
	session.Write(data, 0)
	session.Sync()
	manager.MarkDirty(path, int64(len(data)))

	// Set error for first 2 attempts
	attemptCount := 0
	cosClient.SetError(path, &temporaryError{msg: "temporary failure"})

	// Override PutObject to count attempts and succeed on 3rd
	cosClient.putObjectFn = func(ctx context.Context, p string, d []byte, m map[string]string) error {
		attemptCount++
		if attemptCount <= 2 {
			return &temporaryError{msg: "temporary failure"}
		}
		return nil // Success on 3rd attempt
	}

	// Sync should succeed after retries
	err := worker.syncFile(path)
	if err != nil {
		t.Fatalf("Sync should succeed after retries: %v", err)
	}

	if attemptCount != 3 {
		t.Errorf("Expected exactly 3 attempts, got %d", attemptCount)
	}
}

type temporaryError struct {
	msg string
}

func (e *temporaryError) Error() string {
	return e.msg
}

func TestSyncWorker_TriggerSync(t *testing.T) {
	cfg := createTestConfig(t)
	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	path := "/test/file.txt"
	data := []byte("manual sync test")

	// Create session
	session, _ := manager.GetOrCreateSession(path)
	session.Write(data, 0)
	session.Sync()
	manager.MarkDirty(path, int64(len(data)))

	// Trigger manual sync
	err := worker.TriggerSync(path)
	if err != nil {
		t.Fatalf("Manual sync failed: %v", err)
	}

	// Verify upload
	uploaded, exists := cosClient.GetUpload(path)
	if !exists {
		t.Fatal("File was not uploaded")
	}

	if string(uploaded) != string(data) {
		t.Errorf("Expected %s, got %s", data, uploaded)
	}
}

func TestSyncWorker_TriggerSync_NotDirty(t *testing.T) {
	cfg := createTestConfig(t)
	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	path := "/test/clean.txt"

	// Trigger sync on clean file (should not error)
	err := worker.TriggerSync(path)
	if err != nil {
		t.Errorf("Sync of clean file should not error: %v", err)
	}

	// Should not have uploaded anything
	_, exists := cosClient.GetUpload(path)
	if exists {
		t.Error("Clean file should not be uploaded")
	}
}

func TestSyncWorker_Stats(t *testing.T) {
	cfg := createTestConfig(t)
	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	// Mark some files as dirty
	manager.MarkDirty("/test/file1.txt", 1024)
	manager.MarkDirty("/test/file2.txt", 2048)
	manager.MarkDirty("/test/file3.txt", 4096)

	stats := worker.Stats()

	dirtyFiles := stats["dirty_files"].(int)
	if dirtyFiles != 3 {
		t.Errorf("Expected 3 dirty files, got %d", dirtyFiles)
	}

	totalSize := stats["total_size"].(int64)
	expectedSize := int64(1024 + 2048 + 4096)
	if totalSize != expectedSize {
		t.Errorf("Expected total size %d, got %d", expectedSize, totalSize)
	}

	workerCount := stats["worker_count"].(int)
	if workerCount != cfg.SyncWorkerCount {
		t.Errorf("Expected worker count %d, got %d", cfg.SyncWorkerCount, workerCount)
	}
}

func TestSyncWorker_Stats_OldestDirty(t *testing.T) {
	cfg := createTestConfig(t)
	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	// Mark file as dirty and wait
	manager.MarkDirty("/test/old.txt", 1024)
	time.Sleep(100 * time.Millisecond)

	stats := worker.Stats()

	oldestAge, exists := stats["oldest_dirty_age"]
	if !exists {
		t.Error("oldest_dirty_age should be present")
	}

	ageStr := oldestAge.(string)
	if !strings.Contains(ageStr, "ms") && !strings.Contains(ageStr, "s") {
		t.Errorf("Age string format unexpected: %s", ageStr)
	}
}

func TestSyncWorker_CleanupAfterSync(t *testing.T) {
	cfg := createTestConfig(t)
	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	path := "/test/file.txt"
	data := []byte("cleanup test")

	// Create session with RefCount = 0 (idle)
	session, _ := manager.GetOrCreateSession(path)
	session.Write(data, 0)
	session.Sync()
	manager.ReleaseSession(path) // RefCount = 0
	manager.MarkDirty(path, int64(len(data)))

	// Sync should cleanup idle session
	err := worker.syncFile(path)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Session should be cleaned up
	_, exists := manager.GetSession(path)
	if exists {
		t.Error("Idle session should be cleaned up after sync")
	}
}

func TestSyncWorker_MultipleFiles(t *testing.T) {
	cfg := createTestConfig(t)
	manager, _ := NewStagingManager(cfg)
	defer manager.Shutdown()

	cosClient := NewMockCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	// Create multiple files
	files := map[string][]byte{
		"/test/file1.txt": []byte("data1"),
		"/test/file2.txt": []byte("data2"),
		"/test/file3.txt": []byte("data3"),
	}

	for path, data := range files {
		session, _ := manager.GetOrCreateSession(path)
		session.Write(data, 0)
		session.Sync()
		manager.MarkDirty(path, int64(len(data)))
	}

	// Sync all files
	for path := range files {
		err := worker.syncFile(path)
		if err != nil {
			t.Fatalf("Sync failed for %s: %v", path, err)
		}
	}

	// Verify all uploads
	for path, expectedData := range files {
		uploaded, exists := cosClient.GetUpload(path)
		if !exists {
			t.Errorf("File %s was not uploaded", path)
			continue
		}

		if string(uploaded) != string(expectedData) {
			t.Errorf("File %s: expected %s, got %s", path, expectedData, uploaded)
		}
	}
}

// Made with Bob
