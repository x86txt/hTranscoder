# Video Chunking System

This document describes the video chunking implementation in hTranscode, which enables parallel video processing across multiple workers.

## Overview

The chunking system automatically splits video files into smaller segments that can be processed simultaneously by different workers, significantly reducing transcoding time for large videos.

### Key Features

- **Automatic Chunking**: Videos are split into the same number of chunks as available workers
- **Parallel Processing**: Each chunk is processed by a different worker simultaneously
- **Automatic Reassembly**: Completed chunks are automatically merged back into the final video
- **Fault Tolerance**: Failed chunks can be retried or reassigned to other workers
- **Progress Tracking**: Real-time progress updates for both individual chunks and overall job
- **Cleanup**: Temporary chunk files are automatically cleaned up after processing

## Architecture

### Components

1. **VideoChunker** (`pkg/chunker/chunker.go`)
   - Splits videos into time-based chunks
   - Merges processed chunks back into final video
   - Supports both duration-based and count-based splitting

2. **JobManager** (`internal/manager/job_manager.go`)
   - Manages the complete chunking workflow
   - Distributes chunks to available workers
   - Tracks progress and handles completion
   - Handles cleanup and error recovery

3. **Worker Support** (`pkg/worker/client.go`)
   - Workers already support chunk processing
   - Sends progress updates with chunk IDs
   - Handles chunk-specific transcoding settings

### Workflow

```
1. User submits video for transcoding
2. System counts available workers
3. Video is split into N chunks (where N = number of workers)
4. Each chunk is assigned to a different worker
5. Workers process chunks in parallel
6. Progress is tracked for each chunk
7. When all chunks complete, they are merged
8. Final video is delivered to user
9. Temporary files are cleaned up
```

## Implementation Details

### Video Chunking Approaches

The system uses two different chunking strategies depending on worker location:

#### 1. **Physical Chunking (Local Workers)**
For workers running on the same machine as the master:

- **Pre-splitting**: Master physically splits video into files using FFmpeg
- **Direct File Access**: Workers process pre-created chunk files  
- **Efficient**: Avoids duplicate processing of the same video segments
- **File Paths**: Workers receive paths to physical chunk files

Example command:
```bash
ffmpeg -i input.mp4 -ss 0.00 -t 20.00 -c copy -avoid_negative_ts make_zero chunk_000.mp4
```

#### 2. **Virtual Chunking (Remote Workers)**
For workers running on different machines:

- **Timing-Based**: Master calculates time segments and sends timing info
- **On-Demand Extraction**: Workers extract their assigned segment from original video
- **Network-Friendly**: Avoids transferring large chunk files over network
- **Time Segments**: Workers receive start time + duration parameters

Example worker process:
```bash
# Worker receives: start=60.0s, duration=30.0s
ffmpeg -i original.mp4 -ss 60.00 -t 30.00 -c copy segment.mp4
# Then encodes: ffmpeg -i segment.mp4 [encoding options] output.mp4
```

### Worker Detection

The system automatically categorizes workers:

```go
// Local workers (same machine as master)
isLocal := worker.IPAddress == "127.0.0.1" || worker.IPAddress == "::1" || worker.IPAddress == "localhost"

// Remote workers (different machines)  
isRemote := !isLocal
```

### Chunk Distribution

- **Round-Robin Assignment**: Chunks are distributed evenly among workers
- **Worker Availability**: Only online/idle workers receive chunks
- **Load Balancing**: Workers with lower current job counts are preferred
- **Failure Handling**: If chunk assignment fails, job status is updated accordingly

### Progress Tracking

Each chunk reports progress independently:

- **Chunk Status**: `pending`, `processing`, `completed`, `failed`
- **Progress Percentage**: 0-100% for each chunk
- **Overall Progress**: Calculated as average of all chunk progress
- **Real-time Updates**: Progress updates sent via WebSocket

### Merging Process

