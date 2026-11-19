package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- delta math ------------------------------------------------------------

func TestPercentChangeNonZeroBaseline(t *testing.T) {
	cases := []struct {
		name      string
		cur, prev float64
		wantPct   float64
		wantOK    bool
	}{
		{"increase", 120, 100, 20, true},
		{"decrease", 80, 100, -20, true},
		{"no change", 100, 100, 0, true},
		{"doubling", 200, 100, 100, true},
		{"drop to zero", 0, 50, -100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pct, ok := PercentChange(tc.cur, tc.prev)
			assert.Equal(t, tc.wantOK, ok)
			assert.InDelta(t, tc.wantPct, pct, 1e-9)
		})
	}
}

// Pending the TODO(human) decision in delta.go. Once a policy is chosen,
// remove the Skip and fill in the expected values.
func TestPercentChangeZeroBaselinePolicy(t *testing.T) {
	t.Skip("pending TODO(human): zero-baseline policy in PercentChange")
	_, ok := PercentChange(10, 0)
	assert.False(t, ok, "placeholder expectation; update when the policy is decided")
}

func TestFormatChange(t *testing.T) {
	assert.Equal(t, "+12%", FormatChange(12.4, true))
	assert.Equal(t, "-3%", FormatChange(-3.2, true))
	assert.Equal(t, "0%", FormatChange(0.2, true))
	assert.Equal(t, "—", FormatChange(0, false), "no baseline renders a dash, never NaN or Inf")
}

func TestToneFor(t *testing.T) {
	assert.Equal(t, ToneUp, ToneFor(10, true, false))
	assert.Equal(t, ToneDown, ToneFor(-10, true, false))
	assert.Equal(t, ToneFlat, ToneFor(0.1, true, false), "tiny moves are flat")
	assert.Equal(t, ToneFlat, ToneFor(50, false, false), "unknown delta is flat")

	// Latency rising should look bad (down tone), not good.
	assert.Equal(t, ToneDown, ToneFor(25, true, true))
	assert.Equal(t, ToneUp, ToneFor(-25, true, true))
}

func TestBuildCardsWithoutHistoryShowsDash(t *testing.T) {
	cur := Snapshot{Attendees: 12840, Reactions: 28491, AvgResponseMs: 42, RequestsPerSec: 1240}
	cards := buildCards(cur, nil)

	assert.Equal(t, "12,840", cards.LiveAttendees.Value)
	assert.Equal(t, int64(12840), cards.LiveAttendees.Raw)
	assert.Equal(t, "—", cards.LiveAttendees.Change)
	assert.Equal(t, "42 ms", cards.AvgResponseTime.Value)
	assert.Equal(t, "1,240/s", cards.RequestsPerSec.Value)
	assert.Equal(t, ToneFlat, cards.ReactionsSent.Tone)
}

func TestBuildCardsDeltasAgainstPreviousWindow(t *testing.T) {
	prev := Snapshot{Attendees: 1000, AvgResponseMs: 40, RequestsPerSec: 500}
	cur := Snapshot{Attendees: 1200, AvgResponseMs: 60, RequestsPerSec: 500}
	cards := buildCards(cur, &prev)

	assert.Equal(t, "+20%", cards.LiveAttendees.Change)
	assert.Equal(t, ToneUp, cards.LiveAttendees.Tone)
	assert.Equal(t, "+50%", cards.AvgResponseTime.Change)
	assert.Equal(t, ToneDown, cards.AvgResponseTime.Tone, "slower responses are a warning")
	assert.Equal(t, "0%", cards.RequestsPerSec.Change)
}

func TestSliding60sRate(t *testing.T) {
	// At the start of a minute the previous minute still counts in full.
	assert.InDelta(t, 100.0/60, sliding60sRate(0, 100, 0), 1e-9)
	// At the end of a minute only the current minute matters.
	assert.InDelta(t, 60.0/60, sliding60sRate(60, 100, time.Minute), 1e-9)
	// A negative offset (clock skew) clamps to the start of the minute, so the
	// previous minute counts in full alongside the current one.
	assert.InDelta(t, 160.0/60, sliding60sRate(60, 100, -time.Second), 1e-9)
}

