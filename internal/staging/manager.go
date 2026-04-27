package staging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/oborges/cos-nfs-gateway/internal/config"
	"github.com/oborges/cos-nfs-gateway/internal/logging"
	"github.com/oborges/cos-nfs-gateway/internal/metrics"
	"go.uber.org/zap"
)

// StagingManager manages staging files and write sessions
type StagingManager struct {
	config        *config.StagingConfig
	stagingRoot   string
	sessions      map[string]*WriteSession
	dirtyIndex    *DirtyFileIndex
	mu            sync.RWMutex
	pressureMu    sync.Mutex
	reservedBytes int64
}

const (
	PressureLevelNormal   = "normal"
	PressureLevelHigh     = "high"
	PressureLevelCritical = "critical"

	BackpressureModeBlock    = "block"
	BackpressureModeFailFast = "fail_fast"
)

// PressureState captures the current staging pressure calculation.
type PressureState struct {
	UsedBytes              int64
	AvailableBytes         int64
	QuotaBytes             int64
	HighWatermarkBytes     int64
	CriticalWatermarkBytes int64
	ProjectedBytes         int64
	Level                  string
}

// NewStagingManager creates a new staging manager
func NewStagingManager(cfg *config.StagingConfig) (*StagingManager, error) {
	normalizeBackpressureConfig(cfg)

	// Create staging directory structure
	activeDir := filepath.Join(cfg.RootDir, "active")
	if err := os.MkdirAll(activeDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}

	sm := &StagingManager{
		config:      cfg,
		stagingRoot: cfg.RootDir,
		sessions:    make(map[string]*WriteSession),
		dirtyIndex:  NewDirtyFileIndex(),
	}

	logging.Info("Staging manager initialized",
		zap.String("root_dir", cfg.RootDir),
		zap.Bool("enabled", cfg.Enabled))

	// Recover from disk if needed
	if err := sm.RecoverFromDisk(); err != nil {
		logging.Warn("Failed to recover from disk, continuing anyway",
			zap.Error(err))
	}
	sm.updatePressureMetrics()

	return sm, nil
}

func normalizeBackpressureConfig(cfg *config.StagingConfig) {
	if cfg.BackpressureMode == "" {
		cfg.BackpressureMode = BackpressureModeBlock
	}
	if cfg.BackpressureHighWatermarkPct == 0 {
		cfg.BackpressureHighWatermarkPct = 80
	}
	if cfg.BackpressureCritWatermarkPct == 0 {
		cfg.BackpressureCritWatermarkPct = 95
	}
	if cfg.BackpressureWaitTimeout == "" {
		cfg.BackpressureWaitTimeout = "30s"
	}
	if cfg.BackpressureCheckInterval == "" {
		cfg.BackpressureCheckInterval = "250ms"
	}
}

// GetOrCreateSession gets an existing session or creates a new one
func (sm *StagingManager) GetOrCreateSession(path string) (*WriteSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if session exists
	if session, exists := sm.sessions[path]; exists {
		session.IncrementRefCount()
		logging.Debug("Reusing existing write session",
			zap.String("path", path),
			zap.Int32("ref_count", session.GetRefCount()))
		return session, nil
	}

	// Create new session
	stagingPath := sm.stagingFilePath(path)
	session, err := NewWriteSession(sm, path, stagingPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	sm.sessions[path] = session

	logging.Info("Created new write session",
		zap.String("path", path),
		zap.String("staging_path", stagingPath))

	return session, nil
}

// ReleaseSession decrements the reference count for a session
func (sm *StagingManager) ReleaseSession(path string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, exists := sm.sessions[path]
	if !exists {
		return
	}

	session.DecrementRefCount()

	logging.Debug("Released write session",
		zap.String("path", path),
		zap.Int32("ref_count", session.GetRefCount()))

	// Keep session alive even if RefCount == 0 for potential reopen
	// Cleanup happens after sync + idle timeout
}

// GetSessionsInDirectory organically returns all actively buffering files existing precisely inside specific logical directories!
func (sm *StagingManager) GetSessionsInDirectory(dirPath string) []*WriteSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var sessions []*WriteSession
	prefix := dirPath
	if prefix != "/" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	for path, session := range sm.sessions {
		if strings.HasPrefix(path, prefix) && len(path) > len(prefix) {
			// Extracting only precisely adjacent file instances, ignoring deep nested arrays seamlessly!
			remainder := strings.TrimPrefix(path, prefix)
			if !strings.Contains(remainder, "/") {
				sessions = append(sessions, session)
			}
		}
	}
	return sessions
}

