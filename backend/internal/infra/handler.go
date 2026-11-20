package infra

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/crowdcast/crowdcast/internal/httpapi"
)

// Handler serves GET /infra/nodes.
type Handler struct {
	reg      *Registry
	expected []Expected
	prom     Querier
	monitors []MonitorCheck
}

// NewHandler wires the fleet endpoint. prom may be nil; monitors may be empty.
func NewHandler(reg *Registry, expected []Expected, prom Querier, monitors []MonitorCheck) *Handler {
	return &Handler{reg: reg, expected: expected, prom: prom, monitors: monitors}
}

// Nodes merges the expected fleet with live heartbeats and monitoring checks.
func (h *Handler) Nodes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	names := make([]string, 0, len(h.expected))
	for _, e := range h.expected {
		names = append(names, e.Name)
	}

	live, err := h.reg.Snapshot(ctx, names)
	if err != nil {
		// Redis is down: report every node as Offline rather than failing the page.
		slog.Warn("infra: heartbeat snapshot failed", "error", err)
		live = map[string]NodeRecord{}
	}

	rows, totals, status, degraded := BuildFleet(h.expected, live)
	totals = FleetMetricsFromProm(ctx, h.prom, totals)
	httpapi.RespondJSON(w, http.StatusOK, FleetView{
		FleetStatus:        status,
		Degraded:           degraded,
		Metrics:            totals,
		ServerRows:         rows,
		MonitoringServices: CheckMonitors(ctx, h.monitors, time.Second),
	})
}

// Builder produces this instance's heartbeat record for the given time.
type Builder func(now time.Time) NodeRecord

// RunHeartbeat writes a record every interval until ctx is cancelled. A failed
// write is logged and retried on the next tick; the record's TTL means a node
// that stops heartbeating disappears from the fleet view on its own.
func RunHeartbeat(ctx context.Context, reg *Registry, interval time.Duration, build Builder) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	beat := func() {
		now := time.Now()
		hbCtx, cancel := context.WithTimeout(ctx, interval)
		defer cancel()
		if err := reg.Heartbeat(hbCtx, build(now), now); err != nil {
			slog.Warn("infra: heartbeat failed", "error", err)
		}
	}

	beat()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beat()
		}
	}
}
