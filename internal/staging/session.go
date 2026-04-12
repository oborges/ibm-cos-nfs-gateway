package staging

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// WriteSession represents an active write session for a file path
// Sessions are path-scoped and survive file handle open/close cycles
type WriteSession struct {
	Path        string
	StagingPath string
	File        *os.File
	Size        int64
	Dirty       bool
	Prefetched  bool
	RefCount    int32
	LastWrite   time.Time
	LastAccess  time.Time
	CreatedAt   time.Time
	Multipart   *S3MultipartState
	mu          sync.Mutex
}

// NewWriteSession creates a new write session
func NewWriteSession(path string, stagingPath string) (*WriteSession, error) {
	// Open or create staging file
	file, err := os.OpenFile(stagingPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open staging file: %w", err)
	}

	// Get current size
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat staging file: %w", err)
	}

	now := time.Now()
	return &WriteSession{
		Path:        path,
		StagingPath: stagingPath,
		File:        file,
		Size:        stat.Size(),
		Dirty:       false,
		Prefetched  : false,
		RefCount:    1,
		LastWrite:   now,
		LastAccess:  now,
		CreatedAt:   now,
		Multipart:   NewS3MultipartState(20), // Default 20MB part chunks
	}, nil
}

// Write writes data to the staging file at the specified offset
func (ws *WriteSession) Write(data []byte, offset int64) (int, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Seek to offset
	if _, err := ws.File.Seek(offset, 0); err != nil {
		return 0, fmt.Errorf("failed to seek: %w", err)
	}

	// Write data
	n, err := ws.File.Write(data)
	if err != nil {
		return n, fmt.Errorf("failed to write: %w", err)
	}

	// Update size
	newSize := offset + int64(n)
	if newSize > ws.Size {
		ws.Size = newSize
	}

	ws.Multipart.MarkModified(offset)

	now := time.Now()
	ws.Dirty = true
	ws.LastWrite = now
	ws.LastAccess = now

	return n, nil
}

// Read reads data from the staging file at the specified offset
func (ws *WriteSession) Read(buffer []byte, offset int64) (int, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Check bounds
	if offset >= ws.Size {
		return 0, io.EOF // Return EOF when at or past end of file
	}

	// Seek to offset
	if _, err := ws.File.Seek(offset, 0); err != nil {
		return 0, fmt.Errorf("failed to seek: %w", err)
	}

	// Read data
	n, err := ws.File.Read(buffer)
	if err != nil && err != io.EOF {
		return 0, fmt.Errorf("failed to read: %w", err)
	}

	ws.LastAccess = time.Now()

	// If we read some data but hit EOF, return the data with nil error
	// The next read will return 0, io.EOF
	if err == io.EOF && n > 0 {
		return n, nil
	}

	return n, err
}

// Sync flushes the staging file to disk
func (ws *WriteSession) Sync() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if err := ws.File.Sync(); err != nil {
		return fmt.Errorf("failed to sync: %w", err)
	}

	return nil
}

// Close closes the staging file
func (ws *WriteSession) Close() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.File != nil {
		if err := ws.File.Close(); err != nil {
			return fmt.Errorf("failed to close: %w", err)
		}
		ws.File = nil
	}

	return nil
}

// GetSize returns the current file size
func (ws *WriteSession) GetSize() int64 {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.Size
}

// IncrementRefCount increments the reference count
func (ws *WriteSession) IncrementRefCount() {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	ws.RefCount++
	ws.LastAccess = time.Now()
}

// DecrementRefCount decrements the reference count
func (ws *WriteSession) DecrementRefCount() {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.RefCount > 0 {
		ws.RefCount--
	}
	ws.LastAccess = time.Now()
}

// GetRefCount returns the current reference count
func (ws *WriteSession) GetRefCount() int32 {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.RefCount
}

// Truncate truncates the staging file to the specified size
func (ws *WriteSession) Truncate(size int64) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Truncate the file
	if err := ws.File.Truncate(size); err != nil {
		return fmt.Errorf("failed to truncate: %w", err)
	}

	// Update size and mark as dirty
	ws.Size = size
	ws.Dirty = true
	ws.LastWrite = time.Now()
	ws.LastAccess = time.Now()

	return nil
}

// Prefetch runs the provided fetch function exactly once.
func (ws *WriteSession) Prefetch(fetcher func() error) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.Prefetched {
		return nil
	}

	if err := fetcher(); err != nil {
		return err
	}

	if stat, err := ws.File.Stat(); err == nil {
		ws.Size = stat.Size()
	}
	
	ws.Prefetched = true
	return nil
}

// Made with Bob
