package posix

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/oborges/cos-nfs-gateway/pkg/types"
)

// downObjectStore simulates an unreachable object store: every call fails
// with a non-not-found error.
type downObjectStore struct{}

var errBackendDown = errors.New("dial tcp: connection refused")

func (downObjectStore) GetObject(context.Context, string) ([]byte, error) {
	return nil, errBackendDown
}
func (downObjectStore) GetObjectRange(context.Context, string, int64, int64) ([]byte, error) {
	return nil, errBackendDown
}
func (downObjectStore) GetObjectStream(context.Context, string) (io.ReadCloser, error) {
	return nil, errBackendDown
}
func (downObjectStore) PutObject(context.Context, string, []byte, map[string]string) error {
	return errBackendDown
}
func (downObjectStore) DeleteObject(context.Context, string) error { return errBackendDown }
func (downObjectStore) HeadObject(context.Context, string) (*types.ObjectMetadata, error) {
	return nil, errBackendDown
}
func (downObjectStore) ListObjects(context.Context, string, int) ([]*types.ObjectMetadata, error) {
	return nil, errBackendDown
}
func (downObjectStore) CopyObject(context.Context, string, string) error { return errBackendDown }
func (downObjectStore) UpdateObjectMetadata(context.Context, string, map[string]string) error {
	return errBackendDown
}

func TestStatDuringBackendOutage(t *testing.T) {
	ctx := context.Background()
	ops, _ := newRefreshTestOps(t, nil)
	// Swap in the failing store after construction.
	ops.cosClient = downObjectStore{}

	// A backend outage must surface as an error, never as false ENOENT.
	_, err := ops.Stat(ctx, "/some-file.txt")
	if err == nil {
		t.Fatal("Stat() during outage returned success")
	}
	if os.IsNotExist(err) {
		t.Fatal("Stat() during outage reported ENOENT; must report an I/O error instead")
	}

	// The export root exists by definition, even with the backend down.
	info, err := ops.Stat(ctx, "/")
	if err != nil {
		t.Fatalf("Stat(/) during outage error = %v, want synthetic root", err)
	}
	if !info.IsDir() {
		t.Fatal("Stat(/) must report a directory")
	}
}

func TestStatMissingFileStillENOENT(t *testing.T) {
	ctx := context.Background()
	store := newFakeObjectStore()
	ops, _ := newRefreshTestOps(t, store)

	// With a healthy backend, a genuinely missing path stays ENOENT.
	if _, err := ops.Stat(ctx, "/definitely-missing.txt"); !os.IsNotExist(err) {
		t.Fatalf("Stat(missing) error = %v, want ENOENT", err)
	}
}

// Made with Bob