// ---- helpers ---------------------------------------------------------------

func TestCommasAndDuration(t *testing.T) {
	assert.Equal(t, "0", commas(0))
	assert.Equal(t, "999", commas(999))
	assert.Equal(t, "1,000", commas(1000))
	assert.Equal(t, "28,491", commas(28491))
	assert.Equal(t, "-1,234", commas(-1234))

	assert.Equal(t, "40s", formatDuration(40))
	assert.Equal(t, "12m 40s", formatDuration(760))
}

func TestTimeAgo(t *testing.T) {
	assert.Equal(t, "just now", timeAgo(10*time.Second))
	assert.Equal(t, "5m ago", timeAgo(5*time.Minute+10*time.Second))
	assert.Equal(t, "2h ago", timeAgo(2*time.Hour+5*time.Minute))
}

func TestParseRange(t *testing.T) {
	d, err := parseRange("")
	require.NoError(t, err)
	assert.Equal(t, trafficDefault, d)

	d, err = parseRange("2h")
	require.NoError(t, err)
	assert.Equal(t, 2*time.Hour, d)

	_, err = parseRange("soon")
	assert.Error(t, err)
	_, err = parseRange("-5m")
	assert.Error(t, err)
}

func TestParseOptionalInt(t *testing.T) {
	n, err := parseOptionalInt("", redis.Nil)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), n, "missing key counts as zero")

	n, err = parseOptionalInt("42", nil)
	assert.NoError(t, err)
	assert.Equal(t, int64(42), n)

	_, err = parseOptionalInt("", fmt.Errorf("connection reset"))
	assert.Error(t, err)
}

func TestClampRange(t *testing.T) {
	assert.Equal(t, trafficDefault, clampRange(0))
	assert.Equal(t, trafficMax, clampRange(100*time.Hour))
	assert.Equal(t, time.Hour, clampRange(time.Hour))
}

// ---- detector --------------------------------------------------------------

func snapAt(eventID string, at time.Time, attendees int64, rps float64) Snapshot {
	return Snapshot{EventID: eventID, At: at, Attendees: attendees, RequestsPerSec: rps}
}

func TestDetectorMilestoneAnnouncedOnce(t *testing.T) {
	d := NewDetector()
	now := time.Now()

	anns := d.Observe(snapAt("ev", now, 999, 0), 0)
	assert.Empty(t, anns, "below the first milestone")

	anns = d.Observe(snapAt("ev", now, 1000, 0), 0)
	require.Len(t, anns, 1)
	assert.Equal(t, "milestone", anns[0].Kind)
	assert.Equal(t, int64(1000), anns[0].Milestone)
	assert.Equal(t, "Audience reached 1,000", anns[0].Title)

	// The caller persists the milestone; with it passed back, nothing repeats.
	anns = d.Observe(snapAt("ev", now, 1050, 0), 1000)
	assert.Empty(t, anns)
}

func TestDetectorJumpAnnouncesHighestMilestoneOnly(t *testing.T) {
	d := NewDetector()
	anns := d.Observe(snapAt("ev", time.Now(), 4300, 0), 1000)
	require.Len(t, anns, 1)
	assert.Equal(t, int64(4000), anns[0].Milestone)
}

func TestDetectorSpikeNeedsWarmupAndRespectsCooldown(t *testing.T) {
	d := NewDetector()
	start := time.Now()

	// Warm-up: 10 samples at 100 req/s. No spike can fire yet.
	for i := 0; i < 10; i++ {
		anns := d.Observe(snapAt("ev", start.Add(time.Duration(i)*2*time.Second), 0, 100), 0)
		assert.Empty(t, anns, "no spikes during warm-up")
	}

	// 200 req/s is 2x the mean, above the 1.5x threshold: one spike.
	anns := d.Observe(snapAt("ev", start.Add(20*time.Second), 0, 200), 0)
	require.Len(t, anns, 1)
	assert.Equal(t, "spike", anns[0].Kind)

	// Still spiking 4 seconds later, but inside the 60 s cooldown: silent.
	anns = d.Observe(snapAt("ev", start.Add(24*time.Second), 0, 220), 0)
	assert.Empty(t, anns)

	// Past the cooldown and still well above average: announced again.
	anns = d.Observe(snapAt("ev", start.Add(90*time.Second), 0, 300), 0)
	assert.Len(t, anns, 1)
}

