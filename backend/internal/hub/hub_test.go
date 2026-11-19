package hub

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockClient struct {
	id      string
	eventID string
	msgs    [][]byte
	closed  bool
}

func (m *mockClient) ID() string {
	return m.id
}

func (m *mockClient) EventID() string {
	return m.eventID
}

func (m *mockClient) Send(msg []byte) bool {
	m.msgs = append(m.msgs, msg)
	return true
}

func (m *mockClient) Close(code int, reason string) {
	m.closed = true
}

type slowClient struct {
	id      string
	eventID string
	closed  bool
}

func (s *slowClient) ID() string {
	return s.id
}

func (s *slowClient) EventID() string {
	return s.eventID
}

func (s *slowClient) Send(msg []byte) bool {
	return false // always full (slow consumer)
}

func (s *slowClient) Close(code int, reason string) {
	s.closed = true
}

func TestRoom_LocalBroadcast(t *testing.T) {
	r := newRoom("test-event", nil, nil)
	go r.run()
	defer r.Stop()

	c1 := &mockClient{id: "user-1", eventID: "test-event"}
	c2 := &mockClient{id: "user-2", eventID: "test-event"}

	r.register <- c1
	r.register <- c2
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, 2, r.ClientCount())

	testPayload := []byte(`{"t":"chat","text":"hello"}`)
	r.broadcast <- testPayload
	time.Sleep(20 * time.Millisecond)

	assert.Len(t, c1.msgs, 1)
	assert.Len(t, c2.msgs, 1)
	assert.Equal(t, testPayload, c1.msgs[0])
	assert.Equal(t, testPayload, c2.msgs[0])
}

func TestRoom_SlowConsumerDrop(t *testing.T) {
	r := newRoom("test-event-slow", nil, nil)
	go r.run()
	defer r.Stop()

	slow := &slowClient{id: "slow-user", eventID: "test-event-slow"}
	r.register <- slow
	time.Sleep(20 * time.Millisecond)

	r.broadcast <- []byte(`{"t":"ping"}`)
	time.Sleep(50 * time.Millisecond)

	assert.True(t, slow.closed, "slow consumer should be closed")
}

func TestRoom_Unregister(t *testing.T) {
	r := newRoom("test-event-unreg", nil, nil)
	go r.run()
	defer r.Stop()

	c := &mockClient{id: "user-3", eventID: "test-event-unreg"}
	r.register <- c
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 1, r.ClientCount())

	r.unregister <- c
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 0, r.ClientCount())
}
