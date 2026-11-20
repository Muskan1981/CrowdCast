package redis

import "fmt"

// Key builders matching CROWDCAST.md section 7.
// All Redis key constructors are defined here; nothing is hardcoded elsewhere.

// EventStateKey returns the Redis key for an event's state hash (ev:{id}:state).
func EventStateKey(eventID string) string {
	return fmt.Sprintf("ev:%s:state", eventID)
}

// EventPresenceKey returns the Redis key for an event's presence sorted set (ev:{id}:presence).
func EventPresenceKey(eventID string) string {
	return fmt.Sprintf("ev:%s:presence", eventID)
}

// EventReactionsKey returns the Redis key for an event's reactions hash (ev:{id}:reactions).
func EventReactionsKey(eventID string) string {
	return fmt.Sprintf("ev:%s:reactions", eventID)
}

// EventChatKey returns the Redis key for an event's chat list (ev:{id}:chat).
func EventChatKey(eventID string) string {
	return fmt.Sprintf("ev:%s:chat", eventID)
}

// EventChatCountKey returns the Redis key for an event's total chat message counter (ev:{id}:chatcount).
func EventChatCountKey(eventID string) string {
	return fmt.Sprintf("ev:%s:chatcount", eventID)
}

// PollVotesKey returns the Redis key for a poll's option votes hash (poll:{pid}:votes).
func PollVotesKey(pollID string) string {
	return fmt.Sprintf("poll:%s:votes", pollID)
}

// PollVotersKey returns the Redis key for a poll's voter user_ids set (poll:{pid}:voters).
func PollVotersKey(pollID string) string {
	return fmt.Sprintf("poll:%s:voters", pollID)
}

// NodesKey returns the Redis key for the node registry heartbeat payloads (nodes).
func NodesKey() string {
	return "nodes"
}

// ChaosNodeKey returns the Redis key for a node's chaos injection settings hash (chaos:{node}).
func ChaosNodeKey(nodeName string) string {
	return fmt.Sprintf("chaos:%s", nodeName)
}

// EventChannelKey returns the Redis pub/sub channel for realtime event fan-out (ps:ev:{id}).
func EventChannelKey(eventID string) string {
	return fmt.Sprintf("ps:ev:%s", eventID)
}

// ControlChannelKey returns the Redis pub/sub channel for node control commands (ps:control).
func ControlChannelKey() string {
	return "ps:control"
}

// StatsEventsKey is a set of event IDs that have at least one WebSocket viewer.
// The stats leader iterates it to know which events to tick.
func StatsEventsKey() string {
	return "stats:events"
}

// StatsLeaderKey is the lock key the stats leader holds (SET NX PX + renewal).
func StatsLeaderKey() string {
	return "stats:leader"
}

// StatsHistoryKey is a sorted set (score = unix ms) of per-tick event snapshots,
// used to compute "vs previous window" deltas.
func StatsHistoryKey(eventID string) string {
	return fmt.Sprintf("stats:hist:%s", eventID)
}

// StatsMilestoneKey stores the last audience milestone announced for an event,
// so a new leader does not repeat an announcement after failover.
func StatsMilestoneKey(eventID string) string {
	return fmt.Sprintf("stats:milestone:%s", eventID)
}

// StatsRequestsKey is a hash of minute bucket (unix minute) -> request count,
// written by every instance and read as the Redis fallback for traffic charts.
func StatsRequestsKey() string {
	return "stats:reqs"
}

// NodeHeartbeatKey holds one instance's latest heartbeat record (node:{name}:hb).
// It is written with a TTL, so a dead instance's entry expires on its own.
func NodeHeartbeatKey(nodeName string) string {
	return fmt.Sprintf("node:%s:hb", nodeName)
}

// ChaosNodeKey (chaos:{node}) already exists above; it holds the persisted chaos state.
