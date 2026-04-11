#!/bin/bash

# Quick 5-minute stress test for COS NFS Gateway
# This script runs basic performance tests to quickly validate the mountpoint
# Usage: ./quick_test.sh [test_number]
#   test_number: 1, 2, 3, or 4 to run a specific test, or omit to run all tests

set -e

# Configuration
MOUNT_POINT="${MOUNT_POINT:-/mnt/cos-nfs}"
TEST_DIR="$MOUNT_POINT/quick-test-$$"
SPECIFIC_TEST="${1:-all}"

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
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
if [ "$SPECIFIC_TEST" != "all" ]; then
    echo -e "${YELLOW}Running only Test $SPECIFIC_TEST${NC}"
fi
echo

# Test 1: Sequential Write
if [ "$SPECIFIC_TEST" = "all" ] || [ "$SPECIFIC_TEST" = "1" ]; then
    echo -e "${BLUE}Test 1: Sequential Write (100MB)${NC}"
    fio --name=write --directory="$TEST_DIR" --rw=write \
        --bs=1M --size=100M --numjobs=1 --direct=0 2>&1 | \
        grep -E "write: IOPS=|bw=" | head -2
    echo
    
    # Wait a moment for file to be available
    if [ "$SPECIFIC_TEST" = "all" ]; then
        sleep 2
    fi
fi

# Test 2: Sequential Read
if [ "$SPECIFIC_TEST" = "all" ] || [ "$SPECIFIC_TEST" = "2" ]; then
    echo -e "${BLUE}Test 2: Sequential Read (100MB)${NC}"
    echo -e "${YELLOW}Debug: Checking if write.0.0 file exists...${NC}"
    if [ -f "$TEST_DIR/write.0.0" ]; then
        ls -lh "$TEST_DIR/write.0.0"
    else
        echo -e "${RED}Warning: write.0.0 file not found!${NC}"
    fi
    echo -e "${YELLOW}Running fio read test...${NC}"
    fio --name=write --directory="$TEST_DIR" --rw=read \
        --bs=1M --size=100M --numjobs=1 --direct=0 --readonly 2>&1
    echo
fi

# Test 3: Random IOPS (30 seconds)
if [ "$SPECIFIC_TEST" = "all" ] || [ "$SPECIFIC_TEST" = "3" ]; then
    echo -e "${BLUE}Test 3: Random Read/Write IOPS (30s)${NC}"
    fio --name=random --directory="$TEST_DIR" --rw=randrw \
        --bs=4K --size=50M --numjobs=4 --runtime=30s \
        --time_based --direct=0 2>&1 | \
        grep -E "IOPS=" | head -2
    echo
fi

# Test 4: Small file operations
if [ "$SPECIFIC_TEST" = "all" ] || [ "$SPECIFIC_TEST" = "4" ]; then
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
fi

# Cleanup
if [ "$SPECIFIC_TEST" = "all" ]; then
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
else
    echo -e "${YELLOW}Test $SPECIFIC_TEST completed. Test directory preserved: $TEST_DIR${NC}"
    echo -e "${YELLOW}To cleanup manually: rm -rf $TEST_DIR${NC}"
fi
echo "For full documentation, see:"
echo "  docs/STRESS_TESTING_GUIDE.md"
echo "  docs/QUICK_START_STRESS_TEST.md"

# Made with Bob
