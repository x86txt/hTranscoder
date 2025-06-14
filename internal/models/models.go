package models

import (
	"time"
)

// Worker represents a transcoding worker node
type Worker struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Address      string    `json:"address"`
	IPAddress    string    `json:"ipAddress"`
	Status       string    `json:"status"`
	CPU          string    `json:"cpu"`
	GPU          string    `json:"gpu"`
	MaxJobs      int       `json:"maxJobs"`
	CurrentJobs  int       `json:"currentJobs"`
	LastPing     time.Time `json:"lastPing"`
	Latency      int       `json:"latency"` // milliseconds
	Capabilities []string  `json:"capabilities"`
	// Real-time usage statistics
	GPUUsage     float64 `json:"gpuUsage"`     // GPU utilization percentage
	GPUMemory    float64 `json:"gpuMemory"`    // GPU memory usage percentage
	CPUUsage     float64 `json:"cpuUsage"`     // CPU usage percentage
	NetworkSpeed float64 `json:"networkSpeed"` // Network usage in MB/s
}

// Job represents a video encoding job
type Job struct {
	ID          string          `json:"id"`
	VideoPath   string          `json:"videoPath"`
	OutputPath  string          `json:"outputPath"`
	Status      string          `json:"status"` // queued, processing, completed, failed
	Progress    int             `json:"progress"`
	TotalChunks int             `json:"totalChunks"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	Error       string          `json:"error,omitempty"`
	Settings    *EncodeSettings `json:"settings"`
}

// EncodeSettings contains encoding parameters
type EncodeSettings struct {
	Codec      string `json:"codec"`
	Bitrate    string `json:"bitrate"`
	Resolution string `json:"resolution"`
	Preset     string `json:"preset"`
	GPU        bool   `json:"gpu"`
}

// ChunkJob represents a single chunk encoding task
type ChunkJob struct {
	ID         string          `json:"id"`
	JobID      string          `json:"jobId"`
	ChunkIndex int             `json:"chunkIndex"`
	InputPath  string          `json:"inputPath"`  // Original video path (for remote workers)
	OutputPath string          `json:"outputPath"` // Where to save encoded result
	WorkerID   string          `json:"workerId"`
	Status     string          `json:"status"`
	Progress   int             `json:"progress"`
	StartTime  time.Time       `json:"startTime"`
	EndTime    time.Time       `json:"endTime"`
	RetryCount int             `json:"retryCount"`
	Settings   *EncodeSettings `json:"settings"`
	// Virtual chunking information for remote workers
	SegmentStart    float64 `json:"segmentStart"`    // Start time in seconds
	SegmentDuration float64 `json:"segmentDuration"` // Duration in seconds
	TotalDuration   float64 `json:"totalDuration"`   // Total video duration
	IsRemoteWorker  bool    `json:"isRemoteWorker"`  // Whether this is for a remote worker
}

// WorkerMessage represents messages between master and workers
type WorkerMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// Constants for job and worker statuses
const (
	JobStatusQueued     = "queued"
	JobStatusProcessing = "processing"
	JobStatusCompleted  = "completed"
	JobStatusFailed     = "failed"

	WorkerStatusOnline  = "online"
	WorkerStatusOffline = "offline"
	WorkerStatusBusy    = "busy"
	WorkerStatusIdle    = "idle"

	ChunkStatusPending    = "pending"
	ChunkStatusProcessing = "processing"
	ChunkStatusCompleted  = "completed"
	ChunkStatusFailed     = "failed"
)
