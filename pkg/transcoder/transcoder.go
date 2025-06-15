package transcoder

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// GPUType represents different GPU manufacturers for transcoding
type GPUType string

const (
	GPUTypeNVIDIA GPUType = "nvidia"
	GPUTypeAMD    GPUType = "amd"
	GPUTypeApple  GPUType = "apple"
	GPUTypeIntel  GPUType = "intel"
)

// Transcoder handles video encoding operations
type Transcoder struct {
	UseGPU    bool
	GPUDevice string
	GPUType   GPUType
}

// NewTranscoder creates a new transcoder instance
func NewTranscoder(useGPU bool, gpuDevice string) *Transcoder {
	transcoder := &Transcoder{
		UseGPU:    useGPU,
		GPUDevice: gpuDevice,
	}

	// Auto-detect GPU type if GPU is enabled
	if useGPU {
		transcoder.GPUType = detectGPUType()
	}

	return transcoder
}

// NewTranscoderWithType creates a new transcoder instance with explicit GPU type
func NewTranscoderWithType(useGPU bool, gpuDevice string, gpuType string) *Transcoder {
	transcoder := &Transcoder{
		UseGPU:    useGPU,
		GPUDevice: gpuDevice,
	}

	// Set GPU type from parameter or auto-detect
	if useGPU {
		if gpuType != "" {
			switch gpuType {
			case "nvidia":
				transcoder.GPUType = GPUTypeNVIDIA
			case "amd":
				transcoder.GPUType = GPUTypeAMD
			case "apple":
				transcoder.GPUType = GPUTypeApple
			case "intel":
				transcoder.GPUType = GPUTypeIntel
			default:
				transcoder.GPUType = detectGPUType()
			}
		} else {
			transcoder.GPUType = detectGPUType()
		}
	}

	return transcoder
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

	// Add codec-specific options based on GPU type
	switch opts.Codec {
	case "h264":
		codecArgs, err := t.getH264Args(opts)
		if err != nil {
			return err
		}
		args = append(args, codecArgs...)
	case "h265", "hevc":
		codecArgs, err := t.getH265Args(opts)
		if err != nil {
			return err
		}
		args = append(args, codecArgs...)
	case "av1":
		codecArgs, err := t.getAV1Args(opts)
		if err != nil {
			return err
		}
		args = append(args, codecArgs...)
	default:
		return fmt.Errorf("unsupported codec: %s", opts.Codec)
	}

	// Add resolution scaling if specified
	if opts.Resolution != "" {
		scale := getScaleFilter(opts.Resolution, t.UseGPU, t.GPUType)
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

// getH264Args returns H.264 encoding arguments based on GPU type
func (t *Transcoder) getH264Args(opts EncodeOptions) ([]string, error) {
	if !t.UseGPU {
		// CPU encoding with libx264
		return []string{
			"-c:v", "libx264",
			"-preset", mapPresetTox264(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil
	}

	// GPU encoding based on type
	switch t.GPUType {
	case GPUTypeNVIDIA:
		args := []string{
			"-c:v", "h264_nvenc",
			"-preset", mapPresetToNVENC(opts.Preset),
			"-b:v", opts.Bitrate,
		}
		if t.GPUDevice != "" {
			args = append(args, "-gpu", t.GPUDevice)
		}
		return args, nil

	case GPUTypeAMD:
		// AMD VCE (Video Coding Engine) for H.264
		return []string{
			"-c:v", "h264_amf",
			"-quality", mapPresetToAMF(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil

	case GPUTypeApple:
		// Apple VideoToolbox for H.264
		return []string{
			"-c:v", "h264_videotoolbox",
			"-b:v", opts.Bitrate,
		}, nil

	case GPUTypeIntel:
		// Intel Quick Sync Video for H.264
		return []string{
			"-c:v", "h264_qsv",
			"-preset", mapPresetToQSV(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil

	default:
		// Fallback to CPU
		return []string{
			"-c:v", "libx264",
			"-preset", mapPresetTox264(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil
	}
}

// getH265Args returns H.265/HEVC encoding arguments based on GPU type
func (t *Transcoder) getH265Args(opts EncodeOptions) ([]string, error) {
	if !t.UseGPU {
		// CPU encoding with libx265
		return []string{
			"-c:v", "libx265",
			"-preset", mapPresetTox265(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil
	}

	// GPU encoding based on type
	switch t.GPUType {
	case GPUTypeNVIDIA:
		args := []string{
			"-c:v", "hevc_nvenc",
			"-preset", mapPresetToNVENC(opts.Preset),
			"-b:v", opts.Bitrate,
		}
		if t.GPUDevice != "" {
			args = append(args, "-gpu", t.GPUDevice)
		}
		return args, nil

	case GPUTypeAMD:
		// AMD VCE for HEVC
		return []string{
			"-c:v", "hevc_amf",
			"-quality", mapPresetToAMF(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil

	case GPUTypeApple:
		// Apple VideoToolbox for HEVC
		return []string{
			"-c:v", "hevc_videotoolbox",
			"-b:v", opts.Bitrate,
		}, nil

	case GPUTypeIntel:
		// Intel Quick Sync Video for HEVC
		return []string{
			"-c:v", "hevc_qsv",
			"-preset", mapPresetToQSV(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil

	default:
		// Fallback to CPU
		return []string{
			"-c:v", "libx265",
			"-preset", mapPresetTox265(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil
	}
}

// getAV1Args returns AV1 encoding arguments based on GPU type
func (t *Transcoder) getAV1Args(opts EncodeOptions) ([]string, error) {
	if !t.UseGPU {
		// CPU encoding with libaom-av1 or libsvtav1
		return []string{
			"-c:v", "libsvtav1",
			"-preset", mapPresetToSVTAV1(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil
	}

	// GPU encoding based on type
	switch t.GPUType {
	case GPUTypeNVIDIA:
		// AV1 encoding available on RTX 40 series
		args := []string{
			"-c:v", "av1_nvenc",
			"-preset", mapPresetToNVENC(opts.Preset),
			"-b:v", opts.Bitrate,
		}
		if t.GPUDevice != "" {
			args = append(args, "-gpu", t.GPUDevice)
		}
		return args, nil

	case GPUTypeApple:
		// M3 and newer support AV1 encoding
		return []string{
			"-c:v", "av1_videotoolbox",
			"-b:v", opts.Bitrate,
		}, nil

	case GPUTypeIntel:
		// Intel Arc GPUs support AV1
		return []string{
			"-c:v", "av1_qsv",
			"-preset", mapPresetToQSV(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil

	case GPUTypeAMD:
		// AMD doesn't have widespread AV1 encoding support yet
		// Fall back to CPU encoding
		return []string{
			"-c:v", "libsvtav1",
			"-preset", mapPresetToSVTAV1(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil

	default:
		// Fallback to CPU
		return []string{
			"-c:v", "libsvtav1",
			"-preset", mapPresetToSVTAV1(opts.Preset),
			"-b:v", opts.Bitrate,
		}, nil
	}
}

// detectGPUType detects the primary GPU type for encoding
func detectGPUType() GPUType {
	// Check for NVIDIA first
	if checkNVIDIA() {
		return GPUTypeNVIDIA
	}

	// Check for AMD
	if checkAMD() {
		return GPUTypeAMD
	}

	// Check for Apple Silicon
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return GPUTypeApple
	}

	// Check for Intel
	if checkIntel() {
		return GPUTypeIntel
	}

	// Default fallback
	return GPUTypeNVIDIA
}

// GPU detection helper functions
func checkNVIDIA() bool {
	cmd := exec.Command("nvidia-smi", "--list-gpus")
	return cmd.Run() == nil
}

func checkAMD() bool {
	// Try multiple detection methods
	if runtime.GOOS == "linux" {
		cmd := exec.Command("rocm-smi", "--showid")
		if cmd.Run() == nil {
			return true
		}
	}

	cmd := exec.Command("clinfo")
	output, err := cmd.Output()
	if err == nil {
		return strings.Contains(strings.ToLower(string(output)), "amd") ||
			strings.Contains(strings.ToLower(string(output)), "radeon")
	}

	return false
}

func checkIntel() bool {
	if runtime.GOOS == "linux" {
		cmd := exec.Command("lspci")
		output, err := cmd.Output()
		if err == nil {
			lower := strings.ToLower(string(output))
			return strings.Contains(lower, "intel") &&
				(strings.Contains(lower, "vga") || strings.Contains(lower, "graphics"))
		}
	}
	return false
}

// Preset mapping functions for different encoders
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

func mapPresetToAMF(preset string) string {
	switch preset {
	case "ultrafast", "superfast", "veryfast":
		return "speed"
	case "faster", "fast":
		return "balanced"
	case "medium", "slow", "slower", "veryslow":
		return "quality"
	default:
		return "balanced"
	}
}

func mapPresetToQSV(preset string) string {
	switch preset {
	case "ultrafast", "superfast":
		return "veryfast"
	case "veryfast":
		return "faster"
	case "faster":
		return "fast"
	case "fast":
		return "medium"
	case "medium":
		return "slow"
	case "slow", "slower", "veryslow":
		return "veryslow"
	default:
		return "medium"
	}
}

func mapPresetTox264(preset string) string {
	validPresets := map[string]bool{
		"ultrafast": true, "superfast": true, "veryfast": true,
		"faster": true, "fast": true, "medium": true,
		"slow": true, "slower": true, "veryslow": true,
	}

	if validPresets[preset] {
		return preset
	}
	return "medium" // Default
}

func mapPresetTox265(preset string) string {
	validPresets := map[string]bool{
		"ultrafast": true, "superfast": true, "veryfast": true,
		"faster": true, "fast": true, "medium": true,
		"slow": true, "slower": true, "veryslow": true,
	}

	if validPresets[preset] {
		return preset
	}
	return "medium" // Default
}

func mapPresetToSVTAV1(preset string) string {
	// SVT-AV1 presets range from 0 (highest quality) to 12 (fastest)
	switch preset {
	case "veryslow":
		return "2"
	case "slower":
		return "4"
	case "slow":
		return "5"
	case "medium":
		return "6"
	case "fast":
		return "7"
	case "faster":
		return "8"
	case "veryfast":
		return "9"
	case "superfast":
		return "10"
	case "ultrafast":
		return "12"
	default:
		return "6" // Medium
	}
}

// getScaleFilter returns the FFmpeg scale filter for common resolutions
func getScaleFilter(resolution string, useGPU bool, gpuType GPUType) string {
	var baseScale string

	switch resolution {
	case "1080p":
		baseScale = "scale=-2:1080"
	case "720p":
		baseScale = "scale=-2:720"
	case "480p":
		baseScale = "scale=-2:480"
	case "4k", "2160p":
		baseScale = "scale=-2:2160"
	default:
		return ""
	}

	// Use GPU-accelerated scaling if available
	if useGPU {
		switch gpuType {
		case GPUTypeNVIDIA:
			return baseScale + ":force_original_aspect_ratio=decrease"
		case GPUTypeAMD:
			return baseScale + ":force_original_aspect_ratio=decrease"
		case GPUTypeApple:
			return baseScale + ":force_original_aspect_ratio=decrease"
		case GPUTypeIntel:
			return baseScale + ":force_original_aspect_ratio=decrease"
		}
	}

	return baseScale
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
