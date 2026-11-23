package benchmark

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

func processCPU() (float64, bool) {
	var creation, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user) != nil {
		return 0, false
	}
	ticks := func(t windows.Filetime) uint64 { return uint64(t.HighDateTime)<<32 | uint64(t.LowDateTime) }
	return float64(ticks(kernel)+ticks(user)) / 1e7, true
}
func workingSet() uint64 {
	// PROCESS_MEMORY_COUNTERS, pointer-sized fields preserve the Windows ABI.
	var counters struct {
		Size, PageFaultCount                                                                                                                                                   uint32
		PeakWorkingSetSize, WorkingSetSize, QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage, QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage, PagefileUsage, PeakPagefileUsage uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	result, _, _ := windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	if result == 0 {
		return 0
	}
	return uint64(counters.WorkingSetSize)
}
