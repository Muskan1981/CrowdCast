package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crowdcast/crowdcast/internal/store/postgres"
	"github.com/crowdcast/crowdcast/internal/testutil"
)

func newEventService(t *testing.T) (*Service, string) {
	t.Helper()
	rdb := testutil.Redis(t)
	pool := testutil.Postgres(t)
	eventID := testutil.Event(t, pool)
	svc := NewService(rdb, postgres.NewEventRepository(pool), postgres.NewActivityRepository(pool))
	return svc, eventID
}

func TestGetEventReturnsSeededDetails(t *testing.T) {
	svc, eventID := newEventService(t)
	ev, err := svc.GetEvent(context.Background(), eventID)
	require.NoError(t, err)
	assert.Equal(t, eventID, ev.ID)
	assert.Equal(t, "scheduled", ev.Status)
	assert.Equal(t, "Host", ev.Host.Name)
	assert.Equal(t, "1080p", ev.Stage.Resolution)
}

func TestGetEventNotFound(t *testing.T) {
	svc, _ := newEventService(t)
	_, err := svc.GetEvent(context.Background(), testutil.RandomID("missing"))
	assert.ErrorIs(t, err, ErrEventNotFound)
}

func TestLifecycleTransitionsUpdateStateAndLiveTimer(t *testing.T) {
	svc, eventID := newEventService(t)
	ctx := context.Background()

	status, err := svc.TransitionEvent(ctx, eventID, ActionStart)
	require.NoError(t, err)
	assert.Equal(t, "live", status)

	ev, err := svc.GetEvent(ctx, eventID)
	require.NoError(t, err)
	assert.Equal(t, "live", ev.Status)
	assert.NotEmpty(t, ev.StartedAt, "starting records the start time")

	status, err = svc.TransitionEvent(ctx, eventID, ActionPause)
	require.NoError(t, err)
	assert.Equal(t, "paused", status)

	status, err = svc.TransitionEvent(ctx, eventID, ActionResume)
	require.NoError(t, err)
	assert.Equal(t, "live", status)

	status, err = svc.TransitionEvent(ctx, eventID, ActionEnd)
	require.NoError(t, err)
	assert.Equal(t, "ended", status)

	_, err = svc.TransitionEvent(ctx, eventID, ActionPause)
	assert.ErrorIs(t, err, ErrInvalidTransition, "an ended event cannot pause")
}

func TestTransitionUnknownEvent(t *testing.T) {
	svc, _ := newEventService(t)
	_, err := svc.TransitionEvent(context.Background(), testutil.RandomID("nope"), ActionStart)
	assert.ErrorIs(t, err, ErrEventNotFound)
}

func TestEventHandlers(t *testing.T) {
	svc, eventID := newEventService(t)
	r := chi.NewRouter()
	h := NewHandler(svc)
	r.Get("/events/{id}", h.GetEvent)
	r.Post("/events/{id}/{action}", h.HandleLifecycleAction)

	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	rec := do(http.MethodGet, "/events/"+eventID)
	require.Equal(t, http.StatusOK, rec.Code)
	var ev EventDetails
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ev))
	assert.Equal(t, eventID, ev.ID)

	assert.Equal(t, http.StatusNotFound, do(http.MethodGet, "/events/"+testutil.RandomID("missing")).Code)

	rec = do(http.MethodPost, "/events/"+eventID+"/start")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, http.StatusBadRequest, do(http.MethodPost, "/events/"+eventID+"/explode").Code)
	assert.Equal(t, http.StatusConflict, do(http.MethodPost, "/events/"+eventID+"/start").Code, "starting a live event conflicts")
	assert.Equal(t, http.StatusNotFound, do(http.MethodPost, "/events/"+testutil.RandomID("x")+"/start").Code)
}

func TestLiveForFormatting(t *testing.T) {
	now := time.Now()
	started := now.Add(-time.Hour - 2*time.Minute - 3*time.Second)
	sec, formatted := ComputeLiveFor(StatusLive, &started, nil, 0, now)
	assert.InDelta(t, 3723, sec, 2)
	assert.Equal(t, "01:02:03", formatted)
}
