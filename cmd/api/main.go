package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hTranscode/internal/config"
	"hTranscode/internal/manager"
	"hTranscode/internal/models"
	"hTranscode/pkg/discovery"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

//go:embed templates/*
var templateFS embed.FS

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	workerManager *manager.WorkerManager
	jobManager    *manager.JobManager
	secretKey     string
	appConfig     *config.Config
)

const Version = "0.1.0-alpha"

type VideoFile struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Duration   string `json:"duration"`
	IsUploaded bool   `json:"isUploaded"`
}

type UploadResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	FilePath string `json:"filePath,omitempty"`
	FileName string `json:"fileName,omitempty"`
}

func main() {
	var (
		configFile  = flag.String("config", "htranscode.conf", "Path to the configuration file (JSON or TOML). Example: --config=/etc/htranscode.conf")
		keyFile     = flag.String("key", ".htranscode.key", "Path to the secret key file for worker authentication. Example: --key=/etc/htranscode.key")
		helpFlag    = flag.Bool("help", false, "Show this help message and exit.")
		versionFlag = flag.Bool("version", false, "Show version information and exit.")
	)
	flag.Parse()

	if *helpFlag {
		fmt.Printf("hTranscode API Server (version %s)\n", Version)
		fmt.Println("\nDistributed video transcoding API/master server. Handles job management, worker coordination, uploads, and chunk distribution.")
		fmt.Println("\nUsage:")
		fmt.Println("  htranscode-api --config=PATH --key=PATH")
		fmt.Println("\nFlags:")
		fmt.Println("  --config     Path to the configuration file (default: htranscode.conf)")
		fmt.Println("  --key        Path to the secret key file for worker authentication (default: .htranscode.key)")
		fmt.Println("  --help       Show this help message and exit")
		fmt.Println("  --version    Show version information and exit")
		fmt.Println("\nExample:")
		fmt.Println("  ./htranscode-api --config=prod.conf --key=prod.key")
		os.Exit(0)
	}
	if *versionFlag {
		fmt.Printf("hTranscode API Server version %s\n", Version)
		os.Exit(0)
	}

	// Load configuration
	var err error
	appConfig, err = config.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Clear the upload cache directory on startup
	if err := os.RemoveAll(appConfig.Storage.TempCacheDir); err != nil {
		log.Printf("Warning: Failed to clear upload cache directory %s: %v", appConfig.Storage.TempCacheDir, err)
	} else {
		log.Printf("Cleared upload cache directory: %s", appConfig.Storage.TempCacheDir)
	}

	// Validate configuration
	if err := appConfig.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	log.Printf("Configuration loaded from: %s", *configFile)
	log.Printf("Cache directory: %s", appConfig.Storage.TempCacheDir)
	log.Printf("GPU usage: %t (device: %s, %d%%)", appConfig.Hardware.UseGPU, appConfig.Hardware.GPUDevice, appConfig.Hardware.GPUUsagePercent)
	log.Printf("TLS enabled: %t", appConfig.TLS.Enabled)
	log.Printf("Remote workers: %t", appConfig.Remote.AllowRemoteWorkers)

	// Setup TLS if enabled (this will generate certificates if needed)
	if appConfig.TLS.Enabled {
		_, err := appConfig.SetupTLS()
		if err != nil {
			log.Fatalf("Failed to setup TLS: %v", err)
		}
	}

	// Initialize managers
	workerManager = manager.NewWorkerManager()

	// Get master URL for workers to connect back
	masterURL := appConfig.GetServerURL()

	// Ensure cache directory exists
	if err := os.MkdirAll(appConfig.Storage.TempCacheDir, 0755); err != nil {
		log.Fatalf("Failed to create cache directory: %v", err)
	}

	// Initialize job manager with the master URL
	jobManager = manager.NewJobManager(workerManager, appConfig.Storage.TempCacheDir, "./transcoded", masterURL)

	// Load or generate secret key
	secretKey, err = loadOrGenerateKey(*keyFile)
	if err != nil {
		log.Fatalf("Failed to load/generate secret key: %v", err)
	}
	log.Printf("Server secret key loaded. Share this key with workers for authentication.")
	log.Printf("Key file: %s", *keyFile)

	// Start discovery server
	discoveryServer := discovery.NewServer(secretKey, appConfig.Server.Host, appConfig.Server.Port, appConfig.Server.Name)
	if err := discoveryServer.Start(); err != nil {
		log.Printf("Warning: Failed to start discovery server: %v", err)
		log.Printf("Workers will need to connect manually")
	} else {
		defer discoveryServer.Stop()
	}

	// Start worker health checker
	go func() {
		ticker := time.NewTicker(3 * time.Second) // Check health every 3 seconds
		defer ticker.Stop()
		for range ticker.C {
			workerManager.CheckWorkerHealth()
		}
	}()

	r := mux.NewRouter()

	// Add protocol headers middleware
	r.Use(addProtocolHeaders)

	// Serve static files
	staticDir := "./cmd/static/"
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	// Routes
	r.HandleFunc("/", homeHandler).Methods("GET")
	r.HandleFunc("/simple", simpleHandler).Methods("GET")
	r.HandleFunc("/favicon.ico", faviconHandler).Methods("GET")
	r.PathPrefix("/styles/").HandlerFunc(stylesHandler).Methods("GET")

	// API routes
	apiRouter := r.PathPrefix("/api").Subrouter()
	apiRouter.HandleFunc("/browse", browseHandler).Methods("GET")
	apiRouter.HandleFunc("/encode", encodeHandler).Methods("POST")
	apiRouter.HandleFunc("/workers", workersHandler).Methods("GET")
	apiRouter.HandleFunc("/upload", uploadHandler).Methods("POST")
	apiRouter.HandleFunc("/uploads", listUploadsHandler).Methods("GET")
	apiRouter.HandleFunc("/config", configHandler).Methods("GET")
	apiRouter.HandleFunc("/uploads/delete", deleteUploadHandler).Methods("POST")
	apiRouter.HandleFunc("/jobs", jobsHandler).Methods("GET")
	apiRouter.HandleFunc("/jobs/delete", deleteJobHandler).Methods("POST")
	apiRouter.HandleFunc("/transcoded", listTranscodedHandler).Methods("GET")

	// Chunk transfer endpoints
	apiRouter.HandleFunc("/chunks/{jobId}/{chunkId}/download", chunkDownloadHandler).Methods("GET")
	apiRouter.HandleFunc("/chunks/{jobId}/{chunkId}/upload", chunkUploadHandler).Methods("POST")

	// WebSocket endpoint
	r.HandleFunc("/ws", websocketHandler)

	addr := fmt.Sprintf(":%d", appConfig.Server.Port)
	serverURL := appConfig.GetServerURL()

	fmt.Printf("🚀 hTranscode Server starting on %s\n", serverURL)
	fmt.Printf("📡 Workers can connect to: %s/ws\n", serverURL)
	if appConfig.Remote.AllowRemoteWorkers {
		fmt.Printf("🌐 Remote workers can connect using: %s\n", serverURL)
		fmt.Printf("🔑 Copy the secret key file to workers: %s\n", *keyFile)
	}
	fmt.Printf("🔍 Workers can auto-discover this server using the secret key\n")
	fmt.Printf("hTranscode API Server version %s\n", Version)

	// Enhanced server configuration
	server := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server with enhanced protocol support
	if appConfig.TLS.Enabled {
		// Get TLS configuration
		tlsConfig, err := appConfig.SetupTLS()
		if err != nil {
			log.Fatalf("Failed to setup TLS: %v", err)
		}
		server.TLSConfig = tlsConfig

		fmt.Printf("🔒 TLS 1.3 enabled with modern cipher suites\n")
		fmt.Printf("🏃 HTTP/2 enabled automatically over TLS\n")
		fmt.Printf("📱 WebSockets over HTTPS (WSS)\n")

		log.Fatal(server.ListenAndServeTLS(appConfig.TLS.CertFile, appConfig.TLS.KeyFile))
	} else {
		fmt.Printf("⚠️  HTTP/1.1 only (TLS disabled)\n")
		fmt.Printf("📱 WebSockets over HTTP (WS) - insecure\n")
		log.Fatal(server.ListenAndServe())
	}
}

