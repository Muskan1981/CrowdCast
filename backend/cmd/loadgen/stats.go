package main

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// latencyBoundsMs are the upper edges of the latency histogram buckets. Using
// fixed buckets keeps recording lock-free and memory constant at any user count.
var latencyBoundsMs = []float64{1, 2, 5, 10, 20, 50, 100, 200, 300, 500, 1000, 2000, 5000, 10000}

// histogram is a lock-free fixed-bucket latency histogram.
type histogram struct {
	counts []atomic.Int64 // len(latencyBoundsMs)+1; last is overflow
	total  atomic.Int64
	sumUs  atomic.Int64
}

func newHistogram() *histogram {
	return &histogram{counts: make([]atomic.Int64, len(latencyBoundsMs)+1)}
}

func (h *histogram) observe(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	idx := len(latencyBoundsMs)
	for i, b := range latencyBoundsMs {
		if ms <= b {
			idx = i
			break
		}
	}
	h.counts[idx].Add(1)
	h.total.Add(1)
	h.sumUs.Add(int64(d / time.Microsecond))
}

// quantile returns the upper bound (ms) of the bucket containing quantile q.
func (h *histogram) quantile(q float64) float64 {
	total := h.total.Load()
	if total == 0 {
		return 0
	}
	target := int64(float64(total) * q)
	var seen int64
	for i := range h.counts {
		seen += h.counts[i].Load()
		if seen > target {
			if i < len(latencyBoundsMs) {
				return latencyBoundsMs[i]
			}
			return latencyBoundsMs[len(latencyBoundsMs)-1] * 2
		}
	}
	return 0
}

// servedBy counts responses per instance, from the X-Served-By header.
type servedBy struct {
	mu sync.Mutex
	m  map[string]int64
}

func (s *servedBy) add(instance string) {
	if instance == "" {
		instance = "unknown"
	}
	s.mu.Lock()
	s.m[instance]++
	s.mu.Unlock()
}

func (s *servedBy) snapshot() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// counters is the process-wide state shared by every virtual user.
type counters struct {
	sessionsOK     atomic.Int64
	sessionsFailed atomic.Int64
	connected      atomic.Int64
	connectedPeak  atomic.Int64
	joined         atomic.Int64
	msgsIn         atomic.Int64
	msgsOut        atomic.Int64
	httpRequests   atomic.Int64
	httpErrors     atomic.Int64
	reactions      atomic.Int64
	chats          atomic.Int64
	votes          atomic.Int64
	reconnects     atomic.Int64
	reconnects1012 atomic.Int64
	wsDialFailures atomic.Int64
	errorsByKind   sync.Map // kind -> *atomic.Int64
	latency        *histogram
	dialLatency    *histogram
	served         *servedBy
	prom           *promMetrics
}

func newCounters(withProm bool) *counters {
	c := &counters{
		latency:     newHistogram(),
		dialLatency: newHistogram(),
		served:      &servedBy{m: map[string]int64{}},
	}
	if withProm {
		c.prom = newPromMetrics()
	}
	return c
}

func (c *counters) connUp() {
	n := c.connected.Add(1)
	for {
		peak := c.connectedPeak.Load()
		if n <= peak || c.connectedPeak.CompareAndSwap(peak, n) {
			break
		}
	}
	if c.prom != nil {
		c.prom.connected.Inc()
	}
}

func (c *counters) connDown() {
	c.connected.Add(-1)
	if c.prom != nil {
		c.prom.connected.Dec()
	}
}

