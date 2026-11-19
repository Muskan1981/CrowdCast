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
