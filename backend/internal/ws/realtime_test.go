package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/crowdcast/crowdcast/internal/chat"
	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/polls"
	"github.com/crowdcast/crowdcast/internal/presence"
	"github.com/crowdcast/crowdcast/internal/reactions"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	"github.com/crowdcast/crowdcast/internal/testutil"
)

// TestMain fails the package if a WebSocket, room or service goroutine outlives its test.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("github.com/redis/go-redis/v9/internal/pool.(*ConnPool).reaper"),
		// httptest servers keep an idle keep-alive goroutine until they are closed.
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
}

// rig is a complete realtime stack on the test Redis (and Postgres for polls).
type rig struct {
	server *httptest.Server
	tokens *auth.TokenService
	hub    *hub.Hub
	polls  *polls.Service
	rx     *reactions.Service
}

// newRig starts the WebSocket handler behind httptest. Everything is closed on cleanup.
func newRig(t *testing.T, maxConns int64) *rig {
	t.Helper()
	rdb := testutil.Redis(t)
	pool := testutil.Postgres(t)

	pres := presence.NewService(rdb, nil, nil)
	h := hub.NewHub(rdb, pres)
	chatSvc := chat.NewService(rdb, postgres.NewChatRepository(pool), h, 0)
	rx := reactions.NewService(rdb, postgres.NewReactionRepository(pool), h, 20*time.Millisecond)
	pollSvc := polls.NewService(rdb, postgres.NewPollRepository(pool), h)
	tokens := auth.NewTokenService("test-secret-key-1234567890123456")

	handler := NewHandler(tokens, h, pres, nil, chatSvc, rx, pollSvc, maxConns, 0, "api-test")
	server := httptest.NewServer(handler)

	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
		h.Close()
		rx.Close()
		chatSvc.Close()
		pres.Close()
	})
	return &rig{server: server, tokens: tokens, hub: h, polls: pollSvc, rx: rx}
}

// dial opens a WebSocket as a fresh viewer for eventID and consumes the welcome frame.
func (r *rig) dial(t *testing.T, eventID, name string) (*websocket.Conn, map[string]any) {
	t.Helper()
	token, err := r.tokens.IssueViewerToken(testutil.RandomID("u"), name, "Mumbai", "mint")
	require.NoError(t, err)
	url := "ws" + strings.TrimPrefix(r.server.URL, "http") + "/ws?event=" + eventID + "&token=" + token
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	welcome := read(t, conn)
	require.Equal(t, "welcome", welcome["t"])
	return conn, welcome
}

// read returns the next frame or fails the test after 5 seconds.
func read(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	var frame map[string]any
	require.NoError(t, json.Unmarshal(data, &frame))
	return frame
}

// readUntil reads frames until one with type typ arrives.
func readUntil(t *testing.T, conn *websocket.Conn, typ string) map[string]any {
	t.Helper()
	for i := 0; i < 50; i++ {
		frame := read(t, conn)
		if frame["t"] == typ {
			return frame
		}
	}
	t.Fatalf("no %q frame arrived", typ)
	return nil
}

func send(t *testing.T, conn *websocket.Conn, frame map[string]any) {
	t.Helper()
	data, err := json.Marshal(frame)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, data))
}

func TestRealtime_ChatReachesEveryViewerOnTheEvent(t *testing.T) {
	r := newRig(t, 100)
	eventID := testutil.RandomID("ev")
	alice, _ := r.dial(t, eventID, "Alice")
	bob, _ := r.dial(t, eventID, "Bob")

	send(t, alice, map[string]any{"t": "chat", "text": "hello room"})

	for _, conn := range []*websocket.Conn{alice, bob} {
		frame := readUntil(t, conn, "chat")
		msg := frame["message"].(map[string]any)
		assert.Equal(t, "hello room", msg["text"])
		assert.Equal(t, "Alice", msg["name"])
	}
}

