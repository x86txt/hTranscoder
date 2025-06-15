package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"hTranscode/pkg/discovery"
	"hTranscode/pkg/worker"
)

const Version = "0.1.0-alpha"

func main() {
	var (
		name        = flag.String("name", "", "Worker name (default: auto-generated). Example: --name=worker1")
		serverURL   = flag.String("server", "", "Master server URL (optional if using discovery). Example: --server=https://192.168.1.10:8080")
		maxJobs     = flag.Int("jobs", 2, "Maximum concurrent jobs this worker can process. Example: --jobs=4")
		useGPU      = flag.Bool("gpu", false, "Force enable GPU encoding (overrides auto-detect). Example: --gpu")
		noGPU       = flag.Bool("no-gpu", false, "Disable GPU encoding, even if available. Example: --no-gpu")
		gpuDevice   = flag.String("gpu-device", "", "GPU device to use (e.g., 0). Example: --gpu-device=1")
		gpuType     = flag.String("gpu-type", "", "Force GPU type: nvidia, amd, apple, intel. Example: --gpu-type=nvidia")
		keyFile     = flag.String("key", ".htranscode.key", "Secret key file path. Example: --key=/etc/htranscode.key")
		discover    = flag.Bool("discover", true, "Use auto-discovery to find server (default: true). Example: --discover=false")
		helpFlag    = flag.Bool("help", false, "Show this help message and exit.")
		versionFlag = flag.Bool("version", false, "Show version information and exit.")
	)
	flag.Parse()

	if *helpFlag {
		fmt.Printf("hTranscode Worker (version %s)\n", Version)
		fmt.Println("\nDistributed video transcoding worker. Connects to the master server, receives chunk jobs, and performs encoding using CPU or GPU.")
		fmt.Println("\nUsage:")
		fmt.Println("  htranscode-worker --name=NAME --server=URL --jobs=N [other options]")
		fmt.Println("\nFlags:")
		fmt.Println("  --name        Worker name (default: auto-generated)")
		fmt.Println("  --server      Master server URL (optional if using discovery)")
		fmt.Println("  --jobs        Maximum concurrent jobs (default: 2)")
		fmt.Println("  --gpu         Force enable GPU encoding")
		fmt.Println("  --no-gpu      Disable GPU encoding, even if available")
		fmt.Println("  --gpu-device  GPU device to use (e.g., 0)")
		fmt.Println("  --gpu-type    Force GPU type: nvidia, amd, apple, intel")
		fmt.Println("  --key         Secret key file path (default: .htranscode.key)")
		fmt.Println("  --discover    Use auto-discovery to find server (default: true)")
		fmt.Println("  --help        Show this help message and exit")
		fmt.Println("  --version     Show version information and exit")
		fmt.Println("\nExample:")
		fmt.Println("  ./htranscode-worker --name=worker1 --server=https://192.168.1.10:8080 --jobs=4 --gpu")
		os.Exit(0)
	}
	if *versionFlag {
		fmt.Printf("hTranscode Worker version %s\n", Version)
		os.Exit(0)
	}

	// Load secret key
	secretKey, err := loadSecretKey(*keyFile)
	if err != nil {
		log.Fatalf("Failed to load secret key: %v", err)
	}

	// Generate worker name if not provided
	if *name == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		*name = fmt.Sprintf("worker-%s-%d", hostname, os.Getpid())
	}

	// Determine server URL
	var finalServerURL string
	if *serverURL != "" {
		// Use provided server URL
		finalServerURL = *serverURL
		log.Printf("Using provided server URL: %s", finalServerURL)
	} else if *discover {
		// Try to discover server
		log.Println("Discovering hTranscode server...")
		discoveryClient := discovery.NewClient(secretKey)

		addr, port, serverName, err := discoveryClient.DiscoverServer()
		if err != nil {
			log.Fatalf("Failed to discover server: %v", err)
		}

		// Test if server is using HTTPS
		scheme := detectServerScheme(addr, port)
		finalServerURL = fmt.Sprintf("%s://%s:%d", scheme, addr, port)
		log.Printf("Discovered server '%s' at %s", serverName, finalServerURL)
	} else {
		log.Fatalf("No server URL provided and discovery is disabled")
	}

	// Determine GPU usage
	enableGPU := *useGPU
	if !*noGPU && !*useGPU {
		// Auto-detect GPU if not explicitly disabled or enabled
		if worker.IsGPUAvailable() {
			enableGPU = true
			// Log detected GPUs
			if gpus, err := worker.DetectGPUs(); err == nil && len(gpus) > 0 {
				log.Printf("Detected %d GPU(s):", len(gpus))
				for i, gpu := range gpus {
					log.Printf("  [%d] %s %s (%s) - Type: %s", i, gpu.Name, gpu.Memory,
						func() string {
							if gpu.IsPrimary {
								return "Primary"
							}
							return "Secondary"
						}(), gpu.Type)
				}

				if bestGPU, found := worker.GetBestGPU(); found {
					log.Printf("Selected GPU for encoding: %s (%s)", bestGPU.Name, bestGPU.Type)
				}
			} else {
				log.Println("GPU detected and will be used for encoding")
			}
		} else {
			log.Println("No GPU detected, using CPU encoding")
		}
	} else if *noGPU {
		enableGPU = false
		log.Println("GPU encoding disabled by user")
	} else if *useGPU {
		log.Println("GPU encoding forced by user")
		if *gpuType != "" {
			log.Printf("Forced GPU type: %s", *gpuType)
		}
	}

	// Create worker client
	client := worker.NewClientWithGPUType(*name, finalServerURL, *maxJobs, enableGPU, *gpuType)
	if *gpuDevice != "" {
		if worker.ValidateGPUDevice(*gpuDevice) {
			client.GPUDevice = *gpuDevice
			log.Printf("Using specified GPU device: %s", *gpuDevice)
		} else {
			log.Printf("Warning: GPU device %s not found, using auto-detection", *gpuDevice)
		}
	}

	// Start worker (handles connection automatically)
	log.Printf("Starting worker '%s'...", *name)
	fmt.Printf("hTranscode Worker version %s\n", Version)

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start worker in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- client.Start()
	}()

	// Wait for either error or signal
	select {
	case err := <-errChan:
		if err != nil {
			log.Fatalf("Worker error: %v", err)
		}
	case sig := <-sigChan:
		log.Printf("Received signal %v, shutting down...", sig)
		client.Stop()
	}

	log.Println("Worker stopped")
}

