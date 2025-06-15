//go:build linux

package worker

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func getCPUInfoLinux() CPUInfo {
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

	manufacturer := vendorID
	switch vendorID {
	case "GenuineIntel":
		manufacturer = "Intel"
	case "AuthenticAMD":
		manufacturer = "AMD"
	}

	if modelName == "" {
		modelName = "Unknown CPU"
	}
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

	fields := strings.Fields(lines[0])
	if len(fields) < 8 || fields[0] != "cpu" {
		return 0
	}

	var values []uint64
	for i := 1; i < len(fields) && i <= 7; i++ {
		if val, err := strconv.ParseUint(fields[i], 10, 64); err == nil {
			values = append(values, val)
		}
	}

	if len(values) < 4 {
		return 0
	}

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

	// For simplicity, just return a static calculation
	// In a real implementation, you'd track this over time
	if totalTime == 0 {
		return 0
	}

	cpuUsage := float64(totalTime-idleTime) / float64(totalTime) * 100
	return cpuUsage
}

func detectNVIDIAGPUs() ([]GPUInfo, error) {
	cmd := exec.Command("nvidia-smi", "--query-gpu=index,name,memory.total", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi not available: %w", err)
	}

	var gpus []GPUInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	for _, line := range lines {
		parts := strings.Split(line, ", ")
		if len(parts) >= 3 {
			memoryMB := strings.TrimSpace(parts[2])
			var memoryDisplay string

			if memMB, err := strconv.ParseFloat(memoryMB, 64); err == nil {
				memoryGB := memMB / 1024.0
				memoryDisplay = fmt.Sprintf("%.0fGB", memoryGB)
			} else {
				memoryDisplay = memoryMB + " MB"
			}

			gpu := GPUInfo{
				ID:     strings.TrimSpace(parts[0]),
				Name:   strings.TrimSpace(parts[1]),
				Memory: memoryDisplay,
				Type:   GPUTypeNVIDIA,
				EncodingSupport: map[string]bool{
					"h264": true,
					"h265": true,
					"av1": strings.Contains(strings.ToLower(parts[1]), "rtx 40") ||
						strings.Contains(strings.ToLower(parts[1]), "rtx 41") ||
						strings.Contains(strings.ToLower(parts[1]), "rtx 42"),
				},
			}
			gpus = append(gpus, gpu)
		}
	}

	return gpus, nil
}

func detectAMDGPUs() ([]GPUInfo, error) {
	var gpus []GPUInfo

	// Try ROCm-SMI first
	if rocmGPUs, err := detectAMDROCm(); err == nil {
		gpus = append(gpus, rocmGPUs...)
	}

	// Try OpenCL detection if ROCm failed
	if len(gpus) == 0 {
		if openclGPUs, err := detectAMDOpenCL(); err == nil {
			gpus = append(gpus, openclGPUs...)
		}
	}

	return gpus, nil
}

func detectAMDROCm() ([]GPUInfo, error) {
	cmd := exec.Command("rocm-smi", "--showid", "--showproductname", "--showmeminfo", "vram")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("rocm-smi not available: %w", err)
	}

	var gpus []GPUInfo
	lines := strings.Split(string(output), "\n")

	var currentGPU GPUInfo
	gpuIndex := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "GPU[") {
			if currentGPU.ID != "" {
				gpus = append(gpus, currentGPU)
			}
			currentGPU = GPUInfo{
				ID:   strconv.Itoa(gpuIndex),
				Type: GPUTypeAMD,
				EncodingSupport: map[string]bool{
					"h264": true,
					"h265": true,
					"av1":  false, // Most AMD GPUs don't support AV1 encoding yet
				},
			}
			gpuIndex++
		} else if strings.Contains(line, "Card series:") || strings.Contains(line, "Card model:") {
			parts := strings.Split(line, ":")
			if len(parts) > 1 {
				currentGPU.Name = strings.TrimSpace(parts[1])
			}
		} else if strings.Contains(line, "VRAM Total") {
			parts := strings.Fields(line)
			for i, part := range parts {
				if strings.Contains(part, "MB") || strings.Contains(part, "GB") {
					currentGPU.Memory = part
					break
				} else if i > 0 && (strings.Contains(parts[i-1], "Total") || strings.Contains(parts[i-1], "Memory")) {
					if memMB, err := strconv.ParseFloat(part, 64); err == nil {
						memoryGB := memMB / 1024.0
						currentGPU.Memory = fmt.Sprintf("%.0fGB", memoryGB)
					}
					break
				}
			}
		}
	}

	if currentGPU.ID != "" {
		gpus = append(gpus, currentGPU)
	}

	return gpus, nil
}