func (c *counters) errorKind(kind string) {
	v, _ := c.errorsByKind.LoadOrStore(kind, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
	if c.prom != nil {
		c.prom.errors.WithLabelValues(kind).Inc()
	}
}

func (c *counters) errorCounts() map[string]int64 {
	out := map[string]int64{}
	c.errorsByKind.Range(func(k, v any) bool {
		out[k.(string)] = v.(*atomic.Int64).Load()
		return true
	})
	return out
}

// promMetrics are the loadgen_* series exposed with --metrics-addr.
type promMetrics struct {
	registry  *prometheus.Registry
	connected prometheus.Gauge
	messages  *prometheus.CounterVec
	requests  *prometheus.CounterVec
	errors    *prometheus.CounterVec
	reconns   prometheus.Counter
	latency   prometheus.Histogram
}

func newPromMetrics() *promMetrics {
	reg := prometheus.NewRegistry()
	m := &promMetrics{
		registry:  reg,
		connected: prometheus.NewGauge(prometheus.GaugeOpts{Name: "loadgen_connected_users", Help: "Virtual users with an open WebSocket."}),
		messages:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "loadgen_messages_total", Help: "WebSocket frames by direction."}, []string{"direction"}),
		requests:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "loadgen_http_requests_total", Help: "HTTP requests by status code."}, []string{"code"}),
		errors:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "loadgen_errors_total", Help: "Errors by kind."}, []string{"kind"}),
		reconns:   prometheus.NewCounter(prometheus.CounterOpts{Name: "loadgen_reconnects_total", Help: "Reconnect attempts after a dropped socket."}),
		latency: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "loadgen_request_seconds", Help: "HTTP request latency.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}}),
	}
	reg.MustRegister(m.connected, m.messages, m.requests, m.errors, m.reconns, m.latency)
	return m
}

// summary is the final JSON report written when a run ends.
type summary struct {
	Scenario       string             `json:"scenario"`
	Seed           int64              `json:"seed"`
	Users          int                `json:"users"`
	DurationS      float64            `json:"duration_s"`
	StartedAt      time.Time          `json:"started_at"`
	FinishedAt     time.Time          `json:"finished_at"`
	SessionsOK     int64              `json:"sessions_ok"`
	SessionsFailed int64              `json:"sessions_failed"`
	ConnectedPeak  int64              `json:"connected_peak"`
	MsgsIn         int64              `json:"msgs_in"`
	MsgsOut        int64              `json:"msgs_out"`
	HTTPRequests   int64              `json:"http_requests"`
	HTTPErrors     int64              `json:"http_errors"`
	Reactions      int64              `json:"reactions"`
	Chats          int64              `json:"chats"`
	Votes          int64              `json:"votes"`
	Reconnects     int64              `json:"reconnects"`
	Reconnects1012 int64              `json:"reconnects_1012"`
	LatencyMs      map[string]float64 `json:"latency_ms"`
	DialLatencyMs  map[string]float64 `json:"dial_latency_ms"`
	ServedBy       map[string]int64   `json:"served_by"`
	ServedByPct    map[string]float64 `json:"served_by_pct"`
	Errors         map[string]int64   `json:"errors"`
}

func buildSummary(c *counters, s *summary) {
	s.SessionsOK = c.sessionsOK.Load()
	s.SessionsFailed = c.sessionsFailed.Load()
	s.ConnectedPeak = c.connectedPeak.Load()
	s.MsgsIn = c.msgsIn.Load()
	s.MsgsOut = c.msgsOut.Load()
	s.HTTPRequests = c.httpRequests.Load()
	s.HTTPErrors = c.httpErrors.Load()
	s.Reactions = c.reactions.Load()
	s.Chats = c.chats.Load()
	s.Votes = c.votes.Load()
	s.Reconnects = c.reconnects.Load()
	s.Reconnects1012 = c.reconnects1012.Load()
	s.LatencyMs = quantiles(c.latency)
	s.DialLatencyMs = quantiles(c.dialLatency)
	s.ServedBy = c.served.snapshot()
	s.ServedByPct = percentShares(s.ServedBy)
	s.Errors = c.errorCounts()
}

func quantiles(h *histogram) map[string]float64 {
	return map[string]float64{"p50": h.quantile(0.50), "p95": h.quantile(0.95), "p99": h.quantile(0.99)}
}

func percentShares(m map[string]int64) map[string]float64 {
	var total int64
	for _, v := range m {
		total += v
	}
	out := map[string]float64{}
	if total == 0 {
		return out
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = float64(m[k]) * 100 / float64(total)
	}
	return out
}
