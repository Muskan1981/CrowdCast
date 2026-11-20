package degraded

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func item(id string) Item { return Item{ID: id, Kind: "chat", EventID: "cc-0826", Payload: []byte(id)} }

func TestBufferIsBoundedAndDropsOldest(t *testing.T) {
	b := NewBuffer(3)
	for i := 1; i <= 5; i++ {
		b.Push(item(fmt.Sprint(i)))
	}
	assert.Equal(t, 3, b.Len(), "the buffer never grows past its bound")
	assert.Equal(t, int64(2), b.Dropped())

	ids := []string{}
	for _, it := range b.Batch(10) {
		ids = append(ids, it.ID)
	}
	assert.Equal(t, []string{"3", "4", "5"}, ids, "the newest messages survive, oldest are dropped")
}

func TestBufferPushReportsDrop(t *testing.T) {
	b := NewBuffer(1)
	assert.False(t, b.Push(item("a")))
	assert.True(t, b.Push(item("b")))
}

func TestBufferAckRemovesOnlyThatItem(t *testing.T) {
	b := NewBuffer(10)
	b.Push(item("a"))
	b.Push(item("b"))
	b.Push(item("c"))
	b.Ack("b")
	assert.Equal(t, 2, b.Len())
	got := b.Batch(10)
	assert.Equal(t, "a", got[0].ID)
	assert.Equal(t, "c", got[1].ID)
	b.Ack("missing") // acking an unknown ID is harmless
	assert.Equal(t, 2, b.Len())
}

func TestBufferZeroBoundBecomesOne(t *testing.T) {
	b := NewBuffer(0)
	b.Push(item("a"))
	b.Push(item("b"))
	assert.Equal(t, 1, b.Len())
}

// fakeRedis is an in-memory idempotency store with a failure switch.
type fakeRedis struct {
	mu   sync.Mutex
	keys map[string]bool
	down bool
}

func (f *fakeRedis) Claim(_ context.Context, key string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return false, errors.New("redis down")
	}
	if f.keys[key] {
		return false, nil
	}
	f.keys[key] = true
	return true, nil
}

func (f *fakeRedis) Release(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.keys, key)
	return nil
}

func TestApplyOnceRunsOnlyOnce(t *testing.T) {
	r := &fakeRedis{keys: map[string]bool{}}
	calls := 0
	fn := func(context.Context) error { calls++; return nil }

	require.NoError(t, ApplyOnce(context.Background(), r, "msg-1", fn))
	require.NoError(t, ApplyOnce(context.Background(), r, "msg-1", fn))
	assert.Equal(t, 1, calls, "a replayed message must not be applied twice")
}

func TestApplyOnceReleasesClaimOnFailure(t *testing.T) {
	r := &fakeRedis{keys: map[string]bool{}}
	fails := true
	fn := func(context.Context) error {
		if fails {
			return errors.New("write failed")
		}
		return nil
	}

	assert.Error(t, ApplyOnce(context.Background(), r, "msg-2", fn))
	fails = false
	assert.NoError(t, ApplyOnce(context.Background(), r, "msg-2", fn), "a failed apply must be retryable")
}

func TestFlusherReplaysInOrderAndDrains(t *testing.T) {
	buf := NewBuffer(100)
	for i := 0; i < 5; i++ {
		buf.Push(item(fmt.Sprint(i)))
	}

	var mu sync.Mutex
	var applied []string
	apply := func(_ context.Context, it Item) error {
		mu.Lock()
		defer mu.Unlock()
		applied = append(applied, it.ID)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := NewFlusher(buf, apply, func() bool { return true }, 5*time.Millisecond)
	go f.Run(ctx)

	require.Eventually(t, func() bool { return buf.Len() == 0 }, time.Second, 5*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"0", "1", "2", "3", "4"}, applied)
}

func TestFlusherWaitsWhileRedisIsDown(t *testing.T) {
	buf := NewBuffer(10)
	buf.Push(item("a"))

	var mu sync.Mutex
	calls := 0
	apply := func(context.Context, Item) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return nil
	}
	healthy := false
	var hmu sync.Mutex
	isHealthy := func() bool { hmu.Lock(); defer hmu.Unlock(); return healthy }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewFlusher(buf, apply, isHealthy, 5*time.Millisecond).Run(ctx)

	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 1, buf.Len(), "nothing is removed while Redis is down")

	hmu.Lock()
	healthy = true
	hmu.Unlock()
	require.Eventually(t, func() bool { return buf.Len() == 0 }, time.Second, 5*time.Millisecond)
}

func TestFlusherKeepsItemOnFailure(t *testing.T) {
	buf := NewBuffer(10)
	buf.Push(item("a"))
	buf.Push(item("b"))

	fail := true
	apply := func(_ context.Context, it Item) error {
		if it.ID == "b" && fail {
			return errors.New("transient")
		}
		return nil
	}
	f := NewFlusher(buf, apply, func() bool { return true }, time.Millisecond)

	assert.False(t, f.flushOnce(context.Background()))
	assert.Equal(t, 1, buf.Len(), "a failed item stays buffered, earlier items were acked")
	assert.Equal(t, "b", buf.Batch(1)[0].ID)

	fail = false
	assert.True(t, f.flushOnce(context.Background()))
	assert.Equal(t, 0, buf.Len())
}

func TestModeTracksReachability(t *testing.T) {
	var m Mode
	assert.False(t, m.IsDegraded())
	m.Set(true)
	assert.True(t, m.IsDegraded())
	m.Set(false)
	assert.False(t, m.IsDegraded())
}
