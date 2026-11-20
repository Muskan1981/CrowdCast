package main

import (
	"fmt"
	"math/rand"
	"sort"
	"time"
)

// scenario decides when each virtual user arrives and how intensely it acts.
type scenario struct {
	name string
	// joinAt returns the arrival offset for user i of n.
	joinAt func(i, n int, rng *rand.Rand) time.Duration
	// reactionMult and chatMult scale each user's base action rates.
	reactionMult float64
	chatMult     float64
	// watchMedian is the median watch time before a user leaves.
	watchMedian time.Duration
	// peaksPerMin is how often global peak moments happen (reactions cluster there).
	peaksPerMin float64
}

const baseWatchMedian = 20 * time.Minute

// scenarios lists every named scenario. rampTime is used by ramp and spike.
func scenarios(rampTime time.Duration) map[string]scenario {
	uniformOver := func(window time.Duration) func(int, int, *rand.Rand) time.Duration {
		return func(_, _ int, rng *rand.Rand) time.Duration {
			return time.Duration(rng.Int63n(int64(window) + 1))
		}
	}
	return map[string]scenario{
		"steady": {
			name:         "steady",
			joinAt:       func(_, _ int, _ *rand.Rand) time.Duration { return 0 },
			reactionMult: 1, chatMult: 1, watchMedian: baseWatchMedian, peaksPerMin: 0.8,
		},
		"ramp": {
			name: "ramp",
			joinAt: func(i, n int, _ *rand.Rand) time.Duration {
				return time.Duration(float64(rampTime) * float64(i) / float64(n))
			},
			reactionMult: 1, chatMult: 1, watchMedian: baseWatchMedian, peaksPerMin: 0.8,
		},
		"spike": {
			// The first 500 arrive at once, the rest within rampTime (default 60 s), then hold.
			name: "spike",
			joinAt: func(i, n int, rng *rand.Rand) time.Duration {
				base := 500
				if n <= base {
					return 0
				}
				if i < base {
					return 0
				}
				return time.Duration(rng.Int63n(int64(rampTime) + 1))
			},
			reactionMult: 1.5, chatMult: 1.5, watchMedian: baseWatchMedian, peaksPerMin: 1.5,
		},
		"soak": {
			// Long and quiet: low rates for a long time, to surface leaks and slow drift.
			name:         "soak",
			joinAt:       uniformOver(time.Minute),
			reactionMult: 0.2, chatMult: 0.2, watchMedian: 4 * time.Hour, peaksPerMin: 0.1,
		},
		"reaction-storm": {
			name:         "reaction-storm",
			joinAt:       uniformOver(10 * time.Second),
			reactionMult: 8, chatMult: 0.5, watchMedian: baseWatchMedian, peaksPerMin: 3,
		},
		"chat-storm": {
			name:         "chat-storm",
			joinAt:       uniformOver(10 * time.Second),
			reactionMult: 0.5, chatMult: 10, watchMedian: baseWatchMedian, peaksPerMin: 1,
		},
		"flash-crowd": {
			// Everyone arrives within 10 s, the worst case for the join path.
			name:         "flash-crowd",
			joinAt:       uniformOver(10 * time.Second),
			reactionMult: 1.5, chatMult: 1.5, watchMedian: baseWatchMedian, peaksPerMin: 1.5,
		},
	}
}