func loadOrGenerateKey(keyFile string) (string, error) {
	// Try to read existing key
	if data, err := ioutil.ReadFile(keyFile); err == nil {
		key := strings.TrimSpace(string(data))
		if key != "" {
			return key, nil
		}
	}

	// Generate new key
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}

	key := hex.EncodeToString(bytes)

	// Save key to file
	if err := ioutil.WriteFile(keyFile, []byte(key), 0600); err != nil {
		return "", fmt.Errorf("failed to save key: %w", err)
	}

	log.Printf("Generated new secret key and saved to %s", keyFile)
	return key, nil
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(templateFS, "templates/index.html")
	if err != nil {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		log.Printf("Failed to parse template: %v", err)
		return
	}
	tmpl.Execute(w, nil)
}

func simpleHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(templateFS, "templates/index-simple.html")
	if err != nil {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		log.Printf("Failed to parse template: %v", err)
		return
	}
	tmpl.Execute(w, nil)
}

// stylesHandler serves embedded CSS files
func stylesHandler(w http.ResponseWriter, r *http.Request) {
	// Get the requested file path
	filePath := strings.TrimPrefix(r.URL.Path, "/styles/")
	if filePath == "" {
		http.NotFound(w, r)
		return
	}

	// Handle special cases and missing extensions
	var embeddedPath string
	switch filePath {
	case "tailwindcss":
		// Map tailwindcss to compiled styles.css (which contains processed Tailwind CSS)
		embeddedPath = "templates/styles/styles.css"
	case "styles.css":
		// Map styles.css to compiled styles.css
		embeddedPath = "templates/styles/styles.css"
	case "index.css":
		// Map index.css to compiled styles.css for compatibility
		embeddedPath = "templates/styles/styles.css"
	default:
		// If no extension, assume it's CSS and map to compiled styles
		if !strings.Contains(filePath, ".") {
			embeddedPath = "templates/styles/styles.css"
		} else {
			embeddedPath = "templates/styles/" + filePath
		}
	}

	// Read the file from embedded filesystem
	content, err := templateFS.ReadFile(embeddedPath)
	if err != nil {
		log.Printf("Failed to read embedded CSS file %s: %v", embeddedPath, err)
		http.NotFound(w, r)
		return
	}

	// Always set CSS content type for styles
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	w.Write(content)
}

