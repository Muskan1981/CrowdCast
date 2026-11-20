package reactions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	"github.com/crowdcast/crowdcast/internal/testutil"
)

func TestBatchesAreWrittenToRedis(t *testing.T) {
	rdb := testutil.Redis(t)
	eventID := testutil.RandomID("rx")
	svc := NewService(rdb, nil, nil, 20*time.Millisecond)
	t.Cleanup(svc.Close)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		require.NoError(t, svc.RecordReaction(ctx, eventID, testutil.RandomID("u"), "🔥"))
	}
	require.Eventually(t, func() bool {
		total, err := svc.GetTotalReactions(ctx, eventID)
		return err == nil && total == 3
	}, 2*time.Second, 10*time.Millisecond, "the batch flusher adds 3 reactions to the Redis total")

	total, err := svc.GetTotalReactions(ctx, testutil.RandomID("none"))
	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "an event with no reactions reads as zero")
}

func TestMinuteRollupsArePersisted(t *testing.T) {
	pool := testutil.Postgres(t)
	eventID := testutil.Event(t, pool)
	repo := postgres.NewReactionRepository(pool)
	svc := NewService(nil, repo, nil, time.Hour)
	t.Cleanup(svc.Close)

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		require.NoError(t, svc.RecordReaction(ctx, eventID, testutil.RandomID("u"), "👏"))
	}
	require.NoError(t, svc.RecordReaction(ctx, eventID, testutil.RandomID("u"), "⚡"))

	svc.flushMinuteRollups()

	top, err := repo.GetTopReactions(ctx, eventID)
	require.NoError(t, err)
	totals := map[string]int64{}
	for _, row := range top {
		totals[row.Emoji] = row.Total
	}
	assert.Equal(t, int64(2), totals["👏"])
	assert.Equal(t, int64(1), totals["⚡"])
}

func TestFlushWithoutRedisIsANoOp(t *testing.T) {
	svc := NewService(nil, nil, nil, time.Hour)
	t.Cleanup(svc.Close)
	require.NoError(t, svc.RecordReaction(context.Background(), "ev", "u", "🎉"))
	svc.flushPendingBatches() // must not panic without Redis
	svc.flushMinuteRollups()  // no repository: nothing to write
}

func TestReactionHandler(t *testing.T) {
	rdb := testutil.Redis(t)
	svc := NewService(rdb, nil, nil, 20*time.Millisecond)
	t.Cleanup(svc.Close)
	r := chi.NewRouter()
	r.Post("/events/{id}/reactions", NewHandler(svc).SendReaction)

	burstViewer := testutil.RandomID("viewer")
	post := func(body string, claims bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/events/"+testutil.RandomID("ev")+"/reactions", strings.NewReader(body))
		if claims {
			req = req.WithContext(auth.WithViewerClaims(req.Context(), &auth.ViewerClaims{UserID: burstViewer}))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"emoji":"🔥"}`, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out ReactionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "🔥", out.Emoji)

	assert.Equal(t, http.StatusOK, post(`{"emoji":"👏"}`, false).Code, "anonymous reactions are allowed")
	assert.Equal(t, http.StatusBadRequest, post(`{"emoji":"💩"}`, true).Code)
	assert.Equal(t, http.StatusBadRequest, post(`not json`, true).Code)

	limited := false
	for i := 0; i < 40; i++ {
		if post(`{"emoji":"🔥"}`, true).Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	assert.True(t, limited, "a burst from one viewer is rate limited")
}

func TestHandlerRejectsMissingEventID(t *testing.T) {
	svc := NewService(nil, nil, nil, time.Hour)
	t.Cleanup(svc.Close)
	rec := httptest.NewRecorder()
	NewHandler(svc).SendReaction(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
