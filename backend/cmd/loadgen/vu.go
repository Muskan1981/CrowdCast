package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	headerServedBy     = "X-Served-By"
	baseReactionEvery  = 8 * time.Second
	baseChatEvery      = 90 * time.Second
	writeTimeout       = 5 * time.Second
	dialTimeout        = 10 * time.Second
	backoffBase        = 500 * time.Millisecond
	backoffCap         = 15 * time.Second
	stableSessionAfter = 10 * time.Second // a connection this long resets backoff
)

// runCfg is shared by every virtual user in a run.
type runCfg struct {
	base     *url.URL
	client   *http.Client
	event    string
	seed     int64
	scn      scenario
	peaks    peakSchedule
	start    time.Time
	deadline time.Time
	c        *counters
	ctx      context.Context
}

// viewer is one simulated person.
type viewer struct {
	id     int
	cfg    *runCfg
	rng    *rand.Rand
	city   string
	token  string
	leave  time.Time
	voted  bool
	voteAt time.Time
}

// runViewer drives one virtual user from arrival to departure.
func runViewer(cfg *runCfg, id int, joinAt time.Duration) {
	timer := time.NewTimer(time.Until(cfg.start.Add(joinAt)))
	defer timer.Stop()
	select {
	case <-cfg.ctx.Done():
		return
	case <-timer.C:
	}

	v := &viewer{id: id, cfg: cfg, rng: rand.New(rand.NewSource(cfg.seed + int64(id)*7919))}
	v.city = pickCity(v.rng)
	if err := v.createSession(); err != nil {
		cfg.c.sessionsFailed.Add(1)
		cfg.c.errorKind("session")
		return
	}
	cfg.c.sessionsOK.Add(1)
	cfg.c.joined.Add(1)

	v.leave = time.Now().Add(watchDuration(v.rng, cfg.scn.watchMedian))
	if v.leave.After(cfg.deadline) {
		v.leave = cfg.deadline
	}
	if v.rng.Float64() < 0.6 { // about 60% of viewers vote, once
		v.voted = true
		v.voteAt = time.Now().Add(time.Duration(30+v.rng.Intn(90)) * time.Second)
	}
	v.loop()
}

// loop keeps the viewer connected, reconnecting with exponential backoff and
// jitter after drops, until it leaves or the run ends.
func (v *viewer) loop() {
	c := v.cfg.c
	attempt := 0
	for {
		if v.done() {
			return
		}
		conn, served, err := v.dial()
		if err != nil {
			c.wsDialFailures.Add(1)
			c.errorKind("dial")
			attempt++
			if !v.sleepBackoff(attempt) {
				return
			}
			continue
		}
		c.served.add(served)
		c.connUp()
		started := time.Now()
		closeCode, leftOnPurpose := v.session(conn)
		c.connDown()

		if leftOnPurpose || v.done() {
			return
		}
		c.reconnects.Add(1)
		if c.prom != nil {
			c.prom.reconns.Inc()
		}
		if closeCode == 1012 {
			c.reconnects1012.Add(1)
		}
		if time.Since(started) > stableSessionAfter {
			attempt = 0
		}
		attempt++
		if !v.sleepBackoff(attempt) {
			return
		}
	}
}

func (v *viewer) done() bool {
	return v.cfg.ctx.Err() != nil || time.Now().After(v.leave)
}

// sleepBackoff waits base*2^attempt capped, with full jitter. It returns false if the run ended.
func (v *viewer) sleepBackoff(attempt int) bool {
	ceiling := float64(backoffBase) * math.Pow(2, float64(min(attempt, 10)))
	if ceiling > float64(backoffCap) {
		ceiling = float64(backoffCap)
	}
	wait := time.Duration(v.rng.Float64() * ceiling)
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-v.cfg.ctx.Done():
		return false
	case <-t.C:
		return !v.done()
	}
}