// faviconHandler serves a simple favicon to prevent 404 errors
func faviconHandler(w http.ResponseWriter, r *http.Request) {
	// Return a simple 1x1 transparent PNG as favicon
	// This prevents the 404 error in browser console
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 1 day

	// 1x1 transparent PNG (67 bytes)
	favicon := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
		0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}

	w.Write(favicon)
}

func browseHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path string `json:"path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Validate and clean path
	if request.Path == "" {
		request.Path = "."
	}

	// Check if path exists
	if _, err := os.Stat(request.Path); os.IsNotExist(err) {
		http.Error(w, fmt.Sprintf("Path does not exist: %s", request.Path), http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, fmt.Sprintf("Cannot access path: %s", err.Error()), http.StatusForbidden)
		return
	}

	var videos []VideoFile

	// Read directory
	files, err := ioutil.ReadDir(request.Path)
	if err != nil {
		if os.IsPermission(err) {
			http.Error(w, fmt.Sprintf("Permission denied accessing path: %s", request.Path), http.StatusForbidden)
		} else {
			http.Error(w, fmt.Sprintf("Failed to read directory: %s", err.Error()), http.StatusInternalServerError)
		}
		return
	}

	// Filter video files
	videoExtensions := []string{".mp4", ".avi", ".mkv", ".mov", ".flv", ".wmv", ".webm"}
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		ext := strings.ToLower(filepath.Ext(file.Name()))
		for _, videoExt := range videoExtensions {
			if ext == videoExt {
				videoPath := filepath.Join(request.Path, file.Name())

				// Check if this is from the upload cache directory
				isUploaded := strings.Contains(videoPath, appConfig.Storage.TempCacheDir)

				videos = append(videos, VideoFile{
					Name:       file.Name(),
					Path:       videoPath,
					Size:       file.Size(),
					IsUploaded: isUploaded,
				})
				break
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(videos)
}

func encodeHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		VideoPath string `json:"videoPath"`
		Preset    string `json:"preset"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Validate file exists
	if _, err := os.Stat(request.VideoPath); os.IsNotExist(err) {
		http.Error(w, "Video file not found", http.StatusNotFound)
		return
	}

	// Create encode settings
	settings := &models.EncodeSettings{
		Codec:      "h264",
		Bitrate:    "10M",
		Resolution: "1080p",
		Preset:     request.Preset,
		GPU:        appConfig.Hardware.UseGPU,
	}

	// Create job using JobManager
	job, err := jobManager.CreateJob(request.VideoPath, settings)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create job: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Created encoding job: %s for video: %s", job.ID, request.VideoPath)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

