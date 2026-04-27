package staging

import (
	"sync"

	"github.com/IBM/ibm-cos-sdk-go/service/s3"
)

// S3MultipartState tracks the state of a progressive S3 multipart upload
type S3MultipartState struct {
	UploadID          string
	PartSize          int64
	NextOffset        int64
	MinModifiedOffset int64
	CompletedParts    []*s3.CompletedPart
	mu                sync.Mutex
	Active            bool
}

// NewS3MultipartState creates a new multipart state tracker
func NewS3MultipartState(partSizeMB int64) *S3MultipartState {
	if partSizeMB < 5 {
		partSizeMB = 20 // IBM COS minimum part size is 5MB, recommended is 20MB+
	}
	return &S3MultipartState{
		PartSize:          partSizeMB * 1024 * 1024,
		NextOffset:        0,
		MinModifiedOffset: 0,
		CompletedParts:    make([]*s3.CompletedPart, 0),
		Active:            false,
	}
}

// MarkModified logs where the user modified the staging file
// Enables detection of random IO (e.g. overwriting already uploaded parts)
func (s *S3MultipartState) MarkModified(offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if offset < s.MinModifiedOffset || s.NextOffset == 0 {
		s.MinModifiedOffset = offset
	}
}

// IsSequential returns true if the staging file modifications are exclusively appends
func (s *S3MultipartState) IsSequential() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MinModifiedOffset >= s.NextOffset
}

// AddCompletedPart saves the ETag from an uploaded part
func (s *S3MultipartState) AddCompletedPart(partNumber int64, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	part := &s3.CompletedPart{
		ETag:       &etag,
		PartNumber: &partNumber,
	}
	s.CompletedParts = append(s.CompletedParts, part)

	// Advance offset (assuming sequential part uploads)
	s.NextOffset += s.PartSize
	s.MinModifiedOffset = s.NextOffset // Reset minimum
}

// GetNextUploadRange returns the byte coordinate bounds for the next logical S3 part
func (s *S3MultipartState) GetNextUploadRange() (int64, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.NextOffset, s.NextOffset + s.PartSize
}

// Reset clears all upload session state after a complete, abort, or restart.
func (s *S3MultipartState) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.UploadID = ""
	s.NextOffset = 0
	s.MinModifiedOffset = 0
	s.CompletedParts = make([]*s3.CompletedPart, 0)
	s.Active = false
}

// Made with Bob
