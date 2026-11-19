package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/crowdcast/crowdcast/internal/chat"
	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/polls"
	"github.com/crowdcast/crowdcast/internal/presence"
	"golang.org/x/time/rate"
)

const (
	maxMessageSize = 32 * 1024 // 32 KB
	idleTimeout    = 45 * time.Second
	pingInterval   = 15 * time.Second
	writeWait      = 5 * time.Second
)

type ChatPoster interface {
	PostMessage(ctx context.Context, eventID, userID, displayName, text string) (*chat.ChatMessage, error)
	GetHistory(ctx context.Context, eventID string, limit int) (*chat.ChatHistoryResponse, error)
}

type ReactionRecorder interface {
	RecordReaction(ctx context.Context, eventID, userID, emoji string) error
}

type PollVoter interface {
	Vote(ctx context.Context, pollID, userID string, optionIndex int) (*polls.PollResponse, error)
	GetActivePoll(ctx context.Context, eventID string) (*polls.PollResponse, error)
}

type Client struct {
	id          string
	eventID     string
	displayName string
	color       string
	conn        *websocket.Conn
	send        chan []byte
	hub         *hub.Hub
	presence    *presence.Service
	chat        ChatPoster
	reactions   ReactionRecorder
	polls       PollVoter
	limiter     *rate.Limiter
	closedMu    sync.Mutex
	isClosed    bool
	closeOnce   sync.Once
}

func NewClient(
	id, eventID, displayName, color string,
	conn *websocket.Conn,
	hubInstance *hub.Hub,
	presenceSvc *presence.Service,
	chatSvc ChatPoster,
	rxSvc ReactionRecorder,
	pollSvc PollVoter,
	chatRateLimit time.Duration,
) *Client {
	if chatRateLimit <= 0 {
		chatRateLimit = 1500 * time.Millisecond
	}
	return &Client{
		id:          id,
		eventID:     eventID,
		displayName: displayName,
		color:       color,
		conn:        conn,
		send:        make(chan []byte, 128),
		hub:         hubInstance,
		presence:    presenceSvc,
		chat:        chatSvc,
		reactions:   rxSvc,
		polls:       pollSvc,
		limiter:     rate.NewLimiter(rate.Every(chatRateLimit), 5),
	}
}

func (c *Client) ID() string {
	return c.id
}

func (c *Client) EventID() string {
	return c.eventID
}

// Send non-blockingly enqueues a message. Returns false if buffer full (slow consumer).
func (c *Client) Send(msg []byte) bool {
	c.closedMu.Lock()
	if c.isClosed {
		c.closedMu.Unlock()
		return false
	}
	c.closedMu.Unlock()

	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

// Close disconnects the client.
func (c *Client) Close(code int, reason string) {
	c.closeOnce.Do(func() {
		c.closedMu.Lock()
		c.isClosed = true
		c.closedMu.Unlock()

		_ = c.conn.Close(websocket.StatusCode(code), reason)
	})
}

// ClientMessage wraps incoming client actions.
type ClientMessage struct {
	Type   string `json:"t"`
	Emoji  string `json:"emoji,omitempty"`
	Text   string `json:"text,omitempty"`
	Poll   string `json:"poll,omitempty"`
	Option int    `json:"option,omitempty"`
}

// ReadPump handles incoming WebSocket frames from the client.
func (c *Client) ReadPump(ctx context.Context) {
	defer func() {
		c.hub.Unregister(c)
		_ = c.presence.RecordLeave(ctx, c.eventID, c.id)
		c.Close(int(websocket.StatusNormalClosure), "connection closed")
	}()

	c.conn.SetReadLimit(maxMessageSize)

	for {
		readCtx, cancel := context.WithTimeout(ctx, idleTimeout)
		_, msgBytes, err := c.conn.Read(readCtx)
		cancel()

		if err != nil {
			if errors.Is(err, context.Canceled) || websocket.CloseStatus(err) != -1 {
				return
			}
			slog.Debug("websocket read error", "user_id", c.id, "error", err)
			return
		}

		var msg ClientMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			c.sendErrorMessage("INVALID_FORMAT", "malformed json payload")
			continue
		}

		// Ping heartbeat from client
		if msg.Type == "ping" {
			pong := map[string]any{
				"t":         "pong",
				"timestamp": time.Now().UTC().UnixMilli(),
			}
			data, _ := json.Marshal(pong)
			c.Send(data)
			continue
		}

		// Token-bucket per-connection rate limit
		if !c.limiter.Allow() {
			c.sendErrorMessage("RATE_LIMITED", "you are sending messages too fast")
			continue
		}

		c.handleIncomingMessage(ctx, &msg)
	}
}

func (c *Client) handleIncomingMessage(ctx context.Context, msg *ClientMessage) {
	switch msg.Type {
	case "chat":
		if c.chat != nil {
			_, err := c.chat.PostMessage(ctx, c.eventID, c.id, c.displayName, msg.Text)
			if err != nil {
				c.sendErrorMessage("BAD_REQUEST", err.Error())
			}
		}

	case "reaction":
		if c.reactions != nil {
			err := c.reactions.RecordReaction(ctx, c.eventID, c.id, msg.Emoji)
			if err != nil {
				c.sendErrorMessage("BAD_REQUEST", err.Error())
			}
		}

	case "vote":
		if c.polls != nil {
			_, err := c.polls.Vote(ctx, msg.Poll, c.id, msg.Option)
			if err != nil {
				c.sendErrorMessage("BAD_REQUEST", err.Error())
			}
		}
	}
}

func (c *Client) sendErrorMessage(code, message string) {
	errPayload := map[string]any{
		"t":       "error",
		"code":    code,
		"message": message,
	}
	data, _ := json.Marshal(errPayload)
	c.Send(data)
}

// WritePump writes queued messages from c.send to the WebSocket connection.
func (c *Client) WritePump(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	heartbeatTicker := time.NewTicker(10 * time.Second)
	defer func() {
		ticker.Stop()
		heartbeatTicker.Stop()
		c.Close(int(websocket.StatusNormalClosure), "write pump closing")
	}()

	for {
		select {
		case <-ctx.Done():
			// Server shutdown or context cancel
			return

		case <-heartbeatTicker.C:
			// Refresh Redis presence heartbeat every 10s
			_ = c.presence.RefreshHeartbeat(ctx, c.eventID, c.id)

		case <-ticker.C:
			writeCtx, cancel := context.WithTimeout(ctx, writeWait)
			err := c.conn.Ping(writeCtx)
			cancel()
			if err != nil {
				return
			}

		case msg, ok := <-c.send:
			if !ok {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeWait)
			err := c.conn.Write(writeCtx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				slog.Debug("websocket write error", "user_id", c.id, "error", err)
				return
			}
		}
	}
}
