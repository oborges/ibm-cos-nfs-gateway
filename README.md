# IBM Cloud COS NFS Gateway

IBM Cloud COS NFS Gateway exposes an IBM Cloud Object Storage bucket through an
NFSv3 mount. It is intended for Linux workloads that need a filesystem-shaped
interface while storing file data in COS.

This is an unofficial community project. It is not an IBM product, is not
endorsed by IBM, and is provided as-is without warranty or official support.
Test carefully with your own workload before relying on it.

## What This Gateway Does

- Serves an NFSv3 export backed by one IBM Cloud COS bucket.
- Accepts POSIX-style file operations from Linux NFS clients.
- Uses a local staging layer for writes.
- Syncs staged dirty files to COS asynchronously in background workers.
- Uses multipart upload for large staged objects.
- Provides staging backpressure to prevent the local staging filesystem from
  filling unexpectedly.
- Provides metadata and chunk/range data caching for reads.
- Uses read-ahead, parallel range fetches, and singleflight deduplication to
  reduce repeated COS reads.
- Exposes Prometheus metrics, health endpoints, and debug endpoints when
  enabled.
- Includes a repeatable benchmark suite for write, sync, read, backpressure,
  small-file, crash-safety, and mixed workload validation.

## The Most Important Write Semantics

The gateway uses write-back asynchronous sync.

When an NFS write is accepted by the gateway, the data has been accepted into
local staging. That does not mean the object is already durable in COS.

Durability in COS happens later, when the background sync worker uploads the
staged file and the object becomes visible in the target bucket. Until that
sync completes, the gateway must preserve the staged dirty data locally. If you
need to know whether data is durable in COS, monitor the sync queue, dirty
bytes, upload metrics, logs, or the debug staging endpoint.

In short:

- "Write accepted" means local staging accepted the write.
- "Sync complete" means the staged file was uploaded to COS.
- "Durable in COS" means the uploaded object is visible in COS with the
  expected size/checksum for your validation process.

## Prerequisites

Before running the gateway, the operator must create and provide:

- An IBM Cloud Object Storage service.
- A COS bucket.
- An API key or HMAC credentials with the required permissions for that bucket.
- A Linux host with NFS client utilities.
- Local disk capacity for staging and read cache.
- Go 1.25 or newer if building from source.

The NFS export itself does not implement user authentication. Deploy it only on
trusted hosts or trusted networks, and control access with operating-system,
firewall, VPC, security group, or Kubernetes network policy boundaries.

## Quick Start

Clone and build:

```bash
git clone https://github.com/oborges/ibm-cos-nfs-gateway.git
cd ibm-cos-nfs-gateway
make build
```

Create a configuration file:

```bash
cp configs/config.example.yaml configs/config.yaml
```

Edit `configs/config.yaml` with your COS settings:

```yaml
cos:
  endpoint: "s3.us-south.cloud-object-storage.appdomain.cloud"
  bucket: "my-nfs-bucket"
  region: "us-south"
  auth_type: "iam"
  api_key: "your-ibm-cloud-api-key"
  service_id: "ServiceId-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
```

Run the gateway:

```bash
sudo ./bin/nfs-gateway --config configs/config.yaml
```

Mount it from the same Linux host:

```bash
sudo mkdir -p /mnt/cos-nfs
sudo mount -t nfs -o vers=3,tcp,nolock,mountport=2049,port=2049 localhost:/ /mnt/cos-nfs
```

Unmount when finished:

```bash
sudo umount /mnt/cos-nfs -f
```

## Configuration Areas

The full example lives in `configs/config.example.yaml`. All nested settings can
also be overridden with environment variables using the `NFS_GATEWAY_` prefix.
For example, `cos.api_key` becomes `NFS_GATEWAY_COS_API_KEY`.

### Server

```yaml
server:
  nfs_port: 2049
  metrics_enabled: true
  metrics_port: 8080
  health_enabled: true
  health_port: 8081
  debug_enabled: true
  debug_port: 8082
```

