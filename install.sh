#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running as root
if [[ $EUID -eq 0 ]]; then
   log_error "This script should not be run as root for security reasons"
   log_info "Please run as a regular user. The script will use sudo when needed."
   exit 1
fi

log_info "🚀 hTranscode Installation Script"
echo "This script will install Go, FFmpeg, and other dependencies for hTranscode."
echo

# Detect OS and package manager
detect_os() {
    if [[ -f /etc/os-release ]]; then
        . /etc/os-release
        OS=$ID
        OS_VERSION=$VERSION_ID
    else
        log_error "Cannot detect OS. /etc/os-release not found."
        exit 1
    fi

    # Detect package manager
    if command -v apt >/dev/null 2>&1; then
        PKG_MANAGER="apt"
        PKG_UPDATE="sudo apt update"
        PKG_INSTALL="sudo apt install -y"
    elif command -v dnf >/dev/null 2>&1; then
        PKG_MANAGER="dnf"
        PKG_UPDATE="sudo dnf check-update || true"
        PKG_INSTALL="sudo dnf install -y"
    elif command -v yum >/dev/null 2>&1; then
        PKG_MANAGER="yum"
        PKG_UPDATE="sudo yum check-update || true"
        PKG_INSTALL="sudo yum install -y"
    else
        log_error "Unsupported package manager. This script supports apt (Ubuntu/Debian) and dnf/yum (CentOS/RHEL/Fedora)."
        exit 1
    fi

    log_info "Detected OS: $OS $OS_VERSION"
    log_info "Package Manager: $PKG_MANAGER"
}

# Check if a package is installed
is_package_installed() {
    case $PKG_MANAGER in
        apt)
            dpkg -l | grep -q "^ii  $1 " 2>/dev/null
            ;;
        dnf|yum)
            rpm -q "$1" >/dev/null 2>&1
            ;;
    esac
}

# Check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Install Go
install_go() {
    if command_exists go; then
        GO_VERSION=$(go version | cut -d' ' -f3 | sed 's/go//')
        log_success "Go is already installed: $GO_VERSION"
        return
    fi

    log_info "Installing Go..."
    case $PKG_MANAGER in
        apt)
            if ! is_package_installed golang-go; then
                $PKG_INSTALL golang-go
            fi
            ;;
        dnf|yum)
            if ! is_package_installed golang; then
                $PKG_INSTALL golang
            fi
            ;;
    esac

    # Verify installation
    if command_exists go; then
        GO_VERSION=$(go version | cut -d' ' -f3 | sed 's/go//')
        log_success "Go installed successfully: $GO_VERSION"
    else
        log_error "Go installation failed"
        exit 1
    fi
}

# Install FFmpeg
install_ffmpeg() {
    if command_exists ffmpeg && command_exists ffprobe; then
        FFMPEG_VERSION=$(ffmpeg -version 2>/dev/null | head -n1 | cut -d' ' -f3)
        log_success "FFmpeg is already installed: $FFMPEG_VERSION"
        return
    fi

    log_info "Installing FFmpeg..."
    case $PKG_MANAGER in
        apt)
            if ! is_package_installed ffmpeg; then
                $PKG_INSTALL ffmpeg
            fi
            ;;
        dnf|yum)
            # Enable RPM Fusion for FFmpeg on RHEL-based systems
            if [[ "$OS" == "centos" ]] || [[ "$OS" == "rhel" ]] || [[ "$OS" == "rocky" ]]; then
                if ! rpm -q rpmfusion-free-release >/dev/null 2>&1; then
                    log_info "Enabling RPM Fusion repository for FFmpeg..."
                    sudo $PKG_MANAGER install -y "https://mirrors.rpmfusion.org/free/el/rpmfusion-free-release-$(rpm -E %rhel).noarch.rpm"
                fi
            fi
            if ! is_package_installed ffmpeg; then
                $PKG_INSTALL ffmpeg ffmpeg-devel
            fi
            ;;
    esac

    # Verify installation
    if command_exists ffmpeg && command_exists ffprobe; then
        FFMPEG_VERSION=$(ffmpeg -version 2>/dev/null | head -n1 | cut -d' ' -f3)
        log_success "FFmpeg installed successfully: $FFMPEG_VERSION"
    else
        log_error "FFmpeg installation failed"
        exit 1
    fi
}

