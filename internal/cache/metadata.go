package cache

import (
	"os"
	"sync"
	"time"

	"github.com/oborges/cos-nfs-gateway/internal/config"
	"github.com/oborges/cos-nfs-gateway/internal/logging"
	"github.com/oborges/cos-nfs-gateway/pkg/types"
	"go.uber.org/zap"
)

// MetadataCache caches file and directory metadata
type MetadataCache struct {
	cache   *LRUCache
	enabled bool
	mu      sync.RWMutex
}

// MetadataEntry represents cached metadata
type MetadataEntry struct {
	FileInfo   os.FileInfo
	Attributes *types.POSIXAttributes
	IsDir      bool
	Children   []string // For directory listings
	CachedAt   time.Time
}

// NewMetadataCache creates a new metadata cache
func NewMetadataCache(cfg *config.MetadataCacheConfig) *MetadataCache {
	if cfg == nil || !cfg.Enabled {
		logging.Info("Metadata cache disabled")
		return &MetadataCache{enabled: false}
	}

	cache := NewLRUCache(cfg.MaxEntries, cfg.GetTTL())
	
	// Set eviction callback for logging
	cache.SetEvictCallback(func(key string, value interface{}) {
		logging.Debug("Metadata cache entry evicted", zap.String("key", key))
	})

	logging.Info("Metadata cache initialized",
		zap.Int("maxEntries", cfg.MaxEntries),
		zap.Duration("ttl", cfg.GetTTL()),
	)

	return &MetadataCache{
		cache:   cache,
		enabled: true,
	}
}

// Get retrieves metadata from cache
func (c *MetadataCache) Get(path string) (*MetadataEntry, bool) {
	if !c.enabled {
		return nil, false
	}

	value, ok := c.cache.Get(path)
	if !ok {
		return nil, false
	}

	entry, ok := value.(*MetadataEntry)
	if !ok {
		logging.Warn("Invalid metadata cache entry type", zap.String("path", path))
		c.cache.Delete(path)
		return nil, false
	}

	logging.Debug("Metadata cache hit", zap.String("path", path))
	return entry, true
}

// Set stores metadata in cache
func (c *MetadataCache) Set(path string, entry *MetadataEntry) {
	if !c.enabled {
		return
	}

	entry.CachedAt = time.Now()
	c.cache.Set(path, entry)
	logging.Debug("Metadata cached", zap.String("path", path))
}

// SetFileInfo stores file info in cache
func (c *MetadataCache) SetFileInfo(path string, info os.FileInfo, attrs *types.POSIXAttributes) {
	if !c.enabled {
		return
	}

	entry := &MetadataEntry{
		FileInfo:   info,
		Attributes: attrs,
		IsDir:      info.IsDir(),
		CachedAt:   time.Now(),
	}
	c.cache.Set(path, entry)
	logging.Debug("File info cached", zap.String("path", path))
}

// SetDirListing stores directory listing in cache
func (c *MetadataCache) SetDirListing(path string, children []string) {
	if !c.enabled {
		return
	}

	entry := &MetadataEntry{
		IsDir:    true,
		Children: children,
		CachedAt: time.Now(),
	}
	c.cache.Set(path, entry)
	logging.Debug("Directory listing cached",
		zap.String("path", path),
		zap.Int("children", len(children)),
	)
}

// RemoveChildFromListing removes a child from a cached directory listing
func (c *MetadataCache) RemoveChildFromListing(dirPath string, childName string) bool {
	if !c.enabled {
		return false
	}

	entry, ok := c.Get(dirPath)
	if !ok || !entry.IsDir || entry.Children == nil {
		return false
	}

	// Find and remove the child
	newChildren := make([]string, 0, len(entry.Children)-1)
	found := false
	for _, child := range entry.Children {
		if child != childName {
			newChildren = append(newChildren, child)
		} else {
			found = true
		}
	}

	if found {
		// Update the cache with the new children list
		entry.Children = newChildren
		entry.CachedAt = time.Now()
		c.cache.Set(dirPath, entry)
		logging.Debug("Child removed from cached directory listing",
			zap.String("dir", dirPath),
			zap.String("child", childName),
			zap.Int("remaining", len(newChildren)))
		return true
	}

	return false
}

// Delete removes an entry from cache
func (c *MetadataCache) Delete(path string) {
	if !c.enabled {
		return
	}

	if c.cache.Delete(path) {
		logging.Debug("Metadata cache entry deleted", zap.String("path", path))
	}
}

// InvalidatePath invalidates cache for a specific path
func (c *MetadataCache) InvalidatePath(path string) {
	if !c.enabled {
		return
	}

	c.Delete(path)
	
	// Also invalidate parent directory listing
	if path != "/" && path != "" {
		parentPath := getParentPath(path)
		c.Delete(parentPath)
		logging.Debug("Parent directory cache invalidated", zap.String("parent", parentPath))
	}
}

// InvalidatePrefix invalidates all entries with the given prefix
func (c *MetadataCache) InvalidatePrefix(prefix string) int {
	if !c.enabled {
		return 0
	}

	count := c.cache.DeletePrefix(prefix)
	logging.Debug("Cache entries invalidated by prefix",
		zap.String("prefix", prefix),
		zap.Int("count", count),
	)
	return count
}

// InvalidateDirectory invalidates a directory and all its children
func (c *MetadataCache) InvalidateDirectory(dirPath string) int {
	if !c.enabled {
		return 0
	}

	// Ensure path ends with /
	if dirPath != "/" && dirPath[len(dirPath)-1] != '/' {
		dirPath += "/"
	}

	count := c.InvalidatePrefix(dirPath)
	c.Delete(dirPath)
	
	logging.Debug("Directory cache invalidated",
		zap.String("dir", dirPath),
		zap.Int("count", count),
	)
	return count
}

// Clear removes all entries from cache
func (c *MetadataCache) Clear() {
	if !c.enabled {
		return
	}

	c.cache.Clear()
	logging.Info("Metadata cache cleared")
}

// Stats returns cache statistics
func (c *MetadataCache) Stats() CacheStats {
	if !c.enabled {
		return CacheStats{}
	}

	return c.cache.Stats()
}

// CleanExpired removes expired entries
func (c *MetadataCache) CleanExpired() int {
	if !c.enabled {
		return 0
	}

	count := c.cache.CleanExpired()
	if count > 0 {
		logging.Debug("Expired metadata entries cleaned", zap.Int("count", count))
	}
	return count
}

// IsEnabled returns whether the cache is enabled
func (c *MetadataCache) IsEnabled() bool {
	return c.enabled
}

// getParentPath returns the parent directory path
func getParentPath(path string) string {
	if path == "/" || path == "" {
		return "/"
	}

	// Remove trailing slash if present
	if path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	// Find last slash
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}

	return "/"
}

// Made with Bob