Metrics, health, and debug HTTP servers bind to localhost. Enable only the
endpoints you need.

### Staging And Async Sync

```yaml
staging:
  enabled: true
  root_dir: "/var/staging/nfs-gateway"
  sync_interval: "30s"
  sync_threshold_mb: 10
  max_dirty_age: "5m"
  sync_on_close: false
  max_staging_size_gb: 10
  max_dirty_files: 1000
  sync_worker_count: 4
  sync_queue_size: 100
  max_sync_retries: 3
  retry_backoff_initial: "1s"
  retry_backoff_max: "60s"
  clean_after_sync: true
```

Dirty files are kept under `root_dir` until they are synced. On restart, the
gateway scans staging metadata and resumes syncing dirty data. For production
use, put staging on reliable local storage with enough free capacity for your
largest expected dirty working set.

### Backpressure

```yaml
staging:
  backpressure_enabled: true
  backpressure_mode: "block"
  backpressure_high_watermark_percent: 80
  backpressure_critical_watermark_percent: 95
  backpressure_wait_timeout: "30s"
  backpressure_check_interval: "250ms"
```

Backpressure protects staging before it is full.

- `block` waits for sync workers to drain dirty data until the timeout.
- `fail_fast` rejects writes immediately at or above the critical watermark.
- Above the critical watermark, writes receive deterministic errors instead of
  being allowed to run until the filesystem is full.
- Sync workers continue uploading and cleaning dirty files while pressure is
  active.

Every backpressure decision is logged with the path, requested bytes, available
bytes, pressure level, and decision.

### Read Cache And Read-Ahead

```yaml
cache:
  metadata:
    enabled: true
    size_mb: 256
    ttl_seconds: 60
    max_entries: 10000
  data:
    enabled: true
    size_gb: 10
    path: "/var/cache/nfs-gateway"
    chunk_size_kb: 1024

performance:
  read_ahead_kb: 8192
  max_concurrent_reads: 50
```

Reads can be served from local chunk cache when available. Cold reads fetch
object ranges from COS, warm reads can hit local cache, and read-ahead can fetch
nearby chunks in parallel for sequential access patterns.

### Multipart Upload

```yaml
performance:
  multipart_threshold_mb: 100
  multipart_chunk_mb: 10
  max_concurrent_writes: 25
```

Files larger than the threshold use multipart upload during background sync.
Multipart upload lifecycle is protected by per-object synchronization so
workers do not race the same object.

## Observability

Prometheus metrics are available when `server.metrics_enabled` is true:

```bash
curl http://127.0.0.1:8080/metrics
```

Important metrics include:

- `staging_used_bytes`
- `staging_available_bytes`
- `staging_pressure_level`
- `writes_blocked_total`
- `writes_rejected_total`
- `backpressure_wait_seconds`
- `sync_queue_bytes`
- `staging_sync_queue_depth`
- `staging_sync_queue_bytes`
- `staging_cos_visibility_latency_seconds`
- `staging_upload_duration_seconds`
- `staging_upload_throughput_mib_per_second`
- `cache_hits_total`
- `cache_misses_total`
- `nfs_requests_total`
- `cos_api_calls_total`

Health endpoints are available when `server.health_enabled` is true:

```bash
curl http://127.0.0.1:8081/health/live
curl http://127.0.0.1:8081/health/ready
curl http://127.0.0.1:8081/health
```

Debug endpoints are available when `server.debug_enabled` is true:

```bash
curl http://127.0.0.1:8082/debug/staging/sync
curl http://127.0.0.1:8082/debug/perf
```

Use `/debug/staging/sync` to check dirty files, sync queue depth, queue bytes,
staging pressure, last sync timing, and upload throughput.

## Benchmarking

The benchmark suite is in `scripts/benchmark_suite.py` and
`scripts/run_benchmark_suite.sh`. It writes timestamped results under
`benchmark-results/`.

