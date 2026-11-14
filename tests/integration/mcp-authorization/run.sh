#!/usr/bin/env bash
# Disposable end-to-end authorization fixture for WP-04C2K.
#
# It starts a controlled stdio MCP mutation server, a fake model, PostgreSQL
# 1.17, the API approval endpoint and the Events manual-run endpoint. It proves
# an authorized proposal can be approved only while the current policy, account
# and source ownership remain valid, and that no mutation or dispatch occurs.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE="$ROOT/tests/integration/mcp-authorization"
WORK="${CROWLER_E2E_WORK:-$(mktemp -d /tmp/crowler-mcp-e2e.XXXXXX)}"
PROJECT="${CROWLER_E2E_PROJECT:-wp04c2k-e2e}"
DB_PORT="${CROWLER_E2E_DB_PORT:-55485}"
API_PORT="${CROWLER_E2E_API_PORT:-18085}"
EVENTS_PORT="${CROWLER_E2E_EVENTS_PORT:-18086}"
MODEL_PORT="${CROWLER_E2E_MODEL_PORT:-18095}"
MODEL_SLOW_PORT="${CROWLER_E2E_MODEL_SLOW_PORT:-18096}"
E2E_PASSWORD="wp04c2g-test-pass"
DB_PASSWORD="$(openssl rand -hex 16)"
AUTH_SECRET="$(openssl rand -hex 32)"

cleanup() {
  # Always stop the local fixture models, even when the Compose project is kept
  # for inspection. Leaving them running would occupy the fixed ports and make
  # the next cancellation run use a stale model configuration.
  for pidfile in "$WORK/model.pid" "$WORK/slow-model.pid"; do
    if [ -f "$pidfile" ]; then
      kill "$(cat "$pidfile")" 2>/dev/null || true
    fi
  done
  if [ "${CROWLER_E2E_KEEP:-0}" = "1" ]; then
    echo "Keeping disposable workspace and Compose project for inspection: $WORK / $PROJECT"
    return
  fi
  if [ -f "$WORK/repo/docker-compose.yml" ]; then
    docker compose -p "$PROJECT" -f "$WORK/repo/docker-compose.yml" -f "$WORK/override.yml" \
      down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}
pass() {
  echo "PASS: $*"
}
q() {
  docker exec "${PROJECT}-db" psql -U postgres -d SitesIndex -Atc "$1"
}
assert_eq() {
  local got="$1" want="$2" label="$3"
  if [ "$got" != "$want" ]; then
    fail "$label: got '$got', want '$want'"
  fi
  pass "$label"
}
assert_no_mutation() {
  local label="$1"
  if [ -e "$WORK/state/counter.log" ] && [ -s "$WORK/state/counter.log" ]; then
    fail "$label: mutating MCP fixture executed: $(cat "$WORK/state/counter.log")"
  fi
  assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationDispatchAttempts')" "0" "$label: zero dispatch attempts"
  assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationProposals WHERE state IN ('dispatching','uncertain','reconciliation_required')")" "0" "$label: no dispatch states"
}

write_override() {
  local path="$1" api_config="$2"
  cat >"$path" <<EOF
services:
  crowler-db:
    container_name: ${PROJECT}-db
    ports: !override
      - "${DB_PORT}:5432"
  crowler-api:
    container_name: ${PROJECT}-api
    ports: !override
      - "${API_PORT}:8080"
    volumes:
      - ${WORK}/agents:/app/agents:ro
      - ${api_config}:/app/config.yaml:ro
  crowler-events:
    container_name: ${PROJECT}-events
    ports: !override
      - "${EVENTS_PORT}:8082"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    volumes:
      - ${WORK}/agents:/app/agents
      - ${WORK}/bin:/opt/crowler/bin:ro
      - ${WORK}/state:/tmp/crowler-mcp-state
      - ${WORK}/config.yaml:/app/config.yaml:ro
EOF
}

login() {
  local username="$1"
  curl -sS -X POST "http://127.0.0.1:${API_PORT}/v1/auth/login" \
    -H 'Content-Type: application/json' \
    --data "{\"username\":\"${username}\",\"password\":\"${E2E_PASSWORD}\"}" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])'
}

run_agent_named() {
  local token="$1" agent="$2" out="$3" code
  code="$(curl -sS -o "$out" -w '%{http_code}' -X POST "http://127.0.0.1:${EVENTS_PORT}/v1/agent/run" \
    -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
    --data "{\"agent_id\":\"${agent}\",\"input\":\"go\"}")"
  [ "$code" = "200" ] || fail "agent run HTTP ${code}: $(cat "$out")"
  python3 - "$out" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
