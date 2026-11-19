// Package stats computes the Overview, Traffic and Audience numbers that the
// dashboard shows, pushes live metric cards over WebSocket, and runs the
// activity detector on the leader instance.
package stats

import (
	"fmt"
	"math"
)

// Tone values understood by the frontend metric cards.
const (
	ToneUp   = "up"
	ToneDown = "down"
	ToneFlat = "flat"
)

// PercentChange returns how much current moved relative to previous, as a
// percentage. ok is false when no percentage can be shown.
func PercentChange(current, previous float64) (pct float64, ok bool) {
	// TODO(human): decide what to report when previous == 0. A baseline of
	// zero makes the ratio undefined: the card could show "new" or "—", or a
	// different rule could apply to counts (reactions) than to rates (req/s).
	// Replace the placeholder below with your policy, then keep the return
	// shape (pct, ok) so FormatChange and ToneFor still work.
	if previous == 0 {
		return 0, false
	}
	return (current - previous) / previous * 100, true
}

// FormatChange renders a delta for the card's "change" field: "+12%", "-3%",
// or "—" when no percentage is available.
func FormatChange(pct float64, ok bool) string {
	if !ok {
		return "—"
	}
	rounded := math.Round(pct)
	if rounded == 0 {
		return "0%"
	}
	return fmt.Sprintf("%+.0f%%", rounded)
}

// ToneFor maps a delta to a tone. higherIsBad inverts the colour, which is
// right for latency: a rise there should look like a warning.
func ToneFor(pct float64, ok bool, higherIsBad bool) string {
	if !ok || math.Abs(pct) < 0.5 {
		return ToneFlat
	}
	up := pct > 0
	if higherIsBad {
		up = !up
	}
	if up {
		return ToneUp
	}
	return ToneDown
}