// session runs one WebSocket connection: a reader goroutine plus an actor loop
// that schedules reactions, chat and the vote. All timers are created once per
// connection and re-armed in place, so a long-lived viewer never accumulates timers.
// It returns the close code and whether the viewer chose to leave.
func (v *viewer) session(conn *websocket.Conn) (closeCode int, leftOnPurpose bool) {
	c := v.cfg.c
	ctx, cancel := context.WithCancel(v.cfg.ctx)
	defer cancel()
	defer func() { _ = conn.CloseNow() }()

	readErr := make(chan error, 1)
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				readErr <- err
				return
			}
			c.msgsIn.Add(1)
			if c.prom != nil {
				c.prom.messages.WithLabelValues("in").Inc()
			}
		}
	}()

	reactT := time.NewTimer(time.Until(v.nextReactionAt()))
	defer reactT.Stop()
	chatT := time.NewTimer(v.exp(v.chatMean()))
	defer chatT.Stop()
	leaveT := time.NewTimer(time.Until(v.leave))
	defer leaveT.Stop()
	voteT := time.NewTimer(time.Hour)
	defer voteT.Stop()
	var voteC <-chan time.Time
	if v.voted {
		voteT.Reset(time.Until(v.voteAt))
		voteC = voteT.C
	}

	for {
		select {
		case err := <-readErr:
			return closeStatus(err), false
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "run finished")
			return 0, true
		case <-leaveT.C:
			_ = conn.Close(websocket.StatusNormalClosure, "viewer left")
			return 0, true
		case <-reactT.C:
			v.sendReaction(ctx, conn)
			reactT.Reset(time.Until(v.nextReactionAt()))
		case <-chatT.C:
			v.sendChat(ctx, conn)
			chatT.Reset(v.exp(v.chatMean()))
		case <-voteC:
			voteC = nil
			v.voted = false
			v.castVote()
		}
	}
}

// chatMean is the average gap between chat messages for this scenario.
func (v *viewer) chatMean() time.Duration {
	return time.Duration(float64(baseChatEvery) / math.Max(v.cfg.scn.chatMult, 0.01))
}

func (v *viewer) nextReactionAt() time.Time {
	mult := v.cfg.scn.reactionMult * v.cfg.peaks.multiplier(time.Since(v.cfg.start))
	return time.Now().Add(v.exp(time.Duration(float64(baseReactionEvery) / math.Max(mult, 0.01))))
}

// exp draws an exponential interval with the given mean, which gives Poisson-like arrivals.
func (v *viewer) exp(mean time.Duration) time.Duration {
	if mean <= 0 {
		mean = time.Millisecond
	}
	d := time.Duration(v.rng.ExpFloat64() * float64(mean))
	if d < 50*time.Millisecond {
		d = 50 * time.Millisecond
	}
	return d
}

func (v *viewer) sendReaction(ctx context.Context, conn *websocket.Conn) {
	emoji := reactionEmojis[v.rng.Intn(len(reactionEmojis))]
	payload, _ := json.Marshal(map[string]string{"t": "reaction", "emoji": emoji})
	v.write(ctx, conn, payload, "reaction")
	v.cfg.c.reactions.Add(1)
}

func (v *viewer) sendChat(ctx context.Context, conn *websocket.Conn) {
	text := chatPool[v.rng.Intn(len(chatPool))]
	payload, _ := json.Marshal(map[string]string{"t": "chat", "text": text})
	v.write(ctx, conn, payload, "chat")
	v.cfg.c.chats.Add(1)
}

func (v *viewer) write(ctx context.Context, conn *websocket.Conn, payload []byte, kind string) {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, payload); err != nil {
		v.cfg.c.errorKind("write_" + kind)
		return
	}
	v.cfg.c.msgsOut.Add(1)
	if v.cfg.c.prom != nil {
		v.cfg.c.prom.messages.WithLabelValues("out").Inc()
	}
}

