package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/redis/go-redis/v9"
)

// Room manages connected clients for an event and listens to the single Redis channel for that event.
type Room struct {
	eventID    string
	hub        *Hub
	rdb        *redisstore.Client
	clients    map[Client]struct{}
	clientsMu  sync.RWMutex
	register   chan Client
	unregister chan Client
	broadcast  chan []byte
	stopChan   chan struct{}
	stopped    bool
	stopOnce   sync.Once
}

func newRoom(eventID string, hub *Hub, rdb *redisstore.Client) *Room {
	return &Room{
		eventID:    eventID,
		hub:        hub,
		rdb:        rdb,
		clients:    make(map[Client]struct{}),
		register:   make(chan Client, 256),
		unregister: make(chan Client, 256),
		broadcast:  make(chan []byte, 1024),
		stopChan:   make(chan struct{}),
	}
}

// Start begins the room's main loop and the single Redis subscriber.
func (r *Room) Start(ctx context.Context) {
	// Start single Redis subscription for this event
	channelName := redisstore.EventChannelKey(r.eventID)
	pubsub := r.rdb.Subscribe(ctx, channelName)

	go r.listenRedis(ctx, pubsub)
	go r.run()
}

func (r *Room) run() {
	for {
		select {
		case <-r.stopChan:
			r.clientsMu.Lock()
			for client := range r.clients {
				delete(r.clients, client)
			}
			r.clientsMu.Unlock()
			return

		case client := <-r.register:
			r.clientsMu.Lock()
			r.clients[client] = struct{}{}
			r.clientsMu.Unlock()

		case client := <-r.unregister:
			r.clientsMu.Lock()
			delete(r.clients, client)
			count := len(r.clients)
			r.clientsMu.Unlock()

			if count == 0 {
				// Clean up empty room after idle delay
				go r.checkEmptyRoom()
			}

		case msg := <-r.broadcast:
			r.clientsMu.RLock()
			for client := range r.clients {
				if !client.Send(msg) {
					// Client's buffer was full (slow consumer)
					go func(c Client) {
						c.Close(1008, "slow consumer")
						r.unregister <- c
					}(client)
				}
			}
			r.clientsMu.RUnlock()
		}
	}
}

func (r *Room) listenRedis(ctx context.Context, pubsub *redis.PubSub) {
	defer pubsub.Close()
	ch := pubsub.Channel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopChan:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if msg != nil && len(msg.Payload) > 0 {
				// Fan out Redis payload to local clients
				select {
				case r.broadcast <- []byte(msg.Payload):
				default:
					slog.Warn("room broadcast channel full, dropping frame", "event_id", r.eventID)
				}
			}
		}
	}
}

func (r *Room) checkEmptyRoom() {
	time.Sleep(10 * time.Second)
	r.clientsMu.RLock()
	count := len(r.clients)
	r.clientsMu.RUnlock()

	if count == 0 {
		r.hub.removeRoom(r.eventID)
		r.Stop()
	}
}

// ClientCount returns the number of active clients in this room.
func (r *Room) ClientCount() int {
	r.clientsMu.RLock()
	defer r.clientsMu.RUnlock()
	return len(r.clients)
}

// BroadcastLocalPayload publishes a message to Redis so ALL instances receive it via the single path.
func (r *Room) BroadcastLocalPayload(ctx context.Context, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	channelName := redisstore.EventChannelKey(r.eventID)
	return r.rdb.Publish(ctx, channelName, data).Err()
}

// BroadcastDirect sends directly to local clients (e.g. for presence tick).
func (r *Room) BroadcastDirect(msg []byte) {
	select {
	case r.broadcast <- msg:
	default:
		slog.Warn("room broadcast channel full for direct message", "event_id", r.eventID)
	}
}

// Stop terminates the room and closes subscriptions.
func (r *Room) Stop() {
	r.stopOnce.Do(func() {
		r.stopped = true
		close(r.stopChan)
	})
}
