# IBM Cloud COS NFS Gateway - Deployment Strategy

## Overview

This document outlines the deployment strategy for the IBM Cloud COS NFS Gateway, covering different deployment scenarios, infrastructure requirements, and operational considerations.

## Deployment Models

### Model 1: Single-Instance Deployment

**Use Case**: Development, testing, small workloads

```
┌─────────────────────────────────────┐
│        IBM Cloud VSI                │
│                                     │
│  ┌───────────────────────────────┐ │
│  │  NFS Gateway Container        │ │
│  │  - Port 2049 (NFS)            │ │
│  │  - Port 8080 (Metrics)        │ │
│  │  - Local cache volume         │ │
│  └───────────────────────────────┘ │
│                                     │
└─────────────────┬───────────────────┘
                  │
                  ▼
        ┌─────────────────┐
        │  IBM Cloud COS  │
        └─────────────────┘
```

**Specifications:**
- VSI: 4 vCPU, 16GB RAM, 100GB SSD
- Network: 1 Gbps
- Estimated cost: ~$150/month
- Supports: 10-20 concurrent clients

**Deployment Steps:**
```bash
# 1. Provision VSI
ibmcloud is instance-create nfs-gateway-01 \
  --vpc my-vpc \
  --zone us-south-1 \
  --profile bx2-4x16 \
  --image ubuntu-20-04

# 2. Install Docker
ssh root@<vsi-ip>
curl -fsSL https://get.docker.com | sh

# 3. Deploy gateway
docker run -d \
  --name nfs-gateway \
  --restart always \
  -p 2049:2049 \
  -p 8080:8080 \
  -v /var/cache/nfs-gateway:/var/cache/nfs-gateway \
  -e IBM_CLOUD_API_KEY=${API_KEY} \
  -e COS_ENDPOINT=${COS_ENDPOINT} \
  -e COS_BUCKET=${BUCKET_NAME} \
  cos-nfs-gateway:latest
```

### Model 2: High-Availability Deployment

**Use Case**: Production workloads, mission-critical applications

```
┌─────────────────────────────────────────────────────────┐
│              Load Balancer (VPC LB)                     │
└────────┬──────────────────────────┬─────────────────────┘
         │                          │
    ┌────▼────┐                ┌────▼────┐
    │  VSI 1  │                │  VSI 2  │
    │ Gateway │                │ Gateway │
    └────┬────┘                └────┬────┘
         │                          │
         └──────────┬───────────────┘
                    │
         ┌──────────▼──────────┐
         │   IBM Cloud COS     │
         └─────────────────────┘
```

**Specifications:**
- 2+ VSIs: 8 vCPU, 32GB RAM, 200GB SSD each
- VPC Load Balancer
- Shared cache (optional): Redis cluster
- Network: 10 Gbps
- Estimated cost: ~$500/month
- Supports: 100+ concurrent clients

**Deployment Steps:**
```bash
# 1. Create VPC and subnets
ibmcloud is vpc-create nfs-gateway-vpc
ibmcloud is subnet-create nfs-gateway-subnet-1 \
  --vpc nfs-gateway-vpc \
  --zone us-south-1

# 2. Provision multiple VSIs
for i in 1 2; do
  ibmcloud is instance-create nfs-gateway-0$i \
    --vpc nfs-gateway-vpc \
    --zone us-south-1 \
    --profile bx2-8x32 \
    --image ubuntu-20-04
done

# 3. Create load balancer
ibmcloud is load-balancer-create nfs-gateway-lb \
  --subnet nfs-gateway-subnet-1 \
  --family application

# 4. Deploy gateway on each VSI
# (Use configuration management tool like Ansible)
```

### Model 3: Kubernetes Deployment

**Use Case**: Cloud-native applications, microservices, auto-scaling