result = data.get("result") or {}
calls = result.get("tool_calls") or []
if data.get("status") != "success":
    raise SystemExit("agent run not successful: %s" % data)
if result.get("status") != "completed":
    raise SystemExit("unexpected agent terminal: %s" % result)
if len(calls) != 1 or calls[0].get("status") != "executed":
    raise SystemExit("proposal tool call not executed: %s" % calls)
PY
}

run_agent() {
  run_agent_named "$1" "mcp-e2e-agent" "$2"
}

run_agent_cancelled() {
  local token="$1" out="$2" rc
  set +e
  curl -sS --max-time 2 -o "$out" -X POST "http://127.0.0.1:${EVENTS_PORT}/v1/agent/run" \
    -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
    --data '{"agent_id":"mcp-e2e-slow-agent","input":"go"}' >/dev/null
  rc="$?"
  set -e
  [ "$rc" != "0" ] || fail "slow agent run completed before cancellation"
}

run_agent_expect_denied() {
  local token="$1" out="$2" code
  code="$(curl -sS -o "$out" -w '%{http_code}' -X POST "http://127.0.0.1:${EVENTS_PORT}/v1/agent/run" \
    -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
    --data '{"agent_id":"mcp-e2e-agent","input":"go"}')"
  if [ "$code" != "200" ] && [ "$code" != "502" ]; then
    fail "denied manual run returned unexpected HTTP ${code}: $(cat "$out")"
  fi
  if [ "$code" = "502" ]; then
    return 0
  fi
  python3 - "$out" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
calls = (data.get("result") or {}).get("tool_calls") or []
if len(calls) != 1 or calls[0].get("status") not in ("failed", "denied"):
    raise SystemExit("denied manual run was not reported as a tool denial: %s" % data)
if not calls[0].get("reason_code"):
    raise SystemExit("denied manual run has no reason code: %s" % calls[0])
if not (data.get("result") or {}).get("run_id"):
    raise SystemExit("denied manual run has no run ID: %s" % data)
PY
}

latest_proposal() {
  local token="$1"
  curl -sS -H "Authorization: Bearer ${token}" \
    "http://127.0.0.1:${API_PORT}/v1/console/mcp/mutations/proposals?limit=100" >"$WORK/list.json"
  python3 - "$WORK/list.json" >"$WORK/proposal.env" <<'PY'
import json, sys
items = (json.load(open(sys.argv[1], encoding="utf-8")).get("items") or [])
if not items:
    raise SystemExit("no proposals")
p = items[0]
print("PID=%s" % p["proposal_id"])
print("SHA=%s" % p["proposal_sha256"])
print("EXP=%s" % p["expires_at"])
PY
  # shellcheck disable=SC1090
  . "$WORK/proposal.env"
}

approve() {
  local token="$1" pid="$2" sha="$3" exp="$4" out="$5"
  curl -sS -o "$out" -w '%{http_code}' -X POST \
    "http://127.0.0.1:${API_PORT}/v1/console/mcp/mutations/proposals/${pid}/approve" \
    -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
    --data "{\"proposal_sha256\":\"${sha}\",\"identity_version\":1,\"max_executions\":1,\"requested_expiry\":\"${exp}\",\"reason\":\"e2e authorization\"}"
}

echo "=== MCP authorization E2E workspace: $WORK ==="
mkdir -p "$WORK/repo" "$WORK/agents" "$WORK/bin" "$WORK/state"
chmod 777 "$WORK/state"
(cd "$ROOT" && tar --exclude=.git --exclude=bin --exclude=data --exclude=.env -cf - .) |
  (cd "$WORK/repo" && tar -xf -)
