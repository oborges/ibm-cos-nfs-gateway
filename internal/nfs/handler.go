package nfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/oborges/cos-nfs-gateway/internal/buffer"
	"github.com/oborges/cos-nfs-gateway/internal/config"
	"github.com/oborges/cos-nfs-gateway/internal/feature"
	"github.com/oborges/cos-nfs-gateway/internal/posix"
	"github.com/oborges/cos-nfs-gateway/internal/staging"
	"github.com/oborges/cos-nfs-gateway/pkg/types"
	nfs "github.com/willscott/go-nfs"
	"go.uber.org/zap"
)

// Logger wraps zap.Logger for NFS operations
type Logger struct {
	zap *zap.Logger
}

// NewLogger creates a new logger wrapper
func NewLogger(zapLogger *zap.Logger) *Logger {
	return &Logger{zap: zapLogger}
}

// Info logs an info message
func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	fields := make([]zap.Field, 0, len(keysAndValues)/2)
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			key := fmt.Sprint(keysAndValues[i])
			value := keysAndValues[i+1]
			fields = append(fields, zap.Any(key, value))
		}
	}
	l.zap.Info(msg, fields...)
}

// Error logs an error message
func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	fields := make([]zap.Field, 0, len(keysAndValues)/2)
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			key := fmt.Sprint(keysAndValues[i])
			value := keysAndValues[i+1]
			fields = append(fields, zap.Any(key, value))
		}
	}
	l.zap.Error(msg, fields...)
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	fields := make([]zap.Field, 0, len(keysAndValues)/2)
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			key := fmt.Sprint(keysAndValues[i])
			value := keysAndValues[i+1]
			fields = append(fields, zap.Any(key, value))
		}
	}
	l.zap.Debug(msg, fields...)
}

// COSHandler implements nfs.Handler interface for IBM Cloud COS
type COSHandler struct {
	ops        *posix.OperationsHandler
	logger     *Logger
	handleMap  map[string]*handleEntry
	handleLock sync.RWMutex
	maxHandles int
}

type handleEntry struct {
	path []string
	hash string
}

// NewCOSHandler creates a new NFS handler for COS
func NewCOSHandler(ops *posix.OperationsHandler, logger *Logger) *COSHandler {
	return &COSHandler{
		ops:        ops,
		logger:     logger,
		handleMap:  make(map[string]*handleEntry),
		maxHandles: 10000,
	}
}

// Mount handles NFS mount requests
func (h *COSHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	h.logger.Info("NFS mount request",
		"path", string(req.Dirpath),
		"remote", conn.RemoteAddr().String())

	// Create a billy filesystem wrapper
	fs := &COSFilesystem{
		ops:    h.ops,
		logger: h.logger,
		root:   string(req.Dirpath),
	}

	// Return success with null auth
	return nfs.MountStatusOk, fs, []nfs.AuthFlavor{nfs.AuthFlavorNull}
}

// Change returns a billy.Change interface for write operations
func (h *COSHandler) Change(fs billy.Filesystem) billy.Change {
	// COS filesystem doesn't support chmod operations
	return nil
}

// FSStat fills in filesystem statistics
func (h *COSHandler) FSStat(ctx context.Context, fs billy.Filesystem, stat *nfs.FSStat) error {
	// Set reasonable defaults for COS
	stat.TotalSize = 1 << 50      // 1 PB
	stat.FreeSize = 1 << 50       // 1 PB
	stat.AvailableSize = 1 << 50  // 1 PB
	stat.TotalFiles = 1 << 32     // 4 billion
	stat.FreeFiles = 1 << 32      // 4 billion
	stat.AvailableFiles = 1 << 32 // 4 billion
	stat.CacheHint = 0

	return nil
}

// ToHandle converts a filesystem path to an opaque file handle
func (h *COSHandler) ToHandle(fs billy.Filesystem, path []string) []byte {
	h.handleLock.Lock()
	defer h.handleLock.Unlock()

	// Create a unique hash for this path
	pathStr := strings.Join(path, "/")
	hash := sha256.Sum256([]byte(pathStr))
	hashStr := hex.EncodeToString(hash[:])

	// Store the mapping
	h.handleMap[hashStr] = &handleEntry{
		path: path,
		hash: hashStr,
	}

	// Return the hash as the handle
	return []byte(hashStr)
}

// FromHandle converts an opaque file handle back to a filesystem and path
func (h *COSHandler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	h.handleLock.RLock()
	defer h.handleLock.RUnlock()

	hashStr := string(fh)
	entry, ok := h.handleMap[hashStr]
	if !ok {
		return nil, nil, fmt.Errorf("invalid file handle")
	}

	// Return a new filesystem instance with the stored path
	fs := &COSFilesystem{
		ops:    h.ops,
		logger: h.logger,
		root:   "/",
	}

	return fs, entry.path, nil
}

// InvalidateHandle removes a file handle from the cache
func (h *COSHandler) InvalidateHandle(fs billy.Filesystem, fh []byte) error {
	h.handleLock.Lock()
	defer h.handleLock.Unlock()

	hashStr := string(fh)
	delete(h.handleMap, hashStr)
	return nil
}

