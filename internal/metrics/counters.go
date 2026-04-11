package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// PerformanceCounters tracks detailed performance metrics
type PerformanceCounters struct {
	// ReadDir metrics
	ReadDirCalls       atomic.Int64
	ReadDirTotalTime   atomic.Int64 // nanoseconds
	ReadDirMaxTime     atomic.Int64 // nanoseconds
	
	// ListDirectory metrics
	ListDirCalls       atomic.Int64
	ListDirCacheHits   atomic.Int64
	ListDirCacheMisses atomic.Int64
	ListDirTotalTime   atomic.Int64 // nanoseconds
	
	// COS operation metrics
	COSListObjects     atomic.Int64
	COSHeadObject      atomic.Int64
	COSGetObject       atomic.Int64
	
	// Conversion metrics
	ConversionTime     atomic.Int64 // nanoseconds
	
	// Per-path tracking
	pathCalls          sync.Map // map[string]*int64
	mu                 sync.Mutex
	startTime          time.Time
}

var globalCounters = &PerformanceCounters{
	startTime: time.Now(),
}

// GetGlobalCounters returns the global performance counters
func GetGlobalCounters() *PerformanceCounters {
	return globalCounters
}

// ResetCounters resets all counters
func ResetCounters() {
	globalCounters = &PerformanceCounters{
		startTime: time.Now(),
	}
}

// RecordReadDir records a ReadDir call
func RecordReadDir(duration time.Duration) {
	globalCounters.ReadDirCalls.Add(1)
	nanos := duration.Nanoseconds()
	globalCounters.ReadDirTotalTime.Add(nanos)
	
	// Update max time
	for {
		oldMax := globalCounters.ReadDirMaxTime.Load()
		if nanos <= oldMax {
			break
		}
		if globalCounters.ReadDirMaxTime.CompareAndSwap(oldMax, nanos) {
			break
		}
	}
}

// RecordListDirectory records a ListDirectory call
func RecordListDirectory(duration time.Duration, cacheHit bool) {
	globalCounters.ListDirCalls.Add(1)
	if cacheHit {
		globalCounters.ListDirCacheHits.Add(1)
	} else {
		globalCounters.ListDirCacheMisses.Add(1)
	}
	globalCounters.ListDirTotalTime.Add(duration.Nanoseconds())
}

// RecordCOSListObjects records a COS ListObjects call
func RecordCOSListObjects() {
	globalCounters.COSListObjects.Add(1)
}

// RecordCOSHeadObject records a COS HeadObject call
func RecordCOSHeadObject() {
	globalCounters.COSHeadObject.Add(1)
}

// RecordCOSGetObject records a COS GetObject call
func RecordCOSGetObject() {
	globalCounters.COSGetObject.Add(1)
}

// RecordConversion records time spent in conversion
func RecordConversion(duration time.Duration) {
	globalCounters.ConversionTime.Add(duration.Nanoseconds())
}

// RecordPathCall records a call for a specific path
func (pc *PerformanceCounters) RecordPathCall(path string) {
	val, _ := pc.pathCalls.LoadOrStore(path, new(int64))
	counter := val.(*int64)
	atomic.AddInt64(counter, 1)
}

// GetPathCallCount returns the number of calls for a specific path
func (pc *PerformanceCounters) GetPathCallCount(path string) int64 {
	val, ok := pc.pathCalls.Load(path)
	if !ok {
		return 0
	}
	return atomic.LoadInt64(val.(*int64))
}

// GetReport generates a performance report
func (pc *PerformanceCounters) GetReport() map[string]interface{} {
	readDirCalls := pc.ReadDirCalls.Load()
	listDirCalls := pc.ListDirCalls.Load()
	
	avgReadDir := int64(0)
	if readDirCalls > 0 {
		avgReadDir = pc.ReadDirTotalTime.Load() / readDirCalls
	}
	
	avgListDir := int64(0)
	if listDirCalls > 0 {
		avgListDir = pc.ListDirTotalTime.Load() / listDirCalls
	}
	
	return map[string]interface{}{
		"uptime_seconds": time.Since(pc.startTime).Seconds(),
		"readdir": map[string]interface{}{
			"total_calls":      readDirCalls,
			"avg_latency_ms":   float64(avgReadDir) / 1e6,
			"max_latency_ms":   float64(pc.ReadDirMaxTime.Load()) / 1e6,
			"total_time_ms":    float64(pc.ReadDirTotalTime.Load()) / 1e6,
		},
		"listdirectory": map[string]interface{}{
			"total_calls":      listDirCalls,
			"cache_hits":       pc.ListDirCacheHits.Load(),
			"cache_misses":     pc.ListDirCacheMisses.Load(),
			"avg_latency_ms":   float64(avgListDir) / 1e6,
			"total_time_ms":    float64(pc.ListDirTotalTime.Load()) / 1e6,
		},
		"cos_operations": map[string]interface{}{
			"list_objects":     pc.COSListObjects.Load(),
			"head_object":      pc.COSHeadObject.Load(),
			"get_object":       pc.COSGetObject.Load(),
		},
		"conversion": map[string]interface{}{
			"total_time_ms":    float64(pc.ConversionTime.Load()) / 1e6,
		},
	}
}

// Made with Bob