# Install Git and build tools
install_build_tools() {
    log_info "Installing Git and build tools..."
    
    case $PKG_MANAGER in
        apt)
            $PKG_INSTALL git build-essential curl
            ;;
        dnf|yum)
            $PKG_INSTALL git gcc gcc-c++ make curl
            ;;
    esac
    
    log_success "Build tools installed successfully"
}

# Check for NVIDIA GPU and drivers
check_nvidia() {
    if command_exists nvidia-smi; then
        GPU_INFO=$(nvidia-smi --query-gpu=name --format=csv,noheader,nounits 2>/dev/null | head -1)
        log_success "NVIDIA GPU detected: $GPU_INFO"
        log_info "GPU acceleration will be available"
    else
        log_warning "NVIDIA GPU or drivers not detected"
        log_info "hTranscode will use CPU-only transcoding"
    fi
}

# Clone and build hTranscode
setup_htranscode() {
    INSTALL_DIR="$HOME/hTranscode"
    
    if [[ -d "$INSTALL_DIR" ]]; then
        log_warning "hTranscode directory already exists at $INSTALL_DIR"
        read -p "Do you want to remove it and reinstall? (y/N): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            rm -rf "$INSTALL_DIR"
        else
            log_info "Skipping hTranscode setup"
            return
        fi
    fi

    log_info "Cloning hTranscode repository..."
    git clone https://github.com/yourusername/hTranscode.git "$INSTALL_DIR"
    cd "$INSTALL_DIR"

    log_info "Installing Go dependencies..."
    go mod download

    log_info "Setting up Tailwind CSS..."
    cd bin/tailwind
    chmod +x download-tailwindcss.sh
    ./download-tailwindcss.sh
    cd ../..

    log_info "Building hTranscode..."
    go build -o api ./cmd/api
    go build -o worker ./cmd/worker

    log_info "Generating CSS..."
    ./bin/tailwind/tailwindcss -i ./cmd/templates/styles/index.css -o ./cmd/static/styles.css --minify

    log_success "hTranscode built successfully!"
    log_info "Installation directory: $INSTALL_DIR"
}

# Create systemd services
create_systemd_services() {
    read -p "Do you want to create systemd services for automatic startup? (y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        return
    fi

    INSTALL_DIR="$HOME/hTranscode"
    SERVICE_DIR="/etc/systemd/system"

    log_info "Creating systemd service for hTranscode master..."
    sudo tee "$SERVICE_DIR/htranscode-master.service" > /dev/null <<EOF
[Unit]
Description=hTranscode Master Server
After=network.target

[Service]
Type=simple
User=$USER
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/api
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    log_info "Creating systemd service for hTranscode worker..."
    sudo tee "$SERVICE_DIR/htranscode-worker.service" > /dev/null <<EOF
[Unit]
Description=hTranscode Worker
After=network.target htranscode-master.service

[Service]
Type=simple
User=$USER
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/worker
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    
    log_success "Systemd services created!"
    log_info "To start services:"
    log_info "  sudo systemctl start htranscode-master"
    log_info "  sudo systemctl start htranscode-worker"
    log_info "To enable auto-start:"
    log_info "  sudo systemctl enable htranscode-master"
    log_info "  sudo systemctl enable htranscode-worker"
}

# Print final instructions
print_final_instructions() {
    INSTALL_DIR="$HOME/hTranscode"
    
    echo
    log_success "🎉 hTranscode installation completed successfully!"
    echo
    log_info "📁 Installation directory: $INSTALL_DIR"
    log_info "🚀 To start the master server:"
    echo "  cd $INSTALL_DIR && ./api"
    echo
    log_info "👷 To start a worker:"
    echo "  cd $INSTALL_DIR && ./worker"
    echo
    log_info "🌐 Web interface will be available at:"
    echo "  https://localhost:8080"
    echo
    log_info "📚 Documentation:"
    echo "  README.md     - Usage guide"
    echo "  CHUNKING.md   - Video chunking system"
    echo "  CONFIG.md     - Configuration options"
    echo
    log_warning "🔒 First run will generate SSL certificates"
    log_warning "🔑 Share the .htranscode.key file with remote workers"
}

# Main installation flow
main() {
    detect_os
    
    log_info "Updating package lists..."
    $PKG_UPDATE
    
    install_build_tools
    install_go
    install_ffmpeg
    check_nvidia
    setup_htranscode
    create_systemd_services
    print_final_instructions
}

# Run main function
main "$@" 