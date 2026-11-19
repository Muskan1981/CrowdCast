package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/crowdcast/crowdcast/internal/metrics"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/redis/go-redis/v9"
)

type EventStateMessage struct {
	Type             string `json:"t"`
	EventID          string `json:"event_id"`
	Status           string `json:"status"`
	StartedAt        string `json:"started_at,omitempty"`
	LiveForSeconds   int64  `json:"live_for_seconds"`
	LiveForFormatted string `json:"live_for_formatted"`
	UpdatedAt        string `json:"updated_at"`
}

type EventDetails struct {
	ID               string    `json:"id"`
	EventNumber      string    `json:"event_number"`
	Title            string    `json:"title"`
	TitleMain        string    `json:"title_main"`
	Subtitle         string    `json:"subtitle"`
	SessionCode      string    `json:"session_code"`
	Description      string    `json:"description"`
	Host             HostInfo  `json:"host"`
	Stage            StageInfo `json:"stage"`
	DateEyebrow      string    `json:"date_eyebrow"`
	Status           string    `json:"status"`
	StartedAt        string    `json:"started_at,omitempty"`
	LiveForFormatted string    `json:"live_for_formatted"`
	LiveForSeconds   int64     `json:"live_for_seconds"`
	PeakAudience     int64     `json:"peak_audience"`
	CurrentAttendees int64     `json:"current_attendees"`
	TotalReactions   int64     `json:"total_reactions"`
}

type HostInfo struct {
	Name         string `json:"name"`
	AvatarLetter string `json:"avatar_letter"`
}

type StageInfo struct {
	Name       string `json:"name"`
	Resolution string `json:"resolution"`
}

type Service struct {
	redisClient *redisstore.Client
	eventsRepo  postgres.EventRepository
	activity    postgres.ActivityRepository
}

func NewService(rdb *redisstore.Client, eventsRepo postgres.EventRepository, activity postgres.ActivityRepository) *Service {
	return &Service{
		redisClient: rdb,
		eventsRepo:  eventsRepo,
		activity:    activity,
	}
}

// GetEvent fetches the event metadata from Postgres and augments with real-time state from Redis.
func (s *Service) GetEvent(ctx context.Context, eventID string) (*EventDetails, error) {
	pgEvent, err := s.eventsRepo.Get(ctx, eventID)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("failed to fetch event from database: %w", err)
	}

	stateKey := redisstore.EventStateKey(eventID)
	stateData, err := s.redisClient.HGetAll(ctx, stateKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("failed to fetch event state from redis: %w", err)
	}

	now := time.Now().UTC()
	currentStatus := EventStatus(pgEvent.Status)
	var startedAt *time.Time = pgEvent.StartedAt
	var pausedAt *time.Time
	var pausedMs int64 = 0
	var peakAudience int64 = 0

	// If redis state exists, merge
	if len(stateData) > 0 {
		if st, ok := stateData["status"]; ok && st != "" {
			currentStatus = EventStatus(st)
		}
		if sa, ok := stateData["started_at"]; ok && sa != "" {
			if t, err := time.Parse(time.RFC3339, sa); err == nil {
				startedAt = &t
			}
		}
		if pa, ok := stateData["paused_at"]; ok && pa != "" {
			if t, err := time.Parse(time.RFC3339, pa); err == nil {
				pausedAt = &t
			}
		}
		if pm, ok := stateData["paused_ms"]; ok && pm != "" {
			if val, err := strconv.ParseInt(pm, 10, 64); err == nil {
				pausedMs = val
			}
		}
		if paud, ok := stateData["peak_audience"]; ok && paud != "" {
			if val, err := strconv.ParseInt(paud, 10, 64); err == nil {
				peakAudience = val
			}
		}
	} else {
		// Populate redis cache from Postgres initial record
		initFields := map[string]any{
			"status":        string(currentStatus),
			"paused_ms":     0,
			"peak_audience": 0,
		}
		if startedAt != nil {
			initFields["started_at"] = startedAt.Format(time.RFC3339)
		}
		_ = s.redisClient.HSet(ctx, stateKey, initFields).Err()
	}

	liveForSec, liveForFmt := ComputeLiveFor(currentStatus, startedAt, pausedAt, pausedMs, now)

	// Fetch presence count from Redis sorted set
	presenceKey := redisstore.EventPresenceKey(eventID)
	attendeeCount, _ := s.redisClient.ZCard(ctx, presenceKey).Result()
	if attendeeCount > peakAudience {
		peakAudience = attendeeCount
		_ = s.redisClient.HSet(ctx, stateKey, "peak_audience", peakAudience).Err()
	}

	// Fetch reaction count
	reactionsKey := redisstore.EventReactionsKey(eventID)
	rxMap, _ := s.redisClient.HGetAll(ctx, reactionsKey).Result()
	var totalReactions int64 = 0
	for _, countStr := range rxMap {
		if c, err := strconv.ParseInt(countStr, 10, 64); err == nil {
			totalReactions += c
		}
	}

	startedAtStr := ""
	if startedAt != nil {
		startedAtStr = startedAt.Format(time.RFC3339)
	}

	return &EventDetails{
		ID:          pgEvent.ID,
		EventNumber: pgEvent.EventNumber,
		Title:       pgEvent.Title,
		TitleMain:   pgEvent.TitleMain,
		Subtitle:    pgEvent.Subtitle,
		SessionCode: pgEvent.SessionCode,
		Description: pgEvent.Description,
		Host: HostInfo{
			Name:         pgEvent.HostName,
			AvatarLetter: pgEvent.HostAvatarLetter,
		},
		Stage: StageInfo{
			Name:       pgEvent.StageName,
			Resolution: pgEvent.StageResolution,
		},
		DateEyebrow:      pgEvent.DateEyebrow,
		Status:           string(currentStatus),
		StartedAt:        startedAtStr,
		LiveForFormatted: liveForFmt,
		LiveForSeconds:   liveForSec,
		PeakAudience:     peakAudience,
		CurrentAttendees: attendeeCount,
		TotalReactions:   totalReactions,
	}, nil
}

