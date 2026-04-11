#!/bin/bash

# Comprehensive Stress Test Script for COS NFS Gateway
# This script runs a full suite of performance and stability tests

set -e

# Configuration
MOUNT_POINT="${MOUNT_POINT:-/mnt/cos-nfs}"
TEST_DIR="$MOUNT_POINT/stress-test-$(date +%Y%m%d-%H%M%S)"
RESULTS_DIR="./test-results-$(date +%Y%m%d-%H%M%S)"
LOG_FILE="$RESULTS_DIR/test.log"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log() {
    echo -e "${BLUE}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} $1" | tee -a "$LOG_FILE"
}

log_success() {
    echo -e "${GREEN}[$(date +'%Y-%m-%d %H:%M:%S')] ✓${NC} $1" | tee -a "$LOG_FILE"
}

log_error() {
    echo -e "${RED}[$(date +'%Y-%m-%d %H:%M:%S')] ✗${NC} $1" | tee -a "$LOG_FILE"
}

log_warning() {
    echo -e "${YELLOW}[$(date +'%Y-%m-%d %H:%M:%S')] ⚠${NC} $1" | tee -a "$LOG_FILE"
}

# Check prerequisites
check_prerequisites() {
    log "Checking prerequisites..."
    
    # Check if fio is installed
    if ! command -v fio &> /dev/null; then
        log_error "fio is not installed. Please install it first:"
        echo "  Ubuntu/Debian: sudo apt-get install fio"
        echo "  macOS: brew install fio"
        echo "  RHEL/CentOS: sudo yum install fio"
        exit 1
    fi
    
    # Check if mount point exists and is mounted
    if [ ! -d "$MOUNT_POINT" ]; then
        log_error "Mount point $MOUNT_POINT does not exist"
        exit 1
    fi
    
    if ! mountpoint -q "$MOUNT_POINT" 2>/dev/null; then
        log_error "NFS is not mounted at $MOUNT_POINT"
        echo "Please mount the NFS share first:"
        echo "  sudo mount -t nfs -o vers=3,tcp,rsize=1048576,wsize=1048576 localhost:/bucket $MOUNT_POINT"
        exit 1
    fi
    
    log_success "Prerequisites check passed"
}

# Create test directories
setup_test_environment() {
    log "Setting up test environment..."
    
    mkdir -p "$RESULTS_DIR"
    mkdir -p "$TEST_DIR"
    
    log_success "Test directory created: $TEST_DIR"
    log_success "Results directory created: $RESULTS_DIR"
}

# Test 1: Sequential Write Performance
test_sequential_write() {
    log "Test 1: Sequential Write Performance (100MB, 1MB blocks)"
    
    fio --name=seq-write \
        --directory="$TEST_DIR" \
        --rw=write \
        --bs=1M \
        --size=100M \
        --numjobs=1 \
        --direct=0 \
        --group_reporting \
        --output="$RESULTS_DIR/01-seq-write.json" \
        --output-format=json \
        2>&1 | tee -a "$LOG_FILE"
    
    # Extract and display key metrics
    local bw=$(jq -r '.jobs[0].write.bw' "$RESULTS_DIR/01-seq-write.json" 2>/dev/null || echo "0")
    local iops=$(jq -r '.jobs[0].write.iops' "$RESULTS_DIR/01-seq-write.json" 2>/dev/null || echo "0")
    
    log_success "Sequential Write: ${bw} KB/s ($(echo "scale=2; $bw/1024" | bc) MB/s), IOPS: $iops"
    
    # Check if meets target (20 MB/s = 20480 KB/s)
    if (( $(echo "$bw > 20480" | bc -l) )); then
        log_success "✓ Meets target (>20 MB/s)"
    else
        log_warning "⚠ Below target (>20 MB/s)"
    fi
}

