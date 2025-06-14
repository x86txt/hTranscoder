package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Config holds all application configuration
type Config struct {
	Server   ServerConfig   `json:"server"`
	TLS      TLSConfig      `json:"tls"`
	Remote   RemoteConfig   `json:"remote"`
	Storage  StorageConfig  `json:"storage"`
	Hardware HardwareConfig `json:"hardware"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Port int    `json:"port"`
	Host string `json:"host"`
	Name string `json:"name"`
}

// TLSConfig holds TLS/HTTPS configuration
type TLSConfig struct {
	Enabled      bool   `json:"enabled"`
	CertFile     string `json:"cert_file"`
	KeyFile      string `json:"key_file"`
	AutoGenerate bool   `json:"auto_generate"`
}

// RemoteConfig holds configuration for remote worker connections
type RemoteConfig struct {
	AllowRemoteWorkers bool     `json:"allow_remote_workers"`
	PublicAddress      string   `json:"public_address"`
	PublicPort         int      `json:"public_port"`
	TrustedNetworks    []string `json:"trusted_networks"`
	RequireAuth        bool     `json:"require_auth"`
}

// StorageConfig holds storage-related configuration
type StorageConfig struct {
	TempCacheDir    string   `json:"temp_cache_dir"`
	MaxUploadSizeMB int64    `json:"max_upload_size_mb"`
	AllowedFormats  []string `json:"allowed_formats"`
}

// HardwareConfig holds hardware-related configuration
type HardwareConfig struct {
	UseGPU          bool   `json:"use_gpu"`
	GPUDevice       string `json:"gpu_device"`
	GPUUsagePercent int    `json:"gpu_usage_percent"`
	UseCPU          bool   `json:"use_cpu"`
	MaxCPUThreads   int    `json:"max_cpu_threads"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()

	return &Config{
		Server: ServerConfig{
			Port: 8080,
			Host: "",
			Name: "hTranscode-master",
		},
		TLS: TLSConfig{
			Enabled:      true, // HTTPS by default
			CertFile:     "htranscode.crt",
			KeyFile:      "htranscode.key",
			AutoGenerate: true, // Auto-generate self-signed cert
		},
		Remote: RemoteConfig{
			AllowRemoteWorkers: true,
			PublicAddress:      "",                                                        // Auto-detect
			PublicPort:         0,                                                         // Use same as server port
			TrustedNetworks:    []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}, // Private networks
			RequireAuth:        true,
		},
		Storage: StorageConfig{
			TempCacheDir:    filepath.Join(homeDir, ".htranscode", "cache"),
			MaxUploadSizeMB: 2048, // 2GB max upload
			AllowedFormats:  []string{".mp4", ".avi", ".mkv", ".mov", ".flv", ".wmv", ".webm", ".m4v", ".3gp"},
		},
		Hardware: HardwareConfig{
			UseGPU:          true,
			GPUDevice:       "0", // Default to first GPU
			GPUUsagePercent: 80,  // Use 80% of GPU by default
			UseCPU:          true,
			MaxCPUThreads:   0, // 0 means use all available cores
		},
	}
}

// LoadConfig loads configuration from file, creates default if not exists
func LoadConfig(configPath string) (*Config, error) {
	// If config file doesn't exist, create it with defaults
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		config := DefaultConfig()
		if err := config.Save(configPath); err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
		fmt.Printf("Created default configuration file: %s\n", configPath)
		return config, nil
	}

	// Read existing config file
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Ensure cache directory exists
	if err := os.MkdirAll(config.Storage.TempCacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &config, nil
}

// Save saves the configuration to file
func (c *Config) Save(configPath string) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := ioutil.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Server.Port)
	}

	if c.Storage.MaxUploadSizeMB <= 0 {
		return fmt.Errorf("invalid max upload size: %d", c.Storage.MaxUploadSizeMB)
	}

	if c.Hardware.GPUUsagePercent < 1 || c.Hardware.GPUUsagePercent > 100 {
		return fmt.Errorf("invalid GPU usage percent: %d", c.Hardware.GPUUsagePercent)
	}

	// Validate TLS config
	if c.TLS.Enabled && !c.TLS.AutoGenerate {
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			return fmt.Errorf("TLS enabled but cert_file or key_file not specified")
		}
	}

	// Validate Remote config
	if c.Remote.PublicPort != 0 && (c.Remote.PublicPort <= 0 || c.Remote.PublicPort > 65535) {
		return fmt.Errorf("invalid public port: %d", c.Remote.PublicPort)
	}

	return nil
}

