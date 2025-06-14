package main

import (
	"crypto/rand"
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
		configFile = flag.String("config", "htranscode.conf", "Configuration file path")
		keyFile    = flag.String("key", ".htranscode.key", "Secret key file path")
	)
	flag.Parse()

	// Load configuration
	var err error
	appConfig, err = config.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
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

	// Initialize worker manager
	workerManager = manager.NewWorkerManager()

	// Initialize job manager
	jobManager = manager.NewJobManager(workerManager, appConfig.Storage.TempCacheDir, "./transcoded")

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
		ticker := time.NewTicker(1 * time.Second) // Check health every second
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
	r.HandleFunc("/api/browse", browseHandler).Methods("POST")
	r.HandleFunc("/api/upload", uploadHandler).Methods("POST")
	r.HandleFunc("/api/uploads", listUploadsHandler).Methods("GET")
	r.HandleFunc("/api/uploads/delete", deleteUploadHandler).Methods("DELETE")
	r.HandleFunc("/api/encode", encodeHandler).Methods("POST")
	r.HandleFunc("/api/jobs", jobsHandler).Methods("GET")
	r.HandleFunc("/api/jobs/delete", deleteJobHandler).Methods("DELETE")
	r.HandleFunc("/api/workers", workersHandler).Methods("GET")
	r.HandleFunc("/api/config", configHandler).Methods("GET")
	r.HandleFunc("/ws", websocketHandler)
	r.HandleFunc("/api/transcoded", listTranscodedHandler).Methods("GET")

	addr := fmt.Sprintf(":%d", appConfig.Server.Port)
	serverURL := appConfig.GetServerURL()

	fmt.Printf("🚀 hTranscode Server starting on %s\n", serverURL)
	fmt.Printf("📡 Workers can connect to: %s/ws\n", serverURL)
	if appConfig.Remote.AllowRemoteWorkers {
		fmt.Printf("🌐 Remote workers can connect using: %s\n", serverURL)
		fmt.Printf("🔑 Copy the secret key file to workers: %s\n", *keyFile)
	}
	fmt.Printf("🔍 Workers can auto-discover this server using the secret key\n")

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
	tmpl := template.Must(template.ParseFiles("./cmd/templates/index.html"))
	tmpl.Execute(w, nil)
}

func simpleHandler(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("./cmd/templates/index-simple.html"))
	tmpl.Execute(w, nil)
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
					if id, ok := heartbeatData["id"].(string); ok {
						currentJobs := 0
						if jobs, ok := heartbeatData["currentJobs"].(float64); ok {
							currentJobs = int(jobs)
						}

						// Extract ping time for latency calculation
						var pingTime int64 = 0
						if pt, ok := heartbeatData["pingTime"].(float64); ok {
							pingTime = int64(pt)
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

						workerManager.UpdateWorkerHeartbeatWithUsage(id, currentJobs, pingTime, gpuUsage, gpuMemory, cpuUsage, networkSpeed)
					}
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
