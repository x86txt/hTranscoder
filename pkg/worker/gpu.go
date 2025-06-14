package worker

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GPUInfo represents information about a detected GPU
type GPUInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Memory    string `json:"memory"`
	IsPrimary bool   `json:"isPrimary"`
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

// DetectGPUs detects available NVIDIA GPUs using nvidia-smi
func DetectGPUs() ([]GPUInfo, error) {
	// Check if nvidia-smi is available
	cmd := exec.Command("nvidia-smi", "--query-gpu=index,name,memory.total", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		// No NVIDIA GPUs found or nvidia-smi not available
		return []GPUInfo{}, nil
	}

	var gpus []GPUInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	for i, line := range lines {
		parts := strings.Split(line, ", ")
		if len(parts) >= 3 {
			// Parse memory value and convert MB to GB
			memoryMB := strings.TrimSpace(parts[2])
			var memoryDisplay string

			if memMB, err := strconv.ParseFloat(memoryMB, 64); err == nil {
				memoryGB := memMB / 1024.0
				memoryDisplay = fmt.Sprintf("%.0fGB", memoryGB)
			} else {
				// Fallback if parsing fails
				memoryDisplay = memoryMB + " MB"
			}

			gpu := GPUInfo{
				ID:        parts[0],
				Name:      strings.TrimSpace(parts[1]),
				Memory:    memoryDisplay,
				IsPrimary: i == 0, // First GPU is considered primary
			}
			gpus = append(gpus, gpu)
		}
	}

	return gpus, nil
}

// GetBestGPU returns the best available GPU for encoding
// Currently returns the primary GPU (index 0) if available
func GetBestGPU() (GPUInfo, bool) {
	gpus, err := DetectGPUs()
	if err != nil {
		log.Printf("Error detecting GPUs: %v", err)
		return GPUInfo{}, false
	}

	if len(gpus) == 0 {
		return GPUInfo{}, false
	}

	// Return the primary GPU (first one)
	for _, gpu := range gpus {
		if gpu.IsPrimary {
			return gpu, true
		}
	}

	// Fallback to first GPU if no primary found
	return gpus[0], true
}

// IsGPUAvailable checks if any GPU is available for encoding
func IsGPUAvailable() bool {
	gpus, err := DetectGPUs()
	if err != nil {
		return false
	}
	return len(gpus) > 0
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

	// Multiple GPUs
	primaryGPU := gpus[0]
	return fmt.Sprintf("%s (%s) +%d more", primaryGPU.Name, primaryGPU.Memory, len(gpus)-1)
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
	// Check if nvidia-smi is available
	cmd := exec.Command("nvidia-smi", "--query-gpu=utilization.gpu,utilization.memory", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0 // No GPU or nvidia-smi not available
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return 0, 0
	}

	// Parse first GPU stats
	parts := strings.Split(lines[0], ", ")
	if len(parts) >= 2 {
		if util, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
			utilization = util
		}
		if mem, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
			memory = mem
		}
	}

	return utilization, memory
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
	// Fallback for other platforms - return 0 for now
	return 0
}

// getCPUUsageLinux gets CPU usage on Linux by reading /proc/stat
func getCPUUsageLinux() float64 {
	cmd := exec.Command("cat", "/proc/stat")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) == 0 {
		return 0
	}

	// Parse first line (overall CPU stats)
	fields := strings.Fields(lines[0])
	if len(fields) < 8 || fields[0] != "cpu" {
		return 0
	}

	// Parse CPU time values
	var values []uint64
	for i := 1; i < len(fields) && i <= 7; i++ {
		if val, err := strconv.ParseUint(fields[i], 10, 64); err == nil {
			values = append(values, val)
		}
	}

	if len(values) < 4 {
		return 0
	}

	// Calculate total and idle time
	user, nice, system, idle := values[0], values[1], values[2], values[3]
	iowait, irq, softirq := uint64(0), uint64(0), uint64(0)
	if len(values) > 4 {
		iowait = values[4]
	}
	if len(values) > 5 {
		irq = values[5]
	}
	if len(values) > 6 {
		softirq = values[6]
	}

	totalTime := user + nice + system + idle + iowait + irq + softirq
	idleTime := idle + iowait

	now := time.Now()

	// If this is the first measurement, initialize and return 0
	if lastMeasureTime.IsZero() {
		lastCPUTotal = totalTime
		lastCPUIdle = idleTime
		lastMeasureTime = now
		return 0
	}

	// Calculate differences
	totalDiff := totalTime - lastCPUTotal
	idleDiff := idleTime - lastCPUIdle

	// Update last values
	lastCPUTotal = totalTime
	lastCPUIdle = idleTime
	lastMeasureTime = now

	// Calculate CPU usage percentage
	if totalDiff == 0 {
		return 0
	}

	cpuUsage := float64(totalDiff-idleDiff) / float64(totalDiff) * 100
	return cpuUsage
}