// TransitionEvent processes lifecycle actions, updates Redis and Postgres, audits in activity_log, and publishes to Redis pub/sub.
func (s *Service) TransitionEvent(ctx context.Context, eventID string, action EventAction) (string, error) {
	// First fetch current state
	event, err := s.GetEvent(ctx, eventID)
	if err != nil {
		return "", err
	}

	currentStatus := EventStatus(event.Status)
	nextStatus, err := Transition(currentStatus, action)
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	stateKey := redisstore.EventStateKey(eventID)

	var startedAtStr string = event.StartedAt
	var pausedAtStr string
	var pausedMs int64 = 0

	stateData, _ := s.redisClient.HGetAll(ctx, stateKey).Result()
	if val, ok := stateData["paused_ms"]; ok {
		if p, err := strconv.ParseInt(val, 10, 64); err == nil {
			pausedMs = p
		}
	}

	switch action {
	case ActionStart:
		startedAtStr = now.Format(time.RFC3339)
		pausedAtStr = ""
		pausedMs = 0
	case ActionPause:
		pausedAtStr = now.Format(time.RFC3339)
	case ActionResume:
		if pausedAtVal, ok := stateData["paused_at"]; ok && pausedAtVal != "" {
			if t, err := time.Parse(time.RFC3339, pausedAtVal); err == nil {
				additionalPause := now.Sub(t).Milliseconds()
				if additionalPause > 0 {
					pausedMs += additionalPause
				}
			}
		}
		pausedAtStr = ""
	case ActionEnd:
		pausedAtStr = ""
	}

	// 1. Update Redis state hash
	redisFields := map[string]any{
		"status":    string(nextStatus),
		"paused_ms": pausedMs,
	}
	if startedAtStr != "" {
		redisFields["started_at"] = startedAtStr
	}
	if pausedAtStr != "" {
		redisFields["paused_at"] = pausedAtStr
	} else {
		_ = s.redisClient.HDel(ctx, stateKey, "paused_at").Err()
	}
	if err := s.redisClient.HSet(ctx, stateKey, redisFields).Err(); err != nil {
		return "", fmt.Errorf("failed to update state in redis: %w", err)
	}

	// 2. Update Postgres events table status
	_ = s.eventsRepo.UpdateStatus(ctx, eventID, string(nextStatus))

	// 3. Write activity log audit in Postgres
	_ = s.activity.Insert(ctx, &postgres.ActivityItem{
		EventID:      eventID,
		ActivityType: "lifecycle",
		Title:        fmt.Sprintf("Event %s", nextStatus),
		Description:  fmt.Sprintf("Event transitioned from %s to %s via action %s", currentStatus, nextStatus, action),
		Icon:         "activity",
		Tone:         "neutral",
		CreatedAt:    now,
	})

	// 4. Compute live for stats
	var startedTime *time.Time
	if startedAtStr != "" {
		if t, err := time.Parse(time.RFC3339, startedAtStr); err == nil {
			startedTime = &t
		}
	}
	var pausedTime *time.Time
	if pausedAtStr != "" {
		if t, err := time.Parse(time.RFC3339, pausedAtStr); err == nil {
			pausedTime = &t
		}
	}
	liveSec, liveFmt := ComputeLiveFor(nextStatus, startedTime, pausedTime, pausedMs, now)

	// 5. Publish event_state message onto Redis pub/sub channel ps:ev:{id}
	msg := EventStateMessage{
		Type:             "event_state",
		EventID:          eventID,
		Status:           string(nextStatus),
		StartedAt:        startedAtStr,
		LiveForSeconds:   liveSec,
		LiveForFormatted: liveFmt,
		UpdatedAt:        now.Format(time.RFC3339),
	}
	payloadBytes, _ := json.Marshal(msg)
	channel := redisstore.EventChannelKey(eventID)
	_ = s.redisClient.Publish(ctx, channel, metrics.StampPublish(payloadBytes, time.Now())).Err()

	return string(nextStatus), nil
}
