package polls

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrNotFound       = errors.New("poll not found")
	ErrAlreadyVoted   = errors.New("user has already voted in this poll")
	ErrPollClosed     = errors.New("poll is closed")
	ErrInvalidOption  = errors.New("invalid poll option index")
)

type PollResponse struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Status      string   `json:"status"`
	Options     []string `json:"options"`
	Votes       []int    `json:"votes"`
	Percentages []int    `json:"percentages"`
	TotalVotes  int      `json:"total_votes"`
}

type PollUpdateMessage struct {
	Type        string `json:"t"`
	PollID      string `json:"poll_id"`
	Votes       []int  `json:"votes"`
	Percentages []int  `json:"percentages"`
	TotalVotes  int    `json:"total_votes"`
}

type Service struct {
	rdb     *redisstore.Client
	pollPg  postgres.PollRepository
	hub     *hub.Hub

	// Throttled broadcast per poll (max 2/sec -> 500ms throttle)
	broadcastMu sync.Mutex
	lastUpdate  map[string]time.Time
	pendingSync map[string]string // pollID -> eventID
}

func NewService(rdb *redisstore.Client, pollPg postgres.PollRepository, hubInstance *hub.Hub) *Service {
	s := &Service{
		rdb:         rdb,
		pollPg:      pollPg,
		hub:         hubInstance,
		lastUpdate:  make(map[string]time.Time),
		pendingSync: make(map[string]string),
	}
	return s
}

// GetActivePoll returns the active poll for an event with counts and percentages.
func (s *Service) GetActivePoll(ctx context.Context, eventID string) (*PollResponse, error) {
	if s.pollPg == nil {
		return nil, ErrNotFound
	}

	pgPoll, err := s.pollPg.GetActiveByEvent(ctx, eventID)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to fetch active poll: %w", err)
	}

	return s.populateResults(ctx, pgPoll)
}

// GetPoll returns details and current results for a specific poll by ID.
func (s *Service) GetPoll(ctx context.Context, pollID string) (*PollResponse, error) {
	if s.pollPg == nil {
		return nil, ErrNotFound
	}

	pgPoll, err := s.pollPg.Get(ctx, pollID)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to fetch poll: %w", err)
	}

	return s.populateResults(ctx, pgPoll)
}

func (s *Service) populateResults(ctx context.Context, p *postgres.PollWithDetails) (*PollResponse, error) {
	votesKey := redisstore.PollVotesKey(p.ID)
	var voteMap map[string]string
	var hasRedisVotes bool
	if s.rdb != nil {
		vm, err := s.rdb.HGetAll(ctx, votesKey).Result()
		if err == nil && len(vm) > 0 {
			voteMap = vm
			hasRedisVotes = true
		}
	}

	numOptions := len(p.Options)
	optionsText := make([]string, numOptions)
	votes := make([]int, numOptions)
	totalVotes := 0

	for i, opt := range p.Options {
		optionsText[i] = opt.OptionText
		if hasRedisVotes {
			if cntStr, ok := voteMap[strconv.Itoa(i)]; ok {
				if cnt, err := strconv.Atoi(cntStr); err == nil {
					votes[i] = cnt
				}
			}
		} else {
			// Populate from Postgres
			votes[i] = opt.VoteCount
			if s.rdb != nil {
				_ = s.rdb.HSet(ctx, votesKey, strconv.Itoa(i), opt.VoteCount).Err()
			}
		}
		totalVotes += votes[i]
	}

	percentages := make([]int, numOptions)
	for i := range votes {
		if totalVotes > 0 {
			percentages[i] = int(math.Round(float64(votes[i]) * 100.0 / float64(totalVotes)))
		} else {
			percentages[i] = 0
		}
	}

	return &PollResponse{
		ID:          p.ID,
		Question:    p.Question,
		Status:      p.Status,
		Options:     optionsText,
		Votes:       votes,
		Percentages: percentages,
		TotalVotes:  totalVotes,
	}, nil
}

