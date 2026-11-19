package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCleanText(t *testing.T) {
	raw := "   Hello\x00\x07 World!\r\n  "
	cleaned := CleanText(raw)
	assert.Equal(t, "Hello World!", cleaned)
}

func TestContainsProfanity(t *testing.T) {
	assert.True(t, ContainsProfanity("join our telegram.me group for free crypto"))
	assert.False(t, ContainsProfanity("The lighting effects are incredible!"))
}

func TestDeterministicColor(t *testing.T) {
	c1 := DeterministicColor("usr_12345")
	c2 := DeterministicColor("usr_12345")
	assert.Equal(t, c1, c2, "same user should receive identical color")

	valid := map[string]bool{"mint": true, "peach": true, "lilac": true, "yellow": true}
	assert.True(t, valid[c1])
}

func TestChatValidationAndDuplicateGuard(t *testing.T) {
	svc := NewService(nil, nil, nil, 100*time.Millisecond)
	defer svc.Close()

	ctx := context.Background()

	t.Run("empty message rejected", func(t *testing.T) {
		_, err := svc.PostMessage(ctx, "ev-1", "u1", "User 1", "   ")
		assert.ErrorIs(t, err, ErrEmptyMessage)
	})

	t.Run("message too long rejected", func(t *testing.T) {
		longText := strings.Repeat("A", 281)
		_, err := svc.PostMessage(ctx, "ev-1", "u1", "User 1", longText)
		assert.ErrorIs(t, err, ErrMessageTooLong)
	})

	t.Run("spam rejected", func(t *testing.T) {
		_, err := svc.PostMessage(ctx, "ev-1", "u1", "User 1", "Check this scam website")
		assert.ErrorIs(t, err, ErrProfanitySpam)
	})

	t.Run("duplicate rejected", func(t *testing.T) {
		now := time.Now()
		assert.False(t, svc.isDuplicate("u-dup", "great show!", now))
		// Immediate repeat
		assert.True(t, svc.isDuplicate("u-dup", "great show!", now.Add(500*time.Millisecond)))
		// After 4 seconds allowed
		assert.False(t, svc.isDuplicate("u-dup", "great show!", now.Add(4*time.Second)))
	})
}
