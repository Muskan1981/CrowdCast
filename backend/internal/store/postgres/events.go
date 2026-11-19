package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventRepository interface {
	Get(ctx context.Context, id string) (*Event, error)
	Upsert(ctx context.Context, event *Event) error
	UpdateStatus(ctx context.Context, id string, status string) error
	UpdatePeakAudience(ctx context.Context, id string, peak int) error
}

type eventRepo struct {
	pool *pgxpool.Pool
}

func NewEventRepository(pool *pgxpool.Pool) EventRepository {
	return &eventRepo{pool: pool}
}

func (r *eventRepo) Get(ctx context.Context, id string) (*Event, error) {
	query := `
		SELECT id, event_number, title, title_main, subtitle, session_code, description,
		       host_name, host_avatar_letter, stage_name, stage_resolution, date_eyebrow,
		       status, started_at, ended_at, paused_ms, peak_audience, created_at, updated_at
		FROM events
		WHERE id = $1`

	var ev Event
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&ev.ID, &ev.EventNumber, &ev.Title, &ev.TitleMain, &ev.Subtitle, &ev.SessionCode, &ev.Description,
		&ev.HostName, &ev.HostAvatarLetter, &ev.StageName, &ev.StageResolution, &ev.DateEyebrow,
		&ev.Status, &ev.StartedAt, &ev.EndedAt, &ev.PausedMs, &ev.PeakAudience, &ev.CreatedAt, &ev.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query event failed: %w", err)
	}

	return &ev, nil
}

func (r *eventRepo) Upsert(ctx context.Context, ev *Event) error {
	query := `
		INSERT INTO events (
			id, event_number, title, title_main, subtitle, session_code, description,
			host_name, host_avatar_letter, stage_name, stage_resolution, date_eyebrow,
			status, started_at, ended_at, paused_ms, peak_audience, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18, $19
		)
		ON CONFLICT (id) DO UPDATE SET
			event_number = EXCLUDED.event_number,
			title = EXCLUDED.title,
			title_main = EXCLUDED.title_main,
			subtitle = EXCLUDED.subtitle,
			session_code = EXCLUDED.session_code,
			description = EXCLUDED.description,
			host_name = EXCLUDED.host_name,
			host_avatar_letter = EXCLUDED.host_avatar_letter,
			stage_name = EXCLUDED.stage_name,
			stage_resolution = EXCLUDED.stage_resolution,
			date_eyebrow = EXCLUDED.date_eyebrow,
			status = EXCLUDED.status,
			started_at = COALESCE(EXCLUDED.started_at, events.started_at),
			ended_at = EXCLUDED.ended_at,
			paused_ms = EXCLUDED.paused_ms,
			peak_audience = GREATEST(events.peak_audience, EXCLUDED.peak_audience),
			updated_at = NOW()`

	now := time.Now().UTC()
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = now
	}
	ev.UpdatedAt = now

	_, err := r.pool.Exec(ctx, query,
		ev.ID, ev.EventNumber, ev.Title, ev.TitleMain, ev.Subtitle, ev.SessionCode, ev.Description,
		ev.HostName, ev.HostAvatarLetter, ev.StageName, ev.StageResolution, ev.DateEyebrow,
		ev.Status, ev.StartedAt, ev.EndedAt, ev.PausedMs, ev.PeakAudience, ev.CreatedAt, ev.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert event failed: %w", err)
	}
	return nil
}

func (r *eventRepo) UpdateStatus(ctx context.Context, id string, status string) error {
	query := `UPDATE events SET status = $1, updated_at = NOW() WHERE id = $2`
	tag, err := r.pool.Exec(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("update event status failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *eventRepo) UpdatePeakAudience(ctx context.Context, id string, peak int) error {
	query := `UPDATE events SET peak_audience = GREATEST(peak_audience, $1), updated_at = NOW() WHERE id = $2`
	tag, err := r.pool.Exec(ctx, query, peak, id)
	if err != nil {
		return fmt.Errorf("update peak audience failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
