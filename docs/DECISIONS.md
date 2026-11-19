# Architectural & Design Decisions (ADRs)

### ADR-001: JSON Data Shape Alignment with React Frontend
- **Context:** The frontend React components in `/frontend/src` expect specific data shapes (e.g. `trafficData` with `{time, requests}`, `serverRows` with `{name, zone, weight, requests, cpu, memory, status, color}`, and chat messages with `{id, name, text, time, color}`).
- **Decision:** All REST and WebSocket payloads match these exact field names and types to preserve the rule that frontend markup and CSS remain fixed.

### ADR-002: Instance Naming and Color Mapping
- **Context:** `OverviewPage.jsx` transforms instance names using `.replace('crowdcast-api-', 'Node ')` and renders theme-coded colors (`mint`, `peach`, `lilac`, `yellow`).
- **Decision:** The 3 backend instances are officially identified as `crowdcast-api-01`, `crowdcast-api-02`, and `crowdcast-api-03`. Their static zone labels are "Primary node", "Secondary node", and "Burst node", with weights 50, 30, and 20, assigned colors `mint`, `peach`, and `lilac`.

### ADR-003: WebSocket Message Envelope
- **Context:** CROWDCAST.md section 8 specifies WebSocket messages with type tag `t`.
- **Decision:** All WebSocket messages use JSON envelopes with `"t": "<type>"` discriminator. Inbound commands are `reaction`, `chat`, `vote`, `ping`. Outbound events are `welcome`, `presence`, `reaction_batch`, `chat`, `poll_update`, `event_state`, `stats_tick`, `node_state`, `pong`, and `error`.

### ADR-004: Poll Representation (Counts vs Percentages)
- **Context:** `LiveEventPage.jsx` renders `<b>{pollVotes[i]}%</b>` and `<i style={{width: \`${pollVotes[i]}%\`}}/>`, but also has `<div className="poll-foot">... {pollVotes.reduce((a,b)=>a+b,0)} votes</div>`.
- **Decision:** The API returns both raw `votes: [540, 310, 150]` and normalized `percentages: [54, 31, 15]`, as well as `total_votes: 1000`. The frontend integration hook will bind `pollVotes` to percentages for visual styling while surfacing total votes accurately.
