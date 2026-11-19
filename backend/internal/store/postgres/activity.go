package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ActivityRepository interface {
	Insert(ctx context.Context, item *ActivityItem) error
	ListRecent(ctx context.Context, eventID string, limit int) ([]*ActivityItem, error)
}

type activityRepo struct {
	pool *pgxpool.Pool
}

func NewActivityRepository(pool *pgxpool.Pool) ActivityRepository {
	return &activityRepo{pool: pool}
}

func (r *activityRepo) Insert(ctx context.Context, item *ActivityItem) error {
	query := `
		INSERT INTO activity_log (event_id, activity_type, title, description, icon, tone, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}

	var id int64
	err := r.pool.QueryRow(ctx, query,
		item.EventID, item.ActivityType, item.Title, item.Description, item.Icon, item.Tone, item.CreatedAt,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("insert activity item failed: %w", err)
	}
	item.ID = id
	return nil
}

func (r *activityRepo) ListRecent(ctx context.Context, eventID string, limit int) ([]*ActivityItem, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	query := `
		SELECT id, event_id, activity_type, title, description, icon, tone, created_at
		FROM activity_log
		WHERE event_id = $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := r.pool.Query(ctx, query, eventID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent activity failed: %w", err)
	}
	defer rows.Close()

	var items []*ActivityItem
	for rows.Next() {
		var item ActivityItem
		if err := rows.Scan(
			&item.ID, &item.EventID, &item.ActivityType, &item.Title, &item.Description, &item.Icon, &item.Tone, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan activity item failed: %w", err)
		}
		items = append(items, &item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error for activity items: %w", err)
	}

	return items, nil
}
