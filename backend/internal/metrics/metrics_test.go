package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var expectedFamilies = []string{
	"http_requests_total",
	"http_request_duration_seconds",
	"ws_connections_active",
	"ws_messages_total",
	"ws_dropped_slow_consumer_total",
	"reactions_total",
	"chat_messages_total",
	"poll_votes_total",
	"redis_op_duration_seconds",
	"pubsub_lag_seconds",
	"app_errors_total",
	"event_audience",
	"node_cpu_percent",
	"node_memory_percent",
	"go_goroutines",
	"process_cpu_seconds_total",
}

func TestNewRegistersEveryCollectorWithInstanceLabel(t *testing.T) {
	m := New("api-test")

	// Touch the labelled vectors so they appear in Gather output.
	m.HTTPRequests.WithLabelValues("/x", "GET", "200").Inc()
	m.HTTPDuration.WithLabelValues("/x", "GET").Observe(0.01)
	m.WSMessages.WithLabelValues("chat", "in").Inc()
	m.Reactions.WithLabelValues("🔥").Inc()
	m.RedisOpDuration.WithLabelValues("get").Observe(0.001)
	m.AppErrors.WithLabelValues("panic").Inc()

	families, err := m.Registry().Gather()
	require.NoError(t, err)

	names := make(map[string]bool, len(families))
	for _, f := range families {
		names[f.GetName()] = true
		for _, metric := range f.GetMetric() {
			found := false
			for _, lp := range metric.GetLabel() {
				if lp.GetName() == "instance" && lp.GetValue() == "api-test" {
					found = true
				}
			}
			assert.True(t, found, "%s is missing the instance label", f.GetName())
		}
	}
	for _, want := range expectedFamilies {
		assert.True(t, names[want], "expected metric family %s to be registered", want)
	}
}

func TestNewReturnsIndependentRegistries(t *testing.T) {
	// Two processes-in-one-test must not collide on duplicate registration.
	a := New("a")
	b := New("b")
	assert.NotSame(t, a.Registry(), b.Registry())
}

func TestCountersMoveWithTraffic(t *testing.T) {
	m := New("api-test")
	before := testutil.ToFloat64(m.Reactions.WithLabelValues("🔥"))
	m.Reactions.WithLabelValues("🔥").Inc()
	m.Reactions.WithLabelValues("🔥").Inc()
	assert.Equal(t, before+2, testutil.ToFloat64(m.Reactions.WithLabelValues("🔥")))
}

func TestHTTPMiddlewareLabelsByRoutePattern(t *testing.T) {
	m := New("api-test")

	r := chi.NewRouter()
	r.Use(m.HTTPMiddleware())
	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/events/{id}/chat", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	for _, id := range []string{"cc-0826", "cc-0827", "uuid-1234"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+id+"/chat", nil)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	// All three requests share ONE series labelled by pattern, not raw path.
	got := testutil.ToFloat64(m.HTTPRequests.WithLabelValues("/api/v1/events/{id}/chat", "GET", "200"))
	assert.Equal(t, float64(3), got)

	// No series may contain a raw event ID (cardinality guard).
	body := gatherText(t, m)
	assert.NotContains(t, body, "cc-0826")
	assert.NotContains(t, body, "uuid-1234")
}

func TestHTTPMiddlewareUnmatchedRoute(t *testing.T) {
	m := New("api-test")

	r := chi.NewRouter()
	r.Use(m.HTTPMiddleware())
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/wp-admin/login.php", nil))

	assert.Equal(t, float64(1), testutil.ToFloat64(m.HTTPRequests.WithLabelValues(UnmatchedRoute, "GET", "404")))
}

