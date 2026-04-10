# IBM Cloud COS NFS Gateway - Implementation Guide

## Project Structure

```
ibm-cos-nfs-gateway/
├── cmd/
│   └── nfs-gateway/
│       └── main.go                 # Application entry point
├── internal/
│   ├── config/
│   │   ├── config.go              # Configuration management
│   │   └── validation.go          # Config validation
│   ├── cos/
│   │   ├── client.go              # IBM Cloud COS client wrapper
│   │   ├── operations.go          # COS operations (get, put, delete, list)
│   │   ├── multipart.go           # Multipart upload handling
│   │   └── auth.go                # IAM authentication
│   ├── nfs/
│   │   ├── server.go              # NFS server implementation
│   │   ├── handler.go             # NFS request handler
│   │   └── filesystem.go          # Virtual filesystem interface
│   ├── posix/
│   │   ├── operations.go          # POSIX operation mapping
│   │   ├── attributes.go          # File attribute handling
│   │   └── permissions.go         # Permission management
│   ├── cache/
│   │   ├── metadata.go            # Metadata cache
│   │   ├── data.go                # Data cache
│   │   └── lru.go                 # LRU eviction policy
│   ├── lock/
│   │   ├── manager.go             # Lock manager
│   │   └── distributed.go         # Distributed locking
│   ├── metrics/
│   │   ├── prometheus.go          # Prometheus metrics
│   │   └── collector.go           # Custom collectors
│   ├── health/
│   │   └── checker.go             # Health check handlers
│   └── logging/
│       └── logger.go              # Structured logging
├── pkg/
│   └── types/
│       └── types.go               # Shared types and interfaces
├── deployments/
│   ├── docker/
│   │   ├── Dockerfile
│   │   └── docker-compose.yml
│   └── kubernetes/
│       ├── deployment.yaml
│       ├── service.yaml
│       ├── configmap.yaml
│       ├── secret.yaml
│       └── statefulset.yaml
├── configs/
│   ├── config.yaml                # Default configuration
│   └── config.example.yaml        # Example configuration
├── scripts/
│   ├── build.sh                   # Build script
│   ├── test.sh                    # Test script
│   └── deploy.sh                  # Deployment script
├── docs/
│   ├── API.md                     # API documentation
│   ├── DEPLOYMENT.md              # Deployment guide
│   ├── TROUBLESHOOTING.md         # Troubleshooting guide
│   └── PERFORMANCE.md             # Performance tuning guide
├── test/
│   ├── integration/               # Integration tests
│   └── e2e/                       # End-to-end tests
├── go.mod
├── go.sum
├── Makefile
├── README.md
├── LICENSE
└── .gitignore
```

## Implementation Phases

### Phase 1: Foundation Setup

#### 1.1 Initialize Go Project
```bash
# Initialize Go module
go mod init github.com/ibm-cloud/cos-nfs-gateway

# Add core dependencies
go get github.com/IBM/ibm-cos-sdk-go
go get github.com/willscott/go-nfs
go get github.com/spf13/viper
go get github.com/prometheus/client_golang
go get go.uber.org/zap
go get github.com/stretchr/testify
```

#### 1.2 Configuration Management
**File**: [`internal/config/config.go`](internal/config/config.go)

Key features:
- YAML configuration file support
- Environment variable overrides
- Configuration validation
- Hot-reload capability

```go
type Config struct {
    Server     ServerConfig
    COS        COSConfig
    Cache      CacheConfig
    Performance PerformanceConfig
    Logging    LoggingConfig
}
```

#### 1.3 Logging Setup
**File**: [`internal/logging/logger.go`](internal/logging/logger.go)

Features:
- Structured JSON logging
- Log levels (DEBUG, INFO, WARN, ERROR)
- Request correlation IDs
- Performance logging

### Phase 2: IBM Cloud COS Integration

#### 2.1 COS Client Wrapper
**File**: [`internal/cos/client.go`](internal/cos/client.go)

Responsibilities:
- Initialize IBM Cloud COS SDK
- Handle IAM authentication
- Manage connection pooling
- Implement retry logic

