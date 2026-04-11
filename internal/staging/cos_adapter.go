package staging

import (
	"context"
	"io"
	"time"

	"github.com/oborges/cos-nfs-gateway/internal/cos"
	"github.com/oborges/cos-nfs-gateway/internal/posix"
	"github.com/oborges/cos-nfs-gateway/pkg/types"
)

// COSClientAdapter adapts the COS client to the COSClient interface required by SyncWorker
type COSClientAdapter struct {
	Client *cos.Client
}

// PutObject uploads data to COS
func (a *COSClientAdapter) PutObject(ctx context.Context, key string, data []byte, metadata map[string]string) error {
	// Create POSIX attributes for the file
	now := time.Now()
	attrs := &types.POSIXAttributes{
		Mode:  0644,
		UID:   1000,
		GID:   1000,
		Atime: now,
		Mtime: now,
		Ctime: now,
	}
	
	// Encode attributes to metadata
	cosMetadata := posix.EncodePOSIXAttributes(attrs)
	
	// Merge with any additional metadata provided
	for k, v := range metadata {
		cosMetadata[k] = v
	}
	
	// Upload to COS
	return a.Client.PutObject(ctx, key, data, cosMetadata)
}

// PutObjectStream uploads an object stream to COS
func (a *COSClientAdapter) PutObjectStream(ctx context.Context, key string, body io.ReadSeeker, metadata map[string]string) error {
	// Create POSIX attributes for the file
	now := time.Now()
	attrs := &types.POSIXAttributes{
		Mode:  0644,
		UID:   1000,
		GID:   1000,
		Atime: now,
		Mtime: now,
		Ctime: now,
	}
	
	cosMetadata := posix.EncodePOSIXAttributes(attrs)
	for k, v := range metadata {
		cosMetadata[k] = v
	}
	
	return a.Client.PutObjectStream(ctx, key, body, cosMetadata)
}

// GetObjectStream downloads an object stream from COS
func (a *COSClientAdapter) GetObjectStream(ctx context.Context, key string) (io.ReadCloser, error) {
	return a.Client.GetObjectStream(ctx, key)
}

// Made with Bob