cp "$FIXTURE/agent.yaml" "$WORK/agents/mcp-e2e-agent.yaml"
cp "$FIXTURE/slow-agent.yaml" "$WORK/agents/mcp-e2e-slow-agent.yaml"
cp "$FIXTURE/seed.sql" "$WORK/seed.sql"
python3 "$FIXTURE/prepare-config.py" "$WORK/repo/config.default" "$WORK/config.yaml"
cat >"$WORK/repo/.env" <<EOF
DOCKER_DB_HOST=crowler-db
DOCKER_POSTGRES_DB_NAME=SitesIndex
DOCKER_POSTGRES_USER=postgres
DOCKER_POSTGRES_PASSWORD=${DB_PASSWORD}
DOCKER_POSTGRES_SSL_MODE=disable
DOCKER_CROWLER_DB_USER=crowler
DOCKER_CROWLER_DB_PASSWORD=${DB_PASSWORD}
CROWLER_DB_USER=crowler
CROWLER_DB_PASSWORD=${DB_PASSWORD}
CROWLER_DB_HOST=crowler-db
CROWLER_DB_PORT=5432
CROWLER_DB_NAME=SitesIndex
CROWLER_E2E_AUTH_SECRET=${AUTH_SECRET}
EOF

(
  cd "$WORK/repo"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$WORK/bin/mcp-fixture" ./tests/integration/mcp-authorization/fixture-mcp
  go build -o "$WORK/bin/fake-model" ./tests/integration/mcp-authorization/fake-model
)
write_override "$WORK/override.yml" "$WORK/config.yaml"
(
  cd "$WORK/repo"
  ./scripts/generate-docker-compose.sh -e=1 -v=0 --postgres=yes --prometheus=no --no_jaeger >/dev/null
)
COMPOSE=(docker compose -p "$PROJECT" -f "$WORK/repo/docker-compose.yml" -f "$WORK/override.yml")
if [ "${CROWLER_E2E_BUILD:-0}" = "1" ]; then
  "${COMPOSE[@]}" build crowler-api crowler-events
else
  docker image inspect crowler-api >/dev/null 2>&1 || fail "crowler-api image missing; set CROWLER_E2E_BUILD=1 or build it first"
  docker image inspect crowler-events >/dev/null 2>&1 || fail "crowler-events image missing; set CROWLER_E2E_BUILD=1 or build it first"
fi

docker compose -p "$PROJECT" -f "$WORK/repo/docker-compose.yml" -f "$WORK/override.yml" down -v --remove-orphans >/dev/null 2>&1 || true
"${COMPOSE[@]}" up -d --no-build crowler-db
# The bundled init script starts a temporary server, shuts it down, then
# restarts PostgreSQL. Wait for the explicit init-complete marker and the
# post-restart readiness line before connecting, otherwise psql can race the
# temporary server's shutdown.
for _ in $(seq 1 120); do
  logs="$(docker logs "${PROJECT}-db" 2>&1 || true)"
  if printf '%s\n' "$logs" | grep -q 'PostgreSQL init process complete' &&
    printf '%s\n' "$logs" | awk '/PostgreSQL init process complete/{seen=1} seen && /database system is ready to accept connections/{ok=1} END{exit !ok}'; then
    break
  fi
  sleep 2
done
for _ in $(seq 1 60); do
  docker exec "${PROJECT}-db" pg_isready -U postgres -d SitesIndex >/dev/null 2>&1 &&
    docker exec "${PROJECT}-db" psql -U postgres -d SitesIndex -Atc 'SELECT 1' >/dev/null 2>&1 && break
  sleep 2
done
for _ in $(seq 1 30); do
  [ "$(q "SELECT COUNT(*) FROM pg_tables WHERE schemaname='public' AND tablename='mcpmutationaudit'" 2>/dev/null || true)" = "1" ] && break
  sleep 2
done
docker exec -i "${PROJECT}-db" psql -U postgres -d SitesIndex <"$WORK/seed.sql" >/dev/null
pass "PostgreSQL schema 1.17 and E2E seed"

CROWLER_E2E_MODEL_PORT="$MODEL_PORT" "$WORK/bin/fake-model" >"$WORK/model.log" 2>&1 &
echo $! >"$WORK/model.pid"
CROWLER_E2E_MODEL_PORT="$MODEL_SLOW_PORT" CROWLER_E2E_MODEL_DELAY_MS=10000 "$WORK/bin/fake-model" >"$WORK/slow-model.log" 2>&1 &
echo $! >"$WORK/slow-model.pid"
for port in "$MODEL_PORT" "$MODEL_SLOW_PORT"; do
  for _ in $(seq 1 30); do
    curl -sf "http://127.0.0.1:${port}/health" >/dev/null && break
    sleep 1
  done
done
"${COMPOSE[@]}" up -d --no-build crowler-api crowler-events
for _ in $(seq 1 60); do
  curl -sf "http://127.0.0.1:${API_PORT}/v1/ready" >/dev/null && break
  sleep 2
