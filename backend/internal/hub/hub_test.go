package hub

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/crowdcast/crowdcast/internal/presence"
	"github.com/crowdcast/crowdcast/internal/testutil"
)

// TestMain fails the package if any goroutine outlives its test. Hub rooms,
// presence tickers and Redis subscriptions must all stop when the hub is closed.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// go-redis keeps a pool-maintenance goroutine per client. It exits on Close,
		// but goleak may still see it briefly, so it is named here explicitly.
		goleak.IgnoreTopFunction("github.com/redis/go-redis/v9/internal/pool.(*ConnPool).reaper"),
	)
}

// mockClient is a thread-safe Client that records what it was sent.
type mockClient struct {
	id      string
	eventID string
	mu      sync.Mutex
	msgs    [][]byte
	closed  bool
	code    int
}

func (m *mockClient) ID() string      { return m.id }
func (m *mockClient) EventID() string { return m.eventID }

func (m *mockClient) Send(msg []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
	return true
}

func (m *mockClient) Close(code int, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.code = code
}

func (m *mockClient) received() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.msgs))
	copy(out, m.msgs)
	return out
}

func (m *mockClient) isClosed() (bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed, m.code
}

// slowClient always reports a full buffer, which makes the room drop it.
type slowClient struct {
	id      string
	eventID string
	mu      sync.Mutex
	closed  bool
}

func (s *slowClient) ID() string      { return s.id }
func (s *slowClient) EventID() string { return s.eventID }
func (s *slowClient) Send([]byte) bool {
	return false
}
func (s *slowClient) Close(int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}
func (s *slowClient) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// eventually polls cond until it holds or the deadline passes.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, 2*time.Second, 5*time.Millisecond, msg)
}

// newRealHub builds a hub on the test Redis. The hub is closed on cleanup.
func newRealHub(t *testing.T) *Hub {
	t.Helper()
	rdb := testutil.Redis(t)
	pres := presence.NewService(rdb, nil, nil)
	h := NewHub(rdb, pres)
	t.Cleanup(func() {
		h.Close()
		pres.Close()
	})
	return h
}

func TestRoom_LocalBroadcast(t *testing.T) {
	r := newRoom("test-event", nil, nil)
	go r.run()
	defer r.Stop()

	c1 := &mockClient{id: "user-1", eventID: "test-event"}
	c2 := &mockClient{id: "user-2", eventID: "test-event"}
	r.register <- c1
	r.register <- c2
	eventually(t, func() bool { return r.ClientCount() == 2 }, "both clients registered")

	payload := []byte(`{"t":"chat","text":"hello"}`)
	r.broadcast <- payload
	eventually(t, func() bool { return len(c1.received()) == 1 && len(c2.received()) == 1 }, "both clients got the frame")
	assert.Equal(t, payload, c1.received()[0])
	assert.Equal(t, payload, c2.received()[0])
}

func TestRoom_SlowConsumerDrop(t *testing.T) {
	r := newRoom("test-event-slow", nil, nil)
	go r.run()
	defer r.Stop()

	slow := &slowClient{id: "slow-user", eventID: "test-event-slow"}
	r.register <- slow
	eventually(t, func() bool { return r.ClientCount() == 1 }, "slow client registered")

	r.broadcast <- []byte(`{"t":"ping"}`)
	eventually(t, slow.isClosed, "slow consumer closed")
	eventually(t, func() bool { return r.ClientCount() == 0 }, "slow consumer removed from the room")
}

func TestRoom_Unregister(t *testing.T) {
	r := newRoom("test-event-unreg", nil, nil)
	go r.run()
	defer r.Stop()

	c := &mockClient{id: "user-3", eventID: "test-event-unreg"}
	r.register <- c
	eventually(t, func() bool { return r.ClientCount() == 1 }, "registered")

	r.unregister <- c
	eventually(t, func() bool { return r.ClientCount() == 0 }, "unregistered")
}

func TestRoom_BroadcastDirectDropsWhenFull(t *testing.T) {
	r := newRoom("full", nil, nil) // not started: the broadcast channel fills up and nothing drains it
	for i := 0; i < cap(r.broadcast); i++ {
		r.broadcast <- []byte("x")
	}
	done := make(chan struct{})
	go func() {
		r.BroadcastDirect([]byte("overflow")) // must not block when full
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("BroadcastDirect blocked on a full channel")
	}
}

