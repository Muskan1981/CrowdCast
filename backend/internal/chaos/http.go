package chaos

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/crowdcast/crowdcast/internal/httpapi"
)

// exemptPrefixes are never affected by chaos: probes, metrics and admin
// endpoints must keep working so a failed node can still be restored.
var exemptPrefixes = []string{"/healthz", "/readyz", "/metrics", "/api/v1/admin/"}

// Middleware makes a failed node refuse work (503 NODE_OFFLINE, which the load
// balancer retries elsewhere) and adds the configured latency to other routes.
func Middleware(c *Controller) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isExempt(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if c.Failed() {
				httpapi.RespondError(w, http.StatusServiceUnavailable, "NODE_OFFLINE", "this node is down (simulated failure)", nil)
				return
			}
			if lat := c.Latency(); lat > 0 {
				select {
				case <-time.After(lat):
				case <-r.Context().Done():
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isExempt(path string) bool {
	for _, p := range exemptPrefixes {
		if path == p || strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// HealthzHandler is liveness plus the chaos flag: 503 while failed or draining.
func HealthzHandler(c *Controller, instanceID string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		st := c.State()
		body := map[string]any{"instance_id": instanceID, "chaos": st.Status, "latency_ms": st.LatencyMs}
		if c.Failed() {
			body["status"] = "unhealthy"
			httpapi.RespondJSON(w, http.StatusServiceUnavailable, body)
			return
		}
		body["status"] = "ok"
		httpapi.RespondJSON(w, http.StatusOK, body)
	}
}

// ChaosActionResponse matches the schema in docs/openapi.yaml.
type ChaosActionResponse struct {
	Node          string `json:"node"`
	Action        string `json:"action"`
	Success       bool   `json:"success"`
	CurrentStatus string `json:"current_status"`
}

// Publisher sends a message to a Redis channel (go-redis satisfies it).
type Publisher interface {
	Publish(ctx context.Context, channel string, message any) error
}

// AdminHandler accepts chaos requests on any node and forwards them to the target.
type AdminHandler struct {
	self     string
	nodes    map[string]bool
	pub      Publisher
	channel  string
	store    Store
	origin   string
	timeout  time.Duration
	pollStep time.Duration
}

// NewAdminHandler builds the admin endpoints. nodes is the expected fleet;
// channel is the control channel (ps:control).
func NewAdminHandler(self string, nodes []string, pub Publisher, channel string, store Store) *AdminHandler {
	set := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		set[n] = true
	}
	return &AdminHandler{
		self: self, nodes: set, pub: pub, channel: channel, store: store, origin: self,
		timeout: 2 * time.Second, pollStep: 50 * time.Millisecond,
	}
}

// Fail, Restore and Latency handle the three admin routes.
func (h *AdminHandler) Fail(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, ActionFail, 0, StatusFailed)
}

func (h *AdminHandler) Restore(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, ActionRestore, 0, StatusHealthy)
}

func (h *AdminHandler) Latency(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LatencyMs *int `json:"latency_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LatencyMs == nil {
		httpapi.RespondBadRequest(w, "body must be JSON with latency_ms")
		return
	}
	if *body.LatencyMs < 0 {
		httpapi.RespondBadRequest(w, "latency_ms must be zero or positive")
		return
	}
	ms := clampLatency(*body.LatencyMs)
	h.dispatch(w, r, ActionLatency, ms, "")
}

// dispatch publishes the command, then waits for the target to persist the new
// state. Confirming against the store means the response reflects what really
// happened, not what was requested.
func (h *AdminHandler) dispatch(w http.ResponseWriter, r *http.Request, action string, latencyMs int, wantStatus string) {
	node := chi.URLParam(r, "name")
	if !h.nodes[node] {
		httpapi.RespondNotFound(w, "unknown node "+node)
		return
	}

	cmd := Command{
		ID: uuid.NewString(), Target: node, Action: action,
		LatencyMs: latencyMs, Origin: h.origin, At: time.Now().UTC(),
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		httpapi.RespondInternalError(w, "failed to encode command")
		return
	}
	if err := h.pub.Publish(r.Context(), h.channel, payload); err != nil {
		httpapi.RespondError(w, http.StatusServiceUnavailable, "NODE_OFFLINE", "control channel unavailable: "+err.Error(), nil)
		return
	}

	st, success := h.confirm(r.Context(), node, action, latencyMs, wantStatus)
	httpapi.RespondJSON(w, http.StatusOK, ChaosActionResponse{
		Node: node, Action: action, Success: success, CurrentStatus: st.Status,
	})
}

// confirm polls the target's persisted state until it matches the request or the timeout passes.
func (h *AdminHandler) confirm(ctx context.Context, node, action string, latencyMs int, wantStatus string) (State, bool) {
	deadline := time.Now().Add(h.timeout)
	var last State
	for {
		st, ok, err := h.store.Load(ctx, node)
		if err == nil && ok {
			last = st
			if matches(action, st, latencyMs, wantStatus) {
				return st, true
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return last, false
		}
		select {
		case <-time.After(h.pollStep):
		case <-ctx.Done():
			return last, false
		}
	}
}

func matches(action string, st State, latencyMs int, wantStatus string) bool {
	switch action {
	case ActionLatency:
		return st.LatencyMs == latencyMs
	default:
		return st.Status == wantStatus
	}
}
