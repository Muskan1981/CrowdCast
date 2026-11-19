package reactions

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReactionsWhitelist(t *testing.T) {
	svc := NewService(nil, nil, nil, 100*time.Millisecond)
	defer svc.Close()

	ctx := context.Background()

	// Valid emojis
	for emoji := range AllowedEmojis {
		err := svc.RecordReaction(ctx, "event-1", "user-1", emoji)
		assert.NoError(t, err, "emoji %s should be allowed", emoji)
	}

	// Invalid emoji
	err := svc.RecordReaction(ctx, "event-1", "user-1", "💩")
	assert.ErrorIs(t, err, ErrInvalidEmoji)
}

func TestReactionsRateLimit(t *testing.T) {
	svc := NewService(nil, nil, nil, 100*time.Millisecond)
	defer svc.Close()

	ctx := context.Background()
	user := "burst-user"

	// Should allow first 15 bursts
	var lastErr error
	for i := 0; i < 20; i++ {
		err := svc.RecordReaction(ctx, "event-1", user, "🔥")
		if err != nil {
			lastErr = err
			break
		}
	}
	assert.ErrorIs(t, lastErr, ErrRateLimited)
}