// Vote records a vote, enforces single vote per user, updates Redis & Postgres, and throttles broadcasts.
func (s *Service) Vote(ctx context.Context, pollID, userID string, optionIndex int) (*PollResponse, error) {
	// 1. Fetch poll details
	pollDetails, err := s.GetPoll(ctx, pollID)
	if err != nil {
		return nil, err
	}
	if pollDetails.Status != "active" {
		return pollDetails, ErrPollClosed
	}
	if optionIndex < 0 || optionIndex >= len(pollDetails.Options) {
		return pollDetails, ErrInvalidOption
	}

	// 2. Redis atomic vote check & increment via Lua script
	votersKey := redisstore.PollVotersKey(pollID)
	votesKey := redisstore.PollVotesKey(pollID)

	scriptResult, err := redisstore.PollVoteScript.Run(ctx, s.rdb, []string{votersKey, votesKey}, userID, optionIndex).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("failed to execute poll vote script: %w", err)
	}

	// Check if already voted
	if res, ok := scriptResult.(int64); ok && res == 0 {
		return pollDetails, ErrAlreadyVoted
	}

	// 3. Durable persistence in Postgres
	if s.pollPg != nil {
		if err := s.pollPg.Vote(ctx, pollID, userID, optionIndex); err != nil {
			if errors.Is(err, postgres.ErrAlreadyVoted) {
				return pollDetails, ErrAlreadyVoted
			}
			slog.Error("failed to persist poll vote in postgres", "poll_id", pollID, "user_id", userID, "error", err)
		}
	}

	// 4. Recalculate results
	updated, err := s.GetPoll(ctx, pollID)
	if err != nil {
		return pollDetails, nil
	}

	// 5. Throttled broadcast (max 2/sec -> 500ms interval)
	s.throttledBroadcast(pollID, updated)

	return updated, nil
}

func (s *Service) throttledBroadcast(pollID string, results *PollResponse) {
	s.broadcastMu.Lock()
	now := time.Now()
	last := s.lastUpdate[pollID]
	canSend := now.Sub(last) >= 500*time.Millisecond
	if canSend {
		s.lastUpdate[pollID] = now
	}
	s.broadcastMu.Unlock()

	if canSend && s.hub != nil {
		msg := PollUpdateMessage{
			Type:        "poll_update",
			PollID:      pollID,
			Votes:       results.Votes,
			Percentages: results.Percentages,
			TotalVotes:  results.TotalVotes,
		}
		// Broadcast across room
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			// Find event room or broadcast via hub
			if p, err := s.pollPg.Get(ctx, pollID); err == nil && p != nil {
				_ = s.hub.PublishEventMessage(ctx, p.EventID, msg)
			}
		}()
	}
}

// Admin: CreatePoll creates a new poll with given options.
func (s *Service) CreatePoll(ctx context.Context, eventID, question string, options []string) (*PollResponse, error) {
	if s.pollPg == nil {
		return nil, errors.New("postgres not connected")
	}

	pollID := "poll-" + uuid.New().String()[:8]
	pollModel := &postgres.Poll{
		ID:        pollID,
		EventID:   eventID,
		Question:  question,
		Status:    "scheduled",
		CreatedAt: time.Now().UTC(),
	}

	if err := s.pollPg.Create(ctx, pollModel, options); err != nil {
		return nil, fmt.Errorf("failed to create poll: %w", err)
	}

	return s.GetPoll(ctx, pollID)
}

// Admin: ActivatePoll activates a poll.
func (s *Service) ActivatePoll(ctx context.Context, pollID string) error {
	if s.pollPg == nil {
		return errors.New("postgres not connected")
	}
	return s.pollPg.Activate(ctx, pollID)
}

// Admin: ClosePoll closes an active poll.
func (s *Service) ClosePoll(ctx context.Context, pollID string) error {
	if s.pollPg == nil {
		return errors.New("postgres not connected")
	}
	return s.pollPg.Close(ctx, pollID)
}
