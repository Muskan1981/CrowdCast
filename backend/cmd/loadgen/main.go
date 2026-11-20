// Command loadgen simulates crowd traffic against a CrowdCast deployment.
//
//	loadgen run --scenario spike --users 5000 --duration 3m --target http://localhost:8080
//
// Each virtual user creates a session, opens a WebSocket, and acts with
// realistic probabilities: bursty reactions around peak moments, occasional
// chat from a pool of messages, one poll vote, a random watch time, and a
// reconnect with backoff and jitter after a dropped socket (for example when a
// node fails with close code 1012). A live dashboard refreshes every second,
// and a JSON summary is written when the run ends or on Ctrl-C.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const usage = `usage: loadgen run [flags]

flags:
  --scenario NAME    steady | ramp | spike | soak | reaction-storm | chat-storm | flash-crowd
  --users N          number of virtual users (default 500)
  --duration D       total run length, for example 2m (default 2m)
  --target URL       base URL: nginx at http://localhost:8080 or a single node (default http://localhost:8080)
  --admin-key K      admin key; if set, the event is started first when it is not live
  --ramp-time T      arrival window for ramp and spike (default 60s for spike, half the duration for ramp)
  --seed S           random seed; the same seed gives the same arrival and action pattern (default 1)
  --event ID         event to join (default cc-0826)
  --metrics-addr A   serve loadgen_* Prometheus metrics on this address, for example :9100
  --summary PATH     where to write the JSON summary (default loadgen-summary.json)
  --no-dashboard     print a line every 5s instead of the live dashboard
