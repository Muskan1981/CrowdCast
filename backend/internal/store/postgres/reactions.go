package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ReactionRepository interface {
	RecordRollup(ctx context.Context, rollup *ReactionRollup) error
	GetTopReactions(ctx context.Context, eventID string) ([]*ReactionTotal, error)
}

type reactionRepo struct {
	pool *pgxpool.Pool
}

func NewReactionRepository(pool *pgxpool.Pool) ReactionRepository {
	return &reactionRepo{pool: pool}
}

func (r *reactionRepo) RecordRollup(ctx context.Context, rollup *ReactionRollup) error {
	query := `
		INSERT INTO reaction_rollups (event_id, minute_bucket, emoji, count, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (event_id, minute_bucket, emoji) DO UPDATE SET
			count = reaction_rollups.count + EXCLUDED.count`

	now := time.Now().UTC()
	if rollup.CreatedAt.IsZero() {
		rollup.CreatedAt = now
	}

	_, err := r.pool.Exec(ctx, query, rollup.EventID, rollup.MinuteBucket, rollup.Emoji, rollup.Count, rollup.CreatedAt)
	if err != nil {
		return fmt.Errorf("record reaction rollup failed: %w", err)
	}
	return nil
}

func (r *reactionRepo) GetTopReactions(ctx context.Context, eventID string) ([]*ReactionTotal, error) {
	query := `
		SELECT emoji, COALESCE(SUM(count), 0) AS total
		FROM reaction_rollups
		WHERE event_id = $1
		GROUP BY emoji
		ORDER BY total DESC
		LIMIT 5`

	rows, err := r.pool.Query(ctx, query, eventID)
	if err != nil {
		return nil, fmt.Errorf("query top reactions failed: %w", err)
	}
	defer rows.Close()

	var totals []*ReactionTotal
	for rows.Next() {
		var rt ReactionTotal
		if err := rows.Scan(&rt.Emoji, &rt.Total); err != nil {
			return nil, fmt.Errorf("scan top reaction failed: %w", err)
		}
		totals = append(totals, &rt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error for top reactions: %w", err)
	}

	return totals, nil
}
