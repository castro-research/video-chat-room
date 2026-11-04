#!/bin/bash
# Sobe um servidor Kurz de verdade (Go + Postgres do docker compose do repo, banco descartável) e roda os testes do KurzKit com
# KURZ_E2E_URL apontando para ele. Voz ligada com um SFU de mentira (KURZ_SECRET + KURZ_SFU_URL): só o REST/gateway de voz é exercitado.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../.." && pwd)"
BIN="${KURZ_E2E_BIN:-${TMPDIR:-/tmp}/kurz-e2e}"
DB="kurz_e2e_$$"
LOG="$(mktemp -t kurz-e2e-log)"
PORT="${KURZ_E2E_PORT:-$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')}"

echo "==> build do servidor"
(cd "$REPO/server" && go build -o "$BIN" ./cmd/kurz)

echo "==> Postgres (docker compose, porta 5433)"
(cd "$REPO/server" && docker compose up -d --wait db >/dev/null)
DBC="$(cd "$REPO/server" && docker compose ps -q db)"
docker exec "$DBC" psql -U kurz -d postgres -qc "CREATE DATABASE $DB" >/dev/null

SERVER_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null && wait "$SERVER_PID" 2>/dev/null || true
  docker exec "$DBC" psql -U kurz -d postgres -qc "DROP DATABASE IF EXISTS $DB WITH (FORCE)" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# KURZ_E2E_NOVOICE=1 sobe sem SFU: o teste de voz então espera 501 not_supported.
SECRET="e2e-media-secret-0123456789"; SFU="ws://localhost:9999/v1/media"
[ -n "${KURZ_E2E_NOVOICE:-}" ] && { SECRET=""; SFU=""; }
echo "==> servidor em http://localhost:$PORT (log: $LOG)"
KURZ_DATABASE_URL="postgres://kurz:kurz@localhost:5433/$DB?sslmode=disable" \
KURZ_JWT_SECRET="e2e-secret-e2e-secret-e2e-secret-0123456789" \
KURZ_ADDR=":$PORT" KURZ_RATELIMIT=off \
KURZ_SECRET="$SECRET" KURZ_SFU_URL="$SFU" \
  "$BIN" >"$LOG" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 60); do curl -sf "http://localhost:$PORT/healthz" >/dev/null && break; sleep 0.5; done
curl -sf "http://localhost:$PORT/healthz" >/dev/null || { echo "servidor não subiu; log:"; tail -20 "$LOG"; exit 1; }

echo "==> swift test (KURZ_E2E_URL=http://localhost:$PORT)"
KURZ_E2E_URL="http://localhost:$PORT" "$HERE/swift-test.sh" "$@"
