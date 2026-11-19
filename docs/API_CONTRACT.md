# CrowdCast API Contract & WebSocket Protocol Specification

This document is the authoritative specification for all REST endpoints, WebSocket messages, data schemas, and UI bindings for CrowdCast. It bridges the fixed React frontend in `/frontend` with the stateless Go backend instances (`api-01`, `api-02`, `api-03`).

---

## 1. REST API Contract (`/api/v1`)

### 1.1 Global Conventions & Error Format

- **Base URL:** `/api/v1`
- **Content-Type:** `application/json; charset=utf-8`
- **Authentication:**
  - Viewers: Bearer JWT in `Authorization: Bearer <token>` or WebSocket query parameter `?token=<token>`.
  - Admin operations: `X-Admin-Key: <ADMIN_API_KEY>` or Admin Bearer JWT.
- **Standard Error Response:**
  All error responses (4xx, 5xx) return the standard envelope:
  ```json
  {
    "error": {
      "code": "BAD_REQUEST",
      "message": "Detailed description of what failed",
      "details": {}
    }
  }
  ```
  Standard error codes: `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `BAD_REQUEST`, `RATE_LIMITED`, `NODE_OFFLINE`, `INTERNAL_SERVER_ERROR`.

---

### 1.2 Endpoints Specification

#### 1.2.1 Anonymous Session (`POST /api/v1/session`)
Initializes an anonymous viewer session.
- **Request Body (optional):**
  ```json
  {
    "display_name": "Aarav Mehta",
    "city": "Mumbai"
  }
  ```
  *Note:* If fields are omitted, the backend generates an anonymous name (`Viewer 4821`) and samples a realistic city from the seed distribution.
- **Success Response (`200 OK`):**
  ```json
  {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "viewer": {
      "id": "usr_01JCA0E3N58ABCDEF1234567",
      "display_name": "Aarav Mehta",
      "city": "Mumbai",
      "color": "mint",
      "first_seen_at": "2026-11-17T18:00:00Z"
    }
  }
  ```

#### 1.2.2 Get Event Metadata & State (`GET /api/v1/events/{id}`)
Fetches event metadata and live state.
- **Path Parameter:** `id` (e.g. `cc-0826` or UUID)
- **Success Response (`200 OK`):**
  ```json
  {
    "id": "cc-0826",
    "event_number": "EVENT #CC-0826",
    "title": "After Hours — The Sound of Now",
    "title_main": "AFTER HOURS",
    "subtitle": "THE SOUND OF NOW",
    "session_code": "SESSION 08",
    "description": "An immersive night of electronic sounds, visual art, and people who get it.",
    "host": {
      "name": "Aanya Verma",
      "avatar_letter": "A"
    },
    "stage": {
      "name": "STAGE 01 / MAIN ROOM",
      "resolution": "1080p · 30 FPS"
    },
    "date_eyebrow": "TUESDAY, NOVEMBER 17, 2026",
    "status": "live",
    "started_at": "2026-11-17T18:00:00Z",
    "live_for_formatted": "01:42:18",
    "live_for_seconds": 6138,
    "peak_audience": 13204,
    "current_attendees": 12840,
    "total_reactions": 28491
  }
  ```

#### 1.2.3 Event Lifecycle Controls (`POST /api/v1/events/{id}/{action}`)
Admin actions to change event state.
- **Allowed Actions:** `start`, `pause`, `resume`, `end`
- **Headers:** `X-Admin-Key: <ADMIN_API_KEY>` or Admin Bearer JWT
- **Success Response (`200 OK`):**
  ```json
  {
    "id": "cc-0826",
    "status": "paused",
    "updated_at": "2026-11-17T19:42:18Z"
  }
  ```

#### 1.2.4 Chat History (`GET /api/v1/events/{id}/chat?limit=50`)
Retrieves the most recent messages from Redis (persisted to PostgreSQL).
- **Query Parameters:** `limit` (default: 50, max: 200)
- **Success Response (`200 OK`):**
  ```json
  {
    "messages": [
      {
        "id": 1,
        "name": "Aarav Mehta",
        "text": "The visuals are INSANE 🔥",
        "time": "now",
        "color": "mint",
        "created_at": "2026-11-17T19:41:50Z"
      },
      {
        "id": 2,
        "name": "Ishita Rao",
        "text": "This is my favourite track!",
        "time": "1m",
        "color": "peach",
        "created_at": "2026-11-17T19:40:40Z"
      }
    ],
    "total_messages": 3842
  }
  ```

#### 1.2.5 Send Chat Message via REST (`POST /api/v1/events/{id}/chat`)
REST fallback for sending a chat message.
- **Request Body:**
  ```json
  {
    "text": "The bass drop was unreal!"
  }
  ```
- **Success Response (`201 Created`):**
  ```json
  {
    "id": 1731853320000,
    "name": "Aarav Mehta",
    "text": "The bass drop was unreal!",
    "time": "now",
    "color": "mint",
    "created_at": "2026-11-17T19:42:00Z"
  }
  ```

#### 1.2.6 Send Reaction via REST (`POST /api/v1/events/{id}/reactions`)
REST fallback for sending an emoji reaction.
- **Request Body:**
  ```json
  {
    "emoji": "🔥"
  }
  ```
- **Success Response (`200 OK`):**
  ```json
  {
    "emoji": "🔥",
    "reactions_sent": 28492
  }
  ```

#### 1.2.7 Active Poll & Results (`GET /api/v1/events/{id}/polls/active`)
Returns the currently active poll and vote distribution.
- **Success Response (`200 OK`):**
  ```json
  {
    "id": "poll-01",
    "question": "What's the energy level right now?",
    "status": "active",
    "options": [
      "Absolutely electric ⚡",
      "In the zone ✨",
      "Just warming up 🌱"
    ],
    "votes": [540, 310, 150],
    "percentages": [54, 31, 15],
    "total_votes": 1000,
    "user_voted_option": null
  }
  ```

#### 1.2.8 Cast Poll Vote (`POST /api/v1/polls/{pid}/vote`)
Casts a single vote for an active poll option.
- **Request Body:**
  ```json
  {
    "option": 0
  }
  ```
- **Success Response (`200 OK`):**
  ```json
  {
    "poll_id": "poll-01",
    "selected_option": 0,
    "votes": [541, 310, 150],
    "percentages": [54, 31, 15],
    "total_votes": 1001
  }
  ```
- **Conflict Response (`409 Conflict`):** If user already voted.
  ```json
  {
    "error": {
      "code": "ALREADY_VOTED",
      "message": "User has already voted in this poll"
    }
  }
  ```

#### 1.2.9 Overview Stats (`GET /api/v1/events/{id}/stats/overview`)
Aggregated data powering the **Overview** dashboard page.
- **Success Response (`200 OK`):**
  ```json
  {
    "event": {
      "id": "cc-0826",
      "event_number": "EVENT #CC-0826",
      "title": "After Hours — The Sound of Now",
      "status": "live",
      "live_for": "01:42:18",
      "peak_audience": "13,204",
      "date_eyebrow": "TUESDAY, NOVEMBER 17, 2026"
    },
    "metrics": {
      "live_attendees": {
        "value": "12,840",
        "raw": 12840,
        "change": "12.8%",
        "foot": "vs. last event",
        "tone": "mint"
      },
      "reactions_sent": {
        "value": "28,491",
        "raw": 28491,
        "change": "8.4%",
        "foot": "in this session",
        "tone": "coral"
      },
      "avg_response_time": {
        "value": "42 ms",
        "change": "6.2%",
        "foot": "faster than target",
        "tone": "lilac"
      },
      "requests_per_sec": {
        "value": "1,284",
        "change": "18.6%",
        "foot": "across all nodes",
        "tone": "yellow"
      }
    },
    "traffic_summary": {
      "requests_per_min": "12,840",
      "change": "18.6%",
      "legend": "Requests served across all instances"
    },
    "distribution": {
      "total_requests": "12,840",
      "weights": [50, 30, 20],
      "note": "Weighted routing is distributing traffic according to node capacity."
    },
    "server_rows": [
      {
        "name": "crowdcast-api-01",
        "zone": "Primary node",
        "weight": 50,
        "requests": "6,420",
        "cpu": 42,
        "memory": 58,
        "status": "Healthy",
        "color": "mint"
      },
      {
        "name": "crowdcast-api-02",
        "zone": "Secondary node",
        "weight": 30,
        "requests": "3,852",
        "cpu": 28,
        "memory": 46,
        "status": "Healthy",
        "color": "peach"
      },
      {
        "name": "crowdcast-api-03",
        "zone": "Burst node",
        "weight": 20,
        "requests": "2,568",
        "cpu": 17,
        "memory": 34,
        "status": "Healthy",
        "color": "lilac"
      }
    ],
    "activity": [
      {
        "id": "act-1",
        "title": "Audience milestone reached",
        "description": "12,000 attendees joined the room",
        "time_ago": "2 minutes ago",
        "icon": "Users",
        "tone": "mint"
      },
      {
        "id": "act-2",
        "title": "Traffic spike detected",
        "description": "Requests peaked at 1.4k req/s",
        "time_ago": "8 minutes ago",
        "icon": "Zap",
        "tone": "peach"
      },
      {
        "id": "act-3",
        "title": "All systems operational",
        "description": "Health checks passed on 3 nodes",
        "time_ago": "12 minutes ago",
        "icon": "ShieldCheck",
        "tone": "lilac"
      }
    ]
  }
  ```

#### 1.2.10 Traffic Series Chart Data (`GET /api/v1/events/{id}/stats/traffic?range=60m`)
Returns timeseries buckets for the Recharts AreaChart.
- **Success Response (`200 OK`):**
  ```json
  [
    { "time": "18:00", "requests": 320 },
    { "time": "18:05", "requests": 440 },
    { "time": "18:10", "requests": 390 },
    { "time": "18:15", "requests": 610 },
    { "time": "18:20", "requests": 540 },
    { "time": "18:25", "requests": 780 },
    { "time": "18:30", "requests": 690 },
    { "time": "18:35", "requests": 920 },
    { "time": "18:40", "requests": 860 },
    { "time": "18:45", "requests": 1120 },
    { "time": "18:50", "requests": 1040 },
    { "time": "18:55", "requests": 1280 }
  ]
  ```

#### 1.2.11 Audience Page Stats (`GET /api/v1/events/{id}/stats/audience`)
Data powering the **Audience** page.
- **Success Response (`200 OK`):**
  ```json
  {
    "metrics": {
      "total_attendees": {
        "value": "12,840",
        "change": "12.8%",
        "foot": "vs. last event",
        "tone": "mint"
      },
      "returning_audience": {
        "value": "68.4%",
        "change": "4.2%",
        "foot": "loyal listeners",
        "tone": "coral"
      },
      "avg_watch_time": {
        "value": "48 min",
        "change": "11.2%",
        "foot": "per attendee",
        "tone": "lilac"
      },
      "chat_messages": {
        "value": "3,842",
        "change": "22.1%",
        "foot": "this session",
        "tone": "yellow"
      }
    },
    "city_mix": [
      { "name": "Mumbai", "percentage": "28%" },
      { "name": "Delhi NCR", "percentage": "24%" },
      { "name": "Bengaluru", "percentage": "19%" },
      { "name": "Hyderabad", "percentage": "14%" },
      { "name": "Other cities", "percentage": "15%" }
    ],
    "top_reactions": [
      {
        "emoji": "🔥",
        "name": "Fire reactions",
        "subtitle": "Most used reaction",
        "count": "8,420"
      },
      {
        "emoji": "❤️",
        "name": "Heart reactions",
        "subtitle": "Second most used",
        "count": "6,180"
      },
      {
        "emoji": "👏",
        "name": "Applause",
        "subtitle": "Third most used",
        "count": "4,920"
      }
    ]
  }
  ```

#### 1.2.12 Infrastructure Fleet Status (`GET /api/v1/infra/nodes`)
Data powering the **Infrastructure** page and fleet management table.
- **Success Response (`200 OK`):**
  ```json
  {
    "fleet_status": "All systems healthy",
    "degraded": false,
    "metrics": {
      "total_requests": {
        "value": "12.8k",
        "change": "18.6%",
        "foot": "per minute"
      },
      "avg_latency": {
        "value": "42 ms",
        "change": "6.2%",
        "foot": "vs. previous hour"
      },
      "error_rate": {
        "value": "0.04%",
        "change": "0.02%",
        "foot": "5xx responses"
      },
      "active_connections": {
        "value": "8,492",
        "change": "9.3%",
        "foot": "across 3 nodes"
      }
    },
    "server_rows": [
      {
        "name": "crowdcast-api-01",
        "zone": "Primary node",
        "weight": 50,
        "requests": "6,420",
        "cpu": 42,
        "memory": 58,
        "status": "Healthy",
        "color": "mint"
      },
      {
        "name": "crowdcast-api-02",
        "zone": "Secondary node",
        "weight": 30,
        "requests": "3,852",
        "cpu": 28,
        "memory": 46,
        "status": "Healthy",
        "color": "peach"
      },
      {
        "name": "crowdcast-api-03",
        "zone": "Burst node",
        "weight": 20,
        "requests": "2,568",
        "cpu": 17,
        "memory": 34,
        "status": "Healthy",
        "color": "lilac"
      }
    ],
    "monitoring_services": [
      {
        "name": "Prometheus",
        "subtitle": "Metrics collection · 5s scrape",
        "status": "Running",
        "good": true
      },
      {
        "name": "Grafana",
        "subtitle": "Dashboards · Port 3000",
        "status": "Running",
        "good": true
      }
    ]
  }
  ```

#### 1.2.13 Chaos Injections (`POST /api/v1/admin/nodes/{name}/{action}`)
Injected failure and recovery controls via Redis control pub/sub channel `ps:control`.
- **Allowed Actions:** `fail`, `restore`, `latency`
- **Request Body for `latency`:**
  ```json
  {
    "latency_ms": 300
  }
  ```
- **Success Response (`200 OK`):**
  ```json
  {
    "node": "crowdcast-api-02",
    "action": "fail",
    "success": true,
    "current_status": "Offline"
  }
  ```

#### 1.2.14 Operations & Probes (`/healthz`, `/readyz`, `/metrics`)
- `GET /healthz`: Returns `200 OK` (`{"status":"ok"}`) when node is healthy; returns `503 Service Unavailable` (`{"status":"unhealthy","reason":"chaos_injected"}`) when chaos `fail` is active.
- `GET /readyz`: Returns `200 OK` (`{"ready":true}`) when Redis and Postgres connections are verified healthy; `503` otherwise.
- `GET /metrics`: Standard Prometheus metrics scrapable by Prometheus agent.

---

## 2. WebSocket Protocol (`/ws`)

### 2.1 Connection Handshake
- **URL:** `GET /ws?event={id}&token={jwt}`
- **Upgrade:** `Upgrade: websocket`, `Connection: Upgrade`
- **Subprotocol / Ping:** Server initiates heartbeat ping frame every 15s. Idle timeout 30s.

### 2.2 Client to Server Messages

| Message Type (`t`) | Direction | Trigger | Payload Schema |
|---|---|---|---|
| `reaction` | C → S | User clicks emoji reaction button | `{"t":"reaction", "emoji":"🔥"}` (emoji must be one of: `❤️`, `🔥`, `👏`, `😍`, `⚡`, `🎉`) |
| `chat` | C → S | User submits chat input form | `{"t":"chat", "text":"Message text up to 280 chars"}` |
| `vote` | C → S | User clicks poll option | `{"t":"vote", "poll":"poll-01", "option":0}` |
| `ping` | C → S | Periodic client heartbeat | `{"t":"ping"}` |

### 2.3 Server to Client Messages

| Message Type (`t`) | Direction | Frequency / When Sent | Payload Schema |
|---|---|---|---|
| `welcome` | S → C | Immediately on socket open | `{"t":"welcome", "node":"crowdcast-api-01", "viewer_id":"usr_...", "event_id":"cc-0826"}` |
| `presence` | S → C | Every 2-3s or on viewer count change | `{"t":"presence", "attendees":12840, "delta_10m":"12.8%", "bars":[32,48,42,68,54,78,63,88,72,100,84,92,76,95,82,100,88,94]}` |
| `reaction_batch` | S → C | Coalesced broadcast every ~250ms | `{"t":"reaction_batch", "reactions":{"🔥":12,"❤️":5,"👏":2}, "total":28491}` |
| `chat` | S → C | As messages arrive (fan-out via Redis) | `{"t":"chat", "message":{"id":1731853320000, "name":"Aarav Mehta", "text":"The visuals are INSANE 🔥", "time":"now", "color":"mint"}}` |
| `poll_update` | S → C | On vote or poll state change | `{"t":"poll_update", "poll_id":"poll-01", "votes":[541,310,150], "percentages":[54,31,15], "total_votes":1001}` |
| `event_state` | S → C | When admin changes event state | `{"t":"event_state", "status":"live", "live_for":"01:42:18", "peak_audience":13204}` |
| `stats_tick` | S → C | Every 2-5s for dashboard sync | `{"t":"stats_tick", "attendees":12840, "reactions":28491, "rps":1284, "avg_latency_ms":42}` |
| `node_state` | S → C | On node failure or recovery | `{"t":"node_state", "node":"crowdcast-api-02", "status":"Offline"}` |
| `pong` | S → C | In response to client `{"t":"ping"}` | `{"t":"pong", "timestamp":1731853320000}` |
| `error` | S → C | On validation, rate-limit or authorization error | `{"t":"error", "code":"RATE_LIMITED", "message":"You are sending messages too fast"}` |

---

## 3. UI Element / Page to Endpoint & WebSocket Mapping Table

| UI Page & Section | Visual Component / Element | Source / Binding | Update Channel |
|---|---|---|---|
| **Overview** | Page Header Eyebrow Date | `GET /stats/overview` (`event.date_eyebrow`) | REST initial load |
| **Overview** | Event Banner Tag ("HAPPENING NOW" / "PAUSED") | `GET /stats/overview` (`event.status`) | WS `event_state` |
| **Overview** | Event Banner Meta ("12,840 attending") | `GET /stats/overview` (`metrics.live_attendees.raw`) | WS `presence` / `stats_tick` |
| **Overview** | Event Banner Side Stats (LIVE FOR, PEAK AUDIENCE) | `GET /stats/overview` (`event.live_for`, `event.peak_audience`) | WS `event_state` |
| **Overview** | Metric Card 1: "Live attendees" (12,840) | `GET /stats/overview` (`metrics.live_attendees`) | WS `presence` / `stats_tick` |
| **Overview** | Metric Card 2: "Reactions sent" (28,491) | `GET /stats/overview` (`metrics.reactions_sent`) | WS `reaction_batch` / `stats_tick` |
| **Overview** | Metric Card 3: "Avg. response time" (42 ms) | `GET /stats/overview` (`metrics.avg_response_time`) | WS `stats_tick` |
| **Overview** | Metric Card 4: "Requests / sec" (1,284) | `GET /stats/overview` (`metrics.requests_per_sec`) | WS `stats_tick` |
| **Overview** | Traffic Overview Chart Summary ("12,840 req/min") | `GET /stats/overview` (`traffic_summary`) | REST poll (30s) / WS `stats_tick` |
| **Overview** | Traffic Overview AreaChart series | `GET /stats/traffic?range=60m` | REST poll (30s) |
| **Overview** | Traffic Distribution ("12,840 total requests") | `GET /stats/overview` (`distribution.total_requests`) | REST poll / WS `stats_tick` |
| **Overview** | Traffic Distribution Stacked Bar (50/30/20%) | `GET /stats/overview` (`distribution.weights`) | REST poll |
| **Overview** | Fleet Health Backend Instances Table | `GET /stats/overview` (`server_rows`) | REST poll / WS `node_state` |
| **Overview** | Recent Activity Feed (3 items) | `GET /stats/overview` (`activity`) | REST poll / WS `node_state` |
| **Live Event** | Event Status Pill & "Pause/Resume event" button | `POST /events/{id}/pause` / `resume` | WS `event_state` |
| **Live Event** | Stage Card Top Tag & Bottom Viewer Count | `GET /events/{id}` | WS `event_state` & `presence` |
| **Live Event** | Stage "End stream" button | `POST /events/{id}/end` | WS `event_state` |
| **Live Event** | Quick Pulse Check Poll: Question & Options | `GET /events/{id}/polls/active` | WS `poll_update` |
| **Live Event** | Quick Pulse Check Poll: Click Option | `POST /polls/{pid}/vote` or WS `{"t":"vote"}` | WS `poll_update` |
| **Live Event** | Quick Pulse Check Poll: Total Votes Footer | Calculated from sum of option vote counts | WS `poll_update` |
| **Live Event** | In the Room Audience count ("12,840 people") | WS `presence` (`attendees`) | WS `presence` |
| **Live Event** | In the Room 18-Point Sparkline Bars | WS `presence` (`bars`) | WS `presence` |
| **Live Event** | Live Chat Messages List & Total Count | `GET /events/{id}/chat` | WS `chat` |
| **Live Event** | Chat Input Submit | `POST /events/{id}/chat` or WS `{"t":"chat"}` | WS `chat` broadcast |
| **Live Event** | Send Reaction Emoji Buttons | `POST /events/{id}/reactions` or WS `{"t":"reaction"}` | WS `reaction_batch` |
| **Infrastructure** | Fleet Health Status Pill ("All systems healthy") | `GET /infra/nodes` (`fleet_status`) | WS `node_state` |
| **Infrastructure** | Metric Cards (Total reqs, Avg latency, Error rate, Conns)| `GET /infra/nodes` (`metrics`) | REST poll (5s) |
| **Infrastructure** | "Simulate node failure" / "Restore backend 02" | `POST /admin/nodes/{name}/fail` / `restore` | WS `node_state` |
| **Infrastructure** | Backend Node Management Table | `GET /infra/nodes` (`server_rows`) | REST poll / WS `node_state` |
| **Infrastructure** | Node Capacity Bars (% CPU per node) | `GET /infra/nodes` (`server_rows[].cpu`) | REST poll (5s) |
| **Infrastructure** | Monitoring Services (Prometheus & Grafana) | `GET /infra/nodes` (`monitoring_services`) | REST poll (15s) |
| **Audience** | Metric Cards (Total attendees, Returning %, Watch time, Chat) | `GET /stats/audience` (`metrics`) | REST poll (10s) |
| **Audience** | Where the crowd is from (City breakdown) | `GET /stats/audience` (`city_mix`) | REST poll (10s) |
| **Audience** | What gets them going (Top reactions) | `GET /stats/audience` (`top_reactions`) | WS `reaction_batch` / REST |
| **Topbar & Sidebar** | System Status Pill & Audience Badge | Derived from `attendees` & node health | WS `presence` & `node_state` |

---

## 4. Frontend Quirks & Integration Notes

During inspection of `/frontend/src` (`App.jsx`, `pages/*`, `components/*`, and `data/dashboardData.js`), the following quirks were cataloged. The backend and the integration hook layer must accommodate them without modifying the UI markup or CSS:

1. **Poll Footer Sums Percentages Instead of Raw Counts:**
   - *Location:* `LiveEventPage.jsx` line 22:
     ```jsx
     <span>{pollVotes.reduce((a, b) => a + b, 0)} votes</span>
     ```
   - *Issue:* In `App.jsx`, `pollVotes` is initialized to percentages `[54, 31, 15]`, so summing them displays `100 votes` instead of the actual number of cast votes.
   - *Integration Handling:* In `App.jsx` integration layer, maintain both raw vote counts `pollVoteCounts` and percentages `pollVotes`. The hook will populate `pollVotes` with percentages (for the bar width display: `width: ${pollVotes[i]}%` and `<b>{pollVotes[i]}%</b>`), but ensure total votes can be appropriately reflected or calculated.

2. **Hardcoded Date Eyebrow in Overview Page:**
   - *Location:* `OverviewPage.jsx` line 20:
     ```jsx
     <div className="eyebrow"><span className="eyebrow-line"/> TUESDAY, NOVEMBER 17, 2026</div>
     ```
   - *Integration Handling:* The seed event in the database matches this date (`2026-11-17T18:00:00Z`). The API provides `date_eyebrow` in `GET /events/{id}` for consistency.

3. **Hardcoded Server Names, Zones, and Colors:**
   - *Location:* `dashboardData.js` lines 10-14:
     - `name`: `'crowdcast-api-01'`, `zone`: `'Primary node'`, `weight`: `50`, `color`: `'mint'`
     - `name`: `'crowdcast-api-02'`, `zone`: `'Secondary node'`, `weight`: `30`, `color`: `'peach'`
     - `name`: `'crowdcast-api-03'`, `zone`: `'Burst node'`, `weight`: `20`, `color`: `'lilac'`
   - *UI Logic in `OverviewPage.jsx` line 34:*
     ```jsx
     s.name.replace('crowdcast-api-', 'Node ')
     ```
   - *Requirement:* Backend instances must use the exact names `crowdcast-api-01`, `crowdcast-api-02`, and `crowdcast-api-03` with weights 50, 30, and 20 so `replace('crowdcast-api-', 'Node ')` renders `"Node 01"`, `"Node 02"`, and `"Node 03"`.

4. **Hardcoded Chaos Toggle in `InfrastructurePage.jsx`:**
   - *Location:* `InfrastructurePage.jsx` line 22 assumes index 1 (`crowdcast-api-02`) is the target of the failure simulation:
     ```jsx
     serverDown && i === 1 ? 'Offline' : 'Healthy'
     ```
   - *Requirement:* The chaos control API endpoint must target `crowdcast-api-02` by default when the user clicks **Simulate node failure**.

5. **Client-Side Simulation Interval in `App.jsx`:**
   - *Location:* `App.jsx` lines 32-39:
     ```jsx
     useEffect(() => {
       if (!eventLive || serverDown) return undefined;
       const timer = setInterval(() => {
         setAttendees(value => value + Math.floor(Math.random() * 5));
         setReactions(value => value + Math.floor(Math.random() * 9));
       }, 4500);
       return () => clearInterval(timer);
     }, [eventLive, serverDown]);
     ```
   - *Requirement:* The integration layer in `App.jsx` replaces this local mock interval with actual WebSocket event streams (`presence` and `reaction_batch`).

6. **Chat Messages Slice & Count Logic:**
   - *Location:* `App.jsx` lines 52-55 slices messages to the last 7 items: `.slice(-7)`.
   - *Location:* `LiveEventPage.jsx` line 24 computes:
     ```jsx
     <h3>Live chat <span className="chat-count">{messages.length + 128}</span></h3>
     ```
   - *Integration Handling:* In the integration layer, provide the full message buffer or supply a message array whose length or props correctly map to the UI.

7. **Avatar Color Classes:**
   - *Location:* `LiveEventPage.jsx` and `OverviewPage.jsx` use CSS avatar classes `mint`, `peach`, `lilac`, and `yellow`.
   - *Requirement:* Backend must return viewer and node colors constrained to `["mint", "peach", "lilac", "yellow"]`.

---
