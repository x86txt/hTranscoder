package worker

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"hTranscode/internal/models"
	"hTranscode/pkg/transcoder"

	"github.com/gorilla/websocket"
)

// Client represents a worker client that connects to the master server
type Client struct {
	ID            string
	Name          string
	ServerURL     string
	conn          *websocket.Conn
	connMutex     sync.RWMutex // Protects conn access
	jobs          chan *models.ChunkJob
	done          chan bool
	MaxJobs       int
	GPU           bool
	GPUDevice     string
	ipAddress     string
	retryInterval time.Duration
	maxRetries    int
	transcoder    *transcoder.Transcoder
	outputDir     string
}

// NewClient creates a new worker client
func NewClient(name, serverURL string, maxJobs int, gpu bool) *Client {
	// Set retry interval based on operating system
	// Windows needs much longer retry intervals due to stricter socket management
	retryInterval := 2 * time.Second
	if runtime.GOOS == "windows" {
		retryInterval = 10 * time.Second // Increased from 5 seconds
	}

	client := &Client{
		ID:            fmt.Sprintf("worker_%d", time.Now().Unix()),
		Name:          name,
		ServerURL:     serverURL,
		jobs:          make(chan *models.ChunkJob, maxJobs),
		done:          make(chan bool),
		MaxJobs:       maxJobs,
		GPU:           gpu,
		retryInterval: retryInterval,
		maxRetries:    -1,             // Retry indefinitely
		outputDir:     "./transcoded", // Default output directory
	}

	// Auto-detect GPU if not explicitly disabled
	if !gpu && IsGPUAvailable() {
		log.Println("GPU detected, enabling GPU encoding")
		client.GPU = true

		// Use primary GPU if no specific device specified
		if client.GPUDevice == "" {
			if bestGPU, found := GetBestGPU(); found {
				client.GPUDevice = bestGPU.ID
				log.Printf("Using primary GPU: %s (%s)", bestGPU.Name, bestGPU.Memory)
			}
		}
	}

	// Create transcoder instance with GPU type detection
	var gpuTypeForTranscoder string
	if client.GPU {
		if bestGPU, found := GetBestGPU(); found {
			gpuTypeForTranscoder = string(bestGPU.Type)
			log.Printf("GPU Type detected for transcoding: %s", gpuTypeForTranscoder)
		}
	}

	client.transcoder = transcoder.NewTranscoderWithType(client.GPU, client.GPUDevice, gpuTypeForTranscoder)

	// Ensure output directory exists
	if err := os.MkdirAll(client.outputDir, 0755); err != nil {
		log.Printf("Warning: Could not create output directory %s: %v", client.outputDir, err)
	}

	return client
}

// NewClientWithGPUType creates a new worker client with explicit GPU type
func NewClientWithGPUType(name, serverURL string, maxJobs int, gpu bool, gpuType string) *Client {
	// Set retry interval based on operating system
	// Windows needs much longer retry intervals due to stricter socket management
	retryInterval := 2 * time.Second
	if runtime.GOOS == "windows" {
		retryInterval = 10 * time.Second // Increased from 5 seconds
	}

	client := &Client{
		ID:            fmt.Sprintf("worker_%d", time.Now().Unix()),
		Name:          name,
		ServerURL:     serverURL,
		jobs:          make(chan *models.ChunkJob, maxJobs),
		done:          make(chan bool),
		MaxJobs:       maxJobs,
		GPU:           gpu,
		retryInterval: retryInterval,
		maxRetries:    -1,             // Retry indefinitely
		outputDir:     "./transcoded", // Default output directory
	}

	// Auto-detect GPU if not explicitly disabled
	if !gpu && IsGPUAvailable() {
		log.Println("GPU detected, enabling GPU encoding")
		client.GPU = true

		// Use primary GPU if no specific device specified
		if client.GPUDevice == "" {
			if bestGPU, found := GetBestGPU(); found {
				client.GPUDevice = bestGPU.ID
				log.Printf("Using primary GPU: %s (%s)", bestGPU.Name, bestGPU.Memory)
			}
		}
	}

	// Create transcoder instance with explicit or detected GPU type
	var gpuTypeForTranscoder string
	if client.GPU {
		if gpuType != "" {
			// Use explicitly provided GPU type
			gpuTypeForTranscoder = gpuType
			log.Printf("Using specified GPU type for transcoding: %s", gpuTypeForTranscoder)
		} else {
			// Auto-detect GPU type
			if bestGPU, found := GetBestGPU(); found {
				gpuTypeForTranscoder = string(bestGPU.Type)
				log.Printf("GPU Type detected for transcoding: %s", gpuTypeForTranscoder)
			}
		}
	}

	client.transcoder = transcoder.NewTranscoderWithType(client.GPU, client.GPUDevice, gpuTypeForTranscoder)

	// Ensure output directory exists
	if err := os.MkdirAll(client.outputDir, 0755); err != nil {
		log.Printf("Warning: Could not create output directory %s: %v", client.outputDir, err)
	}

	return client
}