Run a standard profile against a running and mounted gateway:

```bash
PROFILE=standard ./scripts/run_benchmark_suite.sh
```

Run selected categories:

```bash
./scripts/run_benchmark_suite.sh --categories frontend-write sync read
```

Backpressure and crash-safety tests are opt-in because they intentionally stress
staging capacity or kill the gateway:

```bash
./scripts/run_benchmark_suite.sh \
  --categories backpressure \
  --allow-backpressure
```

```bash
./scripts/run_benchmark_suite.sh \
  --categories crash-safety \
  --allow-crash \
  --gateway-command 'cd ~/ibm-cos-nfs-gateway && sudo nohup ./bin/nfs-gateway --config configs/config.yaml >/tmp/nfs-gateway-benchmark.log 2>&1 &' \
  --post-restart-command 'sudo umount /mnt/cos-nfs -f || true; sudo mount -t nfs -o vers=3,tcp,nolock,mountport=2049,port=2049 localhost:/ /mnt/cos-nfs'
```

Each benchmark run produces:

- `SUMMARY.md`
- `results.json`
- `results.csv`
- `baseline.json`
- `environment.json`
- `monitor_samples.csv`
- raw fio output when fio-backed tests are used

See `docs/BENCHMARK_SUITE.md` for the benchmark categories and output format.

## Docker And Kubernetes

Docker and Kubernetes manifests are provided under `deployments/`.

Build the image:

```bash
docker build -t cos-nfs-gateway -f deployments/docker/Dockerfile .
```

Run with Docker Compose:

```bash
cd deployments/docker
COS_ENDPOINT=... COS_BUCKET=... IBM_CLOUD_API_KEY=... docker compose up -d
```

The optional monitoring profile starts Prometheus and Grafana. Grafana requires
`GRAFANA_PASSWORD` to be set before enabling that profile.

Kubernetes manifests are examples and should be reviewed for your cluster,
secret management, storage, network policy, and operational requirements before
use.

## Operational Notes

- Keep staging and cache paths outside ephemeral directories for real workloads.
- Size staging for the largest expected unsynced dirty working set.
- Monitor `sync_queue_bytes`, `staging_used_bytes`, and upload latency.
- Treat a growing sync queue as a durability delay, not just a performance
  issue.
- Use private COS endpoints or private networking where possible.
- Keep COS credentials out of source control.
- Restrict access to the NFS port at the host or network layer.
- Prefer benchmark validation on the same VM shape, disk type, COS region, and
  mount options used in deployment.

## Troubleshooting

Gateway fails to start:

```bash
sudo ss -tlnp | grep 2049
sudo ./bin/nfs-gateway --config configs/config.yaml
```

Mount fails:

```bash
sudo umount /mnt/cos-nfs -f
sudo mount -t nfs -o vers=3,tcp,nolock,mountport=2049,port=2049 localhost:/ /mnt/cos-nfs
```

Writes succeed but objects are not visible in COS yet:

```bash
curl http://127.0.0.1:8082/debug/staging/sync
curl http://127.0.0.1:8080/metrics | grep -E 'sync_queue|staging_|writes_'
```

Remember that accepted writes are staged locally first. Check whether dirty
files or queue bytes are still present before concluding that COS has the final
object.

Backpressure rejects or blocks writes:

```bash
df -h /var/staging/nfs-gateway
curl http://127.0.0.1:8082/debug/staging/sync
curl http://127.0.0.1:8080/metrics | grep -E 'staging_pressure|writes_blocked|writes_rejected|backpressure'
```

## Development

```bash
make build
make test
make benchmark-suite
```

Useful local documentation:

- `docs/BENCHMARK_SUITE.md`
- `docs/BENCHMARKING.md`
- `ARCHITECTURE.md`
- `docs/STAGING_ARCHITECTURE.md`

## License

This project is licensed under the MIT License. See `LICENSE` for details.

## Support

There is no official support channel. Issues and pull requests are handled on a
best-effort basis.