Key methods:
```go
func NewCOSClient(config COSConfig) (*COSClient, error)
func (c *COSClient) GetObject(key string) ([]byte, error)
func (c *COSClient) PutObject(key string, data []byte) error
func (c *COSClient) DeleteObject(key string) error
func (c *COSClient) ListObjects(prefix string) ([]Object, error)
func (c *COSClient) HeadObject(key string) (*ObjectMetadata, error)
```

#### 2.2 Authentication
**File**: [`internal/cos/auth.go`](internal/cos/auth.go)

Support:
- IAM API Key authentication
- HMAC credentials
- Token refresh mechanism
- Credential rotation

#### 2.3 Multipart Upload
**File**: [`internal/cos/multipart.go`](internal/cos/multipart.go)

Features:
- Automatic multipart for large files
- Configurable chunk size
- Parallel upload workers
- Resume capability

### Phase 3: NFS Server Implementation

#### 3.1 NFS Server Core
**File**: [`internal/nfs/server.go`](internal/nfs/server.go)

Based on: `github.com/willscott/go-nfs`

Key components:
```go
type NFSServer struct {
    listener net.Listener
    handler  nfs.Handler
    config   ServerConfig
}

func NewNFSServer(config ServerConfig, fs FileSystem) (*NFSServer, error)
func (s *NFSServer) Start() error
func (s *NFSServer) Stop() error
```

#### 3.2 Virtual Filesystem Interface
**File**: [`internal/nfs/filesystem.go`](internal/nfs/filesystem.go)

Implements NFS handler interface:
```go
type FileSystem interface {
    // File operations
    Open(path string, flags int) (File, error)
    Create(path string, mode os.FileMode) (File, error)
    Remove(path string) error
    Rename(oldPath, newPath string) error
    
    // Directory operations
    Mkdir(path string, mode os.FileMode) error
    Rmdir(path string) error
    ReadDir(path string) ([]os.FileInfo, error)
    
    // Metadata operations
    Stat(path string) (os.FileInfo, error)
    Chmod(path string, mode os.FileMode) error
    Chown(path string, uid, gid int) error
    Chtimes(path string, atime, mtime time.Time) error
}
```

### Phase 4: POSIX Operations Mapping

#### 4.1 Operation Handler
**File**: [`internal/posix/operations.go`](internal/posix/operations.go)

Maps POSIX operations to COS operations:

| POSIX Operation | COS Operation | Notes |
|----------------|---------------|-------|
| `open()`/`read()` | `GetObject()` | With caching |
| `write()`/`close()` | `PutObject()` | With buffering |
| `unlink()` | `DeleteObject()` | Immediate |
| `rename()` | `CopyObject()` + `DeleteObject()` | Not atomic |
| `mkdir()` | `PutObject()` with `/` suffix | Marker object |
| `rmdir()` | `DeleteObject()` | Check empty first |
| `readdir()` | `ListObjects()` | With prefix |
| `stat()` | `HeadObject()` | Cached |

#### 4.2 Attribute Mapping
**File**: [`internal/posix/attributes.go`](internal/posix/attributes.go)

Store POSIX attributes in COS metadata:
```go
type POSIXAttributes struct {
    Mode     os.FileMode  // Stored in x-amz-meta-mode
    UID      int          // Stored in x-amz-meta-uid
    GID      int          // Stored in x-amz-meta-gid
    Atime    time.Time    // Stored in x-amz-meta-atime
    Mtime    time.Time    // From LastModified
    Ctime    time.Time    // From LastModified
}
```

#### 4.3 Path Translation
Convert filesystem paths to COS object keys:
- `/` → bucket root
- `/dir/file.txt` → `dir/file.txt`
- `/dir/` → `dir/` (directory marker)

### Phase 5: Caching Layer

#### 5.1 Metadata Cache
**File**: [`internal/cache/metadata.go`](internal/cache/metadata.go)

Features:
- LRU eviction policy
- TTL-based expiration
- Cache invalidation on writes
- Thread-safe operations

