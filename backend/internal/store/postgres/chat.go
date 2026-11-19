package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ChatRepository interface {
	Insert(ctx context.Context, msg *ChatMessage) error
	ListRecent(ctx context.Context, eventID string, limit int) ([]*ChatMessage, error)
	Count(ctx context.Context, eventID string) (int64, error)
}

type chatRepo struct {
	pool *pgxpool.Pool
}

func NewChatRepository(pool *pgxpool.Pool) ChatRepository {
	return &chatRepo{pool: pool}
}

func (r *chatRepo) Insert(ctx context.Context, msg *ChatMessage) error {
	query := `
		INSERT INTO chat_messages (event_id, user_id, user_name, user_color, text, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}

	var id int64
	err := r.pool.QueryRow(ctx, query, msg.EventID, msg.UserID, msg.UserName, msg.UserColor, msg.Text, msg.CreatedAt).Scan(&id)
	if err != nil {
		return fmt.Errorf("insert chat message failed: %w", err)
	}
	msg.ID = id
	return nil
}

func (r *chatRepo) ListRecent(ctx context.Context, eventID string, limit int) ([]*ChatMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	query := `
		SELECT id, event_id, user_id, user_name, user_color, text, created_at
		FROM (
			SELECT id, event_id, user_id, user_name, user_color, text, created_at
			FROM chat_messages
			WHERE event_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		) sub
		ORDER BY created_at ASC`

	rows, err := r.pool.Query(ctx, query, eventID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent chat failed: %w", err)
	}
	defer rows.Close()

	var messages []*ChatMessage
	for rows.Next() {
		var msg ChatMessage
		if err := rows.Scan(&msg.ID, &msg.EventID, &msg.UserID, &msg.UserName, &msg.UserColor, &msg.Text, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chat message failed: %w", err)
		}
		messages = append(messages, &msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error for chat messages: %w", err)
	}

	return messages, nil
}

func (r *chatRepo) Count(ctx context.Context, eventID string) (int64, error) {
	query := `SELECT COUNT(*) FROM chat_messages WHERE event_id = $1`
	var count int64
	err := r.pool.QueryRow(ctx, query, eventID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count chat messages failed: %w", err)
	}
	return count, nil
}
