package chaos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Actions a command can request.
const (
	ActionFail    = "fail"
	ActionRestore = "restore"
	ActionLatency = "latency"
)

// CloseCodeRestart is WebSocket close code 1012 (service restart): clients
// should reconnect, and the load balancer will send them to a healthy node.
const CloseCodeRestart = 1012

// Command is one chaos instruction, published to the control channel.
type Command struct {
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	Action    string    `json:"action"`
	LatencyMs int       `json:"latency_ms,omitempty"`
	Origin    string    `json:"origin"`
	At        time.Time `json:"at"`
}

// Effects are the side effects a node performs when its state changes.
type Effects interface {
	// CloseSockets closes every local WebSocket with the given close code.
	CloseSockets(code int, reason string)
	// Record writes the change to the activity feed and notifies viewers.
	Record(ctx context.Context, cmd Command, st State)
}

// Controller owns this node's chaos state and applies commands addressed to it.
type Controller struct {
	self  string
	store Store
	fx    Effects
	now   func() time.Time

	mu    sync.RWMutex
	state State

	seenMu sync.Mutex
	seen   map[string]struct{} // command IDs already applied (dedupe redelivery)
}

// NewController builds a controller for the node named self.
func NewController(self string, store Store, fx Effects) *Controller {
	return &Controller{
		self:  self,
		store: store,
		fx:    fx,
		now:   func() time.Time { return time.Now().UTC() },
		state: State{Status: StatusHealthy},
		seen:  make(map[string]struct{}),
	}
}

// Load restores this node's persisted state. Call once at startup, so a
// restarted node that was failed stays failed until restored.
func (c *Controller) Load(ctx context.Context) error {
	st, ok, err := c.store.Load(ctx, c.self)
	if err != nil {
		return fmt.Errorf("load chaos state: %w", err)
	}
	if ok {
		c.mu.Lock()
		c.state = st
		c.mu.Unlock()
	}
	return nil
}

// State returns the current chaos state.
func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Failed reports whether the node should refuse new work and fail health checks.
func (c *Controller) Failed() bool {
	st := c.State()
	return st.Status == StatusFailed || st.Status == StatusDraining
}

// Latency is the artificial delay to add to each request.
func (c *Controller) Latency() time.Duration {
	return time.Duration(c.State().LatencyMs) * time.Millisecond
}

// Apply executes a command if it is addressed to this node. A duplicate command
// ID is ignored, so redelivery cannot run an action twice.
func (c *Controller) Apply(ctx context.Context, cmd Command) (State, error) {
	if cmd.Target != c.self {
		return c.State(), ErrNotTarget
	}
	if c.alreadySeen(cmd.ID) {
		return c.State(), nil
	}

	switch cmd.Action {
	case ActionFail:
		c.setState(ctx, State{Status: StatusDraining, LatencyMs: c.State().LatencyMs})
		c.fx.CloseSockets(CloseCodeRestart, "service restarting")
		c.setState(ctx, State{Status: StatusFailed, LatencyMs: c.State().LatencyMs})
	case ActionRestore:
		c.setState(ctx, State{Status: StatusHealthy})
	case ActionLatency:
		c.setState(ctx, State{Status: c.State().Status, LatencyMs: clampLatency(cmd.LatencyMs)})
	default:
		return c.State(), fmt.Errorf("%w %q", ErrUnknownAction, cmd.Action)
	}

	st := c.State()
	c.fx.Record(ctx, cmd, st)
	return st, nil
}

// Run consumes control-channel messages until ctx is cancelled. Malformed
// messages are logged and skipped.
func (c *Controller) Run(ctx context.Context, messages <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-messages:
			if !ok {
				return
			}
			var cmd Command
			if err := json.Unmarshal(raw, &cmd); err != nil {
				slog.Warn("chaos: malformed control message", "error", err)
				continue
			}
			if cmd.Target != c.self {
				continue // addressed to another node
			}
			if _, err := c.Apply(ctx, cmd); err != nil {
				slog.Warn("chaos: command failed", "action", cmd.Action, "error", err)
				continue
			}
			slog.Info("chaos: command applied", "action", cmd.Action, "id", cmd.ID, "origin", cmd.Origin)
		}
	}
}

// setState updates memory and persists. A persistence error is logged, not
// fatal: the in-memory state still takes effect.
func (c *Controller) setState(ctx context.Context, st State) {
	st.UpdatedAt = c.now()
	c.mu.Lock()
	c.state = st
	c.mu.Unlock()
	if err := c.store.Save(ctx, c.self, st); err != nil {
		slog.Error("chaos: persist state failed", "error", err)
	}
}

const seenLimit = 256

func (c *Controller) alreadySeen(id string) bool {
	if id == "" {
		return false
	}
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	if _, ok := c.seen[id]; ok {
		return true
	}
	if len(c.seen) >= seenLimit {
		c.seen = make(map[string]struct{}, seenLimit)
	}
	c.seen[id] = struct{}{}
	return false
}

// clampLatency keeps latency within [0, MaxLatency].
func clampLatency(ms int) int {
	if ms < 0 {
		return 0
	}
	if max := int(MaxLatency / time.Millisecond); ms > max {
		return max
	}
	return ms
}

// ErrUnknownAction is returned for an action the controller does not know.
var ErrUnknownAction = errors.New("unknown chaos action")