// HandleLimit returns the maximum number of handles that can be cached
func (h *COSHandler) HandleLimit() int {
	return h.maxHandles
}

// COSFilesystem implements billy.Filesystem interface for COS
type COSFilesystem struct {
	ops            *posix.OperationsHandler
	logger         *Logger
	root           string
	perfConfig     *config.PerformanceConfig
	sessionManager *buffer.SessionManager
	// Staging architecture components
	stagingManager *staging.StagingManager
	syncWorker     *staging.SyncWorker
	featureFlags   *feature.FeatureFlags
}

// NewCOSFilesystem creates a new COS filesystem (deprecated, use NewCOSFilesystemWithConfig)
func NewCOSFilesystem(ops *posix.OperationsHandler, logger *Logger, root string) *COSFilesystem {
	// Use default config if not provided
	defaultConfig := &config.PerformanceConfig{
		WriteBufferKB:        4096,
		MultipartThresholdMB: 100,
		MultipartChunkMB:     10,
		ReadAheadKB:          1024,
	}
	return &COSFilesystem{
		ops:        ops,
		logger:     logger,
		root:       root,
		perfConfig: defaultConfig,
	}
}

// NewCOSFilesystemWithConfig creates a new COS filesystem with configuration
func NewCOSFilesystemWithConfig(ops *posix.OperationsHandler, logger *Logger, root string, perfConfig *config.PerformanceConfig, stagingManager *staging.StagingManager, syncWorker *staging.SyncWorker, featureFlags *feature.FeatureFlags) *COSFilesystem {
	bufferSize := int64(perfConfig.WriteBufferKB) * 1024
	sessionTimeout := 5 * time.Minute // Keep sessions alive for 5 minutes after last access
	
	logger.Info("Initializing COS filesystem with configuration",
		"write_buffer_kb", perfConfig.WriteBufferKB,
		"write_buffer_bytes", bufferSize,
		"write_buffer_mb", float64(bufferSize)/(1024*1024),
		"multipart_threshold_mb", perfConfig.MultipartThresholdMB,
		"multipart_chunk_mb", perfConfig.MultipartChunkMB,
		"read_ahead_kb", perfConfig.ReadAheadKB,
		"session_timeout", sessionTimeout,
		"staging_enabled", featureFlags.IsStagingEnabled())
	
	// Create session manager for path-scoped write buffering (legacy path)
	sessionManager := buffer.NewSessionManager(bufferSize, sessionTimeout)
	
	return &COSFilesystem{
		ops:            ops,
		logger:         logger,
		root:           root,
		perfConfig:     perfConfig,
		sessionManager: sessionManager,
		stagingManager: stagingManager,
		syncWorker:     syncWorker,
		featureFlags:   featureFlags,
	}
}

// Create creates a new file
func (fs *COSFilesystem) Create(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

// Open opens a file for reading
func (fs *COSFilesystem) Open(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDONLY, 0)
}

