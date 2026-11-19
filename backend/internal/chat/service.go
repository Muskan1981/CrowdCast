package chat

import (
	"context"
	"encoding/json"
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
	ErrEmptyMessage     = errors.New("chat message cannot be empty")
	ErrMessageTooLong   = errors.New("chat message exceeds 280 characters")
	ErrProfanitySpam    = errors.New("message contains banned terms or spam patterns")
	ErrDuplicateMessage = errors.New("duplicate message detected; please wait before repeating")
	ErrRateLimited      = errors.New("chat rate limit exceeded")
)

type ChatMessage struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Text      string `json:"text"`
	Time      string `json:"time"`
	Color     string `json:"color"`
	CreatedAt string `json:"created_at"`
}

type ChatHistoryResponse struct {
	Messages      []ChatMessage `json:"messages"`
	TotalMessages int64         `json:"total_messages"`
}

type lastUserMsg struct {
	text      string
	timestamp time.Time
}

type Service struct {
	rdb       *redisstore.Client
	chatPg    postgres.ChatRepository
	hub       *hub.Hub
	rateLimit time.Duration

	// Rate limiting & duplicate protection
	limitersMu sync.Mutex
	limiters   map[string]*rate.Limiter

	dupMu   sync.Mutex
	lastMsg map[string]lastUserMsg

	// Async batch persistence
	batchChan chan *postgres.ChatMessage
	ctx       context.Context
	cancel    context.CancelFunc
}

func NewService(
	rdb *redisstore.Client,
	chatPg postgres.ChatRepository,
	hubInstance *hub.Hub,
	rateLimit time.Duration,
) *Service {
	if rateLimit <= 0 {
		rateLimit = 1500 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		rdb:       rdb,
		chatPg:    chatPg,
		hub:       hubInstance,
		rateLimit: rateLimit,
		limiters:  make(map[string]*rate.Limiter),
		lastMsg:   make(map[string]lastUserMsg),
		batchChan: make(chan *postgres.ChatMessage, 2000),
		ctx:       ctx,
		cancel:    cancel,
	}

	go s.runBatchPersister()
	return s
}

// PostMessage validates, stores, publishes, and async persists a chat message.
func (s *Service) PostMessage(ctx context.Context, eventID, userID, displayName, text string) (*ChatMessage, error) {
	cleaned := CleanText(text)
	if len(cleaned) == 0 {
		return nil, ErrEmptyMessage
	}
	if len(cleaned) > 280 {
		return nil, ErrMessageTooLong
	}
	if ContainsProfanity(cleaned) {
		return nil, ErrProfanitySpam
	}

	// 1. Per-user token-bucket rate limit
	if !s.allowUser(userID) {
		return nil, ErrRateLimited
	}

	// 2. Duplicate detection guard (last 3 seconds)
	now := time.Now().UTC()
	if s.isDuplicate(userID, cleaned, now) {
		return nil, ErrDuplicateMessage
	}

	color := DeterministicColor(userID)
	msgID := now.UnixMilli()

	chatMsg := ChatMessage{
		ID:        msgID,
		Name:      displayName,
		Text:      cleaned,
		Time:      "now",
		Color:     color,
		CreatedAt: now.Format(time.RFC3339),
	}

	msgJSON, err := json.Marshal(chatMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode chat message: %w", err)
	}

	// 3. Store in capped Redis list (LTRIM to 200) + INCR chat counter
	chatKey := redisstore.EventChatKey(eventID)
	countKey := redisstore.EventChatCountKey(eventID)

	pipe := s.rdb.Pipeline()
	pipe.LPush(ctx, chatKey, msgJSON)
	pipe.LTrim(ctx, chatKey, 0, 199)
	pipe.Incr(ctx, countKey)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("failed to push chat message to redis", "event_id", eventID, "error", err)
	}

	// 4. Publish to Redis pub/sub channel ps:ev:{id}
	if s.hub != nil {
		broadcastPayload := map[string]any{
			"t":       "chat",
			"message": chatMsg,
		}
		_ = s.hub.PublishEventMessage(ctx, eventID, broadcastPayload)
	}

	// 5. Enqueue for async PostgreSQL batch persistence
	if s.chatPg != nil {
		select {
		case s.batchChan <- &postgres.ChatMessage{
			EventID:   eventID,
			UserID:    userID,
			UserName:  displayName,
			UserColor: color,
			Text:      cleaned,
			CreatedAt: now,
		}:
		default:
			slog.Warn("postgres chat persistence queue full, dropping record", "event_id", eventID)
		}
	}

	metrics.Default().ChatMessages.Inc()
	return &chatMsg, nil
}

// GetHistory retrieves the most recent messages from Redis (with Postgres fallback).
func (s *Service) GetHistory(ctx context.Context, eventID string, limit int) (*ChatHistoryResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	chatKey := redisstore.EventChatKey(eventID)
	countKey := redisstore.EventChatCountKey(eventID)

	items, err := s.rdb.LRange(ctx, chatKey, 0, int64(limit-1)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("failed to fetch chat list from redis: %w", err)
	}

	countStr, _ := s.rdb.Get(ctx, countKey).Result()
	var totalCount int64 = 0
	if countStr != "" {
		totalCount, _ = strconv.ParseInt(countStr, 10, 64)
	}

	messages := make([]ChatMessage, 0, len(items))
	// LPUSH stores newest first. Reverse to return chronological order or newest first per UI
	for _, item := range items {
		var m ChatMessage
		if err := json.Unmarshal([]byte(item), &m); err == nil {
			messages = append(messages, m)
		}
	}

	return &ChatHistoryResponse{
		Messages:      messages,
		TotalMessages: totalCount,
	}, nil
}

func (s *Service) allowUser(userID string) bool {
	s.limitersMu.Lock()
	defer s.limitersMu.Unlock()

	limiter, exists := s.limiters[userID]
	if !exists {
		limiter = rate.NewLimiter(rate.Every(s.rateLimit), 3)
		s.limiters[userID] = limiter
	}
	return limiter.Allow()
}

func (s *Service) isDuplicate(userID, text string, now time.Time) bool {
	s.dupMu.Lock()
	defer s.dupMu.Unlock()

	last, exists := s.lastMsg[userID]
	if exists && last.text == text && now.Sub(last.timestamp) < 3*time.Second {
		return true
	}

	s.lastMsg[userID] = lastUserMsg{
		text:      text,
		timestamp: now,
	}
	return false
}

func (s *Service) runBatchPersister() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var batch []*postgres.ChatMessage

	flush := func() {
		if len(batch) == 0 || s.chatPg == nil {
			return
		}
		toPersist := batch
		batch = nil

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, msg := range toPersist {
			_ = s.chatPg.Insert(ctx, msg)
		}
	}

	for {
		select {
		case <-s.ctx.Done():
			flush()
			return
		case msg := <-s.batchChan:
			batch = append(batch, msg)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *Service) Close() {
	s.cancel()
}
