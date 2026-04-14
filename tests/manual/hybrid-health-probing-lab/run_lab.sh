#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
OUTPUT_DIR="${ROOT_DIR}/tmp/hybrid-health-probing-lab/${TIMESTAMP}"
APP_DIR="${OUTPUT_DIR}/app"
MOCK_PORT="${MOCK_PORT:-19101}"
BIFROST_PORT="${BIFROST_PORT:-18080}"
MOCK_BASE_URL="http://127.0.0.1:${MOCK_PORT}"
BIFROST_BASE_URL="http://127.0.0.1:${BIFROST_PORT}"
MOCK_LOG="${OUTPUT_DIR}/mock-server.log"
BIFROST_LOG="${OUTPUT_DIR}/bifrost.log"

mkdir -p "${APP_DIR}"

cat > "${APP_DIR}/config.json" <<EOF
{
  "\$schema": "https://www.getbifrost.ai/schema",
  "client": {
    "enable_logging": true
  },
  "plugins": [
    {
      "enabled": true,
      "name": "governance",
      "config": {
        "active_health_probe_enabled": true,
        "active_health_probe_interval_seconds": 1,
        "active_health_probe_passive_freshness_seconds": 2,
        "active_health_probe_timeout_seconds": 1,
        "active_health_probe_max_concurrency": 2
      }
    }
  ],
  "config_store": {
    "enabled": true,
    "type": "sqlite",
    "config": {
      "path": "${APP_DIR}/config.db"
    }
  },
  "logs_store": {
    "enabled": true,
    "type": "sqlite",
    "config": {
      "path": "${APP_DIR}/logs.db"
    }
  }
}
EOF

wait_for_url() {
  local url="$1"
  local name="$2"
  local attempts=0
  until curl -fsS "$url" >/dev/null 2>&1; do
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 90 ]; then
      echo "Timed out waiting for ${name} at ${url}" >&2
      return 1
    fi
    sleep 1
  done
}

cleanup() {
  local status=$?
  if [ -n "${BIFROST_PID:-}" ] && kill -0 "${BIFROST_PID}" >/dev/null 2>&1; then
    kill "${BIFROST_PID}" >/dev/null 2>&1 || true
    wait "${BIFROST_PID}" >/dev/null 2>&1 || true
  fi
  if [ -n "${MOCK_PID:-}" ] && kill -0 "${MOCK_PID}" >/dev/null 2>&1; then
    kill "${MOCK_PID}" >/dev/null 2>&1 || true
    wait "${MOCK_PID}" >/dev/null 2>&1 || true
  fi
  exit "${status}"
}
trap cleanup EXIT

cd "${ROOT_DIR}"

go run ./tests/manual/grouped-routing-lab/mock_openai_server.go \
  -port "${MOCK_PORT}" \
  -record-path "${OUTPUT_DIR}/mock-events.jsonl" \
  >"${MOCK_LOG}" 2>&1 &
MOCK_PID=$!

wait_for_url "${MOCK_BASE_URL}/__admin/health" "mock server"

go run ./transports/bifrost-http \
  -app-dir "${APP_DIR}" \
  -host 127.0.0.1 \
  -port "${BIFROST_PORT}" \
  -log-style json \
  -log-level info \
  >"${BIFROST_LOG}" 2>&1 &
BIFROST_PID=$!

wait_for_url "${BIFROST_BASE_URL}/health" "bifrost"

go run ./tests/manual/hybrid-health-probing-lab/run_lab.go \
  -bifrost-url "${BIFROST_BASE_URL}" \
  -mock-admin-url "${MOCK_BASE_URL}" \
  -output-dir "${OUTPUT_DIR}"

echo "Artifacts: ${OUTPUT_DIR}"
