package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// isTerminal reports whether stdout is a character device (a real terminal).
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runDashboard redraws the live view every second on a terminal, or prints a
// line every 5 seconds when the output is not a terminal or --no-dashboard is set.
func runDashboard(ctx context.Context, c *counters, stop <-chan struct{}, users int, started time.Time, plain bool) {
	interactive := !plain && isTerminal()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var lastIn, lastOut, lastHTTP int64
	lastTick := time.Now()
	lines := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
		}
		now := time.Now()
		dt := now.Sub(lastTick).Seconds()
		lastTick = now
		in, out, http := c.msgsIn.Load(), c.msgsOut.Load(), c.httpRequests.Load()
		rateIn := float64(in-lastIn) / dt
		rateOut := float64(out-lastOut) / dt
		rateHTTP := float64(http-lastHTTP) / dt
		lastIn, lastOut, lastHTTP = in, out, http

		if !interactive {
			if int(time.Since(started).Seconds())%5 == 0 {
				fmt.Printf("[%5.0fs] connected=%d msgs/s in=%.0f out=%.0f http/s=%.0f p95=%.0fms errors=%d reconnects=%d\n",
					time.Since(started).Seconds(), c.connected.Load(), rateIn, rateOut, rateHTTP,
					c.latency.quantile(0.95), totalErrors(c), c.reconnects.Load())
			}
			continue
		}
		var b strings.Builder
		if lines > 0 {
			fmt.Fprintf(&b, "\033[%dA", lines) // move up over the previous frame
		}
		frame := renderFrame(c, users, started, rateIn, rateOut, rateHTTP)
		b.WriteString(frame)
		fmt.Print(b.String())
		lines = strings.Count(frame, "\n")
	}
}

func renderFrame(c *counters, users int, started time.Time, rateIn, rateOut, rateHTTP float64) string {
	var b strings.Builder
	line := func(format string, args ...any) {
		fmt.Fprintf(&b, "\033[2K"+format+"\n", args...)
	}
	line("CrowdCast loadgen  elapsed %s   target %d users", time.Since(started).Round(time.Second), users)
	line("  connected   %6d   (peak %d)     sessions ok %d / failed %d",
		c.connected.Load(), c.connectedPeak.Load(), c.sessionsOK.Load(), c.sessionsFailed.Load())
	line("  websocket   in %7.0f/s   out %6.0f/s", rateIn, rateOut)
	line("  http        %7.0f req/s   p50 %4.0f ms   p95 %4.0f ms   p99 %4.0f ms",
		rateHTTP, c.latency.quantile(0.50), c.latency.quantile(0.95), c.latency.quantile(0.99))
	line("  actions     reactions %d   chats %d   votes %d", c.reactions.Load(), c.chats.Load(), c.votes.Load())
	line("  reconnects  %d   (close 1012: %d)     errors %d", c.reconnects.Load(), c.reconnects1012.Load(), totalErrors(c))
	line("  served by   %s", servedLine(c))
	return b.String()
}

func servedLine(c *counters) string {
	m := c.served.snapshot()
	pct := percentShares(m)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %.1f%%", strings.TrimPrefix(k, "crowdcast-"), pct[k]))
	}
	if len(parts) == 0 {
		return "(no responses yet)"
	}
	return strings.Join(parts, "   ")
}

func totalErrors(c *counters) int64 {
	var n int64
	for _, v := range c.errorCounts() {
		n += v
	}
	return n
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
