#!/usr/bin/env bash
# Smoke test for the granary phosphine fumigation closure service.
# Builds the server, starts it against a temporary database, drives a real
# HTTP flow (catalogue -> task -> lock -> query) and cleans everything up.
# It performs no external network access and does not merely run `go test`.
set -euo pipefail

WORKDIR="$(mktemp -d)"
BIN="$WORKDIR/granary-server"
DB="$WORKDIR/granary.db"
PORT="${PORT:-18080}"
ADDR="127.0.0.1:$PORT"
BASE="http://$ADDR"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

echo "==> building server"
go build -o "$BIN" ./cmd/server

echo "==> starting server on $ADDR"
DB_PATH="$DB" ADDR=":$PORT" "$BIN" &
SERVER_PID=$!

# Wait for the health endpoint to become ready.
health=""
for _ in $(seq 1 50); do
  if health="$(curl -fsS "$BASE/healthz" 2>/dev/null)"; then
    break
  fi
  sleep 0.1
done
if [[ -z "$health" ]]; then
  echo "server failed to become ready" >&2
  exit 1
fi
[[ "$health" == *'"status":"ok"'* ]] || { echo "unexpected health: $health" >&2; exit 1; }
echo "==> health ok"

# 1. Register catalogue fixtures.
resp="$(curl -fsS -X POST "$BASE/api/v1/warehouses" -H 'Content-Type: application/json' -d '{
  "code":"WH-01","rated_capacity_dm3":2000,"allowed_grains":["WHEAT"],"structure_version":1,
  "zones":[{"code":"Z1","warehouse":"WH-01","capacity_dm3":1000},{"code":"Z2","warehouse":"WH-01","capacity_dm3":1000}],
  "edges":[{"from":"Z1","to":"Z2"}],
  "devices":[
    {"code":"CAGE-1","warehouse":"WH-01","kind":"DOSING_CAGE"},
    {"code":"FAN-1","warehouse":"WH-01","kind":"FAN_CIRCUIT","mutually_exclusive_with":["FAN-2"]},
    {"code":"FAN-2","warehouse":"WH-01","kind":"FAN_CIRCUIT","mutually_exclusive_with":["FAN-1"]},
    {"code":"SL-1","warehouse":"WH-01","kind":"SAMPLING_LINE"}
  ],
  "sampling_points":[{"code":"SP-1","warehouse":"WH-01","zone":"Z1"},{"code":"SP-2","warehouse":"WH-01","zone":"Z2"}]
}')"
[[ "$resp" == *'"code":"WH-01"'* ]] || { echo "warehouse register failed: $resp" >&2; exit 1; }

resp="$(curl -fsS -X POST "$BASE/api/v1/rules" -H 'Content-Type: application/json' -d '{
  "version":1,"grain_types":["WHEAT"],"min_height_dm":1,"max_height_dm":10,"capacity_factor":1000,
  "target_dose_ct":1000,"sampling_window_slots":3,"slot_duration_sec":60,"leak_threshold":50,
  "reentry_threshold":5,"safe_continuous_slots":2,"retry_max_attempts":3,"retry_base_delay_slots":1
}')"
[[ "$resp" == *'"version":1'* ]] || { echo "rule register failed: $resp" >&2; exit 1; }

resp="$(curl -fsS -X POST "$BASE/api/v1/batches" -H 'Content-Type: application/json' -d '{
  "code":"B-1","initial_mg":100000,"available_mg":100000,"reserved_mg":0,"applied_mg":0,"returned_mg":0,"adjusted_mg":0
}')"
[[ "$resp" == *'"code":"B-1"'* ]] || { echo "batch register failed: $resp" >&2; exit 1; }

# 2. Create a task.
resp="$(curl -fsS -X POST "$BASE/api/v1/tasks" -H 'Content-Type: application/json' -d '{"warehouse_code":"WH-01"}')"
task_number="$(printf '%s' "$resp" | sed -n 's/.*"number":"\([^"]*\)".*/\1/p')"
[[ -n "$task_number" ]] || { echo "task create failed: $resp" >&2; exit 1; }
echo "==> created task $task_number"

# 3. Fetch the canonical summary and lock the task.
summary="$(curl -fsS "$BASE/api/v1/warehouses/WH-01?summary=1")"
digest="$(printf '%s' "$summary" | sed -n 's/.*"digest":"\([^"]*\)".*/\1/p')"
[[ -n "$digest" ]] || { echo "summary digest missing: $summary" >&2; exit 1; }

resp="$(curl -fsS -X POST "$BASE/api/v1/tasks/$task_number/lock" -H 'Content-Type: application/json' -d "{
  \"operation_id\":\"smoke-lock-1\",\"expected_version\":1,\"grain_type\":\"WHEAT\",\"stack_height_dm\":5,
  \"summary\":{\"warehouse_code\":\"WH-01\",\"structure_version\":1,\"rule_version\":1,\"digest\":\"$digest\"}
}")"
[[ "$resp" == *'"status":"AIRTIGHT_CHECKING"'* ]] || { echo "lock failed: $resp" >&2; exit 1; }

# 4. Query the task and confirm the immutable snapshot is present.
resp="$(curl -fsS "$BASE/api/v1/tasks/$task_number")"
[[ "$resp" == *'"status":"AIRTIGHT_CHECKING"'* ]] || { echo "task status wrong: $resp" >&2; exit 1; }
[[ "$resp" == *'"snapshot"'* ]] || { echo "snapshot missing: $resp" >&2; exit 1; }

echo "==> smoke flow completed successfully for $task_number"
