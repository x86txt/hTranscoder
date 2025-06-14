# hTranscode

A distributed video transcoding system built with Go and modern web technologies. The system processes videos across multiple worker nodes for parallel transcoding, leveraging both CPU and NVIDIA GPU capabilities with enterprise-grade security.

## ✨ Features

### 🚀 **Core Functionality**
- **Distributed Processing**: Real FFmpeg-based video transcoding across multiple worker nodes
- **Auto-Discovery**: Workers automatically discover and connect to the master server using UDP broadcast (same network only)
- **GPU Auto-Detection**: Automatically detects and uses NVIDIA GPUs for hardware acceleration

### 🔒 **Enterprise Security**
- **TLS 1.3 Only**: Modern encryption with perfect forward secrecy, end-to-end
- **HTTP/2 Support**: Multiplexed connections for better performance
- **Secure WebSockets**: WSS (WebSocket Secure) for all worker communication
- **Authentication**: Shared secret key-based worker authentication
- **Security Modal**: Interactive security details showing TLS configuration

### 🎨 **Modern UI**
- **Professional Interface**: Built with shadcn/ui design system and Tailwind CSS
- **Real-time Monitoring**: Live GPU/CPU usage, latency, and job tracking
- **Dark Theme**: Professional dark interface with modern typography

### 📊 **Advanced Monitoring**
- **Live Usage Statistics**: Real-time GPU/CPU utilization from nvidia-smi and /proc/stat
- **Network Latency**: Sub-millisecond latency tracking with live charts
- **Worker Details**: Comprehensive modal with hardware info and performance metrics
- **Auto-Refreshing**: Live updates for Last Ping, usage bars, and job counts

## Prerequisites

- **Go 1.21+** - Programming language runtime
- **FFmpeg** - Video processing with H.264/NVENC support
- **NVIDIA GPU** (optional) - Hardware acceleration (auto-detected)
- **Linux** - Currently optimized for Linux (WSL2 supported)

## 🚀 Quick Start

### 1. **Clone and Setup**
```bash
git clone https://github.com/yourusername/hTranscode.git
cd hTranscode

# Install dependencies
go mod download

# Download Tailwind CSS CLI
cd bin/tailwind && chmod +x download-tailwindcss.sh && ./download-tailwindcss.sh && cd ../..
```

### 2. **Start Master Server**
```bash
# Development with hot reloading
go install github.com/air-verse/air@latest
air

# Production deployment
go build -o api ./cmd/api
./api
```

**Server will start with:**
- 🔒 **HTTPS on port 8080** (auto-generated TLS 1.3 certificates)
- 🏃 **HTTP/2 enabled** automatically
- 📡 **WebSocket endpoint** at `/ws`
- 🔍 **Auto-discovery** on UDP port 9999

### 3. **Start Worker Nodes**
```bash
# Build worker
go build -o worker ./cmd/worker

# Auto-discovery (recommended)
./worker

# Manual connection
./worker -server https://master-ip:8080

# GPU-specific options
./worker -gpu -gpu-device 0        # Force specific GPU
./worker -no-gpu                   # Disable GPU acceleration
```

## 🖥️ **Web Interface**

Access the modern web interface at `https://localhost:8080`:

### **Dashboard Features:**
- 📁 **File Browser**: Select videos from local filesystem or upload files
- 👥 **Worker Management**: View all connected workers with real-time status
- 📊 **Live Monitoring**: GPU/CPU usage bars, latency charts, job progress
- 🔒 **Security Details**: Click lock icons to view TLS 1.3 configuration

### **Worker Cards Display:**
- 🏷️ **Vendor Logos**: Official NVIDIA (green), AMD (red), Intel (blue) logos
- 📈 **Usage Bars**: Real-time GPU/CPU utilization percentages
- 🌐 **Network Status**: Latency with color-coded health indicators
- 💻 **Hardware Info**: CPU model, GPU memory, core count

### **Worker Modal (Click for Details):**
- 🔧 **Technical Specs**: Detailed CPU/GPU information
- 📊 **Usage Metrics**: Live performance monitoring
- 📱 **Connection Info**: IP address, protocol, security status
- 📈 **Latency History**: Real-time latency chart with statistics

## ⚙️ Configuration

