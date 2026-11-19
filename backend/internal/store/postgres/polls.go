package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PollRepository interface {
	GetActiveByEvent(ctx context.Context, eventID string) (*PollWithDetails, error)
	Get(ctx context.Context, id string) (*PollWithDetails, error)
	Create(ctx context.Context, poll *Poll, options []string) error
	Vote(ctx context.Context, pollID, userID string, optionIndex int) error
	HasUserVoted(ctx context.Context, pollID, userID string) (bool, int, error)
	Activate(ctx context.Context, id string) error
	Close(ctx context.Context, id string) error
}

type pollRepo struct {
	pool *pgxpool.Pool
}

func NewPollRepository(pool *pgxpool.Pool) PollRepository {
	return &pollRepo{pool: pool}
}

func (r *pollRepo) GetActiveByEvent(ctx context.Context, eventID string) (*PollWithDetails, error) {
	query := `
		SELECT id, event_id, question, status, created_at, closed_at
		FROM polls
		WHERE event_id = $1 AND status = 'active'
		ORDER BY created_at DESC
		LIMIT 1`

	var p Poll
	err := r.pool.QueryRow(ctx, query, eventID).Scan(
		&p.ID, &p.EventID, &p.Question, &p.Status, &p.CreatedAt, &p.ClosedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query active poll failed: %w", err)
	}

	return r.populatePollDetails(ctx, p)
}

func (r *pollRepo) Get(ctx context.Context, id string) (*PollWithDetails, error) {
	query := `
		SELECT id, event_id, question, status, created_at, closed_at
		FROM polls
		WHERE id = $1`

	var p Poll
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.EventID, &p.Question, &p.Status, &p.CreatedAt, &p.ClosedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query poll failed: %w", err)
	}

	return r.populatePollDetails(ctx, p)
}

func (r *pollRepo) populatePollDetails(ctx context.Context, p Poll) (*PollWithDetails, error) {
	optQuery := `
		SELECT id, poll_id, option_index, option_text, vote_count, created_at
		FROM poll_options
		WHERE poll_id = $1
		ORDER BY option_index ASC`

	rows, err := r.pool.Query(ctx, optQuery, p.ID)
	if err != nil {
		return nil, fmt.Errorf("query poll options failed: %w", err)
	}
	defer rows.Close()

	var options []PollOption
	var votes []int
	totalVotes := 0

	for rows.Next() {
		var opt PollOption
		if err := rows.Scan(&opt.ID, &opt.PollID, &opt.OptionIndex, &opt.OptionText, &opt.VoteCount, &opt.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan poll option failed: %w", err)
		}
		options = append(options, opt)
		votes = append(votes, opt.VoteCount)
		totalVotes += opt.VoteCount
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error for poll options: %w", err)
	}

	percentages := make([]int, len(votes))
	if totalVotes > 0 {
		for i, v := range votes {
			percentages[i] = int((float64(v) / float64(totalVotes)) * 100)
		}
	}

	return &PollWithDetails{
		Poll:        p,
		Options:     options,
		Votes:       votes,
		Percentages: percentages,
		TotalVotes:  totalVotes,
	}, nil
}

func (r *pollRepo) Create(ctx context.Context, poll *Poll, options []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction failed: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	pollQuery := `
		INSERT INTO polls (id, event_id, question, status, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			question = EXCLUDED.question,
			status = EXCLUDED.status`

	now := time.Now().UTC()
	if poll.CreatedAt.IsZero() {
		poll.CreatedAt = now
	}

	_, err = tx.Exec(ctx, pollQuery, poll.ID, poll.EventID, poll.Question, poll.Status, poll.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert poll failed: %w", err)
	}

	optQuery := `
		INSERT INTO poll_options (poll_id, option_index, option_text, vote_count, created_at)
		VALUES ($1, $2, $3, 0, $4)
		ON CONFLICT (poll_id, option_index) DO UPDATE SET
			option_text = EXCLUDED.option_text`

	for idx, optText := range options {
		_, err = tx.Exec(ctx, optQuery, poll.ID, idx, optText, now)
		if err != nil {
			return fmt.Errorf("insert poll option %d failed: %w", idx, err)
		}
	}

	return tx.Commit(ctx)
}

func (r *pollRepo) Vote(ctx context.Context, pollID, userID string, optionIndex int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin vote transaction failed: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Check poll status
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM polls WHERE id = $1`, pollID).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("check poll status failed: %w", err)
	}
	if status != "active" {
		return ErrPollClosed
	}

	// Insert vote (enforces one vote per user per poll via unique primary key)
	voteQuery := `INSERT INTO poll_votes (poll_id, user_id, option_index, created_at) VALUES ($1, $2, $3, NOW())`
	_, err = tx.Exec(ctx, voteQuery, pollID, userID, optionIndex)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return ErrAlreadyVoted
		}
		return fmt.Errorf("insert poll vote failed: %w", err)
	}

	// Increment option counter
	updateOptQuery := `UPDATE poll_options SET vote_count = vote_count + 1 WHERE poll_id = $1 AND option_index = $2`
	tag, err := tx.Exec(ctx, updateOptQuery, pollID, optionIndex)
	if err != nil {
		return fmt.Errorf("increment vote count failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("invalid option index %d for poll %s", optionIndex, pollID)
	}

	return tx.Commit(ctx)
}

func (r *pollRepo) HasUserVoted(ctx context.Context, pollID, userID string) (bool, int, error) {
	query := `SELECT option_index FROM poll_votes WHERE poll_id = $1 AND user_id = $2`
	var optionIndex int
	err := r.pool.QueryRow(ctx, query, pollID, userID).Scan(&optionIndex)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, -1, nil
		}
		return false, -1, fmt.Errorf("check user vote failed: %w", err)
	}
	return true, optionIndex, nil
}

func (r *pollRepo) Activate(ctx context.Context, id string) error {
	query := `UPDATE polls SET status = 'active' WHERE id = $1`
	tag, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("activate poll failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *pollRepo) Close(ctx context.Context, id string) error {
	query := `UPDATE polls SET status = 'closed', closed_at = NOW() WHERE id = $1`
	tag, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("close poll failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