func workersHandler(w http.ResponseWriter, r *http.Request) {
	workers := workerManager.GetAllWorkers()
	log.Printf("API request for workers, returning %d workers", len(workers))
	for i, worker := range workers {
		log.Printf("Worker %d: %+v", i, worker)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workers)
}

func websocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket upgrade error:", err)
		return
	}
	defer conn.Close()

	log.Printf("New WebSocket connection from %s", conn.RemoteAddr())

	// Handle worker connection
	var workerConn *models.Worker
	defer func() {
		if workerConn != nil {
			log.Printf("Unregistering worker %s on connection close", workerConn.ID)
			workerManager.UnregisterWorker(workerConn.ID)
		}
	}()

	for {
		var msg models.WorkerMessage
		if err := conn.ReadJSON(&msg); err != nil {
			log.Printf("WebSocket read error: %v", err)
			return
		}

		// Record the time of message receipt for latency calculation,
		// but only if a worker has been associated with this connection.
		if workerConn != nil {
			workerManager.RecordMessageTime(workerConn.ID)
		}

		// Skip logging heartbeat messages to reduce log spam
		if msg.Type != "heartbeat" {
			log.Printf("Received message type: %s", msg.Type)
		}

		switch msg.Type {
		case "register":
			log.Printf("Processing registration message: %+v", msg.Payload)

			// Parse worker registration
			workerData, err := json.Marshal(msg.Payload)
			if err != nil {
				log.Printf("Failed to marshal worker data: %v", err)
				continue
			}

			var worker models.Worker
			if err := json.Unmarshal(workerData, &worker); err != nil {
				log.Printf("Failed to unmarshal worker: %v", err)
				continue
			}

			log.Printf("Parsed worker data: %+v", worker)

			// Register worker
			if err := workerManager.RegisterWorker(&worker, conn); err != nil {
				log.Printf("Failed to register worker: %v", err)
				response := models.WorkerMessage{
					Type:    "error",
					Payload: fmt.Sprintf("Registration failed: %v", err),
				}
				conn.WriteJSON(response)
				continue
			}

			workerConn = &worker
			log.Printf("Worker %s successfully registered", worker.ID)

			// Send success response
			response := models.WorkerMessage{
				Type:    "registered",
				Payload: "Successfully registered",
			}
			if err := conn.WriteJSON(response); err != nil {
				log.Printf("Failed to send registration response: %v", err)
			}

			// Broadcast worker status update
			broadcastWorkerStatus()

		case "heartbeat":
			if workerConn != nil {
				// Parse heartbeat data
				heartbeatData, ok := msg.Payload.(map[string]interface{})
				if ok {
					// The worker's ID is implicit from the connection
					currentJobs := 0
					if jobs, ok := heartbeatData["currentJobs"].(float64); ok {
						currentJobs = int(jobs)
					}

					// Extract usage statistics
					var gpuUsage, gpuMemory, cpuUsage, networkSpeed float64
					if gpu, ok := heartbeatData["gpuUsage"].(float64); ok {
						gpuUsage = gpu
					}
					if gpuMem, ok := heartbeatData["gpuMemory"].(float64); ok {
						gpuMemory = gpuMem
					}
					if cpu, ok := heartbeatData["cpuUsage"].(float64); ok {
						cpuUsage = cpu
					}
					if net, ok := heartbeatData["networkSpeed"].(float64); ok {
						networkSpeed = net
					}

					// Update worker status using the simplified heartbeat data
					workerManager.UpdateWorkerHeartbeatWithUsage(workerConn.ID, currentJobs, gpuUsage, gpuMemory, cpuUsage, networkSpeed)
				}
			}

		case "chunk_status":
			// Handle chunk status updates
			log.Printf("Received chunk status update: %v", msg.Payload)

			// Parse chunk status data
			statusData, ok := msg.Payload.(map[string]interface{})
			if ok {
				chunkID, _ := statusData["chunkId"].(string)
				if chunkID == "" {
					// Fallback to id for backward compatibility
					chunkID, _ = statusData["id"].(string)
				}
				if chunkID == "" {
					// Fallback to jobId for backward compatibility
					chunkID, _ = statusData["jobId"].(string)
				}

				if chunkID != "" {
					status, _ := statusData["status"].(string)
					progress, _ := statusData["progress"].(float64)
					errorMsg, _ := statusData["error"].(string)

					// Update chunk progress using JobManager
					if err := jobManager.UpdateChunkProgress(chunkID, status, int(progress), errorMsg); err != nil {
						log.Printf("Failed to update chunk progress for %s: %v", chunkID, err)
					}
				}
			}

		default:
			log.Printf("Unknown message type: %s", msg.Type)
		}
	}
}

