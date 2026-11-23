// Package benchmark measures bounded, sequential single-source transfers using
// the production engine. It never changes cache, network or machine settings.
package benchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/buildinfo"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

type Case struct {
	ManifestID string `json:"manifest_id"`
	Workers    int    `json:"workers"`
}
type Config struct {
	Label        string `json:"label"`
	Source       string `json:"local_source"`
	Repeats      int    `json:"repeats"`
	SyncChunkMiB int64  `json:"sync_chunk_mib"`
	Cases        []Case `json:"cases"`
	// TrafficBudgetBytes is a payload ceiling, not a transfer speed target.
	TrafficBudgetBytes int64 `json:"traffic_budget_bytes"`
}
type Sample struct {
	Kind           string             `json:"kind"`
	Iteration      int                `json:"iteration"`
	ChunkBytes     int64              `json:"chunk_bytes,omitempty"`
	Workers        int                `json:"workers,omitempty"`
	Cache          string             `json:"cache_condition"`
	Bytes          int64              `json:"bytes"`
	PayloadBytes   int64              `json:"sftp_payload_bytes"`
	Seconds        float64            `json:"seconds"`
	MiBPerSecond   float64            `json:"mib_per_second"`
	SyncSeconds    float64            `json:"sync_seconds,omitempty"`
	CPUSeconds     float64            `json:"process_cpu_seconds"`
	CPUAvailable   bool               `json:"process_cpu_available"`
	PeakWorkingSet uint64             `json:"sampled_peak_working_set_bytes"`
	PeakHeap       uint64             `json:"sampled_peak_go_heap_bytes"`
	SHA256         string             `json:"sha256,omitempty"`
	Verified       bool               `json:"verified"`
	Phases         map[string]float64 `json:"engine_phase_seconds,omitempty"`
}
type Summary struct {
	Kind       string  `json:"kind"`
	ChunkBytes int64   `json:"chunk_bytes,omitempty"`
	Workers    int     `json:"workers,omitempty"`
	Count      int     `json:"count"`
	Min        float64 `json:"min_mib_per_second"`
	Median     float64 `json:"median_mib_per_second"`
	Max        float64 `json:"max_mib_per_second"`
}
type PathObservation struct {
	ObservedAt string `json:"observed_at"`
	Path       string `json:"path"`
}
type Report struct {
	Schema           int               `json:"schema_version"`
	Version          string            `json:"application_version"`
	Go               string            `json:"go_version"`
	OS               string            `json:"os"`
	Arch             string            `json:"arch"`
	CPUs             int               `json:"logical_cpus"`
	Label            string            `json:"environment_label"`
	StartedAt        string            `json:"started_at"`
	FinishedAt       string            `json:"finished_at,omitempty"`
	Complete         bool              `json:"complete"`
	ContentID        string            `json:"content_sha256"`
	Size             int64             `json:"content_bytes"`
	CachePolicy      string            `json:"cache_policy"`
	ResourcePolicy   string            `json:"resource_policy"`
	EstimatedPayload int64             `json:"estimated_sftp_payload_bytes"`
	Paths            []PathObservation `json:"network_observations,omitempty"`
	Samples          []Sample          `json:"samples"`
	Summaries        []Summary         `json:"summaries"`
}

func Validate(c Config) error {
	if c.Source == "" || c.Repeats < 1 || c.Repeats > 10 || c.SyncChunkMiB < 1 || c.SyncChunkMiB > 64 || len(c.Cases) > 27 || c.TrafficBudgetBytes < 0 {
		return errors.New("基准参数无效：需要本机源文件、1–10 次重复、1–64 MiB 同步块及最多 27 组单源参数")
	}
	seen := map[Case]bool{}
	for _, v := range c.Cases {
		if !manifest.ValidID(v.ManifestID) || (v.Workers != 1 && v.Workers != 2 && v.Workers != 4) || seen[v] {
			return errors.New("基准矩阵需要不同的清单/并发组合，并发只允许 1/2/4")
		}
		seen[v] = true
	}
	return nil
}

