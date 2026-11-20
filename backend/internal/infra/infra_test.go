package infra

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeKV stores values with real expiry, so TTL behaviour matches Redis.
type fakeKV struct {
	mu   sync.Mutex
	data map[string]entry
	fail bool
}

type entry struct {
	value   []byte
	expires time.Time
}

func (f *fakeKV) SetEX(_ context.Context, key string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("redis down")
	}
	f.data[key] = entry{value: value, expires: time.Now().Add(ttl)}
	return nil
}

func (f *fakeKV) GetMany(_ context.Context, keys ...string) (map[string][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("redis down")
	}
	out := map[string][]byte{}
	for _, k := range keys {
		if e, ok := f.data[k]; ok && time.Now().Before(e.expires) {
			out[k] = e.value
		}
	}
	return out, nil
}

func newKV() *fakeKV { return &fakeKV{data: map[string]entry{}} }

var names = []string{"crowdcast-api-01", "crowdcast-api-02", "crowdcast-api-03"}

// A record expires on its own when the node stops heartbeating.
func TestRegistryRecordExpiresWithoutHeartbeat(t *testing.T) {
	kv := newKV()
	reg := NewRegistry(kv, 50*time.Millisecond)
	ctx := context.Background()

	require.NoError(t, reg.Heartbeat(ctx, NodeRecord{Name: "crowdcast-api-01", Health: HealthHealthy}, time.Now()))
	live, err := reg.Snapshot(ctx, names)
	require.NoError(t, err)
	assert.Contains(t, live, "crowdcast-api-01")

	time.Sleep(70 * time.Millisecond)
	live, err = reg.Snapshot(ctx, names)
	require.NoError(t, err)
	assert.Empty(t, live, "a dead node disappears once its TTL passes")
}

func TestRegistryRoundTripsRecord(t *testing.T) {
	reg := NewRegistry(newKV(), time.Second)
	now := time.Unix(1_700_000_000, 0)
	rec := NodeRecord{Name: "crowdcast-api-02", Zone: "Secondary node", Weight: 30, RequestsPerSec: 412.5, CPU: 12, Memory: 40, Connections: 7, Health: HealthDraining}
	require.NoError(t, reg.Heartbeat(context.Background(), rec, now))

	live, err := reg.Snapshot(context.Background(), []string{"crowdcast-api-02"})
	require.NoError(t, err)
	got := live["crowdcast-api-02"]
	assert.Equal(t, rec.Health, got.Health)
	assert.Equal(t, int64(7), got.Connections)
	assert.True(t, got.LastSeen.Equal(now.UTC()))
}

func TestRegistryTreatsCorruptRecordAsAbsent(t *testing.T) {
	kv := newKV()
	kv.data["node:crowdcast-api-03:hb"] = entry{value: []byte("{not json"), expires: time.Now().Add(time.Minute)}
	live, err := NewRegistry(kv, time.Second).Snapshot(context.Background(), []string{"crowdcast-api-03"})
	require.NoError(t, err)
	assert.Empty(t, live)
}

// Expected nodes with no live record appear as Offline with zeros.
func TestBuildFleetShowsMissingNodeAsOffline(t *testing.T) {
	live := map[string]NodeRecord{
		"crowdcast-api-01": {Name: "crowdcast-api-01", Health: HealthHealthy, RequestsPerSec: 500, Connections: 10, CPU: 20, Memory: 30},
		"crowdcast-api-03": {Name: "crowdcast-api-03", Health: HealthHealthy, RequestsPerSec: 200, Connections: 4},
	}
	rows, totals, status, degraded := BuildFleet(ExpectedNodes, live)

	require.Len(t, rows, 3)
	assert.Equal(t, "Offline", rows[1].Status)
	assert.Equal(t, "0", rows[1].Requests)
	assert.Equal(t, "peach", rows[1].Color, "the expected colour is kept for the offline row")
	assert.Equal(t, "Healthy", rows[0].Status)
	assert.Equal(t, int64(700), totals.TotalRequests)
	assert.Equal(t, int64(14), totals.ActiveConnections)
	assert.Equal(t, "Degraded", status, "one node missing while others serve")
	assert.True(t, degraded)
}

