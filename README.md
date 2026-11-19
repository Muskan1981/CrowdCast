# CrowdCast:

> Real-time virtual event & concert platform that simulates thousands of concurrent viewers, runs on multiple Go backend instances behind a **weighted load balancer**, and is fully observable via **Prometheus + Grafana**. The point of the project is to *demonstrate* how a system behaves under traffic spikes and backend failures.

---

## 1. What we are building

| Layer | What it is |
|---|---|
| **Frontend** | Existing React + Vite + Recharts dashboard (already built; styling stays untouched). Lives in `/frontend`. |
| **Backend** | Go service (`crowdcast-api`) run as **3 identical instances** (`api-01`, `api-02`, `api-03`). Stateless. Any node can serve any client. |
| **Shared state / fan-out** | Redis (counters, presence, chat buffer, poll votes, **pub/sub** to sync instances) |
| **Durable storage** | PostgreSQL (events, polls, votes, chat history, attendance sessions, rollups) |
| **Load balancer** | Nginx, weighted round-robin **5 : 3 : 2** (= 50 / 30 / 20 %), passive + active health checks, WebSocket upgrade support. Optional stretch: swap in a custom Go L7 balancer. |
| **Observability** | Prometheus (5s scrape), Grafana (provisioned dashboards), Alertmanager (optional), exporters for Redis / Postgres / Nginx / containers |
| **Traffic simulation** | `loadgen`, a Go CLI that spawns thousands of virtual viewers (WebSocket + REST) with ramp / spike / soak scenarios |
| **Chaos** | Admin endpoints to kill/restore a node, inject latency, drop Redis, so failure handling is demo-able live from the UI |

## 2. Goals

1. Handle **thousands of concurrent WebSocket users** per event on a laptop-scale Docker Compose stack.
2. Show **weighted distribution** is real: Grafana and the UI both display ~50/30/20 request split.
3. Show **graceful failure**: kill `api-02` → LB stops routing to it, clients reconnect to healthy nodes, error rate stays low, dashboards show it.
4. Show **traffic spikes** being absorbed (ramp 500 → 5,000 viewers in under a minute) with latency staying in budget.
5. Everything reproducible with **one command**: `make up`.

## 3. Architecture

```
                           ┌──────────────────────────┐
   Browser (React) ───────▶│  Nginx LB (weighted RR)  │◀─── loadgen (virtual viewers)
   REST + WebSocket        └────┬──────────┬─────────┬┘
                                │ w=5      │ w=3     │ w=2
                         ┌──────▼───┐ ┌────▼─────┐ ┌─▼────────┐
                         │ api-01   │ │ api-02   │ │ api-03   │   Go, stateless
                         └──┬───┬───┘ └──┬───┬───┘ └──┬───┬───┘
                            │   │        │   │        │   │
              ┌─────────────▼───▼────────▼───▼────────▼───▼─────┐
              │  Redis: counters · presence · chat buffer ·     │
              │  poll votes · node registry · PUB/SUB fan-out   │
              └──────────────────────┬──────────────────────────┘
                                     │ async write-behind
                              ┌──────▼───────┐
                              │  PostgreSQL  │
                              └──────────────┘

   Prometheus ──scrapes──▶ api-01/02/03 /metrics · nginx-exporter · redis-exporter · postgres-exporter · cAdvisor
   Grafana ──queries──▶ Prometheus          Backend /stats/* ──queries──▶ Prometheus (feeds the React dashboard)
```

**Key design decision:** because every realtime event goes through **Redis pub/sub**, a viewer connected to `api-01` sees chat/reactions/votes from viewers on `api-02`/`api-03`. No sticky sessions needed, so the LB is free to do pure weighted distribution and failover is trivial.

## 4. Tech stack

- **Go 1.23+**, `chi` router, `coder/websocket` (or `gorilla/websocket`), `pgx` + `sqlc` (or plain pgx), `go-redis/v9`, `prometheus/client_golang`, `log/slog`, `golang-migrate`, `testify`, `testcontainers-go`
- **Redis 7**, **PostgreSQL 16**
- **Nginx** (+ `nginx-prometheus-exporter`), **Prometheus**, **Grafana**, `redis_exporter`, `postgres_exporter`, **cAdvisor**
- **Docker + Docker Compose**, **Makefile**, **GitHub Actions**

## 5. Features

