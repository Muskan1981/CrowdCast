package hub

// Client represents a connected WebSocket client interface.
type Client interface {
	ID() string
	EventID() string
	Send(msg []byte) bool
	Close(code int, reason string)
}
