package posix

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/oborges/cos-nfs-gateway/internal/cache"
	"github.com/oborges/cos-nfs-gateway/internal/cos"
	"github.com/oborges/cos-nfs-gateway/internal/logging"
	"github.com/oborges/cos-nfs-gateway/internal/metrics"
	"github.com/oborges/cos-nfs-gateway/pkg/types"
	"go.uber.org/zap"
)

// OperationsHandler handles POSIX filesystem operations
type OperationsHandler struct {
	cosClient     *cos.Client
	metadataCache *cache.MetadataCache
	dataCache     *cache.DataCache
	translator    *PathTranslator
}

// NewOperationsHandler creates a new operations handler
func NewOperationsHandler(
	cosClient *cos.Client,
	metadataCache *cache.MetadataCache,
	dataCache *cache.DataCache,
) *OperationsHandler {
	return &OperationsHandler{
		cosClient:     cosClient,
		metadataCache: metadataCache,
		dataCache:     dataCache,
		translator:    NewPathTranslator(""),
	}
}

// Stat retrieves file/directory metadata
func (h *OperationsHandler) Stat(ctx context.Context, path string) (*FileInfo, error) {
	log := logging.WithOperation("Stat").With(zap.String("path", path))
	start := time.Now()
	defer func() {
		metrics.RecordNFSRequest("stat", "success", time.Since(start))
	}()

	// Check cache first, but skip if it's an implicit directory (needs validation)
	if entry, ok := h.metadataCache.Get(path); ok {
		// If it's an implicit directory, don't trust the cache - validate it exists
		if !entry.IsImplicit {
			metrics.RecordCacheHit("metadata")
			log.Debug("Metadata cache hit")
			
			// If we have FileInfo, use it directly
			if entry.FileInfo != nil {
				return entry.FileInfo.(*FileInfo), nil
			}
			
			// Fallback: construct from attributes
			mode := os.FileMode(0644)
			modTime := DefaultAttributes(entry.IsDir).Mtime
			size := int64(0)
			if entry.Attributes != nil {
				mode = entry.Attributes.Mode
				modTime = entry.Attributes.Mtime
			}
			if entry.IsDir {
				mode = mode | os.ModeDir
			}
			
			return &FileInfo{
				name:    GetBaseName(path),
				size:    size,
				mode:    mode,
				modTime: modTime,
				isDir:   entry.IsDir,
			}, nil
		}
		// Implicit directory - fall through to validate
		log.Info("Implicit directory detected, validating existence", zap.String("path", path))
	}
	metrics.RecordCacheMiss("metadata")
	log.Info("Stat cache miss", zap.String("path", path))

	// Translate path to object key
	objectKey := h.translator.ToObjectKey(path)

	// Try as file first
	metrics.RecordCOSHeadObject()
	metadata, err := h.cosClient.HeadObject(ctx, objectKey)
	if err == nil {
		// It's a file
		attrs := DecodePOSIXAttributes(metadata.Metadata, false)
		info := &FileInfo{
			name:    GetBaseName(path),
			size:    metadata.Size,
			mode:    attrs.Mode,
			modTime: metadata.LastModified,
			isDir:   false,
		}

		// Cache the result
		h.metadataCache.SetFileInfo(path, info, attrs)

		// Removed debug logging from hot path
		return info, nil
	}

	// Try as directory
	dirKey := ToDirectoryKey(objectKey)
	metrics.RecordCOSHeadObject()
	metadata, err = h.cosClient.HeadObject(ctx, dirKey)
	if err == nil {
		// It's a directory
		attrs := DecodePOSIXAttributes(metadata.Metadata, true)
		info := &FileInfo{
			name:    GetBaseName(path),
			size:    0,
			mode:    attrs.Mode | os.ModeDir,
			modTime: metadata.LastModified,
			isDir:   true,
		}

		// Cache the result
		h.metadataCache.SetFileInfo(path, info, attrs)

		// Removed debug logging from hot path
		return info, nil
	}

	// Check if it's an implicit directory (has children but no marker object)
	// This happens when directories are created implicitly by uploading files
	prefix := objectKey
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	
	metrics.RecordCOSListObjects()
	objects, err := h.cosClient.ListObjects(ctx, prefix, 1)
	if err == nil && len(objects) > 0 {
		// It's an implicit directory - has children
		log.Debug("Implicit directory detected", zap.String("prefix", prefix))
		
		// Use default directory attributes
		attrs := DefaultAttributes(true)
		
		info := &FileInfo{
			name:    GetBaseName(path),
			size:    0,
			mode:    attrs.Mode,
			modTime: attrs.Mtime,
			isDir:   true,
		}

		// Cache the result as a normal directory (NOT implicit)
		// Once validated, we don't need to re-validate on every Stat() call
		h.metadataCache.SetFileInfo(path, info, attrs)

		log.Debug("Implicit directory stat successful")
		return info, nil
	}

	log.Debug("Path not found")
	return nil, os.ErrNotExist
}

