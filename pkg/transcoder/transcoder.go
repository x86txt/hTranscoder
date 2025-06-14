package transcoder

import (
	"fmt"
	"os/exec"
	"strings"
)

// Transcoder handles video encoding operations
type Transcoder struct {
	UseGPU    bool
	GPUDevice string
}

// NewTranscoder creates a new transcoder instance
func NewTranscoder(useGPU bool, gpuDevice string) *Transcoder {
	return &Transcoder{
		UseGPU:    useGPU,
		GPUDevice: gpuDevice,
	}
}

// EncodeOptions contains encoding parameters
type EncodeOptions struct {
	InputPath  string
	OutputPath string
	Codec      string
	Bitrate    string
	Resolution string
	Preset     string
}

// EncodeChunk encodes a video chunk with the specified options
func (t *Transcoder) EncodeChunk(opts EncodeOptions) error {
	args := []string{
		"-i", opts.InputPath,
		"-y", // Overwrite output file
	}

	// Add codec-specific options
	switch opts.Codec {
	case "h264":
		if t.UseGPU {
			// Use NVIDIA NVENC for GPU encoding
			args = append(args,
				"-c:v", "h264_nvenc",
				"-preset", mapPresetToNVENC(opts.Preset),
				"-b:v", opts.Bitrate,
			)

			// Add GPU device selection if specified
			if t.GPUDevice != "" {
				args = append(args, "-gpu", t.GPUDevice)
			}
		} else {
			// Use libx264 for CPU encoding
			args = append(args,
				"-c:v", "libx264",
				"-preset", mapPresetTox264(opts.Preset),
				"-b:v", opts.Bitrate,
			)
		}
	default:
		return fmt.Errorf("unsupported codec: %s", opts.Codec)
	}

	// Add resolution scaling if specified
	if opts.Resolution != "" {
		scale := getScaleFilter(opts.Resolution)
		if scale != "" {
			args = append(args, "-vf", scale)
		}
	}

	// Add audio copy to preserve original audio
	args = append(args,
		"-c:a", "copy",
		opts.OutputPath,
	)

	// Execute ffmpeg command
	cmd := exec.Command("ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("encoding failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// GetProgress parses FFmpeg output to extract encoding progress
func (t *Transcoder) GetProgress(output string) int {
	// Look for time= in FFmpeg output
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "time=") {
			// Parse time and calculate progress
			// This is a simplified version - real implementation would need duration
			// to calculate actual percentage
			return 50 // Placeholder
		}
	}
	return 0
}

// mapPresetToNVENC maps generic preset names to NVENC-specific presets
func mapPresetToNVENC(preset string) string {
	switch preset {
	case "ultrafast", "superfast":
		return "p1"
	case "veryfast":
		return "p2"
	case "faster":
		return "p3"
	case "fast":
		return "p4"
	case "medium":
		return "p5"
	case "slow":
		return "p6"
	case "slower", "veryslow":
		return "p7"
	default:
		return "p4" // Default to p4 (fast)
	}
}

// mapPresetTox264 maps generic preset names to x264 presets
func mapPresetTox264(preset string) string {
	validPresets := map[string]bool{
		"ultrafast": true,
		"superfast": true,
		"veryfast":  true,
		"faster":    true,
		"fast":      true,
		"medium":    true,
		"slow":      true,
		"slower":    true,
		"veryslow":  true,
	}

	if validPresets[preset] {
		return preset
	}
	return "medium" // Default
}

// getScaleFilter returns the FFmpeg scale filter for common resolutions
func getScaleFilter(resolution string) string {
	switch resolution {
	case "1080p":
		return "scale=-2:1080"
	case "720p":
		return "scale=-2:720"
	case "480p":
		return "scale=-2:480"
	case "4k", "2160p":
		return "scale=-2:2160"
	default:
		return ""
	}
}