// MarkDirty marks a file as dirty (needs sync)
func (sm *StagingManager) MarkDirty(path string, size int64) {
	sm.dirtyIndex.MarkDirty(path, size)
	sm.updateSyncQueueMetrics()
	sm.updatePressureMetrics()

	logging.Debug("Marked file as dirty",
		zap.String("path", path),
		zap.Int64("size", size),
		zap.Int("total_dirty", sm.dirtyIndex.Count()))
}

// MarkClean marks a file as clean (synced)
func (sm *StagingManager) MarkClean(path string) {
	sm.dirtyIndex.MarkClean(path)
	sm.updateSyncQueueMetrics()
	sm.updatePressureMetrics()

	logging.Debug("Marked file as clean",
		zap.String("path", path),
		zap.Int("total_dirty", sm.dirtyIndex.Count()))
}

// ForgetDirty removes stale dirty bookkeeping when staged data is intentionally gone.
func (sm *StagingManager) ForgetDirty(path, reason string) {
	sm.dirtyIndex.MarkClean(path)
	sm.updateSyncQueueMetrics()
	sm.updatePressureMetrics()

	logging.Info("Forgot dirty staging entry",
		zap.String("path", path),
		zap.String("reason", reason),
		zap.Int("total_dirty", sm.dirtyIndex.Count()))
}

// IsDirty returns true if the file is dirty
func (sm *StagingManager) IsDirty(path string) bool {
	return sm.dirtyIndex.IsDirty(path)
}

// GetSession returns an existing session (without creating)
func (sm *StagingManager) GetSession(path string) (*WriteSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, exists := sm.sessions[path]
	return session, exists
}

// RecoverSessionFromStaging rebuilds an idle write session around an existing staging file.
func (sm *StagingManager) RecoverSessionFromStaging(path string) (*WriteSession, error) {
	stagingPath := sm.stagingFilePath(path)
	info, err := os.Stat(stagingPath)
	if err != nil {
		return nil, err
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, exists := sm.sessions[path]; exists {
		return session, nil
	}

	session, err := NewWriteSession(sm, path, stagingPath)
	if err != nil {
		return nil, err
	}

	session.Size = info.Size()
	session.Dirty = true
	session.RefCount = 0
	session.LastWrite = info.ModTime()
	session.LastAccess = info.ModTime()
	sm.sessions[path] = session

	logging.Info("Recovered missing staging session",
		zap.String("path", path),
		zap.String("staging_path", stagingPath),
		zap.Int64("size", info.Size()))

	return session, nil
}

// GetTotalStagingSize calculates the total byte quota utilized by active tracing sessions
func (sm *StagingManager) GetTotalStagingSize() int64 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var total int64
	for _, session := range sm.sessions {
		total += session.GetSize()
	}
	return total
}

// GetDirtyFiles returns a list of all dirty files
func (sm *StagingManager) GetDirtyFiles() []*DirtyFileMetadata {
	return sm.dirtyIndex.GetDirtyFiles()
}

// SyncQueueStats returns the current staging sync backlog.
func (sm *StagingManager) SyncQueueStats() (depth int, totalBytes int64, oldestAge time.Duration) {
	dirtyFiles := sm.GetDirtyFiles()
	now := time.Now()

	for _, metadata := range dirtyFiles {
		depth++
		totalBytes += metadata.Size
		if metadata.DirtySince.IsZero() {
			continue
		}
		age := now.Sub(metadata.DirtySince)
		if age > oldestAge {
			oldestAge = age
		}
	}

	return depth, totalBytes, oldestAge
}