```
┌─────────────────────────────────────────────────────────┐
│           IBM Cloud Kubernetes Service (IKS)            │
│                                                         │
│  ┌───────────────────────────────────────────────────┐ │
│  │              Ingress Controller                   │ │
│  └─────────────────┬─────────────────────────────────┘ │
│                    │                                   │
│  ┌─────────────────▼─────────────────────────────────┐ │
│  │         Service (LoadBalancer/NodePort)           │ │
│  └─────────────────┬─────────────────────────────────┘ │
│                    │                                   │
│  ┌─────────────────▼─────────────────────────────────┐ │
│  │              StatefulSet                          │ │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐        │ │
│  │  │ Gateway  │  │ Gateway  │  │ Gateway  │        │ │
│  │  │  Pod 1   │  │  Pod 2   │  │  Pod 3   │        │ │
│  │  └────┬─────┘  └────┬─────┘  └────┬─────┘        │ │
│  │       │             │             │               │ │
│  │  ┌────▼─────┐  ┌────▼─────┐  ┌────▼─────┐        │ │
│  │  │   PVC    │  │   PVC    │  │   PVC    │        │ │
│  │  └──────────┘  └──────────┘  └──────────┘        │ │
│  └───────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
                         │
                         ▼
              ┌─────────────────┐
              │ IBM Cloud COS   │
              └─────────────────┘
```

**Specifications:**
- IKS cluster: 3+ worker nodes
- Worker nodes: 8 vCPU, 32GB RAM each
- Persistent volumes for cache
- HPA for auto-scaling
- Estimated cost: ~$800/month
- Supports: 200+ concurrent clients

**Deployment Steps:**
```bash
# 1. Create IKS cluster
ibmcloud ks cluster create classic \
  --name nfs-gateway-cluster \
  --zone us-south-1 \
  --flavor b3c.8x32 \
  --workers 3

# 2. Configure kubectl
ibmcloud ks cluster config --cluster nfs-gateway-cluster

# 3. Create namespace
kubectl create namespace nfs-gateway

# 4. Create secrets
kubectl create secret generic cos-credentials \
  --from-literal=api-key=${IBM_CLOUD_API_KEY} \
  -n nfs-gateway

# 5. Deploy using Helm or kubectl
helm install nfs-gateway ./helm/nfs-gateway \
  --namespace nfs-gateway \
  --set cos.endpoint=${COS_ENDPOINT} \
  --set cos.bucket=${BUCKET_NAME}

# Or using kubectl
kubectl apply -f deployments/kubernetes/ -n nfs-gateway
```

## Infrastructure Requirements

### Compute Resources

| Deployment Model | vCPU | RAM | Storage | Network |
|-----------------|------|-----|---------|---------|
| Development | 2-4 | 8-16 GB | 50 GB | 1 Gbps |
| Production (Single) | 4-8 | 16-32 GB | 100 GB | 10 Gbps |
| Production (HA) | 8-16 per node | 32-64 GB per node | 200 GB per node | 10 Gbps |
| Kubernetes | 8+ per node | 32+ GB per node | 100+ GB per node | 10 Gbps |

### Storage Requirements

**Cache Storage:**
- Metadata cache: 256 MB - 1 GB (in-memory)
- Data cache: 10 GB - 100 GB (disk)
- Logs: 5 GB - 20 GB

**Persistent Volumes (Kubernetes):**
- Cache volume: 50 GB - 200 GB per pod
- Log volume: 10 GB per pod

### Network Requirements

**Bandwidth:**
- Minimum: 1 Gbps
- Recommended: 10 Gbps
- High-performance: 25 Gbps

**Latency:**
- VSI to COS: <10ms (same region)
- Client to Gateway: <5ms (same VPC)

**Ports:**
- 2049: NFS service
- 8080: Prometheus metrics
- 8081: Health checks
- 22: SSH (management)

## Deployment Configurations

### Configuration 1: Development Environment

```yaml
# config-dev.yaml
server:
  nfs_port: 2049
  metrics_port: 8080
  health_port: 8081
  max_connections: 100

cos:
  endpoint: s3.us-south.cloud-object-storage.appdomain.cloud
  bucket: dev-bucket
  region: us-south
  auth_type: iam

cache:
  metadata:
    enabled: true
    size_mb: 128
    ttl_seconds: 30
  data:
    enabled: true
    size_gb: 5

performance:
  read_ahead_kb: 512
  write_buffer_kb: 2048
  worker_pool_size: 50

logging:
  level: debug
  format: json
```

### Configuration 2: Production Environment