// OpenFile opens a file with specified flags and permissions
func (fs *COSFilesystem) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	fullPath := fs.Join(fs.root, filename)

	// Generate unique file handle ID for tracking
	fileID := fmt.Sprintf("%p", &fullPath)
	
	// Log file open with detailed flags
	flagStr := ""
	if flag&os.O_RDONLY != 0 {
		flagStr += "RDONLY|"
	}
	if flag&os.O_WRONLY != 0 {
		flagStr += "WRONLY|"
	}
	if flag&os.O_RDWR != 0 {
		flagStr += "RDWR|"
	}
	if flag&os.O_CREATE != 0 {
		flagStr += "CREATE|"
	}
	if flag&os.O_TRUNC != 0 {
		flagStr += "TRUNC|"
	}
	if flag&os.O_APPEND != 0 {
		flagStr += "APPEND|"
	}
	
	useStagingPath := fs.featureFlags != nil && fs.featureFlags.IsStagingEnabled()
	
	fs.logger.Info("FILE OPEN",
		"file_id", fileID,
		"path", fullPath,
		"flags", flagStr,
		"perm", fmt.Sprintf("%o", perm),
		"staging_enabled", useStagingPath)

	file := &COSFile{
		ops:            fs.ops,
		logger:         fs.logger,
		path:           fullPath,
		flag:           flag,
		perm:           perm,
		offset:         0,
		perfConfig:     fs.perfConfig,
		fileID:         fileID,
		sessionManager: fs.sessionManager,
		stagingManager: fs.stagingManager,
		syncWorker:     fs.syncWorker,
		featureFlags:   fs.featureFlags,
	}

	// Check if file exists
	_, err := fs.ops.Stat(context.Background(), fullPath)
	fileExists := err == nil

	// If using staging path, get or create staging session
	// For writable files: create if needed
	// For read-only files: get existing session if file is being staged
	if useStagingPath {
		if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 {
			// Writable file: get or create session
			// Note: GetOrCreateSession already increments ref count
			session, err := fs.stagingManager.GetOrCreateSession(fullPath)
			if err != nil {
				fs.logger.Error("Failed to get staging session",
					"file_id", fileID,
					"path", fullPath,
					"error", err)
				return nil, err
			}
			file.stagingSession = session
			
			fs.logger.Info("Staging session acquired for write",
				"file_id", fileID,
				"path", fullPath,
				"ref_count", session.GetRefCount())
		} else {
			// Read-only file: check if there's an existing staging session
			session, exists := fs.stagingManager.GetSession(fullPath)
			if exists {
				file.stagingSession = session
				session.IncrementRefCount()
				
				fs.logger.Info("Staging session acquired for read",
					"file_id", fileID,
					"path", fullPath,
					"ref_count", session.GetRefCount())
			}
		}
	}

	// If creating a new file
	if flag&os.O_CREATE != 0 && !fileExists {
		// File will be created on first write
		file.isNew = true
	}

	// If truncating, clear the file
	if flag&os.O_TRUNC != 0 {
		if useStagingPath && file.stagingSession != nil {
			// Truncate staging session to size 0
			if err := file.stagingSession.Truncate(0); err != nil {
				fs.logger.Error("Failed to truncate staging file",
					"file_id", fileID,
					"path", fullPath,
					"error", err)
				return nil, fmt.Errorf("failed to truncate staging file: %w", err)
			}
			fs.stagingManager.MarkDirty(fullPath, 0)
			
			fs.logger.Info("Truncated staging file",
				"file_id", fileID,
				"path", fullPath,
				"size", 0)
		} else {
			// Legacy path: truncate directly to COS
			attrs := &types.POSIXAttributes{
				Mode:  perm,
				UID:   1000,
				GID:   1000,
				Mtime: time.Now(),
			}
			err := fs.ops.WriteFile(context.Background(), fullPath, []byte{}, attrs)
			if err != nil {
				return nil, err
			}
			file.isNew = false
			file.loaded = true
			file.data = []byte{}
		}
	}

	// If appending to existing file, load it and set offset to end
	if flag&os.O_APPEND != 0 && fileExists && flag&os.O_TRUNC == 0 {
		if useStagingPath && file.stagingSession != nil {
			// Get size from staging session
			file.offset = file.stagingSession.Size
		} else {
			// Legacy path: load from COS
			data, err := fs.ops.ReadFile(context.Background(), fullPath, 0, 0)
			if err != nil {
				return nil, err
			}
			file.data = data
			file.loaded = true
			file.offset = int64(len(data))
		}
	}

	return file, nil
}

// Stat returns file information
func (fs *COSFilesystem) Stat(filename string) (os.FileInfo, error) {
	fullPath := fs.Join(fs.root, filename)
	return fs.ops.Stat(context.Background(), fullPath)
}

// Rename renames a file
func (fs *COSFilesystem) Rename(oldpath, newpath string) error {
	oldFull := fs.Join(fs.root, oldpath)
	newFull := fs.Join(fs.root, newpath)
	return fs.ops.RenameFile(context.Background(), oldFull, newFull)
}

// Remove removes a file or directory
func (fs *COSFilesystem) Remove(filename string) error {
	fullPath := fs.Join(fs.root, filename)

	// Check if it's a directory
	info, err := fs.ops.Stat(context.Background(), fullPath)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return fs.ops.DeleteDirectory(context.Background(), fullPath)
	}

	// Before deleting file, cleanup any active sessions to prevent data loss
	// This ensures any buffered writes are flushed to COS before deletion
	if err := fs.cleanupSessionsBeforeDelete(fullPath); err != nil {
		fs.logger.Error("Failed to cleanup sessions before delete",
			zap.String("path", fullPath),
			zap.Error(err))
		// Continue with deletion anyway - session cleanup is best-effort
	}

	return fs.ops.DeleteFile(context.Background(), fullPath)
}
// cleanupSessionsBeforeDelete ensures any active sessions are flushed before file deletion
func (fs *COSFilesystem) cleanupSessionsBeforeDelete(path string) error {
	ctx := context.Background()

	// Handle staging path (new architecture)
	if fs.featureFlags != nil && fs.featureFlags.IsStagingEnabled() && fs.stagingManager != nil {
		session, exists := fs.stagingManager.GetSession(path)
		if exists && session.Dirty {
			fs.logger.Info("Flushing staging session before delete",
				zap.String("path", path),
				zap.Int64("size", session.Size))

			// Sync session to staging file
			if err := session.Sync(); err != nil {
				return fmt.Errorf("failed to sync staging session: %w", err)
			}

			// If sync worker is available, trigger immediate upload
			if fs.syncWorker != nil {
				// Read staging file
				data, err := os.ReadFile(session.StagingPath)
				if err != nil {
					return fmt.Errorf("failed to read staging file: %w", err)
				}

				// Upload directly to COS
				metadata := make(map[string]string)
				if err := fs.syncWorker.UploadToCOS(ctx, path, data, metadata); err != nil {
					return fmt.Errorf("failed to upload to COS: %w", err)
				}

				fs.logger.Info("Uploaded staging file to COS before delete",
					zap.String("path", path),
					zap.Int("size", len(data)))
			}

			// Cleanup session and staging file
			if err := fs.stagingManager.CleanupSession(path, true); err != nil {
				fs.logger.Error("Failed to cleanup staging session",
					zap.String("path", path),
					zap.Error(err))
			}
		}
		return nil
	}

	// Handle legacy buffer path
	if fs.sessionManager != nil {
		session, exists := fs.sessionManager.GetSession(path)
		if exists {
			session.Mu.Lock()
			bufferSize := session.Buffer.Size()
			session.Mu.Unlock()

			if bufferSize > 0 {
				fs.logger.Info("Flushing legacy buffer session before delete",
					zap.String("path", path),
					zap.Int64("buffer_size", bufferSize))

				// Get flush data from buffer
				session.Mu.Lock()
				data, _, err := session.Buffer.GetFlushData()
				session.Mu.Unlock()

				if err != nil {
					return fmt.Errorf("failed to get flush data: %w", err)
				}

				if len(data) > 0 {
					// Get current file attributes
					attrs := &types.POSIXAttributes{
						Mode:  0644,
						UID:   1000,
						GID:   1000,
						Mtime: time.Now(),
					}

					// Upload to COS
					if err := fs.ops.WriteFile(ctx, path, data, attrs); err != nil {
						return fmt.Errorf("failed to flush buffer to COS: %w", err)
					}

					fs.logger.Info("Flushed legacy buffer to COS before delete",
						zap.String("path", path),
						zap.Int("size", len(data)))
				}
			}

			// Close the session
			fs.sessionManager.CloseSession(path)
		}
	}

	return nil
}