done
sleep 4
pass "API and Events started"

OWNER_TOKEN="$(login mcp-e2e-operator)"
ADMIN_TOKEN="$(login mcp-e2e-admin)"
pass "authenticated owner and administrator"

run_agent "$OWNER_TOKEN" "$WORK/run-positive.json"
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationProposals')" "1" "positive proposal admitted"
assert_eq "$(q "SELECT state FROM MCPMutationProposals LIMIT 1")" "approval_required" "positive proposal inert"
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationApprovals')" "0" "positive proposal has no approval"
assert_no_mutation "positive admission"
pass "positive manual run stays inert"

latest_proposal "$OWNER_TOKEN"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$WORK/approve-positive.json")" "200" "authorized current-policy approval"
assert_eq "$(q "SELECT state FROM MCPMutationProposals WHERE proposal_id='${PID}'")" "approved" "approved proposal state"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}' AND state='active'")" "1" "active approval row"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE event_type='mcp_mutation_proposal_created' AND proposal_id='${PID}'")" "1" "proposal-created audit"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE event_type='mcp_mutation_approval_issued' AND proposal_id='${PID}'")" "1" "approval-issued audit"
assert_no_mutation "positive approval"
pass "positive approval is record-only"

# Request cancellation: disconnect a slow manual run while the model call is in
# flight. The trusted request context must abort the provider call and no late
# proposal may be inserted.
BEFORE_CANCEL="$(q 'SELECT COUNT(*) FROM MCPMutationProposals')"
run_agent_cancelled "$OWNER_TOKEN" "$WORK/run-cancelled.json"
# Wait beyond the slow model's delay so a missed cancellation would have had
# time to return a tool call and admit a proposal.
sleep 12
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationProposals')" "$BEFORE_CANCEL" "cancelled run inserted no proposal"
SLOW_STATS="$(curl -sS "http://127.0.0.1:${MODEL_SLOW_PORT}/stats")"
SLOW_CANCELED="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("canceled", 0))' "$SLOW_STATS")"
if [ "$SLOW_CANCELED" -lt 1 ]; then
  fail "request cancellation did not reach the model provider: ${SLOW_STATS}"
fi
pass "request cancellation reached the model provider"

# Positive control for the same slow agent: without cancellation it must create
# exactly one proposal. This proves the cancellation assertion above is not
# vacuous. Remove the control proposal and its audit row afterwards so the
# remaining negative matrix has a predictable fixture state.
BEFORE_SLOW="$(q 'SELECT COUNT(*) FROM MCPMutationProposals')"
run_agent_named "$OWNER_TOKEN" "mcp-e2e-slow-agent" "$WORK/run-slow-positive.json"
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationProposals')" "$((BEFORE_SLOW + 1))" "uncancelled slow agent admitted a proposal"
latest_proposal "$OWNER_TOKEN"
assert_eq "$(q "SELECT state FROM MCPMutationProposals WHERE proposal_id='${PID}'")" "approval_required" "slow-agent control proposal inert"
docker exec "${PROJECT}-db" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c "DELETE FROM MCPMutationAudit WHERE proposal_id='${PID}'" \
  -c "DELETE FROM MCPMutationProposals WHERE proposal_id='${PID}'"
pass "slow-agent positive control admitted one inert proposal"

# Policy mismatch: Events remains on the original policy while the API reloads
# a different policy ID. The approval must fail closed.
run_agent "$OWNER_TOKEN" "$WORK/run-mismatch.json"
cp "$WORK/config.yaml" "$WORK/config-mismatch.yaml"
python3 - "$WORK/config-mismatch.yaml" <<'PY'
import sys
path = sys.argv[1]
text = open(path, encoding="utf-8").read()
text = text.replace("policy_id: e2e-mutation-policy", "policy_id: e2e-mutation-policy-mismatch", 1)
open(path, "w", encoding="utf-8").write(text)
PY
write_override "$WORK/override-mismatch.yml" "$WORK/config-mismatch.yaml"
docker compose -p "$PROJECT" -f "$WORK/repo/docker-compose.yml" -f "$WORK/override-mismatch.yml" \
  up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
for _ in $(seq 1 60); do
  curl -sf "http://127.0.0.1:${API_PORT}/v1/ready" >/dev/null && break
  sleep 2
done
sleep 2
latest_proposal "$OWNER_TOKEN"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$WORK/approve-mismatch.json")" "409" "API/Events policy mismatch blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "policy mismatch left no approval"
pass "policy mismatch fails closed"

