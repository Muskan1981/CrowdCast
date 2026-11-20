package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/redis/go-redis/v9"
)

// Source labels, surfaced to clients and in the X-Data-Source header.
const (
	SourcePrometheus = "prometheus"
	SourceFallback   = "fallback"
)

// Queries are scoped to API routes. "unmatched" (404 noise) and the scrape
// endpoint itself are excluded so they cannot inflate the numbers.
const (
	qRequestsPerSec = `sum(rate(http_requests_total{route!="unmatched",route!="/metrics"}[1m]))`
	qAvgLatencyMs   = `1000 * sum(rate(http_request_duration_seconds_sum{route!="unmatched",route!="/metrics"}[1m])) / sum(rate(http_request_duration_seconds_count{route!="unmatched",route!="/metrics"}[1m]))`
	qRPSByNode      = `sum by (instance) (rate(http_requests_total{route!="unmatched",route!="/metrics"}[1m]))`
)

// Snapshot is one point-in-time reading for an event. The leader writes one
// every tick, and deltas are computed against the snapshot from a minute earlier.
type Snapshot struct {
	EventID        string    `json:"event_id"`
	At             time.Time `json:"at"`
	Attendees      int64     `json:"attendees"`
	Reactions      int64     `json:"reactions"`
	ChatMessages   int64     `json:"chat_messages"`
	RequestsPerSec float64   `json:"requests_per_sec"`
	AvgResponseMs  float64   `json:"avg_response_ms"`
	Source         string    `json:"source"`
}

// TakeSnapshot reads live state for one event. Attendees, reactions and chat
// come from Redis. Request rate and latency come from Prometheus, and if that
// is unreachable the rate falls back to the Redis minute buckets.
func (s *Service) TakeSnapshot(ctx context.Context, eventID string) (Snapshot, error) {
	now := s.now()
	snap := Snapshot{EventID: eventID, At: now, Source: SourcePrometheus}

	presenceStats, err := s.presence.GetPresenceStats(ctx, eventID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("presence: %w", err)
	}
	snap.Attendees = presenceStats.Attendees

	snap.Reactions, err = parseOptionalInt(s.rdb.HGet(ctx, redisstore.EventReactionsKey(eventID), "total").Result())
	if err != nil {
		return Snapshot{}, fmt.Errorf("reactions: %w", err)
	}
	snap.ChatMessages, err = parseOptionalInt(s.rdb.Get(ctx, redisstore.EventChatCountKey(eventID)).Result())
	if err != nil {
		return Snapshot{}, fmt.Errorf("chat count: %w", err)
	}

	// If the first query fails, skip the second. An unreachable Prometheus would
	// otherwise cost two full client timeouts per snapshot.
	rps, err := s.prom.QueryInstant(ctx, qRequestsPerSec)
	if err != nil {
		snap.Source = SourceFallback
		snap.RequestsPerSec = s.fallbackRPS(ctx, now)
		return snap, nil
	}
	latency, err := s.prom.QueryInstant(ctx, qAvgLatencyMs)
	if err != nil {
		snap.Source = SourceFallback
		snap.RequestsPerSec = rps
		return snap, nil
	}
	snap.RequestsPerSec = rps
	snap.AvgResponseMs = latency
	return snap, nil
}

// fallbackRPS estimates requests per second from the Redis minute buckets. It
// blends the current minute with the previous one, weighted by how far into the
// current minute we are, so the figure moves smoothly rather than in steps.
func (s *Service) fallbackRPS(ctx context.Context, now time.Time) float64 {
	cur := now.Truncate(time.Minute)
	prev := cur.Add(-time.Minute)
	vals, err := s.rdb.HMGet(ctx, redisstore.StatsRequestsKey(),
		strconv.FormatInt(cur.Unix(), 10), strconv.FormatInt(prev.Unix(), 10)).Result()
	if err != nil {
		return 0
	}
	curCount := parseBucket(vals, 0)
	prevCount := parseBucket(vals, 1)
	return sliding60sRate(curCount, prevCount, now.Sub(cur))
}

// sliding60sRate is the pure part of fallbackRPS: requests in the last 60 s
// divided by 60, given the current minute's count, the previous minute's count,
// and how far we are into the current minute.
func sliding60sRate(curCount, prevCount float64, intoMinute time.Duration) float64 {
	frac := intoMinute.Seconds() / 60
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return (curCount + prevCount*(1-frac)) / 60
}

// recordHistory appends the snapshot to the event's sorted set and trims entries
// older than 15 minutes. Previous-window lookups then read from this set.
func (s *Service) recordHistory(ctx context.Context, snap Snapshot) error {
	payload, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	key := redisstore.StatsHistoryKey(snap.EventID)
	pipe := s.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(snap.At.UnixMilli()), Member: payload})
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(snap.At.Add(-15*time.Minute).UnixMilli(), 10))
	pipe.Expire(ctx, key, 30*time.Minute)
	_, err = pipe.Exec(ctx)
	return err
}

// previousSnapshot returns the newest snapshot taken at or before now-window, or
// nil if the event has no history that old yet (so the card shows "—").
func (s *Service) previousSnapshot(ctx context.Context, eventID string, now time.Time, window time.Duration) (*Snapshot, error) {
	cutoff := strconv.FormatInt(now.Add(-window).UnixMilli(), 10)
	res, err := s.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     redisstore.StatsHistoryKey(eventID),
		ByScore: true,
		Rev:     true,
		Start:   cutoff,
		Stop:    "-inf",
		Count:   1,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, nil
	}
	var prev Snapshot
	if err := json.Unmarshal([]byte(res[0]), &prev); err != nil {
		return nil, err
	}
	return &prev, nil
}

// parseOptionalInt turns a Redis string reply into an int64. A missing key
// (redis.Nil) counts as 0; any other error is passed through.
func parseOptionalInt(raw string, err error) (int64, error) {
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
}

// parseBucket reads one HMGET value. A missing field is 0.
func parseBucket(vals []any, i int) float64 {
	if i >= len(vals) {
		return 0
	}
	str, ok := vals[i].(string)
	if !ok {
		return 0
	}
	n, err := strconv.ParseFloat(str, 64)
	if err != nil || math.IsNaN(n) {
		return 0
	}
	return n
}
