package stats

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// LockStore is the small part of Redis that leader election needs. Keeping it
// an interface lets the tests use an in-memory fake with a controllable clock.
type LockStore interface {
	// Acquire sets key=owner with the given TTL only if key does not exist.
	Acquire(ctx context.Context, key, owner string, ttl time.Duration) (bool, error)
	// Renew extends key's TTL only if key still equals owner.
	Renew(ctx context.Context, key, owner string, ttl time.Duration) (bool, error)
}

// renewScript extends the TTL only when this owner still holds the lock, so an
// instance that lost the lock cannot extend someone else's lease.
var renewScript = redis.NewScript(`
	if redis.call('GET', KEYS[1]) == ARGV[1] then
		return redis.call('PEXPIRE', KEYS[1], ARGV[2])
	end
	return 0
`)

// RedisLockStore implements LockStore with SET NX PX and a Lua renewal.
type RedisLockStore struct {
	rdb *redis.Client
}

// NewRedisLockStore wraps a go-redis client.
func NewRedisLockStore(rdb *redis.Client) *RedisLockStore {
	return &RedisLockStore{rdb: rdb}
}

func (s *RedisLockStore) Acquire(ctx context.Context, key, owner string, ttl time.Duration) (bool, error) {
	return s.rdb.SetNX(ctx, key, owner, ttl).Result()
}

func (s *RedisLockStore) Renew(ctx context.Context, key, owner string, ttl time.Duration) (bool, error) {
	n, err := renewScript.Run(ctx, s.rdb, []string{key}, owner, ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Elector runs a function only on the instance that holds a named lock. If the
// leader crashes it stops renewing, the key expires after ttl, and another
// instance acquires it on its next attempt.
type Elector struct {
	store      LockStore
	key        string
	owner      string
	ttl        time.Duration
	renewEvery time.Duration
}

// NewElector configures election. renewEvery must be well below ttl (a third is
// typical) so a single missed renewal does not cost leadership.
func NewElector(store LockStore, key, owner string, ttl, renewEvery time.Duration) *Elector {
	return &Elector{store: store, key: key, owner: owner, ttl: ttl, renewEvery: renewEvery}
}

// Run blocks until ctx is cancelled. Whenever this instance becomes leader it
// calls lead with a context that is cancelled as soon as leadership is lost.
// On a transient Redis error the leader steps down rather than risk two leaders.
func (e *Elector) Run(ctx context.Context, lead func(ctx context.Context)) {
	for {
		if ctx.Err() != nil {
			return
		}
		ok, err := e.store.Acquire(ctx, e.key, e.owner, e.ttl)
		if err != nil {
			slog.Warn("leader election: acquire failed", "key", e.key, "error", err)
		}
		if err == nil && ok {
			slog.Info("leader acquired", "key", e.key, "owner", e.owner)
			e.lead(ctx, lead)
			slog.Info("leader released", "key", e.key, "owner", e.owner)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(e.renewEvery):
		}
	}
}

// lead runs the leader function until leadership is lost, the function returns
// on its own, or ctx is cancelled. The lock is deliberately NOT deleted on exit:
// a crashed instance cannot release it either, so expiry handles both cases.
func (e *Elector) lead(ctx context.Context, fn func(ctx context.Context)) {
	leadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(leadCtx)
	}()

	ticker := time.NewTicker(e.renewEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return
		case <-done:
			return
		case <-ticker.C:
			ok, err := e.store.Renew(ctx, e.key, e.owner, e.ttl)
			if err != nil {
				slog.Warn("leader election: renew failed, stepping down", "key", e.key, "error", err)
				cancel()
				<-done
				return
			}
			if !ok {
				slog.Warn("leader election: lock lost to another owner", "key", e.key)
				cancel()
				<-done
				return
			}
		}
	}
}