Completed chunks are merged using FFmpeg concat:

1. Create temporary list file with chunk paths
2. Use FFmpeg concat demuxer: `ffmpeg -f concat -safe 0 -i list.txt -c copy output.mp4`
3. Verify output file creation
4. Clean up temporary files

## Configuration

### Chunking Behavior

The system automatically determines chunk count based on available workers:

```go
// Get available workers
availableWorkers := workerManager.GetAvailableWorkers()
numChunks := len(availableWorkers)
```

### Directory Structure

```
/temp_cache_dir/
  ├── job_123456789/           # Job-specific directory
  │   ├── job_123456789_chunk_000.mp4    # Original chunks
  │   ├── job_123456789_chunk_001.mp4
  │   ├── job_123456789_encoded_chunk_000.mp4  # Encoded chunks
  │   └── job_123456789_encoded_chunk_001.mp4
  └── ...

/transcoded/
  └── job_123456789_transcoded.mp4  # Final merged output
```

## Usage Examples

### Basic Usage

The chunking system is automatically enabled when multiple workers are available:

1. Start multiple workers:
   ```bash
   ./worker -name worker1 &
   ./worker -name worker2 &
   ./worker -name worker3 &
   ```

2. Submit a video through the web interface or API:
   ```bash
   curl -X POST https://localhost:8080/api/encode \
     -H "Content-Type: application/json" \
     -d '{"videoPath": "/path/to/video.mp4", "preset": "medium"}'
   ```

3. The system will automatically:
   - Split the video into 3 chunks (for 3 workers)
   - Distribute chunks to workers
   - Show progress for each chunk
   - Merge completed chunks
   - Deliver final video

### Manual Testing

Use the provided test script:

```bash
# Place a sample video in sample_videos/sample.mp4
go run test_chunking.go
```

This will test:
- Video information extraction
- Chunk splitting
- Chunk merging
- JobManager functionality

## Error Handling

### Common Issues

1. **No Workers Available**
   - Error: "no available workers"
   - Solution: Start at least one worker

2. **Chunk Processing Failures**
   - Individual chunks can fail without affecting others
   - Failed chunks are logged and job status updated
   - Consider implementing retry logic for production

3. **Merge Failures**
   - If merging fails, temporary chunks are preserved
   - Check FFmpeg installation and permissions
   - Verify output directory permissions

### Debugging

Enable detailed logging by checking:

- Master server logs for job distribution
- Worker logs for chunk processing
- FFmpeg output for transcoding errors

## Performance Considerations

### Optimal Worker Count

- **Sweet Spot**: 2-8 workers for most videos
- **Diminishing Returns**: Beyond 8 workers, overhead may outweigh benefits
- **Video Length**: Longer videos benefit more from chunking
- **File Size**: Larger files see greater performance improvements

### Chunk Size Guidelines

- **Minimum Duration**: Avoid chunks shorter than 10 seconds
- **Maximum Count**: Consider limiting to 16 chunks max
- **Storage Overhead**: Each chunk requires temporary disk space

### Network Considerations

- Workers need sufficient bandwidth to receive/send chunks
- Local workers perform better than remote workers for large files
- Consider chunk size vs. network transfer time

## Future Enhancements

### **High Priority (Remote Worker Support)**

- [ ] **Automatic File Transfer**: Upload completed chunks back to master
  ```go
  // Workers upload via HTTP POST or WebSocket binary transfer
  POST /api/chunks/upload
  Content-Type: multipart/form-data
  ```

- [ ] **Shared Storage Detection**: Auto-detect NFS/SMB mounted storage
  ```bash
  # Auto-detect shared paths
  /shared, /mnt, /network -> shared storage
  /home, /tmp, /var -> local storage  
  ```

- [ ] **HTTP Video Streaming**: Stream video segments to workers
  ```http
  GET /api/video/stream?file=video.mp4&start=60&duration=30
  Range: bytes=1048576-2097152
  ```

