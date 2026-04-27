package cache

import (
	"container/list"
	"sync"
	"time"
)

// LRUCache implements a thread-safe LRU cache with TTL support
type LRUCache struct {
	maxSize   int
	ttl       time.Duration
	items     map[string]*list.Element
	evictList *list.List
	mu        sync.RWMutex
	onEvict   func(key string, value interface{})
	hits      uint64
	misses    uint64
	evictions uint64
}

// entry represents a cache entry
type entry struct {
	key       string
	value     interface{}
	expiresAt time.Time
}

// NewLRUCache creates a new LRU cache
func NewLRUCache(maxSize int, ttl time.Duration) *LRUCache {
	return &LRUCache{
		maxSize:   maxSize,
		ttl:       ttl,
		items:     make(map[string]*list.Element),
		evictList: list.New(),
	}
}

// Get retrieves a value from the cache
func (c *LRUCache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		c.misses++
		return nil, false
	}

	// Check if expired
	ent := elem.Value.(*entry)
	if time.Now().After(ent.expiresAt) {
		c.removeElement(elem)
		c.misses++
		return nil, false
	}

	// Move to front (most recently used)
	c.evictList.MoveToFront(elem)
	c.hits++
	return ent.value, true
}

// Set adds or updates a value in the cache
func (c *LRUCache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if key already exists
	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		ent := elem.Value.(*entry)
		ent.value = value
		ent.expiresAt = time.Now().Add(c.ttl)
		return
	}

	// Add new entry
	ent := &entry{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	elem := c.evictList.PushFront(ent)
	c.items[key] = elem

	// Evict oldest if necessary
	if c.evictList.Len() > c.maxSize {
		c.evictOldest()
	}
}

// Delete removes a key from the cache
func (c *LRUCache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.removeElement(elem)
		return true
	}
	return false
}

// DeletePrefix removes all keys with the given prefix
func (c *LRUCache) DeletePrefix(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	for key, elem := range c.items {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			c.removeElement(elem)
			count++
		}
	}
	return count
}

// Clear removes all entries from the cache
func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, elem := range c.items {
		if c.onEvict != nil {
			ent := elem.Value.(*entry)
			c.onEvict(key, ent.value)
		}
		delete(c.items, key)
	}
	c.evictList.Init()
}

// Len returns the number of items in the cache
func (c *LRUCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.evictList.Len()
}

// Stats returns cache statistics
func (c *LRUCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := c.hits + c.misses
	hitRate := float64(0)
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}

	return CacheStats{
		Hits:      c.hits,
		Misses:    c.misses,
		Evictions: c.evictions,
		Size:      c.evictList.Len(),
		HitRate:   hitRate,
	}
}

// ResetStats resets cache statistics
func (c *LRUCache) ResetStats() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.hits = 0
	c.misses = 0
	c.evictions = 0
}

// SetEvictCallback sets a callback function to be called when an entry is evicted
func (c *LRUCache) SetEvictCallback(fn func(key string, value interface{})) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEvict = fn
}

// CleanExpired removes all expired entries
func (c *LRUCache) CleanExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	count := 0

	// Iterate from back (oldest) to front
	for elem := c.evictList.Back(); elem != nil; {
		ent := elem.Value.(*entry)
		if now.After(ent.expiresAt) {
			prev := elem.Prev()
			c.removeElement(elem)
			count++
			elem = prev
		} else {
			// Since list is ordered by access time, we can stop here
			break
		}
	}

	return count
}

// EvictOldest removes the least recently used entry.
func (c *LRUCache) EvictOldest() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.evictList.Len() == 0 {
		return false
	}
	c.evictOldest()
	return true
}

// removeElement removes an element from the cache (must be called with lock held)
func (c *LRUCache) removeElement(elem *list.Element) {
	c.evictList.Remove(elem)
	ent := elem.Value.(*entry)
	delete(c.items, ent.key)

	if c.onEvict != nil {
		c.onEvict(ent.key, ent.value)
	}
}

// evictOldest removes the oldest entry from the cache (must be called with lock held)
func (c *LRUCache) evictOldest() {
	elem := c.evictList.Back()
	if elem != nil {
		c.removeElement(elem)
		c.evictions++
	}
}

// CacheStats represents cache statistics
type CacheStats struct {
	Hits      uint64
	Misses    uint64
	Evictions uint64
	Size      int
	HitRate   float64
}

// Made with Bob
