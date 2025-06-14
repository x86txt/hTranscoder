# hTranscode Configuration

The hTranscode application uses a JSON configuration file to customize its behavior. By default, it looks for `htranscode.conf` in the current directory.

## 🔒 **TLS 1.3 & HTTP/2 by Default**

**hTranscode now uses TLS 1.3 and HTTP/2 by default** with enterprise-grade security and modern performance optimizations:

- **TLS 1.3 Only**: Latest encryption standard with perfect forward secrecy
- **HTTP/2 Support**: Automatic multiplexing and header compression
- **Modern Cipher Suites**: AES-256-GCM, ChaCha20-Poly1305, X25519 key exchange
- **Auto-generated Certificates**: Self-signed certificates created automatically

## Configuration File Location

You can specify a custom configuration file using the `-config` flag:

```bash
./api -config /path/to/your/config.json
```

## Configuration Options

### Server Configuration

```json
{
  "server": {
    "port": 8080,
    "host": "",
    "name": "hTranscode-master"
  }
}
```

- **port**: TCP port for the web server (default: 8080)
- **host**: IP address to bind to (empty = auto-detect)
- **name**: Server name for discovery (default: "hTranscode-master")

### TLS/HTTPS Configuration

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

- **enabled**: Enable HTTPS (default: true)
- **cert_file**: Path to SSL certificate file (default: "htranscode.crt")
- **key_file**: Path to SSL private key file (default: "htranscode.key")
- **auto_generate**: Auto-generate self-signed certificate if files don't exist (default: true)

### Remote Worker Configuration

```json
{
  "remote": {
    "allow_remote_workers": true,
    "public_address": "",
    "public_port": 0,
    "trusted_networks": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"],
    "require_auth": true
  }
}
```

- **allow_remote_workers**: Allow workers from remote networks (default: true)
- **public_address**: Public IP/hostname for remote workers (empty = auto-detect)
- **public_port**: Public port for remote workers (0 = use server port)
- **trusted_networks**: List of trusted network CIDRs for security
- **require_auth**: Require secret key authentication (default: true)

### Storage Configuration

```json
{
  "storage": {
    "temp_cache_dir": "/home/user/.htranscode/cache",
    "max_upload_size_mb": 2048,
    "allowed_formats": [".mp4", ".avi", ".mkv", ".mov", ".flv", ".wmv", ".webm", ".m4v", ".3gp"]
  }
}
```

- **temp_cache_dir**: Directory where uploaded files are stored
- **max_upload_size_mb**: Maximum file size for uploads in MB (default: 2048 = 2GB)
- **allowed_formats**: List of allowed video file extensions

### Hardware Configuration

```json
{
  "hardware": {
    "use_gpu": true,
    "gpu_device": "0",
    "gpu_usage_percent": 80,
    "use_cpu": true,
    "max_cpu_threads": 0
  }
}
```

- **use_gpu**: Enable GPU acceleration (default: true)
- **gpu_device**: GPU device ID to use (default: "0" = first GPU)
- **gpu_usage_percent**: Percentage of GPU to use (1-100, default: 80)
- **use_cpu**: Enable CPU processing (default: true)
- **max_cpu_threads**: Maximum CPU threads (0 = use all available)

## 🌐 Remote Worker Setup

### Quick Setup for Remote Workers

1. **On the Server**:
   ```bash
   # Start server (auto-generates certificates)
   ./api
   
   # Note the connection URL from output:
   # "Remote workers can connect using: https://YOUR_IP:8080"
   ```

2. **Copy Secret Key to Worker**:
   ```bash
   # Secure copy the key file
   scp .htranscode.key user@worker-host:/path/to/worker/
   ```

3. **On the Remote Worker**:
   ```bash
   # Connect using HTTPS (certificate validation automatically ignored)
   ./worker -server https://YOUR_SERVER_IP:8080
   
   # Or use auto-discovery if on same network
   ./worker
   ```

### Example Configurations