// getLocalIP gets the local IP address used to connect to the server
func (c *Client) getLocalIP() string {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return "unknown"
	}

	// Connect to the server to determine which local IP to use
	conn, err := net.Dial("tcp", net.JoinHostPort(u.Hostname(), u.Port()))
	if err != nil {
		return "unknown"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.TCPAddr)
	return localAddr.IP.String()
}

// Connect establishes a WebSocket connection to the master server
func (c *Client) Connect() error {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	// Get local IP address
	c.ipAddress = c.getLocalIP()

	// Change scheme to ws:// or wss://
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	}
	u.Path = "/ws"

	// Create HTTP transport for WebSocket upgrade
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Accept self-signed certificates
		},
		MaxIdleConns:    10,
		IdleConnTimeout: 30 * time.Second,
	}

	// For Windows, configure socket reuse options
	if runtime.GOOS == "windows" {
		// Don't disable keep-alives! This was causing connection drops
		// Just reduce the pool size to avoid socket exhaustion
		transport.MaxIdleConns = 5                   // Reduced pool size but still allow some pooling
		transport.IdleConnTimeout = 30 * time.Second // Keep the same timeout as other platforms
	}

	// Create WebSocket dialer with optimized settings
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Accept self-signed certificates
		},
		HandshakeTimeout:  15 * time.Second, // Increased timeout for Windows
		EnableCompression: false,            // Disable compression to reduce overhead
		NetDial: func(network, addr string) (net.Conn, error) {
			// Use a custom dialer for better control
			d := &net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}
			return d.Dial(network, addr)
		},
	}

	log.Printf("Connecting to %s from %s", u.String(), c.ipAddress)
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Set connection with proper locking
	c.connMutex.Lock()
	c.conn = conn
	c.connMutex.Unlock()

	// Set initial connection timeouts - longer timeouts for stability
	conn.SetReadDeadline(time.Now().Add(5 * time.Minute)) // 5 minute read timeout
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

	// Enable keep-alive at the WebSocket level
	conn.SetPongHandler(func(string) error {
		log.Println("Received pong from server")
		// Refresh read deadline when we receive pong
		conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		return nil
	})

	// Determine GPU status
	gpuStatus := GetGPUStatus()
	if c.GPU && c.GPUDevice != "" {
		gpuStatus = fmt.Sprintf("GPU %s (%s)", c.GPUDevice, gpuStatus)
	}

	// Send registration message
	registration := models.WorkerMessage{
		Type: "register",
		Payload: models.Worker{
			ID:        c.ID,
			Name:      c.Name,
			IPAddress: c.ipAddress,
			Status:    models.WorkerStatusOnline,
			MaxJobs:   c.MaxJobs,
			LastPing:  time.Now(),
			GPU:       gpuStatus,
			CPU:       GetCPUStatus(), // Use detailed CPU information
		},
	}

	log.Printf("Sending registration message: %+v", registration.Payload)
	if err := conn.WriteJSON(registration); err != nil {
		return fmt.Errorf("failed to register: %w", err)
	}

	log.Println("Registration message sent successfully")
	return nil
}