// Join joins path elements
func (fs *COSFilesystem) Join(elem ...string) string {
	return filepath.Join(elem...)
}

// TempFile creates a temporary file
func (fs *COSFilesystem) TempFile(dir, prefix string) (billy.File, error) {
	// Generate a unique temporary filename
	tempName := fmt.Sprintf("%s%d", prefix, os.Getpid())
	fullPath := fs.Join(dir, tempName)
	return fs.Create(fullPath)
}

// ReadDir reads directory contents
func (fs *COSFilesystem) ReadDir(path string) ([]os.FileInfo, error) {
	fullPath := fs.Join(fs.root, path)
	entries, err := fs.ops.ListDirectory(context.Background(), fullPath)
	if err != nil {
		return nil, err
	}

	// Convert []*posix.FileInfo to []os.FileInfo
	result := make([]os.FileInfo, len(entries))
	for i, entry := range entries {
		result[i] = entry
	}
	return result, nil
}

// MkdirAll creates a directory and all parent directories
func (fs *COSFilesystem) MkdirAll(filename string, perm os.FileMode) error {
	fullPath := fs.Join(fs.root, filename)
	attrs := &types.POSIXAttributes{
		Mode:  perm | os.ModeDir,
		UID:   1000,
		GID:   1000,
		Mtime: time.Now(),
	}
	return fs.ops.CreateDirectory(context.Background(), fullPath, attrs)
}

// Lstat returns file information (same as Stat for COS)
func (fs *COSFilesystem) Lstat(filename string) (os.FileInfo, error) {
	return fs.Stat(filename)
}

// Symlink creates a symbolic link (not supported in COS)
func (fs *COSFilesystem) Symlink(target, link string) error {
	return fmt.Errorf("symlinks not supported")
}

// Readlink reads a symbolic link (not supported in COS)
func (fs *COSFilesystem) Readlink(link string) (string, error) {
	return "", fmt.Errorf("symlinks not supported")
}

// Chroot creates a chrooted filesystem
func (fs *COSFilesystem) Chroot(path string) (billy.Filesystem, error) {
	newRoot := fs.Join(fs.root, path)
	return &COSFilesystem{
		ops:    fs.ops,
		logger: fs.logger,
		root:   newRoot,
	}, nil
}

// Root returns the root path
func (fs *COSFilesystem) Root() string {
	return fs.root
}

// Chmod changes the mode of the named file
func (fs *COSFilesystem) Chmod(name string, mode os.FileMode) error {
	// COS doesn't support chmod directly, but we can update metadata
	fullPath := fs.Join(fs.root, name)
	
	// Get current file info
	info, err := fs.ops.Stat(context.Background(), fullPath)
	if err != nil {
		return err
	}
	
	// Update with new mode
	attrs := &types.POSIXAttributes{
		Mode:  mode,
		UID:   1000,
		GID:   1000,
		Mtime: time.Now(),
	}
	
	// For files, we need to read and rewrite with new attributes
	if !info.IsDir() {
		data, err := fs.ops.ReadFile(context.Background(), fullPath, 0, 0)
		if err != nil {
			return err
		}
		return fs.ops.WriteFile(context.Background(), fullPath, data, attrs)
	}
	
	// For directories, just update the marker
	return fs.ops.CreateDirectory(context.Background(), fullPath, attrs)
}

// Lchown changes the uid and gid of the named file (link itself)
func (fs *COSFilesystem) Lchown(name string, uid, gid int) error {
	// COS doesn't support symlinks, so this is the same as Chown
	return fs.Chown(name, uid, gid)
}

