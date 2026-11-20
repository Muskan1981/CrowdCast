package chaos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingEffects captures side effects so tests can assert on them.
type recordingEffects struct {
	mu      sync.Mutex
	closes  []int
	records []Command
}

func (r *recordingEffects) CloseSockets(code int, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes = append(r.closes, code)
}

func (r *recordingEffects) Record(_ context.Context, cmd Command, _ State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, cmd)
}

func cmd(target, action string, latency int) Command {
	return Command{ID: "id-" + target + "-" + action + "-" + time.Now().String(), Target: target, Action: action, LatencyMs: latency, Origin: "api-01"}
}

// Command targeting: A publishes, and only the addressed node changes.
func TestCommandOnlyAffectsTarget(t *testing.T) {
	store := NewMemoryStore()
	fxB := &recordingEffects{}
	nodeB := NewController("crowdcast-api-02", store, fxB)
	nodeC := NewController("crowdcast-api-03", store, &recordingEffects{})

	_, err := nodeC.Apply(context.Background(), cmd("crowdcast-api-02", ActionFail, 0))
	assert.ErrorIs(t, err, ErrNotTarget, "a node must refuse a command addressed elsewhere")
	assert.False(t, nodeC.Failed())

	st, err := nodeB.Apply(context.Background(), cmd("crowdcast-api-02", ActionFail, 0))
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, st.Status)
	assert.True(t, nodeB.Failed())
	assert.Equal(t, []int{CloseCodeRestart}, fxB.closes, "failing closes sockets with 1012")
}

// Commands arrive through the control channel; Run applies only the ones for this node.
func TestRunAppliesOnlyOwnCommands(t *testing.T) {
	node := NewController("crowdcast-api-02", NewMemoryStore(), &recordingEffects{})
	ch := make(chan []byte, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go node.Run(ctx, ch)

	other, _ := json.Marshal(cmd("crowdcast-api-01", ActionFail, 0))
	mine, _ := json.Marshal(cmd("crowdcast-api-02", ActionLatency, 250))
	ch <- other
	ch <- []byte("not json")
	ch <- mine

	require.Eventually(t, func() bool { return node.Latency() == 250*time.Millisecond }, time.Second, 5*time.Millisecond)
	assert.False(t, node.Failed())
}

// A redelivered command must not run twice.
func TestDuplicateCommandIsIgnored(t *testing.T) {
	fx := &recordingEffects{}
	node := NewController("crowdcast-api-02", NewMemoryStore(), fx)
	c := cmd("crowdcast-api-02", ActionFail, 0)

	_, _ = node.Apply(context.Background(), c)
	_, _ = node.Apply(context.Background(), c)
	assert.Len(t, fx.closes, 1, "the second delivery of the same ID is a no-op")
}

func TestRestoreReversesFailure(t *testing.T) {
	node := NewController("crowdcast-api-02", NewMemoryStore(), &recordingEffects{})
	ctx := context.Background()
	_, _ = node.Apply(ctx, cmd("crowdcast-api-02", ActionLatency, 300))
	_, _ = node.Apply(ctx, cmd("crowdcast-api-02", ActionFail, 0))
	require.True(t, node.Failed())

	st, err := node.Apply(ctx, cmd("crowdcast-api-02", ActionRestore, 0))
	require.NoError(t, err)
	assert.Equal(t, StatusHealthy, st.Status)
	assert.False(t, node.Failed())
	assert.Equal(t, time.Duration(0), node.Latency(), "restore also clears latency")
}

func TestLatencyIsCapped(t *testing.T) {
	node := NewController("crowdcast-api-02", NewMemoryStore(), &recordingEffects{})
	st, err := node.Apply(context.Background(), cmd("crowdcast-api-02", ActionLatency, 60_000))
	require.NoError(t, err)
	assert.Equal(t, int(MaxLatency/time.Millisecond), st.LatencyMs, "latency is capped at 5 s")

	assert.Equal(t, 0, clampLatency(-10))
}

func TestUnknownActionRejected(t *testing.T) {
	node := NewController("crowdcast-api-02", NewMemoryStore(), &recordingEffects{})
	_, err := node.Apply(context.Background(), cmd("crowdcast-api-02", "explode", 0))
	assert.True(t, errors.Is(err, ErrUnknownAction))
}

// Chaos state survives a restart: a new controller loads what the old one saved.
func TestChaosPersistsAcrossRestart(t *testing.T) {
	store := NewMemoryStore()
	first := NewController("crowdcast-api-02", store, &recordingEffects{})
	_, err := first.Apply(context.Background(), cmd("crowdcast-api-02", ActionFail, 0))
	require.NoError(t, err)

	restarted := NewController("crowdcast-api-02", store, &recordingEffects{})
	require.False(t, restarted.Failed(), "before Load the new process knows nothing")
	require.NoError(t, restarted.Load(context.Background()))
	assert.True(t, restarted.Failed(), "a restarted failed node stays failed until restored")
}

func TestMiddlewareRefusesWhenFailedButNotExempt(t *testing.T) {
	node := NewController("crowdcast-api-02", NewMemoryStore(), &recordingEffects{})
	_, _ = node.Apply(context.Background(), cmd("crowdcast-api-02", ActionFail, 0))

	h := Middleware(node)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/events/cc-0826", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "failed nodes return 503 so nginx retries elsewhere")

	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/api/v1/admin/nodes/x/restore"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, "%s must stay reachable on a failed node", path)
	}
}