func scenarioNames() []string {
	names := make([]string, 0, 7)
	for k := range scenarios(0) {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// peakSchedule is the shared list of "peak moments": windows when every viewer
// reacts faster. Built once from the seed so every run with the same seed matches.
type peakSchedule struct {
	windows []window
}

type window struct {
	start, end time.Duration
	mult       float64
}

func buildPeaks(rng *rand.Rand, total time.Duration, perMin float64) peakSchedule {
	var ps peakSchedule
	if perMin <= 0 {
		return ps
	}
	mean := time.Duration(float64(time.Minute) / perMin)
	t := time.Duration(rng.Int63n(int64(mean)))
	for t < total {
		length := time.Duration(6+rng.Intn(10)) * time.Second
		ps.windows = append(ps.windows, window{start: t, end: t + length, mult: 4 + rng.Float64()*4})
		gap := time.Duration(rng.ExpFloat64() * float64(mean))
		t += length + gap
	}
	return ps
}

// multiplier returns the reaction boost at elapsed run time t (1 when no peak).
func (ps peakSchedule) multiplier(t time.Duration) float64 {
	for _, w := range ps.windows {
		if t >= w.start && t < w.end {
			return w.mult
		}
	}
	return 1
}

// cityWeights matches the backend's weighted city distribution (generator.go).
var cityWeights = []struct {
	name   string
	weight int
}{
	{"Mumbai", 28}, {"Delhi NCR", 24}, {"Bengaluru", 19}, {"Hyderabad", 14}, {"Other", 15},
}

func pickCity(rng *rand.Rand) string {
	n := rng.Intn(100)
	cum := 0
	for _, c := range cityWeights {
		cum += c.weight
		if n < cum {
			return c.name
		}
	}
	return "Other"
}

// chatPool is about 100 realistic messages, picked at random by viewers.
var chatPool = []string{
	"The visuals are INSANE", "This is my favourite track!", "Hello from Mumbai", "Can we get an encore?",
	"Bass drop was unreal", "Who else is watching from Delhi?", "Sound quality is so clean tonight",
	"This set is perfect for a Friday", "Anyone else getting chills?", "Hello from Bengaluru!",
	"Hyderabad checking in", "Lights are beautiful", "Dancing alone in my room right now",
	"The host is a legend", "Please play that remix again", "How long is this session?",
	"Best night of the week", "Crowd noise is amazing", "Vibes are immaculate", "Who made this stage?",
	"First time at a CrowdCast event, loving it", "The bridge gave me goosebumps", "Turn it up!",
	"Can't stop smiling", "Save this set for the playlist", "Hello friends", "Wow what a drop",
	"Shout out to the sound team", "Such a mood", "Is the recording going to be posted?",
	"This is a whole experience", "Bringing the energy from Pune", "Chennai in the house",
	"Cannot believe it's already been an hour", "Please do a Q&A later", "Those synths hit different",
	"The lighting sync is perfect", "Who is the artist on this track?", "Dropping a heart here",
	"Let's go!", "Hyped for the next set", "The rhythm is contagious", "Loving the visual art",
	"This is my Friday ritual now", "Great stream quality", "Can the stage cam pan left?",
	"Feeling the bass in my chest", "Thanks for hosting", "Kolkata reporting in", "Absolute banger",
	"That last track was new right?", "Who else is dancing?", "Tell the DJ we love him",
	"Night is young", "Peak moment incoming", "Ohh that transition!", "Good vibes only",
	"This crowd is wild", "Who's here from Jaipur?", "Ahmedabad says hi", "My neighbours are dancing too",
	"The intro was so cinematic", "Can we get the setlist?", "Five stars for this",
	"Stream quality is sharp", "That breakdown though", "Nobody leaves until the end",
	"Please bring this back next month", "Smooth mix", "Pure joy", "Where are the merch links?",
	"Feeling it", "Hello from Chandigarh", "Mumbai rain, but the vibes are sunny", "This never gets old",
	"What time does it end?", "Who's your favourite artist tonight?", "Chill and clean sound",
	"The crowd chant was epic", "Literally crying happy tears", "Amazing production",
	"Give the sound engineer a raise", "Big respect to the visuals team", "Let's keep the energy up",
	"Playing this on repeat tomorrow", "Still going strong", "Dance floor vibes", "Hello from Kochi",
	"Lovely night", "Could you share the track name?", "What a finale that would be",
	"This is so good I forgot to eat", "Cheers from Lucknow", "Keep it coming", "Sound check was perfect",
	"Yesss", "Bravo", "Absolutely loving every minute", "The bass is perfect",
}

// reactionEmojis are the six allowed by the backend.
var reactionEmojis = []string{"❤️", "🔥", "👏", "😍", "⚡", "🎉"}

// pollWeights gives the relative chance of each poll option, so results look organic.
func pickPollOption(rng *rand.Rand, options int) int {
	weights := []int{54, 31, 15}
	if options != len(weights) {
		return rng.Intn(options)
	}
	n := rng.Intn(100)
	cum := 0
	for i, w := range weights {
		cum += w
		if n < cum {
			return i
		}
	}
	return options - 1
}

func describe(s scenario) string {
	return fmt.Sprintf("%s (reactions x%.1f, chat x%.1f, watch median %s)", s.name, s.reactionMult, s.chatMult, s.watchMedian)
}