func TestRoom_StopIsIdempotent(t *testing.T) {
	r := newRoom("stop", nil, nil)
	go r.run()
	r.Stop()
	r.Stop()
	assert.True(t, r.stopped)
}

func TestHub_RegisterRoutesRedisMessagesToClients(t *testing.T) {
	h := newRealHub(t)
	eventID := testutil.RandomID("hub")

	c := &mockClient{id: "viewer", eventID: eventID}
	h.Register(c)
	eventually(t, func() bool { return h.TotalClients() == 1 }, "client registered in its room")

	// Redis pub/sub delivers the frame to every subscribed room, including this one.
	require.NoError(t, h.PublishEventMessage(context.Background(), eventID, map[string]string{"t": "chat", "text": "via redis"}))
	eventually(t, func() bool { return len(c.received()) > 0 }, "frame delivered through redis")

	var got map[string]any
	require.NoError(t, json.Unmarshal(c.received()[0], &got))
	assert.Equal(t, "chat", got["t"])
	assert.Contains(t, got, "_ts", "published frames carry the pub/sub timestamp")
}

func TestHub_UnregisterClosesEmptyRoomAfterDelay(t *testing.T) {
	setEmptyRoomDelay(30 * time.Millisecond)
	t.Cleanup(func() { setEmptyRoomDelay(10 * time.Second) })

	h := newRealHub(t)
	eventID := testutil.RandomID("empty")
	c := &mockClient{id: "viewer", eventID: eventID}
	h.Register(c)
	eventually(t, func() bool { return h.TotalClients() == 1 }, "registered")

	h.Unregister(c)
	eventually(t, func() bool {
		h.roomsMu.RLock()
		defer h.roomsMu.RUnlock()
		_, ok := h.rooms[eventID]
		return !ok
	}, "empty room removed after the delay")
}

func TestHub_CloseAllClosesSocketsWithCode(t *testing.T) {
	h := newRealHub(t)
	eventID := testutil.RandomID("closeall")
	c := &mockClient{id: "viewer", eventID: eventID}
	h.Register(c)
	eventually(t, func() bool { return h.TotalClients() == 1 }, "registered")

	h.CloseAll(1012, "service restarting")
	closed, code := c.isClosed()
	assert.True(t, closed)
	assert.Equal(t, 1012, code)
	assert.Equal(t, 1, h.TotalClients(), "CloseAll keeps rooms running; the socket owner unregisters the client")
}

func TestHub_DrainStopsRooms(t *testing.T) {
	h := newRealHub(t)
	c := &mockClient{id: "viewer", eventID: testutil.RandomID("drain")}
	h.Register(c)
	eventually(t, func() bool { return h.TotalClients() == 1 }, "registered")

	h.Drain(100 * time.Millisecond)
	closed, code := c.isClosed()
	assert.True(t, closed)
	assert.Equal(t, 1012, code)
}

func TestHub_PresenceTickReachesClients(t *testing.T) {
	h := newRealHub(t)
	eventID := testutil.RandomID("presence")
	c := &mockClient{id: "viewer", eventID: eventID}
	h.Register(c)
	eventually(t, func() bool { return h.TotalClients() == 1 }, "registered")

	h.broadcastPresenceTicks()
	eventually(t, func() bool { return len(c.received()) > 0 }, "presence frame delivered")
	assert.True(t, strings.Contains(string(c.received()[0]), `"attendees"`))
}

func TestHub_CloseIsIdempotent(t *testing.T) {
	rdb := testutil.Redis(t)
	pres := presence.NewService(rdb, nil, nil)
	t.Cleanup(pres.Close)
	h := NewHub(rdb, pres)
	h.Close()
	h.Close()
	assert.Equal(t, 0, h.TotalClients())
}

// newRealHubForBench is newRealHub for benchmarks, which have no *testing.T cleanup of their own.
func newRealHubForBench(b *testing.B) *Hub {
	b.Helper()
	rdb := testutil.Redis(b)
	pres := presence.NewService(rdb, nil, nil)
	h := NewHub(rdb, pres)
	b.Cleanup(func() {
		h.Close()
		pres.Close()
	})
	return h
}
