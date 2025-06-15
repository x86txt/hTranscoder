//go:build darwin

package worker

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// getCPUInfoDarwin gets CPU information on macOS
func getCPUInfoDarwin() CPUInfo {
	// Get CPU brand string
	brandCmd := exec.Command("sysctl", "-n", "machdep.cpu.brand_string")
	brandOutput, err := brandCmd.Output()
	var model string
	if err == nil {
		model = strings.TrimSpace(string(brandOutput))
	} else {
		model = "Unknown CPU"
	}

	// Get core count
	coreCmd := exec.Command("sysctl", "-n", "hw.physicalcpu")
	coreOutput, err := coreCmd.Output()
	var cores int
	if err == nil {
		if c, err := strconv.Atoi(strings.TrimSpace(string(coreOutput))); err == nil {
			cores = c
		} else {
			cores = runtime.NumCPU()
		}
	} else {
		cores = runtime.NumCPU()
	}

	// Get thread count
	threadCmd := exec.Command("sysctl", "-n", "hw.logicalcpu")
	threadOutput, err := threadCmd.Output()
	var threads int
	if err == nil {
		if t, err := strconv.Atoi(strings.TrimSpace(string(threadOutput))); err == nil {
			threads = t
		} else {
			threads = runtime.NumCPU()
		}
	} else {
		threads = runtime.NumCPU()
	}

	// Determine manufacturer from model name
	manufacturer := "Unknown"
	if strings.Contains(strings.ToLower(model), "intel") {
		manufacturer = "Intel"
	} else if strings.Contains(strings.ToLower(model), "amd") {
		manufacturer = "AMD"
	} else if strings.Contains(strings.ToLower(model), "apple") {
		manufacturer = "Apple"
	}

	return CPUInfo{
		Manufacturer: manufacturer,
		Model:        model,
		Cores:        cores,
		Threads:      threads,
	}
}

// detectAppleGPUs detects Apple Silicon GPUs on macOS
func detectAppleGPUs() ([]GPUInfo, error) {
	// Use system_profiler to get GPU information
	cmd := exec.Command("system_profiler", "SPDisplaysDataType")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("system_profiler not available: %w", err)
	}

	var gpus []GPUInfo
	lines := strings.Split(string(output), "\n")

	var currentGPU GPUInfo
	gpuIndex := 0
	inGPUSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Look for GPU chipset information
		if strings.Contains(trimmed, "Chipset Model:") {
			if currentGPU.Name != "" {
				gpus = append(gpus, currentGPU)
			}

			parts := strings.Split(trimmed, ":")
			if len(parts) > 1 {
				chipset := strings.TrimSpace(parts[1])
				isAppleGPU := strings.Contains(strings.ToLower(chipset), "apple") ||
					strings.Contains(strings.ToLower(chipset), "m1") ||
					strings.Contains(strings.ToLower(chipset), "m2") ||
					strings.Contains(strings.ToLower(chipset), "m3") ||
					strings.Contains(strings.ToLower(chipset), "m4")

				if isAppleGPU {
					currentGPU = GPUInfo{
						ID:   strconv.Itoa(gpuIndex),
						Name: chipset,
						Type: GPUTypeApple,
						EncodingSupport: map[string]bool{
							"h264": true,
							"h265": true,
							"av1":  strings.Contains(strings.ToLower(chipset), "m3") || strings.Contains(strings.ToLower(chipset), "m4"),
						},
					}
					gpuIndex++
					inGPUSection = true
				} else {
					inGPUSection = false
				}
			}
		} else if inGPUSection && (strings.Contains(trimmed, "VRAM") || strings.Contains(trimmed, "Memory")) {
			parts := strings.Split(trimmed, ":")
			if len(parts) > 1 {
				memStr := strings.TrimSpace(parts[1])
				currentGPU.Memory = memStr
			}
		}
	}

	if currentGPU.Name != "" {
		gpus = append(gpus, currentGPU)
	}

	// If no Apple GPUs detected through system_profiler, try to detect based on architecture
	if len(gpus) == 0 && runtime.GOARCH == "arm64" {
		// Likely running on Apple Silicon
		gpus = append(gpus, GPUInfo{
			ID:     "0",
			Name:   "Apple Silicon GPU",
			Type:   GPUTypeApple,
			Memory: "Unified Memory",
			EncodingSupport: map[string]bool{
				"h264": true,
				"h265": true,
				"av1":  false, // Conservative default
			},
		})
	}

	return gpus, nil
}

// Stub implementations for other GPU types (not available on macOS typically)
func detectNVIDIAGPUs() ([]GPUInfo, error) {
	return nil, fmt.Errorf("NVIDIA GPUs not typically available on macOS")
}

func detectAMDGPUs() ([]GPUInfo, error) {
	return nil, fmt.Errorf("AMD GPUs not typically available on modern macOS")
}

func detectIntelGPUs() ([]GPUInfo, error) {
	return nil, fmt.Errorf("Intel GPUs not typically available on Apple Silicon macOS")
}

// Stub implementations for other platform CPU functions
func getCPUInfoLinux() CPUInfo {
	return CPUInfo{
		Manufacturer: "N/A",
		Model:        "N/A",
		Cores:        runtime.NumCPU(),
		Threads:      runtime.NumCPU(),
	}
}

func getCPUInfoWindows() CPUInfo {
	return CPUInfo{
		Manufacturer: "N/A",
		Model:        "N/A",
		Cores:        runtime.NumCPU(),
		Threads:      runtime.NumCPU(),
	}
}

func getCPUUsageLinux() float64 {
	return 0.0
}

// Stub implementations for Linux platform functions
func detectAMDROCm() ([]GPUInfo, error) {
	return nil, fmt.Errorf("ROCm not available on macOS")
}

func detectAMDOpenCL() ([]GPUInfo, error) {
	return nil, fmt.Errorf("AMD OpenCL not available on macOS")
}

func extractIntelGPUName(line string) string {
	return "Intel GPU"
}

func checkIntelHEVCSupport(gpuInfo string) bool {
	return false
}

func checkIntelAV1Support(gpuInfo string) bool {
	return false
}