### 5.1 Event lifecycle
- Seeded event: **"After Hours, The Sound of Now"**, `EVENT #CC-0826`, host *Aanya Verma*, "Session 08".
- States: `scheduled → live → paused → ended`. Admin can start / pause / resume / end (maps to **Pause event / Resume event / End stream** buttons).
- State changes broadcast to all viewers instantly over WS.
- "Live for" timer derived from `started_at` minus paused time. Peak audience tracked.

### 5.2 Realtime gateway (WebSocket)
- `GET /ws?event=<id>&token=<jwt>`: one socket per viewer.
- Server heartbeat (ping/pong), idle timeout, per-connection send buffer with **slow-consumer drop**, max message size, per-connection rate limits.
- Per-instance **Hub** with event rooms; subscribes to Redis channel per event and fans out to local sockets.
- Graceful drain on shutdown: send close code `1012` so clients reconnect through the LB.
- Client reconnect with exponential backoff + jitter (frontend hook).

### 5.3 Presence & audience count
- Join/leave tracked in Redis ZSET (`last_seen` heartbeat), so crashed nodes' users expire automatically.
- Live attendee number (UI: "12,840 watching"), peak audience, "joined in last 10 min" delta, 18-point audience sparkline bars.

### 5.4 Reactions
- Emojis: ❤️ 🔥 👏 😍 ⚡ 🎉 (whitelist-validated).
- Hot path: `INCRBY` in Redis, **coalesced broadcast** every ~250 ms as `reaction_batch` (not one message per tap, which is what keeps it scalable).
- Per-user rate limit (token bucket). Totals per emoji → powers the Engagement panel (Fire / Heart / Applause counts).
- Async rollup to Postgres per minute.

### 5.5 Live chat
- Send/receive over WS (REST fallback `POST`), capped Redis list (last 200) for history on join, async persist to Postgres.
- Validation: length ≤ 280, trim, basic profanity/spam filter, duplicate-message guard, per-user rate limit (e.g. 1 msg / 1.5 s).
- Message shape matches the UI: `{id, name, text, time, color}`. Total count shown ("Live chat 3,842").

### 5.6 Polls
- Create / activate / close polls (admin). Seeded poll: *"What's the energy level right now?"* with 3 options.
- **One vote per user per poll** (Redis `SADD` guard + Postgres unique constraint).
- Results returned as counts **and** percentages; live `poll_update` broadcast (throttled).

### 5.7 Analytics (feeds Overview + Audience pages)
- **Overview:** live attendees, reactions sent, avg response time, requests/sec, traffic chart (last 60 min, 5-min buckets), traffic distribution across nodes, recent activity feed (milestones, spikes, health).
- **Audience:** total attendees, returning audience %, avg watch time, chat message count, city mix (Mumbai / Delhi NCR / Bengaluru / Hyderabad / Other), top reactions.
- Activity feed is generated by a **detector goroutine** (audience milestones every 1,000; traffic spike when req/s > 1.5× rolling avg; health state changes).

### 5.8 Infrastructure & node management (feeds Infrastructure page)
- Node registry: each instance heartbeats `{name, zone, weight, rps, cpu%, mem%, health, conns}` to Redis every 2 s.
- `GET /api/v1/infra/nodes` returns the table (Node / Traffic weight / Requests / CPU+Memory / Health), plus totals (total requests, avg latency, error rate, active connections).
- **Chaos controls** (maps to **Simulate node failure / Restore backend 02**):
  - `POST /api/v1/admin/nodes/{name}/fail`: node's `/healthz` → 503, closes WS with 1012, stops accepting new work
  - `POST /api/v1/admin/nodes/{name}/restore`
  - `POST /api/v1/admin/nodes/{name}/latency` `{ms}`: injected latency
  - Commands are delivered over a Redis **control channel**, so *any* node can instruct *any other* node.
- Observability stack status (Prometheus / Grafana health) shown in "Monitoring services".

### 5.9 Auth (lightweight)
- Viewers: anonymous join → server issues short-lived JWT with `{user_id, display_name, city}`.
- Admin: static admin API key / JWT for `/admin/*` and lifecycle endpoints.

### 5.10 Load generator
- `loadgen run --scenario ramp --users 5000 --duration 5m --target http://localhost:8080`
- Scenarios: `steady`, `ramp`, `spike`, `soak`, `chat-storm`, `reaction-storm`.
- Each virtual user: join → WS connect → reacts/chats/votes on realistic probability curves → leaves. Assigns a city for the audience mix.
- Prints live summary (connected, msgs/s, p50/p95/p99, errors).

## 6. Load balancing design