func TestHealthzReportsChaos(t *testing.T) {
	node := NewController("crowdcast-api-02", NewMemoryStore(), &recordingEffects{})
	h := HealthzHandler(node, "crowdcast-api-02")

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	_, _ = node.Apply(context.Background(), cmd("crowdcast-api-02", ActionFail, 0))
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"unhealthy"`)
}

// fakePublisher records published commands and applies them to the target
// node's controller, standing in for Redis pub/sub in a single process.
type fakePublisher struct {
	targets map[string]*Controller
}

func (f fakePublisher) Publish(ctx context.Context, _ string, message any) error {
	raw, ok := message.([]byte)
	if !ok {
		return errors.New("expected JSON payload")
	}
	var c Command
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	if node, ok := f.targets[c.Target]; ok {
		go func() { _, _ = node.Apply(ctx, c) }()
	}
	return nil
}

// Any node can receive the admin request; the target executes it.
func TestAdminRequestOnOneNodeFailsAnother(t *testing.T) {
	store := NewMemoryStore()
	names := []string{"crowdcast-api-01", "crowdcast-api-02"}
	node01 := NewController("crowdcast-api-01", store, &recordingEffects{})
	node02 := NewController("crowdcast-api-02", store, &recordingEffects{})

	pub := fakePublisher{targets: map[string]*Controller{
		"crowdcast-api-01": node01, "crowdcast-api-02": node02,
	}}
	admin := NewAdminHandler("crowdcast-api-01", names, pub, "ps:control", store)
	admin.timeout = time.Second

	r := chi.NewRouter()
	r.Post("/nodes/{name}/fail", admin.Fail)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withName(httptest.NewRequest(http.MethodPost, "/nodes/crowdcast-api-02/fail", nil), "crowdcast-api-02"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp ChaosActionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, StatusFailed, resp.CurrentStatus)
	assert.True(t, node02.Failed())
	assert.False(t, node01.Failed(), "the instance that received the request is untouched")
}

func TestAdminRejectsUnknownNode(t *testing.T) {
	admin := NewAdminHandler("crowdcast-api-01", []string{"crowdcast-api-01"}, fakePublisher{}, "ps:control", NewMemoryStore())
	r := chi.NewRouter()
	r.Post("/nodes/{name}/fail", admin.Fail)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withName(httptest.NewRequest(http.MethodPost, "/nodes/ghost/fail", nil), "ghost"))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminReportsFailureWhenTargetNeverConfirms(t *testing.T) {
	store := NewMemoryStore()
	admin := NewAdminHandler("crowdcast-api-01", []string{"crowdcast-api-02"}, fakePublisher{}, "ps:control", store)
	admin.timeout = 100 * time.Millisecond

	r := chi.NewRouter()
	r.Post("/nodes/{name}/fail", admin.Fail)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withName(httptest.NewRequest(http.MethodPost, "/nodes/crowdcast-api-02/fail", nil), "crowdcast-api-02"))

	var resp ChaosActionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "no confirmation from the target means success is false")
}

func withName(r *http.Request, name string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", name)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