# Restore the API to the original policy before the remaining checks.
"${COMPOSE[@]}" up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
for _ in $(seq 1 60); do
  curl -sf "http://127.0.0.1:${API_PORT}/v1/ready" >/dev/null && break
  sleep 2
done
sleep 2

# Same policy ID and version but a different canonical operation scope. The
# canonical hash mismatch must block approval even though the identity fields
# are unchanged.
run_agent "$OWNER_TOKEN" "$WORK/run-hash-mismatch.json"
cp "$WORK/config.yaml" "$WORK/config-hash-mismatch.yaml"
python3 - "$WORK/config-hash-mismatch.yaml" <<'PY'
import sys
path = sys.argv[1]
text = open(path, encoding="utf-8").read()
old = "        min_trust_level: trusted"
new = "        min_trust_level: medium"
if old not in text:
    raise SystemExit("mutation policy trust block not found")
text = text.replace(old, new, 1)
open(path, "w", encoding="utf-8").write(text)
PY
write_override "$WORK/override-hash-mismatch.yml" "$WORK/config-hash-mismatch.yaml"
docker compose -p "$PROJECT" -f "$WORK/repo/docker-compose.yml" -f "$WORK/override-hash-mismatch.yml" \
  up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
for _ in $(seq 1 60); do
  curl -sf "http://127.0.0.1:${API_PORT}/v1/ready" >/dev/null && break
  sleep 2
done
sleep 2
latest_proposal "$OWNER_TOKEN"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$WORK/approve-hash-mismatch.json")" "409" "same-version canonical hash change blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "hash mismatch left no approval"
pass "same-version policy hash change fails closed"

# Disabled policy: the API process freezes a disabled policy while Events
# remains enabled. Approval must fail closed rather than use the proposal's
# stored snapshot.
run_agent "$OWNER_TOKEN" "$WORK/run-disabled-policy.json"
cp "$WORK/config.yaml" "$WORK/config-disabled-policy.yaml"
python3 - "$WORK/config-disabled-policy.yaml" <<'PY'
import sys
path = sys.argv[1]
text = open(path, encoding="utf-8").read()
old = "  mutation_policy:\n    enabled: true"
new = "  mutation_policy:\n    enabled: false"
if old not in text:
    raise SystemExit("mutation policy block not found")
text = text.replace(old, new, 1)
open(path, "w", encoding="utf-8").write(text)
PY
write_override "$WORK/override-disabled-policy.yml" "$WORK/config-disabled-policy.yaml"
docker compose -p "$PROJECT" -f "$WORK/repo/docker-compose.yml" -f "$WORK/override-disabled-policy.yml" \
  up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
for _ in $(seq 1 60); do
  curl -sf "http://127.0.0.1:${API_PORT}/v1/ready" >/dev/null && break
  sleep 2
done
sleep 2
latest_proposal "$OWNER_TOKEN"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$WORK/approve-disabled-policy.json")" "503" "disabled current policy blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "disabled policy left no approval"
pass "disabled current policy fails closed"

# Restore the API to the original policy before the remaining checks.
"${COMPOSE[@]}" up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
for _ in $(seq 1 60); do
  curl -sf "http://127.0.0.1:${API_PORT}/v1/ready" >/dev/null && break
  sleep 2
done
sleep 2

# Source ownership transfer after proposal creation blocks the former owner.
# Capture the proposal while the owner can still see it, then transfer.
run_agent "$OWNER_TOKEN" "$WORK/run-ownership.json"
latest_proposal "$OWNER_TOKEN"
docker exec -i "${PROJECT}-db" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
UPDATE Sources SET usr_id = 43 WHERE source_id = 42;
UPDATE SourceOwnership
SET user_id = 43, verified_at = CURRENT_TIMESTAMP,
    verified_by_user_id = 43, verified_by = 'mcp-e2e-fixture'
WHERE source_id = 42;
SQL
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$WORK/approve-ownership.json")" "403" "transferred ownership blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "ownership transfer left no approval"
pass "ownership transfer fails closed"
# Admission must also reject a proposal created after the transfer. The manual
# run reaches the agent, but the durable admission revalidation denies it and
# records a bounded denial event.
BEFORE_DENIED="$(q 'SELECT COUNT(*) FROM MCPMutationProposals')"
run_agent_expect_denied "$OWNER_TOKEN" "$WORK/run-admission-denied.json"
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationProposals')" "$BEFORE_DENIED" "transferred ownership denied admission"
DENIED_RUN="$(python3 -c 'import json,sys; print((json.load(open(sys.argv[1], encoding="utf-8")).get("result") or {}).get("run_id",""))' "$WORK/run-admission-denied.json")"
if [ -n "$DENIED_RUN" ]; then
  assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE event_type='mcp_mutation_admission_denied' AND run_id='${DENIED_RUN}'")" "1" "denied admission audit matches run"