```yaml
# config-prod.yaml
server:
  nfs_port: 2049
  metrics_port: 8080
  health_port: 8081
  max_connections: 1000

cos:
  endpoint: s3.us-south.cloud-object-storage.appdomain.cloud
  bucket: prod-bucket
  region: us-south
  auth_type: iam

cache:
  metadata:
    enabled: true
    size_mb: 512
    ttl_seconds: 60
  data:
    enabled: true
    size_gb: 50

performance:
  read_ahead_kb: 2048
  write_buffer_kb: 8192
  multipart_threshold_mb: 100
  multipart_chunk_mb: 10
  worker_pool_size: 200

logging:
  level: info
  format: json
```

## Kubernetes Manifests

### StatefulSet

```yaml
# statefulset.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: nfs-gateway
  namespace: nfs-gateway
spec:
  serviceName: nfs-gateway
  replicas: 3
  selector:
    matchLabels:
      app: nfs-gateway
  template:
    metadata:
      labels:
        app: nfs-gateway
    spec:
      containers:
      - name: nfs-gateway
        image: cos-nfs-gateway:latest
        ports:
        - containerPort: 2049
          name: nfs
        - containerPort: 8080
          name: metrics
        - containerPort: 8081
          name: health
        env:
        - name: IBM_CLOUD_API_KEY
          valueFrom:
            secretKeyRef:
              name: cos-credentials
              key: api-key
        - name: COS_ENDPOINT
          valueFrom:
            configMapKeyRef:
              name: nfs-gateway-config
              key: cos-endpoint
        - name: COS_BUCKET
          valueFrom:
            configMapKeyRef:
              name: nfs-gateway-config
              key: cos-bucket
        volumeMounts:
        - name: cache
          mountPath: /var/cache/nfs-gateway
        - name: config
          mountPath: /etc/nfs-gateway
        resources:
          requests:
            cpu: 2000m
            memory: 8Gi
          limits:
            cpu: 4000m
            memory: 16Gi
        livenessProbe:
          httpGet:
            path: /health/live
            port: 8081
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health/ready
            port: 8081
          initialDelaySeconds: 10
          periodSeconds: 5
      volumes:
      - name: config
        configMap:
          name: nfs-gateway-config
  volumeClaimTemplates:
  - metadata:
      name: cache
    spec:
      accessModes: [ "ReadWriteOnce" ]
      storageClassName: ibmc-block-gold
      resources:
        requests:
          storage: 100Gi
```

### Service

```yaml
# service.yaml
apiVersion: v1
kind: Service
metadata:
  name: nfs-gateway
  namespace: nfs-gateway
spec:
  type: LoadBalancer
  selector:
    app: nfs-gateway
  ports:
  - name: nfs
    port: 2049
    targetPort: 2049
    protocol: TCP
  - name: metrics
    port: 8080
    targetPort: 8080
    protocol: TCP
```

### HorizontalPodAutoscaler

```yaml
# hpa.yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: nfs-gateway-hpa
  namespace: nfs-gateway
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: StatefulSet
    name: nfs-gateway
  minReplicas: 3
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 80
```

## Monitoring Setup

### Prometheus Configuration

```yaml
# prometheus-config.yaml
scrape_configs:
  - job_name: 'nfs-gateway'
    kubernetes_sd_configs:
      - role: pod
        namespaces:
          names:
            - nfs-gateway
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_app]
        action: keep
        regex: nfs-gateway
      - source_labels: [__meta_kubernetes_pod_ip]
        target_label: __address__
        replacement: $1:8080
```

### Grafana Dashboard

Key metrics to monitor:
- Request rate and latency
- Cache hit/miss ratios
- COS API call statistics
- Error rates
- Resource utilization (CPU, memory, disk)
- Active connections

## Backup and Disaster Recovery

### Backup Strategy

1. **Configuration Backup**
   - Store configs in Git repository
   - Version control all manifests
   - Document custom settings

2. **Cache Backup** (Optional)
   - Not critical (can be rebuilt)
   - Consider for large, stable datasets

3. **Monitoring Data**
   - Prometheus data retention: 15 days
   - Long-term storage in IBM Cloud Monitoring

### Disaster Recovery

**RTO (Recovery Time Objective):** 15 minutes
**RPO (Recovery Point Objective):** 0 (stateless service)

