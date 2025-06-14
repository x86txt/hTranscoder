package worker

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	client := &Client{
		ID:            fmt.Sprintf("worker_%d", time.Now().Unix()),
		Name:          name,
		ServerURL:     serverURL,
		jobs:          make(chan *models.ChunkJob, maxJobs),
		done:          make(chan bool),
		MaxJobs:       maxJobs,
		GPU:           gpu,
		retryInterval: 2 * time.Second,
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

	// Create transcoder instance
	client.transcoder = transcoder.NewTranscoder(client.GPU, client.GPUDevice)

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

	// Create WebSocket dialer with TLS config for self-signed certificates
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Accept self-signed certificates
		},
		HandshakeTimeout: 10 * time.Second,
	}

	log.Printf("Connecting to %s from %s", u.String(), c.ipAddress)
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	c.conn = conn

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
	if err := c.conn.WriteJSON(registration); err != nil {
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
	defer func() {
		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}
	}()

	log.Println("Connection established, starting message processing")

	// Start heartbeat for this connection
	heartbeatDone := make(chan bool)
	go c.heartbeat(heartbeatDone)

	defer func() {
		// Signal heartbeat to stop by closing the channel
		close(heartbeatDone)
	}()

	// Read messages from server
	for {
		select {
		case <-c.done:
			log.Println("Worker shutdown requested during message loop")
			return nil
		default:
			var msg models.WorkerMessage
			if err := c.conn.ReadJSON(&msg); err != nil {
				return fmt.Errorf("read error: %w", err)
			}

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
				if err := c.conn.WriteJSON(pong); err != nil {
					log.Printf("Failed to send pong: %v", err)
					return fmt.Errorf("failed to send pong: %w", err)
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
			// Virtual chunk: extract segment from original video
			log.Printf("Processing virtual chunk %d (%.2fs-%.2fs) from %s",
				job.ChunkIndex, job.SegmentStart, job.SegmentStart+job.SegmentDuration, job.InputPath)

			// Check if original video exists (might be a shared path or URL)
			if _, err := os.Stat(job.InputPath); os.IsNotExist(err) {
				log.Printf("Original video file does not exist: %s", job.InputPath)
				c.sendJobStatus(job, models.ChunkStatusFailed, 0, "Original video file not found")
				continue
			}

			// Create local temp directory for this chunk
			tempDir := filepath.Join(c.outputDir, "temp", job.JobID)
			if err := os.MkdirAll(tempDir, 0755); err != nil {
				log.Printf("Failed to create temp directory: %v", err)
				c.sendJobStatus(job, models.ChunkStatusFailed, 0, "Failed to create temp directory")
				continue
			}

			// Extract the segment using FFmpeg
			segmentFile := filepath.Join(tempDir, fmt.Sprintf("segment_%03d.mp4", job.ChunkIndex))
			if err := c.extractSegment(job.InputPath, segmentFile, job.SegmentStart, job.SegmentDuration); err != nil {
				log.Printf("Failed to extract segment: %v", err)
				c.sendJobStatus(job, models.ChunkStatusFailed, 0, fmt.Sprintf("Failed to extract segment: %v", err))
				continue
			}

			inputPath = segmentFile
			outputPath = filepath.Join(c.outputDir, job.OutputPath)
		} else {
			// Physical chunk: use provided chunk file
			log.Printf("Processing physical chunk from %s", job.InputPath)

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

		// Clean up temporary segment file for virtual chunks
		if job.IsRemoteWorker && inputPath != job.InputPath {
			if err := os.Remove(inputPath); err != nil {
				log.Printf("Warning: failed to clean up temp file %s: %v", inputPath, err)
			}
		}

		// Mark as completed
		c.sendJobStatus(job, models.ChunkStatusCompleted, 100, outputPath)
	}
}

// extractSegment extracts a time segment from a video using FFmpeg
func (c *Client) extractSegment(inputPath, outputPath string, startTime, duration float64) error {
	// Use FFmpeg to extract the segment
	// -ss: start time, -t: duration, -c copy: avoid re-encoding during extraction
	args := []string{
		"-i", inputPath,
		"-ss", fmt.Sprintf("%.2f", startTime),
		"-t", fmt.Sprintf("%.2f", duration),
		"-c", "copy", // Copy streams to avoid quality loss during segmentation
		"-avoid_negative_ts", "make_zero",
		"-y", // Overwrite output file
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg segment extraction failed: %w\nOutput: %s", err, string(output))
	}

	log.Printf("Extracted segment: %.2fs-%.2fs from %s to %s",
		startTime, startTime+duration, filepath.Base(inputPath), filepath.Base(outputPath))

	return nil
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

	if err := c.conn.WriteJSON(update); err != nil {
		log.Printf("Failed to send job status: %v", err)
	}
}

// heartbeat sends periodic heartbeat messages to the server with latency tracking
func (c *Client) heartbeat(done chan bool) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
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

			if err := c.conn.WriteJSON(status); err != nil {
				log.Printf("Failed to send heartbeat: %v", err)
				return
			}

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
	if c.conn != nil {
		c.conn.Close()
	}
}
