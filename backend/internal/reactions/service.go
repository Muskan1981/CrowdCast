package reactions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/metrics"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

var (
	ErrInvalidEmoji = errors.New("invalid reaction emoji")
	ErrRateLimited  = errors.New("reaction rate limit exceeded")
)

var AllowedEmojis = map[string]bool{
	"❤️": true,
	"🔥":  true,
	"👏":  true,
	"😍":  true,
	"⚡":  true,
	"🎉":  true,
}

type ReactionBatchMessage struct {
	Type      string         `json:"t"`
	Reactions map[string]int `json:"reactions"`
	Total     int64          `json:"total"`
}

type Service struct {
	rdb           *redisstore.Client
	reactionsPg   postgres.ReactionRepository
	hub           *hub.Hub
	batchInterval time.Duration

	// In-memory aggregator: eventID -> emoji -> count
	pendingMu sync.Mutex
	pending   map[string]map[string]int

	// Minute rollups accumulator for Postgres: eventID -> emoji -> count
	rollupMu sync.Mutex
	rollups  map[string]map[string]int

	// Per-user rate limiters: userID -> *rate.Limiter
	limitersMu sync.Mutex
	limiters   map[string]*rate.Limiter

	ctx    context.Context
	cancel context.CancelFunc
}

func NewService(
	rdb *redisstore.Client,
	rxRepo postgres.ReactionRepository,
	hubInstance *hub.Hub,
	batchInterval time.Duration,
) *Service {
	if batchInterval <= 0 {
		batchInterval = 250 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		rdb:           rdb,
		reactionsPg:   rxRepo,
		hub:           hubInstance,
		batchInterval: batchInterval,
		pending:       make(map[string]map[string]int),
		rollups:       make(map[string]map[string]int),
		limiters:      make(map[string]*rate.Limiter),
		ctx:           ctx,
		cancel:        cancel,
	}

	go s.runBatchFlusher()
	go s.runMinuteRollupFlusher()
	return s
}

// RecordReaction validates and records an emoji tap into the in-memory batch.
func (s *Service) RecordReaction(ctx context.Context, eventID, userID, emoji string) error {
	if !AllowedEmojis[emoji] {
		return ErrInvalidEmoji
	}

	// Per-user token bucket: 10 taps/sec burst 15
	if !s.allowUser(userID) {
		return ErrRateLimited
	}

	s.pendingMu.Lock()
	if _, ok := s.pending[eventID]; !ok {
		s.pending[eventID] = make(map[string]int)
	}
	s.pending[eventID][emoji]++
	s.pendingMu.Unlock()

	s.rollupMu.Lock()
	if _, ok := s.rollups[eventID]; !ok {
		s.rollups[eventID] = make(map[string]int)
	}
	s.rollups[eventID][emoji]++
	s.rollupMu.Unlock()

	metrics.Default().Reactions.WithLabelValues(emoji).Inc()
	return nil
}

func (s *Service) allowUser(userID string) bool {
	s.limitersMu.Lock()
	defer s.limitersMu.Unlock()

	limiter, exists := s.limiters[userID]
	if !exists {
		// 10 reactions/second, burst of 15
		limiter = rate.NewLimiter(10, 15)
		s.limiters[userID] = limiter
	}
	return limiter.Allow()
}

// runBatchFlusher publishes coalesced reaction batches to Redis every REACTION_BATCH_MS.
func (s *Service) runBatchFlusher() {
	ticker := time.NewTicker(s.batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.flushPendingBatches()
		}
	}
}

func (s *Service) flushPendingBatches() {
	if s.rdb == nil {
		return // no Redis (unit tests): nothing can be persisted, so keep the batch out of the way
	}
	s.pendingMu.Lock()
	if len(s.pending) == 0 {
		s.pendingMu.Unlock()
		return
	}
	batchesToFlush := s.pending
	s.pending = make(map[string]map[string]int)
	s.pendingMu.Unlock()

	for eventID, counts := range batchesToFlush {
		if len(counts) == 0 {
			continue
		}

		ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
		key := redisstore.EventReactionsKey(eventID)

		pipe := s.rdb.Pipeline()
		var batchTotal int64 = 0
		for emoji, count := range counts {
			pipe.HIncrBy(ctx, key, emoji, int64(count))
			batchTotal += int64(count)
		}
		totalCmd := pipe.HIncrBy(ctx, key, "total", batchTotal)
		_, err := pipe.Exec(ctx)
		cancel()

		if err != nil {
			slog.Error("failed to pipeline reactions to redis", "event_id", eventID, "error", err)
			continue
		}

		grandTotal := totalCmd.Val()

		// Publish reaction_batch to Redis pub/sub channel ps:ev:{id}
		msg := ReactionBatchMessage{
			Type:      "reaction_batch",
			Reactions: counts,
			Total:     grandTotal,
		}
		if s.hub != nil {
			pubCtx, pubCancel := context.WithTimeout(s.ctx, 1*time.Second)
			_ = s.hub.PublishEventMessage(pubCtx, eventID, msg)
			pubCancel()
		}
	}
}

// runMinuteRollupFlusher persists minute-level aggregate reaction counts into Postgres.
func (s *Service) runMinuteRollupFlusher() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.flushMinuteRollups()
		}
	}
}

func (s *Service) flushMinuteRollups() {
	s.rollupMu.Lock()
	if len(s.rollups) == 0 {
		s.rollupMu.Unlock()
		return
	}
	rollupsToFlush := s.rollups
	s.rollups = make(map[string]map[string]int)
	s.rollupMu.Unlock()

	if s.reactionsPg == nil {
		return
	}

	minuteBucket := time.Now().UTC().Truncate(time.Minute)
	for eventID, emojis := range rollupsToFlush {
		for emoji, count := range emojis {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = s.reactionsPg.RecordRollup(ctx, &postgres.ReactionRollup{
				EventID:      eventID,
				MinuteBucket: minuteBucket,
				Emoji:        emoji,
				Count:        count,
				CreatedAt:    time.Now().UTC(),
			})
			cancel()
		}
	}
}

// GetTotalReactions fetches the current total reactions count from Redis.
func (s *Service) GetTotalReactions(ctx context.Context, eventID string) (int64, error) {
	key := redisstore.EventReactionsKey(eventID)
	totalStr, err := s.rdb.HGet(ctx, key, "total").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get total reactions: %w", err)
	}
	return strconv.ParseInt(totalStr, 10, 64)
}

func (s *Service) Close() {
	s.cancel()
}
