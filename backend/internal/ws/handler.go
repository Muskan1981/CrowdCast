package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/crowdcast/crowdcast/internal/events"
	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/metrics"
	"github.com/crowdcast/crowdcast/internal/presence"
)

type Handler struct {
	tokens        *auth.TokenService
	hub           *hub.Hub
	presence      *presence.Service
	events        *events.Service
	chat          ChatPoster
	reactions     ReactionRecorder
	polls         PollVoter
	maxConns      int64
	activeConns   int64
	chatRateLimit time.Duration
	instanceID    string
}

func NewHandler(
	tokens *auth.TokenService,
	hubInstance *hub.Hub,
	presenceSvc *presence.Service,
	eventsSvc *events.Service,
	chatSvc ChatPoster,
	rxSvc ReactionRecorder,
	pollSvc PollVoter,
	maxConns int64,
	chatRateLimit time.Duration,
	instanceID string,
) *Handler {
	if maxConns <= 0 {
		maxConns = 5000
	}
	return &Handler{
		tokens:        tokens,
		hub:           hubInstance,
		presence:      presenceSvc,
		events:        eventsSvc,
		chat:          chatSvc,
		reactions:     rxSvc,
		polls:         pollSvc,
		maxConns:      maxConns,
		chatRateLimit: chatRateLimit,
		instanceID:    instanceID,
	}
}

// ActiveConnections returns the current number of active WebSocket connections.
func (h *Handler) ActiveConnections() int64 {
	return atomic.LoadInt64(&h.activeConns)
}

// ServeHTTP handles the WebSocket handshake and spawns client pumps.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Max connections check
	current := atomic.AddInt64(&h.activeConns, 1)
	defer atomic.AddInt64(&h.activeConns, -1)

	if current > h.maxConns {
		slog.Warn("max websocket connections reached on instance", "current", current, "max", h.maxConns)
		http.Error(w, "server connection limit reached", http.StatusServiceUnavailable)
		return
	}

	// 2. Extract and validate parameters
	eventID := r.URL.Query().Get("event")
	if eventID == "" {
		eventID = r.URL.Query().Get("event_id")
	}
	if eventID == "" {
		http.Error(w, "event query parameter is required", http.StatusBadRequest)
		return
	}

	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Error(w, "token query parameter is required", http.StatusUnauthorized)
		return
	}

	// 3. JWT validation
	claims, err := h.tokens.VerifyToken(tokenStr)
	if err != nil {
		http.Error(w, "invalid or expired authentication token", http.StatusUnauthorized)
		return
	}

	// 4. Upgrade HTTP connection to WebSocket
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		metrics.Default().AppErrors.WithLabelValues("ws_upgrade").Inc()
		slog.Error("websocket upgrade failed", "error", err)
		return
	}

	m := metrics.Default()
	m.ConnOpened()
	defer m.ConnClosed()

	client := NewClient(
		claims.UserID,
		eventID,
		claims.DisplayName,
		claims.Color,
		conn,
		h.hub,
		h.presence,
		h.chat,
		h.reactions,
		h.polls,
		h.chatRateLimit,
	)

	// 5. Send `welcome` message immediately on connect
	h.sendWelcomeMessage(r.Context(), client, eventID, claims.UserID)

	// 6. Record presence join
	_ = h.presence.RecordJoin(r.Context(), eventID, claims.UserID)

	// 7. Register client in room
	h.hub.Register(client)

	// 8. Run write pump in background and read pump in foreground
	connCtx := r.Context()
	go client.WritePump(connCtx)
	client.ReadPump(connCtx)
}

func (h *Handler) sendWelcomeMessage(ctx context.Context, client *Client, eventID, userID string) {
	welcomePayload := map[string]any{
		"t":         "welcome",
		"node":      h.instanceID,
		"viewer_id": userID,
		"event_id":  eventID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	// Attach event state if event service available
	if h.events != nil {
		ev, err := h.events.GetEvent(ctx, eventID)
		if err == nil && ev != nil {
			welcomePayload["event_state"] = map[string]any{
				"status":           ev.Status,
				"started_at":       ev.StartedAt,
				"live_for":         ev.LiveForFormatted,
				"live_for_seconds": ev.LiveForSeconds,
				"peak_audience":    ev.PeakAudience,
			}
		}
	}

	// Attach initial presence stats
	if h.presence != nil {
		stats, err := h.presence.GetPresenceStats(ctx, eventID)
		if err == nil && stats != nil {
			welcomePayload["presence"] = stats
		}
	}

	// Attach recent chat messages
	if h.chat != nil {
		hist, err := h.chat.GetHistory(ctx, eventID, 20)
		if err == nil && hist != nil {
			welcomePayload["recent_chat"] = hist.Messages
		}
	}

	// Attach active poll
	if h.polls != nil {
		poll, err := h.polls.GetActivePoll(ctx, eventID)
		if err == nil && poll != nil {
			welcomePayload["active_poll"] = poll
		}
	}

	data, err := json.Marshal(welcomePayload)
	if err == nil {
		client.Send(data)
	}
}
