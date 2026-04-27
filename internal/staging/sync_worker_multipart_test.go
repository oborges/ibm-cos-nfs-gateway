package staging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/IBM/ibm-cos-sdk-go/service/s3"
)

type multipartLifecycleCOSClient struct {
	mu                 sync.Mutex
	nextUpload         int
	uploads            map[string]*multipartLifecycleUpload
	objects            map[string]int64
	createCount        int
	completeCount      int
	abortCount         int
	uploadPartCount    int
	failNoSuchOnce     bool
	noSuchInjected     bool
	beforeUploadPartFn func(uploadID string, partNumber int64)
	beforeCompleteFn   func(uploadID string)
}

type multipartLifecycleUpload struct {
	key   string
	parts map[int64]int64
}

func newMultipartLifecycleCOSClient() *multipartLifecycleCOSClient {
	return &multipartLifecycleCOSClient{
		uploads: make(map[string]*multipartLifecycleUpload),
		objects: make(map[string]int64),
	}
}

func (m *multipartLifecycleCOSClient) PutObject(context.Context, string, []byte, map[string]string) error {
	return fmt.Errorf("unexpected PutObject call")
}

func (m *multipartLifecycleCOSClient) PutObjectStream(ctx context.Context, key string, body io.ReadSeeker, metadata map[string]string) error {
	n, err := io.Copy(io.Discard, body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.objects[key] = n
	m.mu.Unlock()
	return nil
}

func (m *multipartLifecycleCOSClient) GetObjectStream(ctx context.Context, key string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *multipartLifecycleCOSClient) CreateMultipartUpload(ctx context.Context, key string, metadata map[string]string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextUpload++
	uploadID := fmt.Sprintf("upload-%d", m.nextUpload)
	m.uploads[uploadID] = &multipartLifecycleUpload{key: key, parts: make(map[int64]int64)}
	m.createCount++
	return uploadID, nil
}

func (m *multipartLifecycleCOSClient) UploadPart(ctx context.Context, key, uploadID string, partNumber int64, body io.ReadSeeker) (string, error) {
	if m.beforeUploadPartFn != nil {
		m.beforeUploadPartFn(uploadID, partNumber)
	}

	n, err := io.Copy(io.Discard, body)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNoSuchOnce && !m.noSuchInjected {
		m.noSuchInjected = true
		delete(m.uploads, uploadID)
		return "", fmt.Errorf("NoSuchUpload: Invalid uploadId:%s", uploadID)
	}

	upload, ok := m.uploads[uploadID]
	if !ok || upload.key != key {
		return "", fmt.Errorf("NoSuchUpload: Invalid uploadId:%s", uploadID)
	}

	m.uploadPartCount++
	upload.parts[partNumber] = n
	return fmt.Sprintf("etag-%s-%d", uploadID, partNumber), nil
}

func (m *multipartLifecycleCOSClient) CompleteMultipartUpload(ctx context.Context, key, uploadID string, completedParts []*s3.CompletedPart) error {
	if m.beforeCompleteFn != nil {
		m.beforeCompleteFn(uploadID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	upload, ok := m.uploads[uploadID]
	if !ok || upload.key != key {
		return fmt.Errorf("NoSuchUpload: Invalid uploadId:%s", uploadID)
	}

	var total int64
	var previous int64
	for _, part := range completedParts {
		if part == nil || part.PartNumber == nil || part.ETag == nil {
			return fmt.Errorf("invalid completed part")
		}
		if *part.PartNumber <= previous {
			return fmt.Errorf("parts are not ordered")
		}
		previous = *part.PartNumber
		total += upload.parts[*part.PartNumber]
	}

	m.objects[key] = total
	delete(m.uploads, uploadID)
	m.completeCount++
	return nil
}

func (m *multipartLifecycleCOSClient) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.uploads[uploadID]; !ok {
		m.abortCount++
		return fmt.Errorf("NoSuchUpload: Invalid uploadId:%s", uploadID)
	}
	delete(m.uploads, uploadID)
	m.abortCount++
	return nil
}

func (m *multipartLifecycleCOSClient) objectSize(key string) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	size, ok := m.objects[key]
	return size, ok
}

func createSparseDirtySession(t *testing.T, manager *StagingManager, path string, size int64) *WriteSession {
	t.Helper()

	session, err := manager.GetOrCreateSession(path)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.Truncate(size); err != nil {
		t.Fatalf("truncate sparse session: %v", err)
	}
	if err := session.Sync(); err != nil {
		t.Fatalf("sync sparse session: %v", err)
	}
	manager.MarkDirty(path, size)
	return session
}

func TestIntegrationMultipartSync1GiBSucceeds(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.CleanAfterSync = false
	cfg.RetryBackoffInit = "1ms"
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer manager.Shutdown()

	const size = int64(1 << 30)
	path := "/multipart/one-gib.bin"
	createSparseDirtySession(t, manager, path, size)

	cosClient := newMultipartLifecycleCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)
	if err := worker.syncFile(path); err != nil {
		t.Fatalf("multipart sync failed: %v", err)
	}

	uploadedSize, ok := cosClient.objectSize(path)
	if !ok {
		t.Fatal("object was not completed")
	}
	if uploadedSize != size {
		t.Fatalf("uploaded size mismatch: got %d want %d", uploadedSize, size)
	}
	if cosClient.completeCount != 1 {
		t.Fatalf("CompleteMultipartUpload count = %d, want 1", cosClient.completeCount)
	}
	if manager.IsDirty(path) {
		t.Fatal("file should be clean after successful multipart sync")
	}
}

