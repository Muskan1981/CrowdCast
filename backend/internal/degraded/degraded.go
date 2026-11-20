// Package degraded keeps an instance serving when Redis is unreachable. Writes
// that cannot reach Redis go into a bounded in-memory buffer, and a flusher
// replays them, once Redis is back, with idempotency keys so a retry cannot
// apply the same message twice.
package degraded

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Item is one write that could not reach Redis.
type Item struct {
	ID      string // unique; also the idempotency key
	Kind    string // e.g. "chat", "reaction"
	EventID string
	Payload []byte
}

// Buffer is a bounded FIFO. When full, the oldest item is dropped, so memory
// stays bounded during a long outage and the newest messages survive.
type Buffer struct {
	mu      sync.Mutex
	max     int
	items   []Item
	dropped int64
}

// NewBuffer returns a buffer holding at most max items (max < 1 becomes 1).
func NewBuffer(max int) *Buffer {
	if max < 1 {
		max = 1
	}
	return &Buffer{max: max}
}

// Push appends an item and reports whether an older item had to be dropped.
func (b *Buffer) Push(it Item) (droppedOldest bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) >= b.max {
		b.items = b.items[1:]
		b.dropped++
		droppedOldest = true
	}
	b.items = append(b.items, it)
	return droppedOldest
}

// Batch returns up to n oldest items without removing them.
func (b *Buffer) Batch(n int) []Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > len(b.items) {
		n = len(b.items)
	}
	out := make([]Item, n)
	copy(out, b.items[:n])
	return out
}

// Ack removes the item with the given ID once it has been applied.
func (b *Buffer) Ack(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, it := range b.items {
		if it.ID == id {
			b.items = append(b.items[:i], b.items[i+1:]...)
			return
		}
	}
}

// Len is the number of buffered items.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}

// Dropped is how many items were discarded because the buffer was full.
func (b *Buffer) Dropped() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// Mode tracks whether Redis is currently reachable.
type Mode struct {
	degraded atomic.Bool
}

// IsDegraded reports whether the instance is in degraded mode.
func (m *Mode) IsDegraded() bool { return m.degraded.Load() }

// Set records the current Redis reachability.
func (m *Mode) Set(degraded bool) { m.degraded.Store(degraded) }

// Watch pings Redis every interval and updates the mode. It returns when ctx is cancelled.
func (m *Mode) Watch(ctx context.Context, ping func(context.Context) error, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		pctx, cancel := context.WithTimeout(ctx, interval)
		m.Set(ping(pctx) != nil)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Claimer is the idempotency store, normally Redis SET NX with a TTL.
type Claimer interface {
	// Claim sets key if absent and reports whether this caller set it.
	Claim(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// Release deletes key, used when the apply failed and a retry must be allowed.
	Release(ctx context.Context, key string) error
}

// DedupeTTL is how long an applied item's idempotency key is remembered.
const DedupeTTL = time.Hour

// ApplyOnce runs fn for id at most once. If fn fails the claim is released,
// so the flusher can retry. A lost acknowledgement after fn succeeded is the
// only case where a retry can run again, and then the claim blocks it.
func ApplyOnce(ctx context.Context, c Claimer, id string, fn func(context.Context) error) error {
	key := "dedupe:" + id
	claimed, err := c.Claim(ctx, key, DedupeTTL)
	if err != nil {
		return err
	}
	if !claimed {
		return nil // already applied
	}
	if err := fn(ctx); err != nil {
		_ = c.Release(ctx, key)
		return err
	}
	return nil
}

// Apply replays one buffered item against Redis.
type Apply func(ctx context.Context, it Item) error

// Flusher replays buffered items when Redis is healthy again.
type Flusher struct {
	buf      *Buffer
	apply    Apply
	healthy  func() bool
	interval time.Duration
	batch    int
	backoff  time.Duration
	maxBack  time.Duration
}

// NewFlusher builds a flusher. interval is the poll period; backoff grows up to maxBack after failures.
func NewFlusher(buf *Buffer, apply Apply, healthy func() bool, interval time.Duration) *Flusher {
	return &Flusher{
		buf: buf, apply: apply, healthy: healthy,
		interval: interval, batch: 100, backoff: interval, maxBack: 10 * time.Second,
	}
}

// Run flushes until ctx is cancelled. Items are removed only after they are
// applied, so a failure leaves them in place for the next attempt.
func (f *Flusher) Run(ctx context.Context) {
	wait := f.interval
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if f.buf.Len() == 0 || !f.healthy() {
			wait = f.interval
			continue
		}
		if f.flushOnce(ctx) {
			wait = f.interval
			continue
		}
		wait = f.backoff
		f.backoff = min(f.backoff*2, f.maxBack)
	}
}

// flushOnce applies one batch in order. It returns false if any item failed.
func (f *Flusher) flushOnce(ctx context.Context) bool {
	for _, it := range f.buf.Batch(f.batch) {
		if err := f.apply(ctx, it); err != nil {
			return false
		}
		f.buf.Ack(it.ID)
	}
	f.backoff = f.interval
	return true
}

// RedisClaimer implements Claimer with SET NX and DEL.
type RedisClaimer struct {
	Client *redis.Client
}

func (r RedisClaimer) Claim(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return r.Client.SetNX(ctx, key, "1", ttl).Result()
}

func (r RedisClaimer) Release(ctx context.Context, key string) error {
	return r.Client.Del(ctx, key).Err()
}