func TestRealtime_ReactionsArriveAsBatches(t *testing.T) {
	r := newRig(t, 100)
	eventID := testutil.RandomID("ev")
	alice, _ := r.dial(t, eventID, "Alice")
	bob, _ := r.dial(t, eventID, "Bob")

	send(t, alice, map[string]any{"t": "reaction", "emoji": "🔥"})
	send(t, bob, map[string]any{"t": "reaction", "emoji": "👏"})

	batch := readUntil(t, alice, "reaction_batch")
	reactions := batch["reactions"].(map[string]any)
	assert.GreaterOrEqual(t, reactions["🔥"].(float64)+reactions["👏"].(float64), 1.0)
}

func TestRealtime_PollVoteBroadcastsUpdate(t *testing.T) {
	r := newRig(t, 100)
	pool := testutil.Postgres(t)
	eventID := testutil.Event(t, pool)

	ctx := context.Background()
	poll, err := r.polls.CreatePoll(ctx, eventID, "Energy?", []string{"High", "Low"})
	require.NoError(t, err)
	require.NoError(t, r.polls.ActivatePoll(ctx, poll.ID))

	alice, welcome := r.dial(t, eventID, "Alice")
	bob, _ := r.dial(t, eventID, "Bob")
	require.NotNil(t, welcome["active_poll"], "the welcome frame carries the active poll")

	send(t, alice, map[string]any{"t": "vote", "poll": poll.ID, "option": 0})

	update := readUntil(t, bob, "poll_update")
	assert.Equal(t, poll.ID, update["poll_id"])
	assert.EqualValues(t, 1, update["total_votes"])
}

func TestRealtime_MalformedFrameGetsErrorNotDisconnect(t *testing.T) {
	r := newRig(t, 100)
	conn, _ := r.dial(t, testutil.RandomID("ev"), "Alice")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte("not json")))
	frame := readUntil(t, conn, "error")
	assert.Equal(t, "INVALID_FORMAT", frame["code"])

	// The socket is still usable after the error.
	send(t, conn, map[string]any{"t": "ping"})
	readUntil(t, conn, "pong")
}

func TestRealtime_PingGetsPong(t *testing.T) {
	r := newRig(t, 100)
	conn, _ := r.dial(t, testutil.RandomID("ev"), "Alice")
	send(t, conn, map[string]any{"t": "ping"})
	pong := readUntil(t, conn, "pong")
	assert.NotNil(t, pong["timestamp"])
}

func TestRealtime_DisconnectRemovesClientFromRoom(t *testing.T) {
	r := newRig(t, 100)
	eventID := testutil.RandomID("ev")
	conn, _ := r.dial(t, eventID, "Alice")
	require.Eventually(t, func() bool { return r.hub.TotalClients() == 1 }, 2*time.Second, 5*time.Millisecond)

	_ = conn.Close(websocket.StatusNormalClosure, "bye")
	require.Eventually(t, func() bool { return r.hub.TotalClients() == 0 }, 2*time.Second, 5*time.Millisecond)
}

func TestRealtime_ConnectionLimitReturns503(t *testing.T) {
	r := newRig(t, 1)
	eventID := testutil.RandomID("ev")
	first, _ := r.dial(t, eventID, "Alice")
	_ = first

	token, err := r.tokens.IssueViewerToken("u-2", "Bob", "Mumbai", "mint")
	require.NoError(t, err)
	resp, err := http.Get(r.server.URL + "/ws?event=" + eventID + "&token=" + token)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestRealtime_ServerCloseFramesReachClients(t *testing.T) {
	r := newRig(t, 100)
	conn, _ := r.dial(t, testutil.RandomID("ev"), "Alice")
	require.Eventually(t, func() bool { return r.hub.TotalClients() == 1 }, 2*time.Second, 5*time.Millisecond)

	r.hub.CloseAll(1012, "service restarting")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	require.Error(t, err)
	assert.EqualValues(t, 1012, websocket.CloseStatus(err))
}
