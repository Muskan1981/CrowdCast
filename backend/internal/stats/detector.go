package stats

import (
	"fmt"
	"sync"
	"time"
)

// Detector turns a stream of snapshots into activity-feed announcements. It is
// pure logic (no I/O), so the leader can feed it snapshots and persist the output.
//
// Per event it keeps a rolling window of request rates. A spike is a rate above
// spikeFactor times that window's mean, after a warm-up period, with a cooldown
// so one burst is announced once.
type Detector struct {
	spikeFactor float64
	windowSize  int
	warmup      int
	cooldown    time.Duration
	milestone   int64

	mu     sync.Mutex
	states map[string]*rateWindow
}

type rateWindow struct {
	samples   []float64
	lastSpike time.Time
}

// Announcement is one activity-feed entry the detector wants recorded.
type Announcement struct {
	Kind        string // "milestone" | "spike"
	Title       string
	Description string
	Icon        string
	Tone        string
	Milestone   int64 // set on milestone announcements so the caller can persist it
}

// NewDetector uses the spec values: 1.5x spike factor, 30-sample window,
// 10-sample warm-up, 60 s cooldown, 1,000-attendee milestones.
func NewDetector() *Detector {
	return &Detector{
		spikeFactor: 1.5,
		windowSize:  30,
		warmup:      10,
		cooldown:    60 * time.Second,
		milestone:   1000,
		states:      make(map[string]*rateWindow),
	}
}

// Observe processes one snapshot and returns any announcements it triggers.
// lastMilestone is the highest milestone already announced for this event. The
// caller reads it from Redis, so a new leader does not repeat announcements.
func (d *Detector) Observe(snap Snapshot, lastMilestone int64) []Announcement {
	var out []Announcement

	if m, ok := d.checkMilestone(snap.Attendees, lastMilestone); ok {
		out = append(out, Announcement{
			Kind:        "milestone",
			Title:       fmt.Sprintf("Audience reached %s", commas(m)),
			Description: "Live attendance crossed another milestone.",
			Icon:        "users",
			Tone:        "good",
			Milestone:   m,
		})
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	w, ok := d.states[snap.EventID]
	if !ok {
		w = &rateWindow{}
		d.states[snap.EventID] = w
	}

	if spike, ok := d.checkSpike(w, snap); ok {
		out = append(out, spike)
	}

	w.samples = append(w.samples, snap.RequestsPerSec)
	if len(w.samples) > d.windowSize {
		w.samples = w.samples[len(w.samples)-d.windowSize:]
	}
	return out
}

// checkMilestone returns the highest 1,000-attendee milestone reached above
// lastMilestone, if any. A jump past several milestones announces only the highest.
func (d *Detector) checkMilestone(attendees, lastMilestone int64) (int64, bool) {
	current := (attendees / d.milestone) * d.milestone
	if current >= d.milestone && current > lastMilestone {
		return current, true
	}
	return 0, false
}

// checkSpike compares the current rate to the window mean (not including the
// current sample). It only fires after warm-up and outside the cooldown.
func (d *Detector) checkSpike(w *rateWindow, snap Snapshot) (Announcement, bool) {
	if len(w.samples) < d.warmup {
		return Announcement{}, false
	}
	var sum float64
	for _, s := range w.samples {
		sum += s
	}
	mean := sum / float64(len(w.samples))
	if mean <= 0 || snap.RequestsPerSec <= d.spikeFactor*mean {
		return Announcement{}, false
	}
	if !w.lastSpike.IsZero() && snap.At.Sub(w.lastSpike) < d.cooldown {
		return Announcement{}, false
	}
	w.lastSpike = snap.At
	return Announcement{
		Kind:        "spike",
		Title:       "Traffic spike",
		Description: fmt.Sprintf("%.0f req/s, %.1fx the recent average", snap.RequestsPerSec, snap.RequestsPerSec/mean),
		Icon:        "zap",
		Tone:        "warn",
	}, true
}
