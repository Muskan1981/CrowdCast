# CrowdCast runbook

Each section matches a `runbook_url` in `deploy/prometheus/rules/alerts.yml`.
Open Prometheus at http://localhost:9090/alerts to see what is firing, and
Grafana (Node Health, Load Balancer, Failure Drill) for context.

Quick commands (from the repo root):

    make ps                              # service status and health
    make logs svc=crowdcast-api-02       # logs for one service (use the compose service name, api-02)
    make chaos-restore name=crowdcast-api-02   # bring a failed node back

---

## NodeDown

**Severity:** critical. **Fires when:** Prometheus cannot scrape an instance for 15 s.

1. Check the container: `docker compose -f deploy/docker-compose.yml ps api-02`.
2. Read its logs: `make logs svc=api-02`. Look for panics, OOM kills or failed migrations.
3. If the container is stopped, restart it: `docker compose -f deploy/docker-compose.yml start api-02`.
4. If it is running but unhealthy, check `/healthz` on the host port (api-02 is on 8082).
   A 503 with `"chaos":"failed"` means a chaos fail is active. Run `make chaos-restore name=crowdcast-api-02`.
5. Traffic moves to the remaining nodes automatically (nginx passive health checks). Expect
   the distribution panel to skew until the node is back.

## High5xxRate

**Severity:** critical. **Fires when:** more than 1% of requests return 5xx for 1 minute.

1. Find which instance: Grafana, Load Balancer dashboard, error ratio per instance.
2. Check the logs of that instance for the error. Look for `panic_recovered` or Redis/Postgres errors.
3. If Redis or Postgres is failing, see [RedisDown](#redisdown) and check `make ps`.
4. A node in chaos fail returns 503 `NODE_OFFLINE` on purpose. Clear it with `make chaos-restore`.

## HighP95Latency

**Severity:** warning. **Fires when:** p95 latency is above 250 ms for 2 minutes.

1. Check the Node Health dashboard: CPU and goroutines per instance. A pegged CPU points to load.
2. If a node has injected latency, look for it in the Failure Drill dashboard (orange markers).
   Clear it with `make chaos-latency name=crowdcast-api-03 ms=0`.
3. Check Redis latency on the Redis & Postgres dashboard. Slow Redis slows every node.
4. If the load is genuine, scale by adding an instance (see the compose file) or reduce the load
   with a smaller `make loadgen` run.

## WSSlowConsumerDrops

**Severity:** warning. **Fires when:** WebSocket clients are disconnected as slow consumers.

1. A client's send buffer filled up. It usually means a client is not reading fast enough,
   or the node is overloaded and can't flush messages.
2. Check the reaction and chat rates on the Overview dashboard. A burst can overflow the buffer.
3. Dropped clients reconnect automatically through the load balancer. If the rate stays high,
   reduce `REACTION_BATCH_MS` or the load.

## RedisDown

**Severity:** critical. **Fires when:** the Redis exporter cannot reach Redis for 15 s.

1. `make ps` to confirm the Redis container is up and healthy.
2. `make logs svc=redis`. Check for out-of-memory or disk errors.
3. Nodes keep serving in degraded mode and buffer writes locally. Once Redis is back,
   buffered items are flushed. Confirm that `/readyz` returns `"status":"ok"` again.
4. Check the Redis & Postgres dashboard afterwards for memory pressure.

## DistributionSkew

**Severity:** warning. **Fires when:** a node's traffic share is more than 15 points away from
its weight (50/30/20). It is expected during a node failure, because the other nodes take more load.

1. Look at the Load Balancer dashboard. If one node is down, the skew is the expected result.
2. If all nodes are up, check the nginx upstream weights in `deploy/nginx/nginx.conf`.
3. Remember that nginx keeps resolved upstream addresses until it reloads. After recreating
   containers, restart nginx: `docker compose -f deploy/docker-compose.yml restart nginx`.


 docker compose --env-file .env -f deploy/docker-compose.yml --profile load run --rm loadgen run --scenario ramp --users 500 --duration 2m --target http://nginx:8080 --summary /dev/null