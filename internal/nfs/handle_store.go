package nfs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// HandleStore provides persistent storage for file handle mappings
type HandleStore struct {
	storePath string
	handles   map[string][]string
	mu        sync.RWMutex
}

// NewHandleStore creates a new handle store
func NewHandleStore(storePath string) (*HandleStore, error) {
	store := &HandleStore{
		storePath: storePath,
		handles:   make(map[string][]string),
	}
	
	// Create store directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(storePath), 0700); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}
	
	// Load existing handles
	if err := store.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load handles: %w", err)
	}
	
	return store, nil
}

// Store saves a handle mapping
func (s *HandleStore) Store(hash string, path []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.handles[hash] = path
	return s.save()
}

// Get retrieves a path from a handle hash
func (s *HandleStore) Get(hash string) ([]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	path, ok := s.handles[hash]
	return path, ok
}

// Delete removes a handle mapping
func (s *HandleStore) Delete(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	delete(s.handles, hash)
	return s.save()
}

// GenerateHash creates a deterministic hash from a path
func (s *HandleStore) GenerateHash(path []string) string {
	pathStr := strings.Join(path, "/")
	hash := sha256.Sum256([]byte(pathStr))
	return hex.EncodeToString(hash[:])
}

// load reads handles from disk
func (s *HandleStore) load() error {
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		return err
	}
	
	return json.Unmarshal(data, &s.handles)
}

// save writes handles to disk
func (s *HandleStore) save() error {
	data, err := json.MarshalIndent(s.handles, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(s.storePath, data, 0600)
}

// Made with Bob