// GenerateSelfSignedCert generates a self-signed certificate for HTTPS
func (c *Config) GenerateSelfSignedCert() error {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	// Get all local IP addresses
	localIPs, err := getLocalIPAddresses()
	if err != nil {
		fmt.Printf("Warning: Could not detect local IPs, using defaults: %v\n", err)
		localIPs = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"hTranscode"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
			CommonName:    "hTranscode-server",
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour), // Valid for 1 year
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: localIPs,
		DNSNames:    []string{"localhost", "hTranscode-server"},
	}

	// Add public address if specified
	if c.Remote.PublicAddress != "" {
		if ip := net.ParseIP(c.Remote.PublicAddress); ip != nil {
			// Check if it's not already in the list
			found := false
			for _, existingIP := range template.IPAddresses {
				if existingIP.Equal(ip) {
					found = true
					break
				}
			}
			if !found {
				template.IPAddresses = append(template.IPAddresses, ip)
			}
		} else {
			template.DNSNames = append(template.DNSNames, c.Remote.PublicAddress)
		}
	}

	fmt.Printf("Certificate will include IPs: %v\n", template.IPAddresses)
	fmt.Printf("Certificate will include DNS names: %v\n", template.DNSNames)

	// Generate certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	// Save certificate
	certOut, err := os.Create(c.TLS.CertFile)
	if err != nil {
		return fmt.Errorf("failed to create cert file: %w", err)
	}
	defer certOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Save private key
	keyOut, err := os.Create(c.TLS.KeyFile)
	if err != nil {
		return fmt.Errorf("failed to create key file: %w", err)
	}
	defer keyOut.Close()

	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	// Set appropriate permissions
	os.Chmod(c.TLS.CertFile, 0644)
	os.Chmod(c.TLS.KeyFile, 0600)

	fmt.Printf("Generated self-signed certificate: %s\n", c.TLS.CertFile)
	fmt.Printf("Generated private key: %s\n", c.TLS.KeyFile)

	return nil
}

// getLocalIPAddresses returns all local IP addresses
func getLocalIPAddresses() ([]net.IP, error) {
	var ips []net.IP

	// Always include localhost
	ips = append(ips, net.IPv4(127, 0, 0, 1), net.IPv6loopback)

	// Get all network interfaces
	interfaces, err := net.Interfaces()
	if err != nil {
		return ips, err
	}

	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		// Get addresses for this interface
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// Skip if not a valid unicast IP
			if ip == nil || ip.IsLoopback() || ip.IsMulticast() {
				continue
			}

			// Add IPv4 and IPv6 addresses
			if ip.To4() != nil || ip.To16() != nil {
				ips = append(ips, ip)
			}
		}
	}

	return ips, nil
}

// SetupTLS configures TLS for the server
func (c *Config) SetupTLS() (*tls.Config, error) {
	if !c.TLS.Enabled {
		return nil, fmt.Errorf("TLS is not enabled")
	}

	// Generate certificate if needed
	if c.TLS.AutoGenerate {
		if _, err := os.Stat(c.TLS.CertFile); os.IsNotExist(err) {
			if err := c.GenerateSelfSignedCert(); err != nil {
				return nil, fmt.Errorf("failed to generate certificate: %w", err)
			}
		}
	}

	// Load certificate
	cert, err := tls.LoadX509KeyPair(c.TLS.CertFile, c.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load certificate: %w", err)
	}

	// Browser-compatible TLS configuration
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12, // Allow TLS 1.2 for broader compatibility
		MaxVersion:   tls.VersionTLS13, // Up to TLS 1.3

		// Browser-compatible cipher suites (TLS 1.2 + 1.3)
		CipherSuites: []uint16{
			// TLS 1.2 cipher suites (for browser compatibility)
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		},

		// TLS 1.3 cipher suites (automatically used for TLS 1.3)
		// These don't need to be specified as they're defaults for TLS 1.3

		CurvePreferences: []tls.CurveID{
			tls.X25519, // Modern elliptic curve
			tls.CurveP256,
			tls.CurveP384,
		},

		// Enable HTTP/2 explicitly
		NextProtos: []string{"h2", "http/1.1"},

		// Improve compatibility
		PreferServerCipherSuites: true,
	}

	return tlsConfig, nil
}

// GetServerURL returns the complete server URL for workers to connect to
func (c *Config) GetServerURL() string {
	scheme := "http"
	if c.TLS.Enabled {
		scheme = "https"
	}

	host := c.Remote.PublicAddress
	if host == "" {
		if c.Server.Host != "" {
			host = c.Server.Host
		} else {
			// Auto-detect default route interface IP
			if ip := getDefaultRouteIP(); ip != "" {
				host = ip
			} else {
				host = "localhost"
			}
		}
	}

	port := c.Remote.PublicPort
	if port == 0 {
		port = c.Server.Port
	}

	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// getDefaultRouteIP returns the IP address of the interface used for the default route
func getDefaultRouteIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