func TestBuildFleetStatusLevels(t *testing.T) {
	all := map[string]NodeRecord{}
	for _, e := range ExpectedNodes {
		all[e.Name] = NodeRecord{Name: e.Name, Health: HealthHealthy}
	}
	_, _, status, degraded := BuildFleet(ExpectedNodes, all)
	assert.Equal(t, "Operational", status)
	assert.False(t, degraded)

	_, _, status, _ = BuildFleet(ExpectedNodes, map[string]NodeRecord{})
	assert.Equal(t, "Outage", status, "no serving node at all is an outage")

	// Redis-down nodes still serve, so they count as serving.
	deg := map[string]NodeRecord{"crowdcast-api-01": {Name: "crowdcast-api-01", Health: HealthDegraded}}
	_, _, status, _ = BuildFleet(ExpectedNodes, deg)
	assert.Equal(t, "Degraded", status)
}

func TestStatusLabels(t *testing.T) {
	assert.Equal(t, "Healthy", statusLabel(HealthHealthy))
	assert.Equal(t, "Draining", statusLabel(HealthDraining))
	assert.Equal(t, "Failed", statusLabel(HealthFailed))
	assert.Equal(t, "Offline", statusLabel("unknown"))
}

func TestCommas(t *testing.T) {
	assert.Equal(t, "6,420", commas(6420))
	assert.Equal(t, "0", commas(0))
	assert.Equal(t, "-1,000", commas(-1000))
}

type fakeProm struct{ latency, errRate float64 }

func (f fakeProm) QueryInstant(_ context.Context, q string) (float64, error) {
	if q == qAvgLatency {
		return f.latency, nil
	}
	return f.errRate, nil
}

func TestFleetMetricsFromPromFillsLatencyAndErrors(t *testing.T) {
	out := FleetMetricsFromProm(context.Background(), fakeProm{latency: 42, errRate: 0.5}, FleetMetrics{TotalRequests: 9})
	assert.Equal(t, 42.0, out.AvgLatencyMs)
	assert.Equal(t, 0.5, out.ErrorRatePct)
	assert.Equal(t, int64(9), out.TotalRequests)
}

func TestMonitorChecksReportDownWithoutFailingPage(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer up.Close()

	out := CheckMonitors(context.Background(), []MonitorCheck{
		{Name: "Prometheus", URL: up.URL},
		{Name: "Grafana", URL: "http://127.0.0.1:1"},
	}, 200*time.Millisecond)

	require.Len(t, out, 2)
	assert.Equal(t, "Up", out[0].Status)
	assert.True(t, out[0].Good)
	assert.Equal(t, "Down", out[1].Status)
	assert.False(t, out[1].Good)
}

func TestHandlerReturnsOfflineWhenRedisDown(t *testing.T) {
	kv := newKV()
	kv.fail = true
	h := NewHandler(NewRegistry(kv, time.Second), ExpectedNodes, nil, nil)

	rec := httptest.NewRecorder()
	h.Nodes(rec, httptest.NewRequest(http.MethodGet, "/api/v1/infra/nodes", nil))
	require.Equal(t, http.StatusOK, rec.Code, "a Redis outage must not fail the fleet page")

	var view FleetView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	assert.Equal(t, "Outage", view.FleetStatus)
	assert.Len(t, view.ServerRows, 3)
}

func TestRunHeartbeatWritesRecords(t *testing.T) {
	kv := newKV()
	reg := NewRegistry(kv, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	beats := 0
	build := func(time.Time) NodeRecord {
		mu.Lock()
		defer mu.Unlock()
		beats++
		return NodeRecord{Name: "crowdcast-api-01", Health: HealthHealthy}
	}
	go RunHeartbeat(ctx, reg, 5*time.Millisecond, build)

	require.Eventually(t, func() bool {
		live, _ := reg.Snapshot(context.Background(), []string{"crowdcast-api-01"})
		return len(live) == 1
	}, time.Second, 5*time.Millisecond)
}
