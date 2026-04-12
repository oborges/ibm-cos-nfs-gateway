package staging

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/IBM/ibm-cos-sdk-go/service/s3"
	"github.com/oborges/cos-nfs-gateway/internal/config"
	"github.com/oborges/cos-nfs-gateway/internal/logging"
	"go.uber.org/zap"
)

// COSClient interface for uploading objects
type COSClient interface {
	PutObject(ctx context.Context, key string, data []byte, metadata map[string]string) error
	PutObjectStream(ctx context.Context, key string, body io.ReadSeeker, metadata map[string]string) error
	GetObjectStream(ctx context.Context, key string) (io.ReadCloser, error)
	CreateMultipartUpload(ctx context.Context, key string, metadata map[string]string) (string, error)
	UploadPart(ctx context.Context, key, uploadID string, partNumber int64, body io.ReadSeeker) (string, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, completedParts []*s3.CompletedPart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
}

// SyncWorker handles background synchronization of dirty files to COS
type SyncWorker struct {
	manager    *StagingManager
	cosClient  COSClient
	config     *config.StagingConfig
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	syncTicker *time.Ticker
}

// NewSyncWorker creates a new sync worker
func NewSyncWorker(manager *StagingManager, cosClient COSClient, cfg *config.StagingConfig) *SyncWorker {
	ctx, cancel := context.WithCancel(context.Background())

	return &SyncWorker{
		manager:   manager,
		cosClient: cosClient,
		config:    cfg,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start begins the background sync process
func (sw *SyncWorker) Start() {
	syncInterval, _ := sw.config.GetSyncInterval()
	sw.syncTicker = time.NewTicker(syncInterval)

	// Start worker goroutines
	for i := 0; i < sw.config.SyncWorkerCount; i++ {
		sw.wg.Add(1)
		go sw.workerLoop(i)
	}

	logging.Info("Sync worker started",
		zap.Int("workers", sw.config.SyncWorkerCount),
		zap.Duration("interval", syncInterval))
}

// Stop gracefully stops the sync worker
func (sw *SyncWorker) Stop() {
	logging.Info("Stopping sync worker")

	if sw.syncTicker != nil {
		sw.syncTicker.Stop()
	}

	sw.cancel()
	sw.wg.Wait()

	logging.Info("Sync worker stopped")
}

// workerLoop is the main loop for each worker goroutine
func (sw *SyncWorker) workerLoop(workerID int) {
	defer sw.wg.Done()

	logging.Debug("Sync worker started",
		zap.Int("worker_id", workerID))

	for {
		select {
		case <-sw.ctx.Done():
			logging.Debug("Sync worker stopping",
				zap.Int("worker_id", workerID))
			return

		case <-sw.syncTicker.C:
			sw.processMultipartFiles(workerID)
			sw.processDirtyFiles(workerID)
		}
	}
}

// processDirtyFiles processes all dirty files that need syncing
func (sw *SyncWorker) processDirtyFiles(workerID int) {
	dirtyFiles := sw.manager.GetDirtyFiles()

	if len(dirtyFiles) == 0 {
		return
	}

	logging.Debug("Processing dirty files",
		zap.Int("worker_id", workerID),
		zap.Int("count", len(dirtyFiles)))

	for _, metadata := range dirtyFiles {
		// Check if we should sync this file
		if !sw.shouldSync(metadata) {
			continue
		}

		// Sync the file
		if err := sw.syncFile(metadata.Path); err != nil {
			logging.Error("Failed to sync file",
				zap.Int("worker_id", workerID),
				zap.String("path", metadata.Path),
				zap.Error(err))

			// Update error count
			sw.manager.dirtyIndex.IncrementSyncAttempts(metadata.Path, err)
		} else {
			logging.Info("Successfully synced file",
				zap.Int("worker_id", workerID),
				zap.String("path", metadata.Path),
				zap.Int64("size", metadata.Size))
		}
	}
}

// shouldSync determines if a file should be synced based on triggers
func (sw *SyncWorker) shouldSync(metadata *DirtyFileMetadata) bool {
	now := time.Now()

	// Quick-Flush 80% Buffer Disk Quotas
	totalSize := sw.manager.GetTotalStagingSize()
	maxSize := sw.manager.config.MaxStagingSizeGB * 1024 * 1024 * 1024
	if maxSize > 0 && float64(totalSize) >= float64(maxSize)*0.8 {
		logging.Warn("Disk Quota at 80%, actively forcing flush", zap.String("path", metadata.Path))
		return true
	}

	// Check size threshold (convert MB to bytes)
	syncThreshold := sw.config.SyncThresholdMB * 1024 * 1024
	if metadata.Size >= syncThreshold {
		logging.Debug("File exceeds size threshold",
			zap.String("path", metadata.Path),
			zap.Int64("size", metadata.Size),
			zap.Int64("threshold", syncThreshold))
		return true
	}

	// Check age threshold
	maxDirtyAge, _ := sw.config.GetMaxDirtyAge()
	age := now.Sub(metadata.DirtySince)
	if age >= maxDirtyAge {
		logging.Debug("File exceeds age threshold",
			zap.String("path", metadata.Path),
			zap.Duration("age", age),
			zap.Duration("threshold", maxDirtyAge))
		return true
	}

	// Check if session is idle (no active handles)
	session, exists := sw.manager.GetSession(metadata.Path)
	if exists && session.GetRefCount() == 0 {
		// Session is idle, check idle time
		idleTime := now.Sub(session.LastWrite)
		if idleTime >= 5*time.Second { // Configurable idle threshold
			logging.Debug("File session is idle",
				zap.String("path", metadata.Path),
				zap.Duration("idle_time", idleTime))
			return true
		}
	}

	return false
}

// syncFile synchronizes a single file to COS
func (sw *SyncWorker) syncFile(path string) error {
	// Get the session
	session, exists := sw.manager.GetSession(path)
	if !exists {
		return fmt.Errorf("session not found for path: %s", path)
	}

	// Sync the session (flushes to staging file)
	if err := session.Sync(); err != nil {
		return fmt.Errorf("failed to sync session: %w", err)
	}

	// Read staging file using file stream to prevent OOM on large files
	file, err := os.Open(session.StagingPath)
	if err != nil {
		return fmt.Errorf("failed to open staging file: %w", err)
	}
	defer file.Close()

	if session.Multipart != nil && session.Multipart.Active && session.Multipart.IsSequential() {
		// Upload the final remaining bytes as the last part
		start, _ := session.Multipart.GetNextUploadRange()
		finalSize := session.Size - start
		
		if finalSize > 0 {
			section := io.NewSectionReader(file, start, finalSize)
			partNumber := int64(len(session.Multipart.CompletedParts)) + 1
			etag, err := sw.cosClient.UploadPart(sw.ctx, path, session.Multipart.UploadID, partNumber, section)
			if err != nil {
				return fmt.Errorf("failed to upload final part: %w", err)
			}
			session.Multipart.AddCompletedPart(partNumber, etag)
		}
		
		err := sw.cosClient.CompleteMultipartUpload(sw.ctx, path, session.Multipart.UploadID, session.Multipart.CompletedParts)
		if err != nil {
			return fmt.Errorf("failed to complete multipart upload: %w", err)
		}
		logging.Info("Successfully completed S3 multipart payload", zap.String("path", path))
	} else {
		// Upload to COS with retry (monolithic loop)
		if err := sw.uploadWithRetryStream(path, file); err != nil {
			return fmt.Errorf("failed to upload to COS: %w", err)
		}
	}

	// Mark as clean
	sw.manager.MarkClean(path)

	// Cleanup session if idle
	if session.GetRefCount() == 0 {
		if err := sw.manager.CleanupSession(path, true); err != nil {
			logging.Warn("Failed to cleanup session after sync",
				zap.String("path", path),
				zap.Error(err))
		}
	}

	return nil
}

// uploadWithRetry uploads a file to COS with exponential backoff retry
func (sw *SyncWorker) uploadWithRetry(path string, data []byte) error {
	var lastErr error

	retryBackoff, _ := sw.config.GetRetryBackoffInitial()
	maxRetryBackoff, _ := sw.config.GetRetryBackoffMax()

	for attempt := 0; attempt < sw.config.MaxSyncRetries; attempt++ {
		if attempt > 0 {
			// Calculate backoff delay
			backoff := retryBackoff * time.Duration(1<<uint(attempt-1))
			if backoff > maxRetryBackoff {
				backoff = maxRetryBackoff
			}

			logging.Debug("Retrying upload after backoff",
				zap.String("path", path),
				zap.Int("attempt", attempt+1),
				zap.Duration("backoff", backoff))

			select {
			case <-time.After(backoff):
			case <-sw.ctx.Done():
				return sw.ctx.Err()
			}
		}

		// Attempt upload
		err := sw.cosClient.PutObject(sw.ctx, path, data, nil)
		if err == nil {
			if attempt > 0 {
				logging.Info("Upload succeeded after retry",
					zap.String("path", path),
					zap.Int("attempts", attempt+1))
			}
			return nil
		}

		lastErr = err
		logging.Warn("Upload attempt failed",
			zap.String("path", path),
			zap.Int("attempt", attempt+1),
			zap.Error(err))
	}

	return fmt.Errorf("upload failed after %d attempts: %w", sw.config.MaxSyncRetries, lastErr)
}

// uploadWithRetryStream uploads a file object stream to COS with exponential backoff retry
func (sw *SyncWorker) uploadWithRetryStream(path string, body io.ReadSeeker) error {
	var lastErr error

	retryBackoff, _ := sw.config.GetRetryBackoffInitial()
	maxRetryBackoff, _ := sw.config.GetRetryBackoffMax()

	for attempt := 0; attempt < sw.config.MaxSyncRetries; attempt++ {
		if attempt > 0 {
			backoff := retryBackoff * time.Duration(1<<uint(attempt-1))
			if backoff > maxRetryBackoff {
				backoff = maxRetryBackoff
			}

			logging.Debug("Retrying upload stream after backoff",
				zap.String("path", path),
				zap.Int("attempt", attempt+1),
				zap.Duration("backoff", backoff))

			select {
			case <-time.After(backoff):
			case <-sw.ctx.Done():
				return sw.ctx.Err()
			}
		}

		// Ensure body is rewound before upload retry
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek upload body: %w", err)
		}

		err := sw.cosClient.PutObjectStream(sw.ctx, path, body, nil)
		if err == nil {
			if attempt > 0 {
				logging.Info("Upload stream succeeded after retry",
					zap.String("path", path),
					zap.Int("attempts", attempt+1))
			}
			return nil
		}

		lastErr = err
		logging.Warn("Upload stream attempt failed",
			zap.String("path", path),
			zap.Int("attempt", attempt+1),
			zap.Error(err))
	}

	return fmt.Errorf("upload stream failed after %d attempts: %w", sw.config.MaxSyncRetries, lastErr)
}

// TriggerSync manually triggers a sync for a specific file
func (sw *SyncWorker) TriggerSync(path string) error {
	if !sw.manager.IsDirty(path) {
		return nil // Nothing to sync
	}

	logging.Info("Manual sync triggered",
		zap.String("path", path))

	return sw.syncFile(path)
}

// Stats returns statistics about the sync worker
func (sw *SyncWorker) Stats() map[string]interface{} {
	dirtyFiles := sw.manager.GetDirtyFiles()

	totalSize := int64(0)
	oldestDirty := time.Time{}

	for _, metadata := range dirtyFiles {
		totalSize += metadata.Size
		if oldestDirty.IsZero() || metadata.DirtySince.Before(oldestDirty) {
			oldestDirty = metadata.DirtySince
		}
	}

	stats := map[string]interface{}{
		"dirty_files":  len(dirtyFiles),
		"total_size":   totalSize,
		"worker_count": sw.config.SyncWorkerCount,
	}

	if !oldestDirty.IsZero() {
		stats["oldest_dirty_age"] = time.Since(oldestDirty).String()
	}

	return stats
}

// processMultipartFiles processes progressive multi-part chunk streaming 
func (sw *SyncWorker) processMultipartFiles(workerID int) {
	for _, metadata := range sw.manager.GetDirtyFiles() {
		session, exists := sw.manager.GetSession(metadata.Path)
		if !exists || session.Multipart == nil {
			continue
		}
		
		// Attempt progressive chunk boundaries
		for {
			start, end := session.Multipart.GetNextUploadRange()
			
			// Limit uploads sequentially against full boundary
			if session.Size >= end && session.Multipart.IsSequential() {
				if session.Multipart.UploadID == "" {
					uploadID, err := sw.cosClient.CreateMultipartUpload(sw.ctx, metadata.Path, nil)
					if err != nil {
						logging.Error("Failed to initiate multipart", zap.Error(err))
						break
					}
					session.Multipart.UploadID = uploadID
					session.Multipart.Active = true
				}
				
				session.Sync() 
				file, err := os.Open(session.StagingPath)
				if err != nil {
					break
				}
				
				section := io.NewSectionReader(file, start, end-start)
				partNumber := int64(len(session.Multipart.CompletedParts)) + 1
				
				etag, err := sw.cosClient.UploadPart(sw.ctx, metadata.Path, session.Multipart.UploadID, partNumber, section)
				file.Close()
				
				if err == nil {
					session.Multipart.AddCompletedPart(partNumber, etag)
					logging.Info("Uploaded progressive part chunk", zap.Int64("part", partNumber), zap.String("path", metadata.Path))
				} else {
					logging.Error("Failed progressive upload", zap.Error(err))
					break
				}
			} else {
				break
			}
		}
	}
}

// Made with Bob

// UploadToCOS uploads data directly to COS (used for immediate sync before delete)
func (sw *SyncWorker) UploadToCOS(ctx context.Context, path string, data []byte, metadata map[string]string) error {
	return sw.uploadWithRetry(path, data)
}
