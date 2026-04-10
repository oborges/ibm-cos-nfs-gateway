# IBM Cloud COS NFS Gateway - Quick Start Guide

## Overview

The IBM Cloud COS NFS Gateway provides NFS filesystem access to IBM Cloud Object Storage (COS), enabling you to mount COS buckets as network filesystems on your IBM Cloud Virtual Server Instances (VSIs).

## Prerequisites

- IBM Cloud account with COS service
- IBM Cloud API key or HMAC credentials
- COS bucket created
- Docker or Kubernetes cluster (for deployment)

## Quick Start with Docker

### 1. Create Configuration File

Create a `config.yaml` file:

```yaml
server:
  nfs_port: 2049
  metrics_port: 8080
  health_port: 8081

cos:
  endpoint: "s3.us-south.cloud-object-storage.appdomain.cloud"
  bucket: "my-nfs-bucket"
  region: "us-south"
  auth_type: "iam"
  api_key: "${IBM_CLOUD_API_KEY}"

cache:
  metadata:
    enabled: true
    size_mb: 256
    ttl_seconds: 60
  data:
    enabled: true
    size_gb: 10
    path: "/var/cache/nfs-gateway"

logging:
  level: "info"
  format: "json"
```

### 2. Run with Docker

```bash
# Set your IBM Cloud API key
export IBM_CLOUD_API_KEY="your-api-key-here"

# Run the container
docker run -d \
  --name cos-nfs-gateway \
  -p 2049:2049 \
  -p 8080:8080 \
  -p 8081:8081 \
  -e IBM_CLOUD_API_KEY="${IBM_CLOUD_API_KEY}" \
  -v $(pwd)/config.yaml:/etc/nfs-gateway/config.yaml \
  -v nfs-cache:/var/cache/nfs-gateway \
  oborges/cos-nfs-gateway:latest
```

### 3. Mount the NFS Share

On your client machine:

```bash
# Create mount point
sudo mkdir -p /mnt/cos

# Mount the NFS share
sudo mount -t nfs -o vers=3,tcp localhost:/ /mnt/cos

# Verify mount
df -h /mnt/cos
```

### 4. Test the Mount

```bash
# Create a test file
echo "Hello from COS!" > /mnt/cos/test.txt

# List files
ls -la /mnt/cos/

# Read the file
cat /mnt/cos/test.txt
```

## Quick Start with Docker Compose

### 1. Create docker-compose.yml

```yaml
version: '3.8'

services:
  nfs-gateway:
    image: oborges/cos-nfs-gateway:latest
    ports:
      - "2049:2049"
      - "8080:8080"
      - "8081:8081"
    environment:
      - COS_ENDPOINT=s3.us-south.cloud-object-storage.appdomain.cloud
      - COS_BUCKET=my-nfs-bucket
      - COS_REGION=us-south
      - IBM_CLOUD_API_KEY=${IBM_CLOUD_API_KEY}
    volumes:
      - ./config.yaml:/etc/nfs-gateway/config.yaml
      - nfs-cache:/var/cache/nfs-gateway
    restart: unless-stopped

volumes:
  nfs-cache:
```

### 2. Start the Service

```bash
# Set your API key
export IBM_CLOUD_API_KEY="your-api-key-here"

# Start the service
docker-compose up -d

# Check logs
docker-compose logs -f
```

## Quick Start with Kubernetes

### 1. Create Secret

```bash
kubectl create secret generic nfs-gateway-secret \
  --from-literal=ibm-cloud-api-key="your-api-key-here"
```

### 2. Deploy

```bash
# Apply all manifests
kubectl apply -f deployments/kubernetes/

# Check status
kubectl get pods -l app=nfs-gateway
kubectl get svc nfs-gateway
```

### 3. Get Service IP

```bash
# Get the LoadBalancer IP
kubectl get svc nfs-gateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
```

### 4. Mount from Client

```bash
# Replace <SERVICE_IP> with the actual IP
sudo mount -t nfs -o vers=3,tcp <SERVICE_IP>:/ /mnt/cos
```

## Monitoring

### Health Checks

```bash
# Liveness probe
curl http://localhost:8081/health/live

# Readiness probe
curl http://localhost:8081/health/ready

# Detailed health
curl http://localhost:8081/health
```

### Metrics

```bash
# View Prometheus metrics
curl http://localhost:8080/metrics
```

### Logs

```bash
# Docker
docker logs -f cos-nfs-gateway

# Docker Compose
docker-compose logs -f

# Kubernetes
kubectl logs -f -l app=nfs-gateway
```

## Configuration

### Environment Variables

All configuration can be overridden with environment variables using the `NFS_GATEWAY_` prefix:

```bash
# Example
export NFS_GATEWAY_LOGGING_LEVEL=debug
export NFS_GATEWAY_CACHE_METADATA_ENABLED=true
export NFS_GATEWAY_CACHE_DATA_SIZE_GB=20
```

### Authentication Methods

#### IAM (Recommended)

```yaml
cos:
  auth_type: "iam"
  api_key: "your-api-key"
```

#### HMAC

```yaml
cos:
  auth_type: "hmac"
  access_key: "your-access-key"
  secret_key: "your-secret-key"
```

## Performance Tuning

### For High Throughput

```yaml
performance:
  read_ahead_kb: 2048
  write_buffer_kb: 8192
  worker_pool_size: 200
  max_concurrent_reads: 100
  max_concurrent_writes: 50

cache:
  data:
    size_gb: 50
```

### For Low Latency

```yaml
cache:
  metadata:
    ttl_seconds: 30
  data:
    enabled: true
    size_gb: 20

performance:
  read_ahead_kb: 512
```

## Troubleshooting

### Connection Issues

```bash
# Check if service is running
curl http://localhost:8081/health/live

# Check COS connectivity
curl http://localhost:8081/health/ready

# View logs
docker logs cos-nfs-gateway
```

### Mount Issues

```bash
# Check NFS port is accessible
telnet localhost 2049

# Try mounting with verbose output
sudo mount -t nfs -o vers=3,tcp,v localhost:/ /mnt/cos

# Check mount options
mount | grep /mnt/cos
```

### Performance Issues

```bash
# Check cache statistics
curl http://localhost:8080/metrics | grep cache

# Check COS API latency
curl http://localhost:8080/metrics | grep cos_api_duration

# Increase cache size in config.yaml
```

## Next Steps

- [Full Documentation](../README.md)
- [Configuration Reference](CONFIGURATION.md)
- [Performance Tuning](PERFORMANCE.md)
- [Troubleshooting Guide](TROUBLESHOOTING.md)
- [API Documentation](API.md)

## Support

For issues and questions:
- GitHub Issues: https://github.com/oborges/cos-nfs-gateway/issues
- Documentation: https://github.com/oborges/cos-nfs-gateway/docs