func (sm *StagingManager) updateSyncQueueMetrics() {
	depth, totalBytes, oldestAge := sm.SyncQueueStats()
	metrics.SetStagingSyncQueue(depth, totalBytes, oldestAge)
}

// ReserveWrite applies staging backpressure and reserves new bytes before a write.
func (sm *StagingManager) ReserveWrite(path string, requestedBytes, growthBytes int64) (func(), error) {
	if requestedBytes < 0 {
		requestedBytes = 0
	}
	if growthBytes < 0 {
		growthBytes = 0
	}

	releaseNoop := func() {}
	if sm == nil || sm.config == nil || sm.config.MaxStagingSizeGB <= 0 {
		return releaseNoop, nil
	}

	timeout, err := sm.config.GetBackpressureWaitTimeout()
	if err != nil {
		timeout = 0
	}
	checkInterval, err := sm.config.GetBackpressureCheckInterval()
	if err != nil || checkInterval <= 0 {
		checkInterval = 250 * time.Millisecond
	}

	start := time.Now()
	blocked := false
	deadline := start.Add(timeout)

	for {
		sm.pressureMu.Lock()
		state := sm.pressureStateLocked(growthBytes)
		mode := strings.ToLower(sm.config.BackpressureMode)
		if mode == "" {
			mode = BackpressureModeBlock
		}

		switch {
		case !sm.config.BackpressureEnabled:
			if state.ProjectedBytes > state.QuotaBytes {
				sm.logBackpressureDecision(path, requestedBytes, state, "reject")
				metrics.RecordBackpressureRejected()
				sm.pressureMu.Unlock()
				return releaseNoop, syscall.ENOSPC
			}
			sm.reservedBytes += growthBytes
			sm.logBackpressureDecision(path, requestedBytes, state, "allow")
			sm.updatePressureMetricsLocked()
			sm.pressureMu.Unlock()
			return sm.releaseReservation(growthBytes), nil

		case state.Level == PressureLevelCritical || state.ProjectedBytes > state.QuotaBytes:
			sm.logBackpressureDecision(path, requestedBytes, state, "reject")
			metrics.RecordBackpressureRejected()
			sm.pressureMu.Unlock()
			return releaseNoop, syscall.ENOSPC

		case state.Level == PressureLevelHigh && mode == BackpressureModeBlock:
			if timeout <= 0 || time.Now().After(deadline) {
				if blocked {
					metrics.RecordBackpressureWait(time.Since(start))
				}
				sm.logBackpressureDecision(path, requestedBytes, state, "reject")
				metrics.RecordBackpressureRejected()
				sm.pressureMu.Unlock()
				return releaseNoop, syscall.ENOSPC
			}
			sm.logBackpressureDecision(path, requestedBytes, state, "block")
			if !blocked {
				metrics.RecordBackpressureBlocked()
				blocked = true
			}
			wait := checkInterval
			if remaining := time.Until(deadline); remaining < wait {
				wait = remaining
			}
			sm.pressureMu.Unlock()
			time.Sleep(wait)

		default:
			if blocked {
				metrics.RecordBackpressureWait(time.Since(start))
			}
			sm.reservedBytes += growthBytes
			sm.logBackpressureDecision(path, requestedBytes, state, "allow")
			sm.updatePressureMetricsLocked()
			sm.pressureMu.Unlock()
			return sm.releaseReservation(growthBytes), nil
		}
	}
}

func (sm *StagingManager) releaseReservation(bytes int64) func() {
	return func() {
		if bytes <= 0 {
			return
		}
		sm.pressureMu.Lock()
		if bytes > sm.reservedBytes {
			sm.reservedBytes = 0
		} else {
			sm.reservedBytes -= bytes
		}
		sm.updatePressureMetricsLocked()
		sm.pressureMu.Unlock()
	}
}

// CurrentPressure returns the current staging pressure without adding a new write.
func (sm *StagingManager) CurrentPressure() PressureState {
	sm.pressureMu.Lock()
	defer sm.pressureMu.Unlock()
	return sm.pressureStateLocked(0)
}

