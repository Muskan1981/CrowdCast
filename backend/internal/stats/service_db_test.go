package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crowdcast/crowdcast/internal/events"
	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/metrics"
	"github.com/crowdcast/crowdcast/internal/presence"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/crowdcast/crowdcast/internal/testutil"
)

// env is a stats service wired to the test Redis and Postgres, with a fake Prometheus.
type env struct {
	svc    *Service
	rdb    *redisstore.Client
	pool   *pgxpool.Pool
	event  string
	prom   *fakeProm
	server *httptest.Server
}

// fakeProm answers Prometheus API calls. Each path can be given a handler; by
// default every call fails with 500, which exercises the fallback path.
type fakeProm struct {
	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
}

func (f *fakeProm) set(path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[path] = h
}

func (f *fakeProm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	h := f.handlers[r.URL.Path]
	f.mu.Unlock()
	if h == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	h(w, r)
}

// promVector answers an instant query with one value per instance label.
func promVector(values map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var result []string
		for inst, v := range values {
			result = append(result, fmt.Sprintf(`{"metric":{"instance":%q},"value":[1700000000,%q]}`, inst, v))
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[%s]}}`, strings.Join(result, ","))
	}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	rdb := testutil.Redis(t)
	pool := testutil.Postgres(t)
	eventID := testutil.Event(t, pool)

	prom := &fakeProm{handlers: map[string]http.HandlerFunc{}}
	server := httptest.NewServer(prom)
	t.Cleanup(server.Close)

	viewers := postgres.NewViewerRepository(pool)
	pres := presence.NewService(rdb, viewers, postgres.NewEventRepository(pool))
	t.Cleanup(pres.Close)
	h := hub.NewHub(rdb, pres)
	t.Cleanup(h.Close)

	svc := NewService(Deps{
		Redis:     rdb,
		Presence:  pres,
		Events:    events.NewService(rdb, postgres.NewEventRepository(pool), postgres.NewActivityRepository(pool)),
		Viewers:   viewers,
		Reactions: postgres.NewReactionRepository(pool),
		Stats:     postgres.NewStatsRepository(pool),
		Activity:  postgres.NewActivityRepository(pool),
		Hub:       h,
		Prom:      NewPromClient(server.URL, 2*time.Second),
	})
	return &env{svc: svc, rdb: rdb, pool: pool, event: eventID, prom: prom, server: server}
}

func TestTakeSnapshotFromPrometheus(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	require.NoError(t, e.rdb.HSet(ctx, redisstore.EventReactionsKey(e.event), "total", 42).Err())
	require.NoError(t, e.rdb.Set(ctx, redisstore.EventChatCountKey(e.event), 7, 0).Err())
	e.prom.set("/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if strings.Contains(q, "histogram") || strings.Contains(q, "_sum") {
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"42.5"]}]}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"12.5"]}]}}`)
	})

	snap, err := e.svc.TakeSnapshot(ctx, e.event)
	require.NoError(t, err)
	assert.Equal(t, SourcePrometheus, snap.Source)
	assert.Equal(t, int64(42), snap.Reactions)
	assert.Equal(t, int64(7), snap.ChatMessages)
	assert.InDelta(t, 12.5, snap.RequestsPerSec, 1e-9)
}

func TestTakeSnapshotFallsBackToRedisBuckets(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Now().UTC()
	cur := now.Truncate(time.Minute)
	require.NoError(t, e.rdb.HIncrBy(ctx, redisstore.StatsRequestsKey(), fmt.Sprint(cur.Unix()), 120).Err())

	snap, err := e.svc.TakeSnapshot(ctx, e.event) // no Prometheus handlers: every call fails
	require.NoError(t, err)
	assert.Equal(t, SourceFallback, snap.Source)
	assert.Greater(t, snap.RequestsPerSec, 0.0, "the Redis minute bucket feeds the fallback rate")
}

func TestHistoryAndPreviousSnapshot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-5 * time.Minute)
	old := Snapshot{EventID: e.event, At: base, Attendees: 100}
	recent := Snapshot{EventID: e.event, At: base.Add(4 * time.Minute), Attendees: 180}
	require.NoError(t, e.svc.recordHistory(ctx, old))
	require.NoError(t, e.svc.recordHistory(ctx, recent))

	// At base+4m30s, the cutoff is base+3m30s: only the old snapshot is at or before it.
	prev, err := e.svc.previousSnapshot(ctx, e.event, base.Add(4*time.Minute+30*time.Second), time.Minute)
	require.NoError(t, err)
	require.NotNil(t, prev)
	assert.Equal(t, int64(100), prev.Attendees, "the newest snapshot at or before now-1m is the baseline")

	// At base+5m, the cutoff is base+4m, which includes the recent snapshot exactly on the boundary.
	prev, err = e.svc.previousSnapshot(ctx, e.event, base.Add(5*time.Minute), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(180), prev.Attendees, "a snapshot exactly on the cutoff counts")

	none, err := e.svc.previousSnapshot(ctx, e.event, base.Add(time.Second), time.Minute)
	require.NoError(t, err)
	assert.Nil(t, none, "no snapshot old enough yields nil, so the card shows a dash")
}