func loadSecretKey(keyFile string) (string, error) {
	// First, try to read from file
	if data, err := ioutil.ReadFile(keyFile); err == nil {
		key := strings.TrimSpace(string(data))
		if key != "" {
			log.Printf("Loaded secret key from %s", keyFile)
			return key, nil
		}
	}

	// If file doesn't exist, check environment variable
	if key := os.Getenv("HTRANSCODE_KEY"); key != "" {
		log.Println("Using secret key from HTRANSCODE_KEY environment variable")
		return key, nil
	}

	// Prompt user to provide key
	return "", fmt.Errorf("no secret key found. Please provide the key via:\n"+
		"  1. The key file: %s\n"+
		"  2. Environment variable: HTRANSCODE_KEY\n"+
		"  3. Copy the key from the master server", keyFile)
}

func detectServerScheme(addr string, port int) string {
	// Try HTTPS first (since we prefer secure connections)
	httpsURL := fmt.Sprintf("https://%s:%d/api/config", addr, port)

	// Create HTTP client that ignores self-signed certificates
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	// Test HTTPS
	log.Printf("Testing HTTPS connection to %s", httpsURL)
	resp, err := client.Get(httpsURL)
	if err == nil {
		resp.Body.Close()
		log.Println("Server is using HTTPS")
		return "https"
	}
	log.Printf("HTTPS test failed: %v", err)

	// Test HTTP as fallback
	httpURL := fmt.Sprintf("http://%s:%d/api/config", addr, port)
	log.Printf("Testing HTTP connection to %s", httpURL)

	// Use a basic client for HTTP
	httpClient := &http.Client{
		Timeout: 3 * time.Second,
	}

	resp, err = httpClient.Get(httpURL)
	if err == nil {
		resp.Body.Close()
		log.Println("Server is using HTTP")
		return "http"
	}
	log.Printf("HTTP test failed: %v", err)

	// Default to HTTP if both fail (connection might work at WebSocket level)
	log.Println("Both HTTPS and HTTP tests failed, defaulting to HTTP")
	return "http"
}