func TestHTTPMiddlewareRecordsStatusCode(t *testing.T) {
	m := New("api-test")

	r := chi.NewRouter()
	r.Use(m.HTTPMiddleware())
	r.Post("/boom", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/boom", nil))

	assert.Equal(t, float64(1), testutil.ToFloat64(m.HTTPRequests.WithLabelValues("/boom", "POST", "418")))
}

func TestLabelRouteTreatsSubrouterNotFoundAsUnmatched(t *testing.T) {
	assert.Equal(t, UnmatchedRoute, labelRoute("/api/v1/*", http.StatusNotFound))
	assert.Equal(t, "/api/v1/*", labelRoute("/api/v1/*", http.StatusOK), "a real catch-all keeps its label")
	assert.Equal(t, "/api/v1/events/{id}", labelRoute("/api/v1/events/{id}", http.StatusNotFound))
}

func TestHTTPMiddlewareLabelsSubrouterNotFoundAsUnmatched(t *testing.T) {
	m := New("api-test")
	r := chi.NewRouter()
	r.Use(m.HTTPMiddleware())
	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {})
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))
	assert.Equal(t, float64(1), testutil.ToFloat64(m.HTTPRequests.WithLabelValues(UnmatchedRoute, "GET", "404")))
}

func TestRoutePatternFallsBackWithoutRouteContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	assert.Equal(t, UnmatchedRoute, RoutePattern(req))
}

func TestMetricsHandlerServesExposition(t *testing.T) {
	m := New("api-test")
	m.ChatMessages.Inc()

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "chat_messages_total")
	assert.Contains(t, rec.Body.String(), `instance="api-test"`)
}

func TestMessageType(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		want  string
	}{
		{"known outbound", `{"t":"reaction_batch","total":5}`, "reaction_batch"},
		{"known inbound", `{"t":"chat","text":"hi"}`, "chat"},
		{"unknown type is bucketed", `{"t":"hacker-type-123"}`, "other"},
		{"no discriminator", `{"text":"hi"}`, "unknown"},
		{"unterminated value", `{"t":"chat`, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, MessageType([]byte(tc.frame)))
		})
	}
}

func TestStampPublishAddsTimestampAndKeepsObject(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_123)

	stamped := string(StampPublish([]byte(`{"t":"chat","text":"hi"}`), now))
	assert.Equal(t, `{"t":"chat","text":"hi","_ts":1700000000123}`, stamped)

	empty := string(StampPublish([]byte(`{}`), now))
	assert.Equal(t, `{"_ts":1700000000123}`, empty)

	notObject := []byte(`[1,2,3]`)
	assert.Equal(t, notObject, StampPublish(notObject, now))
}

func TestObservePubSubLagIgnoresMissingAndSkewedStamps(t *testing.T) {
	m := New("api-test")
	SetDefault(m)
	t.Cleanup(func() { SetDefault(New("unknown")) })

	// Frame without _ts: nothing observed.
	ObservePubSubLag([]byte(`{"t":"chat"}`), time.Now())

	// Frame stamped 50ms ago: one observation.
	past := time.Now().Add(-50 * time.Millisecond)
	frame := StampPublish([]byte(`{"t":"chat"}`), past)
	ObservePubSubLag(frame, time.Now())

	body := gatherText(t, m)
	assert.Contains(t, body, "pubsub_lag_seconds_count{instance=\"api-test\"} 1")
}

func TestParseCgroupValue(t *testing.T) {
	v, ok := parseCgroupValue("1073741824\n")
	assert.True(t, ok)
	assert.Equal(t, uint64(1073741824), v)

	_, ok = parseCgroupValue("max\n")
	assert.False(t, ok, "max means unlimited and must not be used as a limit")

	_, ok = parseCgroupValue("")
	assert.False(t, ok)

	_, ok = parseCgroupValue("not-a-number")
	assert.False(t, ok)
}

func TestPercentOf(t *testing.T) {
	assert.InDelta(t, 50.0, percentOf(512, 1024), 0.0001)
	assert.Equal(t, 0.0, percentOf(100, 0), "zero limit must not divide by zero")
	assert.Equal(t, 100.0, percentOf(2048, 1024), "usage above limit is capped at 100")
}

func TestNodeStatsAccessorIsInRange(t *testing.T) {
	m := New("api-test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.StartSampler(ctx, 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)

	stats := m.NodeStats()
	assert.GreaterOrEqual(t, stats.CPUPercent, 0.0)
	assert.LessOrEqual(t, stats.CPUPercent, 100.0)
	assert.GreaterOrEqual(t, stats.MemoryPercent, 0.0)
	assert.LessOrEqual(t, stats.MemoryPercent, 100.0)
}

func gatherText(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return strings.TrimSpace(rec.Body.String())
}
