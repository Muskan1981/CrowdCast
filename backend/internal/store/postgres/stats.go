package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StatsRepository answers the audience aggregates that need SQL. Live counters
// come from Redis; these are durable, so they are computed from attendance_sessions.
type StatsRepository interface {
	TotalAttendees(ctx context.Context, eventID string) (int64, error)
	ReturningPercent(ctx context.Context, eventID string) (float64, error)
	AvgWatchSeconds(ctx context.Context, eventID string) (float64, error)
}

type statsRepo struct {
	pool *pgxpool.Pool
}

func NewStatsRepository(pool *pgxpool.Pool) StatsRepository {
	return &statsRepo{pool: pool}
}

// TotalAttendees counts distinct viewers who ever joined the event.
func (r *statsRepo) TotalAttendees(ctx context.Context, eventID string) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT viewer_id) FROM attendance_sessions WHERE event_id = $1`,
		eventID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count attendees: %w", err)
	}
	return n, nil
}

// ReturningPercent is the share of this event's viewers who have more than one
// attendance session across all events (0-100).
func (r *statsRepo) ReturningPercent(ctx context.Context, eventID string) (float64, error) {
	query := `
		WITH audience AS (
			SELECT DISTINCT viewer_id FROM attendance_sessions WHERE event_id = $1
		)
		SELECT COALESCE(
			100.0 * COUNT(*) FILTER (WHERE sc.n > 1) / NULLIF(COUNT(*), 0),
			0
		)::float8
		FROM audience a
		CROSS JOIN LATERAL (
			SELECT COUNT(*) AS n FROM attendance_sessions s WHERE s.viewer_id = a.viewer_id
		) sc`

	var pct float64
	if err := r.pool.QueryRow(ctx, query, eventID).Scan(&pct); err != nil {
		return 0, fmt.Errorf("returning audience: %w", err)
	}
	return pct, nil
}

// AvgWatchSeconds averages session length for this event. Sessions that are
// still open are measured up to now.
func (r *statsRepo) AvgWatchSeconds(ctx context.Context, eventID string) (float64, error) {
	var avg float64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (COALESCE(left_at, NOW()) - joined_at))), 0)::float8
		FROM attendance_sessions
		WHERE event_id = $1`,
		eventID,
	).Scan(&avg)
	if err != nil {
		return 0, fmt.Errorf("average watch time: %w", err)
	}
	return avg, nil
}