// Start begins processing jobs from the master server with automatic retry
func (c *Client) Start() error {
	log.Println("Starting worker with automatic retry")

	// Start job processor
	go c.processJobs()

	// Main connection loop with retry
	for {
		select {
		case <-c.done:
			log.Println("Worker shutdown requested")
			return nil
		default:
			if err := c.connectAndRun(); err != nil {
				log.Printf("Connection lost: %v", err)

				// Ensure connection is fully cleaned up before retry
				c.connMutex.Lock()
				if c.conn != nil {
					c.conn.Close()
					c.conn = nil
				}
				c.connMutex.Unlock()

				// Windows-specific cleanup before retry
				if runtime.GOOS == "windows" {
					log.Printf("Windows detected - performing additional cleanup before retry")
					runtime.GC()                // Force garbage collection
					time.Sleep(2 * time.Second) // Additional cleanup time for Windows
				}

				log.Printf("Retrying connection in %v...", c.retryInterval)

				// Wait for retry interval or shutdown signal
				select {
				case <-c.done:
					log.Println("Worker shutdown during retry wait")
					return nil
				case <-time.After(c.retryInterval):
					log.Println("Attempting to reconnect...")
					continue
				}
			}
		}
	}
}

// connectAndRun establishes connection and runs the message loop
func (c *Client) connectAndRun() error {
	// Establish connection
	if err := c.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Improved connection cleanup with proper locking and Windows-specific handling
	defer func() {
		c.connMutex.Lock()
		defer c.connMutex.Unlock()

		if c.conn != nil {
			// Send close message before closing connection
			closeMessage := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
			c.conn.WriteMessage(websocket.CloseMessage, closeMessage)

			// Give time for close message to be sent
			time.Sleep(100 * time.Millisecond)

			c.conn.Close()
			c.conn = nil

			// On Windows, give additional time for socket cleanup and force garbage collection
			if runtime.GOOS == "windows" {
				time.Sleep(1 * time.Second) // Increased from 500ms
				runtime.GC()                // Force garbage collection to help with socket cleanup
			}
		}
	}()

	log.Println("Connection established, starting message processing")

	// Start heartbeat for this connection
	heartbeatDone := make(chan bool)
	go c.heartbeat(heartbeatDone)

	defer func() {
		// Signal heartbeat to stop by closing the channel
		close(heartbeatDone)
		// Give heartbeat time to stop
		time.Sleep(100 * time.Millisecond)
	}()

	// Create a ticker to refresh read deadline periodically
	refreshTicker := time.NewTicker(2 * time.Minute)
	defer refreshTicker.Stop()

	// Read messages from server
	for {
		select {
		case <-c.done:
			log.Println("Worker shutdown requested during message loop")
			return nil
		case <-refreshTicker.C:
			// Periodically refresh read deadline to keep connection alive
			c.connMutex.RLock()
			conn := c.conn
			c.connMutex.RUnlock()

			if conn != nil {
				conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
				log.Println("Refreshed connection read deadline")
			}
		default:
			// Get connection safely
			c.connMutex.RLock()
			conn := c.conn
			c.connMutex.RUnlock()

			if conn == nil {
				log.Println("Connection is nil, exiting message loop")
				return fmt.Errorf("connection lost")
			}

			// Set a shorter read deadline for message reading
			conn.SetReadDeadline(time.Now().Add(70 * time.Second))

			var msg models.WorkerMessage
			if err := conn.ReadJSON(&msg); err != nil {
				// Check if it's a close error (normal shutdown)
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Println("Connection closed normally")
					return nil
				}

				// Check if it's a timeout - these are expected
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					log.Println("Read timeout, refreshing connection")
					// Refresh read deadline and continue
					conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
					continue
				}

				return fmt.Errorf("read error: %w", err)
			}

			// Refresh read deadline after successfully reading a message
			conn.SetReadDeadline(time.Now().Add(5 * time.Minute))

			log.Printf("Received message from server: type=%s", msg.Type)

			switch msg.Type {
			case "registered":
				log.Printf("Registration confirmed by server: %v", msg.Payload)

			case "chunk_job":
				// Convert payload to ChunkJob
				jobData, err := json.Marshal(msg.Payload)
				if err != nil {
					log.Printf("Failed to marshal job payload: %v", err)
					continue
				}

				var job models.ChunkJob
				if err := json.Unmarshal(jobData, &job); err != nil {
					log.Printf("Failed to unmarshal chunk job: %v", err)
					continue
				}

				// Add job to queue
				select {
				case c.jobs <- &job:
					log.Printf("Received chunk job: %s", job.ID)
				default:
					log.Printf("Job queue full, rejecting job: %s", job.ID)
					// Send rejection message
					c.sendJobStatus(&job, models.ChunkStatusFailed, 0, "Worker queue full")
				}

			case "ping":
				log.Printf("Received ping from server")
				// Respond to ping
				pong := models.WorkerMessage{
					Type:    "pong",
					Payload: c.ID,
				}

				// Set write deadline before sending pong
				c.connMutex.RLock()
				currentConn := c.conn
				c.connMutex.RUnlock()

				if currentConn != nil {
					currentConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err := currentConn.WriteJSON(pong); err != nil {
						log.Printf("Failed to send pong: %v", err)
						return fmt.Errorf("failed to send pong: %w", err)
					}
				}

			case "shutdown":
				log.Println("Received shutdown command")
				close(c.done)
				return nil

			case "error":
				log.Printf("Received error from server: %v", msg.Payload)

			default:
				log.Printf("Unknown message type: %s", msg.Type)
			}
		}
	}
}