`

func main() {
	if len(os.Args) < 2 || os.Args[1] != "run" {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "loadgen: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	scenarioName := fs.String("scenario", "steady", "scenario name")
	users := fs.Int("users", 500, "virtual users")
	duration := fs.Duration("duration", 2*time.Minute, "run length")
	target := fs.String("target", "http://localhost:8080", "base URL")
	adminKey := fs.String("admin-key", "", "admin key (starts the event if needed)")
	rampTime := fs.Duration("ramp-time", 0, "arrival window for ramp and spike")
	seed := fs.Int64("seed", 1, "random seed")
	event := fs.String("event", "cc-0826", "event ID")
	metricsAddr := fs.String("metrics-addr", "", "Prometheus listen address for loadgen_* metrics")
	summaryPath := fs.String("summary", "loadgen-summary.json", "JSON summary path")
	noDashboard := fs.Bool("no-dashboard", false, "plain log output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *users <= 0 {
		return errors.New("--users must be positive")
	}
	base, err := url.Parse(*target)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return fmt.Errorf("bad --target %q", *target)
	}
	scenarioMap := scenarios(effectiveRampTime(*scenarioName, *rampTime, *duration))
	scn, ok := scenarioMap[*scenarioName]
	if !ok {
		return fmt.Errorf("unknown scenario %q (choose one of %s)", *scenarioName, strings.Join(scenarioNames(), ", "))
	}

	raised, err := raiseFileLimit(uint64(*users*2 + 512))
	if err != nil {
		log.Printf("warning: could not raise file-descriptor limit: %v", err)
	} else if raised < uint64(*users*2+512) {
		log.Printf("warning: file-descriptor limit is %d, need about %d for %d users; run `ulimit -n` higher", raised, *users*2+512, *users)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := newClient(*users)
	c := newCounters(*metricsAddr != "")
	if *metricsAddr != "" {
		go serveMetrics(*metricsAddr, c.prom.registry)
	}

	if *adminKey != "" {
		ensureLive(ctx, client, base, *event, *adminKey)
	}

	started := time.Now()
	cfg := &runCfg{
		base:     base,
		client:   client,
		event:    *event,
		seed:     *seed,
		scn:      scn,
		start:    started,
		deadline: started.Add(*duration),
		c:        c,
		ctx:      ctx,
	}
	rng := rand.New(rand.NewSource(*seed))
	cfg.peaks = buildPeaks(rng, *duration, scn.peaksPerMin)

	fmt.Printf("loadgen: %s\n", describe(scn))
	fmt.Printf("loadgen: users=%d duration=%s target=%s event=%s seed=%d cpus=%d\n",
		*users, *duration, base, *event, *seed, runtime.NumCPU())

	stopDash := make(chan struct{})
	dashDone := make(chan struct{})
	go func() {
		defer close(dashDone)
		runDashboard(ctx, c, stopDash, *users, started, *noDashboard)
	}()

	var wg sync.WaitGroup
	for i := 0; i < *users; i++ {
		join := scn.joinAt(i, *users, rng)
		wg.Add(1)
		go func(i int, join time.Duration) {
			defer wg.Done()
			runViewer(cfg, i, join)
		}(i, join)
	}

	allDone := make(chan struct{})
	go func() { wg.Wait(); close(allDone) }()

	timeout := time.NewTimer(*duration + 15*time.Second)
	defer timeout.Stop()
	select {
	case <-allDone:
	case <-ctx.Done():
		fmt.Println("\nloadgen: interrupted, closing sockets")
		select {
		case <-allDone:
		case <-time.After(10 * time.Second):
			fmt.Println("loadgen: some sockets did not close in time")
		}
	case <-timeout.C:
	}

	close(stopDash)
	<-dashDone

	finished := time.Now()
	sum := &summary{
		Scenario:   scn.name,
		Seed:       *seed,
		Users:      *users,
		DurationS:  finished.Sub(started).Seconds(),
		StartedAt:  started.UTC(),
		FinishedAt: finished.UTC(),
	}
	buildSummary(c, sum)
	if err := writeSummary(*summaryPath, sum); err != nil {
		return err
	}
	printSummary(sum, *summaryPath)
	return nil
}

// effectiveRampTime picks the arrival window: spike is 60 s by default, ramp is half the run.
func effectiveRampTime(scenario string, flagValue, duration time.Duration) time.Duration {
	if flagValue > 0 {
		return flagValue
	}
	switch scenario {
	case "spike":
		return 60 * time.Second
	case "ramp":
		return duration / 2
	default:
		return 0
	}
}

// newClient builds the one HTTP client every virtual user shares. Idle
// connections are kept per host so REST calls reuse sockets. WebSockets are
// dialled through the same transport.
func newClient(users int) *http.Client {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        users + 64,
		MaxIdleConnsPerHost: users + 64,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   false,
	}
	return &http.Client{Transport: tr}
}

// raiseFileLimit lifts the soft RLIMIT_NOFILE to the hard limit (up to want).
func raiseFileLimit(want uint64) (uint64, error) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return 0, err
	}
	target := lim.Max
	if target > want {
		target = want
	}
	if lim.Cur < target {
		lim.Cur = target
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
			return lim.Cur, err
		}
	}
	return lim.Cur, nil
}

// ensureLive starts the event if it is scheduled, so the viewers can join.
func ensureLive(ctx context.Context, client *http.Client, base *url.URL, event, adminKey string) {
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base.String(), "/")+"/api/v1/events/"+event, nil)
	if err != nil {
		return
	}
	resp, err := client.Do(get)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	var ev struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(resp.Body).Decode(&ev) != nil || ev.Status == "live" {
		return
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base.String(), "/")+"/api/v1/events/"+event+"/start", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	if r, err := client.Do(req); err == nil {
		_ = r.Body.Close()
		log.Printf("loadgen: started event %s (was %s)", event, ev.Status)
	}
}

func serveMetrics(addr string, reg *prometheus.Registry) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("loadgen: metrics server: %v", err)
	}
}

func writeSummary(path string, sum *summary) error {
	data, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func printSummary(sum *summary, path string) {
	fmt.Println()
	fmt.Println("=== loadgen summary ===")
	fmt.Printf("scenario %s, %d users, %.0fs, seed %d\n", sum.Scenario, sum.Users, sum.DurationS, sum.Seed)
	fmt.Printf("sessions ok=%d failed=%d  peak connected=%d\n", sum.SessionsOK, sum.SessionsFailed, sum.ConnectedPeak)
	fmt.Printf("frames in=%d out=%d  reactions=%d chats=%d votes=%d\n", sum.MsgsIn, sum.MsgsOut, sum.Reactions, sum.Chats, sum.Votes)
	fmt.Printf("http requests=%d errors=%d  latency p50=%.0fms p95=%.0fms p99=%.0fms\n",
		sum.HTTPRequests, sum.HTTPErrors, sum.LatencyMs["p50"], sum.LatencyMs["p95"], sum.LatencyMs["p99"])
	fmt.Printf("reconnects=%d (close 1012: %d)\n", sum.Reconnects, sum.Reconnects1012)
	fmt.Println("served by (X-Served-By):")
	for _, k := range sortedKeys(sum.ServedBy) {
		fmt.Printf("  %-18s %7d  %5.1f%%\n", k, sum.ServedBy[k], sum.ServedByPct[k])
	}
	if len(sum.Errors) > 0 {
		fmt.Println("errors:")
		for _, k := range sortedKeys(sum.Errors) {
			fmt.Printf("  %-18s %d\n", k, sum.Errors[k])
		}
	}
	fmt.Printf("summary written to %s\n", path)
}
