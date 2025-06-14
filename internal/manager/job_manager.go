package manager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"hTranscode/internal/models"
	"hTranscode/pkg/chunker"
)

// JobManager manages video transcoding jobs with chunking
type JobManager struct {
	jobs          map[string]*models.Job
	chunkJobs     map[string]*models.ChunkJob // chunk job ID -> chunk job
	jobChunks     map[string][]string         // job ID -> chunk job IDs
	workerManager *WorkerManager
	videoChunker  *chunker.VideoChunker
	mu            sync.RWMutex
	tempDir       string
	outputDir     string
}

// NewJobManager creates a new job manager
func NewJobManager(workerManager *WorkerManager, tempDir, outputDir string) *JobManager {
	return &JobManager{
		jobs:          make(map[string]*models.Job),
		chunkJobs:     make(map[string]*models.ChunkJob),
		jobChunks:     make(map[string][]string),
		workerManager: workerManager,
		videoChunker:  chunker.NewVideoChunkerByCount(1), // Will be updated per job
		tempDir:       tempDir,
		outputDir:     outputDir,
	}
}

// CreateJob creates a new transcoding job with chunking
func (jm *JobManager) CreateJob(videoPath string, settings *models.EncodeSettings) (*models.Job, error) {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	// Generate job ID
	jobID := generateJobID()

	// Get available workers
	availableWorkers := jm.workerManager.GetAvailableWorkers()
	if len(availableWorkers) == 0 {
		return nil, fmt.Errorf("no available workers")
	}

	// Determine number of chunks based on available workers
	numChunks := len(availableWorkers)

	// Create job
	job := &models.Job{
		ID:          jobID,
		VideoPath:   videoPath,
		OutputPath:  filepath.Join(jm.outputDir, fmt.Sprintf("%s_transcoded.mp4", jobID)),
		Status:      models.JobStatusQueued,
		Progress:    0,
		TotalChunks: numChunks,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Settings:    settings,
	}

	// Store job
	jm.jobs[jobID] = job

	log.Printf("Created job %s for video %s with %d chunks", jobID, videoPath, numChunks)

	// Start chunking and distribution in background
	go jm.processJob(job)

	return job, nil
}

// processJob handles the chunking and distribution of a job
func (jm *JobManager) processJob(job *models.Job) {
	log.Printf("Processing job %s", job.ID)

	// Update job status
	jm.updateJobStatus(job.ID, models.JobStatusProcessing)

	// Create temp directory for chunks
	chunkDir := filepath.Join(jm.tempDir, job.ID)
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		jm.failJob(job.ID, fmt.Sprintf("failed to create chunk directory: %v", err))
		return
	}

	// Split video into chunks
	chunks, err := jm.videoChunker.SplitVideoByChunks(job.VideoPath, chunkDir, job.ID, job.TotalChunks)
	if err != nil {
		jm.failJob(job.ID, fmt.Sprintf("failed to split video: %v", err))
		return
	}

	log.Printf("Split job %s into %d chunks", job.ID, len(chunks))

	// Create chunk jobs and distribute to workers
	availableWorkers := jm.workerManager.GetAvailableWorkers()
	if len(availableWorkers) == 0 {
		jm.failJob(job.ID, "no available workers")
		return
	}

	jm.mu.Lock()
	chunkJobIDs := make([]string, 0, len(chunks))

	for i, chunk := range chunks {
		// Select worker (round-robin)
		worker := availableWorkers[i%len(availableWorkers)]

		// Generate output path for encoded chunk
		encodedChunkPath := filepath.Join(chunkDir, fmt.Sprintf("%s_encoded_chunk_%03d.mp4", job.ID, i))

		// Create chunk job
		chunkJob := &models.ChunkJob{
			ID:         chunk.ID,
			JobID:      job.ID,
			ChunkIndex: i,
			InputPath:  chunk.Path,
			OutputPath: encodedChunkPath,
			WorkerID:   worker.Worker.ID,
			Status:     models.ChunkStatusPending,
			Progress:   0,
			StartTime:  time.Now(),
			RetryCount: 0,
			Settings:   job.Settings,
		}

		// Store chunk job
		jm.chunkJobs[chunk.ID] = chunkJob
		chunkJobIDs = append(chunkJobIDs, chunk.ID)

		// Send chunk job to worker
		message := &models.WorkerMessage{
			Type:    "chunk_job",
			Payload: chunkJob,
		}

		if err := jm.workerManager.SendToWorker(worker.Worker.ID, message); err != nil {
			log.Printf("Failed to send chunk job %s to worker %s: %v", chunk.ID, worker.Worker.ID, err)
			chunkJob.Status = models.ChunkStatusFailed
		} else {
			log.Printf("Sent chunk job %s to worker %s", chunk.ID, worker.Worker.ID)
			chunkJob.Status = models.ChunkStatusProcessing
		}
	}

	// Store chunk job IDs for this job
	jm.jobChunks[job.ID] = chunkJobIDs
	jm.mu.Unlock()
}