**Recovery Steps:**
1. Deploy new gateway instance
2. Apply configuration
3. Verify COS connectivity
4. Update DNS/load balancer
5. Validate client connectivity

## Security Hardening

### Network Security

```yaml
# network-policy.yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: nfs-gateway-policy
  namespace: nfs-gateway
spec:
  podSelector:
    matchLabels:
      app: nfs-gateway
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          name: allowed-clients
    ports:
    - protocol: TCP
      port: 2049
  egress:
  - to:
    - podSelector: {}
    ports:
    - protocol: TCP
      port: 443  # COS API
```

### Pod Security Policy

```yaml
# psp.yaml
apiVersion: policy/v1beta1
kind: PodSecurityPolicy
metadata:
  name: nfs-gateway-psp
spec:
  privileged: false
  allowPrivilegeEscalation: false
  requiredDropCapabilities:
    - ALL
  volumes:
    - 'configMap'
    - 'secret'
    - 'persistentVolumeClaim'
  runAsUser:
    rule: 'MustRunAsNonRoot'
  seLinux:
    rule: 'RunAsAny'
  fsGroup:
    rule: 'RunAsAny'
```

## Cost Optimization

### Cost Breakdown (Monthly)

**Single-Instance Deployment:**
- VSI (4 vCPU, 16GB): ~$120
- COS Storage (1TB): ~$21
- Data Transfer (500GB): ~$45
- **Total: ~$186/month**

**HA Deployment (2 instances):**
- VSIs (2x 8 vCPU, 32GB): ~$400
- Load Balancer: ~$50
- COS Storage (1TB): ~$21
- Data Transfer (1TB): ~$90
- **Total: ~$561/month**

**Kubernetes Deployment (3 nodes):**
- IKS Worker Nodes (3x 8 vCPU, 32GB): ~$600
- Persistent Volumes (300GB): ~$30
- Load Balancer: ~$50
- COS Storage (1TB): ~$21
- Data Transfer (1TB): ~$90
- **Total: ~$791/month**

### Cost Optimization Tips

1. **Right-size instances** based on actual usage
2. **Use reserved instances** for predictable workloads
3. **Implement lifecycle policies** on COS
4. **Optimize cache sizes** to reduce COS API calls
5. **Use private endpoints** to avoid data transfer charges
6. **Monitor and adjust** based on metrics

## Deployment Checklist

### Pre-Deployment
- [ ] IBM Cloud account configured
- [ ] COS bucket created
- [ ] IAM service ID and API key created
- [ ] VPC and subnets configured
- [ ] Security groups configured
- [ ] DNS records prepared

### Deployment
- [ ] Infrastructure provisioned
- [ ] Gateway deployed
- [ ] Configuration applied
- [ ] Health checks passing
- [ ] Monitoring configured
- [ ] Logging configured

### Post-Deployment
- [ ] Performance testing completed
- [ ] Security scan completed
- [ ] Documentation updated
- [ ] Team training completed
- [ ] Runbooks created
- [ ] Backup procedures tested

## Rollback Procedures

### Quick Rollback (< 5 minutes)
```bash
# Kubernetes
kubectl rollout undo statefulset/nfs-gateway -n nfs-gateway

# Docker
docker stop nfs-gateway
docker run -d --name nfs-gateway cos-nfs-gateway:previous-version
```

### Full Rollback (< 15 minutes)
1. Stop current deployment
2. Restore previous configuration
3. Deploy previous version
4. Verify functionality
5. Update monitoring

## Support and Maintenance

### Regular Maintenance Tasks

**Daily:**
- Monitor health checks
- Review error logs
- Check resource utilization

**Weekly:**
- Review performance metrics
- Analyze cache hit ratios
- Check for security updates

**Monthly:**
- Update dependencies
- Review and optimize configuration
- Capacity planning review
- Disaster recovery drill

### Troubleshooting Resources

- Logs: `/var/log/nfs-gateway/`
- Metrics: `http://<gateway-ip>:8080/metrics`
- Health: `http://<gateway-ip>:8081/health/`
- Documentation: See TROUBLESHOOTING.md

## Next Steps

1. Choose appropriate deployment model
2. Provision infrastructure
3. Deploy gateway
4. Configure monitoring
5. Test thoroughly
6. Document procedures
7. Train operations team
8. Go live with monitoring