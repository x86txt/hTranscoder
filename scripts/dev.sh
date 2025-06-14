#!/bin/bash

# hTranscode Development Script
# Runs both the API server and worker with hot reloading

set -e

echo "🚀 Starting hTranscode development environment..."
echo ""

# Function to cleanup background processes on exit
cleanup() {
    echo ""
    echo "🛑 Shutting down development environment..."
    jobs -p | xargs -r kill
    exit
}

# Set trap to cleanup on script exit
trap cleanup EXIT INT TERM

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if Air is installed
if ! command -v air &> /dev/null; then
    echo -e "${RED}❌ Air is not installed. Please install it first:${NC}"
    echo "   go install github.com/air-verse/air@latest"
    exit 1
fi

# Create tmp directory if it doesn't exist
mkdir -p tmp

echo -e "${GREEN}✅ Starting API server with hot reloading...${NC}"
air &
SERVER_PID=$!

# Wait a moment for the server to start
sleep 2

echo -e "${BLUE}✅ Starting worker with hot reloading...${NC}"
air -c .air-worker.toml &
WORKER_PID=$!

echo ""
echo -e "${YELLOW}🎯 Development environment ready!${NC}"
echo -e "   📡 API Server: http://localhost:8080"
echo -e "   👷 Worker: Auto-discovering server and detecting GPU..."
echo ""
echo -e "${YELLOW}📝 Logs:${NC}"
echo -e "   Server: tmp/build-errors.log"
echo -e "   Worker: tmp/build-errors-worker.log"
echo ""
echo -e "${YELLOW}💡 Features:${NC}"
echo -e "   🔍 Auto-discovery: Workers find server automatically"
echo -e "   🎮 GPU Detection: NVIDIA GPUs detected and used automatically"
echo -e "   📊 Real-time Monitoring: IP addresses and latency tracking"
echo ""
echo -e "${GREEN}Press Ctrl+C to stop both services${NC}"
echo ""

# Wait for background processes
wait 