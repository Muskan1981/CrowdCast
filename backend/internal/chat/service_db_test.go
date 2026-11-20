package chat

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

// newChatService wires chat to the test Redis and Postgres. The rate limit is long
// so tests control it, and the service is closed on cleanup.
func newChatService(t *testing.T, rateLimit time.Duration) (*Service, *postgres.ChatRepository, string) {
	t.Helper()
	rdb := testutil.Redis(t)
	pool := testutil.Postgres(t)
	eventID := testutil.Event(t, pool)
	repo := postgres.NewChatRepository(pool)
	svc := NewService(rdb, repo, nil, rateLimit)
	t.Cleanup(svc.Close)
	return svc, nil, eventID
}

func TestPostAndHistory(t *testing.T) {
	svc, _, eventID := newChatService(t, time.Millisecond)
	ctx := context.Background()
	user := testutil.RandomID("u")

	msg, err := svc.PostMessage(ctx, eventID, user, "Aarav", "  the visuals are great  ")
	require.NoError(t, err)
	assert.Equal(t, "the visuals are great", msg.Text, "text is trimmed")
	assert.Equal(t, "Aarav", msg.Name)
	assert.NotEmpty(t, msg.Color)

	hist, err := svc.GetHistory(ctx, eventID, 10)
	require.NoError(t, err)
	require.Len(t, hist.Messages, 1)
	assert.Equal(t, msg.Text, hist.Messages[0].Text)
	assert.Equal(t, int64(1), hist.TotalMessages)

	hist, err = svc.GetHistory(ctx, eventID, 0)
	require.NoError(t, err, "a zero limit falls back to the default")
	assert.Len(t, hist.Messages, 1)
}

func TestPostValidation(t *testing.T) {
	svc, _, eventID := newChatService(t, time.Millisecond)
	ctx := context.Background()

	_, err := svc.PostMessage(ctx, eventID, testutil.RandomID("u"), "A", "   ")
	assert.ErrorIs(t, err, ErrEmptyMessage)

	_, err = svc.PostMessage(ctx, eventID, testutil.RandomID("u"), "A", strings.Repeat("x", 281))
	assert.ErrorIs(t, err, ErrMessageTooLong)

	for _, word := range defaultBannedWords {
		_, err = svc.PostMessage(ctx, eventID, testutil.RandomID("u"), "A", "this has "+word+" in it")
		assert.ErrorIs(t, err, ErrProfanitySpam)
		break
	}
}

func TestDuplicateAndRateLimit(t *testing.T) {
	svc, _, eventID := newChatService(t, time.Hour)
	ctx := context.Background()
	user := testutil.RandomID("u")

	_, err := svc.PostMessage(ctx, eventID, user, "A", "same text")
	require.NoError(t, err)
	_, err = svc.PostMessage(ctx, eventID, user, "A", "same text")
	assert.ErrorIs(t, err, ErrDuplicateMessage)

	// The limiter allows a burst of 3, so the fourth distinct message is limited.
	var limited error
	for i := 0; i < 5; i++ {
		_, limited = svc.PostMessage(ctx, eventID, user, "A", "message number "+string(rune('a'+i)))
		if limited != nil {
			break
		}
	}
	assert.ErrorIs(t, limited, ErrRateLimited)
}

func TestMessagesArePersistedToPostgres(t *testing.T) {
	rdb := testutil.Redis(t)
	pool := testutil.Postgres(t)
	eventID := testutil.Event(t, pool)
	repo := postgres.NewChatRepository(pool)
	svc := NewService(rdb, repo, nil, time.Millisecond)
	t.Cleanup(svc.Close)

	_, err := svc.PostMessage(context.Background(), eventID, testutil.RandomID("u"), "Ishita", "persist me")
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		recent, err := repo.ListRecent(context.Background(), eventID, 10)
		return err == nil && len(recent) == 1
	}, 6*time.Second, 100*time.Millisecond, "the batch persister writes the message to Postgres")
}

func TestChatHandlers(t *testing.T) {
	svc, _, eventID := newChatService(t, time.Hour)
	r := chi.NewRouter()
	h := NewHandler(svc)
	r.Get("/events/{id}/chat", h.GetHistory)
	r.Post("/events/{id}/chat", h.SendMessage)

	user := testutil.RandomID("viewer")
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/events/"+eventID+"/chat", strings.NewReader(body))
		req = req.WithContext(auth.WithViewerClaims(req.Context(), &auth.ViewerClaims{UserID: user, DisplayName: "Dev"}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"text":"hello from the handler"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created ChatMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "Dev", created.Name)

	assert.Equal(t, http.StatusBadRequest, post(`not json`).Code)
	assert.Equal(t, http.StatusBadRequest, post(`{"text":""}`).Code)
	assert.Equal(t, http.StatusBadRequest, post(`{"text":"`+strings.Repeat("y", 300)+`"}`).Code)

	get := httptest.NewRecorder()
	r.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/events/"+eventID+"/chat?limit=5", nil))
	require.Equal(t, http.StatusOK, get.Code)
	var hist ChatHistoryResponse
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &hist))
	assert.GreaterOrEqual(t, len(hist.Messages), 1)
}

func TestHandlerRejectsMissingEvent(t *testing.T) {
	svc, _, _ := newChatService(t, time.Hour)
	rec := httptest.NewRecorder()
	NewHandler(svc).SendMessage(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"text":"x"}`)))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
