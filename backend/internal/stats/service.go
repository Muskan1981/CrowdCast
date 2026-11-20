package stats

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/crowdcast/crowdcast/internal/events"
	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/presence"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
)

const (
	tickInterval     = 2 * time.Second
	tickBudget       = 4 * time.Second
	overviewCacheTTL = 1500 * time.Millisecond
	trafficCacheTTL  = 2 * time.Second
	trafficBucket    = 5 * time.Minute
	trafficDefault   = 60 * time.Minute
	trafficMax       = 24 * time.Hour

	// qTraffic is the 5-minute request count for the Traffic chart.
	qTraffic = `sum(increase(http_requests_total{route!="unmatched",route!="/metrics"}[5m]))`
)

// ErrEventNotFound is returned when the event does not exist.
var ErrEventNotFound = events.ErrEventNotFound

// Deps bundles what the stats service needs. Taking one struct keeps
// NewService readable and makes it obvious what each piece is for.
type Deps struct {
	Redis     *redisstore.Client
	Presence  *presence.Service
	Events    *events.Service
	Viewers   postgres.ViewerRepository
	Reactions postgres.ReactionRepository
	Stats     postgres.StatsRepository
	Activity  postgres.ActivityRepository
	Hub       *hub.Hub
	Prom      *PromClient
}

// Service computes stats for the HTTP handlers and, on the leader, ticks them.
type Service struct {
	rdb       *redisstore.Client
	presence  *presence.Service
	events    *events.Service
	viewers   postgres.ViewerRepository
	reactions postgres.ReactionRepository
	statsRepo postgres.StatsRepository
	activity  postgres.ActivityRepository
	hub       *hub.Hub
	prom      *PromClient
	cache     *Cache
	detector  *Detector
	now       func() time.Time
}

// NewService wires the stats service from its dependencies.
func NewService(d Deps) *Service {
	return &Service{
		rdb:       d.Redis,
		presence:  d.Presence,
		events:    d.Events,
		viewers:   d.Viewers,
		reactions: d.Reactions,
		statsRepo: d.Stats,
		activity:  d.Activity,
		hub:       d.Hub,
		prom:      d.Prom,
		cache:     NewCache(),
		detector:  NewDetector(),
		// Always UTC: the traffic chart labels are HH:MM in UTC, matching Prometheus.
		now: func() time.Time { return time.Now().UTC() },
	}
}

// ---- Metric cards -----------------------------------------------------------

// MetricCard is the shape the frontend metric cards bind to (see MetricCardData).
type MetricCard struct {
	Value  string `json:"value"`
	Raw    int64  `json:"raw"`
	Change string `json:"change"`
	Foot   string `json:"foot"`
	Tone   string `json:"tone"`
}

// MetricCards are the four Overview numbers, also pushed as stats_tick.
type MetricCards struct {
	LiveAttendees   MetricCard `json:"live_attendees"`
	ReactionsSent   MetricCard `json:"reactions_sent"`
	AvgResponseTime MetricCard `json:"avg_response_time"`
	RequestsPerSec  MetricCard `json:"requests_per_sec"`
}

// buildCards turns the current snapshot and the one a minute earlier into cards.
// prev may be nil (no history yet), in which case the change is shown as "—".
func buildCards(cur Snapshot, prev *Snapshot) MetricCards {
	const foot = "vs 1 min ago"
	return MetricCards{
		LiveAttendees: buildCard(float64(cur.Attendees), prevField(prev, func(p Snapshot) float64 { return float64(p.Attendees) }),
			formatCount, false, foot),
		ReactionsSent: buildCard(float64(cur.Reactions), prevField(prev, func(p Snapshot) float64 { return float64(p.Reactions) }),
			formatCount, false, foot),
		AvgResponseTime: buildCard(cur.AvgResponseMs, prevField(prev, func(p Snapshot) float64 { return p.AvgResponseMs }),
			func(v float64) string { return fmt.Sprintf("%.0f ms", v) }, true, foot),
		RequestsPerSec: buildCard(cur.RequestsPerSec, prevField(prev, func(p Snapshot) float64 { return p.RequestsPerSec }),
			func(v float64) string { return formatCount(math.Round(v)) + "/s" }, false, foot),
	}
}

