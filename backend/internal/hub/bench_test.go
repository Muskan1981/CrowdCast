package hub

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// countingClient counts frames without retaining them, so a benchmark measures
// the fan-out cost and not the memory used by recorded messages.
type countingClient struct {
	id    string
	event string
	n     atomic.Int64
}

func (c *countingClient) ID() string      { return c.id }
func (c *countingClient) EventID() string { return c.event }
func (c *countingClient) Send([]byte) bool {
	c.n.Add(1)
	return true
}
func (c *countingClient) Close(int, string) {}

// BenchmarkRoomFanout measures one room delivering a frame to N local clients,
// which is the work the room goroutine does per message. Reported time is per
// message, so the per-recipient cost is time/op divided by N.
func BenchmarkRoomFanout(b *testing.B) {
	for _, n := range []int{1, 100, 5000} {
		b.Run(fmt.Sprintf("clients=%d", n), func(b *testing.B) {
			r := newRoom("bench", nil, nil)
			go r.run()
			defer r.Stop()

			clients := make([]*countingClient, n)
			for i := range clients {
				clients[i] = &countingClient{id: fmt.Sprint(i), event: "bench"}
				r.register <- clients[i]
			}
			for r.ClientCount() != n {
			}
			payload := []byte(`{"t":"reaction_batch","reactions":{"🔥":4},"total":1000}`)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.broadcast <- payload
			}
			// Wait for the room to drain so the timing covers the fan-out itself.
			for {
				var total int64
				for _, c := range clients {
					total += c.n.Load()
				}
				if total >= int64(b.N)*int64(n) {
					break
				}
			}
			b.StopTimer()
		})
	}
}

// BenchmarkHubPublish measures PUBLISH through Redis, the cluster-wide path. It
// needs TEST_REDIS_URL or a local Redis and is skipped otherwise.
func BenchmarkHubPublish(b *testing.B) {
	h := newRealHubForBench(b)
	defer h.Close()
	payload := map[string]any{"t": "chat", "text": "bench"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.PublishEventMessage(b.Context(), "ev-bench", payload)
	}
}
