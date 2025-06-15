package worker

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

// GPUType represents different GPU manufacturers
type GPUType string

const (
	GPUTypeNVIDIA  GPUType = "nvidia"
	GPUTypeAMD     GPUType = "amd"
	GPUTypeApple   GPUType = "apple"
	GPUTypeIntel   GPUType = "intel"
	GPUTypeUnknown GPUType = "unknown"
)

// GPUInfo represents information about a detected GPU
type GPUInfo struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Memory          string          `json:"memory"`
	Type            GPUType         `json:"type"`
	IsPrimary       bool            `json:"isPrimary"`
	EncodingSupport map[string]bool `json:"encodingSupport"` // codec -> supported
}

// CPUInfo represents information about the CPU
type CPUInfo struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Cores        int    `json:"cores"`
	Threads      int    `json:"threads"`
}

// UsageStats represents current system usage statistics
type UsageStats struct {
	GPUUsage     float64 `json:"gpuUsage"`     // GPU utilization percentage
	GPUMemory    float64 `json:"gpuMemory"`    // GPU memory usage percentage
	CPUUsage     float64 `json:"cpuUsage"`     // CPU usage percentage
	NetworkSpeed float64 `json:"networkSpeed"` // Network usage in MB/s
}

// DetectGPUs detects all available GPUs across different manufacturers
func DetectGPUs() ([]GPUInfo, error) {
	var allGPUs []GPUInfo

	if nvidiaGPUs, err := detectNVIDIAGPUs(); err == nil {
		allGPUs = append(allGPUs, nvidiaGPUs...)
	}
	if amdGPUs, err := detectAMDGPUs(); err == nil {
		allGPUs = append(allGPUs, amdGPUs...)
	}
	if appleGPUs, err := detectAppleGPUs(); err == nil {
		allGPUs = append(allGPUs, appleGPUs...)
	}
	if intelGPUs, err := detectIntelGPUs(); err == nil {
		allGPUs = append(allGPUs, intelGPUs...)
	}

	if len(allGPUs) > 0 {
		allGPUs[0].IsPrimary = true
	}

	return allGPUs, nil
}

// GetBestGPU returns the best available GPU for encoding
func GetBestGPU() (GPUInfo, bool) {
	gpus, err := DetectGPUs()
	if err != nil || len(gpus) == 0 {
		return GPUInfo{}, false
	}
	// Simple logic: prefer discrete GPUs over integrated
	for _, gpu := range gpus {
		if gpu.Type == GPUTypeNVIDIA {
			return gpu, true
		}
	}
	for _, gpu := range gpus {
		if gpu.Type == GPUTypeAMD {
			return gpu, true
		}
	}
	for _, gpu := range gpus {
		if gpu.Type == GPUTypeApple {
			return gpu, true
		}
	}
	// Return first available GPU (likely Intel integrated)
	return gpus[0], true
}

// IsGPUAvailable checks if any GPU is available for encoding
func IsGPUAvailable() bool {
	gpus, err := DetectGPUs()
	return err == nil && len(gpus) > 0
}

// GetGPUStatus returns a string describing GPU availability
func GetGPUStatus() string {
	gpus, err := DetectGPUs()
	if err != nil {
		return "Error detecting GPU"
	}
	if len(gpus) == 0 {
		return "None"
	}
	if len(gpus) == 1 {
		gpu := gpus[0]
		return fmt.Sprintf("%s (%s)", gpu.Name, gpu.Memory)
	}
	// Multiple GPUs - show primary and count
	primaryGPU := gpus[0]
	return fmt.Sprintf("%s (%s) +%d more", primaryGPU.Name, primaryGPU.Memory, len(gpus)-1)
}

// GetCPUStatus returns a string describing CPU information
func GetCPUStatus() string {
	cpuInfo := GetCPUInfo()
	modelLower := strings.ToLower(cpuInfo.Model)
	manufacturerLower := strings.ToLower(cpuInfo.Manufacturer)

	if strings.Contains(modelLower, manufacturerLower) {
		return fmt.Sprintf("%s (%d cores)", cpuInfo.Model, cpuInfo.Cores)
	}
	return fmt.Sprintf("%s %s (%d cores)", cpuInfo.Manufacturer, cpuInfo.Model, cpuInfo.Cores)
}

// GetCPUInfo returns detailed CPU information by dispatching to the correct OS-specific implementation.
func GetCPUInfo() CPUInfo {
	switch runtime.GOOS {
	case "linux":
		return getCPUInfoLinux()
	case "windows":
		return getCPUInfoWindows()
	case "darwin":
		return getCPUInfoDarwin()
	default:
		return CPUInfo{
			Manufacturer: "Unknown",
			Model:        "Unknown",
			Cores:        runtime.NumCPU(),
			Threads:      runtime.NumCPU(),
		}
	}
}

var (
	lastCPUTotal, lastCPUIdle uint64
	lastMeasureTime           time.Time
)

// GetCPUUsage returns current CPU usage percentage
func GetCPUUsage() float64 {
	if runtime.GOOS == "linux" {
		return getCPUUsageLinux()
	}
	// Fallback for other platforms
	return 0
}

// GetUsageStats returns comprehensive system usage statistics
func GetUsageStats() UsageStats {
	gpuUtil, gpuMem := 0.0, 0.0 // Placeholder
	cpuUsage := GetCPUUsage()

	return UsageStats{
		GPUUsage:     gpuUtil,
		GPUMemory:    gpuMem,
		CPUUsage:     cpuUsage,
		NetworkSpeed: 0, // Placeholder
	}
}

// ValidateGPUDevice checks if the specified GPU device exists
func ValidateGPUDevice(deviceID string) bool {
	gpus, err := DetectGPUs()
	if err != nil {
		return false
	}

	for _, gpu := range gpus {
		if gpu.ID == deviceID {
			return true
		}
	}
	return false
}

// GetGPUUsage returns current GPU utilization and memory usage percentages
func GetGPUUsage() (utilization float64, memory float64) {
	gpus, err := DetectGPUs()
	if err != nil || len(gpus) == 0 {
		return 0, 0
	}

	// Get usage for the primary GPU
	primaryGPU := gpus[0]

	switch primaryGPU.Type {
	case GPUTypeNVIDIA:
		return getNVIDIAGPUUsage()
	case GPUTypeAMD:
		return getAMDGPUUsage()
	case GPUTypeApple:
		return getAppleGPUUsage()
	case GPUTypeIntel:
		return getIntelGPUUsage()
	default:
		return 0, 0
	}
}

// GPU usage functions (placeholders for now)
func getNVIDIAGPUUsage() (float64, float64) {
	return 0, 0 // TODO: Implement actual NVIDIA GPU usage monitoring
}

func getAMDGPUUsage() (float64, float64) {
	return 0, 0 // TODO: Implement actual AMD GPU usage monitoring
}

func getAppleGPUUsage() (float64, float64) {
	return 0, 0 // TODO: Implement actual Apple GPU usage monitoring
}

func getIntelGPUUsage() (float64, float64) {
	return 0, 0 // TODO: Implement actual Intel GPU usage monitoring
}