// Chown changes the uid and gid of the named file
func (fs *COSFilesystem) Chown(name string, uid, gid int) error {
	fullPath := fs.Join(fs.root, name)
	
	// Get current file info
	info, err := fs.ops.Stat(context.Background(), fullPath)
	if err != nil {
		return err
	}
	
	// Update with new ownership
	attrs := &types.POSIXAttributes{
		Mode:  info.Mode(),
		UID:   uid,
		GID:   gid,
		Mtime: time.Now(),
	}
	
	// For files, read and rewrite with new attributes
	if !info.IsDir() {
		data, err := fs.ops.ReadFile(context.Background(), fullPath, 0, 0)
		if err != nil {
			return err
		}
		return fs.ops.WriteFile(context.Background(), fullPath, data, attrs)
	}
	
	// For directories, update the marker
	return fs.ops.CreateDirectory(context.Background(), fullPath, attrs)
}

// Chtimes changes the access and modification times
func (fs *COSFilesystem) Chtimes(name string, atime time.Time, mtime time.Time) error {
	fullPath := fs.Join(fs.root, name)
	
	// Get current file info
	info, err := fs.ops.Stat(context.Background(), fullPath)
	if err != nil {
		return err
	}
	
	// Update with new times
	attrs := &types.POSIXAttributes{
		Mode:  info.Mode(),
		UID:   1000,
		GID:   1000,
		Mtime: mtime,
		Atime: atime,
	}
	
	// Use efficient metadata update (no need to read/rewrite entire file)
	return fs.ops.UpdateAttributes(context.Background(), fullPath, attrs)
}

// COSFile implements billy.File interface
type COSFile struct {
	ops            *posix.OperationsHandler
	logger         *Logger
	path           string
	flag           int
	perm           os.FileMode
	offset         int64
	isNew          bool
	data           []byte
	loaded         bool
	size           int64  // File size (for read-only files without data loaded)
	writeSession   *buffer.WriteSession  // Shared write session (survives handle close) - LEGACY
	flushCount     int    // Number of flushes performed
	totalFlushed   int64  // Total bytes flushed
	totalWrites    int    // Total number of Write() calls
	perfConfig     *config.PerformanceConfig // Performance configuration
	fileID         string // Unique file handle ID for tracking
	sessionManager *buffer.SessionManager // Session manager for path-scoped buffering - LEGACY
	// Staging architecture components
	stagingSession *staging.WriteSession  // Staging write session
	stagingManager *staging.StagingManager
	syncWorker     *staging.SyncWorker
	featureFlags   *feature.FeatureFlags
}

// Name returns the file name
func (f *COSFile) Name() string {
	return filepath.Base(f.path)
}

// Read reads data from the file
func (f *COSFile) Read(p []byte) (int, error) {
	// STAGING PATH: Check staging session first for dirty files
	if f.featureFlags != nil && f.featureFlags.IsStagingEnabled() && f.stagingSession != nil {
		// Read from staging session
		n, err := f.stagingSession.Read(p, f.offset)
		if err != nil && err != io.EOF {
			f.logger.Error("Failed to read from staging session",
				"path", f.path,
				"offset", f.offset,
				"error", err)
			return 0, err
		}
		f.offset += int64(n)
		
		f.logger.Debug("Read from staging session",
			"path", f.path,
			"offset", f.offset-int64(n),
			"bytes", n)
		
		return n, err
	}

	// LEGACY PATH: Original read logic
	if err := f.ensureLoaded(); err != nil {
		return 0, err
	}

	// Check write session buffer first for read-after-write consistency
	if f.writeSession != nil {
		f.writeSession.Mu.Lock()
		bufferedData := f.writeSession.Buffer.Read(f.offset, int64(len(p)))
		f.writeSession.Mu.Unlock()
		
		if len(bufferedData) > 0 {
			n := copy(p, bufferedData)
			f.offset += int64(n)
			f.logger.Debug("Read from write session buffer",
				"path", f.path,
				"session_id", f.writeSession.SessionID,
				"offset", f.offset-int64(n),
				"bytes", n)
			return n, nil
		}
	}

	// If data is loaded in memory (writable file), use it
	if f.data != nil {
		if f.offset >= int64(len(f.data)) {
			return 0, io.EOF
		}
		n := copy(p, f.data[f.offset:])
		f.offset += int64(n)
		return n, nil
	}

	// Check if we're at or past EOF
	if f.offset >= f.size {
		return 0, io.EOF
	}

	// Read-only file: fetch data on-demand using range read
	data, err := f.ops.ReadFile(context.Background(), f.path, f.offset, int64(len(p)))
	if err != nil {
		// Check if it's EOF (no more data to read)
		if err.Error() == "EOF" || strings.Contains(err.Error(), "EOF") {
			return 0, io.EOF
		}
		return 0, err
	}

	// If we got no data, we're at EOF
	if len(data) == 0 {
		return 0, io.EOF
	}

	n := copy(p, data)
	f.offset += int64(n)
	
	// IMPORTANT: Per io.Reader contract, don't return EOF with data
	// Return data with nil error, EOF will be returned on next call
	return n, nil
}

