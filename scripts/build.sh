#!/bin/bash

# hTranscode Build Script
# Compiles Go applications for multiple architectures

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Build configurations
declare -A PLATFORMS
PLATFORMS=(
    ["linux-amd64"]="linux amd64"
    ["linux-arm64"]="linux arm64"
    ["windows-amd64"]="windows amd64"
    ["darwin-arm64"]="darwin arm64"
    ["default"]="default default"
)

# Applications to build
APPS=("api" "worker")

# Default build directory
BUILD_DIR="build"

# Function to print usage
usage() {
    echo -e "${BLUE}Usage: $0 [OPTIONS]${NC}"
    echo ""
    echo -e "${YELLOW}Options:${NC}"
    echo "  -a, --arch ARCH     Target architecture (can be specified multiple times)"
    echo "                      Available: linux-amd64, linux-arm64, windows-amd64, darwin-arm64, default"
    echo "  -o, --output DIR    Output directory (default: build)"
    echo "  -h, --help          Show this help message"
    echo ""
    echo -e "${YELLOW}Examples:${NC}"
    echo "  $0                                    # Build for all architectures"
    echo "  $0 -a linux-amd64                    # Build for Linux x86_64 only"
    echo "  $0 -a linux-amd64 -a windows-amd64   # Build for Linux x86_64 and Windows"
    echo "  $0 -a default                        # Build for current system"
    echo "  $0 -o dist                           # Build all to 'dist' directory"
}

# Function to build for a specific platform
build_platform() {
    local platform=$1
    local goos=$2
    local goarch=$3
    
    echo -e "${BLUE}Building for $platform...${NC}"
    
    for app in "${APPS[@]}"; do
        local output_name="$app"
        local output_dir="$BUILD_DIR"
        
        if [ "$platform" != "default" ]; then
            output_dir="$BUILD_DIR/$platform"
            mkdir -p "$output_dir"
        fi
        
        # Add .exe extension for Windows
        if [ "$goos" = "windows" ]; then
            output_name="${app}.exe"
        fi
        
        local output_path="$output_dir/$output_name"
        
        echo -e "  ${YELLOW}Building $app...${NC}"
        
        if [ "$platform" = "default" ]; then
            # Build with system defaults
            if ! go build -o "$output_path" "./cmd/$app"; then
                echo -e "  ${RED}Failed to build $app for $platform${NC}"
                return 1
            fi
        else
            # Build with specific GOOS and GOARCH
            if ! GOOS="$goos" GOARCH="$goarch" go build -o "$output_path" "./cmd/$app"; then
                echo -e "  ${RED}Failed to build $app for $platform${NC}"
                return 1
            fi
        fi
        
        echo -e "  ${GREEN}✓ $app built successfully -> $output_path${NC}"
    done
    
    echo -e "${GREEN}✓ Completed builds for $platform${NC}"
    echo ""
}

# Function to clean build directory
clean_build() {
    if [ -d "$BUILD_DIR" ]; then
        echo -e "${YELLOW}Cleaning build directory...${NC}"
        rm -rf "$BUILD_DIR"
    fi
}

# Parse command line arguments
SELECTED_PLATFORMS=()
while [[ $# -gt 0 ]]; do
    case $1 in
        -a|--arch)
            if [ -z "$2" ]; then
                echo -e "${RED}Error: Architecture not specified${NC}"
                usage
                exit 1
            fi
            if [[ ! " ${!PLATFORMS[@]} " =~ " $2 " ]]; then
                echo -e "${RED}Error: Unknown architecture '$2'${NC}"
                echo -e "${YELLOW}Available architectures: ${!PLATFORMS[@]}${NC}"
                exit 1
            fi
            SELECTED_PLATFORMS+=("$2")
            shift 2
            ;;
        -o|--output)
            if [ -z "$2" ]; then
                echo -e "${RED}Error: Output directory not specified${NC}"
                usage
                exit 1
            fi
            BUILD_DIR="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo -e "${RED}Error: Unknown option '$1'${NC}"
            usage
            exit 1
            ;;
    esac
done

# If no platforms specified, build for all
if [ ${#SELECTED_PLATFORMS[@]} -eq 0 ]; then
    SELECTED_PLATFORMS=(${!PLATFORMS[@]})
fi

# Print build information
echo -e "${BLUE}hTranscode Build Script${NC}"
echo -e "${BLUE}=======================${NC}"
echo -e "Applications: ${APPS[*]}"
echo -e "Platforms: ${SELECTED_PLATFORMS[*]}"
echo -e "Output directory: $BUILD_DIR"
echo ""

# Check if go is installed
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed or not in PATH${NC}"
    exit 1
fi

# Check if we're in the right directory (has go.mod)
if [ ! -f "go.mod" ]; then
    echo -e "${RED}Error: go.mod not found. Please run this script from the project root.${NC}"
    exit 1
fi

# Clean and create build directory
clean_build
mkdir -p "$BUILD_DIR"

# Track build results
SUCCESSFUL_BUILDS=()
FAILED_BUILDS=()

# Build for each selected platform
for platform in "${SELECTED_PLATFORMS[@]}"; do
    IFS=' ' read -r goos goarch <<< "${PLATFORMS[$platform]}"
    
    if build_platform "$platform" "$goos" "$goarch"; then
        SUCCESSFUL_BUILDS+=("$platform")
    else
        FAILED_BUILDS+=("$platform")
    fi
done

# Print summary
echo -e "${BLUE}Build Summary${NC}"
echo -e "${BLUE}=============${NC}"

if [ ${#SUCCESSFUL_BUILDS[@]} -gt 0 ]; then
    echo -e "${GREEN}Successful builds (${#SUCCESSFUL_BUILDS[@]}):${NC}"
    for platform in "${SUCCESSFUL_BUILDS[@]}"; do
        echo -e "  ${GREEN}✓ $platform${NC}"
    done
fi

if [ ${#FAILED_BUILDS[@]} -gt 0 ]; then
    echo -e "${RED}Failed builds (${#FAILED_BUILDS[@]}):${NC}"
    for platform in "${FAILED_BUILDS[@]}"; do
        echo -e "  ${RED}✗ $platform${NC}"
    done
    exit 1
fi

echo -e "${GREEN}All builds completed successfully!${NC}"
echo -e "Binaries are available in: ${BUILD_DIR}/"
