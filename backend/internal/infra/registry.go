// Package infra tracks the fleet of backend instances: each instance heartbeats
// its own record into Redis, and the Infrastructure page reads the merged view.
package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/redis/go-redis/v9"
)

// Health values an instance can heartbeat.
const (
	HealthHealthy  = "healthy"
	HealthDegraded = "degraded" // Redis is down; still serving from the local buffer
	HealthDraining = "draining" // chaos fail in progress; closing sockets with 1012
	HealthFailed   = "failed"   // chaos fail complete; refusing new work
)

// Expected is one node the fleet is supposed to contain. Nodes missing from
// Redis still appear in the table, as Offline, so the UI can show the failure.
type Expected struct {
	Name   string
	Zone   string
	Weight int
	Color  string
}

// ExpectedNodes is the fleet from the spec: 50/30/20 weights, one colour each.
var ExpectedNodes = []Expected{
	{Name: "crowdcast-api-01", Zone: "Primary node", Weight: 50, Color: "mint"},
	{Name: "crowdcast-api-02", Zone: "Secondary node", Weight: 30, Color: "peach"},
	{Name: "crowdcast-api-03", Zone: "Burst node", Weight: 20, Color: "lilac"},
}

// NodeRecord is what one instance writes about itself every heartbeat.
type NodeRecord struct {
	Name           string    `json:"name"`
	Zone           string    `json:"zone"`
	Weight         int       `json:"weight"`
	RequestsPerSec float64   `json:"requests_per_sec"`
	CPU            float64   `json:"cpu"`
	Memory         float64   `json:"memory"`
	Connections    int64     `json:"connections"`
	Health         string    `json:"health"`
	LastSeen       time.Time `json:"last_seen"`
}

// KV is the Redis subset the registry uses. An interface keeps the TTL and
// merge logic testable without a live Redis.
type KV interface {
	// SetEX stores value under key with a TTL.
	SetEX(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// GetMany returns the values that exist. Missing keys are absent from the map.
	GetMany(ctx context.Context, keys ...string) (map[string][]byte, error)
}

// Registry reads and writes heartbeat records.
type Registry struct {
	kv  KV
	ttl time.Duration
}

// HeartbeatTTL is how long a record lives without renewal (spec: 6 s).
const HeartbeatTTL = 6 * time.Second

// NewRegistry builds a registry with the given record TTL.
func NewRegistry(kv KV, ttl time.Duration) *Registry {
	return &Registry{kv: kv, ttl: ttl}
}

// Heartbeat writes this instance's record with a TTL. Stamps LastSeen itself.
func (r *Registry) Heartbeat(ctx context.Context, rec NodeRecord, now time.Time) error {
	rec.LastSeen = now.UTC()
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode heartbeat: %w", err)
	}
	return r.kv.SetEX(ctx, redisstore.NodeHeartbeatKey(rec.Name), payload, r.ttl)
}

// Snapshot returns the live records for the named nodes. Expired or never-seen
// nodes are absent from the result.
func (r *Registry) Snapshot(ctx context.Context, names []string) (map[string]NodeRecord, error) {
	keys := make([]string, 0, len(names))
	for _, n := range names {
		keys = append(keys, redisstore.NodeHeartbeatKey(n))
	}
	raw, err := r.kv.GetMany(ctx, keys...)
	if err != nil {
		return nil, err
	}
	out := make(map[string]NodeRecord, len(raw))
	for _, n := range names {
		payload, ok := raw[redisstore.NodeHeartbeatKey(n)]
		if !ok {
			continue
		}
		var rec NodeRecord
		if err := json.Unmarshal(payload, &rec); err != nil {
			continue // a corrupt record is treated as absent
		}
		out[n] = rec
	}
	return out, nil
}

// RedisKV adapts go-redis to the KV interface.
type RedisKV struct {
	Client *redis.Client
}

func (k RedisKV) SetEX(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return k.Client.Set(ctx, key, value, ttl).Err()
}

func (k RedisKV) GetMany(ctx context.Context, keys ...string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return map[string][]byte{}, nil
	}
	vals, err := k.Client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(keys))
	for i, v := range vals {
		if s, ok := v.(string); ok {
			out[keys[i]] = []byte(s)
		}
	}
	return out, nil
}