func buildCard(cur float64, prev *float64, format func(float64) string, higherIsBad bool, foot string) MetricCard {
	var pct float64
	var ok bool
	if prev != nil {
		pct, ok = PercentChange(cur, *prev)
	}
	return MetricCard{
		Value:  format(cur),
		Raw:    int64(math.Round(cur)),
		Change: FormatChange(pct, ok),
		Foot:   foot,
		Tone:   ToneFor(pct, ok, higherIsBad),
	}
}

func prevField(prev *Snapshot, f func(Snapshot) float64) *float64 {
	if prev == nil {
		return nil
	}
	v := f(*prev)
	return &v
}

// ---- Overview ---------------------------------------------------------------

// EventBanner is the header strip: live timer and peak audience.
type EventBanner struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	Status           string `json:"status"`
	LiveForFormatted string `json:"live_for_formatted"`
	LiveForSeconds   int64  `json:"live_for_seconds"`
	PeakAudience     int64  `json:"peak_audience"`
}

// NodeShare is one instance's slice of request traffic (the distribution chart).
type NodeShare struct {
	Instance       string  `json:"instance"`
	RequestsPerSec float64 `json:"requests_per_sec"`
	SharePct       int     `json:"share_pct"`
}

// ActivityEntry is one row of the recent activity feed.
type ActivityEntry struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	TimeAgo     string `json:"time_ago"`
	Icon        string `json:"icon"`
	Tone        string `json:"tone"`
}

// TrafficSummary says where the Overview numbers came from.
type TrafficSummary struct {
	Source         string `json:"source"`
	RequestsPerSec int64  `json:"requests_per_sec"`
}

// OverviewResponse matches OverviewStats in docs/openapi.yaml.
type OverviewResponse struct {
	Event          EventBanner     `json:"event"`
	Metrics        MetricCards     `json:"metrics"`
	TrafficSummary TrafficSummary  `json:"traffic_summary"`
	Distribution   []NodeShare     `json:"distribution"`
	ServerRows     []any           `json:"server_rows"` // filled once the node registry lands
	Activity       []ActivityEntry `json:"activity"`
}

// Overview returns the Overview page data, cached for ~1.5 s per instance.
func (s *Service) Overview(ctx context.Context, eventID string) (OverviewResponse, error) {
	return Get(s.cache, "overview:"+eventID, overviewCacheTTL, func() (OverviewResponse, error) {
		return s.buildOverview(ctx, eventID)
	})
}

func (s *Service) buildOverview(ctx context.Context, eventID string) (OverviewResponse, error) {
	ev, err := s.events.GetEvent(ctx, eventID)
	if err != nil {
		return OverviewResponse{}, err
	}
	snap, err := s.TakeSnapshot(ctx, eventID)
	if err != nil {
		return OverviewResponse{}, err
	}
	prev, err := s.previousSnapshot(ctx, eventID, snap.At, time.Minute)
	if err != nil {
		slog.Warn("stats: previous snapshot unavailable", "event_id", eventID, "error", err)
	}

	activity, err := s.recentActivity(ctx, eventID, snap.At)
	if err != nil {
		return OverviewResponse{}, err
	}

	return OverviewResponse{
		Event: EventBanner{
			ID:               ev.ID,
			Title:            ev.Title,
			Status:           ev.Status,
			LiveForFormatted: ev.LiveForFormatted,
			LiveForSeconds:   ev.LiveForSeconds,
			PeakAudience:     ev.PeakAudience,
		},
		Metrics: buildCards(snap, prev),
		TrafficSummary: TrafficSummary{
			Source:         snap.Source,
			RequestsPerSec: int64(math.Round(snap.RequestsPerSec)),
		},
		Distribution: s.distribution(ctx),
		ServerRows:   []any{},
		Activity:     activity,
	}, nil
}