// Run creates a dedicated, new output directory. Only files created within that
// directory are removed, after independent verification. Private task databases
// stay local; report.json contains no device addresses or credential paths.
func Run(ctx context.Context, c Config, device app.DownloadRequest, output string, progress func(Sample)) (report Report, err error) {
	if err = Validate(c); err != nil {
		return report, err
	}
	source, err := filepath.Abs(c.Source)
	if err != nil {
		return report, err
	}
	if err = filesystem.CheckComponents(source); err != nil {
		return report, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return report, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 4<<30 {
		return report, errors.New("基准源必须为 1 字节至 4 GiB 的常规合成文件")
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return report, err
	}
	if err = filesystem.CheckComponents(output); err != nil {
		return report, err
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return report, fmt.Errorf("基准输出目录必须尚不存在：%w", err)
	}
	report = Report{Schema: 1, Version: buildinfo.Version, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Label: c.Label, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Size: info.Size(), CachePolicy: "No OS cache flush. First local hash: cache unknown. Subsequent local reads warm. Network matrix: after one full verified SFTP warm-up; destinations are new files.", ResourcePolicy: "Client process CPU delta; working set and Go heap sampled every 50ms, not system-wide or remote CPU; short peaks can be missed.", Samples: []Sample{}, Summaries: []Summary{}}
	save := func() error {
		b, e := json.MarshalIndent(report, "", "  ")
		if e != nil {
			return e
		}
		return os.WriteFile(filepath.Join(output, "report.json"), append(b, '\n'), 0600)
	}
	defer func() {
		report.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		report.Summaries = summarize(report.Samples)
		err = errors.Join(err, save())
	}()
	record := func(s Sample, e error) error {
		s.Verified = e == nil
		report.Samples = append(report.Samples, s)
		if progress != nil {
			progress(s)
		}
		return errors.Join(e, save())
	}
	var expected string
	for i := 0; i < c.Repeats; i++ {
		s, e := measure(ctx, Sample{Kind: "local_sha256", Iteration: i + 1, Cache: "warm after first local pass"}, func(s *Sample) error {
			n, hash, e := hashFile(ctx, source)
			s.Bytes = n
			s.SHA256 = hash
			if e == nil && expected != "" && hash != expected {
				return errors.New("基准源在测量中变化")
			}
			return e
		})
		if i == 0 {
			s.Cache = "unknown; OS cache not flushed"
			expected = s.SHA256
			report.ContentID = expected
		}
		if e = record(s, e); e != nil {
			return report, e
		}
	}
	for i := 0; i < c.Repeats; i++ {
		target := filepath.Join(output, fmt.Sprintf("disk-%d.bin", i))
		s, e := measure(ctx, Sample{Kind: "local_write_sync", Iteration: i + 1, ChunkBytes: c.SyncChunkMiB << 20, Cache: "warm source; new destination"}, func(s *Sample) error {
			input, e := os.Open(source)
			if e != nil {
				return e
			}
			defer input.Close()
			f, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return e
			}
			buffer := make([]byte, 1<<20)
			for offset := int64(0); offset < info.Size(); {
				length := min(c.SyncChunkMiB<<20, info.Size()-offset)
				n, e := copyContext(ctx, f, io.LimitReader(input, length), buffer)
				s.Bytes += n
				if e != nil || n != length {
					f.Close()
					return errors.Join(e, io.ErrUnexpectedEOF)
				}
				at := time.Now()
				e = f.Sync()
				s.SyncSeconds += time.Since(at).Seconds()
				if e != nil {
					f.Close()
					return e
				}
				offset += length
			}
			return f.Close()
		})
		if e == nil {
			_, s.SHA256, e = hashFile(ctx, target)
			if e == nil && s.SHA256 != expected {
				e = errors.New("磁盘基准哈希错误")
			}
		}
		if e = record(s, e); e != nil {
			return report, e
		}
		if err = os.Remove(target); err != nil {
			return report, err
		}
	}
	if len(c.Cases) == 0 {
		report.Complete = true
		return report, nil
	}
	// Exact budget includes one warm-up and each verified raw and engine pass.
	report.EstimatedPayload = info.Size() * int64(1+c.Repeats*(1+len(c.Cases)))
	if report.EstimatedPayload > c.TrafficBudgetBytes {
		return report, errors.New("预计 SFTP 有效字节超过 traffic_budget_bytes，未开始网络基准")
	}
	client, err := nodeclient.New(device.Agent)
	if err != nil {
		return report, err
	}
	defer client.Close()
	type published struct {
		id, path string
		chunk    int64
	}
	cases := make([]published, 0, len(c.Cases))
	for _, v := range c.Cases {
		value, e := client.Manifest(ctx, device.ShareID, v.ManifestID)
		if e != nil {
			return report, e
		}
		if value.Manifest.ContentID != expected || value.Manifest.SizeBytes != info.Size() || value.Manifest.PayloadKind == "folder" {
			return report, errors.New("各源清单必须指向同一本机合成文件内容与长度，不能使用目录包")
		}
		cases = append(cases, published{v.ManifestID, value.SFTPPath, value.Manifest.ChunkSizeBytes})
	}
	observe := func() {
		r := app.Diagnose(ctx, app.DownloadRequest{Agent: device.Agent, SSH: device.SSH})
		report.Paths = append(report.Paths, PathObservation{r.ObservedAt, r.Path})
	}
	observe()
	if err = save(); err != nil {
		return report, err
	}
	// This one connection raw baseline includes dial, SFTP and streaming SHA-256,
	// but excludes target disk, Sync, SQLite and production final full reread.
	raw := func(iteration int, kind string) (Sample, error) {
		return measure(ctx, Sample{Kind: kind, Iteration: iteration, Workers: 1, Cache: "after verified network warm-up"}, func(s *Sample) error {
			readContext, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			cfg := device.SSH
			cfg.OnBytes = func(n int64) { s.PayloadBytes += n }
			c, e := transport.Dial(readContext, cfg)
			if e != nil {
				return e
			}
			defer c.Close()
			h := sha256.New()
			s.Bytes, e = c.ReadRange(readContext, cases[0].path, 0, info.Size(), h)
			s.SHA256 = hex.EncodeToString(h.Sum(nil))
			if e == nil && s.SHA256 != expected {
				return errors.New("SFTP 基准哈希错误")
			}
			return e
		})
	}
	warm, e := raw(0, "sftp_warmup")
	warm.Cache = "unknown before pass; establishes warm source baseline"
	if e = record(warm, e); e != nil {
		return report, e
	}
	for i := 0; i < c.Repeats; i++ {
		s, e := raw(i+1, "sftp_read_sha256")
		if e = record(s, e); e != nil {
			return report, e
		}
	}
	// Rotate case order between repeats to reduce systematic warm/order bias.
	for repeat := 0; repeat < c.Repeats; repeat++ {
		for j := range cases {
			index := (j + repeat) % len(cases)
			v := cases[index]
			target := filepath.Join(output, fmt.Sprintf("download-%d-%d.bin", repeat, index))
			var payload atomic.Int64
			s, e := measure(ctx, Sample{Kind: "engine_download", Iteration: repeat + 1, ChunkBytes: v.chunk, Workers: c.Cases[index].Workers, Cache: "warm source; new destination; rotated case order", Phases: map[string]float64{}}, func(s *Sample) error {
				r := device
				r.ManagedAgentConfig = ""
				r.ManagedAgentBinary = ""
				r.ManifestID = v.id
				r.Destination = target
				r.Database = filepath.Join(output, fmt.Sprintf("tasks-%d-%d.db", repeat, index))
				r.Workers = s.Workers
				r.Retries = 0
				r.OnBytes = func(n int64) { payload.Add(n) }
				phase := "control_and_setup"
				at := time.Now()
				r.OnProgress = func(p engine.Progress) {
					if p.State != phase {
						s.Phases[phase] += time.Since(at).Seconds()
						phase = p.State
						at = time.Now()
					}
				}
				result, e := app.Download(ctx, r)
				s.Phases[phase] += time.Since(at).Seconds()
				s.Bytes = result.Bytes
				s.SHA256 = result.SHA256
				s.PayloadBytes = payload.Load()
				if e == nil && (result.ReusedChunks != 0 || s.SHA256 != expected || s.Bytes != info.Size() || s.PayloadBytes != info.Size()) {
					return errors.New("引擎基准长度、哈希、实际字节或复用块不符合全新下载")
				}
				return e
			})
			// Independent final reread excluded from timing; engine already timed its
			// own required full verification before publishing.
			if e == nil {
				_, hash, checkErr := hashFile(ctx, target)
				e = checkErr
				if e == nil && hash != expected {
					e = errors.New("独立目标哈希核验失败")
				}
			}
			if e = record(s, e); e != nil {
				return report, e
			}
			if err = os.Remove(target); err != nil {
				return report, err
			}
		}
	}
	observe()
	report.Complete = true
	return report, nil
}

