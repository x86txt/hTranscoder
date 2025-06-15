//go:build !windows

package worker

import "runtime"

// getCPUInfoWindows is a stub for non-Windows platforms to allow compilation.
// It should never be called.
func getCPUInfoWindows() CPUInfo {
	return CPUInfo{
		Manufacturer: "N/A",
		Model:        "N/A",
		Cores:        runtime.NumCPU(),
		Threads:      runtime.NumCPU(),
	}
}