### **Master Server Options**
```bash
./api -help
  -config string    Configuration file path (default "htranscode.conf")
  -key string       Secret key file path (default ".htranscode.key")
```

### **Worker Options**
```bash
./worker -help
  -name string       Worker name (auto-generated if not specified)
  -server string     Master server URL (auto-discovered if not specified)
  -jobs int          Maximum concurrent jobs (default 2)
  -gpu               Force enable GPU encoding
  -no-gpu            Disable GPU encoding
  -gpu-device string GPU device ID (auto-detected if not specified)
  -key string        Secret key file path (default ".htranscode.key")
  -discover          Use auto-discovery (default true)
```

### **Security Configuration**

The system uses TLS 1.3 by default with auto-generated certificates:

```json
{
  "tls": {
    "enabled": true,
    "cert_file": "htranscode.crt", 
    "key_file": "htranscode.key",
    "auto_generate": true
  }
}
```

**Security Features:**
- 🔒 **TLS 1.3 Only**: Modern cipher suites (AES-256-GCM, ChaCha20-Poly1305)
- 🌐 **HTTP/2**: Automatic protocol negotiation
- 🔑 **Perfect Forward Secrecy**: X25519 key exchange
- 📱 **Secure WebSockets**: WSS for all worker communication

## 🏗️ **Architecture**

### **Project Structure**
```
hTranscode/
├── cmd/
│   ├── api/               # HTTPS server (TLS 1.3 + HTTP/2)
│   ├── worker/            # Worker node with real transcoding
│   ├── static/            # Generated CSS and assets
│   └── templates/         # Modern UI templates
├── pkg/
│   ├── transcoder/        # FFmpeg integration (real processing)
│   ├── discovery/         # UDP auto-discovery
│   └── worker/            # GPU detection and monitoring
├── internal/
│   ├── config/            # TLS 1.3 configuration
│   ├── manager/           # Worker lifecycle management
│   └── models/            # Data structures
└── bin/tailwind/          # Standalone Tailwind CSS CLI
```

### **Communication Flow**
```
Browser (HTTPS) ──► Master Server (TLS 1.3) ──► Worker Nodes (WSS)
                         ▼
                   Real FFmpeg Processing
                         ▼
                   ./transcoded/ Output
```

## 🎯 **Usage Workflow**

1. **🌐 Access Interface**: Open `https://localhost:8080` in your browser
2. **🔒 Security Check**: Click the golden lock icon to view TLS 1.3 details  
3. **📁 Select Videos**: Browse local files or upload videos
4. **👥 Monitor Workers**: View connected workers with real-time hardware usage
5. **▶️ Start Transcoding**: Begin processing with live progress tracking
6. **📥 Download Results**: Find completed videos in `./transcoded/` directory

## 🚧 **Current Status**

### ✅ **Production Ready**
- ✅ Real FFmpeg-based video transcoding
- ✅ TLS 1.3 with enterprise-grade security
- ✅ HTTP/2 protocol support
- ✅ Live GPU/CPU monitoring
- ✅ Auto-discovery and worker management
- ✅ Modern responsive UI with vendor branding
- ✅ Real-time latency tracking (sub-millisecond accuracy)

### 🚀 **Future Enhancements**
- [ ] Video chunking for larger files
- [ ] Additional codec support (H.265, VP9, AV1)
- [ ] Cloud storage integration (S3, GCS)
- [ ] HTTP/3 support (infrastructure ready)
- [ ] Job queue persistence
- [ ] Multi-master setup
- [ ] Advanced encoding profiles

## 🔧 **Development**

### **Hot Reloading**
```bash
air  # Auto-restarts on code changes
```

### **Building for Production**
```bash
# Server
go build -o api ./cmd/api

# Worker
go build -o worker ./cmd/worker

# Minified CSS
./bin/tailwind/tailwindcss -i ./cmd/templates/styles/index.css -o ./cmd/static/styles.css --minify
```

### **Adding Features**
- **Encoding presets**: Modify `internal/config/`
- **UI components**: Edit `cmd/templates/`
- **Worker logic**: Extend `pkg/worker/`

## 📜 License

MIT License - See [LICENSE](LICENSE) for details

---

**Enterprise-grade distributed video transcoding with modern security and monitoring** 🎬✨ 