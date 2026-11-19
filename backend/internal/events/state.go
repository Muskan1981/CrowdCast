package events

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidTransition = errors.New("invalid event state transition")
	ErrEventNotFound     = errors.New("event not found")
)

type EventStatus string

const (
	StatusScheduled EventStatus = "scheduled"
	StatusLive      EventStatus = "live"
	StatusPaused    EventStatus = "paused"
	StatusEnded     EventStatus = "ended"
)

type EventAction string

const (
	ActionStart  EventAction = "start"
	ActionPause  EventAction = "pause"
	ActionResume EventAction = "resume"
	ActionEnd    EventAction = "end"
)

// Transition calculates the next status given current status and requested action.
// Returns ErrInvalidTransition if the transition is prohibited.
func Transition(current EventStatus, action EventAction) (EventStatus, error) {
	switch current {
	case StatusScheduled:
		if action == ActionStart {
			return StatusLive, nil
		}
	case StatusLive:
		if action == ActionPause {
			return StatusPaused, nil
		}
		if action == ActionEnd {
			return StatusEnded, nil
		}
	case StatusPaused:
		if action == ActionResume {
			return StatusLive, nil
		}
		if action == ActionEnd {
			return StatusEnded, nil
		}
	case StatusEnded:
		// Terminal state, cannot transition anywhere
		return "", fmt.Errorf("%w: cannot transition from ended status", ErrInvalidTransition)
	}

	return "", fmt.Errorf("%w: action %q is not allowed when event is in %q status", ErrInvalidTransition, action, current)
}

// ComputeLiveFor calculates elapsed live seconds and formatted HH:MM:SS.
func ComputeLiveFor(status EventStatus, startedAt *time.Time, pausedAt *time.Time, pausedMs int64, now time.Time) (int64, string) {
	if startedAt == nil || startedAt.IsZero() || status == StatusScheduled {
		return 0, "00:00:00"
	}

	var elapsed time.Duration
	if status == StatusPaused && pausedAt != nil && !pausedAt.IsZero() {
		elapsed = pausedAt.Sub(*startedAt) - time.Duration(pausedMs)*time.Millisecond
	} else {
		elapsed = now.Sub(*startedAt) - time.Duration(pausedMs)*time.Millisecond
	}

	if elapsed < 0 {
		elapsed = 0
	}

	totalSeconds := int64(elapsed.Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	formatted := fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	return totalSeconds, formatted
}