// distribution splits the request rate per instance label. If Prometheus is
// down it returns an empty list rather than failing the whole page.
func (s *Service) distribution(ctx context.Context) []NodeShare {
	byNode, err := s.prom.QueryByLabel(ctx, qRPSByNode, "instance")
	if err != nil {
		return []NodeShare{}
	}
	var total float64
	for _, v := range byNode {
		total += v
	}
	out := make([]NodeShare, 0, len(byNode))
	for inst, v := range byNode {
		share := 0
		if total > 0 {
			share = int(math.Round(v / total * 100))
		}
		out = append(out, NodeShare{Instance: inst, RequestsPerSec: v, SharePct: share})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

func (s *Service) recentActivity(ctx context.Context, eventID string, now time.Time) ([]ActivityEntry, error) {
	items, err := s.activity.ListRecent(ctx, eventID, 10)
	if err != nil {
		return nil, fmt.Errorf("activity: %w", err)
	}
	out := make([]ActivityEntry, 0, len(items))
	for _, it := range items {
		out = append(out, ActivityEntry{
			ID:          it.ID,
			Title:       it.Title,
			Description: it.Description,
			TimeAgo:     timeAgo(now.Sub(it.CreatedAt)),
			Icon:        it.Icon,
			Tone:        it.Tone,
		})
	}
	return out, nil
}

// ---- Traffic ----------------------------------------------------------------

// TrafficPoint is one 5-minute bucket of the traffic chart.
type TrafficPoint struct {
	Time     string `json:"time"`
	Requests int64  `json:"requests"`
}

type trafficResult struct {
	Points []TrafficPoint
	Source string
}

// Traffic returns 5-minute request buckets over the last rng. It reads
// Prometheus first and falls back to Redis minute buckets, reporting which one
// it used. Note: traffic is cluster-wide, so eventID is not part of the query.
func (s *Service) Traffic(ctx context.Context, rng time.Duration) ([]TrafficPoint, string, error) {
	rng = clampRange(rng)
	res, err := Get(s.cache, "traffic:"+rng.String(), trafficCacheTTL, func() (trafficResult, error) {
		return s.buildTraffic(ctx, rng), nil
	})
	return res.Points, res.Source, err
}

func clampRange(rng time.Duration) time.Duration {
	if rng <= 0 {
		return trafficDefault
	}
	if rng > trafficMax {
		return trafficMax
	}
	return rng
}

func (s *Service) buildTraffic(ctx context.Context, rng time.Duration) trafficResult {
	end := s.now()
	start := end.Add(-rng).Truncate(trafficBucket)

	samples, err := s.prom.QueryRange(ctx, qTraffic, start, end, trafficBucket)
	if err == nil {
		points := make([]TrafficPoint, 0, len(samples))
		for _, sm := range samples {
			// A sample at t counts the 5 minutes ending at t; label it by its start.
			points = append(points, TrafficPoint{
				Time:     sm.T.Add(-trafficBucket).Format("15:04"),
				Requests: int64(math.Round(sm.V)),
			})
		}
		return trafficResult{Points: points, Source: SourcePrometheus}
	}
	slog.Warn("stats: prometheus traffic query failed, using redis fallback", "error", err)
	return trafficResult{Points: s.fallbackTraffic(ctx, start, end), Source: SourceFallback}
}

// fallbackTraffic sums the Redis minute buckets into 5-minute points.
func (s *Service) fallbackTraffic(ctx context.Context, start, end time.Time) []TrafficPoint {
	var points []TrafficPoint
	for b := start; b.Before(end); b = b.Add(trafficBucket) {
		fields := make([]string, 0, 5)
		for m := b; m.Before(b.Add(trafficBucket)); m = m.Add(time.Minute) {
			fields = append(fields, fmt.Sprintf("%d", m.Unix()))
		}
		vals, err := s.rdb.HMGet(ctx, redisstore.StatsRequestsKey(), fields...).Result()
		if err != nil {
			return points
		}
		var sum float64
		for i := range vals {
			sum += parseBucket(vals, i)
		}
		points = append(points, TrafficPoint{Time: b.Format("15:04"), Requests: int64(math.Round(sum))})
	}
	return points
}

// ---- Audience ---------------------------------------------------------------

// AudienceMetrics are the four audience cards.
type AudienceMetrics struct {
	TotalAttendees    MetricCard `json:"total_attendees"`
	ReturningAudience MetricCard `json:"returning_audience"`
	AvgWatchTime      MetricCard `json:"avg_watch_time"`
	ChatMessages      MetricCard `json:"chat_messages"`
}

// CityShare is one row of the city mix.
type CityShare struct {
	Name       string `json:"name"`
	Percentage string `json:"percentage"`
}

// TopReaction is one row of the top-reactions list.
type TopReaction struct {
	Emoji    string `json:"emoji"`
	Name     string `json:"name"`
	Subtitle string `json:"subtitle"`
	Count    string `json:"count"`
}

// AudienceResponse matches AudienceStats in docs/openapi.yaml.
type AudienceResponse struct {
	Metrics      AudienceMetrics `json:"metrics"`
	CityMix      []CityShare     `json:"city_mix"`
	TopReactions []TopReaction   `json:"top_reactions"`
}

var emojiNames = map[string]string{
	"❤️": "Heart", "🔥": "Fire", "👏": "Applause", "😍": "Love", "⚡": "Electric", "🎉": "Party",
}

var rankSubtitles = []string{"Most used", "Second", "Third"}

// Audience returns the Audience page data, cached for ~1.5 s per instance.
func (s *Service) Audience(ctx context.Context, eventID string) (AudienceResponse, error) {
	return Get(s.cache, "audience:"+eventID, overviewCacheTTL, func() (AudienceResponse, error) {
		return s.buildAudience(ctx, eventID)
	})
}

func (s *Service) buildAudience(ctx context.Context, eventID string) (AudienceResponse, error) {
	total, err := s.statsRepo.TotalAttendees(ctx, eventID)
	if err != nil {
		return AudienceResponse{}, err
	}
	returning, err := s.statsRepo.ReturningPercent(ctx, eventID)
	if err != nil {
		return AudienceResponse{}, err
	}
	watch, err := s.statsRepo.AvgWatchSeconds(ctx, eventID)
	if err != nil {
		return AudienceResponse{}, err
	}
	snap, err := s.TakeSnapshot(ctx, eventID)
	if err != nil {
		return AudienceResponse{}, err
	}
	prev, _ := s.previousSnapshot(ctx, eventID, snap.At, time.Minute)

	cities, err := s.viewers.GetCityMix(ctx, eventID)
	if err != nil {
		return AudienceResponse{}, err
	}
	reactions, err := s.reactions.GetTopReactions(ctx, eventID)
	if err != nil {
		return AudienceResponse{}, err
	}

	chatDelta := prevField(prev, func(p Snapshot) float64 { return float64(p.ChatMessages) })
	chatCard := buildCard(float64(snap.ChatMessages), chatDelta, formatCount, false, "vs 1 min ago")

	return AudienceResponse{
		Metrics: AudienceMetrics{
			TotalAttendees: MetricCard{Value: formatCount(float64(total)), Raw: total, Change: "—", Foot: "all time", Tone: ToneFlat},
			ReturningAudience: MetricCard{
				Value: fmt.Sprintf("%.0f%%", returning), Raw: int64(math.Round(returning)),
				Change: "—", Foot: "seen at another event", Tone: ToneFlat,
			},
			AvgWatchTime: MetricCard{
				Value: formatDuration(watch), Raw: int64(math.Round(watch)),
				Change: "—", Foot: "per session", Tone: ToneFlat,
			},
			ChatMessages: chatCard,
		},
		CityMix:      toCityMix(cities),
		TopReactions: toTopReactions(reactions),
	}, nil
}

func toCityMix(cities []postgres.CityShare) []CityShare {
	out := make([]CityShare, 0, len(cities))
	for _, c := range cities {
		out = append(out, CityShare{Name: c.Name, Percentage: c.Percentage})
	}
	return out
}

func toTopReactions(totals []*postgres.ReactionTotal) []TopReaction {
	sorted := make([]*postgres.ReactionTotal, 0, len(totals))
	for _, t := range totals {
		if t != nil {
			sorted = append(sorted, t)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Total > sorted[j].Total })

	out := make([]TopReaction, 0, 3)
	for i, t := range sorted {
		if i >= len(rankSubtitles) {
			break
		}
		name := emojiNames[t.Emoji]
		if name == "" {
			name = "Reaction"
		}
		out = append(out, TopReaction{
			Emoji:    t.Emoji,
			Name:     name,
			Subtitle: rankSubtitles[i],
			Count:    formatCount(float64(t.Total)),
		})
	}
	return out
}

// ---- Leader tick ------------------------------------------------------------

// RunLeader is the work the elected leader does: every tick it snapshots each
// live event, records history, publishes one stats_tick per event, and runs the
// activity detector. It returns when ctx is cancelled (leadership lost or shutdown).
func (s *Service) RunLeader(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	s.tickAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tickAll(ctx)
		}
	}
}