func copyContext(ctx context.Context, w io.Writer, r io.Reader, buffer []byte) (int64, error) {
	var total int64
	for {
		if e := ctx.Err(); e != nil {
			return total, e
		}
		n, e := r.Read(buffer)
		if n > 0 {
			m, we := w.Write(buffer[:n])
			total += int64(m)
			if we != nil {
				return total, we
			}
			if m != n {
				return total, io.ErrShortWrite
			}
		}
		if e == io.EOF {
			return total, nil
		}
		if e != nil {
			return total, e
		}
	}
}
func hashFile(ctx context.Context, path string) (int64, string, error) {
	f, e := os.Open(path)
	if e != nil {
		return 0, "", e
	}
	defer f.Close()
	h := sha256.New()
	n, e := copyContext(ctx, h, f, make([]byte, 1<<20))
	return n, hex.EncodeToString(h.Sum(nil)), e
}
func summarize(samples []Sample) []Summary {
	result := []Summary{}
	groups := map[string][]float64{}
	keys := map[string]Sample{}
	for _, s := range samples {
		if !s.Verified || s.Kind == "sftp_warmup" {
			continue
		}
		key := fmt.Sprintf("%s/%d/%d", s.Kind, s.ChunkBytes, s.Workers)
		groups[key] = append(groups[key], s.MiBPerSecond)
		keys[key] = s
	}
	names := make([]string, 0, len(groups))
	for k := range groups {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := groups[k]
		sort.Float64s(v)
		median := v[len(v)/2]
		if len(v)%2 == 0 {
			median = (v[len(v)/2-1] + median) / 2
		}
		s := keys[k]
		result = append(result, Summary{s.Kind, s.ChunkBytes, s.Workers, len(v), v[0], median, v[len(v)-1]})
	}
	return result
}