// GetUsageStats returns comprehensive system usage statistics
func GetUsageStats() UsageStats {
	gpuUtil, gpuMem := GetGPUUsage()
	cpuUsage := GetCPUUsage()

	return UsageStats{
		GPUUsage:     gpuUtil,
		GPUMemory:    gpuMem,
		CPUUsage:     cpuUsage,
		NetworkSpeed: 0, // TODO: Implement network monitoring if needed
	}
}

// GetCPUInfo returns detailed CPU information
func GetCPUInfo() CPUInfo {
	if runtime.GOOS != "linux" {
		return CPUInfo{
			Manufacturer: "Unknown",
			Model:        "Unknown",
			Cores:        runtime.NumCPU(),
			Threads:      runtime.NumCPU(),
		}
	}

	cmd := exec.Command("cat", "/proc/cpuinfo")
	output, err := cmd.Output()
	if err != nil {
		return CPUInfo{
			Manufacturer: "Unknown",
			Model:        "Unknown",
			Cores:        runtime.NumCPU(),
			Threads:      runtime.NumCPU(),
		}
	}

	lines := strings.Split(string(output), "\n")
	var modelName, vendorID string
	coreCount := 0
	threadCount := runtime.NumCPU()

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "model name") {
			parts := strings.Split(line, ":")
			if len(parts) > 1 {
				modelName = strings.TrimSpace(parts[1])
			}
		} else if strings.HasPrefix(line, "vendor_id") {
			parts := strings.Split(line, ":")
			if len(parts) > 1 {
				vendorID = strings.TrimSpace(parts[1])
			}
		} else if strings.HasPrefix(line, "processor") {
			coreCount++
		}
	}

	// Map vendor IDs to readable names
	manufacturer := vendorID
	switch vendorID {
	case "GenuineIntel":
		manufacturer = "Intel"
	case "AuthenticAMD":
		manufacturer = "AMD"
	case "CentaurHauls":
		manufacturer = "VIA"
	case "GenuineTMx86":
		manufacturer = "Transmeta"
	}

	// Clean up model name
	if modelName == "" {
		modelName = "Unknown CPU"
	}

	// Use detected core count, fallback to runtime.NumCPU()
	if coreCount == 0 {
		coreCount = threadCount
	}

	return CPUInfo{
		Manufacturer: manufacturer,
		Model:        modelName,
		Cores:        coreCount,
		Threads:      threadCount,
	}
}

// GetCPUStatus returns a string describing CPU information
func GetCPUStatus() string {
	cpuInfo := GetCPUInfo()

	// Check if model name already contains the manufacturer to avoid duplication
	modelLower := strings.ToLower(cpuInfo.Model)
	manufacturerLower := strings.ToLower(cpuInfo.Manufacturer)

	if strings.Contains(modelLower, manufacturerLower) {
		// Model already contains manufacturer, just use model and core count
		return fmt.Sprintf("%s (%d cores)", cpuInfo.Model, cpuInfo.Cores)
	} else {
		// Model doesn't contain manufacturer, prepend it
		return fmt.Sprintf("%s %s (%d cores)", cpuInfo.Manufacturer, cpuInfo.Model, cpuInfo.Cores)
	}
}