### **Medium Priority (Optimization)**

- [ ] **Smart Chunk Sizing**: Adjust chunk size based on video content and network
- [ ] **Retry Logic**: Automatic retry for failed chunks with exponential backoff
- [ ] **Chunk Prioritization**: Process important chunks first (beginning/end)
- [ ] **Progressive Delivery**: Stream chunks as they complete
- [ ] **Quality Validation**: Verify chunk quality before merging

### **Low Priority (Advanced Features)**

- [ ] **Custom Chunk Boundaries**: Split at scene changes using ML
- [ ] **Parallel Encoding Passes**: Multi-pass encoding with chunking
- [ ] **Adaptive Bitrate**: Different quality for different chunks
- [ ] **GPU Load Balancing**: Distribute based on GPU capabilities
- [ ] **Cloud Integration**: Auto-scale workers in cloud environments

## API Reference

### JobManager Methods

```go
// Create a new job with automatic chunking
func (jm *JobManager) CreateJob(videoPath string, settings *models.EncodeSettings) (*models.Job, error)

// Update chunk progress
func (jm *JobManager) UpdateChunkProgress(chunkID, status string, progress int, errorMsg string) error

// Get job status
func (jm *JobManager) GetJob(jobID string) (*models.Job, bool)

// List all jobs
func (jm *JobManager) GetAllJobs() []*models.Job

// Delete job and cleanup
func (jm *JobManager) DeleteJob(jobID string) error
```

### VideoChunker Methods

```go
// Split video by chunk count
func (vc *VideoChunker) SplitVideoByChunks(videoPath, outputDir, jobID string, numChunks int) ([]Chunk, error)

// Get video information
func (vc *VideoChunker) GetVideoInfo(videoPath string) (*VideoInfo, error)

// Merge chunks
func (vc *VideoChunker) MergeChunks(chunks []string, outputPath string) error
```

## Conclusion

The chunking system provides significant performance improvements for video transcoding by enabling parallel processing across multiple workers. It's designed to be automatic and transparent to users while providing the flexibility needed for various deployment scenarios.

For questions or issues, check the logs and refer to the troubleshooting section above.

## Current Limitations & Requirements

### File Access Requirements

#### **For Local Workers:**
- ✅ **Ready to Use**: Works out of the box
- ✅ **Shared File System**: Master and workers share the same file system
- ✅ **Efficient Processing**: Pre-split chunks avoid duplicate processing

#### **For Remote Workers:**
- ⚠️ **Original Video Access**: Remote workers must have access to the original video file
- ⚠️ **File Transfer Gap**: Completed chunks must be accessible to master for merging
- ⚠️ **Network Bandwidth**: Workers download/access full original video

### Current Workarounds

**Option 1: Shared Network Storage**
```bash
# Mount shared storage on all machines
sudo mount -t nfs master:/shared /shared
# Place videos in shared location
cp video.mp4 /shared/videos/
# Workers can access /shared/videos/video.mp4
```

**Option 2: Local File Replication**
```bash
# Copy video to all worker machines
scp video.mp4 worker1:/path/to/video.mp4
scp video.mp4 worker2:/path/to/video.mp4
# Update paths in job submission
```

**Option 3: HTTP/HTTPS Access**
```bash
# Serve videos via HTTP from master
python3 -m http.server 8000 --directory /videos
# Workers access: http://master:8000/video.mp4
```

### Known Issues

1. **Remote Chunk Collection**: 
   - Remote workers produce chunks locally
   - Master cannot access remote chunks for merging
   - **Workaround**: Use shared storage or implement file transfer

2. **Network Efficiency**:
   - Each remote worker downloads full original video
   - **Future**: Implement chunk streaming or delta transfer

3. **Path Resolution**:
   - Original video paths must be valid on worker machines
   - **Workaround**: Use absolute paths or shared mount points 