package polls

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	"github.com/crowdcast/crowdcast/internal/testutil"
)

// newDBService wires the poll service to the test Redis and Postgres. Hub is nil,
// so broadcasts are skipped; the broadcast path is covered by the ws realtime tests.
func newDBService(t *testing.T) (*Service, string) {
	t.Helper()
	rdb := testutil.Redis(t)
	pool := testutil.Postgres(t)
	eventID := testutil.Event(t, pool)
	return NewService(rdb, postgres.NewPollRepository(pool), nil), eventID
}

func TestPollLifecycleAndVoting(t *testing.T) {
	svc, eventID := newDBService(t)
	ctx := context.Background()

	poll, err := svc.CreatePoll(ctx, eventID, "Energy?", []string{"High", "Medium", "Low"})
	require.NoError(t, err)
	assert.Equal(t, "scheduled", poll.Status, "a new poll starts scheduled until it is activated")
	require.NoError(t, svc.ActivatePoll(ctx, poll.ID))

	active, err := svc.GetActivePoll(ctx, eventID)
	require.NoError(t, err)
	assert.Equal(t, poll.ID, active.ID)

	v1, err := svc.Vote(ctx, poll.ID, "user-1", 0)
	require.NoError(t, err)
	assert.Equal(t, 1, v1.TotalVotes)
	assert.Equal(t, []int{1, 0, 0}, v1.Votes)
	assert.Equal(t, []int{100, 0, 0}, v1.Percentages)

	_, err = svc.Vote(ctx, poll.ID, "user-1", 1)
	assert.ErrorIs(t, err, ErrAlreadyVoted, "one vote per user")

	_, err = svc.Vote(ctx, poll.ID, "user-2", 9)
	assert.ErrorIs(t, err, ErrInvalidOption)

	v3, err := svc.Vote(ctx, poll.ID, "user-3", 2)
	require.NoError(t, err)
	assert.Equal(t, 2, v3.TotalVotes)

	require.NoError(t, svc.ClosePoll(ctx, poll.ID))
	_, err = svc.Vote(ctx, poll.ID, "user-4", 0)
	assert.ErrorIs(t, err, ErrPollClosed)
}

func TestPollNotFound(t *testing.T) {
	svc, _ := newDBService(t)
	_, err := svc.GetPoll(context.Background(), "poll-does-not-exist")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGetActivePollWithoutActivePoll(t *testing.T) {
	svc, eventID := newDBService(t)
	_, err := svc.GetActivePoll(context.Background(), eventID)
	assert.Error(t, err, "an event with no active poll reports not found")
}

func TestActivatingSecondPollClosesOrRejectsFirst(t *testing.T) {
	svc, eventID := newDBService(t)
	ctx := context.Background()
	first, err := svc.CreatePoll(ctx, eventID, "First?", []string{"a", "b"})
	require.NoError(t, err)
	require.NoError(t, svc.ActivatePoll(ctx, first.ID))

	second, err := svc.CreatePoll(ctx, eventID, "Second?", []string{"a", "b"})
	require.NoError(t, err)
	require.NoError(t, svc.ActivatePoll(ctx, second.ID))

	active, err := svc.GetActivePoll(ctx, eventID)
	require.NoError(t, err)
	assert.Equal(t, second.ID, active.ID)
}

func TestPollHandlers(t *testing.T) {
	svc, eventID := newDBService(t)
	h := NewHandler(svc)
	r := chi.NewRouter()
	r.Get("/events/{id}/polls/active", h.GetActivePoll)
	r.Post("/events/{id}/polls", h.AdminCreatePoll)
	r.Post("/polls/{pid}/activate", h.AdminActivatePoll)
	r.Post("/polls/{pid}/close", h.AdminClosePoll)
	r.Post("/polls/{pid}/vote", h.CastVote)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(auth.WithViewerClaims(req.Context(), &auth.ViewerClaims{UserID: "viewer-1", DisplayName: "V"}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := do(http.MethodPost, "/events/"+eventID+"/polls", `{"question":"Q?","options":["a","b"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created PollResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	require.Equal(t, http.StatusOK, do(http.MethodPost, "/polls/"+created.ID+"/activate", "").Code)
	require.Equal(t, http.StatusOK, do(http.MethodGet, "/events/"+eventID+"/polls/active", "").Code)

	rec = do(http.MethodPost, "/polls/"+created.ID+"/vote", `{"option":1}`)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, http.StatusConflict, do(http.MethodPost, "/polls/"+created.ID+"/vote", `{"option":0}`).Code)
	assert.Equal(t, http.StatusBadRequest, do(http.MethodPost, "/polls/"+created.ID+"/vote", `not json`).Code)
	assert.Equal(t, http.StatusBadRequest, do(http.MethodPost, "/polls/"+created.ID+"/vote", `{"option":7}`).Code)

	require.Equal(t, http.StatusOK, do(http.MethodPost, "/polls/"+created.ID+"/close", "").Code)
	assert.Equal(t, http.StatusNotFound, do(http.MethodPost, "/polls/poll-missing/close", "").Code)
}