```go
type MetadataCache struct {
    cache *lru.Cache
    ttl   time.Duration
    mu    sync.RWMutex
}

func (c *MetadataCache) Get(key string) (*Metadata, bool)
func (c *MetadataCache) Set(key string, metadata *Metadata)
func (c *MetadataCache) Invalidate(key string)
func (c *MetadataCache) InvalidatePrefix(prefix string)
```

#### 5.2 Data Cache
**File**: [`internal/cache/data.go`](internal/cache/data.go)

Features:
- Disk-based storage
- Chunk-based caching
- Read-ahead prefetching
- Write buffering

```go
type DataCache struct {
    basePath  string
    maxSize   int64
    currentSize int64
    lru       *lru.Cache
    mu        sync.RWMutex
}

func (c *DataCache) Read(key string, offset, length int64) ([]byte, error)
func (c *DataCache) Write(key string, offset int64, data []byte) error
func (c *DataCache) Evict(key string) error
```

### Phase 6: File Locking

#### 6.1 Lock Manager
**File**: [`internal/lock/manager.go`](internal/lock/manager.go)

Features:
- Advisory locks (flock, fcntl)
- Shared and exclusive locks
- Lock timeout and lease renewal
- Deadlock detection

```go
type LockManager struct {
    locks map[string]*Lock
    mu    sync.RWMutex
}

type Lock struct {
    Type      LockType  // Shared or Exclusive
    Owner     string
    ExpiresAt time.Time
}

func (m *LockManager) AcquireLock(path string, lockType LockType, timeout time.Duration) error
func (m *LockManager) ReleaseLock(path string, owner string) error
func (m *LockManager) RenewLock(path string, owner string) error
```

#### 6.2 Distributed Locking
**File**: [`internal/lock/distributed.go`](internal/lock/distributed.go)

For multi-instance deployments:
- Use COS object metadata for lock state
- Implement lease-based locking
- Handle lock expiration and cleanup

### Phase 7: Monitoring & Observability

#### 7.1 Prometheus Metrics
**File**: [`internal/metrics/prometheus.go`](internal/metrics/prometheus.go)

Key metrics:
```go
// Request metrics
nfs_requests_total{operation, status}
nfs_request_duration_seconds{operation}

// COS metrics
cos_api_calls_total{operation, status}
cos_api_duration_seconds{operation}

// Cache metrics
cache_hits_total{cache_type}
cache_misses_total{cache_type}
cache_size_bytes{cache_type}
cache_evictions_total{cache_type}

// Performance metrics
bytes_read_total
bytes_written_total
active_connections
```

#### 7.2 Health Checks
**File**: [`internal/health/checker.go`](internal/health/checker.go)

Endpoints:
- `/health/live` - Liveness probe
- `/health/ready` - Readiness probe
- `/health/startup` - Startup probe

Checks:
- NFS server running
- COS connectivity
- Cache availability
- Disk space

### Phase 8: Containerization

#### 8.1 Dockerfile
**File**: [`deployments/docker/Dockerfile`](deployments/docker/Dockerfile)

Multi-stage build:
```dockerfile
# Build stage
FROM golang:1.21-alpine AS builder
WORKDIR /build
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o nfs-gateway ./cmd/nfs-gateway

# Runtime stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /build/nfs-gateway .
COPY configs/config.yaml /etc/nfs-gateway/config.yaml
EXPOSE 2049 8080 8081
CMD ["./nfs-gateway"]
```

#### 8.2 Kubernetes Deployment
**File**: [`deployments/kubernetes/statefulset.yaml`](deployments/kubernetes/statefulset.yaml)

Key features:
- StatefulSet for stable network identity
- Persistent volume for cache
- ConfigMap for configuration
- Secret for credentials
- Service for load balancing
- HPA for auto-scaling

### Phase 9: Testing Strategy

#### 9.1 Unit Tests
Coverage targets:
- COS client operations: 90%
- POSIX mapping: 85%
- Cache operations: 90%
- Lock manager: 85%

#### 9.2 Integration Tests
**Directory**: [`test/integration/`](test/integration/)

Test scenarios:
- COS connectivity and authentication
- Basic file operations
- Directory operations
- Concurrent access
- Cache behavior
- Lock acquisition and release