func TestMultipartConcurrentSyncAttemptsDoNotRace(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.CleanAfterSync = false
	cfg.RetryBackoffInit = "1ms"
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer manager.Shutdown()

	const size = int64(64 << 20)
	path := "/multipart/concurrent.bin"
	createSparseDirtySession(t, manager, path, size)

	cosClient := newMultipartLifecycleCOSClient()
	worker := NewSyncWorker(manager, cosClient, cfg)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := worker.TriggerSync(path); err != nil {
				t.Errorf("trigger sync failed: %v", err)
			}
		}()
	}
	wg.Wait()

	uploadedSize, ok := cosClient.objectSize(path)
	if !ok || uploadedSize != size {
		t.Fatalf("uploaded size mismatch: got %d exists=%v want %d", uploadedSize, ok, size)
	}
	if cosClient.completeCount != 1 {
		t.Fatalf("CompleteMultipartUpload count = %d, want 1", cosClient.completeCount)
	}
	if cosClient.createCount != 1 {
		t.Fatalf("CreateMultipartUpload count = %d, want 1", cosClient.createCount)
	}
}

func TestMultipartNoSuchUploadRestartsCleanly(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.CleanAfterSync = false
	cfg.RetryBackoffInit = "1ms"
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer manager.Shutdown()

	const size = int64(64 << 20)
	path := "/multipart/retry-nosuch.bin"
	createSparseDirtySession(t, manager, path, size)

	cosClient := newMultipartLifecycleCOSClient()
	cosClient.failNoSuchOnce = true
	worker := NewSyncWorker(manager, cosClient, cfg)
	if err := worker.syncFile(path); err != nil {
		t.Fatalf("sync should restart after NoSuchUpload: %v", err)
	}

	uploadedSize, ok := cosClient.objectSize(path)
	if !ok || uploadedSize != size {
		t.Fatalf("uploaded size mismatch: got %d exists=%v want %d", uploadedSize, ok, size)
	}
	if cosClient.createCount < 2 {
		t.Fatalf("expected multipart restart with a new upload id, createCount=%d", cosClient.createCount)
	}
	if cosClient.completeCount != 1 {
		t.Fatalf("CompleteMultipartUpload count = %d, want 1", cosClient.completeCount)
	}
}

func TestMultipartSnapshotChangeBeforeCompleteKeepsDirtyAndDoesNotPublishPartialObject(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.CleanAfterSync = false
	cfg.RetryBackoffInit = "1ms"
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer manager.Shutdown()

	const initialSize = int64(64 << 20)
	const finalSize = int64(80 << 20)
	path := "/multipart/snapshot-changed.bin"
	session := createSparseDirtySession(t, manager, path, initialSize)

	var once sync.Once
	cosClient := newMultipartLifecycleCOSClient()
	cosClient.beforeUploadPartFn = func(uploadID string, partNumber int64) {
		if partNumber == 4 {
			once.Do(func() {
				if err := session.Truncate(finalSize); err != nil {
					t.Errorf("truncate during multipart upload: %v", err)
				}
				manager.MarkDirty(path, finalSize)
			})
		}
	}

	worker := NewSyncWorker(manager, cosClient, cfg)
	if err := worker.syncFile(path); !errors.Is(err, errMultipartSnapshotChanged) {
		t.Fatalf("expected snapshot changed error, got %v", err)
	}

	if _, ok := cosClient.objectSize(path); ok {
		t.Fatal("partial multipart snapshot should not be completed")
	}
	if !manager.IsDirty(path) {
		t.Fatal("file should remain dirty after snapshot changes during multipart upload")
	}
	if cosClient.completeCount != 0 {
		t.Fatalf("CompleteMultipartUpload count = %d, want 0", cosClient.completeCount)
	}
	if cosClient.abortCount == 0 {
		t.Fatal("changed multipart snapshot should be aborted")
	}
}