// DownloadToFile streams the object from COS into a local file path
func (h *OperationsHandler) DownloadToFile(ctx context.Context, path string, localPath string) error {
	log := logging.WithOperation("DownloadToFile").With(
		zap.String("path", path),
		zap.String("local_path", localPath),
	)

	objectKey := h.translator.ToObjectKey(path)
	metrics.RecordCOSGetObject()

	stream, err := h.cosClient.GetObjectStream(ctx, objectKey)
	if err != nil {
		return err
	}
	defer stream.Close()

	file, err := os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open local file for prefetch: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, stream); err != nil {
		return fmt.Errorf("failed to copy stream body to file: %w", err)
	}

	log.Debug("Stream download to file complete")
	return nil
}

// ReadFile reads file content
func (h *OperationsHandler) ReadFile(ctx context.Context, path string, offset, length int64) ([]byte, error) {
	log := logging.WithOperation("ReadFile").With(
		zap.String("path", path),
		zap.Int64("offset", offset),
		zap.Int64("length", length),
	)
	start := time.Now()
	defer func() {
		metrics.RecordNFSRequest("read", "success", time.Since(start))
	}()

	// Try cache first - but only for full file reads
	// The cache is designed for complete files, not partial ranges
	if h.dataCache.IsEnabled() && offset == 0 && length == 0 {
		if data, err := h.dataCache.Read(path, offset, length); err == nil {
			metrics.RecordCacheHit("data")
			metrics.RecordBytesRead(int64(len(data)))
			log.Debug("Data cache hit", zap.Int("bytes", len(data)))
			return data, nil
		}
		metrics.RecordCacheMiss("data")
	}

	// Read from COS
	objectKey := h.translator.ToObjectKey(path)
	
	var data []byte
	var err error
	
	if length > 0 {
		// Range read - don't cache partial reads
		data, err = h.cosClient.GetObjectRange(ctx, objectKey, offset, length)
	} else {
		// Full read - can be cached
		data, err = h.cosClient.GetObject(ctx, objectKey)
	}

	if err != nil {
		log.Error("Failed to read file", zap.Error(err))
		return nil, err
	}

	// Cache the data - but only for full file reads
	if h.dataCache.IsEnabled() && len(data) > 0 && offset == 0 && length == 0 {
		if err := h.dataCache.Write(path, data); err != nil {
			log.Warn("Failed to cache data", zap.Error(err))
		}
	}

	metrics.RecordBytesRead(int64(len(data)))
	log.Debug("File read successful", zap.Int("bytes", len(data)))
	return data, nil
}

// WriteFile writes file content
func (h *OperationsHandler) WriteFile(ctx context.Context, path string, data []byte, attrs *types.POSIXAttributes) error {
	log := logging.WithOperation("WriteFile").With(
		zap.String("path", path),
		zap.Int("bytes", len(data)),
	)
	start := time.Now()
	defer func() {
		metrics.RecordNFSRequest("write", "success", time.Since(start))
	}()

	objectKey := h.translator.ToObjectKey(path)

	// Encode attributes
	metadata := EncodePOSIXAttributes(attrs)

	// Write to COS
	err := h.cosClient.PutObject(ctx, objectKey, data, metadata)
	if err != nil {
		log.Error("Failed to write file", zap.Error(err))
		return err
	}

	// Invalidate caches
	h.metadataCache.InvalidatePath(path)
	if h.dataCache.IsEnabled() {
		h.dataCache.Delete(path)
	}

	metrics.RecordBytesWritten(int64(len(data)))
	log.Debug("File write successful")
	return nil
}

// DeleteFile deletes a file
func (h *OperationsHandler) DeleteFile(ctx context.Context, path string) error {
	log := logging.WithOperation("DeleteFile").With(zap.String("path", path))
	start := time.Now()
	defer func() {
		metrics.RecordNFSRequest("delete", "success", time.Since(start))
	}()

	objectKey := h.translator.ToObjectKey(path)

	// Delete from COS
	err := h.cosClient.DeleteObject(ctx, objectKey)
	if err != nil {
		log.Error("Failed to delete file", zap.Error(err))
		return err
	}

	// Invalidate caches
	h.metadataCache.InvalidatePath(path)
	if h.dataCache.IsEnabled() {
		h.dataCache.Delete(path)
	}

	log.Debug("File deleted successfully")
	return nil
}