// Write writes data to the file with session-based buffering
func (f *COSFile) Write(p []byte) (int, error) {
	f.totalWrites++

	// STAGING PATH: Write to staging session
	if f.featureFlags != nil && f.featureFlags.IsStagingEnabled() && f.stagingSession != nil {
		n, err := f.stagingSession.Write(p, f.offset)
		if err != nil {
			f.logger.Error("STAGING WRITE ERROR",
				"file_id", f.fileID,
				"path", f.path,
				"offset", f.offset,
				"bytes", len(p),
				"error", err)
			return 0, err
		}

		f.offset += int64(n)
		
		// Mark file as dirty and update size
		f.stagingManager.MarkDirty(f.path, f.stagingSession.Size)

		f.logger.Info("STAGING WRITE",
			"file_id", f.fileID,
			"path", f.path,
			"offset", f.offset-int64(n),
			"bytes", n,
			"session_size", f.stagingSession.Size,
			"write_count", f.totalWrites)

		return n, nil
	}

	// LEGACY PATH: Original write logic
	if err := f.ensureLoaded(); err != nil && !f.isNew {
		return 0, err
	}

	// Get or create write session for this path
	if f.writeSession == nil {
		f.writeSession = f.sessionManager.GetOrCreateSession(f.path)
		f.logger.Info("FILE OPEN - Write session acquired",
			"file_id", f.fileID,
			"session_id", f.writeSession.SessionID,
			"path", f.path)
	}

	// Write to session buffer (thread-safe)
	f.writeSession.Mu.Lock()
	n, err := f.writeSession.Buffer.Write(f.offset, p)
	shouldFlush := f.writeSession.Buffer.ShouldFlush()
	bufferSize := f.writeSession.Buffer.Size()
	f.writeSession.Mu.Unlock()

	if err != nil {
		f.logger.Error("WRITE ERROR",
			"file_id", f.fileID,
			"session_id", f.writeSession.SessionID,
			"path", f.path,
			"offset", f.offset,
			"bytes", len(p),
			"error", err)
		return 0, err
	}

	f.offset += int64(n)

	f.logger.Info("WRITE",
		"file_id", f.fileID,
		"session_id", f.writeSession.SessionID,
		"path", f.path,
		"offset", f.offset-int64(n),
		"bytes", n,
		"buffer_size_bytes", bufferSize,
		"buffer_size_mb", float64(bufferSize)/(1024*1024),
		"write_count", f.totalWrites)

	// Check if we should flush
	if shouldFlush {
		thresholdBytes := int64(f.perfConfig.WriteBufferKB) * 1024
		f.logger.Info("FLUSH TRIGGER: threshold reached",
			"file_id", f.fileID,
			"session_id", f.writeSession.SessionID,
			"path", f.path,
			"buffer_size_bytes", bufferSize,
			"buffer_size_mb", float64(bufferSize)/(1024*1024),
			"threshold_bytes", thresholdBytes,
			"threshold_mb", float64(thresholdBytes)/(1024*1024))
		
		if err := f.flushSessionBuffer(); err != nil {
			f.logger.Error("FLUSH ERROR",
				"file_id", f.fileID,
				"session_id", f.writeSession.SessionID,
				"path", f.path,
				"error", err)
			return n, err
		}
	}

	return n, nil
}

// flushSessionBuffer flushes the write session buffer to COS
func (f *COSFile) flushSessionBuffer() error {
	if f.writeSession == nil {
		return nil
	}

	f.writeSession.Mu.Lock()
	bufferSize := f.writeSession.Buffer.Size()
	if bufferSize == 0 {
		f.writeSession.Mu.Unlock()
		return nil
	}

	start := time.Now()
	
	// Get data to flush
	data, startOffset, err := f.writeSession.Buffer.GetFlushData()
	if err != nil {
		f.writeSession.Mu.Unlock()
		f.logger.Error("Failed to get flush data",
			"file_id", f.fileID,
			"session_id", f.writeSession.SessionID,
			"path", f.path,
			"error", err)
		return err
	}
	flushSize := int64(len(data))
	f.writeSession.Mu.Unlock()
	
	f.logger.Info("FLUSH START",
		"file_id", f.fileID,
		"session_id", f.writeSession.SessionID,
		"path", f.path,
		"bytes", flushSize,
		"start_offset", startOffset,
		"flush_count", f.flushCount+1)

	// For new files or full rewrites, just write the data
	if f.isNew || startOffset == 0 {
		attrs := &types.POSIXAttributes{
			Mode:  f.perm,
			UID:   1000,
			GID:   1000,
			Mtime: time.Now(),
		}
		
		err := f.ops.WriteFile(context.Background(), f.path, data, attrs)
		if err != nil {
			f.logger.Error("Failed to write file during flush",
				"file_id", f.fileID,
				"session_id", f.writeSession.SessionID,
				"path", f.path,
				"bytes", flushSize,
				"error", err)
			return err
		}
	} else {
		// For appends/updates, we need to read existing data and merge
		// This is a limitation of COS - we can't do partial updates
		existingData, err := f.ops.ReadFile(context.Background(), f.path, 0, 0)
		if err != nil && !strings.Contains(err.Error(), "not found") {
			f.logger.Error("Failed to read existing file for merge",
				"file_id", f.fileID,
				"session_id", f.writeSession.SessionID,
				"path", f.path,
				"error", err)
			return err
		}

		// Merge the data
		needed := startOffset + int64(len(data))
		if needed > int64(len(existingData)) {
			newData := make([]byte, needed)
			copy(newData, existingData)
			existingData = newData
		}
		copy(existingData[startOffset:], data)

		attrs := &types.POSIXAttributes{
			Mode:  f.perm,
			UID:   1000,
			GID:   1000,
			Mtime: time.Now(),
		}
		
		err = f.ops.WriteFile(context.Background(), f.path, existingData, attrs)
		if err != nil {
			f.logger.Error("Failed to write merged file during flush",
				"file_id", f.fileID,
				"session_id", f.writeSession.SessionID,
				"path", f.path,
				"bytes", len(existingData),
				"error", err)
			return err
		}
	}

	duration := time.Since(start)
	f.flushCount++
	f.totalFlushed += flushSize

	f.logger.Info("FLUSH COMPLETE",
		"file_id", f.fileID,
		"session_id", f.writeSession.SessionID,
		"path", f.path,
		"bytes", flushSize,
		"duration_ms", duration.Milliseconds(),
		"throughput_mbps", float64(flushSize)/duration.Seconds()/1024/1024,
		"total_flushes", f.flushCount,
		"total_flushed", f.totalFlushed)

	// Clear the session buffer after successful flush
	f.writeSession.Mu.Lock()
	thresholdBytes := int64(f.perfConfig.WriteBufferKB) * 1024
	f.writeSession.Buffer = buffer.NewWriteBuffer(thresholdBytes)
	f.writeSession.Mu.Unlock()

	return nil
}

