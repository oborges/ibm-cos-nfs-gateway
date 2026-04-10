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
	"github.com/oborges/cos-nfs-gateway/internal/posix"
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
	ops    *posix.OperationsHandler
	logger *Logger
	root   string
}

// NewCOSFilesystem creates a new COS filesystem
func NewCOSFilesystem(ops *posix.OperationsHandler, logger *Logger, root string) *COSFilesystem {
	return &COSFilesystem{
		ops:    ops,
		logger: logger,
		root:   root,
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

	file := &COSFile{
		ops:    fs.ops,
		logger: fs.logger,
		path:   fullPath,
		flag:   flag,
		perm:   perm,
		offset: 0,
	}

	// If creating or truncating, handle accordingly
	if flag&os.O_CREATE != 0 {
		// File will be created on first write
		file.isNew = true
	}

	if flag&os.O_TRUNC != 0 {
		// Truncate the file
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
	return fs.ops.DeleteFile(context.Background(), fullPath)
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

// COSFile implements billy.File interface
type COSFile struct {
	ops    *posix.OperationsHandler
	logger *Logger
	path   string
	flag   int
	perm   os.FileMode
	offset int64
	isNew  bool
	data   []byte
	loaded bool
}

// Name returns the file name
func (f *COSFile) Name() string {
	return filepath.Base(f.path)
}

// Read reads data from the file
func (f *COSFile) Read(p []byte) (int, error) {
	if err := f.ensureLoaded(); err != nil {
		return 0, err
	}

	if f.offset >= int64(len(f.data)) {
		return 0, io.EOF
	}

	n := copy(p, f.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

// Write writes data to the file
func (f *COSFile) Write(p []byte) (int, error) {
	if err := f.ensureLoaded(); err != nil && !f.isNew {
		return 0, err
	}

	// Extend data if necessary
	needed := f.offset + int64(len(p))
	if needed > int64(len(f.data)) {
		newData := make([]byte, needed)
		copy(newData, f.data)
		f.data = newData
	}

	n := copy(f.data[f.offset:], p)
	f.offset += int64(n)
	return n, nil
}

// Close closes the file and writes changes back to COS
func (f *COSFile) Close() error {
	if f.flag&(os.O_WRONLY|os.O_RDWR) != 0 && (f.isNew || len(f.data) > 0) {
		// Write the data back to COS
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

	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		f.offset = int64(len(f.data)) + offset
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

	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}

	n := copy(p, f.data[off:])
	if n < len(p) {
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

	data, err := f.ops.ReadFile(context.Background(), f.path, 0, 0)
	if err != nil {
		return err
	}

	f.data = data
	f.loaded = true
	return nil
}

// Made with Bob