func (sm *StagingManager) pressureStateLocked(growthBytes int64) PressureState {
	quotaBytes := sm.config.MaxStagingSizeGB * 1024 * 1024 * 1024
	usedBytes := sm.GetTotalStagingSize() + sm.reservedBytes
	projectedBytes := usedBytes + growthBytes
	quotaAvailable := quotaBytes - usedBytes
	if quotaAvailable < 0 {
		quotaAvailable = 0
	}
	availableBytes := quotaAvailable
	if diskAvailable, ok := sm.stagingDiskAvailableBytes(); ok && diskAvailable < availableBytes {
		availableBytes = diskAvailable
	}

	level := PressureLevelNormal
	highBytes := quotaBytes
	criticalBytes := quotaBytes
	if quotaBytes > 0 {
		highBytes = quotaBytes * int64(sm.config.BackpressureHighWatermarkPct) / 100
		criticalBytes = quotaBytes * int64(sm.config.BackpressureCritWatermarkPct) / 100
		if projectedBytes >= criticalBytes || growthBytes > availableBytes {
			level = PressureLevelCritical
		} else if projectedBytes >= highBytes {
			level = PressureLevelHigh
		}
	}

	return PressureState{
		UsedBytes:              usedBytes,
		AvailableBytes:         availableBytes,
		QuotaBytes:             quotaBytes,
		HighWatermarkBytes:     highBytes,
		CriticalWatermarkBytes: criticalBytes,
		ProjectedBytes:         projectedBytes,
		Level:                  level,
	}
}

func (sm *StagingManager) stagingDiskAvailableBytes() (int64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(sm.stagingRoot, &stat); err != nil {
		return 0, false
	}
	return int64(stat.Bavail) * int64(stat.Bsize), true
}

func (sm *StagingManager) updatePressureMetrics() {
	sm.pressureMu.Lock()
	defer sm.pressureMu.Unlock()
	sm.updatePressureMetricsLocked()
}

func (sm *StagingManager) updatePressureMetricsLocked() {
	state := sm.pressureStateLocked(0)
	metrics.SetStagingPressure(state.UsedBytes, state.AvailableBytes, state.Level)
}

func (sm *StagingManager) logBackpressureDecision(path string, requestedBytes int64, state PressureState, decision string) {
	logging.Info("Staging backpressure decision",
		zap.String("path", path),
		zap.Int64("requested_bytes", requestedBytes),
		zap.Int64("available_bytes", state.AvailableBytes),
		zap.Int64("used_bytes", state.UsedBytes),
		zap.Int64("projected_bytes", state.ProjectedBytes),
		zap.String("pressure_level", state.Level),
		zap.String("decision", decision))
}

// stagingFilePath generates the staging file path for a logical path
func (sm *StagingManager) stagingFilePath(path string) string {
	// Use SHA256 hash of path as filename
	hash := sha256.Sum256([]byte(path))
	filename := hex.EncodeToString(hash[:16]) + ".data"
	return filepath.Join(sm.stagingRoot, "active", filename)
}

// RecoverFromDisk scans the staging directory and rebuilds state
func (sm *StagingManager) RecoverFromDisk() error {
	activeDir := filepath.Join(sm.stagingRoot, "active")

	// Check if directory exists
	if _, err := os.Stat(activeDir); os.IsNotExist(err) {
		return nil // Nothing to recover
	}

	// Scan directory
	entries, err := os.ReadDir(activeDir)
	if err != nil {
		return fmt.Errorf("failed to read staging directory: %w", err)
	}

	recovered := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".data") {
			continue
		}

		// Get file info
		filePath := filepath.Join(activeDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			logging.Warn("Failed to stat staging file",
				zap.String("file", filePath),
				zap.Error(err))
			continue
		}

		// Resolve original path metadata gracefully
		metadataPath := filePath + ".metadata"
		metadataPayload := make(map[string]interface{})
		if metadataBytes, err := os.ReadFile(metadataPath); err == nil {
			json.Unmarshal(metadataBytes, &metadataPayload)
		}

		rawPath, ok := metadataPayload["original_path"]
		originalPath, isString := rawPath.(string)
		if !ok || !isString || originalPath == "" {
			logging.Warn("Failed to extract valid metadata maps for orphaned file. Leaving stranded.",
				zap.String("file", entry.Name()))
			continue
		}

		// Mark as dirty for re-sync safely preserving original maps!
		session, err := sm.GetOrCreateSession(originalPath)
		if err != nil {
			logging.Warn("Failed to reconstruct Write Session for orphaned file", zap.Error(err))
			continue
		}

		// Force size resolution based on orphaned hash stats
		session.Size = info.Size()
		session.Dirty = true
		session.LastWrite = info.ModTime()
		session.LastAccess = info.ModTime()

		// Flag the index triggering immediate background upload evaluation!
		sm.MarkDirty(originalPath, info.Size())

		logging.Debug("Orphaned staging file recovered natively",
			zap.String("file", entry.Name()),
			zap.String("original_path", originalPath),
			zap.Int64("size", info.Size()))

		recovered++
	}

	if recovered > 0 {
		logging.Info("Recovered staging files automatically after daemon crash",
			zap.Int("count", recovered))
	}

	return nil
}

