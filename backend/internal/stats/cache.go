package stats

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Cache is a small per-instance TTL cache combined with singleflight. When 5,000
// viewers poll the same endpoint, only one computation runs per key at a time,
// and its result is reused for ttl.
type Cache struct {
	mu    sync.Mutex
	items map[string]cacheEntry
	group singleflight.Group
	now   func() time.Time
}

type cacheEntry struct {
	value   any
	expires time.Time
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{items: make(map[string]cacheEntry), now: time.Now}
}

// Get returns the cached value for key if it is still fresh. Otherwise it runs
// load once (shared by concurrent callers) and caches the result for ttl.
// Errors are never cached, so a transient Prometheus failure does not stick.
func Get[T any](c *Cache, key string, ttl time.Duration, load func() (T, error)) (T, error) {
	if v, ok := c.lookup(key); ok {
		return v.(T), nil
	}

	res, err, _ := c.group.Do(key, func() (any, error) {
		v, err := load()
		if err != nil {
			return nil, err
		}
		c.store(key, v, ttl)
		return v, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return res.(T), nil
}

func (c *Cache) lookup(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || !c.now().Before(e.expires) {
		return nil, false
	}
	return e.value, true
}

func (c *Cache) store(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = cacheEntry{value: value, expires: c.now().Add(ttl)}
}