// castVote fetches the active poll and casts one vote with the organic option weights.
func (v *viewer) castVote() {
	status, _, body, err := v.do(http.MethodGet, fmt.Sprintf("/api/v1/events/%s/polls/active", v.cfg.event), nil, false)
	if err != nil || status != http.StatusOK {
		v.cfg.c.errorKind("poll_fetch")
		return
	}
	var poll struct {
		ID      string   `json:"id"`
		Options []string `json:"options"`
	}
	if json.Unmarshal(body, &poll) != nil || poll.ID == "" || len(poll.Options) == 0 {
		v.cfg.c.errorKind("poll_parse")
		return
	}
	option := pickPollOption(v.rng, len(poll.Options))
	payload, _ := json.Marshal(map[string]int{"option": option})
	status, _, _, err = v.do(http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", payload, true)
	switch {
	case err != nil:
		v.cfg.c.errorKind("vote")
	case status == http.StatusOK:
		v.cfg.c.votes.Add(1)
	case status == http.StatusConflict:
		// Already voted (for example after a reconnect). Not an error.
	default:
		v.cfg.c.errorKind(fmt.Sprintf("vote_%d", status))
	}
}

// createSession posts /session with the viewer's city, with a few retries.
func (v *viewer) createSession() error {
	payload, _ := json.Marshal(map[string]string{"city": v.city, "display_name": fmt.Sprintf("Viewer %04d", v.id%10000)})
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		status, _, body, err := v.do(http.MethodPost, "/api/v1/session", payload, false)
		if err == nil && status == http.StatusOK {
			var out struct {
				Token string `json:"token"`
			}
			if err := json.Unmarshal(body, &out); err == nil && out.Token != "" {
				v.token = out.Token
				return nil
			}
			lastErr = errors.New("bad session body")
		} else if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("session HTTP %d", status)
		}
		if !v.sleepBackoff(attempt + 1) {
			break
		}
	}
	return lastErr
}

// do performs one REST call on the shared transport and records latency, status and X-Served-By.
func (v *viewer) do(method, path string, body []byte, auth bool) (int, http.Header, []byte, error) {
	c := v.cfg.c
	endpoint := strings.TrimRight(v.cfg.base.String(), "/") + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	ctx, cancel := context.WithTimeout(v.cfg.ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth && v.token != "" {
		req.Header.Set("Authorization", "Bearer "+v.token)
	}

	start := time.Now()
	resp, err := v.cfg.client.Do(req)
	elapsed := time.Since(start)
	c.httpRequests.Add(1)
	c.latency.observe(elapsed)
	if c.prom != nil {
		c.prom.latency.Observe(elapsed.Seconds())
	}
	if err != nil {
		c.httpErrors.Add(1)
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	c.served.add(resp.Header.Get(headerServedBy))
	if c.prom != nil {
		c.prom.requests.WithLabelValues(fmt.Sprint(resp.StatusCode)).Inc()
	}
	if resp.StatusCode >= 500 {
		c.httpErrors.Add(1)
	}
	return resp.StatusCode, resp.Header, data, nil
}

// dial opens the WebSocket through the shared transport. It returns the X-Served-By
// value from the upgrade response, which nginx sets from the node's X-Instance.
func (v *viewer) dial() (*websocket.Conn, string, error) {
	wsURL := *v.cfg.base
	if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	} else {
		wsURL.Scheme = "ws"
	}
	wsURL.Path = "/ws"
	wsURL.RawQuery = url.Values{"event": {v.cfg.event}, "token": {v.token}}.Encode()

	ctx, cancel := context.WithTimeout(v.cfg.ctx, dialTimeout)
	defer cancel()
	start := time.Now()
	conn, resp, err := websocket.Dial(ctx, wsURL.String(), &websocket.DialOptions{HTTPClient: v.cfg.client})
	v.cfg.c.dialLatency.observe(time.Since(start))
	if err != nil {
		return nil, "", err
	}
	served := ""
	if resp != nil {
		served = resp.Header.Get(headerServedBy)
	}
	conn.SetReadLimit(1 << 20)
	return conn, served, nil
}

func closeStatus(err error) int {
	if s := websocket.CloseStatus(err); s != -1 {
		return int(s)
	}
	return 0
}

// watchDuration draws a log-normal watch time with the given median, clamped to [30 s, 4 h].
func watchDuration(rng *rand.Rand, median time.Duration) time.Duration {
	z := rng.NormFloat64()
	d := time.Duration(float64(median) * math.Exp(0.6*z))
	if d < 30*time.Second {
		d = 30 * time.Second
	}
	if d > 4*time.Hour {
		d = 4 * time.Hour
	}
	return d
}
