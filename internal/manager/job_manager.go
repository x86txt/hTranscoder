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

	// Get video information for timing calculations
	videoInfo, err := jm.videoChunker.GetVideoInfo(job.VideoPath)
	if err != nil {
		jm.failJob(job.ID, fmt.Sprintf("failed to get video info: %v", err))
		return
	}

	log.Printf("Video duration: %.2f seconds", videoInfo.Duration)

	// Get available workers and categorize them
	availableWorkers := jm.workerManager.GetAvailableWorkers()
	if len(availableWorkers) == 0 {
		jm.failJob(job.ID, "no available workers")
		return
	}

	localWorkers, remoteWorkers := jm.categorizeWorkers(availableWorkers)
	log.Printf("Found %d local workers, %d remote workers", len(localWorkers), len(remoteWorkers))

	// Create temp directory for local chunks if we have local workers
	var chunkDir string
	var localChunks []chunker.Chunk

	if len(localWorkers) > 0 {
		chunkDir = filepath.Join(jm.tempDir, job.ID)
		if err := os.MkdirAll(chunkDir, 0755); err != nil {
			jm.failJob(job.ID, fmt.Sprintf("failed to create chunk directory: %v", err))
			return
		}

		// Create physical chunks for local workers
		localChunks, err = jm.videoChunker.SplitVideoByChunks(job.VideoPath, chunkDir, job.ID, len(localWorkers))
		if err != nil {
			jm.failJob(job.ID, fmt.Sprintf("failed to split video: %v", err))
			return
		}
		log.Printf("Created %d physical chunks for local workers", len(localChunks))
	}

	// Calculate chunk duration for virtual chunks
	chunkDuration := videoInfo.Duration / float64(job.TotalChunks)

	jm.mu.Lock()
	chunkJobIDs := make([]string, 0, job.TotalChunks)
	chunkIndex := 0

	// Process local workers with physical chunks
	for i, worker := range localWorkers {
		if i >= len(localChunks) {
			break
		}

		chunk := localChunks[i]
		encodedChunkPath := filepath.Join(chunkDir, fmt.Sprintf("%s_encoded_chunk_%03d.mp4", job.ID, chunkIndex))

		chunkJob := &models.ChunkJob{
			ID:              chunk.ID,
			JobID:           job.ID,
			ChunkIndex:      chunkIndex,
			InputPath:       chunk.Path, // Physical chunk file
			OutputPath:      encodedChunkPath,
			WorkerID:        worker.Worker.ID,
			Status:          models.ChunkStatusPending,
			Progress:        0,
			StartTime:       time.Now(),
			RetryCount:      0,
			Settings:        job.Settings,
			SegmentStart:    chunk.Start,
			SegmentDuration: chunk.Duration,
			TotalDuration:   videoInfo.Duration,
			IsRemoteWorker:  false,
		}

		jm.sendChunkJobToWorker(chunkJob, worker.Worker.ID, &chunkJobIDs)
		chunkIndex++
	}

	// Process remote workers with virtual chunks
	for _, worker := range remoteWorkers {
		if chunkIndex >= job.TotalChunks {
			break
		}

		segmentStart := float64(chunkIndex) * chunkDuration
		segmentDuration := chunkDuration

		// Adjust last chunk to include any remaining time
		if chunkIndex == job.TotalChunks-1 {
			segmentDuration = videoInfo.Duration - segmentStart
		}

		chunkID := fmt.Sprintf("%s_%03d", job.ID, chunkIndex)
		outputPath := fmt.Sprintf("%s_remote_chunk_%03d.mp4", job.ID, chunkIndex)

		chunkJob := &models.ChunkJob{
			ID:              chunkID,
			JobID:           job.ID,
			ChunkIndex:      chunkIndex,
			InputPath:       job.VideoPath, // Original video path
			OutputPath:      outputPath,    // Worker will determine local output path
			WorkerID:        worker.Worker.ID,
			Status:          models.ChunkStatusPending,
			Progress:        0,
			StartTime:       time.Now(),
			RetryCount:      0,
			Settings:        job.Settings,
			SegmentStart:    segmentStart,
			SegmentDuration: segmentDuration,
			TotalDuration:   videoInfo.Duration,
			IsRemoteWorker:  true,
		}

		jm.sendChunkJobToWorker(chunkJob, worker.Worker.ID, &chunkJobIDs)
		chunkIndex++
	}

	// Store chunk job IDs for this job
	jm.jobChunks[job.ID] = chunkJobIDs
	jm.mu.Unlock()

	log.Printf("Distributed job %s to %d workers (%d local, %d remote)",
		job.ID, len(chunkJobIDs), len(localWorkers), len(remoteWorkers))
}

