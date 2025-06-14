package chunker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// VideoChunker handles splitting videos into chunks
type VideoChunker struct {
	ChunkDuration int // Duration of each chunk in seconds
}

// NewVideoChunker creates a new VideoChunker instance
func NewVideoChunker(chunkDuration int) *VideoChunker {
	if chunkDuration <= 0 {
		chunkDuration = 60 // Default to 60 seconds chunks
	}
	return &VideoChunker{
		ChunkDuration: chunkDuration,
	}
}

// VideoInfo contains information about a video file
type VideoInfo struct {
	Path     string
	Duration float64 // Duration in seconds
	Size     int64
}

// Chunk represents a video chunk
type Chunk struct {
	ID       string
	Index    int
	Path     string
	Start    float64
	Duration float64
}

// GetVideoInfo retrieves information about a video file using ffprobe
func (vc *VideoChunker) GetVideoInfo(videoPath string) (*VideoInfo, error) {
	// Get video duration
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get video info: %w", err)
	}

	duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse duration: %w", err)
	}

	// Get file size
	fileInfo, err := os.Stat(videoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	return &VideoInfo{
		Path:     videoPath,
		Duration: duration,
		Size:     fileInfo.Size(),
	}, nil
}

// SplitVideo splits a video into equal-sized chunks
func (vc *VideoChunker) SplitVideo(videoPath string, outputDir string, jobID string) ([]Chunk, error) {
	// Get video info
	info, err := vc.GetVideoInfo(videoPath)
	if err != nil {
		return nil, err
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Calculate number of chunks
	numChunks := int(info.Duration / float64(vc.ChunkDuration))
	if info.Duration > float64(numChunks*vc.ChunkDuration) {
		numChunks++
	}

	chunks := make([]Chunk, 0, numChunks)

	// Split video into chunks
	for i := 0; i < numChunks; i++ {
		start := float64(i * vc.ChunkDuration)
		duration := float64(vc.ChunkDuration)

		// Adjust duration for last chunk
		if start+duration > info.Duration {
			duration = info.Duration - start
		}

		chunkPath := filepath.Join(outputDir, fmt.Sprintf("%s_chunk_%03d.mp4", jobID, i))

		// Use ffmpeg to extract chunk
		cmd := exec.Command("ffmpeg",
			"-i", videoPath,
			"-ss", fmt.Sprintf("%.2f", start),
			"-t", fmt.Sprintf("%.2f", duration),
			"-c", "copy", // Copy codec to avoid re-encoding during split
			"-avoid_negative_ts", "make_zero",
			chunkPath,
		)

		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to create chunk %d: %w", i, err)
		}

		chunks = append(chunks, Chunk{
			ID:       fmt.Sprintf("%s_%03d", jobID, i),
			Index:    i,
			Path:     chunkPath,
			Start:    start,
			Duration: duration,
		})
	}

	return chunks, nil
}

// MergeChunks merges encoded chunks back into a single video
func (vc *VideoChunker) MergeChunks(chunks []string, outputPath string) error {
	// Create a temporary file list for ffmpeg concat
	listFile := outputPath + ".txt"
	defer os.Remove(listFile)

	file, err := os.Create(listFile)
	if err != nil {
		return fmt.Errorf("failed to create list file: %w", err)
	}
	defer file.Close()

	// Write chunk paths to list file
	for _, chunk := range chunks {
		_, err := fmt.Fprintf(file, "file '%s'\n", chunk)
		if err != nil {
			return fmt.Errorf("failed to write to list file: %w", err)
		}
	}

	// Use ffmpeg concat to merge chunks
	cmd := exec.Command("ffmpeg",
		"-f", "concat",
		"-safe", "0",
		"-i", listFile,
		"-c", "copy",
		outputPath,
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to merge chunks: %w", err)
	}

	return nil
}
