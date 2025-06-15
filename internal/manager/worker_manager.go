package manager

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"hTranscode/internal/models"

	"github.com/gorilla/websocket"
)

// WorkerManager manages connected workers
type WorkerManager struct {
	workers map[string]*WorkerConnection
	mu      sync.RWMutex
}

// WorkerConnection represents a connected worker
type WorkerConnection struct {
	Worker          *models.Worker
	Conn            *websocket.Conn
	LastPing        time.Time
	lastMessageTime time.Time // Server time when the last message was received
}

// NewWorkerManager creates a new worker manager
func NewWorkerManager() *WorkerManager {
	return &WorkerManager{
		workers: make(map[string]*WorkerConnection),
	}
}

// extractIPFromConn extracts the IP address from a websocket connection
func extractIPFromConn(conn *websocket.Conn) string {
	if conn == nil {
		return "unknown"
	}

	// Get remote address and extract IP
	remoteAddr := conn.RemoteAddr()
	if remoteAddr != nil {
		if tcpAddr, ok := remoteAddr.(*net.TCPAddr); ok {
			return tcpAddr.IP.String()
		}
		// Fallback for other address types
		return remoteAddr.String()
	}

	return "unknown"
}

// RegisterWorker registers a new worker
func (wm *WorkerManager) RegisterWorker(worker *models.Worker, conn *websocket.Conn) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Check if worker ID is unique
	if _, exists := wm.workers[worker.ID]; exists {
		return fmt.Errorf("worker ID %s already registered", worker.ID)
	}

	log.Printf("Registering worker: %s (%s)", worker.Name, worker.ID)
	wm.workers[worker.ID] = &WorkerConnection{
		Worker:          worker,
		Conn:            conn,
		LastPing:        time.Now(),
		lastMessageTime: time.Now(), // Initialize with current time
	}

	// Set IP address if not provided by worker
	if worker.IPAddress == "" {
		worker.IPAddress = extractIPFromConn(conn)
	}

	// Initialize latency to 0
	worker.Latency = 0

	fmt.Printf("Worker registered: %s (%s) from %s\n", worker.Name, worker.ID, worker.IPAddress)
	return nil
}

// UnregisterWorker removes a worker
func (wm *WorkerManager) UnregisterWorker(workerID string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if worker, exists := wm.workers[workerID]; exists {
		worker.Conn.Close()
		delete(wm.workers, workerID)
		fmt.Printf("Worker unregistered: %s\n", workerID)
	}
}

// GetWorker returns a specific worker
func (wm *WorkerManager) GetWorker(workerID string) (*WorkerConnection, bool) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	worker, exists := wm.workers[workerID]
	return worker, exists
}

// GetAllWorkers returns all connected workers
func (wm *WorkerManager) GetAllWorkers() []*models.Worker {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	workers := make([]*models.Worker, 0, len(wm.workers))
	for _, wc := range wm.workers {
		workers = append(workers, wc.Worker)
	}
	return workers
}

// GetAvailableWorkers returns workers that can accept jobs
func (wm *WorkerManager) GetAvailableWorkers() []*WorkerConnection {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	available := make([]*WorkerConnection, 0)
	for _, wc := range wm.workers {
		if wc.Worker.Status == models.WorkerStatusOnline || wc.Worker.Status == models.WorkerStatusIdle {
			if wc.Worker.CurrentJobs < wc.Worker.MaxJobs {
				available = append(available, wc)
			}
		}
	}
	return available
}

// UpdateWorkerStatus updates a worker's status
func (wm *WorkerManager) UpdateWorkerStatus(workerID string, status string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wc, exists := wm.workers[workerID]; exists {
		wc.Worker.Status = status
		wc.Worker.LastPing = time.Now()
		wc.LastPing = time.Now()
		return nil
	}
	return fmt.Errorf("worker %s not found", workerID)
}