// Close closes the file and releases the write session
func (f *COSFile) Close() error {
	// STAGING PATH: Release staging session
	if f.featureFlags != nil && f.featureFlags.IsStagingEnabled() && f.stagingSession != nil {
		sessionSize := f.stagingSession.Size
		isDirty := f.stagingSession.Dirty
		
		f.stagingSession.DecrementRefCount()
		refCount := f.stagingSession.GetRefCount()
		
		f.logger.Info("FILE CLOSE - Releasing staging session",
			"file_id", f.fileID,
			"path", f.path,
			"session_size", sessionSize,
			"ref_count", refCount,
			"dirty", isDirty)

		// If this is a zero-byte file that was truncated and this is the last handle,
		// immediately sync it to COS to ensure it exists for NFS attribute operations
		// Note: refCount == 1 means this was the last handle (session starts at 1, we increment to 2 in Open)
		if sessionSize == 0 && isDirty && refCount == 1 && f.totalWrites == 0 {
			f.logger.Info("Immediately syncing zero-byte truncated file",
				"file_id", f.fileID,
				"path", f.path)
			
			// Sync to staging file
			if err := f.stagingSession.Sync(); err != nil {
				f.logger.Error("Failed to sync zero-byte file to staging",
					"file_id", f.fileID,
					"path", f.path,
					"error", err)
			} else {
				// Upload empty file to COS immediately
				attrs := &types.POSIXAttributes{
					Mode:  f.perm,
					UID:   1000,
					GID:   1000,
					Mtime: time.Now(),
				}
				if err := f.ops.WriteFile(context.Background(), f.path, []byte{}, attrs); err != nil {
					f.logger.Error("Failed to upload zero-byte file to COS",
						"file_id", f.fileID,
						"path", f.path,
						"error", err)
				} else {
					// Mark as clean since we just synced it
					f.stagingManager.MarkClean(f.path)
					f.logger.Info("Successfully synced zero-byte file to COS",
						"file_id", f.fileID,
						"path", f.path)
				}
			}
		}

		// Release session reference (session persists for other handles)
		f.stagingManager.ReleaseSession(f.path)
		
		// Log final statistics for this handle
		if f.totalWrites > 0 {
			f.logger.Info("File handle closed with write statistics",
				"file_id", f.fileID,
				"path", f.path,
				"total_writes", f.totalWrites,
				"session_size", f.stagingSession.Size)
		}
		
		return nil
	}

	// LEGACY PATH: Release write session and flush if this is the last reference
	if f.writeSession != nil {
		f.writeSession.Mu.Lock()
		bufferSize := f.writeSession.Buffer.Size()
		sessionID := f.writeSession.SessionID
		f.writeSession.Mu.Unlock()

		f.logger.Info("FILE CLOSE - Releasing write session",
			"file_id", f.fileID,
			"session_id", sessionID,
			"path", f.path,
			"buffer_size_bytes", bufferSize,
			"buffer_size_mb", float64(bufferSize)/(1024*1024))

		// Release session reference
		f.sessionManager.ReleaseSession(f.path)
		
		// Check if this was the last reference and flush if needed
		if session, exists := f.sessionManager.GetSession(f.path); exists {
			session.Mu.Lock()
			refCount := session.RefCount
			hasData := session.Buffer.Size() > 0
			session.Mu.Unlock()
			
			// If no more references and buffer has data, flush immediately
			if refCount == 0 && hasData {
				f.logger.Info("FILE CLOSE - Last reference, flushing buffer",
					"file_id", f.fileID,
					"session_id", sessionID,
					"path", f.path,
					"buffer_size_bytes", bufferSize)
				
				if err := f.flushSessionBuffer(); err != nil {
					f.logger.Error("FILE CLOSE - Flush failed",
						"file_id", f.fileID,
						"session_id", sessionID,
						"path", f.path,
						"error", err)
					return err
				}
				
				// Close the session after successful flush
				f.sessionManager.CloseSession(f.path)
			}
		}
	}

	// Log final statistics for this handle
	if f.flushCount > 0 {
		f.logger.Info("File handle closed with write statistics",
			"file_id", f.fileID,
			"path", f.path,
			"total_flushes", f.flushCount,
			"total_bytes", f.totalFlushed,
			"total_writes", f.totalWrites,
			"avg_flush_size", f.totalFlushed/int64(f.flushCount))
	}

	// Legacy path: handle old-style in-memory data if present
	if f.flag&(os.O_WRONLY|os.O_RDWR) != 0 && len(f.data) > 0 && f.writeSession == nil && f.stagingSession == nil {
		attrs := &types.POSIXAttributes{
			Mode:  f.perm,
			UID:   1000,
			GID:   1000,
			Mtime: time.Now(),
		}
		err := f.ops.WriteFile(context.Background(), f.path, f.data, attrs)
		if err != nil {
			return err
		}
	}
	
	return nil
}