func (s *Service) tickAll(ctx context.Context) {
	ids, err := s.rdb.SMembers(ctx, redisstore.StatsEventsKey()).Result()
	if err != nil {
		slog.Warn("stats: list events failed", "error", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		s.tickEvent(ctx, id)
	}
}

func (s *Service) tickEvent(ctx context.Context, eventID string) {
	// The budget must cover one Prometheus attempt (up to its 2 s client timeout,
	// which includes DNS failure for a missing host) plus the Redis work after it.
	// A 1.5 s budget let a failed Prometheus call starve every later Redis call.
	tickCtx, cancel := context.WithTimeout(ctx, tickBudget)
	defer cancel()

	snap, err := s.TakeSnapshot(tickCtx, eventID)
	if err != nil {
		slog.Warn("stats: snapshot failed", "event_id", eventID, "error", err)
		return
	}
	if err := s.recordHistory(tickCtx, snap); err != nil {
		slog.Warn("stats: record history failed", "event_id", eventID, "error", err)
	}

	prev, _ := s.previousSnapshot(tickCtx, eventID, snap.At, time.Minute)
	s.publishStatsTick(tickCtx, eventID, buildCards(snap, prev), snap)
	s.runDetector(tickCtx, snap)
}

// StatsTick is the WebSocket message carrying the four live metric cards.
type StatsTick struct {
	Type    string      `json:"t"`
	EventID string      `json:"event_id"`
	Metrics MetricCards `json:"metrics"`
	At      string      `json:"at"`
	Source  string      `json:"source"`
}

func (s *Service) publishStatsTick(ctx context.Context, eventID string, cards MetricCards, snap Snapshot) {
	if s.hub == nil {
		return
	}
	msg := StatsTick{
		Type:    "stats_tick",
		EventID: eventID,
		Metrics: cards,
		At:      snap.At.UTC().Format(time.RFC3339),
		Source:  snap.Source,
	}
	if err := s.hub.PublishEventMessage(ctx, eventID, msg); err != nil {
		slog.Warn("stats: publish stats_tick failed", "event_id", eventID, "error", err)
	}
}

// runDetector feeds the snapshot to the detector, persists each announcement to
// activity_log, remembers the milestone in Redis, and pushes an activity message.
func (s *Service) runDetector(ctx context.Context, snap Snapshot) {
	last, err := parseOptionalInt(s.rdb.Get(ctx, redisstore.StatsMilestoneKey(snap.EventID)).Result())
	if err != nil {
		slog.Warn("stats: read milestone failed", "event_id", snap.EventID, "error", err)
		return
	}

	for _, a := range s.detector.Observe(snap, last) {
		item := &postgres.ActivityItem{
			EventID:      snap.EventID,
			ActivityType: a.Kind,
			Title:        a.Title,
			Description:  a.Description,
			Icon:         a.Icon,
			Tone:         a.Tone,
			CreatedAt:    snap.At,
		}
		if err := s.activity.Insert(ctx, item); err != nil {
			slog.Warn("stats: persist activity failed", "event_id", snap.EventID, "error", err)
			continue
		}
		if a.Milestone > 0 {
			if err := s.rdb.Set(ctx, redisstore.StatsMilestoneKey(snap.EventID), a.Milestone, 0).Err(); err != nil {
				slog.Warn("stats: store milestone failed", "event_id", snap.EventID, "error", err)
			}
		}
		s.publishActivity(ctx, snap.EventID, item)
	}
}

// ActivityMessage is pushed to clients when the detector announces something.
type activityMessage struct {
	Type    string        `json:"t"`
	EventID string        `json:"event_id"`
	Item    ActivityEntry `json:"item"`
}

func (s *Service) publishActivity(ctx context.Context, eventID string, item *postgres.ActivityItem) {
	if s.hub == nil {
		return
	}
	msg := activityMessage{
		Type:    "activity",
		EventID: eventID,
		Item: ActivityEntry{
			ID:          item.ID,
			Title:       item.Title,
			Description: item.Description,
			TimeAgo:     "just now",
			Icon:        item.Icon,
			Tone:        item.Tone,
		},
	}
	if err := s.hub.PublishEventMessage(ctx, eventID, msg); err != nil {
		slog.Warn("stats: publish activity failed", "event_id", eventID, "error", err)
	}
}

// ---- helpers ----------------------------------------------------------------

// formatCount renders 12840 as "12,840".
func formatCount(v float64) string {
	return commas(int64(math.Round(v)))
}

func commas(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// formatDuration renders seconds as "12m 40s" (or "40s" under a minute).
func formatDuration(sec float64) string {
	total := int64(math.Round(sec))
	m, s := total/60, total%60
	if m == 0 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}

// timeAgo renders an age for the activity feed.
func timeAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}
