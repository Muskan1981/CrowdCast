package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Seed populates the database with initial demonstration fixtures idempotently.
func Seed(ctx context.Context, pool *pgxpool.Pool) error {
	eventRepo := NewEventRepository(pool)
	pollRepo := NewPollRepository(pool)
	activityRepo := NewActivityRepository(pool)

	now := time.Now().UTC()
	startedAt := now.Add(-102 * time.Minute) // Started ~1h 42m ago for "01:42:18" display

	// Seed event: "After Hours, The Sound of Now" (#CC-0826, host Aanya Verma, Session 08)
	seededEvent := &Event{
		ID:               "cc-0826",
		EventNumber:      "EVENT #CC-0826",
		Title:            "After Hours — The Sound of Now",
		TitleMain:        "AFTER HOURS",
		Subtitle:         "THE SOUND OF NOW",
		SessionCode:      "SESSION 08",
		Description:      "An immersive night of electronic sounds, visual art, and people who get it.",
		HostName:         "Aanya Verma",
		HostAvatarLetter: "A",
		StageName:        "STAGE 01 / MAIN ROOM",
		StageResolution:  "1080p · 30 FPS",
		DateEyebrow:      "TUESDAY, NOVEMBER 17, 2026",
		Status:           "live",
		StartedAt:        &startedAt,
		PausedMs:         0,
		PeakAudience:     13204,
		CreatedAt:        startedAt,
		UpdatedAt:        now,
	}

	if err := eventRepo.Upsert(ctx, seededEvent); err != nil {
		return fmt.Errorf("failed to seed event: %w", err)
	}

	// Seed active poll: "What's the energy level right now?" with 3 options
	poll := &Poll{
		ID:        "poll-01",
		EventID:   "cc-0826",
		Question:  "What's the energy level right now?",
		Status:    "active",
		CreatedAt: now.Add(-30 * time.Minute),
	}

	options := []string{
		"Absolutely electric ⚡",
		"In the zone ✨",
		"Just warming up 🌱",
	}

	if err := pollRepo.Create(ctx, poll, options); err != nil {
		return fmt.Errorf("failed to seed poll: %w", err)
	}

	// Seed initial activity items for the Overview panel
	activities := []*ActivityItem{
		{
			EventID:      "cc-0826",
			ActivityType: "milestone",
			Title:        "Audience milestone reached",
			Description:  "12,000 attendees joined the room",
			Icon:         "Users",
			Tone:         "mint",
			CreatedAt:    now.Add(-2 * time.Minute),
		},
		{
			EventID:      "cc-0826",
			ActivityType: "spike",
			Title:        "Traffic spike detected",
			Description:  "Requests peaked at 1.4k req/s",
			Icon:         "Zap",
			Tone:         "peach",
			CreatedAt:    now.Add(-8 * time.Minute),
		},
		{
			EventID:      "cc-0826",
			ActivityType: "health",
			Title:        "All systems operational",
			Description:  "Health checks passed on 3 nodes",
			Icon:         "ShieldCheck",
			Tone:         "lilac",
			CreatedAt:    now.Add(-12 * time.Minute),
		},
	}

	for _, act := range activities {
		_ = activityRepo.Insert(ctx, act)
	}

	return nil
}
