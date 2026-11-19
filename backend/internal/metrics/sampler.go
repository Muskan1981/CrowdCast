package metrics

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

// Cgroup v2 files. Inside Docker these describe the container's memory limit.
const (
	cgroupMemCurrent = "/sys/fs/cgroup/memory.current"
	cgroupMemMax     = "/sys/fs/cgroup/memory.max"
)

// NodeStats is the latest CPU and memory sample, exposed in-process so the node
// registry can heartbeat it to Redis for the Infrastructure page.
type NodeStats struct {
	CPUPercent    float64
	MemoryPercent float64
}

// sampler keeps the last sample under a mutex. CPU needs the previous reading
// to compute a delta, so the sampler holds that state too.
type sampler struct {
	mu       sync.RWMutex
	latest   NodeStats
	lastCPU  float64 // cumulative user+system seconds at last sample
	lastWall time.Time
	proc     *process.Process
	numCPU   float64
}

// NodeStats returns the most recent CPU and memory sample. It is safe to call
// from any goroutine and never blocks on sampling.
func (m *Metrics) NodeStats() NodeStats {
	m.sampler.mu.RLock()
	defer m.sampler.mu.RUnlock()
	return m.sampler.latest
}

// StartSampler samples CPU and memory every interval until ctx is cancelled.
func (m *Metrics) StartSampler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	m.initSampler()
	m.sample()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.sample()
			}
		}
	}()
}

func (m *Metrics) initSampler() {
	m.sampler.mu.Lock()
	defer m.sampler.mu.Unlock()
	if m.sampler.proc != nil {
		return
	}
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		m.sampler.proc = p
	}
	m.sampler.numCPU = float64(runtime.NumCPU())
}

func (m *Metrics) sample() {
	cpu := m.sampleCPU()
	mem := memoryPercent()

	m.sampler.mu.Lock()
	m.sampler.latest = NodeStats{CPUPercent: cpu, MemoryPercent: mem}
	m.sampler.mu.Unlock()

	m.NodeCPU.Set(cpu)
	m.NodeMemory.Set(mem)
}

// sampleCPU returns this process's CPU use since the previous sample, as a
// percentage of total machine capacity (0-100).
func (m *Metrics) sampleCPU() float64 {
	m.sampler.mu.Lock()
	defer m.sampler.mu.Unlock()
	if m.sampler.proc == nil {
		return 0
	}
	times, err := m.sampler.proc.Times()
	if err != nil {
		return m.sampler.latest.CPUPercent
	}
	total := times.User + times.System
	now := time.Now()

	var pct float64
	if !m.sampler.lastWall.IsZero() {
		wall := now.Sub(m.sampler.lastWall).Seconds()
		if wall > 0 {
			pct = (total - m.sampler.lastCPU) / wall / m.sampler.numCPU * 100
		}
	}
	m.sampler.lastCPU = total
	m.sampler.lastWall = now
	return clamp(pct, 0, 100)
}

// memoryPercent prefers the container's cgroup limit. Outside a container (or
// when the limit is "max") it falls back to the host's total memory.
func memoryPercent() float64 {
	if used, limit, ok := readCgroupMemory(); ok {
		return percentOf(used, limit)
	}
	vm, err := mem.VirtualMemory()
	if err != nil || vm.Total == 0 {
		return 0
	}
	return percentOf(vm.Used, vm.Total)
}

// readCgroupMemory returns current usage and the limit from cgroup v2 files.
// ok is false when the files are absent or the limit is unbounded ("max").
func readCgroupMemory() (used, limit uint64, ok bool) {
	cur, err := os.ReadFile(cgroupMemCurrent)
	if err != nil {
		return 0, 0, false
	}
	max, err := os.ReadFile(cgroupMemMax)
	if err != nil {
		return 0, 0, false
	}
	used, okUsed := parseCgroupValue(string(cur))
	limit, okLimit := parseCgroupValue(string(max))
	if !okUsed || !okLimit {
		return 0, 0, false
	}
	return used, limit, true
}

// parseCgroupValue parses a cgroup counter. "max" (unlimited) returns ok=false.
func parseCgroupValue(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// percentOf returns used/limit as a 0-100 percentage, guarding against a zero limit.
func percentOf(used, limit uint64) float64 {
	if limit == 0 {
		return 0
	}
	return clamp(float64(used)/float64(limit)*100, 0, 100)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
