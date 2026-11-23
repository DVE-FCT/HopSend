//go:build !windows

package benchmark

func processCPU() (float64, bool) { return 0, false }
func workingSet() uint64          { return 0 }
