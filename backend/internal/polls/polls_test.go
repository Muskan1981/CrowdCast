package polls

import (
	"context"
	"testing"

	"github.com/crowdcast/crowdcast/internal/store/postgres"
	"github.com/stretchr/testify/assert"
)

func TestPollPercentagesCalculation(t *testing.T) {
	svc := &Service{}

	pollData := &postgres.PollWithDetails{
		Poll: postgres.Poll{
			ID:       "poll-test",
			Question: "Energy level?",
			Status:   "active",
		},
		Options: []postgres.PollOption{
			{OptionIndex: 0, OptionText: "Electric", VoteCount: 540},
			{OptionIndex: 1, OptionText: "Zone", VoteCount: 310},
			{OptionIndex: 2, OptionText: "Warming", VoteCount: 150},
		},
	}

	res, err := svc.populateResults(context.Background(), pollData)
	assert.NoError(t, err)
	assert.Equal(t, 1000, res.TotalVotes)
	assert.Equal(t, []int{54, 31, 15}, res.Percentages)
	assert.Equal(t, []int{540, 310, 150}, res.Votes)
}

func TestPollPercentagesZeroVotes(t *testing.T) {
	svc := &Service{}

	pollData := &postgres.PollWithDetails{
		Poll: postgres.Poll{
			ID:       "poll-zero",
			Question: "Zero votes test?",
			Status:   "active",
		},
		Options: []postgres.PollOption{
			{OptionIndex: 0, OptionText: "Opt A", VoteCount: 0},
			{OptionIndex: 1, OptionText: "Opt B", VoteCount: 0},
		},
	}

	res, err := svc.populateResults(context.Background(), pollData)
	assert.NoError(t, err)
	assert.Equal(t, 0, res.TotalVotes)
	assert.Equal(t, []int{0, 0}, res.Percentages)
}
