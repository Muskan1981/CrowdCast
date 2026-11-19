package chat

import (
	"hash/fnv"
	"strings"
	"unicode"
)

var defaultBannedWords = []string{
	"scam", "spam", "free crypto", "telegram.me", "airdrop",
	"buy followers", "whatsapp group", "phishing",
}

var deterministicColors = []string{
	"mint", "peach", "lilac", "yellow",
}

// CleanText trims leading/trailing spaces and strips non-printable control characters.
func CleanText(input string) string {
	var sb strings.Builder
	for _, r := range strings.TrimSpace(input) {
		// Keep printable characters or whitespace
		if unicode.IsPrint(r) || r == '\n' || r == '\t' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// ContainsProfanity checks for banned or spam words.
func ContainsProfanity(text string) bool {
	lower := strings.ToLower(text)
	for _, word := range defaultBannedWords {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// DeterministicColor computes one of the UI colors (mint, peach, lilac, yellow) from a user identifier.
func DeterministicColor(userID string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(userID))
	idx := int(h.Sum32()) % len(deterministicColors)
	return deterministicColors[idx]
}
