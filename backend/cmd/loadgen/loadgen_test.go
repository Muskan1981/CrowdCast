package main

import (
	"math/rand"
	"testing"
	"time"
)

func TestHistogramQuantileUsesBucketBounds(t *testing.T) {
	h := newHistogram()
	for i := 0; i < 90; i++ {
		h.observe(3 * time.Millisecond) // bucket <= 5ms
	}
	for i := 0; i < 10; i++ {
		h.observe(400 * time.Millisecond) // bucket <= 500ms
	}
	if got := h.quantile(0.50); got != 5 {
		t.Fatalf("p50 = %v, want 5", got)
	}
	if got := h.quantile(0.99); got != 500 {
		t.Fatalf("p99 = %v, want 500", got)
	}
	if empty := newHistogram().quantile(0.95); empty != 0 {
		t.Fatalf("empty histogram quantile = %v, want 0", empty)
	}
}

func TestPercentSharesSumToHundred(t *testing.T) {
	shares := percentShares(map[string]int64{"a": 50, "b": 30, "c": 20})
	var total float64
	for _, v := range shares {
		total += v
	}
	if total < 99.999 || total > 100.001 {
		t.Fatalf("shares sum to %v, want 100", total)
	}
	if got := shares["a"]; got < 49.99 || got > 50.01 {
		t.Fatalf("a = %v, want 50", got)
	}
	if len(percentShares(map[string]int64{})) != 0 {
		t.Fatal("no responses should give no shares")
	}
}

func TestPickCityMatchesBackendWeights(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	counts := map[string]int{}
	const n = 100000
	for i := 0; i < n; i++ {
		counts[pickCity(rng)]++
	}
	want := map[string]float64{"Mumbai": 28, "Delhi NCR": 24, "Bengaluru": 19, "Hyderabad": 14, "Other": 15}
	for city, pct := range want {
		got := float64(counts[city]) * 100 / n
		if got < pct-1.5 || got > pct+1.5 {
			t.Errorf("%s = %.1f%%, want about %.0f%%", city, got, pct)
		}
	}
}

func TestSpikeJoinsBaselineAtOnce(t *testing.T) {
	scn := scenarios(60 * time.Second)["spike"]
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		if d := scn.joinAt(i, 2000, rng); d != 0 {
			t.Fatalf("baseline user %d joins at %v, want 0", i, d)
		}
	}
	for i := 500; i < 2000; i++ {
		if d := scn.joinAt(i, 2000, rng); d < 0 || d > 60*time.Second {
			t.Fatalf("spike user %d joins at %v, want within 60s", i, d)
		}
	}
}

func TestRampIsLinearOverRampTime(t *testing.T) {
	scn := scenarios(100 * time.Second)["ramp"]
	rng := rand.New(rand.NewSource(1))
	if d := scn.joinAt(50, 100, rng); d != 50*time.Second {
		t.Fatalf("user 50 of 100 joins at %v, want 50s", d)
	}
}

func TestPeakScheduleIsReproducibleFromSeed(t *testing.T) {
	a := buildPeaks(rand.New(rand.NewSource(7)), 10*time.Minute, 2)
	b := buildPeaks(rand.New(rand.NewSource(7)), 10*time.Minute, 2)
	if len(a.windows) == 0 || len(a.windows) != len(b.windows) {
		t.Fatalf("expected reproducible, non-empty schedule, got %d and %d", len(a.windows), len(b.windows))
	}
	for i := range a.windows {
		if a.windows[i] != b.windows[i] {
			t.Fatalf("window %d differs between runs with the same seed", i)
		}
	}
}

func TestEffectiveRampTimeDefaults(t *testing.T) {
	if got := effectiveRampTime("spike", 0, 5*time.Minute); got != 60*time.Second {
		t.Fatalf("spike default = %v, want 60s", got)
	}
	if got := effectiveRampTime("ramp", 0, 4*time.Minute); got != 2*time.Minute {
		t.Fatalf("ramp default = %v, want half the run", got)
	}
	if got := effectiveRampTime("ramp", 30*time.Second, 4*time.Minute); got != 30*time.Second {
		t.Fatalf("explicit ramp-time ignored: %v", got)
	}
}

func TestWatchDurationIsClamped(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 10000; i++ {
		d := watchDuration(rng, 20*time.Minute)
		if d < 30*time.Second || d > 4*time.Hour {
			t.Fatalf("watch duration %v outside [30s, 4h]", d)
		}
	}
}
