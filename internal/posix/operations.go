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

	// Check cache first
	if entry, ok := h.metadataCache.Get(path); ok {
		metrics.RecordCacheHit("metadata")
		log.Debug("Metadata cache hit")
		
		// If we have FileInfo, use it directly
		if entry.FileInfo != nil {
			return entry.FileInfo.(*FileInfo), nil
		}
		
		// Fallback: construct from attributes
		mode := os.FileMode(0644)
		modTime := time.Now()
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
	metrics.RecordCacheMiss("metadata")

	// Translate path to object key
	objectKey := h.translator.ToObjectKey(path)

	// Try as file first
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

		log.Debug("File stat successful", zap.Int64("size", metadata.Size))
		return info, nil
	}

	// Try as directory
	dirKey := ToDirectoryKey(objectKey)
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

		log.Debug("Directory stat successful")
		return info, nil
	}

	log.Debug("Path not found")
	return nil, os.ErrNotExist
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
	defer func() {
		metrics.RecordNFSRequest("readdir", "success", time.Since(start))
	}()

	// Check cache first
	if entry, ok := h.metadataCache.Get(path); ok && entry.Children != nil {
		metrics.RecordCacheHit("metadata")
		log.Debug("Directory listing cache hit", zap.Int("entries", len(entry.Children)))
		
		// Convert children names to FileInfo by statting each one
		entries := make([]*FileInfo, 0, len(entry.Children))
		failedStats := 0
		maxFailures := len(entry.Children) / 2
		cacheInvalidated := false
		
		for i, childName := range entry.Children {
			childPath := path
			if path == "/" {
				childPath = "/" + childName
			} else {
				childPath = path + "/" + childName
			}
			
			// Get file info from cache or COS
			info, err := h.Stat(ctx, childPath)
			if err != nil {
				failedStats++
				
				// Remove this stale child from the cached directory listing
				h.metadataCache.RemoveChildFromListing(path, childName)
				
				log.Debug("Cached child not found, removed from cache",
					zap.String("child", childName))
				
				// Check failure threshold AFTER incrementing - if >50% have failed, stop and re-list
				if failedStats > maxFailures {
					log.Warn("Too many cached children not found, invalidating cache and re-listing",
						zap.String("path", path),
						zap.Int("failed", failedStats),
						zap.Int("checked", i+1),
						zap.Int("total", len(entry.Children)))
					h.metadataCache.InvalidatePath(path)
					cacheInvalidated = true
					break
				}
				continue
			}
			entries = append(entries, info)
		}
		
		// If cache was invalidated due to too many failures, fall through to re-list from COS
		if cacheInvalidated {
			log.Debug("Falling through to re-list from COS after cache invalidation")
			// Fall through to ListObjects below
		} else if failedStats <= maxFailures {
			// If we successfully got most entries, return them
			log.Debug("Directory listing from cache successful",
				zap.Int("entries", len(entries)),
				zap.Int("skipped", failedStats))
			return entries, nil
		}
		// Otherwise fall through to re-list from COS
	}
	metrics.RecordCacheMiss("metadata")

	// List from COS
	prefix := ListPrefix(path)
	log.Debug("Listing directory", zap.String("prefix", prefix))
	
	objects, err := h.cosClient.ListObjects(ctx, prefix, 1000)
	if err != nil {
		log.Error("Failed to list directory", zap.Error(err))
		return nil, err
	}

	log.Debug("Got objects from COS", zap.Int("count", len(objects)))

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

	// Cache the listing
	children := make([]string, len(entries))
	for i, entry := range entries {
		children[i] = entry.Name()
	}
	h.metadataCache.SetDirListing(path, children)

	log.Debug("Directory listed successfully", zap.Int("entries", len(entries)))
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