// categorizeWorkers separates local and remote workers
func (jm *JobManager) categorizeWorkers(workers []*WorkerConnection) ([]*WorkerConnection, []*WorkerConnection) {
	var localWorkers, remoteWorkers []*WorkerConnection

	for _, worker := range workers {
		if jm.isLocalWorker(worker.Worker) {
			localWorkers = append(localWorkers, worker)
		} else {
			remoteWorkers = append(remoteWorkers, worker)
		}
	}

	return localWorkers, remoteWorkers
}

// isLocalWorker determines if a worker is running on the same machine as the master
func (jm *JobManager) isLocalWorker(worker *models.Worker) bool {
	// Check if IP is localhost or loopback
	ip := worker.IPAddress
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost"
}

// sendChunkJobToWorker sends a chunk job to a worker and updates tracking
func (jm *JobManager) sendChunkJobToWorker(chunkJob *models.ChunkJob, workerID string, chunkJobIDs *[]string) {
	// Store chunk job
	jm.chunkJobs[chunkJob.ID] = chunkJob
	*chunkJobIDs = append(*chunkJobIDs, chunkJob.ID)

	// Send chunk job to worker
	message := &models.WorkerMessage{
		Type:    "chunk_job",
		Payload: chunkJob,
	}

	if err := jm.workerManager.SendToWorker(workerID, message); err != nil {
		log.Printf("Failed to send chunk job %s to worker %s: %v", chunkJob.ID, workerID, err)
		chunkJob.Status = models.ChunkStatusFailed
	} else {
		chunkType := "physical"
		if chunkJob.IsRemoteWorker {
			chunkType = "virtual"
		}
		log.Printf("Sent %s chunk job %s to worker %s (%.2fs-%.2fs)",
			chunkType, chunkJob.ID, workerID, chunkJob.SegmentStart, chunkJob.SegmentStart+chunkJob.SegmentDuration)
		chunkJob.Status = models.ChunkStatusProcessing
	}
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

	// Collect encoded chunk paths in order and check availability
	chunkPaths := make([]string, len(chunkIDs))
	missingChunks := make([]string, 0)

	for _, chunkID := range chunkIDs {
		chunkJob := jm.chunkJobs[chunkID]
		chunkPath := chunkJob.OutputPath

		if chunkJob.IsRemoteWorker {
			// For remote workers, the output path might be relative to their working directory
			// We need to construct the full path or handle file transfer
			log.Printf("Remote worker chunk: %s -> %s", chunkID, chunkPath)

			// For now, log this as a limitation
			log.Printf("Warning: Remote worker chunks need to be accessible to master for merging")
			log.Printf("Consider implementing file transfer or shared storage for chunk: %s", chunkPath)
		}

		// Check if chunk file exists
		if _, err := os.Stat(chunkPath); os.IsNotExist(err) {
			missingChunks = append(missingChunks, fmt.Sprintf("%s (%s)", chunkID, chunkPath))
			log.Printf("Missing chunk file: %s", chunkPath)
		}

		chunkPaths[chunkJob.ChunkIndex] = chunkPath
	}

	// If we have missing chunks, fail the job with detailed information
	if len(missingChunks) > 0 {
		errorMsg := fmt.Sprintf("Cannot merge: %d chunks missing: %v", len(missingChunks), missingChunks)
		if len(missingChunks) > 0 {
			// Check if all missing chunks are from remote workers
			allRemote := true
			for _, chunkID := range chunkIDs {
				chunkJob := jm.chunkJobs[chunkID]
				if _, err := os.Stat(chunkJob.OutputPath); os.IsNotExist(err) {
					if !chunkJob.IsRemoteWorker {
						allRemote = false
						break
					}
				}
			}

			if allRemote {
				errorMsg += "\nNote: This appears to be due to remote worker chunks not being transferred back to master."
				errorMsg += "\nConsider implementing shared storage or file transfer mechanism."
			}
		}
		jm.failJob(jobID, errorMsg)
		return
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
