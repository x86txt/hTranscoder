# Auto-Discovery System

The hTranscode auto-discovery system allows worker nodes to automatically find and connect to the master server without manual configuration. This document explains how it works and how to use it effectively.

## Overview

The discovery system uses UDP broadcast messages to allow workers to find the master server on the local network. This eliminates the need to manually configure each worker with the server address.

## How It Works

### 1. Master Server Discovery Service

When the master server starts, it:
- Listens on UDP port 9999 for discovery requests
- Validates incoming requests using the secret key hash
- Responds with connection details to authenticated requests

### 2. Worker Discovery Process

When a worker starts with auto-discovery enabled:
1. Broadcasts UDP discovery requests every second
2. Includes a SHA256 hash of the secret key in requests
3. Waits up to 10 seconds for a response
4. Connects to the first server that responds with valid credentials

### 3. Security

The system uses a shared secret key for authentication:
- Master generates a random 256-bit key on first run
- Key is hashed using SHA256 before transmission
- Only workers with the correct key can discover the server

## Configuration

### Master Server

```bash
# Start with default settings (auto-detects IP)
./htranscode

# Specify a custom network interface
./htranscode -host 192.168.1.100

# Use a custom secret key file
./htranscode -key /path/to/custom.key

# Disable discovery (workers must connect manually)
# Simply don't start the discovery service
```

### Worker Nodes

```bash
# Auto-discover and connect (default)
./worker

# Provide secret key via environment variable
export HTRANSCODE_KEY=your-secret-key-here
./worker

# Use a custom key file
./worker -key /path/to/custom.key

# Disable discovery and connect manually
./worker -server http://192.168.1.100:8080 -discover=false
```

## Network Requirements

### Local Network
- UDP port 9999 must not be blocked by firewalls
- Broadcast packets must be allowed on the network
- Works on most home and office networks

### Limitations
- Only works on the same broadcast domain (local network)
- Cannot traverse routers or VPNs without special configuration
- May not work on some corporate networks with strict policies

## Troubleshooting

### Workers Can't Find Server

1. **Check network connectivity**:
   ```bash
   # On worker machine, ping the server
   ping server-ip-address
   ```

2. **Verify UDP port is open**:
   ```bash
   # On server
   sudo netstat -lupen | grep 9999
   ```

3. **Check firewall rules**:
   ```bash
   # On Ubuntu/Debian
   sudo ufw status
   
   # Allow UDP port 9999
   sudo ufw allow 9999/udp
   ```

4. **Verify secret key**:
   - Ensure the same key file is on both server and workers
   - Check file permissions (should be readable)

### Manual Connection Fallback

If discovery fails, connect manually:
```bash
# Find server IP address
ip addr show  # on server

# Connect worker manually
./worker -server http://SERVER_IP:8080 -discover=false
```

## Advanced Usage

### Multiple Networks

For servers with multiple network interfaces:
```bash
# Bind to specific interface
./htranscode -host 192.168.1.100

# This ensures discovery responses use the correct IP
```

### Docker/Container Deployment

When using containers:
```dockerfile
# Expose discovery port
EXPOSE 9999/udp
EXPOSE 8080/tcp

# Run with host networking for discovery
docker run --network host htranscode
```

### Cloud Deployment

For cloud deployments where broadcast doesn't work:

1. **Option 1**: Disable discovery and use manual connection
   ```bash
   ./worker -server http://cloud-server.example.com:8080 -discover=false
   ```

2. **Option 2**: Use a VPN to create a virtual local network

3. **Option 3**: Implement a registry service (future enhancement)

## Security Considerations

1. **Key Distribution**: 
   - Transfer key files securely (SCP, encrypted email, etc.)
   - Never commit keys to version control
   - Rotate keys periodically

2. **Network Security**:
   - Discovery only works on local networks
   - Use VPN for remote workers
   - Consider TLS for production deployments

3. **Monitoring**:
   - Check server logs for unauthorized discovery attempts
   - Monitor worker connections

## Implementation Details

The discovery system is implemented in `pkg/discovery/discovery.go`:

- **Server**: Listens for UDP broadcasts and responds to authenticated requests
- **Client**: Sends broadcast requests and processes responses
- **Protocol**: Simple JSON messages with type, secret hash, and connection details

## Future Enhancements

Planned improvements to the discovery system:

1. **Multicast Support**: Use multicast instead of broadcast for better network efficiency
2. **Registry Service**: Central registry for cloud deployments
3. **mDNS/Bonjour**: Alternative discovery using standard protocols
4. **Encryption**: Encrypt discovery messages for additional security
5. **Multiple Masters**: Support for discovering multiple master servers 