func TestDetectorIgnoresZeroBaseline(t *testing.T) {
	d := NewDetector()
	start := time.Now()
	for i := 0; i < 10; i++ {
		d.Observe(snapAt("ev", start.Add(time.Duration(i)*time.Second), 0, 0), 0)
	}
	anns := d.Observe(snapAt("ev", start.Add(11*time.Second), 0, 500), 0)
	assert.Empty(t, anns, "a zero mean is not a spike baseline")
}

// ---- prometheus client -----------------------------------------------------

func TestPromQueryInstantSumsSeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/query", r.URL.Path)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"instance":"a"},"value":[1700000000,"10.5"]},
			{"metric":{"instance":"b"},"value":[1700000000,"4.5"]}]}}`))
	}))
	defer srv.Close()

	c := NewPromClient(srv.URL, time.Second)
	got, err := c.QueryInstant(context.Background(), "sum(x)")
	require.NoError(t, err)
	assert.InDelta(t, 15.0, got, 1e-9)
}

func TestPromQueryInstantEmptyResultIsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	got, err := NewPromClient(srv.URL, time.Second).QueryInstant(context.Background(), "sum(x)")
	require.NoError(t, err)
	assert.Equal(t, 0.0, got)
}

func TestPromQueryRangeSumsAndSorts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"instance":"a"},"values":[[1700000300,"5"],[1700000000,"NaN"]]},
			{"metric":{"instance":"b"},"values":[[1700000300,"7"],[1700000000,"3"]]}]}}`))
	}))
	defer srv.Close()

	start := time.Unix(1700000000, 0)
	samples, err := NewPromClient(srv.URL, time.Second).QueryRange(context.Background(), "sum(x)", start, start.Add(5*time.Minute), 5*time.Minute)
	require.NoError(t, err)
	require.Len(t, samples, 2)
	assert.Equal(t, time.Unix(1700000000, 0).UTC(), samples[0].T)
	assert.InDelta(t, 3.0, samples[0].V, 1e-9, "NaN is treated as no data")
	assert.InDelta(t, 12.0, samples[1].V, 1e-9)
}