// CreateDirectory creates a directory
func (h *OperationsHandler) CreateDirectory(ctx context.Context, path string, attrs *types.POSIXAttributes) error {
	log := logging.WithOperation("CreateDirectory").With(zap.String("path", path))
	start := time.Now()
	defer func() {
		metrics.RecordNFSRequest("mkdir", "success", time.Since(start))
	}()

	objectKey := ToDirectoryKey(h.translator.ToObjectKey(path))

	// Encode attributes
	if attrs == nil {
		attrs = DefaultAttributes(true)
	}
	metadata := EncodePOSIXAttributes(attrs)

	// Create directory marker in COS
	err := h.cosClient.PutObject(ctx, objectKey, []byte{}, metadata)
	if err != nil {
		log.Error("Failed to create directory", zap.Error(err))
		return err
	}

	// Invalidate parent directory cache
	h.metadataCache.InvalidatePath(GetParentPath(path))

	log.Debug("Directory created successfully")
	return nil
}

// DeleteDirectory deletes a directory
func (h *OperationsHandler) DeleteDirectory(ctx context.Context, path string) error {
	log := logging.WithOperation("DeleteDirectory").With(zap.String("path", path))
	start := time.Now()
	defer func() {
		metrics.RecordNFSRequest("rmdir", "success", time.Since(start))
	}()

	// Check if directory is empty
	entries, err := h.ListDirectory(ctx, path)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		log.Warn("Directory not empty", zap.Int("entries", len(entries)))
		return fmt.Errorf("directory not empty")
	}

	objectKey := ToDirectoryKey(h.translator.ToObjectKey(path))

	// Delete directory marker
	err = h.cosClient.DeleteObject(ctx, objectKey)
	if err != nil {
		log.Error("Failed to delete directory", zap.Error(err))
		return err
	}

	// Invalidate caches
	h.metadataCache.InvalidateDirectory(path)

	log.Debug("Directory deleted successfully")
	return nil
}

// ListDirectory lists directory contents
func (h *OperationsHandler) ListDirectory(ctx context.Context, path string) ([]*FileInfo, error) {
	log := logging.WithOperation("ListDirectory").With(zap.String("path", path))
	start := time.Now()
	cacheHit := false
	
	defer func() {
		duration := time.Since(start)
		metrics.RecordNFSRequest("readdir", "success", duration)
		metrics.RecordListDirectory(duration, cacheHit)
		
		// Log first call, cache misses, or slow operations
		counters := metrics.GetGlobalCounters()
		callCount := counters.ListDirCalls.Load()
		if callCount == 1 || !cacheHit || duration > 10*time.Millisecond {
			log.Info("ListDirectory",
				zap.Bool("cache_hit", cacheHit),
				zap.Int64("duration_ms", duration.Milliseconds()),
				zap.Int64("call_number", callCount))
		}
	}()

	// Check cache first - NEW: Return full FileInfo entries directly (O(1) cache hit)
	if entry, ok := h.metadataCache.Get(path); ok && entry.ChildEntries != nil {
		cacheHit = true
		metrics.RecordCacheHit("metadata")
		
		// Convert []os.FileInfo to []*FileInfo (just type assertion, no COS calls)
		entries := make([]*FileInfo, len(entry.ChildEntries))
		for i, info := range entry.ChildEntries {
			if fi, ok := info.(*FileInfo); ok {
				entries[i] = fi
			} else {
				// Shouldn't happen, but handle gracefully
				log.Warn("Invalid cached entry type, refetching from COS")
				h.metadataCache.InvalidatePath(path)
				goto fetchFromCOS
			}
		}
		
		// O(1) cache hit - no per-file Stat() calls!
		return entries, nil
	}
	
	// DEPRECATED: Old cache format with just names (fallback for compatibility)
	if entry, ok := h.metadataCache.Get(path); ok && entry.Children != nil {
		log.Warn("Using deprecated cache format, will upgrade on next fetch")
		h.metadataCache.InvalidatePath(path)
		// Fall through to fetch fresh listing
	}
	
fetchFromCOS:
	cacheHit = false
	metrics.RecordCacheMiss("metadata")

	// List from COS
	prefix := ListPrefix(path)
	log.Info("ListDirectory cache miss, fetching from COS", zap.String("prefix", prefix))
	
	cosStart := time.Now()
	metrics.RecordCOSListObjects()
	objects, err := h.cosClient.ListObjects(ctx, prefix, 0)
	if err != nil {
		log.Error("Failed to list directory", zap.Error(err))
		return nil, err
	}

	log.Info("Got objects from COS",
		zap.Int("count", len(objects)),
		zap.Duration("cos_duration", time.Since(cosStart)))

	// Convert to FileInfo
	entries := make([]*FileInfo, 0, len(objects))
	seen := make(map[string]bool)

	for _, obj := range objects {
		log.Debug("Processing object", zap.String("key", obj.Key))
		
		// Remove prefix to get relative path
		relPath := obj.Key
		if prefix != "" {
			relPath = strings.TrimPrefix(relPath, prefix)
		}

		// Skip if empty
		if relPath == "" {
			log.Debug("Skipping empty relPath")
			continue
		}

		// For root directory, get the first path component
		// For subdirectories, get the immediate child
		parts := strings.Split(strings.Trim(relPath, "/"), "/")
		if len(parts) == 0 {
			log.Debug("Skipping - no parts")
			continue
		}
		
		name := parts[0]
		isDir := len(parts) > 1 || strings.HasSuffix(obj.Key, "/")
		
		// Skip if already seen
		if seen[name] {
			log.Debug("Skipping duplicate", zap.String("name", name))
			continue
		}
		seen[name] = true

		attrs := DecodePOSIXAttributes(obj.Metadata, isDir)
		mode := attrs.Mode
		if isDir && (mode&os.ModeDir) == 0 {
			mode = mode | os.ModeDir
		}

		info := &FileInfo{
			name:    name,
			size:    obj.Size,
			mode:    mode,
			modTime: obj.LastModified,
			isDir:   isDir,
		}

		log.Debug("Adding entry",
			zap.String("name", name),
			zap.Bool("isDir", isDir),
			zap.Int64("size", obj.Size))

		entries = append(entries, info)
	}

	// Cache the full FileInfo entries (NEW: O(1) retrieval on cache hit)
	osEntries := make([]os.FileInfo, len(entries))
	for i, entry := range entries {
		osEntries[i] = entry
	}
	h.metadataCache.SetDirEntries(path, osEntries)

	log.Info("Directory listed and cached", zap.Int("entries", len(entries)))
	return entries, nil
}