- Nginx `upstream` with `weight=5/3/2`, `max_fails=2 fail_timeout=10s`, `keepalive`, and `proxy_next_upstream error timeout http_503` (idempotent requests only).
- WebSocket: `proxy_http_version 1.1`, `Upgrade`/`Connection` headers, long `proxy_read_timeout`.
- Active health: apps expose `/healthz` (liveness + chaos flag) and `/readyz` (Redis/Postgres reachable). Compose healthchecks + passive Nginx checks; optional `nginx_upstream_check`/Nginx Plus-style alternative documented.
- Failure math to demo: with `api-02` down, split becomes ≈ **71 / 0 / 29** (5:2 renormalised); dashboards must show it.
- **Stretch:** custom Go L7 weighted LB (smooth weighted round-robin + health checks + `/metrics`) as drop-in replacement for Nginx.

## 7. Data model

### Redis keys
| Key | Type | Purpose |
|---|---|---|
| `ev:{id}:state` | HASH | status, started_at, paused_ms, peak_audience |
| `ev:{id}:presence` | ZSET | user_id → last_seen (heartbeat); count = `ZCOUNT` in window |
| `ev:{id}:reactions` | HASH | emoji → count |
| `ev:{id}:chat` | LIST | last 200 messages (JSON), `LTRIM`'d |
| `ev:{id}:chatcount` | STRING | total chat messages |
| `poll:{pid}:votes` | HASH | option_idx → count |
| `poll:{pid}:voters` | SET | user_ids that voted |
| `nodes` | HASH / ZSET | node registry heartbeat payloads |
| `chaos:{node}` | HASH | fail flag, latency ms |
| `ps:ev:{id}` | PUB/SUB | realtime event fan-out |
| `ps:control` | PUB/SUB | node control commands |

### Postgres tables
`events`, `polls`, `poll_options`, `poll_votes (poll_id, user_id UNIQUE)`, `viewers (id, display_name, city, first_seen_at)`, `attendance_sessions (viewer_id, event_id, joined_at, left_at)`, `chat_messages`, `reaction_rollups (event_id, minute, emoji, count)`, `activity_log`.

## 8. API

Base: `/api/v1`. JSON. Errors: `{ "error": { "code": "...", "message": "..." } }`.

### REST
| Method & path | Purpose |
|---|---|
| `POST /session` | anonymous join → JWT + profile |
| `GET /events/{id}` | event metadata + state |
| `POST /events/{id}/start \| pause \| resume \| end` | lifecycle (admin) |
| `GET /events/{id}/chat?limit=` | chat history |
| `POST /events/{id}/chat` | send message (REST fallback) |
| `POST /events/{id}/reactions` | send reaction (REST fallback) |
| `GET /events/{id}/polls/active` | active poll + results |
| `POST /polls/{pid}/vote` | cast vote |
| `GET /events/{id}/stats/overview` | metric cards, banner stats, distribution, activity |
| `GET /events/{id}/stats/traffic?range=60m` | traffic chart series |
| `GET /events/{id}/stats/audience` | audience page data |
| `GET /infra/nodes` | node table + capacity + totals |
| `POST /admin/nodes/{name}/fail \| restore \| latency` | chaos |
| `GET /healthz`, `/readyz`, `/metrics` | ops |

### WebSocket protocol (`/ws`)
Client → server: `{"t":"reaction","emoji":"🔥"}` · `{"t":"chat","text":"..."}` · `{"t":"vote","poll":"..","option":0}` · `{"t":"ping"}`
Server → client: `welcome` · `presence` · `reaction_batch` · `chat` · `poll_update` · `event_state` · `stats_tick` · `node_state` · `error`

## 9. Observability

**App metrics (per instance, label `instance`):**
`http_requests_total{route,method,status}`, `http_request_duration_seconds` (histogram), `ws_connections_active`, `ws_messages_total{type,dir}`, `ws_dropped_slow_consumer_total`, `reactions_total{emoji}`, `chat_messages_total`, `poll_votes_total`, `redis_op_duration_seconds`, `pubsub_lag_seconds`, `app_errors_total`, plus Go runtime/process collectors.

**Grafana dashboards (provisioned as code):**
1. *CrowdCast Overview*: req/s, p50/p95/p99 latency, error %, active connections, audience
2. *Load Balancer & Distribution*: per-node req/s share vs target weights, upstream response times
3. *Node Health*: CPU, memory, goroutines, GC, WS conns per node, up/down state
4. *Redis & Postgres*: ops/s, latency, memory, connections
5. *Chaos / Failure Drill*: annotated timeline of fail/restore events, error spike, recovery time