# Test 2: Sequential Read Performance
test_sequential_read() {
    log "Test 2: Sequential Read Performance (100MB, 1MB blocks)"
    
    fio --name=seq-read \
        --directory="$TEST_DIR" \
        --rw=read \
        --bs=1M \
        --size=100M \
        --numjobs=1 \
        --direct=0 \
        --group_reporting \
        --output="$RESULTS_DIR/02-seq-read.json" \
        --output-format=json \
        2>&1 | tee -a "$LOG_FILE"
    
    local bw=$(jq -r '.jobs[0].read.bw' "$RESULTS_DIR/02-seq-read.json" 2>/dev/null || echo "0")
    local iops=$(jq -r '.jobs[0].read.iops' "$RESULTS_DIR/02-seq-read.json" 2>/dev/null || echo "0")
    
    log_success "Sequential Read: ${bw} KB/s ($(echo "scale=2; $bw/1024" | bc) MB/s), IOPS: $iops"
    
    # Check if meets target (50 MB/s = 51200 KB/s)
    if (( $(echo "$bw > 51200" | bc -l) )); then
        log_success "✓ Meets target (>50 MB/s)"
    else
        log_warning "⚠ Below target (>50 MB/s)"
    fi
}

# Test 3: Random Read Performance
test_random_read() {
    log "Test 3: Random Read Performance (4K blocks, 4 jobs, 60s)"
    
    fio --name=rand-read \
        --directory="$TEST_DIR" \
        --rw=randread \
        --bs=4K \
        --size=100M \
        --numjobs=4 \
        --direct=0 \
        --time_based \
        --runtime=60s \
        --group_reporting \
        --output="$RESULTS_DIR/03-rand-read.json" \
        --output-format=json \
        2>&1 | tee -a "$LOG_FILE"
    
    local iops=$(jq -r '.jobs[0].read.iops' "$RESULTS_DIR/03-rand-read.json" 2>/dev/null || echo "0")
    local lat=$(jq -r '.jobs[0].read.lat_ns.mean' "$RESULTS_DIR/03-rand-read.json" 2>/dev/null || echo "0")
    local lat_ms=$(echo "scale=2; $lat/1000000" | bc)
    
    log_success "Random Read: IOPS: $iops, Avg Latency: ${lat_ms}ms"
    
    # Check if meets target (100 IOPS)
    if (( $(echo "$iops > 100" | bc -l) )); then
        log_success "✓ Meets target (>100 IOPS)"
    else
        log_warning "⚠ Below target (>100 IOPS)"
    fi
}

# Test 4: Random Write Performance
test_random_write() {
    log "Test 4: Random Write Performance (4K blocks, 4 jobs, 60s)"
    
    fio --name=rand-write \
        --directory="$TEST_DIR" \
        --rw=randwrite \
        --bs=4K \
        --size=100M \
        --numjobs=4 \
        --direct=0 \
        --time_based \
        --runtime=60s \
        --group_reporting \
        --output="$RESULTS_DIR/04-rand-write.json" \
        --output-format=json \
        2>&1 | tee -a "$LOG_FILE"
    
    local iops=$(jq -r '.jobs[0].write.iops' "$RESULTS_DIR/04-rand-write.json" 2>/dev/null || echo "0")
    local lat=$(jq -r '.jobs[0].write.lat_ns.mean' "$RESULTS_DIR/04-rand-write.json" 2>/dev/null || echo "0")
    local lat_ms=$(echo "scale=2; $lat/1000000" | bc)
    
    log_success "Random Write: IOPS: $iops, Avg Latency: ${lat_ms}ms"
    
    # Check if meets target (50 IOPS)
    if (( $(echo "$iops > 50" | bc -l) )); then
        log_success "✓ Meets target (>50 IOPS)"
    else
        log_warning "⚠ Below target (>50 IOPS)"
    fi
}

# Test 5: Mixed Read/Write Workload
test_mixed_workload() {
    log "Test 5: Mixed Read/Write Workload (70/30, 64K blocks, 4 jobs, 60s)"
    
    fio --name=mixed-rw \
        --directory="$TEST_DIR" \
        --rw=randrw \
        --rwmixread=70 \
        --bs=64K \
        --size=100M \
        --numjobs=4 \
        --direct=0 \
        --time_based \
        --runtime=60s \
        --group_reporting \
        --output="$RESULTS_DIR/05-mixed-rw.json" \
        --output-format=json \
        2>&1 | tee -a "$LOG_FILE"
    
    local read_iops=$(jq -r '.jobs[0].read.iops' "$RESULTS_DIR/05-mixed-rw.json" 2>/dev/null || echo "0")
    local write_iops=$(jq -r '.jobs[0].write.iops' "$RESULTS_DIR/05-mixed-rw.json" 2>/dev/null || echo "0")
    
    log_success "Mixed Workload: Read IOPS: $read_iops, Write IOPS: $write_iops"
}

