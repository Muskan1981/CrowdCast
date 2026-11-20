# CrowdCast performance

Numbers from local benchmarks. Re-run them with the commands below before
quoting them; they vary with hardware and background load.

## Environment

| | |
|---|---|
| Machine | Apple M4 (10 logical CPUs), macOS, darwin/arm64 |
| Go | 1.27.4 (module declares 1.26) |
| Redis | 7 (compose service, loopback) |
| Date | 2026-11-20 |

## Reaction hot path

The code every emoji tap runs: validation, the per-user token bucket and the
batch counter. Redis is not involved; the batch flusher is the only part that
talks to Redis, and it runs every 250 ms, off this path.

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkRecordReactionHotPath` (one caller) | 65.5 | 0 | 0 |
| `BenchmarkRecordReactionParallel` (10 goroutines) | 251.6 | 15 | 1 |

At 250 ns per tap, one core accepts about 4 million taps per second. The
limit for the platform is the fan-out, not this path.

## Hub fan-out (in-process)

One room goroutine delivering one frame to N in-memory clients. The clients
count frames and do nothing else, so this isolates the room's own cost.

| Clients | ns per frame | per recipient |
|---:|---:|---:|
| 1 | 180 | 180 ns |
| 100 | 754 | 7.5 ns |
| 5,000 | 35,322 | 7.1 ns |

## Real-socket fan-out

One Redis `PUBLISH`, the subscriber, the room, and one socket write per
client, on loopback. Time is until every client has read the frame.

| Sockets | ms per frame | µs per socket | allocs per frame |
|---:|---:|---:|---:|
| 1 | 0.23 | 230 | 59 |
| 100 | 0.67 | 6.7 | 2,139 |
| 1,000 | 6.0 | 6.0 | 21,044 |
| 5,000 | 27.3 | 5.5 | 106,776 |

The cost is linear at about 5.5–6.7 µs per socket. The 5,000-socket run is
one process holding 5,000 client and 5,000 server sockets, so it also
includes the cost of the test client.

A single Redis `PUBLISH` round trip is about 100 µs (`BenchmarkHubPublish`).

## What these numbers mean for the target

The spec target is 5,000 concurrent viewers per event across three nodes.
Per node that is about 1,700 sockets. At ~6 µs per socket, the fan-out for
one frame to 1,700 local sockets is about 10 ms of CPU per frame on one core.
The reaction batch goes out 4 times a second and presence ticks every 1.5 s,
so the steady-state load is a few tens of ms of fan-out per second per node.
Socket writes from one process are not the limit at this scale.

These are loopback numbers. They do not include network latency between
nodes or the nginx hop, which the end-to-end test measures instead.

## Reproducing

```bash
cd backend
go test -run xxx -bench RecordReaction -benchmem ./internal/reactions/
go test -run xxx -bench RoomFanout     -benchmem ./internal/hub/
go test -run xxx -bench HubPublish     -benchmem ./internal/hub/
ulimit -n 30000
go test -run xxx -bench RealSocketFanout -benchtime=200x -benchmem ./internal/ws/
```

`BenchmarkRealSocketFanout` needs Redis (`TEST_REDIS_URL`, default
`redis://localhost:6379/15`) and about 10,000 open files for the 5,000-socket
case, so raise the limit first.

## End-to-end: node failure on the compose stack

`scripts/e2e.sh` (also `make e2e`, and the `compose-smoke` CI job). Measured on the
same laptop, from a cold checkout (`make reset`, then `make up`), 150 viewers per phase:

| Phase | Result |
|---|---|
| Baseline | 150 sessions, 0 errors; served by 01 / 02 / 03 = 49.7 / 29.7 / 20.7 % (target 50 / 30 / 20) |
| Backend 02 failed via chaos API | `/healthz` 503 within 1 s; 150 requests, **0 errors (0.00 %)** |
| Share during failure | 02 **0.0 %**; 01 71.7 %, 03 28.3 % (the 5:2 split, 71 / 29 expected) |
| Restore | fleet back to Operational within the heartbeat interval |

Assertion thresholds: error ratio under 2 %, failed node under 2 % of responses,
remaining nodes over 98 %, all nodes Healthy after restore.

The run found one real problem: nginx does not retry POST by default, so a session
created during a node failure failed with 503 (19 % errors in the first run). The
fix is to allow `non_idempotent` retries for `/api/v1/session` only. A node in chaos
fail refuses before touching any state, so replaying that request elsewhere cannot
duplicate anything.
