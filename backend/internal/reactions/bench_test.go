package reactions

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// BenchmarkRecordReactionHotPath measures the in-memory reaction path that every
// emoji tap takes: validation, the per-user limiter and the batch counter. It
// runs with no Redis so only the Go code is measured.
func BenchmarkRecordReactionHotPath(b *testing.B) {
	svc := NewService(nil, nil, nil, time.Hour) // flush interval is irrelevant here
	defer svc.Close()
	ctx := context.Background()
	users := make([]string, 1024)
	for i := range users {
		users[i] = fmt.Sprintf("user-%d", i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Rotating users keeps each limiter from being exhausted, so the benchmark
		// measures the accept path rather than the rate-limit rejection path.
		_ = svc.RecordReaction(ctx, "ev-bench", users[i%len(users)], "🔥")
	}
}

// BenchmarkRecordReactionParallel runs the same path from many goroutines, as
// thousands of viewers do at once.
func BenchmarkRecordReactionParallel(b *testing.B) {
	svc := NewService(nil, nil, nil, time.Hour)
	defer svc.Close()
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			_ = svc.RecordReaction(ctx, "ev-bench", fmt.Sprintf("u-%d", i%4096), "👏")
		}
	})
}
