#!/bin/bash
# Script to capture READDIR trace for directory listing analysis

set -e

if [ $# -lt 1 ]; then
    echo "Usage: $0 <mount_point> [directory]"
    echo "Example: $0 /mnt/nfs quick-test-27156/small"
    exit 1
fi

MOUNT_POINT="$1"
TEST_DIR="${2:-quick-test-27156/small}"
GATEWAY_URL="http://localhost:8080"

echo "=== READDIR Trace Capture ==="
echo "Mount point: $MOUNT_POINT"
echo "Test directory: $TEST_DIR"
echo ""

# Enable tracing
echo "1. Enabling READDIR tracing..."
curl -s -X POST "$GATEWAY_URL/debug/readdir/enable" || {
    echo "Failed to enable tracing. Is the gateway running?"
    exit 1
}
echo ""

# Clear any existing traces
echo "2. Clearing old traces..."
curl -s -X POST "$GATEWAY_URL/debug/readdir/clear"
echo ""

# Wait a moment
sleep 1

# Perform directory listing
echo "3. Performing directory listing..."
FULL_PATH="$MOUNT_POINT/$TEST_DIR"
echo "   ls -la $FULL_PATH"
START_TIME=$(date +%s)
ls -la "$FULL_PATH" > /dev/null 2>&1
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))
echo "   Completed in ${DURATION}s"
echo ""

# Wait for any pending operations
sleep 2

# Get trace summary
echo "4. Fetching trace summary..."
echo ""
curl -s "$GATEWAY_URL/debug/readdir/traces" | jq '.'
echo ""

# Get detailed trace for the specific path
echo ""
echo "5. Detailed trace for $TEST_DIR:"
echo "========================================"
# URL encode the path
ENCODED_PATH=$(python3 -c "import urllib.parse; print(urllib.parse.quote('/$TEST_DIR'))")
curl -s "$GATEWAY_URL/debug/readdir/trace?path=$ENCODED_PATH"
echo ""
echo "========================================"
echo ""

# Disable tracing
echo "6. Disabling READDIR tracing..."
curl -s -X POST "$GATEWAY_URL/debug/readdir/disable"
echo ""

echo ""
echo "=== Analysis Complete ==="
echo ""
echo "To view traces again:"
echo "  curl $GATEWAY_URL/debug/readdir/traces | jq"
echo ""
echo "To get detailed trace:"
echo "  curl '$GATEWAY_URL/debug/readdir/trace?path=$ENCODED_PATH'"
echo ""
echo "To clear traces:"
echo "  curl -X POST $GATEWAY_URL/debug/readdir/clear"

# Made with Bob
