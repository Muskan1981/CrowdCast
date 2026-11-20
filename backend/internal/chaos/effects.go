package chaos

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/metrics"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
)

// HubEffects applies the real side effects: closing sockets on this node, and
// recording each change for every live event.
type HubEffects struct {
	Hub      *hub.Hub
	Redis    *redisstore.Client
	Activity postgres.ActivityRepository
}

func (e HubEffects) CloseSockets(code int, reason string) {
	if e.Hub != nil {
		e.Hub.CloseAll(code, reason)
	}
}

// Record writes an activity row per live event and pushes a node_state frame.
func (e HubEffects) Record(ctx context.Context, cmd Command, st State) {
	if e.Redis == nil {
		return
	}
	eventIDs, err := e.Redis.SMembers(ctx, redisstore.StatsEventsKey()).Result()
	if err != nil {
		slog.Warn("chaos: list events for activity failed", "error", err)
		return
	}

	metrics.Default().ChaosEvents.WithLabelValues(cmd.Target, cmd.Action).Inc()

	title, desc, tone := describe(cmd, st)
	for _, id := range eventIDs {
		if e.Activity != nil {
			item := &postgres.ActivityItem{
				EventID: id, ActivityType: "node_state",
				Title: title, Description: desc, Icon: "server", Tone: tone,
			}
			if err := e.Activity.Insert(ctx, item); err != nil {
				slog.Warn("chaos: persist activity failed", "event_id", id, "error", err)
			}
		}
		if e.Hub != nil {
			_ = e.Hub.PublishEventMessage(ctx, id, NodeStateMessage{
				Type: "node_state", Node: cmd.Target, Status: st.Status,
				LatencyMs: st.LatencyMs, Action: cmd.Action,
			})
		}
	}
}

// NodeStateMessage is pushed to viewers when a node's chaos state changes.
type NodeStateMessage struct {
	Type      string `json:"t"`
	Node      string `json:"node"`
	Status    string `json:"status"`
	LatencyMs int    `json:"latency_ms"`
	Action    string `json:"action"`
}

func describe(cmd Command, st State) (title, desc, tone string) {
	switch cmd.Action {
	case ActionFail:
		return "Node failure simulated", fmt.Sprintf("%s was taken down; viewers are reconnecting", cmd.Target), "peach"
	case ActionRestore:
		return "Node restored", fmt.Sprintf("%s is serving traffic again", cmd.Target), "mint"
	case ActionLatency:
		return "Latency injected", fmt.Sprintf("%d ms added on %s", st.LatencyMs, cmd.Target), "yellow"
	default:
		return "Node state changed", cmd.Target, "lilac"
	}
}

// ControlSource subscribes to the control channel and forwards raw payloads.
// The channel closes when ctx is cancelled.
func ControlSource(ctx context.Context, rdb *redis.Client) <-chan []byte {
	out := make(chan []byte, 64)
	ps := rdb.Subscribe(ctx, redisstore.ControlChannelKey())
	go func() {
		defer close(out)
		defer func() { _ = ps.Close() }()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ps.Channel():
				if !ok {
					return
				}
				select {
				case out <- []byte(msg.Payload):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// RedisPublisher adapts go-redis to the Publisher interface.
type RedisPublisher struct {
	Client *redis.Client
}

func (p RedisPublisher) Publish(ctx context.Context, channel string, message any) error {
	return p.Client.Publish(ctx, channel, message).Err()
}
