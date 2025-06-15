//go:build windows

package worker

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// getCPUInfoWindows gets CPU information on Windows using multiple fallbacks
func getCPUInfoWindows() CPUInfo {
	// First, try WMIC (the original method)
	if info, ok := getCPUInfoWindowsWmic(); ok {
		log.Println("Windows CPU detection: Success with WMIC")
		return info
	}

	// If WMIC fails, try PowerShell/CIM (more modern)
	if info, ok := getCPUInfoWindowsPowerShell(); ok {
		log.Println("Windows CPU detection: Success with PowerShell/CIM")
		return info
	}

	// If both fail, fall back to registry reading (most reliable)
	if info, ok := getCPUInfoWindowsRegistry(); ok {
		log.Println("Windows CPU detection: Success with Registry")
		return info
	}

	// If all methods fail, return basic info from runtime
	log.Println("Windows CPU detection: All methods failed, using runtime fallback")
	return CPUInfo{
		Manufacturer: "Unknown",
		Model:        "Unknown",
		Cores:        runtime.NumCPU(),
		Threads:      runtime.NumCPU(),
	}
}

// getCPUInfoWindowsWmic gets CPU info using WMIC
func getCPUInfoWindowsWmic() (CPUInfo, bool) {
	cmd := exec.Command("wmic", "cpu", "get", "Name,Manufacturer,NumberOfCores,NumberOfLogicalProcessors", "/format:csv")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("WMIC command failed: %v", err)
		return CPUInfo{}, false
	}

	outputStr := strings.ReplaceAll(string(output), "\r\n", "\n")
	lines := strings.Split(outputStr, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "Manufacturer,Name") {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) >= 5 {
			manufacturer := strings.TrimSpace(parts[1])
			model := strings.TrimSpace(parts[2])
			coresStr := strings.TrimSpace(parts[3])
			threadsStr := strings.TrimSpace(parts[4])

			cores, _ := strconv.Atoi(coresStr)
			threads, _ := strconv.Atoi(threadsStr)

			if model != "" {
				return CPUInfo{
					Manufacturer: manufacturer,
					Model:        model,
					Cores:        cores,
					Threads:      threads,
				}, true
			}
		}
	}

	return CPUInfo{}, false
}

// getCPUInfoWindowsPowerShell gets CPU info using PowerShell
func getCPUInfoWindowsPowerShell() (CPUInfo, bool) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Get-CimInstance -ClassName Win32_Processor | Select-Object -Property Name,Manufacturer,NumberOfCores,NumberOfLogicalProcessors | Format-List")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("PowerShell command failed: %v", err)
		return CPUInfo{}, false
	}

	info := CPUInfo{}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "Name":
				info.Model = value
			case "Manufacturer":
				info.Manufacturer = value
			case "NumberOfCores":
				info.Cores, _ = strconv.Atoi(value)
			case "NumberOfLogicalProcessors":
				info.Threads, _ = strconv.Atoi(value)
			}
		}
	}

	if info.Model != "" {
		return info, true
	}

	return CPUInfo{}, false
}

// getCPUInfoWindowsRegistry gets CPU info from the Windows Registry
func getCPUInfoWindowsRegistry() (CPUInfo, bool) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		log.Printf("Failed to open registry key: %v", err)
		return CPUInfo{}, false
	}
	defer key.Close()

	model, _, err := key.GetStringValue("ProcessorNameString")
	if err != nil {
		log.Printf("Failed to read registry value: %v", err)
		return CPUInfo{}, false
	}

	// For registry, we can only reliably get the model.
	// We fall back to runtime for core/thread counts.
	return CPUInfo{
		Manufacturer: "Unknown", // Manufacturer not typically stored here
		Model:        strings.TrimSpace(model),
		Cores:        runtime.NumCPU(),
		Threads:      runtime.NumCPU(),
	}, true
}

// Stub implementations for other platforms
func getCPUInfoLinux() CPUInfo {
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

func getCPUUsageLinux() float64 {
	return 0.0
}

func detectNVIDIAGPUs() ([]GPUInfo, error) {
	return nil, fmt.Errorf("NVIDIA detection not implemented on Windows")
}

func detectAMDGPUs() ([]GPUInfo, error) {
	return nil, fmt.Errorf("AMD detection not implemented on Windows")
}

func detectAppleGPUs() ([]GPUInfo, error) {
	return nil, fmt.Errorf("not on Apple platform")
}

func detectIntelGPUs() ([]GPUInfo, error) {
	return nil, fmt.Errorf("Intel detection not implemented on Windows")
}