// UpdateWorkerHeartbeat updates the last ping time for a worker and calculates latency
func (wm *WorkerManager) UpdateWorkerHeartbeat(workerID string, currentJobs int, pingTime int64) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wc, exists := wm.workers[workerID]; exists {
		now := time.Now()
		wc.Worker.LastPing = now
		wc.Worker.CurrentJobs = currentJobs
		wc.LastPing = now

		// Calculate latency if pingTime is provided
		if pingTime > 0 {
			pingTimeMs := time.UnixMilli(pingTime)
			latency := now.Sub(pingTimeMs)
			// Store latency in microseconds for precision
			wc.Worker.Latency = int(latency.Microseconds())
		}

		// Update status based on job count
		if currentJobs >= wc.Worker.MaxJobs {
			wc.Worker.Status = models.WorkerStatusBusy
		} else if currentJobs > 0 {
			wc.Worker.Status = models.WorkerStatusOnline
		} else {
			wc.Worker.Status = models.WorkerStatusIdle
		}

		return nil
	}
	return fmt.Errorf("worker %s not found", workerID)
}

// UpdateWorkerHeartbeatWithUsage updates a worker's status based on a heartbeat message,
// including real-time usage statistics and latency calculation.
func (wm *WorkerManager) UpdateWorkerHeartbeatWithUsage(id string, currentJobs int, gpuUsage, gpuMemory, cpuUsage, networkSpeed float64) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if conn, exists := wm.workers[id]; exists {
		now := time.Now()

		// Update worker state - update BOTH LastPing fields
		conn.Worker.CurrentJobs = currentJobs
		conn.Worker.LastPing = now // Use server time for the heartbeat
		conn.LastPing = now        // Also update connection LastPing for health check
		conn.Worker.Status = models.WorkerStatusOnline

		// Update usage stats
		conn.Worker.GPUUsage = gpuUsage
		conn.Worker.GPUMemory = gpuMemory
		conn.Worker.CPUUsage = cpuUsage
		conn.Worker.NetworkSpeed = networkSpeed

		// Calculate latency based on the last message time, not client-provided time
		conn.Worker.Latency = int(time.Since(conn.lastMessageTime).Milliseconds())

		// Determine if the worker is busy or idle
		if conn.Worker.CurrentJobs >= conn.Worker.MaxJobs {
			conn.Worker.Status = models.WorkerStatusBusy
		} else {
			conn.Worker.Status = models.WorkerStatusIdle
		}
	}
}

// CheckWorkerHealth marks workers as offline if they haven't pinged recently
func (wm *WorkerManager) CheckWorkerHealth() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	now := time.Now()
	for id, worker := range wm.workers {
		// Increase timeout window from 10 to 20 seconds
		if now.Sub(worker.lastMessageTime) > 20*time.Second {
			worker.Worker.Status = "offline"
			log.Printf("Worker %s marked as offline", id)
		}
	}
}

// BroadcastMessage sends a message to all connected workers
func (wm *WorkerManager) BroadcastMessage(message *models.WorkerMessage) error {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	var errors []error
	for _, wc := range wm.workers {
		if err := wc.Conn.WriteJSON(message); err != nil {
			errors = append(errors, fmt.Errorf("failed to send to worker %s: %w", wc.Worker.ID, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("broadcast errors: %v", errors)
	}
	return nil
}

// SendToWorker sends a message to a specific worker
func (wm *WorkerManager) SendToWorker(workerID string, message *models.WorkerMessage) error {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	if wc, exists := wm.workers[workerID]; exists {
		return wc.Conn.WriteJSON(message)
	}
	return fmt.Errorf("worker %s not found", workerID)
}

func (wm *WorkerManager) checkOfflineWorkers() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	timeout := 10 * time.Second // Increased from 5s to allow for network delays
	for id, conn := range wm.workers {
		if time.Since(conn.LastPing) > timeout {
			if conn.Worker.Status != models.WorkerStatusOffline {
				log.Printf("Worker %s marked as offline (no heartbeat)", id)
				conn.Worker.Status = models.WorkerStatusOffline
			}
		}
	}
}

// RecordMessageTime updates the last message timestamp for a given worker.
// This is used for server-side latency calculations.
func (wm *WorkerManager) RecordMessageTime(workerID string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if conn, exists := wm.workers[workerID]; exists {
		conn.lastMessageTime = time.Now()
	}
}
