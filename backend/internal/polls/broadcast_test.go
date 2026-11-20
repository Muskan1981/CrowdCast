package polls

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	"github.com/crowdcast/crowdcast/internal/testutil"
)

// frameClient is a hub client that records every frame it is sent.
type frameClient struct {
	id, event string
	mu        sync.Mutex
	frames    []string
}

func (f *frameClient) ID() string      { return f.id }
func (f *frameClient) EventID() string { return f.event }
func (f *frameClient) Send(b []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, string(b))
	return true
}
func (f *frameClient) Close(int, string) {}
func (f *frameClient) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.frames)
}

func TestVoteBroadcastsPollUpdateToTheRoom(t *testing.T) {
	rdb := testutil.Redis(t)
	pool := testutil.Postgres(t)
	eventID := testutil.Event(t, pool)
	h := hub.NewHub(rdb, nil)
	t.Cleanup(h.Close)
	svc := NewService(rdb, postgres.NewPollRepository(pool), h)

	ctx := context.Background()
	poll, err := svc.CreatePoll(ctx, eventID, "Q?", []string{"a", "b"})
	require.NoError(t, err)
	require.NoError(t, svc.ActivatePoll(ctx, poll.ID))

	viewer := &frameClient{id: "viewer", event: eventID}
	h.Register(viewer)
	require.Eventually(t, func() bool { return h.TotalClients() == 1 }, 2*time.Second, 5*time.Millisecond)

	_, err = svc.Vote(ctx, poll.ID, "voter-1", 1)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return viewer.count() > 0 }, 2*time.Second, 10*time.Millisecond)
}

func TestVoteHandlerRejectsUnauthenticatedVoters(t *testing.T) {
	svc, _ := newDBService(t)
	r := chi.NewRouter()
	r.Post("/polls/{pid}/vote", NewHandler(svc).CastVote)

	req := httptest.NewRequest(http.MethodPost, "/polls/poll-x/vote", nil)
	req.Body = http.NoBody
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestActivePollHandlerReturnsNotFoundWithoutPoll(t *testing.T) {
	svc, eventID := newDBService(t)
	r := chi.NewRouter()
	r.Get("/events/{id}/polls/active", NewHandler(svc).GetActivePoll)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events/"+eventID+"/polls/active", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreatePollRequiresPostgres(t *testing.T) {
	svc := NewService(testutil.Redis(t), nil, nil)
	_, err := svc.CreatePoll(context.Background(), "ev", "q", []string{"a"})
	assert.Error(t, err)
	assert.Error(t, svc.ActivatePoll(context.Background(), "p"))
	assert.Error(t, svc.ClosePoll(context.Background(), "p"))
}