else
  DENIED_AUDITS="$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE event_type='mcp_mutation_admission_denied'")"
  if [ "$DENIED_AUDITS" -lt 1 ]; then
    fail "denied admission produced no audit event"
  fi
fi
pass "denied admission recorded an audit event"
docker exec -i "${PROJECT}-db" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
UPDATE Sources SET usr_id = 42 WHERE source_id = 42;
UPDATE SourceOwnership
SET user_id = 42, verified_at = CURRENT_TIMESTAMP,
    verified_by_user_id = 42, verified_by = 'mcp-e2e-fixture'
WHERE source_id = 42;
SQL

# Incorrect proposal fingerprint.
run_agent "$OWNER_TOKEN" "$WORK/run-hash.json"
latest_proposal "$OWNER_TOKEN"
WRONG_SHA="$(printf '0%.0s' $(seq 1 64))"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$WRONG_SHA" "$EXP" "$WORK/approve-hash.json")" "409" "incorrect proposal hash blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "incorrect hash left no approval"
pass "incorrect proposal fingerprint fails closed"

# Expired proposal.
run_agent "$OWNER_TOKEN" "$WORK/run-expired.json"
latest_proposal "$OWNER_TOKEN"
docker exec "${PROJECT}-db" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c "UPDATE MCPMutationProposals SET created_at=now() - interval '10 minutes', expires_at=now() - interval '1 minute' WHERE proposal_id='${PID}'"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$WORK/approve-expired.json")" "410" "expired proposal blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "expired proposal left no approval"
pass "expired proposal fails closed"

# Corrupted stored policy snapshot.
run_agent "$OWNER_TOKEN" "$WORK/run-corrupt.json"
latest_proposal "$OWNER_TOKEN"
docker exec "${PROJECT}-db" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c "UPDATE MCPMutationProposals SET policy_snapshot='{}'::jsonb WHERE proposal_id='${PID}'"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$WORK/approve-corrupt.json")" "503" "corrupted policy snapshot blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "corrupt proposal left no approval"
# Remove the deliberately corrupted row so it cannot poison later list calls.
docker exec "${PROJECT}-db" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c "DELETE FROM MCPMutationProposals WHERE proposal_id='${PID}'"
pass "corrupted policy snapshot fails closed"

# Disabled proposal principal: an administrator can still reach the approval
# boundary, but the proposal principal re-resolution must fail closed.
run_agent "$OWNER_TOKEN" "$WORK/run-disabled.json"
latest_proposal "$OWNER_TOKEN"
docker exec "${PROJECT}-db" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c 'UPDATE Users SET disabled=TRUE WHERE user_id=42'
assert_eq "$(approve "$ADMIN_TOKEN" "$PID" "$SHA" "$EXP" "$WORK/approve-disabled.json")" "403" "disabled proposal principal blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "disabled principal left no approval"
pass "disabled principal fails closed"

assert_no_mutation "negative matrix"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE redacted_detail::text LIKE '%source_id%'")" "0" "audit redaction contains no source arguments"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE redacted_detail::text LIKE '%fixture-resource%'")" "0" "audit redaction contains no resource arguments"

# Database outage: the approval must not report success.
docker stop "${PROJECT}-db" >/dev/null
OUTAGE_CODE="$(approve "$ADMIN_TOKEN" "$PID" "$SHA" "$EXP" "$WORK/approve-outage.json")"
if [ "$OUTAGE_CODE" = "200" ]; then
  fail "database outage produced an approval"
fi
pass "database outage fails closed (HTTP ${OUTAGE_CODE})"
docker start "${PROJECT}-db" >/dev/null
for _ in $(seq 1 60); do
  docker exec "${PROJECT}-db" pg_isready -U postgres -d SitesIndex >/dev/null 2>&1 && break
  sleep 2
done
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "outage left no approval"
assert_no_mutation "database outage"

echo "=== MCP authorization E2E PASSED ==="