// CleanupSession removes a session and optionally deletes the staging file
func (sm *StagingManager) CleanupSession(path string, deleteStagingFile bool) error {
	if deleteStagingFile && sm.dirtyIndex.IsDirty(path) && sm.dirtyIndex.IsSyncing(path) {
		logging.Warn("Refusing to cleanup staging session during active sync",
			zap.String("path", path),
			zap.String("event", "cleanup_skip"),
			zap.String("reason", "active_sync"))
		return fmt.Errorf("cannot cleanup active syncing session: %s", path)
	}

	sm.mu.Lock()
	session, exists := sm.sessions[path]
	if exists {
		delete(sm.sessions, path)
	}
	sm.mu.Unlock()

	if !exists {
		return nil
	}

	if session.Multipart != nil && session.Multipart.Active {
		logging.Warn("Cleaning session with active multipart state without aborting upload",
			zap.String("path", path),
			zap.String("upload_id", session.Multipart.UploadID),
			zap.String("multipart_state", "active"),
			zap.String("event", "cleanup"),
			zap.String("reason", "session_cleanup"))
	}

	// Close the session
	if err := session.Close(); err != nil {
		logging.Warn("Failed to close session during cleanup",
			zap.String("path", path),
			zap.Error(err))
	}

	// Delete staging file if requested
	if deleteStagingFile {
		if err := os.Remove(session.StagingPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove staging file: %w", err)
		}
		// Safely clear `.metadata` journals maintaining boundaries safely tracking S3 maps
		os.Remove(session.StagingPath + ".metadata")
		sm.ForgetDirty(path, "staging_file_deleted")

		logging.Debug("Removed staging file",
			zap.String("path", path),
			zap.String("staging_path", session.StagingPath))
	}

	sm.updatePressureMetrics()

	return nil
}

// Shutdown closes all sessions and cleans up
func (sm *StagingManager) Shutdown() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	logging.Info("Shutting down staging manager",
		zap.Int("active_sessions", len(sm.sessions)))

	// Close all sessions
	for path, session := range sm.sessions {
		if err := session.Close(); err != nil {
			logging.Warn("Failed to close session during shutdown",
				zap.String("path", path),
				zap.Error(err))
		}
	}

	sm.sessions = make(map[string]*WriteSession)

	return nil
}

// Stats returns statistics about the staging manager
func (sm *StagingManager) Stats() map[string]interface{} {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	activeSessions := 0
	for _, session := range sm.sessions {
		if session.GetRefCount() > 0 {
			activeSessions++
		}
	}

	pressure := sm.CurrentPressure()

	return map[string]interface{}{
		"total_sessions":          len(sm.sessions),
		"active_sessions":         activeSessions,
		"dirty_files":             sm.dirtyIndex.Count(),
		"syncing_files":           sm.dirtyIndex.SyncingCount(),
		"staging_used_bytes":      pressure.UsedBytes,
		"staging_available_bytes": pressure.AvailableBytes,
		"staging_pressure_level":  pressure.Level,
	}
}

// Made with Bob