// processJobs processes encoding jobs from the queue
func (c *Client) processJobs() {
	for job := range c.jobs {
		log.Printf("Processing job: %s", job.ID)

		// Update status to processing
		c.sendJobStatus(job, models.ChunkStatusProcessing, 0, "")

		var inputPath string
		var outputPath string

		if job.IsRemoteWorker {
			// Remote worker: download chunk from master
			log.Printf("Remote worker - downloading chunk %d from master", job.ChunkIndex)

			// Create local temp directory for this chunk
			tempDir := filepath.Join(c.outputDir, "temp", job.JobID)
			if err := os.MkdirAll(tempDir, 0755); err != nil {
				log.Printf("Failed to create temp directory: %v", err)
				c.sendJobStatus(job, models.ChunkStatusFailed, 0, "Failed to create temp directory")
				continue
			}

			// Download chunk from master
			chunkFile := filepath.Join(tempDir, fmt.Sprintf("chunk_%03d.mp4", job.ChunkIndex))
			if err := c.downloadChunk(job, chunkFile); err != nil {
				log.Printf("Failed to download chunk: %v", err)
				c.sendJobStatus(job, models.ChunkStatusFailed, 0, fmt.Sprintf("Failed to download chunk: %v", err))
				continue
			}

			inputPath = chunkFile
			outputPath = filepath.Join(tempDir, fmt.Sprintf("encoded_chunk_%03d.mp4", job.ChunkIndex))
		} else {
			// Local worker: use provided chunk file path
			log.Printf("Local worker - processing chunk from %s", job.InputPath)

			// Check if input file exists
			if _, err := os.Stat(job.InputPath); os.IsNotExist(err) {
				log.Printf("Input file does not exist: %s", job.InputPath)
				c.sendJobStatus(job, models.ChunkStatusFailed, 0, "Input file not found")
				continue
			}

			inputPath = job.InputPath
			outputPath = job.OutputPath
		}

		// Ensure output directory exists
		if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
			log.Printf("Failed to create output directory: %v", err)
			c.sendJobStatus(job, models.ChunkStatusFailed, 0, "Failed to create output directory")
			continue
		}

		// Prepare encoding options
		opts := transcoder.EncodeOptions{
			InputPath:  inputPath,
			OutputPath: outputPath,
			Codec:      getCodecFromJob(job),
			Bitrate:    getBitrateFromJob(job),
			Resolution: getResolutionFromJob(job),
			Preset:     getPresetFromJob(job),
		}

		log.Printf("Transcoding %s -> %s", inputPath, outputPath)
		log.Printf("Options: Codec=%s, Bitrate=%s, Resolution=%s, Preset=%s",
			opts.Codec, opts.Bitrate, opts.Resolution, opts.Preset)

		// Update progress to 10% before starting
		c.sendJobStatus(job, models.ChunkStatusProcessing, 10, "")

		// Perform actual transcoding
		if err := c.transcoder.EncodeChunk(opts); err != nil {
			log.Printf("Transcoding failed for job %s: %v", job.ID, err)
			c.sendJobStatus(job, models.ChunkStatusFailed, 0, fmt.Sprintf("Transcoding failed: %v", err))
			continue
		}

		// Check if output file was created
		if _, err := os.Stat(outputPath); os.IsNotExist(err) {
			log.Printf("Output file was not created: %s", outputPath)
			c.sendJobStatus(job, models.ChunkStatusFailed, 0, "Output file not created")
			continue
		}

		// Get output file size for logging
		if stat, err := os.Stat(outputPath); err == nil {
			log.Printf("Transcoding completed: %s (%.2f MB)", outputPath, float64(stat.Size())/(1024*1024))
		}

		// For remote workers, upload the processed chunk back to master
		if job.IsRemoteWorker {
			log.Printf("Uploading processed chunk back to master")
			if err := c.uploadChunk(job, outputPath); err != nil {
				log.Printf("Failed to upload chunk: %v", err)
				c.sendJobStatus(job, models.ChunkStatusFailed, 0, fmt.Sprintf("Failed to upload chunk: %v", err))
				continue
			}
		}

		// Clean up temporary files for remote workers
		if job.IsRemoteWorker {
			// Clean up downloaded chunk
			if err := os.Remove(inputPath); err != nil {
				log.Printf("Warning: failed to clean up input file %s: %v", inputPath, err)
			}
			// Clean up encoded chunk (already uploaded)
			if err := os.Remove(outputPath); err != nil {
				log.Printf("Warning: failed to clean up output file %s: %v", outputPath, err)
			}
		}

		// Mark as completed
		c.sendJobStatus(job, models.ChunkStatusCompleted, 100, outputPath)
	}
}

