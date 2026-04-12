# IBM Cloud COS NFS Gateway

> ⚠️ **DISCLAIMER**: This is an **unofficial, community-driven hobby project** and is **NOT affiliated with, endorsed by, or supported by IBM Corporation**. This software is provided "as-is" without any warranty. Use at your own risk.

A high-performance NFS v3 gateway that provides POSIX filesystem access to IBM Cloud Object Storage (COS). This solution enables legacy applications and workflows to seamlessly use cloud object storage through standard NFS mounts.

## ⚠️ Important Notices

- **NOT AN IBM PRODUCT**: This is a personal hobby project, not an official IBM solution
- **NO WARRANTY**: Provided "as-is" without warranties of any kind
- **NO SUPPORT**: No official support is provided - use at your own risk
- **EXPERIMENTAL**: This is experimental software - thoroughly test before any production use
- **YOUR RESPONSIBILITY**: You are solely responsible for any data loss, costs, or issues
- **NOT FOR PRODUCTION**: Not recommended for production workloads without extensive testing

## 🌟 Features

- **NFSv3 Protocol Support**: Full NFSv3 implementation using go-nfs library
- **IBM Cloud COS Backend**: Transparent object storage integration
- **High Performance Caching**: Multi-tier staging layer avoiding Out Of Memory logic dynamically pushing max chunk boundaries preserving limits natively!
- **Zero-Copy MMap Optimization**: Eliminates networking overhead by Memory-Mapping Linux Page Caches direct out to HTTP bindings.
- **Progressive S3 Multipart**: Massive isolated gigabyte streaming chunk pipelines uploaded organically concurrently intercepting POSIX sequences!
- **Strict Hardware OS Quotas**: Transparent disk metrics mapping Native `ENOSPC` bounds halting scaling connections preventing underlying memory faults securely!
- **POSIX Semantics**: Preserves active local caching sequences maintaining mapping validations synchronously.

## 📋 Prerequisites

- Go 1.21 or higher
- IBM Cloud account with Cloud Object Storage service
- Linux system with NFS utilities
- (Optional) Docker for containerized deployment
- (Optional) Kubernetes cluster for production deployment

## 🚀 Quick Start

### 1. Clone the Repository

```bash
git clone https://github.com/oborges/ibm-cos-nfs-gateway.git
cd ibm-cos-nfs-gateway
```

### 2. Configure

Create your configuration file:

```bash
cp configs/config.example.yaml configs/config.yaml
```

Edit `configs/config.yaml` with your IBM Cloud COS credentials:

```yaml
cos:
  endpoint: "s3.us-south.cloud-object-storage.appdomain.cloud"
  region: "us-south"
  bucket: "your-bucket-name"
  api_key: "your-api-key"
  service_instance_id: "your-service-instance-id"
```

### 3. Build

```bash
make build
```

### 4. Run

```bash
sudo ./bin/nfs-gateway -config configs/config.yaml
```

### 5. Mount

```bash
sudo mkdir -p /mnt/cos-nfs
sudo mount -t nfs -o vers=3,tcp localhost:/ /mnt/cos-nfs
```

## 📖 Documentation

- [Quick Start Guide](docs/QUICKSTART.md) - Detailed setup instructions
- [Architecture](ARCHITECTURE.md) - System design and components
- [Gateway Staging Structuring](docs/STAGING_ARCHITECTURE.md) - Deep dive resolving active File constraints scaling S3 mapping limits organically!
- [Enterprise Dashboard Benchmarks](docs/BENCHMARKING.md) - FIO Stress FIO implementations parsing Hardware validation tracking.

## 🏗️ Architecture

```
┌─────────────┐
│ NFS Clients │
└──────┬──────┘
       │ NFSv3
       ▼
┌─────────────────────────────────────┐
│     NFS Gateway (Go)                │
│  ┌──────────┐  ┌─────────────────┐ │
│  │   NFS    │  │  POSIX Ops      │ │
│  │  Server  │──│  Handler        │ │
│  └──────────┘  └─────────────────┘ │
│  ┌──────────┐  ┌─────────────────┐ │
│  │  Cache   │  │  Lock Manager   │ │
│  │  Layer   │  │                 │ │
│  └──────────┘  └─────────────────┘ │
└───────────────┬─────────────────────┘
                │ S3 API
                ▼
        ┌───────────────┐
        │  IBM Cloud    │
        │     COS       │
        └───────────────┘
```

## 🔧 Configuration

Key configuration options:

```yaml
server:
  nfs_port: 2049          # NFS server port
  metrics_port: 9090      # Prometheus metrics
  health_port: 8080       # Health checks

cache:
  metadata:
    enabled: true
    max_size: 10000       # Max cached entries
    ttl: 300s             # Cache TTL
  data:
    enabled: true
    max_size_mb: 1024     # Max cache size
    directory: "/tmp/nfs-cache"

performance:
  multipart_threshold_mb: 100
  multipart_chunk_size_mb: 10
  max_concurrent_uploads: 4
```

## 🐳 Docker Deployment