func TestOverviewBuildsCardsActivityAndDistribution(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.prom.set("/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if strings.Contains(q, "instance") {
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"instance":"crowdcast-api-01"},"value":[1,"50"]},`+
				`{"metric":{"instance":"crowdcast-api-02"},"value":[1,"30"]}]}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"9"]}]}}`)
	})
	require.NoError(t, e.svc.activity.Insert(ctx, &postgres.ActivityItem{EventID: e.event, ActivityType: "spike", Title: "Spike", Description: "d", Icon: "zap", Tone: "warn"}))

	out, err := e.svc.Overview(ctx, e.event)
	require.NoError(t, err)
	assert.Equal(t, e.event, out.Event.ID)
	assert.Equal(t, SourcePrometheus, out.TrafficSummary.Source)
	require.Len(t, out.Distribution, 2)
	assert.Equal(t, 63, out.Distribution[0].SharePct, "50 of 80 requests is 62.5%, rounded")
	require.NotEmpty(t, out.Activity)
	assert.Equal(t, "Spike", out.Activity[0].Title)
	assert.Equal(t, "—", out.Metrics.LiveAttendees.Change, "no previous snapshot yet")
}

func TestOverviewUnknownEventIsNotFound(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Overview(context.Background(), testutil.RandomID("missing"))
	assert.ErrorIs(t, err, events.ErrEventNotFound)
}

func TestTrafficFromPrometheusAndFallback(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Hour)
	e.prom.set("/api/v1/query_range", func(w http.ResponseWriter, r *http.Request) {
		ts1 := start.Add(5 * time.Minute).Unix()
		ts2 := start.Add(10 * time.Minute).Unix()
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[%d,"30"],[%d,"45"]]}]}}`, ts1, ts2)
	})
	points, source, err := e.svc.Traffic(ctx, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, SourcePrometheus, source)
	require.NotEmpty(t, points)
	assert.Equal(t, int64(30), points[0].Requests)

	// Without Prometheus, the minute buckets in Redis are summed into 5-minute points.
	e2 := newEnv(t)
	bucket := time.Now().UTC().Truncate(5 * time.Minute).Add(-5 * time.Minute)
	require.NoError(t, e2.rdb.HIncrBy(ctx, redisstore.StatsRequestsKey(), fmt.Sprint(bucket.Add(time.Minute).Unix()), 11).Err())
	require.NoError(t, e2.rdb.HIncrBy(ctx, redisstore.StatsRequestsKey(), fmt.Sprint(bucket.Add(2*time.Minute).Unix()), 4).Err())
	points, source, err = e2.svc.Traffic(ctx, 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, SourceFallback, source)
	var total int64
	for _, p := range points {
		total += p.Requests
	}
	assert.GreaterOrEqual(t, total, int64(15))
}

func TestAudienceStatsFromPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	viewerRepo := postgres.NewViewerRepository(e.pool)
	now := time.Now().UTC()
	for i, city := range []string{"Mumbai", "Mumbai", "Pune"} {
		vid := testutil.RandomID("v")
		require.NoError(t, viewerRepo.Upsert(ctx, &postgres.Viewer{ID: vid, DisplayName: fmt.Sprint("V", i), City: city, Color: "mint", FirstSeenAt: now, LastSeenAt: now}))
		_, err := viewerRepo.RecordAttendance(ctx, &postgres.AttendanceSession{ViewerID: vid, EventID: e.event, JoinedAt: now.Add(-10 * time.Minute)})
		require.NoError(t, err)
	}
	reactionRepo := postgres.NewReactionRepository(e.pool)
	require.NoError(t, reactionRepo.RecordRollup(ctx, &postgres.ReactionRollup{EventID: e.event, MinuteBucket: now.Truncate(time.Minute), Emoji: "🔥", Count: 9, CreatedAt: now}))
	require.NoError(t, reactionRepo.RecordRollup(ctx, &postgres.ReactionRollup{EventID: e.event, MinuteBucket: now.Truncate(time.Minute), Emoji: "👏", Count: 4, CreatedAt: now}))

	out, err := e.svc.Audience(ctx, e.event)
	require.NoError(t, err)
	assert.Equal(t, "3", out.Metrics.TotalAttendees.Value)
	assert.NotEmpty(t, out.CityMix)
	require.NotEmpty(t, out.TopReactions)
	assert.Equal(t, "🔥", out.TopReactions[0].Emoji)
	assert.Equal(t, "Most used", out.TopReactions[0].Subtitle)
	assert.Equal(t, "Fire", out.TopReactions[0].Name)
}

func TestRunDetectorPersistsMilestoneOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	snap := Snapshot{EventID: e.event, At: time.Now().UTC(), Attendees: 1500, RequestsPerSec: 5}
	e.svc.runDetector(ctx, snap)

	last, err := e.rdb.Get(ctx, redisstore.StatsMilestoneKey(e.event)).Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(1000), last)

	e.svc.runDetector(ctx, snap)
	items, err := e.svc.activity.ListRecent(ctx, e.event, 10)
	require.NoError(t, err)
	count := 0
	for _, it := range items {
		if it.ActivityType == "milestone" {
			count++
		}
	}
	assert.Equal(t, 1, count, "the same milestone is announced once, even after a leader change")
}

func TestTickAllRecordsHistoryForLiveEvents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	require.NoError(t, e.rdb.SAdd(ctx, redisstore.StatsEventsKey(), e.event).Err())
	e.svc.tickAll(ctx)

	n, err := e.rdb.ZCard(ctx, redisstore.StatsHistoryKey(e.event)).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func TestRunLeaderStopsWhenCancelled(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.svc.RunLeader(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunLeader did not stop after cancel")
	}
}

func TestRedisLockStore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	store := NewRedisLockStore(e.rdb.Client)
	key := testutil.RandomID("lock")

	got, err := store.Acquire(ctx, key, "api-01", time.Second)
	require.NoError(t, err)
	assert.True(t, got)

	got, err = store.Acquire(ctx, key, "api-02", time.Second)
	require.NoError(t, err)
	assert.False(t, got, "a held lock cannot be taken")

	ok, err := store.Renew(ctx, key, "api-01", time.Second)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = store.Renew(ctx, key, "api-02", time.Second)
	require.NoError(t, err)
	assert.False(t, ok, "only the owner can renew")
}

func TestRequestFlusherWritesCountsToRedis(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	m := metrics.New("flush-test")
	handler := m.HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	go RunRequestFlusher(ctx, e.rdb, m, 20*time.Millisecond)
	for i := 0; i < 5; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	}
	<-ctx.Done()

	vals, err := e.rdb.HGetAll(context.Background(), redisstore.StatsRequestsKey()).Result()
	require.NoError(t, err)
	var sum int
	for _, v := range vals {
		var n int
		_, _ = fmt.Sscan(v, &n)
		sum += n
	}
	assert.GreaterOrEqual(t, sum, 5)
}

func TestStatsHandlers(t *testing.T) {
	e := newEnv(t)
	e.prom.set("/api/v1/query_range", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	})
	h := NewHandler(e.svc)
	r := chi.NewRouter()
	r.Get("/events/{id}/stats/overview", h.Overview)
	r.Get("/events/{id}/stats/traffic", h.Traffic)
	r.Get("/events/{id}/stats/audience", h.Audience)

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	rec := get("/events/" + e.event + "/stats/overview")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body OverviewResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, e.event, body.Event.ID)

	assert.Equal(t, http.StatusNotFound, get("/events/"+testutil.RandomID("x")+"/stats/overview").Code)
	// Audience for an unknown event is an empty result, not an error: the page shows zeros.
	assert.Equal(t, http.StatusOK, get("/events/"+testutil.RandomID("x")+"/stats/audience").Code)

	rec = get("/events/" + e.event + "/stats/traffic?range=2h")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-Data-Source"))
	assert.Equal(t, http.StatusBadRequest, get("/events/"+e.event+"/stats/traffic?range=soon").Code)

	assert.Equal(t, http.StatusOK, get("/events/"+e.event+"/stats/audience").Code)
}

func TestPromQueryByLabel(t *testing.T) {
	e := newEnv(t)
	e.prom.set("/api/v1/query", promVector(map[string]string{"crowdcast-api-01": "5", "crowdcast-api-02": "3"}))
	got, err := e.svc.prom.QueryByLabel(context.Background(), "x", "instance")
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"crowdcast-api-01": 5, "crowdcast-api-02": 3}, got)
}

func TestPublishingReachesRoomClients(t *testing.T) {
	e := newEnv(t)
	client := &recordingClient{id: "viewer", event: e.event}
	e.svc.hub.Register(client)
	require.Eventually(t, func() bool { return e.svc.hub.TotalClients() == 1 }, 2*time.Second, 5*time.Millisecond)

	e.svc.publishStatsTick(context.Background(), e.event, buildCards(Snapshot{}, nil), Snapshot{At: time.Now()})
	e.svc.publishActivity(context.Background(), e.event, &postgres.ActivityItem{ID: 1, Title: "t", Icon: "zap", Tone: "warn"})
	require.Eventually(t, func() bool { return client.count() >= 2 }, 2*time.Second, 5*time.Millisecond)
}

type recordingClient struct {
	id, event string
	mu        sync.Mutex
	n         int
}

func (c *recordingClient) ID() string      { return c.id }
func (c *recordingClient) EventID() string { return c.event }
func (c *recordingClient) Send([]byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return true
}
func (c *recordingClient) Close(int, string) {}
func (c *recordingClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}