# Test 6: Small File Operations
test_small_files() {
    log "Test 6: Small File Operations (1000 files)"
    
    local small_dir="$TEST_DIR/small-files"
    mkdir -p "$small_dir"
    
    # Create files
    log "Creating 1000 small files..."
    local start_time=$(date +%s)
    for i in {1..1000}; do
        echo "test data $i" > "$small_dir/file_$i.txt"
    done
    local end_time=$(date +%s)
    local create_time=$((end_time - start_time))
    
    log_success "Created 1000 files in ${create_time}s"
    
    # Read files
    log "Reading 1000 small files..."
    start_time=$(date +%s)
    for i in {1..1000}; do
        cat "$small_dir/file_$i.txt" > /dev/null
    done
    end_time=$(date +%s)
    local read_time=$((end_time - start_time))
    
    log_success "Read 1000 files in ${read_time}s"
    
    # Delete files
    log "Deleting 1000 small files..."
    start_time=$(date +%s)
    rm -f "$small_dir"/file_*.txt
    end_time=$(date +%s)
    local delete_time=$((end_time - start_time))
    
    log_success "Deleted 1000 files in ${delete_time}s"
    
    # Check targets
    if [ $create_time -lt 60 ]; then
        log_success "✓ Create time meets target (<60s)"
    else
        log_warning "⚠ Create time above target (<60s)"
    fi
}

# Test 7: Large File Operations
test_large_file() {
    log "Test 7: Large File Operations (500MB)"
    
    # Write large file
    log "Writing 500MB file..."
    local start_time=$(date +%s)
    dd if=/dev/zero of="$TEST_DIR/large-file" bs=1M count=500 2>&1 | tee -a "$LOG_FILE"
    local end_time=$(date +%s)
    local write_time=$((end_time - start_time))
    local write_speed=$(echo "scale=2; 500/$write_time" | bc)
    
    log_success "Wrote 500MB in ${write_time}s (${write_speed} MB/s)"
    
    # Read large file
    log "Reading 500MB file..."
    start_time=$(date +%s)
    dd if="$TEST_DIR/large-file" of=/dev/null bs=1M 2>&1 | tee -a "$LOG_FILE"
    end_time=$(date +%s)
    local read_time=$((end_time - start_time))
    local read_speed=$(echo "scale=2; 500/$read_time" | bc)
    
    log_success "Read 500MB in ${read_time}s (${read_speed} MB/s)"
    
    # Check targets
    if (( $(echo "$write_speed > 20" | bc -l) )); then
        log_success "✓ Write speed meets target (>20 MB/s)"
    else
        log_warning "⚠ Write speed below target (>20 MB/s)"
    fi
    
    if (( $(echo "$read_speed > 50" | bc -l) )); then
        log_success "✓ Read speed meets target (>50 MB/s)"
    else
        log_warning "⚠ Read speed below target (>50 MB/s)"
    fi
}

# Test 8: Direct I/O
test_direct_io() {
    log "Test 8: Direct I/O (bypass cache)"
    
    # Create test file if not exists
    if [ ! -f "$TEST_DIR/large-file" ]; then
        dd if=/dev/zero of="$TEST_DIR/large-file" bs=1M count=100 2>&1 | tee -a "$LOG_FILE"
    fi
    
    # Direct I/O read
    log "Testing direct I/O read..."
    dd if="$TEST_DIR/large-file" of=/dev/null bs=16K count=1000 iflag=direct 2>&1 | tee -a "$LOG_FILE"
    
    if [ $? -eq 0 ]; then
        log_success "✓ Direct I/O read successful"
    else
        log_error "✗ Direct I/O read failed"
    fi
    
    # Direct I/O write
    log "Testing direct I/O write..."
    dd if=/dev/zero of="$TEST_DIR/direct-test" bs=16K count=1000 oflag=direct 2>&1 | tee -a "$LOG_FILE"
    
    if [ $? -eq 0 ]; then
        log_success "✓ Direct I/O write successful"
    else
        log_error "✗ Direct I/O write failed"
    fi
}