#### 9.3 Performance Tests
Benchmarks:
- Sequential read/write throughput
- Random read/write IOPS
- Metadata operation latency
- Cache hit ratio
- Concurrent client scalability

### Phase 10: Documentation

#### 10.1 User Guide
**File**: [`docs/USER_GUIDE.md`](docs/USER_GUIDE.md)

Contents:
- Installation instructions
- Configuration guide
- Mounting the filesystem
- Best practices
- Common use cases

#### 10.2 API Documentation
**File**: [`docs/API.md`](docs/API.md)

Contents:
- Configuration API
- Metrics API
- Health check API
- Management API

#### 10.3 Troubleshooting Guide
**File**: [`docs/TROUBLESHOOTING.md`](docs/TROUBLESHOOTING.md)

Contents:
- Common issues and solutions
- Performance tuning
- Debugging techniques
- Log analysis

## Development Workflow

### Local Development
```bash
# Clone repository
git clone https://github.com/ibm-cloud/cos-nfs-gateway.git
cd cos-nfs-gateway

# Install dependencies
go mod download

# Run tests
make test

# Build binary
make build

# Run locally
./bin/nfs-gateway --config configs/config.yaml
```

### Testing
```bash
# Unit tests
make test-unit

# Integration tests
make test-integration

# Performance tests
make test-performance

# Coverage report
make coverage
```

### Building Container
```bash
# Build Docker image
make docker-build

# Push to registry
make docker-push

# Deploy to Kubernetes
make k8s-deploy
```

## Key Dependencies

### Core Libraries
- `github.com/IBM/ibm-cos-sdk-go` - IBM Cloud COS SDK
- `github.com/willscott/go-nfs` - NFS server implementation
- `github.com/aws/aws-sdk-go` - S3 compatibility layer

### Configuration & CLI
- `github.com/spf13/viper` - Configuration management
- `github.com/spf13/cobra` - CLI framework

### Monitoring & Logging
- `github.com/prometheus/client_golang` - Prometheus metrics
- `go.uber.org/zap` - Structured logging

### Utilities
- `github.com/hashicorp/golang-lru` - LRU cache
- `golang.org/x/sync/errgroup` - Goroutine management
- `github.com/stretchr/testify` - Testing framework

## Performance Considerations

### Optimization Strategies
1. **Aggressive Caching**
   - Cache metadata for 60 seconds
   - Cache frequently accessed files
   - Implement read-ahead for sequential access

2. **Connection Pooling**
   - Maintain persistent connections to COS
   - Configure appropriate pool size
   - Implement connection health checks

3. **Parallel Operations**
   - Use goroutines for concurrent requests
   - Implement worker pools
   - Batch operations where possible

4. **Efficient Data Transfer**
   - Use multipart uploads for large files
   - Stream data instead of buffering
   - Compress data in transit

### Benchmarking Targets
- Sequential read: 100+ MB/s
- Sequential write: 50+ MB/s
- Random read IOPS: 1000+
- Random write IOPS: 500+
- Metadata operations: <100ms (cached)
- Cache hit ratio: >80%

## Security Best Practices

### Credential Management
- Use IBM Cloud IAM service IDs
- Store credentials in Kubernetes secrets
- Implement credential rotation
- Never log credentials

### Network Security
- Deploy in private network
- Use security groups
- Enable VPC isolation
- Consider VPN for remote access

### Access Control
- Implement IP-based restrictions
- Use NFS export controls
- Map UIDs/GIDs appropriately
- Audit access logs

## Next Steps

After completing the planning phase, proceed to implementation:

1. Set up development environment
2. Implement core COS client
3. Build NFS server foundation
4. Add POSIX operation mapping
5. Implement caching layer
6. Add monitoring and logging
7. Create deployment artifacts
8. Write comprehensive tests
9. Document everything
10. Deploy and validate

## Success Metrics

### Technical Metrics
- Code coverage: >80%
- Performance benchmarks met
- Zero critical security issues
- All tests passing

### Operational Metrics
- Successful deployment to Kubernetes
- Health checks passing
- Metrics being collected
- Logs being generated

### User Metrics
- Successful mount on VSI
- File operations working
- Performance acceptable
- Documentation clear