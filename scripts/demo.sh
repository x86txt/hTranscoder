#!/bin/bash

# hTranscode Demo Script
# This script helps you quickly test the auto-discovery system

set -e

echo "=== hTranscode Demo Script ==="
echo ""

# Check if binaries exist
if [ ! -f "./htranscode" ] || [ ! -f "./worker" ]; then
    echo "Building binaries..."
    go build -o htranscode ./cmd/api
    go build -o worker ./cmd/worker
    echo "✓ Binaries built"
fi

# Start master server
echo ""
echo "Starting master server..."
./htranscode &
MASTER_PID=$!
echo "✓ Master server started (PID: $MASTER_PID)"

# Wait for server to start
sleep 3

# Start a worker
echo ""
echo "Starting worker with auto-discovery..."
./worker -name "demo-worker-1" &
WORKER1_PID=$!
echo "✓ Worker 1 started (PID: $WORKER1_PID)"

# Optional: Start a second worker with GPU
if [ "$1" == "--gpu" ]; then
    echo ""
    echo "Starting GPU worker..."
    ./worker -name "demo-gpu-worker" -gpu -gpu-device 0 &
    WORKER2_PID=$!
    echo "✓ GPU Worker started (PID: $WORKER2_PID)"
fi

echo ""
echo "==================================="
echo "✓ Demo system is running!"
echo ""
echo "Open your browser at: http://localhost:8080"
echo ""
echo "To stop the demo, press Ctrl+C"
echo "==================================="
echo ""

# Function to cleanup on exit
cleanup() {
    echo ""
    echo "Stopping demo..."
    kill $MASTER_PID 2>/dev/null || true
    kill $WORKER1_PID 2>/dev/null || true
    [ ! -z "$WORKER2_PID" ] && kill $WORKER2_PID 2>/dev/null || true
    echo "✓ Demo stopped"
    exit 0
}

# Set trap to cleanup on Ctrl+C
trap cleanup INT

# Wait indefinitely
while true; do
    sleep 1
done 