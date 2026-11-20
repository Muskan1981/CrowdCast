package presence

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/crowdcast/crowdcast/internal/store/postgres"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/redis/go-redis/v9"
)

type PresenceMessage struct {
	Type      string `json:"t"`
	Attendees int64  `json:"attendees"`
	Delta10m  string `json:"delta_10m"`
	Bars      []int  `json:"bars"`
}

type SessionRecord struct {
	ViewerID string
	EventID  string
	JoinedAt time.Time
}

type Service struct {
	rdb         *redisstore.Client
	viewers     postgres.ViewerRepository
	events      postgres.EventRepository
	sessionChan chan SessionRecord
	closeChan   chan struct {
		sessionID int64
		leftAt    time.Time
	}
	activeSessions sync.Map // viewerID_eventID -> sessionID (int64)
	sparklineBars  []int
	barsMu         sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
}

func NewService(rdb *redisstore.Client, viewers postgres.ViewerRepository, events postgres.EventRepository) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		ctx:         ctx,
		cancel:      cancel,
		rdb:         rdb,
		viewers:     viewers,
		events:      events,
		sessionChan: make(chan SessionRecord, 1000),
		closeChan: make(chan struct {
			sessionID int64
			leftAt    time.Time
		}, 1000),
		sparklineBars: []int{32, 48, 42, 68, 54, 78, 63, 88, 72, 100, 84, 92, 76, 95, 82, 100, 88, 94},
	}

	go s.processAttendanceSessions()
	return s
}

// RecordJoin registers the viewer in the presence sorted set and queues attendance session.
func (s *Service) RecordJoin(ctx context.Context, eventID, viewerID string) error {
	now := time.Now().UTC()
	key := redisstore.EventPresenceKey(eventID)

	// Add/update to Redis ZSet
	if err := s.rdb.ZAdd(ctx, key, redis.Z{
		Score:  float64(now.Unix()),
		Member: viewerID,
	}).Err(); err != nil {
		return fmt.Errorf("failed to record presence: %w", err)
	}

	// Queue async Postgres session write
	if s.viewers != nil {
		select {
		case s.sessionChan <- SessionRecord{ViewerID: viewerID, EventID: eventID, JoinedAt: now}:
		default:
			slog.Warn("attendance session queue full, dropping record", "viewer_id", viewerID)
		}
	}

	return nil
}

// RefreshHeartbeat refreshes the last_seen timestamp in Redis.
func (s *Service) RefreshHeartbeat(ctx context.Context, eventID, viewerID string) error {
	now := time.Now().UTC()
	key := redisstore.EventPresenceKey(eventID)
	return s.rdb.ZAdd(ctx, key, redis.Z{
		Score:  float64(now.Unix()),
		Member: viewerID,
	}).Err()
}

// RecordLeave removes the viewer from Redis presence and closes attendance in Postgres.
func (s *Service) RecordLeave(ctx context.Context, eventID, viewerID string) error {
	key := redisstore.EventPresenceKey(eventID)
	_ = s.rdb.ZRem(ctx, key, viewerID).Err()

	sessionKey := viewerID + "_" + eventID
	if val, ok := s.activeSessions.LoadAndDelete(sessionKey); ok {
		sessionID := val.(int64)
		if s.viewers != nil {
			select {
			case s.closeChan <- struct {
				sessionID int64
				leftAt    time.Time
			}{sessionID: sessionID, leftAt: time.Now().UTC()}:
			default:
			}
		}
	}

	return nil
}

// GetPresenceStats computes current attendee count, 10m delta, and sparkline bars.
func (s *Service) GetPresenceStats(ctx context.Context, eventID string) (*PresenceMessage, error) {
	now := time.Now().UTC()
	key := redisstore.EventPresenceKey(eventID)

	// 1. Prune stale entries older than 30s
	cutoff30s := now.Add(-30 * time.Second).Unix()
	_ = s.rdb.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(cutoff30s, 10)).Err()

	// 2. Count active viewers
	count, err := s.rdb.ZCard(ctx, key).Result()
	if err != nil && !errorsIsNil(err) {
		return nil, fmt.Errorf("failed to get active count: %w", err)
	}

	// 3. Count joined in last 10 minutes (score >= now - 600)
	cutoff10m := now.Add(-10 * time.Minute).Unix()
	recentCount, _ := s.rdb.ZCount(ctx, key, strconv.FormatInt(cutoff10m, 10), "+inf").Result()

	deltaPct := 0.0
	if count > 0 {
		deltaPct = float64(recentCount) / float64(count) * 100.0
	}
	deltaStr := fmt.Sprintf("+%.1f%%", deltaPct)

	// 4. Update peak audience if needed
	stateKey := redisstore.EventStateKey(eventID)
	peakStr, _ := s.rdb.HGet(ctx, stateKey, "peak_audience").Result()
	var peak int64 = 0
	if peakStr != "" {
		peak, _ = strconv.ParseInt(peakStr, 10, 64)
	}
	if count > peak {
		_ = s.rdb.HSet(ctx, stateKey, "peak_audience", count).Err()
		if s.events != nil {
			_ = s.events.UpdatePeakAudience(ctx, eventID, int(count))
		}
	}

	s.barsMu.RLock()
	barsCopy := make([]int, len(s.sparklineBars))
	copy(barsCopy, s.sparklineBars)
	s.barsMu.RUnlock()

	return &PresenceMessage{
		Type:      "presence",
		Attendees: count,
		Delta10m:  deltaStr,
		Bars:      barsCopy,
	}, nil
}

// UpdateSparkline allows updating the 18-bar distribution over time.
func (s *Service) UpdateSparkline(val int) {
	s.barsMu.Lock()
	defer s.barsMu.Unlock()
	if len(s.sparklineBars) >= 18 {
		s.sparklineBars = s.sparklineBars[1:]
	}
	// normalize between 20 and 100
	normalized := int(math.Min(100, math.Max(20, float64(val))))
	s.sparklineBars = append(s.sparklineBars, normalized)
}

// Close stops the attendance writer goroutine. It is safe to call more than once.
func (s *Service) Close() {
	s.cancel()
}

func (s *Service) processAttendanceSessions() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case rec := <-s.sessionChan:
			if s.viewers != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				sess := &postgres.AttendanceSession{
					ViewerID: rec.ViewerID,
					EventID:  rec.EventID,
					JoinedAt: rec.JoinedAt,
				}
				sessionID, err := s.viewers.RecordAttendance(ctx, sess)
				cancel()
				if err == nil && sessionID > 0 {
					sessionKey := rec.ViewerID + "_" + rec.EventID
					s.activeSessions.Store(sessionKey, sessionID)
				}
			}

		case closeRec := <-s.closeChan:
			if s.viewers != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = s.viewers.EndAttendance(ctx, closeRec.sessionID)
				cancel()
			}
		}
	}
}

func errorsIsNil(err error) bool {
	return err == nil || err == redis.Nil
}