// Helper functions to extract encoding parameters from job
func getCodecFromJob(job *models.ChunkJob) string {
	// Default to h264 if not specified
	if job.Settings != nil && job.Settings.Codec != "" {
		return job.Settings.Codec
	}
	return "h264"
}

func getBitrateFromJob(job *models.ChunkJob) string {
	// Default bitrate based on resolution or use specified
	if job.Settings != nil && job.Settings.Bitrate != "" {
		return job.Settings.Bitrate
	}
	// Default bitrates
	return "2M" // 2 Mbps default
}

func getResolutionFromJob(job *models.ChunkJob) string {
	// Return resolution if specified, otherwise keep original
	if job.Settings != nil && job.Settings.Resolution != "" {
		return job.Settings.Resolution
	}
	return ""
}

func getPresetFromJob(job *models.ChunkJob) string {
	// Default to medium if not specified
	if job.Settings != nil && job.Settings.Preset != "" {
		return job.Settings.Preset
	}
	return "medium"
}

// sendJobStatus sends job status update to the master server
func (c *Client) sendJobStatus(job *models.ChunkJob, status string, progress int, error string) {
	c.connMutex.RLock()
	conn := c.conn
	c.connMutex.RUnlock()

	if conn == nil {
		log.Printf("Cannot send job status: connection is nil")
		return
	}

	update := models.WorkerMessage{
		Type: "chunk_status",
		Payload: map[string]interface{}{
			"jobId":      job.JobID,
			"chunkId":    job.ID,
			"chunkIndex": job.ChunkIndex,
			"status":     status,
			"progress":   progress,
			"error":      error,
			"workerId":   c.ID,
		},
	}

	// Set write deadline
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

	if err := conn.WriteJSON(update); err != nil {
		log.Printf("Failed to send job status: %v", err)
	}
}