func TestMultipartChangedAfterCompleteKeepsDirtyForRetry(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.CleanAfterSync = false
	cfg.RetryBackoffInit = "1ms"
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer manager.Shutdown()

	const initialSize = int64(64 << 20)
	const finalSize = int64(80 << 20)
	path := "/multipart/changed-after-complete.bin"
	session := createSparseDirtySession(t, manager, path, initialSize)

	var once sync.Once
	cosClient := newMultipartLifecycleCOSClient()
	cosClient.beforeCompleteFn = func(uploadID string) {
		once.Do(func() {
			if err := session.Truncate(finalSize); err != nil {
				t.Errorf("truncate during multipart upload: %v", err)
			}
			manager.MarkDirty(path, finalSize)
		})
	}

	worker := NewSyncWorker(manager, cosClient, cfg)
	if err := worker.syncFile(path); err == nil {
		t.Fatal("expected changed snapshot to keep file dirty")
	}

	if !manager.IsDirty(path) {
		t.Fatal("file should remain dirty when it changes immediately after complete")
	}
}

func TestMultipartCleanupDoesNotAbortActiveUpload(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.CleanAfterSync = false
	cfg.RetryBackoffInit = "1ms"
	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer manager.Shutdown()

	const size = int64(64 << 20)
	path := "/multipart/cleanup-active.bin"
	createSparseDirtySession(t, manager, path, size)

	partStarted := make(chan struct{})
	releasePart := make(chan struct{})
	var once sync.Once
	cosClient := newMultipartLifecycleCOSClient()
	cosClient.beforeUploadPartFn = func(uploadID string, partNumber int64) {
		if partNumber != 1 {
			return
		}
		once.Do(func() { close(partStarted) })
		<-releasePart
	}

	worker := NewSyncWorker(manager, cosClient, cfg)
	if !manager.dirtyIndex.LockFile(path) {
		t.Fatal("failed to claim dirty file")
	}
	done := make(chan error, 1)
	go func() {
		defer manager.dirtyIndex.UnlockFile(path)
		done <- worker.syncFileWithWorker(path, 7)
	}()

	<-partStarted
	if err := manager.CleanupSession(path, true); err == nil {
		t.Fatal("cleanup should be refused while sync is active")
	}
	if cosClient.abortCount != 0 {
		t.Fatalf("cleanup aborted active multipart upload: abortCount=%d", cosClient.abortCount)
	}

	close(releasePart)
	if err := <-done; err != nil {
		t.Fatalf("sync failed after cleanup refusal: %v", err)
	}
}

func TestMultipartRestartDuringUploadRestartsCleanly(t *testing.T) {
	cfg := createTestConfig(t)
	cfg.CleanAfterSync = false
	cfg.RetryBackoffInit = "1ms"

	manager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	const size = int64(64 << 20)
	path := "/multipart/restart.bin"
	session := createSparseDirtySession(t, manager, path, size)
	session.Multipart.UploadID = "lost-upload-id"
	session.Multipart.Active = true
	if err := manager.Shutdown(); err != nil {
		t.Fatalf("shutdown manager: %v", err)
	}

	recoveredManager, err := NewStagingManager(cfg)
	if err != nil {
		t.Fatalf("recover manager: %v", err)
	}
	defer recoveredManager.Shutdown()

	cosClient := newMultipartLifecycleCOSClient()
	worker := NewSyncWorker(recoveredManager, cosClient, cfg)
	if err := worker.syncFile(path); err != nil {
		t.Fatalf("sync after restart should start a clean multipart upload: %v", err)
	}

	uploadedSize, ok := cosClient.objectSize(path)
	if !ok || uploadedSize != size {
		t.Fatalf("uploaded size mismatch: got %d exists=%v want %d", uploadedSize, ok, size)
	}
	if cosClient.createCount != 1 {
		t.Fatalf("expected one fresh multipart upload after restart, got %d", cosClient.createCount)
	}
}
