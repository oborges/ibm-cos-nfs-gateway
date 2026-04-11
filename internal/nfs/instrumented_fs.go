package nfs

import (
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/oborges/cos-nfs-gateway/internal/metrics"
)

// InstrumentedFilesystem wraps a billy.Filesystem to track NFS-level operations
type InstrumentedFilesystem struct {
	billy.Filesystem
	logger *Logger
	
	// Per-path tracking for detecting loops
	pathCalls sync.Map // map[string]*PathCallTracker
}

// PathCallTracker tracks calls to a specific path
type PathCallTracker struct {
	callCount   atomic.Int64
	firstCall   time.Time
	lastCall    time.Time
	mu          sync.Mutex
	callTimes   []time.Time // Track timing of each call
}

// NewInstrumentedFilesystem wraps a filesystem with instrumentation
func NewInstrumentedFilesystem(fs billy.Filesystem, logger *Logger) *InstrumentedFilesystem {
	return &InstrumentedFilesystem{
		Filesystem: fs,
		logger:     logger,
	}
}

// ReadDir wraps the ReadDir call with detailed instrumentation
func (ifs *InstrumentedFilesystem) ReadDir(path string) ([]os.FileInfo, error) {
	start := time.Now()
	
	// Get or create tracker for this path
	trackerVal, _ := ifs.pathCalls.LoadOrStore(path, &PathCallTracker{
		firstCall: start,
		callTimes: make([]time.Time, 0, 100),
	})
	tracker := trackerVal.(*PathCallTracker)
	
	// Record this call
	callNum := tracker.callCount.Add(1)
	tracker.mu.Lock()
	tracker.lastCall = start
	if len(tracker.callTimes) < 200 {
		tracker.callTimes = append(tracker.callTimes, start)
	}
	tracker.mu.Unlock()
	
	// Log first few calls and detect rapid loops
	if callNum <= 10 {
		ifs.logger.Info("ReadDir call",
			"path", path,
			"call_number", callNum,
			"time_since_first_ms", start.Sub(tracker.firstCall).Milliseconds())
	}
	
	// Detect rapid repeated calls (potential infinite loop)
	if callNum > 100 && callNum%100 == 0 {
		tracker.mu.Lock()
		recentCalls := len(tracker.callTimes)
		var avgGap time.Duration
		if recentCalls > 1 {
			totalGap := tracker.callTimes[recentCalls-1].Sub(tracker.callTimes[0])
			avgGap = totalGap / time.Duration(recentCalls-1)
		}
		tracker.mu.Unlock()
		
		ifs.logger.Info("ReadDir loop detected",
			"path", path,
			"total_calls", callNum,
			"avg_gap_ms", avgGap.Milliseconds(),
			"duration_s", start.Sub(tracker.firstCall).Seconds())
	}
	
	// Call the underlying filesystem
	fsStart := time.Now()
	entries, err := ifs.Filesystem.ReadDir(path)
	fsDuration := time.Since(fsStart)
	
	totalDuration := time.Since(start)
	overheadDuration := totalDuration - fsDuration
	
	// Log timing breakdown for slow calls or first few
	if totalDuration > 10*time.Millisecond || callNum <= 5 {
		ifs.logger.Info("ReadDir timing",
			"path", path,
			"call_number", callNum,
			"total_ms", totalDuration.Milliseconds(),
			"fs_ms", fsDuration.Milliseconds(),
			"overhead_ms", overheadDuration.Milliseconds(),
			"entries", len(entries),
			"error", err != nil)
	}
	
	// Record metrics
	metrics.GetGlobalCounters().RecordPathCall(path)
	
	return entries, err
}

// GetPathStats returns statistics for a specific path
func (ifs *InstrumentedFilesystem) GetPathStats(path string) map[string]interface{} {
	trackerVal, ok := ifs.pathCalls.Load(path)
	if !ok {
		return map[string]interface{}{
			"calls": 0,
		}
	}
	
	tracker := trackerVal.(*PathCallTracker)
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	
	callCount := tracker.callCount.Load()
	duration := tracker.lastCall.Sub(tracker.firstCall)
	
	var avgGap time.Duration
	if len(tracker.callTimes) > 1 {
		totalGap := tracker.callTimes[len(tracker.callTimes)-1].Sub(tracker.callTimes[0])
		avgGap = totalGap / time.Duration(len(tracker.callTimes)-1)
	}
	
	return map[string]interface{}{
		"total_calls":       callCount,
		"duration_seconds":  duration.Seconds(),
		"avg_gap_ms":        avgGap.Milliseconds(),
		"calls_per_second":  float64(callCount) / duration.Seconds(),
	}
}

// GetAllPathStats returns statistics for all paths
func (ifs *InstrumentedFilesystem) GetAllPathStats() map[string]interface{} {
	stats := make(map[string]interface{})
	
	ifs.pathCalls.Range(func(key, value interface{}) bool {
		path := key.(string)
		stats[path] = ifs.GetPathStats(path)
		return true
	})
	
	return stats
}

// Made with Bob