**Alerts (Prometheus rules):** node down > 15 s, 5xx rate > 1 %, p95 > 250 ms for 2 m, WS drop rate high, Redis unavailable, traffic distribution deviating > 15 pts from weights.

## 10. Demo scenarios (the "wow" script)

1. **Baseline:** `make up`, open UI + Grafana, 500 viewers steady → ~50/30/20 split.
2. **Spike:** `make spike` → 500 → 5,000 viewers in 60 s; latency stays flat; traffic chart climbs live.
3. **Node failure:** click **Simulate node failure** in the UI → `api-02` offline, share becomes ~71/29, brief error blip, auto-recovery, Grafana annotation.
4. **Restore:** click **Restore backend 02** → weights return to 50/30/20.
5. **Redis hiccup:** `make chaos-redis` → degraded mode (reactions/chat buffered locally, polls read-only), recovers cleanly.
6. **Slow node:** inject 300 ms latency on `api-03` → p95 per-node divergence visible.

## 11. Repo layout

```
crowdcast/
├── frontend/                  # existing React app (UI unchanged)
├── backend/
│   ├── cmd/
│   │   ├── api/               # main service
│   │   └── loadgen/           # traffic simulator
│   ├── internal/
│   │   ├── config/  auth/  httpapi/  ws/  hub/  events/  reactions/
│   │   ├── chat/  polls/  presence/  stats/  infra/  chaos/
│   │   ├── store/ (redis, postgres)  metrics/  middleware/
│   ├── migrations/
│   └── Dockerfile
├── deploy/
│   ├── docker-compose.yml
│   ├── nginx/ (nginx.conf, conf.d/)
│   ├── prometheus/ (prometheus.yml, rules/)
│   ├── grafana/ (provisioning/, dashboards/)
│   └── alertmanager/
├── docs/ (API_CONTRACT.md, ARCHITECTURE.md, RUNBOOK.md, DEMO.md)
├── .github/workflows/ci.yml
├── Makefile
└── README.md
```

## 12. Configuration (env)

`INSTANCE_ID`, `INSTANCE_ZONE`, `INSTANCE_WEIGHT`, `HTTP_ADDR`, `REDIS_URL`, `DATABASE_URL`, `PROMETHEUS_URL`, `JWT_SECRET`, `ADMIN_API_KEY`, `ALLOWED_ORIGINS`, `WS_MAX_CONNS`, `REACTION_BATCH_MS`, `CHAT_RATE_LIMIT`, `LOG_LEVEL`.

## 13. Frontend ↔ backend map

| UI element | Source |
|---|---|
| Overview metric cards, banner (live for, peak, attending) | `GET /stats/overview` + WS `stats_tick` |
| Traffic overview chart | `GET /stats/traffic` (Prometheus `query_range`) |
| Traffic distribution + Backend instances | `GET /infra/nodes` |
| Recent activity | `stats/overview.activity[]` |
| Live room: Pause / Resume / End | lifecycle endpoints + WS `event_state` |
| Audience count + sparkline | WS `presence` |
| Live chat | WS `chat` + `GET /chat` |
| Quick pulse poll | `GET /polls/active`, `POST /vote`, WS `poll_update` |
| Reactions | WS `reaction`/`reaction_batch` |
| Infrastructure: Simulate node failure | `POST /admin/nodes/{name}/fail \| restore` |
| Audience page | `GET /stats/audience` |

Frontend work is limited to an **API/WebSocket layer + hooks** replacing demo state in `App.jsx`; markup and CSS remain as they are. Small fixes: the poll footer should total *counts*, not percentages, and the date eyebrow should come from the event.

## 14. Non-functional targets

- 5,000+ concurrent WS connections on the 3-node compose stack
- p95 REST latency < 100 ms, WS fan-out latency < 300 ms at target load
- Failover: errors visible < 10 s after node loss; zero data loss for votes/chat
- Graceful shutdown drains in < 15 s
- Test coverage > 70 % on core packages; CI green on every PR

## 15. Phases

1. Foundation (scaffold, schema, config, contract)
2. Core API + realtime (hub, Redis, reactions, chat, polls)
3. Analytics + infra registry + chaos
4. DevOps (Docker, Nginx LB, Prometheus, Grafana, alerts)
5. Loadgen + frontend integration
6. Tests, CI, docs, demo rehearsal

## 16. Out of scope

Real video/audio streaming (the "stage" is a visual placeholder), payments/ticketing, real user accounts/OAuth, multi-region, Kubernetes (can be a follow-up: Helm chart + HPA).