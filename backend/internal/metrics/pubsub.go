package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Known WebSocket message types. Anything else is bucketed as "other" so a
// malicious client cannot blow up label cardinality by inventing types.
var knownMessageTypes = map[string]bool{
	"welcome": true, "presence": true, "reaction_batch": true, "chat": true,
	"poll_update": true, "event_state": true, "stats_tick": true, "activity": true,
	"node_state": true, "pong": true, "error": true,
	"reaction": true, "vote": true, "ping": true,
}

// MessageType extracts the "t" discriminator from a JSON frame without fully
// decoding it. It returns "other" for unknown types and "unknown" if absent.
func MessageType(frame []byte) string {
	marker := []byte(`"t":"`)
	i := bytes.Index(frame, marker)
	if i < 0 {
		return "unknown"
	}
	rest := frame[i+len(marker):]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return "unknown"
	}
	t := string(rest[:end])
	if knownMessageTypes[t] {
		return t
	}
	return "other"
}

// StampPublish adds an "_ts" field (Unix milliseconds) to a JSON object just
// before it is PUBLISHed, so receivers can measure pubsub lag. Non-object
// payloads are returned unchanged.
func StampPublish(frame []byte, now time.Time) []byte {
	trimmed := bytes.TrimSpace(frame)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return frame
	}
	body := trimmed[:len(trimmed)-1]
	var out bytes.Buffer
	out.Write(body)
	if len(bytes.TrimSpace(body[1:])) > 0 {
		out.WriteByte(',')
	}
	out.WriteString(`"_ts":`)
	out.WriteString(strconv.FormatInt(now.UnixMilli(), 10))
	out.WriteByte('}')
	return out.Bytes()
}

// ObservePubSubLag records how long a frame spent between PUBLISH and this
// receive. Frames without an "_ts" field are ignored.
func ObservePubSubLag(frame []byte, received time.Time) {
	var env struct {
		TS int64 `json:"_ts"`
	}
	if err := json.Unmarshal(frame, &env); err != nil || env.TS == 0 {
		return
	}
	lag := received.Sub(time.UnixMilli(env.TS)).Seconds()
	if lag < 0 {
		lag = 0 // clock skew between instances
	}
	Default().PubSubLag.Observe(lag)
}

// RedisHook records every Redis command's latency and counts failures.
// Register it with client.AddHook. redis.Nil ("key not found") is not a failure.
type RedisHook struct{}

var _ redis.Hook = RedisHook{}

func (RedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (RedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		observeRedis(cmd.Name(), start, err)
		return err
	}
}

func (RedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		observeRedis("pipeline", start, err)
		return err
	}
}

func observeRedis(op string, start time.Time, err error) {
	m := Default()
	m.RedisOpDuration.WithLabelValues(op).Observe(time.Since(start).Seconds())
	if err != nil && !errors.Is(err, redis.Nil) {
		m.AppErrors.WithLabelValues("redis").Inc()
	}
}
