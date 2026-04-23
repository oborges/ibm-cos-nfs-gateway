package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/oborges/cos-nfs-gateway/internal/logging"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (
	// NFS request metrics
	nfsRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nfs_requests_total",
			Help: "Total number of NFS requests",
		},
		[]string{"operation", "status"},
	)

	nfsRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nfs_request_duration_seconds",
			Help:    "NFS request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	// COS API metrics
	cosAPICallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cos_api_calls_total",
			Help: "Total number of COS API calls",
		},
		[]string{"operation", "status"},
	)

	cosAPIDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cos_api_duration_seconds",
			Help:    "COS API call duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	// Cache metrics
	cacheHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_hits_total",
			Help: "Total number of cache hits",
		},
		[]string{"cache_type"},
	)

	cacheMissesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_misses_total",
			Help: "Total number of cache misses",
		},
		[]string{"cache_type"},
	)

	cacheSizeBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cache_size_bytes",
			Help: "Current cache size in bytes",
		},
		[]string{"cache_type"},
	)

	cacheEvictionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_evictions_total",
			Help: "Total number of cache evictions",
		},
		[]string{"cache_type"},
	)

	// Data transfer metrics
	bytesReadTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "bytes_read_total",
			Help: "Total bytes read",
		},
	)

	bytesWrittenTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "bytes_written_total",
			Help: "Total bytes written",
		},
	)

	// Connection metrics
	activeConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_connections",
			Help: "Number of active connections",
		},
	)

	// Lock metrics
	activeLocksTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_locks_total",
			Help: "Number of active file locks",
		},
	)
)

// Initialize registers all metrics with Prometheus
func Initialize() {
	prometheus.MustRegister(
		nfsRequestsTotal,
		nfsRequestDuration,
		cosAPICallsTotal,
		cosAPIDuration,
		cacheHitsTotal,
		cacheMissesTotal,
		cacheSizeBytes,
		cacheEvictionsTotal,
		bytesReadTotal,
		bytesWrittenTotal,
		activeConnections,
		activeLocksTotal,
	)

	logging.Info("Metrics initialized")
}

// StartMetricsServer starts the Prometheus metrics HTTP server
func StartMetricsServer(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	
	logging.Info("Starting metrics server", zap.String("addr", addr))
	
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			logging.Error("Metrics server failed", zap.Error(err))
		}
	}()
	
	return nil
}

// RecordNFSRequest records an NFS request
func RecordNFSRequest(operation, status string, duration time.Duration) {
	nfsRequestsTotal.WithLabelValues(operation, status).Inc()
	nfsRequestDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

// RecordCOSAPICall records a COS API call
func RecordCOSAPICall(operation, status string, duration time.Duration) {
	cosAPICallsTotal.WithLabelValues(operation, status).Inc()
	cosAPIDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

// RecordCacheHit records a cache hit
func RecordCacheHit(cacheType string) {
	cacheHitsTotal.WithLabelValues(cacheType).Inc()
}

// RecordCacheMiss records a cache miss
func RecordCacheMiss(cacheType string) {
	cacheMissesTotal.WithLabelValues(cacheType).Inc()
}

// SetCacheSize sets the current cache size
func SetCacheSize(cacheType string, size int64) {
	cacheSizeBytes.WithLabelValues(cacheType).Set(float64(size))
}

// RecordCacheEviction records a cache eviction
func RecordCacheEviction(cacheType string) {
	cacheEvictionsTotal.WithLabelValues(cacheType).Inc()
}

// RecordBytesRead records bytes read
func RecordBytesRead(bytes int64) {
	bytesReadTotal.Add(float64(bytes))
}

// RecordBytesWritten records bytes written
func RecordBytesWritten(bytes int64) {
	bytesWrittenTotal.Add(float64(bytes))
}

// SetActiveConnections sets the number of active connections
func SetActiveConnections(count int) {
	activeConnections.Set(float64(count))
}

// SetActiveLocks sets the number of active locks
func SetActiveLocks(count int) {
	activeLocksTotal.Set(float64(count))
}

// Made with Bob