// RenameFile renames/moves a file or directory
func (h *OperationsHandler) RenameFile(ctx context.Context, oldPath, newPath string) error {
	log := logging.WithOperation("RenameFile").With(
		zap.String("oldPath", oldPath),
		zap.String("newPath", newPath),
	)
	start := time.Now()
	defer func() {
		metrics.RecordNFSRequest("rename", "success", time.Since(start))
	}()

	oldKey := h.translator.ToObjectKey(oldPath)
	newKey := h.translator.ToObjectKey(newPath)

	// Copy to new location
	err := h.cosClient.CopyObject(ctx, oldKey, newKey)
	if err != nil {
		log.Error("Failed to copy object", zap.Error(err))
		return err
	}

	// Delete old location
	err = h.cosClient.DeleteObject(ctx, oldKey)
	if err != nil {
		log.Error("Failed to delete old object", zap.Error(err))
		// Try to clean up the copy
		h.cosClient.DeleteObject(ctx, newKey)
		return err
	}

	// Invalidate caches
	h.metadataCache.InvalidatePath(oldPath)
	h.metadataCache.InvalidatePath(newPath)
	if h.dataCache.IsEnabled() {
		h.dataCache.Delete(oldPath)
	}

	log.Debug("File renamed successfully")
	return nil
}

// UpdateAttributes updates file/directory attributes without rewriting content
func (h *OperationsHandler) UpdateAttributes(ctx context.Context, path string, attrs *types.POSIXAttributes) error {
	log := logging.WithOperation("UpdateAttributes").With(zap.String("path", path))
	start := time.Now()
	defer func() {
		metrics.RecordNFSRequest("setattr", "success", time.Since(start))
	}()

	objectKey := h.translator.ToObjectKey(path)

	// Check if it's a directory
	info, err := h.Stat(ctx, path)
	if err != nil {
		return err
	}

	if info.IsDir() {
		objectKey = ToDirectoryKey(objectKey)
	}

	// Encode attributes
	metadata := EncodePOSIXAttributes(attrs)

	// Update metadata using copy-to-self
	err = h.cosClient.UpdateObjectMetadata(ctx, objectKey, metadata)
	if err != nil {
		log.Error("Failed to update attributes", zap.Error(err))
		return err
	}

	// Invalidate metadata cache
	h.metadataCache.InvalidatePath(path)

	log.Debug("Attributes updated successfully")
	return nil
}

// FileInfo represents file information
type FileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

// Implement os.FileInfo interface
func (f *FileInfo) Name() string       { return f.name }
func (f *FileInfo) Size() int64        { return f.size }
func (f *FileInfo) Mode() os.FileMode  { return f.mode }
func (f *FileInfo) ModTime() time.Time { return f.modTime }
func (f *FileInfo) IsDir() bool        { return f.isDir }
func (f *FileInfo) Sys() interface{}   { return nil }

var _ os.FileInfo = (*FileInfo)(nil)
var _ io.Closer = (*OperationsHandler)(nil)

// Close closes the operations handler
func (h *OperationsHandler) Close() error {
	return nil
}

// Made with Bob
