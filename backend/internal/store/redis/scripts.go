package redis

import (
	"github.com/redis/go-redis/v9"
)

// Common atomic Lua scripts for CrowdCast operations.
var (
	// EventStateUpdateScript atomically updates event state in Redis hash.
	// KEYS[1] = ev:{id}:state
	// ARGV[1] = new_status
	// ARGV[2] = started_at (RFC3339) or empty
	// ARGV[3] = paused_ms (string int)
	// ARGV[4] = paused_at (RFC3339) or empty
	EventStateUpdateScript = redis.NewScript(`
		local key = KEYS[1]
		local new_status = ARGV[1]
		local started_at = ARGV[2]
		local paused_ms = ARGV[3]
		local paused_at = ARGV[4]

		redis.call('HSET', key, 'status', new_status)
		if started_at ~= '' then
			redis.call('HSET', key, 'started_at', started_at)
		end
		if paused_ms ~= '' then
			redis.call('HSET', key, 'paused_ms', paused_ms)
		end
		if paused_at ~= '' then
			redis.call('HSET', key, 'paused_at', paused_at)
		else
			redis.call('HDEL', key, 'paused_at')
		end
		return 'OK'
	`)

	// PresenceHeartbeatScript adds or updates a viewer in the presence sorted set,
	// removes expired viewers, and returns the current active count.
	// KEYS[1] = ev:{id}:presence
	// ARGV[1] = viewer_id
	// ARGV[2] = current_timestamp (unix seconds)
	// ARGV[3] = cutoff_timestamp (unix seconds)
	PresenceHeartbeatScript = redis.NewScript(`
		local key = KEYS[1]
		local viewer_id = ARGV[1]
		local now = tonumber(ARGV[2])
		local cutoff = tonumber(ARGV[3])

		redis.call('ZADD', key, now, viewer_id)
		redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)
		return redis.call('ZCARD', key)
	`)

	// PollVoteScript ensures atomic one-vote-per-user in Redis.
	// KEYS[1] = poll:{pid}:voters (SET)
	// KEYS[2] = poll:{pid}:votes (HASH)
	// ARGV[1] = user_id
	// ARGV[2] = option_idx
	PollVoteScript = redis.NewScript(`
		local voter_key = KEYS[1]
		local votes_key = KEYS[2]
		local user_id = ARGV[1]
		local opt_idx = ARGV[2]

		if redis.call('SISMEMBER', voter_key, user_id) == 1 then
			return 0
		end

		redis.call('SADD', voter_key, user_id)
		redis.call('HINCRBY', votes_key, opt_idx, 1)
		return 1
	`)
)
