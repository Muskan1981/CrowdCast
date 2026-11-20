// Package metrics owns the Prometheus registry for one backend process.
//
// Every collector is created once in New and carries a constant
// instance="<INSTANCE_ID>" label, so Prometheus can tell api-01/02/03 apart
// when it scrapes them. The process-wide instance is reachable through
// Default(), which lets packages record metrics without threading a
// *Metrics through every constructor.
package metrics

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// latencyBuckets covers 5ms to 2s, the range that matters for the p95 < 250 ms budget.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2}

// Metrics holds every collector for one process.
type Metrics struct {
	InstanceID string
	registry   *prometheus.Registry

	HTTPRequests    *prometheus.CounterVec   // route, method, status
	HTTPDuration    *prometheus.HistogramVec // route, method
	WSActive        prometheus.Gauge
	WSMessages      *prometheus.CounterVec // type, direction
	WSDropped       prometheus.Counter
	Reactions       *prometheus.CounterVec // emoji
	ChatMessages    prometheus.Counter
	PollVotes       prometheus.Counter
	RedisOpDuration *prometheus.HistogramVec // op
	PubSubLag       prometheus.Histogram
	AppErrors       *prometheus.CounterVec // kind
	EventAudience   prometheus.Gauge
	NodeCPU         prometheus.Gauge
	NodeMemory      prometheus.Gauge
	ChaosEvents     *prometheus.CounterVec // node, action

	sampler sampler

	// requests counts every HTTP request handled by this instance. Unlike the
	// labelled counter it can be read cheaply, so the stats package uses it to
	// feed the Redis traffic fallback.
	requests atomic.Uint64
	conns    atomic.Int64
}

// RequestCount returns the total HTTP requests this instance has handled.
func (m *Metrics) RequestCount() uint64 {
	return m.requests.Load()
}

// New builds a fresh registry with every CrowdCast collector plus the Go and
// process collectors. Each call returns an independent registry, so tests can
// create as many instances as they need.
func New(instanceID string) *Metrics {
	reg := prometheus.NewRegistry()
	// Every metric registered through `reg` gets instance="<id>" for free.
	wrapped := prometheus.WrapRegistererWith(prometheus.Labels{"instance": instanceID}, reg)

	m := &Metrics{
		InstanceID: instanceID,
		registry:   reg,

		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests served, labelled by chi route pattern.",
		}, []string{"route", "method", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by chi route pattern (WebSocket upgrades excluded).",
			Buckets: latencyBuckets,
		}, []string{"route", "method"}),
		WSActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ws_connections_active",
			Help: "Open WebSocket connections on this instance.",
		}),
		WSMessages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ws_messages_total",
			Help: "WebSocket frames by message type and direction (in|out).",
		}, []string{"type", "direction"}),
		WSDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ws_dropped_slow_consumer_total",
			Help: "Clients disconnected because their send buffer was full.",
		}),
		Reactions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reactions_total",
			Help: "Accepted reactions by emoji.",
		}, []string{"emoji"}),
		ChatMessages: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "chat_messages_total",
			Help: "Accepted chat messages.",
		}),
		PollVotes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "poll_votes_total",
			Help: "Accepted poll votes.",
		}),
		RedisOpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "redis_op_duration_seconds",
			Help:    "Redis command round-trip time by command name.",
			Buckets: latencyBuckets,
		}, []string{"op"}),
		PubSubLag: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "pubsub_lag_seconds",
			Help:    "Time from Redis PUBLISH (embedded _ts) to local receive.",
			Buckets: latencyBuckets,
		}),
		AppErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "app_errors_total",
			Help: "Application errors by kind (panic, redis, ws_upgrade, ...).",
		}, []string{"kind"}),
		EventAudience: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "event_audience",
			Help: "Current attendee count for the most recently ticked event on this instance.",
		}),
		NodeCPU: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "node_cpu_percent",
			Help: "CPU usage of this process as a percentage of one core's wall time, normalised by core count.",
		}),
		ChaosEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "chaos_events_total",
			Help: "Chaos commands applied on this node, by action. Grafana annotates from changes to this counter.",
		}, []string{"node", "action"}),
		NodeMemory: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "node_memory_percent",
			Help: "Memory in use as a percentage of the container limit (host memory when unlimited).",
		}),
	}

	wrapped.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.HTTPRequests, m.HTTPDuration,
		m.WSActive, m.WSMessages, m.WSDropped,
		m.Reactions, m.ChatMessages, m.PollVotes,
		m.RedisOpDuration, m.PubSubLag,
		m.AppErrors, m.EventAudience,
		m.NodeCPU, m.NodeMemory, m.ChaosEvents,
	)
	return m
}

// Registry exposes the underlying registry (useful for tests and custom handlers).
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// Handler serves the Prometheus text exposition format. Mount it at the ROOT
// path (/metrics), never under /api/v1, so the public Nginx route can block it.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

var (
	defaultMu sync.Mutex
	defaultM  *Metrics
)

// Default returns the process-wide Metrics. If SetDefault was never called it
// creates one labelled "unknown" so callers never receive nil.
func Default() *Metrics {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultM == nil {
		defaultM = New("unknown")
	}
	return defaultM
}

// SetDefault installs the process-wide Metrics. main calls it once at startup.
func SetDefault(m *Metrics) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultM = m
}

// ConnOpened records a new WebSocket connection on this instance. It updates
// the Prometheus gauge and an atomic counter the node heartbeat reads cheaply.
func (m *Metrics) ConnOpened() {
	m.WSActive.Inc()
	m.conns.Add(1)
}

// ConnClosed is the counterpart of ConnOpened.
func (m *Metrics) ConnClosed() {
	m.WSActive.Dec()
	m.conns.Add(-1)
}

// ActiveConnections returns the open WebSocket connections on this instance.
func (m *Metrics) ActiveConnections() int64 {
	return m.conns.Load()
}
