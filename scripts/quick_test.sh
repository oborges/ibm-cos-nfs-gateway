#!/bin/bash

# Quick 5-minute stress test for COS NFS Gateway
# This script runs basic performance tests to quickly validate the mountpoint

set -e

# Configuration
MOUNT_POINT="${MOUNT_POINT:-/mnt/cos-nfs}"
TEST_DIR="$MOUNT_POINT/quick-test-$$"

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

echo "=========================================="
echo "Quick COS NFS Gateway Performance Test"
echo "=========================================="
echo

# Check prerequisites
if ! command -v fio &> /dev/null; then
    echo -e "${RED}Error: fio is not installed${NC}"
    echo "Install with: sudo apt-get install fio (Ubuntu/Debian)"
    exit 1
fi

if ! mountpoint -q "$MOUNT_POINT" 2>/dev/null; then
    echo -e "${RED}Error: NFS is not mounted at $MOUNT_POINT${NC}"
    exit 1
fi

# Create test directory
mkdir -p "$TEST_DIR"
echo -e "${BLUE}Test directory: $TEST_DIR${NC}"
echo

# Test 1: Sequential Write
echo -e "${BLUE}Test 1: Sequential Write (100MB)${NC}"
fio --name=write --directory="$TEST_DIR" --rw=write \
    --bs=1M --size=100M --numjobs=1 --direct=0 2>&1 | \
    grep -E "write: IOPS=|bw=" | head -2
echo

# Test 2: Sequential Read
echo -e "${BLUE}Test 2: Sequential Read (100MB)${NC}"
fio --name=read --directory="$TEST_DIR" --rw=read \
    --bs=1M --size=100M --numjobs=1 --direct=0 2>&1 | \
    grep -E "read: IOPS=|bw=" | head -2
echo

# Test 3: Random IOPS (30 seconds)
echo -e "${BLUE}Test 3: Random Read/Write IOPS (30s)${NC}"
fio --name=random --directory="$TEST_DIR" --rw=randrw \
    --bs=4K --size=50M --numjobs=4 --runtime=30s \
    --time_based --direct=0 2>&1 | \
    grep -E "IOPS=" | head -2
echo

# Test 4: Small file operations
echo -e "${BLUE}Test 4: Small File Operations (100 files)${NC}"
small_dir="$TEST_DIR/small"
mkdir -p "$small_dir"

start=$(date +%s)
for i in {1..100}; do
    echo "test $i" > "$small_dir/file_$i.txt"
done
end=$(date +%s)
create_time=$((end - start))
echo "Created 100 files in ${create_time}s"

start=$(date +%s)
for i in {1..100}; do
    cat "$small_dir/file_$i.txt" > /dev/null
done
end=$(date +%s)
read_time=$((end - start))
echo "Read 100 files in ${read_time}s"
echo

# Cleanup
echo -e "${BLUE}Cleaning up...${NC}"
rm -rf "$TEST_DIR"

echo
echo -e "${GREEN}=== Test Complete ===${NC}"
echo
echo "Performance Summary:"
echo "-------------------"
echo "✓ Sequential write test completed"
echo "✓ Sequential read test completed"
echo "✓ Random IOPS test completed"
echo "✓ Small file operations completed"
echo
echo "For detailed stress testing, run:"
echo "  ./scripts/run_stress_tests.sh"
echo
echo "For full documentation, see:"
echo "  docs/STRESS_TESTING_GUIDE.md"
echo "  docs/QUICK_START_STRESS_TEST.md"

# Made with Bob