#### Public Server Setup (Internet-facing)
```json
{
  "server": {
    "port": 8080,
    "host": "0.0.0.0",
    "name": "hTranscode-public"
  },
  "tls": {
    "enabled": true,
    "cert_file": "htranscode.crt",
    "key_file": "htranscode.key",
    "auto_generate": true
  },
  "remote": {
    "allow_remote_workers": true,
    "public_address": "your-public-domain.com",
    "public_port": 8080,
    "trusted_networks": ["0.0.0.0/0"],
    "require_auth": true
  }
}
```

#### Local Network Only
```json
{
  "remote": {
    "allow_remote_workers": true,
    "public_address": "",
    "public_port": 0,
    "trusted_networks": ["192.168.1.0/24"],
    "require_auth": true
  }
}
```

#### High-Performance GPU Setup
```json
{
  "server": {
    "port": 8080,
    "host": "",
    "name": "hTranscode-gpu-server"
  },
  "storage": {
    "temp_cache_dir": "/fast/nvme/htranscode/cache",
    "max_upload_size_mb": 4096
  },
  "hardware": {
    "use_gpu": true,
    "gpu_device": "0",
    "gpu_usage_percent": 95,
    "use_cpu": false,
    "max_cpu_threads": 0
  }
}
```

#### CPU-Only Setup
```json
{
  "tls": {
    "enabled": false
  },
  "hardware": {
    "use_gpu": false,
    "gpu_device": "0",
    "gpu_usage_percent": 0,
    "use_cpu": true,
    "max_cpu_threads": 8
  }
}
```

## 🔐 Security Features

### Enterprise-Grade Security
- **TLS 1.3 Only**: Modern encryption with perfect forward secrecy
- **HTTP/2 Protocol**: Automatic multiplexing and performance optimization
- **Secure WebSockets (WSS)**: All worker communication encrypted
- **Advanced Cipher Suites**: AES-256-GCM, ChaCha20-Poly1305
- **X25519 Key Exchange**: Fastest elliptic curve for maximum security
- **Interactive Security Modal**: Click lock icons to view TLS configuration

### Remote Worker Security
- **Shared secret authentication**: Workers must have correct secret key
- **Network restrictions**: Trusted network CIDR filtering
- **Encrypted communication**: All data encrypted in transit

## Runtime Configuration

The application will:

1. **Auto-create config**: If no config file exists, it creates one with defaults
2. **Generate certificates**: Auto-creates SSL certificates if TLS enabled
3. **Create cache directory**: Automatically creates the cache directory if it doesn't exist
4. **Validate settings**: Checks all configuration values on startup
5. **Display connection info**: Shows URLs for remote workers

## Worker Connection Examples

### Local Worker (Auto-discovery)
```bash
# Works on same network
./worker
```

### Remote Worker (Manual)
```bash
# Copy secret key first
scp server:.htranscode.key .

# Connect to remote server
./worker -server https://server.example.com:8080

# Force GPU usage
./worker -server https://server.example.com:8080 -gpu

# Disable auto-discovery for remote connection
./worker -server https://server.example.com:8080 -discover=false
```

### Cloud Deployment
```bash
# Server with custom domain
./worker -server https://transcode.mycompany.com:8080
```

## Troubleshooting

### Connection Issues
- **"TLS handshake error"**: Normal - happens when HTTP clients try to connect to HTTPS server
- **"Certificate validation failed"**: Worker automatically ignores certificate errors for self-signed certs
- **"Connection refused"**: Check if server is running and port is accessible

### Remote Worker Issues
```bash
# Test server connectivity
curl -k https://YOUR_SERVER:8080/api/config

# Check secret key
cat .htranscode.key  # Should match server key

# Test worker connection
./worker -server https://YOUR_SERVER:8080 -discover=false
```

### Firewall Configuration
```bash
# Allow HTTPS traffic
sudo ufw allow 8080/tcp

# For discovery (optional)
sudo ufw allow 9999/udp
```

- **Port already in use**: Change the `port` setting
- **Permission denied**: Ensure the cache directory is writable
- **GPU not found**: Check `gpu_device` setting matches your system
- **Invalid config**: Check JSON syntax and value ranges
- **Certificate errors**: Delete `.crt` and `.key` files to regenerate 