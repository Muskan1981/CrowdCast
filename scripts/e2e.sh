#!/usr/bin/env bash
# End-to-end check on the compose stack:
#   stack up -> baseline load -> kill backend 02 via the chaos API -> load again
#   -> assert: error ratio under 2%, backend 02 share near zero, traffic re-balances
#   -> restore backend 02 -> assert the fleet is back to Operational.
#
# Usage: scripts/e2e.sh            (needs a .env; run from anywhere)
# Env:   E2E_USERS (default 150), E2E_PHASE_SECONDS (default 30), E2E_MAX_ERROR_PCT (default 2)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
set -a; . ./.env; set +a

COMPOSE=(docker compose --env-file .env -f deploy/docker-compose.yml)
USERS="${E2E_USERS:-150}"
SECS="${E2E_PHASE_SECONDS:-30}"
OUT="$ROOT/loadgen-out/e2e"
APP="http://localhost:${APP_PORT:-8080}"
mkdir -p "$OUT"
rm -f "$OUT"/*.json

log() { printf '\n== %s\n' "$*"; }

run_load() { # run_load <name>
  "${COMPOSE[@]}" --profile load run --rm --no-deps loadgen run \
    --scenario steady --users "$USERS" --duration "${SECS}s" \
    --target http://nginx:8080 --event "${LOADGEN_EVENT:-cc-0826}" \
    --summary "/out/e2e/$1.json" --no-dashboard
}

log "1. start the stack (first-run waits for every healthy check)"
./scripts/first-run.sh

log "2. baseline load: all three nodes serving"
run_load baseline

log "3. kill backend 02 through the chaos API (admin request lands on api-01)"
curl -fsS -X POST "http://localhost:8081/api/v1/admin/nodes/crowdcast-api-02/fail" \
  -H "X-Admin-Key: ${ADMIN_API_KEY}" | tee "$OUT/fail.json"
echo
# Give the node's heartbeat and nginx's passive check a moment to notice.
for _ in $(seq 1 20); do
  state=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:8082/healthz" || true)
  [ "$state" = "503" ] && break
  sleep 0.5
done
echo "backend 02 /healthz: HTTP $state"

log "4. load while backend 02 is down"
run_load failure

log "5. restore backend 02"
curl -fsS -X POST "http://localhost:8081/api/v1/admin/nodes/crowdcast-api-02/restore" \
  -H "X-Admin-Key: ${ADMIN_API_KEY}" | tee "$OUT/restore.json"
echo

log "6. assertions"
python3 "$ROOT/scripts/assert_e2e.py" "$OUT" \
  --max-error-pct "${E2E_MAX_ERROR_PCT:-2}" \
  --killed crowdcast-api-02 \
  --app "$APP" \
  --admin-key "$ADMIN_API_KEY"
