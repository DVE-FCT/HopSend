package benchmark

import (
	"context"
	"runtime"
	"time"
)

func measure(ctx context.Context, s Sample, action func(*Sample) error) (Sample, error) {
	startCPU, cpuOK := processCPU()
	start := time.Now()
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			s.PeakHeap = max(s.PeakHeap, mem.HeapAlloc)
			s.PeakWorkingSet = max(s.PeakWorkingSet, workingSet())
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	var err error
	if err = ctx.Err(); err == nil {
		err = action(&s)
	}
	elapsed := time.Since(start).Seconds()
	close(done)
	<-sampled
	endCPU, endOK := processCPU()
	s.Seconds = elapsed
	s.CPUAvailable = cpuOK && endOK
	s.CPUSeconds = max(0, endCPU-startCPU)
	if elapsed > 0 {
		s.MiBPerSecond = float64(s.Bytes) / (1 << 20) / elapsed
	}
	return s, err
}
