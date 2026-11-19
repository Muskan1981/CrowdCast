package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/crowdcast/crowdcast/internal/store/postgres"
)

func setupTestDB(t *testing.T) (*pgxpool.Pool, string) {
	ctx := context.Background()

	// Spin up isolated PostgreSQL 16 container with proper wait strategy
	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("crowdcast_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = pgContainer.Terminate(ctx)
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Apply migrations
	err = postgres.MigrateUp(connStr)
	require.NoError(t, err)

	pool, err := postgres.NewPool(ctx, connStr)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Close()
	})

	return pool, connStr
}

func TestPostgresRepositoriesIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	pool, _ := setupTestDB(t)

	// 1. Test Event Repository
	t.Run("EventRepository", func(t *testing.T) {
		repo := postgres.NewEventRepository(pool)
		now := time.Now().UTC()

		ev := &postgres.Event{
			ID:               "ev-test-01",
			EventNumber:      "EVENT #TEST-01",
			Title:            "Test Concert",
			TitleMain:        "TEST CONCERT",
			Subtitle:         "LIVE SESSION",
			SessionCode:      "S01",
			Description:      "A test event",
			HostName:         "DJ Test",
			HostAvatarLetter: "T",
			StageName:        "MAIN STAGE",
			StageResolution:  "1080p",
			DateEyebrow:      "TODAY",
			Status:           "scheduled",
			StartedAt:        &now,
			PausedMs:         0,
			PeakAudience:     500,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		err := repo.Upsert(ctx, ev)
		require.NoError(t, err)

		fetched, err := repo.Get(ctx, ev.ID)
		require.NoError(t, err)
		assert.Equal(t, ev.Title, fetched.Title)
		assert.Equal(t, "scheduled", fetched.Status)
		assert.Equal(t, 500, fetched.PeakAudience)

		err = repo.UpdateStatus(ctx, ev.ID, "live")
		require.NoError(t, err)

		err = repo.UpdatePeakAudience(ctx, ev.ID, 1200)
		require.NoError(t, err)

		fetchedAfter, err := repo.Get(ctx, ev.ID)
		require.NoError(t, err)
		assert.Equal(t, "live", fetchedAfter.Status)
		assert.Equal(t, 1200, fetchedAfter.PeakAudience)
	})

	// 2. Test Poll Repository (including single vote constraint)
	t.Run("PollRepository", func(t *testing.T) {
		repo := postgres.NewPollRepository(pool)

		poll := &postgres.Poll{
			ID:       "poll-test-01",
			EventID:  "ev-test-01",
			Question: "How is the music?",
			Status:   "active",
		}
		options := []string{"Fire 🔥", "Decent 👌", "Nah 👎"}

		err := repo.Create(ctx, poll, options)
		require.NoError(t, err)

		active, err := repo.GetActiveByEvent(ctx, "ev-test-01")
		require.NoError(t, err)
		assert.Equal(t, poll.ID, active.ID)
		assert.Equal(t, 3, len(active.Options))
		assert.Equal(t, 0, active.TotalVotes)

		// User 1 votes for option 0
		err = repo.Vote(ctx, poll.ID, "user-01", 0)
		require.NoError(t, err)

		// Duplicate vote from user 1 should fail
		err = repo.Vote(ctx, poll.ID, "user-01", 1)
		require.ErrorIs(t, err, postgres.ErrAlreadyVoted)

		// User 2 votes for option 1
		err = repo.Vote(ctx, poll.ID, "user-02", 1)
		require.NoError(t, err)

		hasVoted, optIdx, err := repo.HasUserVoted(ctx, poll.ID, "user-01")
		require.NoError(t, err)
		assert.True(t, hasVoted)
		assert.Equal(t, 0, optIdx)

		pollDetails, err := repo.Get(ctx, poll.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, pollDetails.TotalVotes)
		assert.Equal(t, 1, pollDetails.Votes[0])
		assert.Equal(t, 1, pollDetails.Votes[1])
		assert.Equal(t, 0, pollDetails.Votes[2])
		assert.Equal(t, 50, pollDetails.Percentages[0])
		assert.Equal(t, 50, pollDetails.Percentages[1])

		// Close poll
		err = repo.Close(ctx, poll.ID)
		require.NoError(t, err)

		// Voting on closed poll should fail
		err = repo.Vote(ctx, poll.ID, "user-03", 0)
		require.ErrorIs(t, err, postgres.ErrPollClosed)
	})

	// 3. Test Viewer & Attendance
	t.Run("ViewerRepository", func(t *testing.T) {
		repo := postgres.NewViewerRepository(pool)

		v := &postgres.Viewer{
			ID:          "usr-01",
			DisplayName: "Aarav",
			City:        "Mumbai",
			Color:       "mint",
		}

		err := repo.Upsert(ctx, v)
		require.NoError(t, err)

		gotViewer, err := repo.Get(ctx, v.ID)
		require.NoError(t, err)
		assert.Equal(t, "Aarav", gotViewer.DisplayName)
		assert.Equal(t, "Mumbai", gotViewer.City)

		sessionID, err := repo.RecordAttendance(ctx, &postgres.AttendanceSession{
			ViewerID: v.ID,
			EventID:  "ev-test-01",
		})
		require.NoError(t, err)
		assert.Greater(t, sessionID, int64(0))

		err = repo.EndAttendance(ctx, sessionID)
		require.NoError(t, err)

		cityMix, err := repo.GetCityMix(ctx, "ev-test-01")
		require.NoError(t, err)
		assert.NotEmpty(t, cityMix)
	})

	// 4. Test Chat Repository
	t.Run("ChatRepository", func(t *testing.T) {
		repo := postgres.NewChatRepository(pool)

		msg := &postgres.ChatMessage{
			EventID:   "ev-test-01",
			UserID:    "usr-01",
			UserName:  "Aarav",
			UserColor: "mint",
			Text:      "Crowd is electric!",
		}

		err := repo.Insert(ctx, msg)
		require.NoError(t, err)
		assert.Greater(t, msg.ID, int64(0))

		messages, err := repo.ListRecent(ctx, "ev-test-01", 10)
		require.NoError(t, err)
		assert.Len(t, messages, 1)
		assert.Equal(t, "Crowd is electric!", messages[0].Text)

		count, err := repo.Count(ctx, "ev-test-01")
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})

	// 5. Test Reaction Rollup
	t.Run("ReactionRepository", func(t *testing.T) {
		repo := postgres.NewReactionRepository(pool)
		nowBucket := time.Now().UTC().Truncate(time.Minute)

		err := repo.RecordRollup(ctx, &postgres.ReactionRollup{
			EventID:      "ev-test-01",
			MinuteBucket: nowBucket,
			Emoji:        "🔥",
			Count:        15,
		})
		require.NoError(t, err)

		// Increment same bucket
		err = repo.RecordRollup(ctx, &postgres.ReactionRollup{
			EventID:      "ev-test-01",
			MinuteBucket: nowBucket,
			Emoji:        "🔥",
			Count:        10,
		})
		require.NoError(t, err)

		totals, err := repo.GetTopReactions(ctx, "ev-test-01")
		require.NoError(t, err)
		require.NotEmpty(t, totals)
		assert.Equal(t, "🔥", totals[0].Emoji)
		assert.Equal(t, int64(25), totals[0].Total)
	})

	// 6. Test Activity Repository
	t.Run("ActivityRepository", func(t *testing.T) {
		repo := postgres.NewActivityRepository(pool)

		item := &postgres.ActivityItem{
			EventID:      "ev-test-01",
			ActivityType: "milestone",
			Title:        "1,000 attendees",
			Description:  "Milestone reached",
			Icon:         "Users",
			Tone:         "mint",
		}

		err := repo.Insert(ctx, item)
		require.NoError(t, err)
		assert.Greater(t, item.ID, int64(0))

		items, err := repo.ListRecent(ctx, "ev-test-01", 5)
		require.NoError(t, err)
		assert.NotEmpty(t, items)
		assert.Equal(t, "1,000 attendees", items[0].Title)
	})

	// 7. Test Seed Fixtures
	t.Run("SeedFixtures", func(t *testing.T) {
		err := postgres.Seed(ctx, pool)
		require.NoError(t, err)

		eventRepo := postgres.NewEventRepository(pool)
		ev, err := eventRepo.Get(ctx, "cc-0826")
		require.NoError(t, err)
		assert.Equal(t, "After Hours — The Sound of Now", ev.Title)
		assert.Equal(t, "EVENT #CC-0826", ev.EventNumber)
		assert.Equal(t, "Aanya Verma", ev.HostName)

		pollRepo := postgres.NewPollRepository(pool)
		poll, err := pollRepo.GetActiveByEvent(ctx, "cc-0826")
		require.NoError(t, err)
		assert.Equal(t, "What's the energy level right now?", poll.Question)
		assert.Equal(t, 3, len(poll.Options))
		assert.Equal(t, "Absolutely electric ⚡", poll.Options[0].OptionText)
	})
}
