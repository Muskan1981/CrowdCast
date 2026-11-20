#!/usr/bin/env bash
# First-run helper: checks prerequisites, starts the stack, waits until every
# service is healthy, and prints where to find each UI.
#
# Usage: scripts/first-run.sh            (after `cp .env.example .env`)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE=(docker compose --env-file "$ROOT/.env" -f "$ROOT/deploy/docker-compose.yml")
TIMEOUT="${FIRST_RUN_TIMEOUT:-300}"

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }

command -v docker >/dev/null || { red "docker is not installed"; exit 1; }
docker info >/dev/null 2>&1 || { red "docker daemon is not running; start Docker Desktop and retry"; exit 1; }

if [[ ! -f "$ROOT/.env" ]]; then
  red "missing .env. Run: cp .env.example .env   (then set the CHANGE ME values)"
  exit 1
fi

if grep -q "change-me" "$ROOT/.env"; then
  echo "warning: .env still contains 'change-me' placeholders. Change them before exposing this stack."
fi

echo "starting the stack (first build can take a few minutes)..."
"${COMPOSE[@]}" up -d --build

echo "waiting for services to become healthy (timeout ${TIMEOUT}s)..."
start=$(date +%s)
while true; do
  unhealthy=$("${COMPOSE[@]}" ps --format '{{.Service}} {{.Health}} {{.State}}' \
    | awk '$3=="running" && $2!="healthy" && $2!="" {print $1}' || true)
  # Services with no healthcheck (exporters, one-shot jobs) are excluded above.
  # One-shot jobs must have exited successfully.
  failed=$("${COMPOSE[@]}" ps -a --format '{{.Service}} {{.State}} {{.ExitCode}}' \
    | awk '$1=="migrate" || $1=="seed" {if ($2!="exited" || $3!="0") print $1}' || true)
  if [[ -z "$unhealthy" && -z "$failed" ]]; then
    break
  fi
  if [[ -n "$failed" ]]; then
    red "one-shot job failed: $failed"
    "${COMPOSE[@]}" logs --tail 50 $failed
    exit 1
  fi
  if (( $(date +%s) - start > TIMEOUT )); then
    red "timed out waiting for: $unhealthy"
    "${COMPOSE[@]}" ps
    exit 1
  fi
  sleep 3
done

green "all services are healthy after $(( $(date +%s) - start ))s"
echo
echo "  App         http://localhost:${APP_PORT:-8080}"
echo "  Grafana     http://localhost:${GRAFANA_PORT:-3000}   (user from .env, anonymous viewing on)"
echo "  Prometheus  http://localhost:${PROMETHEUS_PORT:-9090}"
echo
echo "Try: make loadgen   |   make chaos-node name=crowdcast-api-02   |   make ps"
