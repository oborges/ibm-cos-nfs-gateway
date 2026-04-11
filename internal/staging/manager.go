package staging

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/oborges/cos-nfs-gateway/internal/config"
	"github.com/oborges/cos-nfs-gateway/internal/logging"
	"go.uber.org/zap"
)

// StagingManager manages staging files and write sessions
type StagingManager struct {
	config      *config.StagingConfig
	stagingRoot string
	sessions    map[string]*WriteSession
	dirtyIndex  *DirtyFileIndex
	mu          sync.RWMutex
}

// NewStagingManager creates a new staging manager
func NewStagingManager(cfg *config.StagingConfig) (*StagingManager, error) {
	// Create staging directory structure
	activeDir := filepath.Join(cfg.RootDir, "active")
	if err := os.MkdirAll(activeDir, 0755); err != nil {
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

	return sm, nil
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
	session, err := NewWriteSession(path, stagingPath)
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

// MarkDirty marks a file as dirty (needs sync)
func (sm *StagingManager) MarkDirty(path string, size int64) {
	sm.dirtyIndex.MarkDirty(path, size)

	logging.Debug("Marked file as dirty",
		zap.String("path", path),
		zap.Int64("size", size),
		zap.Int("total_dirty", sm.dirtyIndex.Count()))
}

// MarkClean marks a file as clean (synced)
func (sm *StagingManager) MarkClean(path string) {
	sm.dirtyIndex.MarkClean(path)

	logging.Debug("Marked file as clean",
		zap.String("path", path),
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

// GetDirtyFiles returns a list of all dirty files
func (sm *StagingManager) GetDirtyFiles() []*DirtyFileMetadata {
	return sm.dirtyIndex.GetDirtyFiles()
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
		if entry.IsDir() {
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

		// Mark as dirty for re-sync
		// Note: We don't know the original path from the filename alone
		// This is a limitation of the MVP - we'll improve this with metadata files
		logging.Debug("Found staging file during recovery",
			zap.String("file", entry.Name()),
			zap.Int64("size", info.Size()))

		recovered++
	}

	if recovered > 0 {
		logging.Info("Recovered staging files",
			zap.Int("count", recovered))
	}

	return nil
}

// CleanupSession removes a session and optionally deletes the staging file
func (sm *StagingManager) CleanupSession(path string, deleteStagingFile bool) error {
	sm.mu.Lock()
	session, exists := sm.sessions[path]
	if exists {
		delete(sm.sessions, path)
	}
	sm.mu.Unlock()

	if !exists {
		return nil
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
		logging.Debug("Removed staging file",
			zap.String("path", path),
			zap.String("staging_path", session.StagingPath))
	}

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

	return map[string]interface{}{
		"total_sessions":  len(sm.sessions),
		"active_sessions": activeSessions,
		"dirty_files":     sm.dirtyIndex.Count(),
	}
}

// Made with Bob