func broadcastWorkerStatus() {
	// Get all workers and broadcast to web clients
	workers := workerManager.GetAllWorkers()

	// TODO: Implement broadcasting to web clients
	// For now, just log
	log.Printf("Active workers: %d", len(workers))
}

func generateJobID() string {
	// Simple ID generation - in production use UUID
	return fmt.Sprintf("job_%d", time.Now().Unix())
}

// uploadHandler handles file uploads
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// Limit upload size
	maxSize := appConfig.Storage.MaxUploadSizeMB * 1024 * 1024 // Convert MB to bytes
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	// Parse multipart form
	if err := r.ParseMultipartForm(maxSize); err != nil {
		respondWithError(w, http.StatusBadRequest, "File too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("videoFile")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "No file provided or invalid file")
		return
	}
	defer file.Close()

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	validFormat := false
	for _, allowedExt := range appConfig.Storage.AllowedFormats {
		if ext == allowedExt {
			validFormat = true
			break
		}
	}

	if !validFormat {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported file format: %s. Allowed formats: %v", ext, appConfig.Storage.AllowedFormats))
		return
	}

	// Generate unique filename to avoid conflicts
	timestamp := time.Now().Format("20060102_150405")
	uniqueFilename := fmt.Sprintf("%s_%s", timestamp, header.Filename)
	destPath := filepath.Join(appConfig.Storage.TempCacheDir, uniqueFilename)

	// Create destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create upload file")
		return
	}
	defer destFile.Close()

	// Copy uploaded file to destination
	_, err = io.Copy(destFile, file)
	if err != nil {
		os.Remove(destPath) // Clean up on error
		respondWithError(w, http.StatusInternalServerError, "Failed to save uploaded file")
		return
	}

	log.Printf("File uploaded successfully: %s (size: %d bytes)", uniqueFilename, header.Size)

	response := UploadResponse{
		Success:  true,
		Message:  "File uploaded successfully",
		FilePath: destPath,
		FileName: uniqueFilename,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// listUploadsHandler lists all uploaded files in the cache directory
func listUploadsHandler(w http.ResponseWriter, r *http.Request) {
	var uploads []VideoFile

	// Read cache directory
	files, err := ioutil.ReadDir(appConfig.Storage.TempCacheDir)
	if err != nil {
		// If directory doesn't exist, return empty array instead of error
		if os.IsNotExist(err) {
			uploads = make([]VideoFile, 0)
		} else {
			respondWithError(w, http.StatusInternalServerError, "Failed to read uploads directory")
			return
		}
	} else {
		// Filter video files
		for _, file := range files {
			if file.IsDir() {
				continue
			}

			ext := strings.ToLower(filepath.Ext(file.Name()))
			for _, allowedExt := range appConfig.Storage.AllowedFormats {
				if ext == allowedExt {
					filePath := filepath.Join(appConfig.Storage.TempCacheDir, file.Name())
					uploads = append(uploads, VideoFile{
						Name:       file.Name(),
						Path:       filePath,
						Size:       file.Size(),
						IsUploaded: true,
					})
					break
				}
			}
		}
	}

	// Ensure we always return a valid array, never null
	if uploads == nil {
		uploads = make([]VideoFile, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(uploads)
}

// configHandler returns current configuration (without sensitive data)
func configHandler(w http.ResponseWriter, r *http.Request) {
	// Create a safe config response (without sensitive info)
	safeConfig := map[string]interface{}{
		"storage": map[string]interface{}{
			"max_upload_size_mb": appConfig.Storage.MaxUploadSizeMB,
			"allowed_formats":    appConfig.Storage.AllowedFormats,
		},
		"hardware": map[string]interface{}{
			"use_gpu":           appConfig.Hardware.UseGPU,
			"gpu_device":        appConfig.Hardware.GPUDevice,
			"gpu_usage_percent": appConfig.Hardware.GPUUsagePercent,
			"use_cpu":           appConfig.Hardware.UseCPU,
			"max_cpu_threads":   appConfig.Hardware.MaxCPUThreads,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(safeConfig)
}

// respondWithError is a helper function to send error responses
func respondWithError(w http.ResponseWriter, statusCode int, message string) {
	w.WriteHeader(statusCode)
	response := UploadResponse{
		Success: false,
		Message: message,
	}
	json.NewEncoder(w).Encode(response)
}

// deleteUploadHandler deletes an uploaded file
func deleteUploadHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		FilePath string `json:"filePath"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Ensure the file is in the cache directory (security check)
	if !strings.Contains(request.FilePath, appConfig.Storage.TempCacheDir) {
		respondWithError(w, http.StatusForbidden, "Can only delete files from upload cache")
		return
	}

	// Delete the file
	if err := os.Remove(request.FilePath); err != nil {
		if os.IsNotExist(err) {
			respondWithError(w, http.StatusNotFound, "File not found")
		} else {
			respondWithError(w, http.StatusInternalServerError, "Failed to delete file")
		}
		return
	}

	log.Printf("Deleted uploaded file: %s", request.FilePath)

	response := UploadResponse{
		Success: true,
		Message: "File deleted successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// jobsHandler returns all active jobs
func jobsHandler(w http.ResponseWriter, r *http.Request) {
	jobs := jobManager.GetAllJobs()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

// deleteJobHandler deletes/cancels a job
func deleteJobHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		JobID string `json:"jobId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	if err := jobManager.DeleteJob(request.JobID); err != nil {
		respondWithError(w, http.StatusNotFound, "Job not found")
		return
	}

	log.Printf("Deleted job: %s", request.JobID)

	response := UploadResponse{
		Success: true,
		Message: "Job deleted successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// listTranscodedHandler lists transcoded files
func listTranscodedHandler(w http.ResponseWriter, r *http.Request) {
	var transcoded []VideoFile

	transcodedDir := "./transcoded"

	// Read transcoded directory
	files, err := ioutil.ReadDir(transcodedDir)
	if err != nil {
		// If directory doesn't exist, return empty array instead of error
		if os.IsNotExist(err) {
			transcoded = make([]VideoFile, 0)
		} else {
			respondWithError(w, http.StatusInternalServerError, "Failed to read transcoded directory")
			return
		}
	} else {
		// List all video files in transcoded directory
		videoExtensions := []string{".mp4", ".avi", ".mkv", ".mov", ".flv", ".wmv", ".webm"}
		for _, file := range files {
			if file.IsDir() {
				continue
			}

			ext := strings.ToLower(filepath.Ext(file.Name()))
			for _, videoExt := range videoExtensions {
				if ext == videoExt {
					filePath := filepath.Join(transcodedDir, file.Name())
					transcoded = append(transcoded, VideoFile{
						Name:       file.Name(),
						Path:       filePath,
						Size:       file.Size(),
						IsUploaded: false, // These are transcoded, not uploaded
					})
					break
				}
			}
		}
	}

	// Ensure we always return a valid array, never null
	if transcoded == nil {
		transcoded = make([]VideoFile, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(transcoded)
}

// addProtocolHeaders adds security headers and prepares for HTTP/3 support
func addProtocolHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Add HSTS header for HTTPS
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// chunkDownloadHandler serves chunk files to workers
func chunkDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID := vars["jobId"]
	chunkID := vars["chunkId"]

	// Security check - validate job and chunk IDs
	if jobID == "" || chunkID == "" {
		http.Error(w, "Invalid job or chunk ID", http.StatusBadRequest)
		return
	}

	// Extract chunk index from chunkID (format: job_XXXXX_000)
	parts := strings.Split(chunkID, "_")
	var chunkIndex string
	if len(parts) >= 3 {
		chunkIndex = parts[len(parts)-1]
	} else {
		http.Error(w, "Invalid chunk ID format", http.StatusBadRequest)
		return
	}

	// Construct chunk file path - matches the pattern created by chunker
	chunkPath := filepath.Join(appConfig.Storage.TempCacheDir, jobID, fmt.Sprintf("%s_chunk_%s.mp4", jobID, chunkIndex))

	// Check if file exists
	if _, err := os.Stat(chunkPath); os.IsNotExist(err) {
		http.Error(w, "Chunk file not found", http.StatusNotFound)
		return
	}

	// Open the file
	file, err := os.Open(chunkPath)
	if err != nil {
		http.Error(w, "Failed to open chunk file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Get file info for size
	fileInfo, err := file.Stat()
	if err != nil {
		http.Error(w, "Failed to get file info", http.StatusInternalServerError)
		return
	}

	// Set headers
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(chunkPath)))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

	// Stream the file
	log.Printf("Serving chunk file: %s to worker", chunkPath)
	http.ServeContent(w, r, filepath.Base(chunkPath), fileInfo.ModTime(), file)
}

// chunkUploadHandler receives processed chunks from workers
func chunkUploadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID := vars["jobId"]
	chunkID := vars["chunkId"]

	// Security check - validate job and chunk IDs
	if jobID == "" || chunkID == "" {
		respondWithError(w, http.StatusBadRequest, "Invalid job or chunk ID")
		return
	}

	// Limit upload size to prevent abuse
	maxSize := appConfig.Storage.MaxUploadSizeMB * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	// Parse multipart form
	if err := r.ParseMultipartForm(maxSize); err != nil {
		respondWithError(w, http.StatusBadRequest, "File too large or invalid form data")
		return
	}

	file, _, err := r.FormFile("chunkFile")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "No chunk file provided")
		return
	}
	defer file.Close()

	// Create chunk directory if it doesn't exist
	chunkDir := filepath.Join(appConfig.Storage.TempCacheDir, jobID)
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create chunk directory")
		return
	}

	// Determine chunk file path based on the chunk index
	// Extract chunk index from chunkID (format: job_XXXXX_000)
	parts := strings.Split(chunkID, "_")
	var chunkIndex string
	if len(parts) >= 3 {
		chunkIndex = parts[len(parts)-1]
	} else {
		chunkIndex = chunkID
	}

	destPath := filepath.Join(chunkDir, fmt.Sprintf("%s_encoded_chunk_%s.mp4", jobID, chunkIndex))

	// Create destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create chunk file")
		return
	}
	defer destFile.Close()

	// Copy uploaded chunk to destination
	writtenBytes, err := io.Copy(destFile, file)
	if err != nil {
		os.Remove(destPath) // Clean up on error
		respondWithError(w, http.StatusInternalServerError, "Failed to save chunk file")
		return
	}

	log.Printf("Received processed chunk: %s for job %s (size: %d bytes)", chunkID, jobID, writtenBytes)

	// Update chunk status to indicate file is available
	// The worker should have already sent the completion status via WebSocket

	response := map[string]interface{}{
		"success":  true,
		"message":  "Chunk uploaded successfully",
		"chunkId":  chunkID,
		"jobId":    jobID,
		"size":     writtenBytes,
		"filePath": destPath,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
