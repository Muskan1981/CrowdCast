package ws

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/presence"
	"github.com/crowdcast/crowdcast/internal/testutil"
)

// BenchmarkRealSocketFanout connects N real WebSocket clients to one instance and
// times how long one Redis-published frame takes to reach all of them. This is the
// full path: PUBLISH, subscriber, room, socket write. It needs Redis (see testutil).
func BenchmarkRealSocketFanout(b *testing.B) {
	for _, n := range []int{1, 100, 1000, 5000} {
		b.Run(fmt.Sprintf("sockets=%d", n), func(b *testing.B) {
			benchFanout(b, n)
		})
	}
}

func benchFanout(b *testing.B, n int) {
	rdb := testutil.Redis(b)
	pres := presence.NewService(rdb, nil, nil)
	h := hub.NewHub(rdb, pres)
	tokens := auth.NewTokenService("bench-secret-key-1234567890123456")
	server := httptest.NewServer(NewHandler(tokens, h, pres, nil, nil, nil, nil, 1<<20, 0, "bench"))
	b.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
		h.Close()
		pres.Close()
	})

	eventID := testutil.RandomID("bench")
	var received atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)

	var wg sync.WaitGroup
	conns := make([]*websocket.Conn, 0, n)
	for i := 0; i < n; i++ {
		token, err := tokens.IssueViewerToken(fmt.Sprintf("u-%d", i), "B", "Mumbai", "mint")
		if err != nil {
			b.Fatal(err)
		}
		url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?event=" + eventID + "&token=" + token
		conn, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			b.Fatalf("dial %d: %v", i, err)
		}
		conns = append(conns, conn)
		wg.Add(1)
		go func(c *websocket.Conn) {
			defer wg.Done()
			for {
				if _, _, err := c.Read(ctx); err != nil {
					return
				}
				received.Add(1)
			}
		}(conn)
	}
	b.Cleanup(func() {
		for _, c := range conns {
			_ = c.CloseNow()
		}
		wg.Wait()
	})

	// Wait until every socket is registered in the room before timing.
	deadline := time.Now().Add(30 * time.Second)
	for h.TotalClients() < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	payload := map[string]any{"t": "reaction_batch", "reactions": map[string]int{"🔥": 4}, "total": 1}

	// Each round is one published frame. Counting received frames waits for all sockets.
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := received.Load() + int64(n)
		if err := h.PublishEventMessage(ctx, eventID, payload); err != nil {
			b.Fatal(err)
		}
		for received.Load() < target {
			time.Sleep(50 * time.Microsecond)
		}
	}
	b.StopTimer()
}