```bash
# Build image
docker build -t cos-nfs-gateway -f deployments/docker/Dockerfile .

# Run container
docker run -d \
  --name nfs-gateway \
  --cap-add SYS_ADMIN \
  --device /dev/fuse \
  -p 2049:2049 \
  -p 9090:9090 \
  -p 8080:8080 \
  -v $(pwd)/configs:/app/configs \
  cos-nfs-gateway
```

Or use Docker Compose:

```bash
cd deployments/docker
docker-compose up -d
```

## ☸️ Kubernetes Deployment

```bash
# Create namespace
kubectl create namespace nfs-gateway

# Apply manifests
kubectl apply -f deployments/kubernetes/

# Check status
kubectl get pods -n nfs-gateway
kubectl logs -f deployment/nfs-gateway -n nfs-gateway
```

## 📊 Monitoring

### Health Checks

```bash
# Liveness probe
curl http://localhost:8080/health

# Readiness probe
curl http://localhost:8080/ready
```

### Metrics

Prometheus metrics available at `http://localhost:9090/metrics`:

- `nfs_requests_total` - Total NFS requests
- `nfs_request_duration_seconds` - Request latency
- `cos_api_calls_total` - COS API calls
- `cache_hits_total` - Cache hit count
- `cache_misses_total` - Cache miss count
- `bytes_read_total` - Total bytes read
- `bytes_written_total` - Total bytes written

## 🧪 Testing

### Unit Tests

```bash
# Run unit tests
make test

# Run with coverage
make test-coverage

# Run specific test
go test -v ./internal/posix -run TestPathTranslation
```

### Performance/Stress Testing

```bash
# Quick performance check (5 minutes)
./scripts/quick_test.sh

# Comprehensive stress test suite (15-20 minutes)
./scripts/run_stress_tests.sh

# Manual fio tests
fio --name=test --directory=/mnt/cos-nfs --rw=write --bs=1M --size=100M
```

See [Stress Testing Guide](docs/STRESS_TESTING_GUIDE.md) for detailed testing procedures and performance targets.

## 🔒 Security Considerations

- Store API keys securely (use Kubernetes secrets in production)
- Use private endpoints when possible
- Enable SSL/TLS for COS connections
- Implement network policies in Kubernetes
- Regular security updates and patches

## 🚦 Performance Tuning

For optimal performance:

1. **Increase cache sizes** for frequently accessed data
2. **Adjust multipart settings** based on file sizes
3. **Use private endpoints** to reduce latency
4. **Enable chunk cache** for read-heavy workloads
5. **Tune TTL values** based on data freshness requirements
6. **Optimize NFS mount options**: Use `rsize=1048576,wsize=1048576` for better throughput
7. **Run stress tests** to validate performance meets your requirements

### Performance Targets

| Metric | Target | Acceptable |
|--------|--------|------------|
| Sequential Read | >100 MB/s | >50 MB/s |
| Sequential Write | >50 MB/s | >20 MB/s |
| Random Read IOPS | >200 | >100 |
| Random Write IOPS | >100 | >50 |

Run `./scripts/quick_test.sh` to validate your deployment meets these targets.

## 🐛 Troubleshooting

### Gateway won't start
```bash
# Check logs
journalctl -u nfs-gateway -n 50

# Verify port availability
sudo ss -tlnp | grep 2049

# Test COS connectivity
curl -I https://s3.us-south.cloud-object-storage.appdomain.cloud
```

### Mount fails
```bash
# Check NFS server status
systemctl status nfs-gateway

# Try verbose mount
sudo mount -t nfs -o vers=3,tcp,v localhost:/ /mnt/cos-nfs

# Check firewall
sudo firewall-cmd --list-ports
```

### Files not appearing in COS
```bash
# Check gateway logs
journalctl -u nfs-gateway | grep -i error

# Verify COS credentials
# Check metrics for failed operations
curl http://localhost:9090/metrics | grep cos_api_calls_total
```

## 🤝 Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## 📝 License

This project is licensed under the MIT License - see the LICENSE file for details.

**DISCLAIMER**: This software is provided "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied. In no event shall the authors or copyright holders be liable for any claim, damages or other liability arising from the use of this software.

## 👥 Authors

- **Olavo Borges** - Personal hobby project - [@oborges](https://github.com/oborges)

## 🙏 Acknowledgments

- [go-nfs](https://github.com/willscott/go-nfs) - NFS server implementation
- [go-billy](https://github.com/go-git/go-billy) - Filesystem abstraction
- IBM Cloud Object Storage (this project is NOT affiliated with IBM)

## 📞 Support

**NO OFFICIAL SUPPORT PROVIDED** - This is a hobby project.

For community help:
- Open an issue on GitHub (best effort, no guarantees)
- Check existing documentation
- Review troubleshooting guide

**Note**: The author provides no warranty, support, or liability for this software.

## 🗺️ Roadmap

- [ ] NFSv4 support
- [ ] Enhanced caching strategies
- [ ] Multi-bucket support
- [ ] Advanced monitoring dashboard
- [ ] Performance benchmarks
- [ ] Integration tests

---

**Made with ❤️ for IBM Cloud**