func TestPromErrorsAreReturned(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"http 500": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		"status error": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
		},
		"garbage body": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html>not json`))
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			_, err := NewPromClient(srv.URL, time.Second).QueryInstant(context.Background(), "x")
			assert.Error(t, err)
		})
	}
}

func TestPromTimeoutIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	start := time.Now()
	_, err := NewPromClient(srv.URL, 100*time.Millisecond).QueryInstant(context.Background(), "x")
	assert.Error(t, err)
	assert.Less(t, time.Since(start), time.Second, "the 2s client timeout must bound the call")
}

// ---- cache -----------------------------------------------------------------

func TestCacheSharesOneLoadAcrossConcurrentCallers(t *testing.T) {
	c := NewCache()
	var loads atomic.Int32
	release := make(chan struct{})

	var wg sync.WaitGroup
	results := make([]int, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := Get(c, "k", time.Second, func() (int, error) {
				loads.Add(1)
				<-release
				return 7, nil
			})
			assert.NoError(t, err)
			results[i] = v
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.Equal(t, int32(1), loads.Load(), "singleflight: one computation for 50 callers")
	for _, v := range results {
		assert.Equal(t, 7, v)
	}
}

func TestCacheExpiresAndDoesNotCacheErrors(t *testing.T) {
	c := NewCache()
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }

	calls := 0
	load := func() (string, error) {
		calls++
		if calls == 1 {
			return "", fmt.Errorf("prometheus down")
		}
		return "ok", nil
	}

	_, err := Get(c, "k", time.Second, load)
	assert.Error(t, err)

	// The error was not cached, so the next call retries and succeeds.
	v, err := Get(c, "k", time.Second, load)
	require.NoError(t, err)
	assert.Equal(t, "ok", v)

	// Within the TTL it is served from cache.
	now = now.Add(500 * time.Millisecond)
	_, _ = Get(c, "k", time.Second, load)
	assert.Equal(t, 2, calls)

	// After the TTL it reloads.
	now = now.Add(2 * time.Second)
	_, _ = Get(c, "k", time.Second, load)
	assert.Equal(t, 3, calls)
}

// ---- leader election -------------------------------------------------------

// fakeLockStore is an in-memory LockStore with real time, so TTLs behave as in Redis.
type fakeLockStore struct {
	mu      sync.Mutex
	owner   string
	expires time.Time
}

func (f *fakeLockStore) Acquire(_ context.Context, _ string, owner string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owner != "" && time.Now().Before(f.expires) {
		return false, nil
	}
	f.owner = owner
	f.expires = time.Now().Add(ttl)
	return true, nil
}

func (f *fakeLockStore) Renew(_ context.Context, _ string, owner string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owner != owner || !time.Now().Before(f.expires) {
		return false, nil
	}
	f.expires = time.Now().Add(ttl)
	return true, nil
}

func (f *fakeLockStore) currentOwner() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Now().After(f.expires) {
		return ""
	}
	return f.owner
}

// leaderProbe counts how many leader functions are running at once.
type leaderProbe struct {
	running atomic.Int32
	maxSeen atomic.Int32
}

func (p *leaderProbe) lead(name string) func(context.Context) {
	return func(ctx context.Context) {
		n := p.running.Add(1)
		for {
			m := p.maxSeen.Load()
			if n <= m || p.maxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		_ = name
		<-ctx.Done()
		p.running.Add(-1)
	}
}

func TestElectionSingleLeaderAndTakeoverAfterCrash(t *testing.T) {
	store := &fakeLockStore{}
	const ttl = 200 * time.Millisecond
	const renew = 50 * time.Millisecond

	var probe leaderProbe

	ctxA, stopA := context.WithCancel(context.Background())
	defer stopA()
	ctxB, stopB := context.WithCancel(context.Background())
	defer stopB()

	elA := NewElector(store, "stats:leader", "api-01", ttl, renew)
	elB := NewElector(store, "stats:leader", "api-02", ttl, renew)

	go elA.Run(ctxA, probe.lead("api-01"))
	go elB.Run(ctxB, probe.lead("api-02"))

	require.Eventually(t, func() bool { return store.currentOwner() != "" }, time.Second, 10*time.Millisecond)
	first := store.currentOwner()
	assert.Contains(t, []string{"api-01", "api-02"}, first)

	// Only one contender leads at a time.
	time.Sleep(3 * renew)
	assert.Equal(t, int32(1), probe.running.Load())
	assert.Equal(t, int32(1), probe.maxSeen.Load())

	// Crash the leader: its context stops, renewals stop, the lock expires.
	if first == "api-01" {
		stopA()
	} else {
		stopB()
	}

	require.Eventually(t, func() bool {
		owner := store.currentOwner()
		return owner != "" && owner != first
	}, 2*time.Second, 10*time.Millisecond, "the surviving instance must take over after the TTL")

	assert.Equal(t, int32(1), probe.maxSeen.Load(), "never more than one leader at once")
}

func TestElectorStepsDownWhenLockIsLost(t *testing.T) {
	store := &fakeLockStore{owner: "someone-else", expires: time.Now().Add(time.Hour)}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	var started atomic.Bool
	el := NewElector(store, "k", "api-01", time.Second, 20*time.Millisecond)
	go el.Run(ctx, func(context.Context) { started.Store(true) })

	time.Sleep(100 * time.Millisecond)
	assert.False(t, started.Load(), "a held lock must not start the leader function")
}

// ---- snapshot history ------------------------------------------------------

func TestSnapshotJSONRoundTrip(t *testing.T) {
	in := Snapshot{EventID: "cc-0826", At: time.Unix(1_700_000_000, 0).UTC(), Attendees: 5, Source: SourcePrometheus}
	raw, err := json.Marshal(in)
	require.NoError(t, err)

	var out Snapshot
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in.Attendees, out.Attendees)
	assert.True(t, strings.Contains(string(raw), `"source":"prometheus"`))
}
