// Package chaos lets one instance break another on purpose, for the failure
// demo. Commands travel over the Redis control channel: any instance can
// receive an admin request, but only the addressed target executes it.
package chaos

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/redis/go-redis/v9"
)

// Node statuses.
const (
	StatusHealthy  = "healthy"
	StatusDraining = "draining" // sockets are being closed with 1012
	StatusFailed   = "failed"   // refusing new work, /healthz returns 503
)

// MaxLatency caps injected latency (spec: 5 s).
const MaxLatency = 5 * time.Second

// State is the chaos condition of one node. It is persisted so a restart keeps
// the failure until someone restores the node.
type State struct {
	Status    string    `json:"status"`
	LatencyMs int       `json:"latency_ms"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store persists state per node name.
type Store interface {
	Save(ctx context.Context, node string, s State) error
	// Load returns the stored state. ok is false if the node has never been changed.
	Load(ctx context.Context, node string) (s State, ok bool, err error)
}

// RedisStore keeps state in the chaos:{node} hash, with no TTL.
type RedisStore struct {
	Client *redis.Client
}

func (r RedisStore) Save(ctx context.Context, node string, s State) error {
	return r.Client.HSet(ctx, redisstore.ChaosNodeKey(node), map[string]any{
		"status":     s.Status,
		"latency_ms": strconv.Itoa(s.LatencyMs),
		"updated_at": s.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}).Err()
}

func (r RedisStore) Load(ctx context.Context, node string) (State, bool, error) {
	fields, err := r.Client.HGetAll(ctx, redisstore.ChaosNodeKey(node)).Result()
	if err != nil {
		return State{}, false, err
	}
	if len(fields) == 0 {
		return State{}, false, nil
	}
	st := State{Status: fields["status"]}
	if st.Status == "" {
		st.Status = StatusHealthy
	}
	st.LatencyMs, _ = strconv.Atoi(fields["latency_ms"])
	if t, err := time.Parse(time.RFC3339Nano, fields["updated_at"]); err == nil {
		st.UpdatedAt = t
	}
	return st, true, nil
}

// ErrNotTarget is returned when a command addressed to another node reaches this one.
var ErrNotTarget = errors.New("command is addressed to another node")

// MemoryStore keeps state in process memory. It is used when Redis is not
// configured, such as in unit tests; state then does not survive a restart.
type MemoryStore struct {
	mu     sync.Mutex
	states map[string]State
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{states: make(map[string]State)}
}

func (m *MemoryStore) Save(_ context.Context, node string, s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[node] = s
	return nil
}

func (m *MemoryStore) Load(_ context.Context, node string) (State, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[node]
	return s, ok, nil
}