// Seek sets the file offset
func (f *COSFile) Seek(offset int64, whence int) (int64, error) {
	if err := f.ensureLoaded(); err != nil && !f.isNew {
		return 0, err
	}

	var fileSize int64
	if f.data != nil {
		fileSize = int64(len(f.data))
	} else {
		fileSize = f.size
	}

	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		f.offset = fileSize + offset
	default:
		return 0, fmt.Errorf("invalid whence")
	}

	if f.offset < 0 {
		f.offset = 0
	}

	return f.offset, nil
}

// Lock locks the file (no-op for COS)
func (f *COSFile) Lock() error {
	return nil
}

// Unlock unlocks the file (no-op for COS)
func (f *COSFile) Unlock() error {
	return nil
}

// ReadAt reads data from the file at a specific offset
func (f *COSFile) ReadAt(p []byte, off int64) (int, error) {
	if err := f.ensureLoaded(); err != nil {
		return 0, err
	}

	// If data is loaded in memory (writable file), use it
	if f.data != nil {
		if off >= int64(len(f.data)) {
			return 0, io.EOF
		}
		n := copy(p, f.data[off:])
		if n < len(p) {
			return n, io.EOF
		}
		return n, nil
	}

	// Check if we're at or past EOF
	if off >= f.size {
		return 0, io.EOF
	}

	// Read-only file: fetch data on-demand using range read
	data, err := f.ops.ReadFile(context.Background(), f.path, off, int64(len(p)))
	if err != nil {
		// Check if it's EOF (no more data to read)
		if err.Error() == "EOF" || strings.Contains(err.Error(), "EOF") {
			return 0, io.EOF
		}
		return 0, err
	}

	// If we got no data, we're at EOF
	if len(data) == 0 {
		return 0, io.EOF
	}

	n := copy(p, data)
	
	// Per io.ReaderAt contract: return EOF only if no bytes were read
	// If we read some bytes but less than requested, that's still success
	// The caller will detect EOF on the next call when off >= size
	if n < len(p) && off+int64(n) >= f.size {
		return n, io.EOF
	}
	
	return n, nil
}

// Truncate truncates the file to a specified size
func (f *COSFile) Truncate(size int64) error {
	if err := f.ensureLoaded(); err != nil && !f.isNew {
		return err
	}

	if size < int64(len(f.data)) {
		f.data = f.data[:size]
	} else if size > int64(len(f.data)) {
		newData := make([]byte, size)
		copy(newData, f.data)
		f.data = newData
	}

	return nil
}

// ensureLoaded loads file data from COS if not already loaded
func (f *COSFile) ensureLoaded() error {
	if f.loaded || f.isNew {
		return nil
	}

	// STAGING PATH: If using staging, skip COS load - data is in staging file
	if f.featureFlags != nil && f.featureFlags.IsStagingEnabled() && f.stagingSession != nil {
		// Get size from staging session
		f.size = f.stagingSession.GetSize()
		f.loaded = true
		f.data = nil
		return nil
	}

	// For read-only files, don't load entire file into memory
	// Instead, use lazy loading and read from COS on demand
	if f.flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		// Read-only mode - get file size but don't load data
		// Data will be fetched on-demand in Read/ReadAt operations
		info, err := f.ops.Stat(context.Background(), f.path)
		if err != nil {
			return err
		}
		f.size = info.Size()
		f.loaded = true
		f.data = nil
		return nil
	}

	// For writable files, we need to load the data for read-modify-write
	data, err := f.ops.ReadFile(context.Background(), f.path, 0, 0)
	if err != nil {
		return err
	}

	f.data = data
	f.size = int64(len(data))
	f.loaded = true
	return nil
}

// Made with Bob