func detectAMDOpenCL() ([]GPUInfo, error) {
	cmd := exec.Command("clinfo")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("clinfo not available: %w", err)
	}

	var gpus []GPUInfo
	lines := strings.Split(string(output), "\n")

	var currentGPU GPUInfo
	gpuIndex := 0
	inAMDDevice := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "Device Name") && (strings.Contains(strings.ToLower(line), "amd") ||
			strings.Contains(strings.ToLower(line), "radeon") || strings.Contains(strings.ToLower(line), "rx")) {
			if currentGPU.ID != "" && inAMDDevice {
				gpus = append(gpus, currentGPU)
			}

			parts := strings.Split(line, ":")
			if len(parts) > 1 {
				currentGPU = GPUInfo{
					ID:   strconv.Itoa(gpuIndex),
					Name: strings.TrimSpace(parts[1]),
					Type: GPUTypeAMD,
					EncodingSupport: map[string]bool{
						"h264": true,
						"h265": true,
						"av1":  false,
					},
				}
				gpuIndex++
				inAMDDevice = true
			}
		} else if inAMDDevice && strings.Contains(line, "Global memory size") {
			parts := strings.Fields(line)
			for i, part := range parts {
				if strings.Contains(part, "(") && i > 0 {
					// Extract memory size
					memStr := strings.Trim(parts[i-1], "()")
					if memMB, err := strconv.ParseFloat(memStr, 64); err == nil {
						if memMB > 1024*1024*1024 { // If it's in bytes
							memoryGB := memMB / (1024 * 1024 * 1024)
							currentGPU.Memory = fmt.Sprintf("%.0fGB", memoryGB)
						}
					}
					break
				}
			}
		}
	}

	if currentGPU.ID != "" && inAMDDevice {
		gpus = append(gpus, currentGPU)
	}

	return gpus, nil
}

func detectIntelGPUs() ([]GPUInfo, error) {
	// Try to detect Intel GPU on Linux
	cmd := exec.Command("lspci", "-v")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lspci not available: %w", err)
	}

	var gpus []GPUInfo
	lines := strings.Split(string(output), "\n")
	gpuIndex := 0

	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "vga") &&
			strings.Contains(strings.ToLower(line), "intel") {

			gpu := GPUInfo{
				ID:     strconv.Itoa(gpuIndex),
				Name:   extractIntelGPUName(line),
				Type:   GPUTypeIntel,
				Memory: "Shared",
				EncodingSupport: map[string]bool{
					"h264": true,
					"h265": checkIntelHEVCSupport(line),
					"av1":  checkIntelAV1Support(line),
				},
			}
			gpus = append(gpus, gpu)
			gpuIndex++
		}
	}

	return gpus, nil
}

func extractIntelGPUName(line string) string {
	// Extract GPU name from lspci output
	if idx := strings.Index(line, ": "); idx != -1 {
		return strings.TrimSpace(line[idx+2:])
	}
	return "Intel GPU"
}

func checkIntelHEVCSupport(gpuInfo string) bool {
	// Intel Gen 8 (Broadwell) and newer support HEVC
	lower := strings.ToLower(gpuInfo)
	return strings.Contains(lower, "iris") ||
		strings.Contains(lower, "uhd") ||
		strings.Contains(lower, "xe")
}

func checkIntelAV1Support(gpuInfo string) bool {
	// Intel Arc and newer support AV1
	lower := strings.ToLower(gpuInfo)
	return strings.Contains(lower, "arc") || strings.Contains(lower, "xe")
}

func detectAppleGPUs() ([]GPUInfo, error) {
	return nil, fmt.Errorf("not on Apple platform")
}

// Stub implementations for other platform CPU functions
func getCPUInfoWindows() CPUInfo {
	return CPUInfo{
		Manufacturer: "N/A",
		Model:        "N/A",
		Cores:        runtime.NumCPU(),
		Threads:      runtime.NumCPU(),
	}
}

func getCPUInfoDarwin() CPUInfo {
	return CPUInfo{
		Manufacturer: "N/A",
		Model:        "N/A",
		Cores:        runtime.NumCPU(),
		Threads:      runtime.NumCPU(),
	}
}
