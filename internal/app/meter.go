package app

import (
	"sync"
	"time"
)

type byteSample struct {
	at    time.Time
	bytes int64
}

// A meter is scoped to one Start/Resume execution. Rechecked bytes never enter
// its payload counter or the per-attempt ETA baseline.
type transferMeter struct {
	mu            sync.Mutex
	now           func() time.Time
	start         time.Time
	bytes         int64
	samples       []byteSample
	state         string
	baseline      int64
	transferStart time.Time
}

func newTransferMeter() *transferMeter { return &transferMeter{now: time.Now, start: time.Now()} }

func (m *transferMeter) add(n int64) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.bytes += n
	// Bound memory even when many workers report at high throughput.
	bucket := now.Truncate(250 * time.Millisecond)
	if len(m.samples) > 0 && m.samples[len(m.samples)-1].at.Equal(bucket) {
		m.samples[len(m.samples)-1].bytes += n
	} else {
		m.samples = append(m.samples, byteSample{bucket, n})
	}
	m.trim(now)
}

func (m *transferMeter) trim(now time.Time) {
	i := 0
	for i < len(m.samples) && m.samples[i].at.Before(now.Add(-5*time.Second)) {
		i++
	}
	m.samples = m.samples[i:]
}

func (m *transferMeter) progress(p TaskStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if (p.State == "downloading" || p.State == "uploading") && m.state != p.State {
		m.baseline = p.VerifiedBytes
		m.transferStart = m.now()
	}
	if p.State != m.state {
		m.samples = nil
		m.start = m.now()
	}
	m.state = p.State
}

func (m *transferMeter) decorate(s TaskStatus) TaskStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.MetricsAvailable = true
	s.TransferredBytes = m.bytes
	s.SpeedBytesPerSecond = 0
	s.ETASeconds = 0
	if s.State != "downloading" && s.State != "uploading" {
		return s
	}
	now := m.now()
	m.trim(now)
	var recent int64
	for _, sample := range m.samples {
		recent += sample.bytes
	}
	window := min(5.0, now.Sub(m.start).Seconds())
	if window > 0 {
		s.SpeedBytesPerSecond = float64(recent) / window
	}
	elapsed := now.Sub(m.transferStart).Seconds()
	effective := s.VerifiedBytes - m.baseline
	if elapsed > 0 && effective > 0 && s.TotalBytes > s.VerifiedBytes && recent > 0 {
		s.ETASeconds = float64(s.TotalBytes-s.VerifiedBytes) / (float64(effective) / elapsed)
	}
	return s
}
