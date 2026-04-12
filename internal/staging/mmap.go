package staging

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
)

// MMapReader provides a read-only memory-mapped interface to a file chunk
// It enables zero-copy reads from disk directly into Go's address bounds bypassing generic IO syscalls.
type MMapReader struct {
	data   []byte
	reader *bytes.Reader
}

// NewMMapReader creates a memory-mapped section from a file.
// IMPORTANT: 'offset' parameter intrinsically must map cleanly to Linux `min(PageSize)` multipliers!
func NewMMapReader(file *os.File, offset int64, length int64) (*MMapReader, error) {
	if length <= 0 {
		return nil, fmt.Errorf("length must be greater than zero")
	}

	// syscall.Mmap securely loads physical ext4 logic into byte representations natively across Kernel thresholds!
	data, err := syscall.Mmap(int(file.Fd()), offset, int(length), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap failed natively: %w", err)
	}

	return &MMapReader{
		data:   data,
		reader: bytes.NewReader(data),
	}, nil
}

// Read implements io.Reader
func (m *MMapReader) Read(p []byte) (n int, err error) {
	return m.reader.Read(p)
}

// Seek implements io.Seeker seamlessly integrating natively alongside standard interfaces
func (m *MMapReader) Seek(offset int64, whence int) (int64, error) {
	return m.reader.Seek(offset, whence)
}

// Close unmaps the memory-mapped system arrays cleanly freeing underlying kernels structures dynamically
func (m *MMapReader) Close() error {
	if m.data != nil {
		err := syscall.Munmap(m.data)
		m.data = nil
		return err
	}
	return nil
}
