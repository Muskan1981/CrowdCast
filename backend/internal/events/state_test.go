package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransitionTable(t *testing.T) {
	tests := []struct {
		name        string
		current     EventStatus
		action      EventAction
		expected    EventStatus
		expectError bool
	}{
		// From Scheduled
		{"start from scheduled", StatusScheduled, ActionStart, StatusLive, false},
		{"pause from scheduled", StatusScheduled, ActionPause, "", true},
		{"resume from scheduled", StatusScheduled, ActionResume, "", true},
		{"end from scheduled", StatusScheduled, ActionEnd, "", true},

		// From Live
		{"start from live", StatusLive, ActionStart, "", true},
		{"pause from live", StatusLive, ActionPause, StatusPaused, false},
		{"resume from live", StatusLive, ActionResume, "", true},
		{"end from live", StatusLive, ActionEnd, StatusEnded, false},

		// From Paused
		{"start from paused", StatusPaused, ActionStart, "", true},
		{"pause from paused", StatusPaused, ActionPause, "", true},
		{"resume from paused", StatusPaused, ActionResume, StatusLive, false},
		{"end from paused", StatusPaused, ActionEnd, StatusEnded, false},

		// From Ended (terminal)
		{"start from ended", StatusEnded, ActionStart, "", true},
		{"pause from ended", StatusEnded, ActionPause, "", true},
		{"resume from ended", StatusEnded, ActionResume, "", true},
		{"end from ended", StatusEnded, ActionEnd, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, err := Transition(tt.current, tt.action)
			if tt.expectError {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidTransition)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, next)
			}
		})
	}
}

func TestComputeLiveFor(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-1 * time.Hour) // 1 hour ago (3600s)

	t.Run("scheduled status returns zero", func(t *testing.T) {
		sec, fmtStr := ComputeLiveFor(StatusScheduled, &startedAt, nil, 0, now)
		assert.Equal(t, int64(0), sec)
		assert.Equal(t, "00:00:00", fmtStr)
	})

	t.Run("live status without pause", func(t *testing.T) {
		sec, fmtStr := ComputeLiveFor(StatusLive, &startedAt, nil, 0, now)
		assert.Equal(t, int64(3600), sec)
		assert.Equal(t, "01:00:00", fmtStr)
	})

	t.Run("live status with accumulated paused_ms", func(t *testing.T) {
		// 1 hour ago, but paused for 10 minutes (600,000 ms) -> 50 min (3000s)
		sec, fmtStr := ComputeLiveFor(StatusLive, &startedAt, nil, 600000, now)
		assert.Equal(t, int64(3000), sec)
		assert.Equal(t, "00:50:00", fmtStr)
	})

	t.Run("paused status uses paused_at", func(t *testing.T) {
		pausedAt := startedAt.Add(30 * time.Minute) // paused after 30 min
		sec, fmtStr := ComputeLiveFor(StatusPaused, &startedAt, &pausedAt, 0, now)
		assert.Equal(t, int64(1800), sec)
		assert.Equal(t, "00:30:00", fmtStr)
	})
}
