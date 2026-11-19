package stats

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/crowdcast/crowdcast/internal/events"
	"github.com/crowdcast/crowdcast/internal/httpapi"
	"github.com/crowdcast/crowdcast/internal/metrics"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/redis/go-redis/v9"
)

// Handler exposes the three stats endpoints.
type Handler struct {
	svc *Service
}

// NewHandler wraps a Service for HTTP use.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Overview handles GET /events/{id}/stats/overview.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Overview(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, events.ErrEventNotFound) {
			httpapi.RespondNotFound(w, "event not found")
			return
		}
		httpapi.RespondInternalError(w, "failed to build overview")
		return
	}
	httpapi.RespondJSON(w, http.StatusOK, out)
}

// Traffic handles GET /events/{id}/stats/traffic?range=60m.
func (h *Handler) Traffic(w http.ResponseWriter, r *http.Request) {
	rng, err := parseRange(r.URL.Query().Get("range"))
	if err != nil {
		httpapi.RespondBadRequest(w, err.Error())
		return
	}
	points, source, err := h.svc.Traffic(r.Context(), rng)
	if err != nil {
		httpapi.RespondInternalError(w, "failed to build traffic series")
		return
	}
	w.Header().Set("X-Data-Source", source)
	httpapi.RespondJSON(w, http.StatusOK, points)
}

// Audience handles GET /events/{id}/stats/audience.
func (h *Handler) Audience(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Audience(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, events.ErrEventNotFound) {
			httpapi.RespondNotFound(w, "event not found")
			return
		}
		httpapi.RespondInternalError(w, "failed to build audience stats")
		return
	}
	httpapi.RespondJSON(w, http.StatusOK, out)
}

// parseRange accepts Go durations ("60m", "2h"). An empty value means the default.
func parseRange(raw string) (time.Duration, error) {
	if raw == "" {
		return trafficDefault, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, errors.New("range must be a positive duration such as 60m or 2h")
	}
	return d, nil
}

// RunRequestFlusher adds this instance's request count to the shared Redis
// minute buckets every interval. It costs one HINCRBY per instance per tick,
// and it feeds the traffic and rate fallbacks when Prometheus is unreachable.
func RunRequestFlusher(ctx context.Context, rdb *redisstore.Client, m *metrics.Metrics, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	last := m.RequestCount()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			cur := m.RequestCount()
			delta := cur - last
			last = cur
			if delta == 0 {
				continue
			}
			field := strconv.FormatInt(now.UTC().Truncate(time.Minute).Unix(), 10)
			if err := rdb.HIncrBy(ctx, redisstore.StatsRequestsKey(), field, int64(delta)).Err(); err != nil && !errors.Is(err, redis.Nil) {
				// Best effort: losing one interval of fallback data is acceptable.
				continue
			}
		}
	}
}