// heartbeat sends periodic heartbeat messages to the server with latency tracking
func (c *Client) heartbeat(done chan bool) {
	// Use a reasonable heartbeat frequency - 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Also send WebSocket pings for keep-alive
	pingTicker := time.NewTicker(45 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check if connection is still valid with proper locking
			c.connMutex.RLock()
			conn := c.conn
			c.connMutex.RUnlock()

			if conn == nil {
				log.Println("Heartbeat stopping: connection is nil")
				return
			}

			// Record time for latency calculation
			pingTime := time.Now()

			// Get real-time usage statistics
			usageStats := GetUsageStats()

			status := models.WorkerMessage{
				Type: "heartbeat",
				Payload: map[string]interface{}{
					"id":           c.ID,
					"currentJobs":  len(c.jobs),
					"maxJobs":      c.MaxJobs,
					"pingTime":     pingTime.UnixMilli(),
					"gpuUsage":     usageStats.GPUUsage,
					"gpuMemory":    usageStats.GPUMemory,
					"cpuUsage":     usageStats.CPUUsage,
					"networkSpeed": usageStats.NetworkSpeed,
				},
			}

			// Set write deadline before sending heartbeat
			conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

			if err := conn.WriteJSON(status); err != nil {
				log.Printf("Failed to send heartbeat: %v", err)
				return
			}

			// Don't log successful heartbeats to avoid spam (every 5 seconds)

		case <-pingTicker.C:
			// Send WebSocket ping for keep-alive
			c.connMutex.RLock()
			conn := c.conn
			c.connMutex.RUnlock()

			if conn == nil {
				log.Println("Ping stopping: connection is nil")
				return
			}

			// Send WebSocket ping
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				log.Printf("Failed to send ping: %v", err)
				return
			}

			// Don't log successful pings to avoid spam

		case <-done:
			log.Println("Heartbeat stopping due to connection close")
			return
		case <-c.done:
			log.Println("Heartbeat stopping due to worker shutdown")
			return
		}
	}
}

// Stop gracefully shuts down the worker client
func (c *Client) Stop() {
	close(c.done)
	close(c.jobs)

	// Close connection with proper locking
	c.connMutex.Lock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connMutex.Unlock()
}

// insecureHTTPClient creates an http.Client that skips TLS certificate verification.
// This is necessary for connecting to a master with a self-signed certificate.
func (c *Client) insecureHTTPClient() *http.Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Skip verification
		},
		MaxIdleConns:    10,
		IdleConnTimeout: 30 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Minute, // Increased timeout for large chunk transfers
	}
}

// downloadChunk downloads a chunk file from the master server
func (c *Client) downloadChunk(job *models.ChunkJob, destPath string) error {
	// Construct download URL
	downloadURL := fmt.Sprintf("%s/api/chunks/%s/%s/download", job.MasterURL, job.JobID, job.ID)

	log.Printf("Downloading chunk from: %s", downloadURL)

	// Create HTTP client that trusts self-signed certs
	client := c.insecureHTTPClient()

	// Create request
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Create destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	// Copy response body to file
	written, err := io.Copy(destFile, resp.Body)
	if err != nil {
		os.Remove(destPath) // Clean up on error
		return fmt.Errorf("failed to save chunk: %w", err)
	}

	log.Printf("Downloaded chunk successfully: %d bytes", written)
	return nil
}

// uploadChunk uploads a processed chunk back to the master server
func (c *Client) uploadChunk(job *models.ChunkJob, chunkPath string) error {
	// Open the chunk file
	file, err := os.Open(chunkPath)
	if err != nil {
		return fmt.Errorf("failed to open chunk file: %w", err)
	}
	defer file.Close()

	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file to form
	part, err := writer.CreateFormFile("chunkFile", filepath.Base(chunkPath))
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	// Copy file content to form
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("failed to copy file to form: %w", err)
	}

	// Close the writer to finalize the form
	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Construct upload URL
	uploadURL := fmt.Sprintf("%s/api/chunks/%s/%s/upload", job.MasterURL, job.JobID, job.ID)

	log.Printf("Uploading chunk to: %s (size: %d bytes)", uploadURL, fileInfo.Size())

	// Create HTTP client that trusts self-signed certs
	client := c.insecureHTTPClient()

	// Create request
	req, err := http.NewRequest("POST", uploadURL, body)
	if err != nil {
		return fmt.Errorf("failed to create upload request: %w", err)
	}

	// Set content type
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var uploadResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&uploadResp); err != nil {
		return fmt.Errorf("failed to parse upload response: %w", err)
	}

	if success, ok := uploadResp["success"].(bool); !ok || !success {
		return fmt.Errorf("upload failed: %v", uploadResp["message"])
	}

	log.Printf("Uploaded chunk successfully")
	return nil
}
