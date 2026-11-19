package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/crowdcast/crowdcast/internal/metrics"
	"github.com/crowdcast/crowdcast/internal/presence"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
)

// Hub coordinates all event rooms for this backend instance.
type Hub struct {
	rdb       *redisstore.Client
	presence  *presence.Service
	rooms     map[string]*Room
	roomsMu   sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	runningMu sync.Mutex
}

func NewHub(rdb *redisstore.Client, presenceSvc *presence.Service) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{
		rdb:      rdb,
		presence: presenceSvc,
		rooms:    make(map[string]*Room),
		ctx:      ctx,
		cancel:   cancel,
	}

	go h.startPresenceTicker()
	return h
}

// GetOrCreateRoom retrieves or instantiates an event room.
func (h *Hub) GetOrCreateRoom(eventID string) *Room {
	h.roomsMu.RLock()
	room, exists := h.rooms[eventID]
	h.roomsMu.RUnlock()
	if exists {
		return room
	}

	h.roomsMu.Lock()
	defer h.roomsMu.Unlock()

	// Double-check under write lock
	if room, exists = h.rooms[eventID]; exists {
		return room
	}

	room = newRoom(eventID, h, h.rdb)
	h.rooms[eventID] = room
	room.Start(h.ctx)

	slog.Info("created room for event with single redis subscriber", "event_id", eventID)
	return room
}

func (h *Hub) removeRoom(eventID string) {
	h.roomsMu.Lock()
	defer h.roomsMu.Unlock()
	delete(h.rooms, eventID)
	slog.Info("closed idle room", "event_id", eventID)
}

// Register adds a client to its event room.
func (h *Hub) Register(client Client) {
	room := h.GetOrCreateRoom(client.EventID())
	room.register <- client

	// Record that this event has live viewers so the stats leader ticks it.
	if h.rdb != nil {
		go h.rdb.SAdd(context.Background(), redisstore.StatsEventsKey(), client.EventID())
	}
}

// Unregister removes a client from its event room.
func (h *Hub) Unregister(client Client) {
	h.roomsMu.RLock()
	room, exists := h.rooms[client.EventID()]
	h.roomsMu.RUnlock()
	if exists {
		room.unregister <- client
	}
}

// PublishEventMessage publishes an event message to Redis for cluster-wide fanout.
func (h *Hub) PublishEventMessage(ctx context.Context, eventID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	channel := redisstore.EventChannelKey(eventID)
	return h.rdb.Publish(ctx, channel, metrics.StampPublish(data, time.Now())).Err()
}

// startPresenceTicker coalesces presence broadcasts every 1.5s per active room.
func (h *Hub) startPresenceTicker() {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			h.broadcastPresenceTicks()
		}
	}
}

func (h *Hub) broadcastPresenceTicks() {
	h.roomsMu.RLock()
	roomList := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		if r.ClientCount() > 0 {
			roomList = append(roomList, r)
		}
	}
	h.roomsMu.RUnlock()

	for _, room := range roomList {
		ctx, cancel := context.WithTimeout(h.ctx, 1*time.Second)
		stats, err := h.presence.GetPresenceStats(ctx, room.eventID)
		cancel()
		if err == nil && stats != nil {
			metrics.Default().EventAudience.Set(float64(stats.Attendees))
			if data, err := json.Marshal(stats); err == nil {
				room.BroadcastDirect(data)
			}
		}
	}
}

// TotalClients returns total active clients across all rooms on this node.
func (h *Hub) TotalClients() int {
	h.roomsMu.RLock()
	defer h.roomsMu.RUnlock()
	total := 0
	for _, room := range h.rooms {
		total += room.ClientCount()
	}
	return total
}

// Drain sends close code 1012 (Service Restart) to all active sockets and flushes.
func (h *Hub) Drain(timeout time.Duration) {
	slog.Info("draining websocket hub for graceful shutdown", "timeout", timeout)
	h.cancel()

	h.roomsMu.RLock()
	allRooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		allRooms = append(allRooms, r)
	}
	h.roomsMu.RUnlock()

	for _, room := range allRooms {
		room.clientsMu.RLock()
		for client := range room.clients {
			// Code 1012: Service Restart
			client.Close(1012, "service restarting")
		}
		room.clientsMu.RUnlock()
		room.Stop()
	}

	time.Sleep(200 * time.Millisecond)
}