// UpdateChunkProgress updates the progress of a chunk job
func (jm *JobManager) UpdateChunkProgress(chunkID, status string, progress int, errorMsg string) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	chunkJob, exists := jm.chunkJobs[chunkID]
	if !exists {
		return fmt.Errorf("chunk job %s not found", chunkID)
	}

	// Update chunk job
	chunkJob.Status = status
	chunkJob.Progress = progress
	if status == models.ChunkStatusCompleted {
		chunkJob.EndTime = time.Now()
	}

	log.Printf("Updated chunk %s: status=%s, progress=%d%%", chunkID, status, progress)

	// Check if all chunks are completed for this job
	if status == models.ChunkStatusCompleted || status == models.ChunkStatusFailed {
		jm.checkJobCompletion(chunkJob.JobID)
	}

	return nil
}

// checkJobCompletion checks if all chunks for a job are completed and merges them
func (jm *JobManager) checkJobCompletion(jobID string) {
	job, exists := jm.jobs[jobID]
	if !exists {
		return
	}

	chunkIDs, exists := jm.jobChunks[jobID]
	if !exists {
		return
	}

	// Check status of all chunks
	completedChunks := 0
	failedChunks := 0
	totalProgress := 0

	for _, chunkID := range chunkIDs {
		chunkJob := jm.chunkJobs[chunkID]
		switch chunkJob.Status {
		case models.ChunkStatusCompleted:
			completedChunks++
			totalProgress += 100
		case models.ChunkStatusFailed:
			failedChunks++
		default:
			totalProgress += chunkJob.Progress
		}
	}

	// Update job progress
	job.Progress = totalProgress / len(chunkIDs)
	job.UpdatedAt = time.Now()

	// Check if all chunks are completed
	if completedChunks == len(chunkIDs) {
		log.Printf("All chunks completed for job %s, starting merge", jobID)
		go jm.mergeChunks(jobID)
	} else if failedChunks > 0 && completedChunks+failedChunks == len(chunkIDs) {
		// Some chunks failed and no more are processing
		jm.failJob(jobID, fmt.Sprintf("%d chunks failed", failedChunks))
	}
}

// mergeChunks merges completed chunks back into final video
func (jm *JobManager) mergeChunks(jobID string) {
	jm.mu.RLock()
	job := jm.jobs[jobID]
	chunkIDs := jm.jobChunks[jobID]
	jm.mu.RUnlock()

	if job == nil {
		return
	}

	log.Printf("Merging chunks for job %s", jobID)

	// Collect encoded chunk paths in order
	chunkPaths := make([]string, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		chunkJob := jm.chunkJobs[chunkID]
		chunkPaths[chunkJob.ChunkIndex] = chunkJob.OutputPath
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(job.OutputPath), 0755); err != nil {
		jm.failJob(jobID, fmt.Sprintf("failed to create output directory: %v", err))
		return
	}

	// Merge chunks
	if err := jm.videoChunker.MergeChunks(chunkPaths, job.OutputPath); err != nil {
		jm.failJob(jobID, fmt.Sprintf("failed to merge chunks: %v", err))
		return
	}

	// Update job status
	jm.updateJobStatus(jobID, models.JobStatusCompleted)
	jm.mu.Lock()
	job.Progress = 100
	jm.mu.Unlock()

	log.Printf("Job %s completed successfully", jobID)

	// Clean up temporary chunks
	go jm.cleanupChunks(jobID)
}

// cleanupChunks removes temporary chunk files
func (jm *JobManager) cleanupChunks(jobID string) {
	chunkDir := filepath.Join(jm.tempDir, jobID)
	if err := os.RemoveAll(chunkDir); err != nil {
		log.Printf("Warning: failed to clean up chunks for job %s: %v", jobID, err)
	} else {
		log.Printf("Cleaned up chunks for job %s", jobID)
	}
}

// updateJobStatus updates a job's status
func (jm *JobManager) updateJobStatus(jobID, status string) {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	if job, exists := jm.jobs[jobID]; exists {
		job.Status = status
		job.UpdatedAt = time.Now()
	}
}

// failJob marks a job as failed
func (jm *JobManager) failJob(jobID, errorMsg string) {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	if job, exists := jm.jobs[jobID]; exists {
		job.Status = models.JobStatusFailed
		job.Error = errorMsg
		job.UpdatedAt = time.Now()
		log.Printf("Job %s failed: %s", jobID, errorMsg)
	}
}

// GetJob returns a job by ID
func (jm *JobManager) GetJob(jobID string) (*models.Job, bool) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, exists := jm.jobs[jobID]
	return job, exists
}

// GetAllJobs returns all jobs
func (jm *JobManager) GetAllJobs() []*models.Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]*models.Job, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		jobs = append(jobs, job)
	}

	// Sort by creation time (newest first)
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})

	return jobs
}

// DeleteJob removes a job and cancels any in-progress chunks
func (jm *JobManager) DeleteJob(jobID string) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	_, exists := jm.jobs[jobID]
	if !exists {
		return fmt.Errorf("job %s not found", jobID)
	}

	// TODO: Send cancel messages to workers for in-progress chunks

	// Remove job and associated chunk jobs
	delete(jm.jobs, jobID)
	if chunkIDs, exists := jm.jobChunks[jobID]; exists {
		for _, chunkID := range chunkIDs {
			delete(jm.chunkJobs, chunkID)
		}
		delete(jm.jobChunks, jobID)
	}

	log.Printf("Deleted job %s", jobID)

	// Clean up chunks in background
	go jm.cleanupChunks(jobID)

	return nil
}

// generateJobID generates a unique job ID
func generateJobID() string {
	return fmt.Sprintf("job_%d", time.Now().UnixNano())
}