# Test 9: Concurrent Access
test_concurrent_access() {
    log "Test 9: Concurrent Access (8 jobs, 60s)"
    
    fio --name=concurrent \
        --directory="$TEST_DIR" \
        --rw=randrw \
        --bs=64K \
        --size=50M \
        --numjobs=8 \
        --direct=0 \
        --time_based \
        --runtime=60s \
        --group_reporting \
        --output="$RESULTS_DIR/09-concurrent.json" \
        --output-format=json \
        2>&1 | tee -a "$LOG_FILE"
    
    local read_bw=$(jq -r '.jobs[0].read.bw' "$RESULTS_DIR/09-concurrent.json" 2>/dev/null || echo "0")
    local write_bw=$(jq -r '.jobs[0].write.bw' "$RESULTS_DIR/09-concurrent.json" 2>/dev/null || echo "0")
    local total_bw=$(echo "scale=2; ($read_bw + $write_bw)/1024" | bc)
    
    log_success "Concurrent Access: Total throughput: ${total_bw} MB/s"
    
    if (( $(echo "$total_bw > 20" | bc -l) )); then
        log_success "✓ Meets target (>20 MB/s aggregate)"
    else
        log_warning "⚠ Below target (>20 MB/s aggregate)"
    fi
}

# Generate summary report
generate_summary() {
    log "Generating summary report..."
    
    local summary_file="$RESULTS_DIR/SUMMARY.txt"
    
    cat > "$summary_file" << EOF
=================================================================
COS NFS Gateway Stress Test Summary
=================================================================
Test Date: $(date)
Mount Point: $MOUNT_POINT
Test Directory: $TEST_DIR

-----------------------------------------------------------------
Performance Results:
-----------------------------------------------------------------
EOF
    
    # Extract key metrics from JSON files
    if [ -f "$RESULTS_DIR/01-seq-write.json" ]; then
        local bw=$(jq -r '.jobs[0].write.bw' "$RESULTS_DIR/01-seq-write.json" 2>/dev/null || echo "0")
        echo "Sequential Write: $(echo "scale=2; $bw/1024" | bc) MB/s" >> "$summary_file"
    fi
    
    if [ -f "$RESULTS_DIR/02-seq-read.json" ]; then
        local bw=$(jq -r '.jobs[0].read.bw' "$RESULTS_DIR/02-seq-read.json" 2>/dev/null || echo "0")
        echo "Sequential Read:  $(echo "scale=2; $bw/1024" | bc) MB/s" >> "$summary_file"
    fi
    
    if [ -f "$RESULTS_DIR/03-rand-read.json" ]; then
        local iops=$(jq -r '.jobs[0].read.iops' "$RESULTS_DIR/03-rand-read.json" 2>/dev/null || echo "0")
        echo "Random Read IOPS: $iops" >> "$summary_file"
    fi
    
    if [ -f "$RESULTS_DIR/04-rand-write.json" ]; then
        local iops=$(jq -r '.jobs[0].write.iops' "$RESULTS_DIR/04-rand-write.json" 2>/dev/null || echo "0")
        echo "Random Write IOPS: $iops" >> "$summary_file"
    fi
    
    cat >> "$summary_file" << EOF

-----------------------------------------------------------------
Performance Targets:
-----------------------------------------------------------------
Sequential Read:  >50 MB/s (target: 100+ MB/s)
Sequential Write: >20 MB/s (target: 50+ MB/s)
Random Read IOPS: >100 (4K blocks)
Random Write IOPS: >50 (4K blocks)

-----------------------------------------------------------------
Test Files Location:
-----------------------------------------------------------------
Results: $RESULTS_DIR
Test Data: $TEST_DIR

=================================================================
EOF
    
    cat "$summary_file"
    log_success "Summary report saved to: $summary_file"
}

# Cleanup
cleanup() {
    log "Cleaning up test files..."
    
    # Ask user if they want to keep test files
    read -p "Do you want to delete test files in $TEST_DIR? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        rm -rf "$TEST_DIR"
        log_success "Test files deleted"
    else
        log "Test files kept at: $TEST_DIR"
    fi
}

# Main execution
main() {
    echo "=========================================="
    echo "COS NFS Gateway Stress Test Suite"
    echo "=========================================="
    echo
    
    check_prerequisites
    setup_test_environment
    
    echo
    log "Starting stress tests..."
    echo
    
    # Run all tests
    test_sequential_write
    echo
    test_sequential_read
    echo
    test_random_read
    echo
    test_random_write
    echo
    test_mixed_workload
    echo
    test_small_files
    echo
    test_large_file
    echo
    test_direct_io
    echo
    test_concurrent_access
    echo
    
    # Generate summary
    generate_summary
    
    echo
    log_success "All tests completed!"
    echo
    
    # Cleanup
    cleanup
}

# Run main function
main "$@"

# Made with Bob
