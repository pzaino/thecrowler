#!/usr/bin/env bash
# Ownership-safe, hermetically isolated MCP authorization E2E fixture.
#
# Every Docker resource is created under a cryptographically random run ID,
# carries an ownership label, and is removed only by exact ID/name after that
# label is verified. Postgres and fake models are internal-only; API and Events
# publish dynamic loopback ports.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE="$ROOT/tests/integration/mcp-authorization"
SUPPORT="$FIXTURE/e2e_support.py"
HELPER_IMAGE="curlimages/curl:8.10.1"
E2E_PASSWORD="wp04c2g-test-pass"
DB_PASSWORD="$(openssl rand -hex 16)"
AUTH_SECRET="$(openssl rand -hex 32)"

if [ -n "${CROWLER_E2E_API_PORT:-}" ] || [ -n "${CROWLER_E2E_EVENTS_PORT:-}" ] || \
   [ -n "${CROWLER_E2E_DB_PORT:-}" ] || [ -n "${CROWLER_E2E_MODEL_PORT:-}" ]; then
  echo "BLOCKED: fixed E2E host ports are not supported; the fixture uses dynamic loopback ports" >&2
  exit 2
fi

RUN_ID="$(python3 "$SUPPORT" new-run-id)"
PROJECT="${CROWLER_E2E_PROJECT:-crowler-e2e-${RUN_ID:0:12}}"
KEEP="${CROWLER_E2E_KEEP:-0}"
REUSE_IMAGES="${CROWLER_E2E_REUSE_IMAGES:-0}"
SOURCE_SHA="$(git -C "$ROOT" rev-parse HEAD)"
SOURCE_TREE="$(python3 "$SUPPORT" source-digest --root "$ROOT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["sha256"])')"
ARTIFACT_ROOT="${CROWLER_E2E_ARTIFACT_ROOT:-${TMPDIR:-/tmp}/crowler-e2e-artifacts}"
ARTIFACTS="$ARTIFACT_ROOT/$RUN_ID"
RESULTS="$ARTIFACTS/results.ndjson"
case "$(realpath -m "$ARTIFACT_ROOT")" in
  "$(realpath -m "$ROOT")"|"$(realpath -m "$ROOT")"/*|"/"|"$(realpath -m "${HOME:-/nonexistent}")")
    echo "BLOCKED: CROWLER_E2E_ARTIFACT_ROOT must not be /, the home directory or inside the repository" >&2
    exit 2
    ;;
esac
WORK=""
CLEANED=0
DB_CID=""
API_CID=""
EVENTS_CID=""
MODEL_CID=""
SLOW_MODEL_CID=""
API_URL=""
EVENTS_URL=""
COMPOSE=()
HTTP_DIR=""

mkdir -p "$ARTIFACTS/logs" "$ARTIFACTS/meta"
chmod 700 "$ARTIFACTS" "$ARTIFACTS/logs" "$ARTIFACTS/meta"
: >"$RESULTS"

json_value() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$1" "$2"; }

record() {
  local name="$1" status="$2" detail="${3:-}"
  python3 "$SUPPORT" record --results "$RESULTS" --name "$name" --status "$status" --detail "$detail" >/dev/null
}

pass() {
  echo "PASS: $*"
  record "$*" PASS
}

fail() {
  echo "FAIL: $*" >&2
  record "$*" FAIL
  exit 1
}

blocked() {
  echo "BLOCKED: $*" >&2
  record "$*" BLOCKED
  exit 2
}

http_host_mode() {
  case "${DOCKER_HOST:-}" in
    "") return 0 ;;
    unix://*) return 0 ;;
    *) return 1 ;;
  esac
}

http_helper() {
  docker run --rm --network host -i "$HELPER_IMAGE" "$@"
}

http_status_get() {
  local url="$1" out="$2" code="000" rc=0
  set +e
  if http_host_mode; then
    code="$(curl -sS -o "$out" -w '%{http_code}' "$url")"
  else
    code="$(http_helper -sS -o /dev/null -w '%{http_code}' "$url")"
  fi
  rc="$?"
  set -e
  if [ "$rc" != "0" ]; then
    code="000"
    : >"$out"
  fi
  printf '%s' "$code"
}

http_get_to_file() {
  local url="$1" out="$2" code
  code="$(http_status_get "$url" "$out")"
  [ "$code" = "200" ] || fail "GET ${url} returned HTTP ${code}"
}

http_get_auth() {
  local url="$1" token="$2" out="$3" raw code rc=0
  set +e
  if http_host_mode; then
    code="$(curl -sS -o "$out" -w '%{http_code}' -H "Authorization: Bearer ${token}" "$url")"
  else
    raw="$(http_helper -sS -w $'\n%{http_code}' -H "Authorization: Bearer ${token}" "$url")"
    rc="$?"
    code="${raw##*$'\n'}"
    printf '%s' "${raw%$'\n'*}" >"$out"
  fi
  set -e
  if [ "$rc" != "0" ] || [ "$code" != "200" ]; then
    fail "authenticated GET ${url} returned HTTP ${code:-000}"
  fi
}

http_post_json() {
  local url="$1" token="$2" body="$3" out="$4" raw code rc=0
  set +e
  if http_host_mode; then
    code="$(curl -sS -o "$out" -w '%{http_code}' -X POST "$url" \
      -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
      --data-binary "@${body}")"
  else
    raw="$(http_helper -sS -w $'\n%{http_code}' -X POST "$url" \
      -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
      --data-binary @- <"$body")"
    rc="$?"
    code="${raw##*$'\n'}"
    printf '%s' "${raw%$'\n'*}" >"$out"
  fi
  set -e
  if [ "$rc" != "0" ]; then
    code="000"
    : >"$out"
  fi
  printf '%s' "$code"
}

http_post_json_anon() {
  local url="$1" body="$2" out="$3" raw code rc=0
  set +e
  if http_host_mode; then
    code="$(curl -sS -o "$out" -w '%{http_code}' -X POST "$url" \
      -H 'Content-Type: application/json' --data-binary "@${body}")"
  else
    raw="$(http_helper -sS -w $'\n%{http_code}' -X POST "$url" \
      -H 'Content-Type: application/json' --data-binary @- <"$body")"
    rc="$?"
    code="${raw##*$'\n'}"
    printf '%s' "${raw%$'\n'*}" >"$out"
  fi
  set -e
  if [ "$rc" != "0" ]; then
    code="000"
    : >"$out"
  fi
  printf '%s' "$code"
}

http_get_text() {
  local url="$1" raw rc=0
  set +e
  if http_host_mode; then
    raw="$(curl -sS "$url")"
  else
    raw="$(http_helper -sS "$url")"
  fi
  rc="$?"
  set -e
  [ "$rc" = "0" ] || return "$rc"
  printf '%s' "$raw"
}

refresh_api_url() {
  local addr
  addr="$("${COMPOSE[@]}" port crowler-api 8080 | head -1)"
  case "$addr" in
    127.0.0.1:*) ;;
    *) fail "API port is not dynamically loopback-bound: ${addr:-<none>}" ;;
  esac
  API_URL="http://${addr}"
}

wait_api_ready() {
  local code=""
  refresh_api_url
  for _ in $(seq 1 60); do
    code="$(http_status_get "$API_URL/v1/ready" "$HTTP_DIR/ready.json" 2>/dev/null || true)"
    [ "$code" = "200" ] && return 0
    sleep 2
  done
  fail "API readiness failed at ${API_URL}"
}

collect_logs() {
  local id role
  for pair in "${DB_CID}:db" "${API_CID}:api" "${EVENTS_CID}:events" "${MODEL_CID}:model" "${SLOW_MODEL_CID}:slow-model"; do
    id="${pair%%:*}"
    role="${pair##*:}"
    [ -n "$id" ] || continue
    docker logs "$id" >"$ARTIFACTS/logs/${role}.raw.log" 2>&1 || true
    python3 "$SUPPORT" sanitize --input "$ARTIFACTS/logs/${role}.raw.log" \
      --output "$ARTIFACTS/logs/${role}.log" --secret-file "$WORK/secrets.env" || true
    rm -f "$ARTIFACTS/logs/${role}.raw.log"
  done
}

on_exit() {
  local rc=$? cleanup_rc=0 final_rc=0
  trap - EXIT INT TERM
  set +e
  [ -n "$WORK" ] && [ -n "$HTTP_DIR" ] && collect_logs
  if [ -n "$WORK" ] && [ -f "$RESULTS" ] && [ -f "$WORK/secrets.env" ]; then
    tmp="$RESULTS.sanitized"
    if python3 "$SUPPORT" sanitize --input "$RESULTS" --output "$tmp" --secret-file "$WORK/secrets.env"; then
      mv "$tmp" "$RESULTS"
    else
      rm -f "$tmp"
      cleanup_rc=1
    fi
  fi
  if [ "$CLEANED" = "0" ] && [ -n "$WORK" ]; then
    if [ "$KEEP" = "1" ]; then
      python3 "$SUPPORT" cleanup --run-id "$RUN_ID" --project "$PROJECT" --work "$WORK" --artifacts "$ARTIFACTS" --keep
    else
      python3 "$SUPPORT" cleanup --run-id "$RUN_ID" --project "$PROJECT" --work "$WORK" --artifacts "$ARTIFACTS"
    fi
    cleanup_rc=$?
    CLEANED=1
  fi
  if [ -n "$ARTIFACTS" ] && [ -f "$RESULTS" ]; then
    python3 "$SUPPORT" finalize --results "$RESULTS" --artifacts "$ARTIFACTS" \
      --run-id "$RUN_ID" --project "$PROJECT" --source-sha "$SOURCE_SHA" --source-tree "$SOURCE_TREE" || final_rc=$?
  fi
  if [ "$rc" = "0" ] && { [ "$cleanup_rc" != "0" ] || [ "$final_rc" != "0" ]; }; then
    rc=1
  fi
  exit "$rc"
}
trap on_exit EXIT INT TERM

echo "=== MCP authorization E2E run ${RUN_ID} project ${PROJECT} ==="
echo "Source ${SOURCE_SHA} tree ${SOURCE_TREE}"

API_IMAGE="${PROJECT}-api:${RUN_ID}"
EVENTS_IMAGE="${PROJECT}-events:${RUN_ID}"
MODEL_IMAGE="${PROJECT}-model:${RUN_ID}"
PREPARE_IMAGES=()
if [ "$REUSE_IMAGES" = "1" ]; then
  API_IMAGE="${CROWLER_E2E_API_IMAGE:-}"
  EVENTS_IMAGE="${CROWLER_E2E_EVENTS_IMAGE:-}"
  MODEL_IMAGE="${CROWLER_E2E_MODEL_IMAGE:-}"
  [ -n "$API_IMAGE" ] && [ -n "$EVENTS_IMAGE" ] && [ -n "$MODEL_IMAGE" ] ||
    blocked "CROWLER_E2E_REUSE_IMAGES=1 requires CROWLER_E2E_API_IMAGE, CROWLER_E2E_EVENTS_IMAGE and CROWLER_E2E_MODEL_IMAGE"
else
  PREPARE_IMAGES=(--api-image "$API_IMAGE" --events-image "$EVENTS_IMAGE" --model-image "$MODEL_IMAGE")
fi

PREPARE_JSON="$(python3 "$SUPPORT" prepare --run-id "$RUN_ID" --project "$PROJECT" \
  --repo-root "$ROOT" --work-root "${CROWLER_E2E_WORK:-}" --artifacts "$ARTIFACTS" "${PREPARE_IMAGES[@]}")"
WORK="$(printf '%s' "$PREPARE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["work"])')"
HTTP_DIR="$WORK/http"
mkdir -p "$WORK/repo" "$WORK/agents" "$WORK/bin" "$WORK/state" "$WORK/http" "$WORK/model"
chmod 777 "$WORK/state"
printf 'DB_PASSWORD=%s\nAUTH_SECRET=%s\n' "$DB_PASSWORD" "$AUTH_SECRET" >"$WORK/secrets.env"
chmod 600 "$WORK/secrets.env"

if [ "$REUSE_IMAGES" = "1" ]; then
  for image in "$API_IMAGE" "$EVENTS_IMAGE" "$MODEL_IMAGE"; do
    python3 "$SUPPORT" image-info --image "$image" --run-id "$RUN_ID" \
      --source-sha "$SOURCE_SHA" --source-tree "$SOURCE_TREE" --require-source >/dev/null ||
      blocked "reused image provenance mismatch: $image"
  done
fi

tar --exclude=.git --exclude=bin --exclude=data --exclude=.env --exclude=artifacts \
  -cf "$WORK/source.tar" -C "$ROOT" .
tar -xf "$WORK/source.tar" -C "$WORK/repo"
cp "$WORK/repo/config.default" "$WORK/repo/config.yaml"
cp "$FIXTURE/agent.yaml" "$WORK/agents/mcp-e2e-agent.yaml"
cp "$FIXTURE/slow-agent.yaml" "$WORK/agents/mcp-e2e-slow-agent.yaml"
cp "$FIXTURE/seed.sql" "$WORK/seed.sql"
python3 "$FIXTURE/prepare-config.py" "$WORK/repo/config.default" "$WORK/config.yaml"
chmod 644 "$WORK/config.yaml"
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
chmod 600 "$WORK/repo/.env"

(
  cd "$WORK/repo"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$WORK/bin/mcp-fixture" ./tests/integration/mcp-authorization/fixture-mcp
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$WORK/bin/fake-model" ./tests/integration/mcp-authorization/fake-model
)
cat >"$WORK/model/Dockerfile" <<'EOF'
FROM alpine:3.20
RUN adduser -D -u 65534 e2emodel || true
COPY fake-model /usr/local/bin/fake-model
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/fake-model"]
EOF
cp "$WORK/bin/fake-model" "$WORK/model/fake-model"
chmod 755 "$WORK/model/fake-model"

(cd "$WORK/repo" && ./scripts/generate-docker-compose.sh -e=1 -v=0 --postgres=yes --prometheus=no --no_jaeger >/dev/null)
python3 "$SUPPORT" write-overrides --work "$WORK" --output "$WORK/override.yml" \
  --project "$PROJECT" --run-id "$RUN_ID" --source-sha "$SOURCE_SHA" --source-tree "$SOURCE_TREE" \
  --api-image "$API_IMAGE" --events-image "$EVENTS_IMAGE" --model-image "$MODEL_IMAGE" \
  --config "$WORK/config.yaml" >/dev/null
python3 "$SUPPORT" validate-compose --work "$WORK" --project "$PROJECT" --run-id "$RUN_ID" \
  --override "$WORK/override.yml" --api-image "$API_IMAGE" --events-image "$EVENTS_IMAGE" \
  --model-image "$MODEL_IMAGE" >/dev/null
pass "compose bindings and labels validated"
if [ "${CROWLER_E2E_PREFLIGHT_ONLY:-0}" = "1" ]; then
  pass "preflight-only run completed without creating Docker resources"
  exit 0
fi

if [ "$REUSE_IMAGES" != "1" ]; then
  docker build --label "org.crowler.e2e.run=$RUN_ID" --label "org.crowler.e2e.project=$PROJECT" \
    --label "org.crowler.e2e.source.sha=$SOURCE_SHA" --label "org.crowler.e2e.source.tree=$SOURCE_TREE" \
    --label "org.crowler.e2e.role=api" -t "$API_IMAGE" -f Dockerfile.searchapi "$WORK/repo" \
    >"$ARTIFACTS/logs/build-api.log" 2>&1 || fail "API image build failed; see $ARTIFACTS/logs/build-api.log"
  docker build --label "org.crowler.e2e.run=$RUN_ID" --label "org.crowler.e2e.project=$PROJECT" \
    --label "org.crowler.e2e.source.sha=$SOURCE_SHA" --label "org.crowler.e2e.source.tree=$SOURCE_TREE" \
    --label "org.crowler.e2e.role=events" -t "$EVENTS_IMAGE" -f Dockerfile.events "$WORK/repo" \
    >"$ARTIFACTS/logs/build-events.log" 2>&1 || fail "Events image build failed; see $ARTIFACTS/logs/build-events.log"
  docker build --label "org.crowler.e2e.run=$RUN_ID" --label "org.crowler.e2e.project=$PROJECT" \
    --label "org.crowler.e2e.source.sha=$SOURCE_SHA" --label "org.crowler.e2e.source.tree=$SOURCE_TREE" \
    --label "org.crowler.e2e.role=model" -t "$MODEL_IMAGE" "$WORK/model" \
    >"$ARTIFACTS/logs/build-model.log" 2>&1 || fail "model image build failed; see $ARTIFACTS/logs/build-model.log"
fi

python3 "$SUPPORT" image-info --image "$API_IMAGE" --run-id "$RUN_ID" --source-sha "$SOURCE_SHA" \
  --source-tree "$SOURCE_TREE" --require-source >"$ARTIFACTS/meta/api-image.json" ||
  blocked "API image provenance mismatch"
python3 "$SUPPORT" image-info --image "$EVENTS_IMAGE" --run-id "$RUN_ID" --source-sha "$SOURCE_SHA" \
  --source-tree "$SOURCE_TREE" --require-source >"$ARTIFACTS/meta/events-image.json" ||
  blocked "Events image provenance mismatch"
python3 "$SUPPORT" image-info --image "$MODEL_IMAGE" --run-id "$RUN_ID" --source-sha "$SOURCE_SHA" \
  --source-tree "$SOURCE_TREE" --require-source >"$ARTIFACTS/meta/model-image.json" ||
  blocked "model image provenance mismatch"
python3 - "$ARTIFACTS/meta/api-image.json" "$ARTIFACTS/meta/events-image.json" "$ARTIFACTS/meta/model-image.json" >"$ARTIFACTS/images.json" <<'PY'
import json, sys
rows = []
for role, path in zip(("api", "events", "model"), sys.argv[1:]):
    info = json.load(open(path, encoding="utf-8"))
    info["role"] = role
    rows.append(info)
print(json.dumps(rows, indent=2))
PY
pass "source-labelled images verified"

COMPOSE=(docker compose -p "$PROJECT" --env-file "$WORK/repo/.env" -f "$WORK/repo/docker-compose.yml" -f "$WORK/override.yml")
"${COMPOSE[@]}" up -d --no-build crowler-db crowler-e2e-model crowler-e2e-slow-model
DB_CID="$("${COMPOSE[@]}" ps -q crowler-db)"
MODEL_CID="$("${COMPOSE[@]}" ps -q crowler-e2e-model)"
SLOW_MODEL_CID="$("${COMPOSE[@]}" ps -q crowler-e2e-slow-model)"
[ -n "$DB_CID" ] && [ -n "$MODEL_CID" ] && [ -n "$SLOW_MODEL_CID" ] || fail "internal services did not start"

for _ in $(seq 1 120); do
  logs="$(docker logs "$DB_CID" 2>&1 || true)"
  if printf '%s\n' "$logs" | grep -q 'PostgreSQL init process complete' &&
    printf '%s\n' "$logs" | awk '/PostgreSQL init process complete/{seen=1} seen && /database system is ready to accept connections/{ok=1} END{exit !ok}'; then
    break
  fi
  sleep 2
done
for _ in $(seq 1 60); do
  docker exec "$DB_CID" pg_isready -U postgres -d SitesIndex >/dev/null 2>&1 &&
    docker exec "$DB_CID" psql -U postgres -d SitesIndex -Atc 'SELECT 1' >/dev/null 2>&1 && break
  sleep 2
done
for _ in $(seq 1 30); do
  [ "$(docker exec "$DB_CID" psql -U postgres -d SitesIndex -Atc "SELECT COUNT(*) FROM pg_tables WHERE schemaname='public' AND tablename='mcpmutationaudit'" 2>/dev/null || true)" = "1" ] && break
  sleep 2
done
docker exec -i "$DB_CID" psql -U postgres -d SitesIndex <"$WORK/seed.sql" >/dev/null
for cid in "$MODEL_CID" "$SLOW_MODEL_CID"; do
  for _ in $(seq 1 30); do
    [ "$(docker inspect -f '{{.State.Health.Status}}' "$cid" 2>/dev/null || true)" = "healthy" ] && break
    sleep 1
  done
done
pass "PostgreSQL schema 1.17, seed and internal models ready"
if [ "${CROWLER_E2E_STOP_AFTER:-}" = "models" ]; then
  echo "=== MCP authorization E2E stopped after internal models ==="
  exit 0
fi

"${COMPOSE[@]}" up -d --no-build crowler-api crowler-events
API_CID="$("${COMPOSE[@]}" ps -q crowler-api)"
EVENTS_CID="$("${COMPOSE[@]}" ps -q crowler-events)"
[ -n "$API_CID" ] && [ -n "$EVENTS_CID" ] || fail "API/Events did not start"
python3 "$SUPPORT" verify-runtime --work "$WORK" --project "$PROJECT" --run-id "$RUN_ID" \
  --source-sha "$SOURCE_SHA" --source-tree "$SOURCE_TREE" --images-json "$ARTIFACTS/images.json" \
  --override "$WORK/override.yml" >"$ARTIFACTS/meta/runtime.json" ||
  fail "runtime provenance or loopback port verification failed"
EVENTS_ADDR="$("${COMPOSE[@]}" port crowler-events 8082 | head -1)"
case "$EVENTS_ADDR" in 127.0.0.1:*) ;; *) fail "Events port is not loopback: $EVENTS_ADDR" ;; esac
EVENTS_URL="http://${EVENTS_ADDR}"
wait_api_ready
pass "API and Events started with dynamic loopback ports"
if [ "${CROWLER_E2E_STOP_AFTER:-}" = "runtime" ]; then
  echo "=== MCP authorization E2E stopped after runtime startup ==="
  exit 0
fi

q() {
  docker exec "$DB_CID" psql -U postgres -d SitesIndex -Atc "$1"
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

login() {
  local username="$1"
  printf '{"username":"%s","password":"%s"}' "$username" "$E2E_PASSWORD" >"$HTTP_DIR/login.json"
  local code
  code="$(http_post_json_anon "$API_URL/v1/auth/login" "$HTTP_DIR/login.json" "$HTTP_DIR/login.out")"
  [ "$code" = "200" ] || fail "login for ${username} returned HTTP ${code}"
  python3 - "$HTTP_DIR/login.out" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
token = data.get("access_token")
if not token:
    raise SystemExit("login response has no access token")
print(token)
PY
}

run_agent_named() {
  local token="$1" agent="$2" out="$3" code
  printf '{"agent_id":"%s","input":"go"}' "$agent" >"$HTTP_DIR/run-body.json"
  code="$(http_post_json "$EVENTS_URL/v1/agent/run" "$token" "$HTTP_DIR/run-body.json" "$out")"
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

run_agent() { run_agent_named "$1" "mcp-e2e-agent" "$2"; }

run_agent_cancelled() {
  local token="$1" out="$2" rc
  printf '{"agent_id":"mcp-e2e-slow-agent","input":"go"}' >"$HTTP_DIR/cancel-body.json"
  set +e
  if http_host_mode; then
    curl -sS --max-time 2 -o "$out" -X POST "$EVENTS_URL/v1/agent/run" \
      -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
      --data-binary "@$HTTP_DIR/cancel-body.json" >/dev/null
  else
    http_helper -sS --max-time 2 -X POST "$EVENTS_URL/v1/agent/run" \
      -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
      --data-binary @- <"$HTTP_DIR/cancel-body.json" >/dev/null
  fi
  rc="$?"
  set -e
  [ "$rc" != "0" ] || fail "slow agent run completed before cancellation"
}

run_agent_expect_denied() {
  local token="$1" out="$2" code
  printf '{"agent_id":"mcp-e2e-agent","input":"go"}' >"$HTTP_DIR/deny-body.json"
  code="$(http_post_json "$EVENTS_URL/v1/agent/run" "$token" "$HTTP_DIR/deny-body.json" "$out")"
  if [ "$code" != "200" ] && [ "$code" != "502" ]; then
    fail "denied manual run returned unexpected HTTP ${code}: $(cat "$out")"
  fi
  [ "$code" = "502" ] && return 0
  python3 - "$out" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
result = data.get("result") or {}
calls = result.get("tool_calls") or []
if len(calls) != 1 or calls[0].get("status") not in ("failed", "denied"):
    raise SystemExit("denied manual run was not reported as a tool denial: %s" % data)
if not calls[0].get("reason_code") or not result.get("run_id"):
    raise SystemExit("denied manual run lacks reason/run identity: %s" % data)
PY
}

latest_proposal() {
  local token="$1"
  http_get_auth "$API_URL/v1/console/mcp/mutations/proposals?limit=100" "$token" "$HTTP_DIR/list.json"
  python3 - "$HTTP_DIR/list.json" >"$HTTP_DIR/proposal.env" <<'PY'
import json, sys
items = (json.load(open(sys.argv[1], encoding="utf-8")).get("items") or [])
if not items:
    raise SystemExit("no proposals")
p = items[0]
print("PID=%s" % p["proposal_id"])
print("SHA=%s" % p["proposal_sha256"])
print("EXP=%s" % p["expires_at"])
PY
  . "$HTTP_DIR/proposal.env"
}

approve() {
  local token="$1" pid="$2" sha="$3" exp="$4" out="$5"
  printf '{"proposal_sha256":"%s","identity_version":1,"max_executions":1,"requested_expiry":"%s","reason":"e2e authorization"}' "$sha" "$exp" >"$HTTP_DIR/approve-body.json"
  http_post_json "$API_URL/v1/console/mcp/mutations/proposals/${pid}/approve" "$token" "$HTTP_DIR/approve-body.json" "$out"
}

echo "=== MCP authorization E2E workspace: $WORK ==="
OWNER_TOKEN="$(login mcp-e2e-operator)"
ADMIN_TOKEN="$(login mcp-e2e-admin)"
pass "authenticated owner and administrator"

run_agent "$OWNER_TOKEN" "$HTTP_DIR/run-positive.json"
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationProposals')" "1" "positive proposal admitted"
assert_eq "$(q "SELECT state FROM MCPMutationProposals LIMIT 1")" "approval_required" "positive proposal inert"
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationApprovals')" "0" "positive proposal has no approval"
assert_no_mutation "positive admission"
pass "positive manual run stays inert"

latest_proposal "$OWNER_TOKEN"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$HTTP_DIR/approve-positive.json")" "200" "authorized current-policy approval"
assert_eq "$(q "SELECT state FROM MCPMutationProposals WHERE proposal_id='${PID}'")" "approved" "approved proposal state"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}' AND state='active'")" "1" "active approval row"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE event_type='mcp_mutation_proposal_created' AND proposal_id='${PID}'")" "1" "proposal-created audit"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE event_type='mcp_mutation_approval_issued' AND proposal_id='${PID}'")" "1" "approval-issued audit"
assert_no_mutation "positive approval"
pass "positive approval is record-only"

BEFORE_CANCEL="$(q 'SELECT COUNT(*) FROM MCPMutationProposals')"
run_agent_cancelled "$OWNER_TOKEN" "$HTTP_DIR/run-cancelled.json"
sleep 12
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationProposals')" "$BEFORE_CANCEL" "cancelled run inserted no proposal"
SLOW_STATS="$(docker exec "$SLOW_MODEL_CID" wget -qO- http://127.0.0.1:18095/stats)"
SLOW_CANCELED="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("canceled", 0))' "$SLOW_STATS")"
if [ "$SLOW_CANCELED" -lt 1 ]; then
  fail "request cancellation did not reach the model provider: ${SLOW_STATS}"
fi
pass "request cancellation reached the model provider"

BEFORE_SLOW="$(q 'SELECT COUNT(*) FROM MCPMutationProposals')"
run_agent_named "$OWNER_TOKEN" "mcp-e2e-slow-agent" "$HTTP_DIR/run-slow-positive.json"
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationProposals')" "$((BEFORE_SLOW + 1))" "uncancelled slow agent admitted a proposal"
latest_proposal "$OWNER_TOKEN"
assert_eq "$(q "SELECT state FROM MCPMutationProposals WHERE proposal_id='${PID}'")" "approval_required" "slow-agent control proposal inert"
docker exec "$DB_CID" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c "DELETE FROM MCPMutationAudit WHERE proposal_id='${PID}'" \
  -c "DELETE FROM MCPMutationProposals WHERE proposal_id='${PID}'"
pass "slow-agent positive control admitted one inert proposal"

run_agent "$OWNER_TOKEN" "$HTTP_DIR/run-mismatch.json"
cp "$WORK/config.yaml" "$WORK/config-mismatch.yaml"
chmod 644 "$WORK/config-mismatch.yaml"
python3 - "$WORK/config-mismatch.yaml" <<'PY'
import sys
path = sys.argv[1]
text = open(path, encoding="utf-8").read()
text = text.replace("policy_id: e2e-mutation-policy", "policy_id: e2e-mutation-policy-mismatch", 1)
open(path, "w", encoding="utf-8").write(text)
PY
python3 "$SUPPORT" write-overrides --work "$WORK" --output "$WORK/override-mismatch.yml" \
  --project "$PROJECT" --run-id "$RUN_ID" --source-sha "$SOURCE_SHA" --source-tree "$SOURCE_TREE" \
  --api-image "$API_IMAGE" --events-image "$EVENTS_IMAGE" --model-image "$MODEL_IMAGE" \
  --config "$WORK/config-mismatch.yaml" >/dev/null
"${COMPOSE[@]}" -f "$WORK/override-mismatch.yml" up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
wait_api_ready
sleep 2
latest_proposal "$OWNER_TOKEN"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$HTTP_DIR/approve-mismatch.json")" "409" "API/Events policy mismatch blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "policy mismatch left no approval"
pass "policy mismatch fails closed"

"${COMPOSE[@]}" up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
wait_api_ready
sleep 2

run_agent "$OWNER_TOKEN" "$HTTP_DIR/run-hash-mismatch.json"
cp "$WORK/config.yaml" "$WORK/config-hash-mismatch.yaml"
chmod 644 "$WORK/config-hash-mismatch.yaml"
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
python3 "$SUPPORT" write-overrides --work "$WORK" --output "$WORK/override-hash-mismatch.yml" \
  --project "$PROJECT" --run-id "$RUN_ID" --source-sha "$SOURCE_SHA" --source-tree "$SOURCE_TREE" \
  --api-image "$API_IMAGE" --events-image "$EVENTS_IMAGE" --model-image "$MODEL_IMAGE" \
  --config "$WORK/config-hash-mismatch.yaml" >/dev/null
"${COMPOSE[@]}" -f "$WORK/override-hash-mismatch.yml" up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
wait_api_ready
sleep 2
latest_proposal "$OWNER_TOKEN"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$HTTP_DIR/approve-hash-mismatch.json")" "409" "same-version canonical hash change blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "hash mismatch left no approval"
pass "same-version policy hash change fails closed"

run_agent "$OWNER_TOKEN" "$HTTP_DIR/run-disabled-policy.json"
cp "$WORK/config.yaml" "$WORK/config-disabled-policy.yaml"
chmod 644 "$WORK/config-disabled-policy.yaml"
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
python3 "$SUPPORT" write-overrides --work "$WORK" --output "$WORK/override-disabled-policy.yml" \
  --project "$PROJECT" --run-id "$RUN_ID" --source-sha "$SOURCE_SHA" --source-tree "$SOURCE_TREE" \
  --api-image "$API_IMAGE" --events-image "$EVENTS_IMAGE" --model-image "$MODEL_IMAGE" \
  --config "$WORK/config-disabled-policy.yaml" >/dev/null
"${COMPOSE[@]}" -f "$WORK/override-disabled-policy.yml" up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
wait_api_ready
sleep 2
latest_proposal "$OWNER_TOKEN"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$HTTP_DIR/approve-disabled-policy.json")" "503" "disabled current policy blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "disabled policy left no approval"
pass "disabled current policy fails closed"

"${COMPOSE[@]}" up -d --no-deps --force-recreate --no-build crowler-api >/dev/null
wait_api_ready
sleep 2

run_agent "$OWNER_TOKEN" "$HTTP_DIR/run-ownership.json"
latest_proposal "$OWNER_TOKEN"
docker exec -i "$DB_CID" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
UPDATE Sources SET usr_id = 43 WHERE source_id = 42;
UPDATE SourceOwnership
SET user_id = 43, verified_at = CURRENT_TIMESTAMP,
    verified_by_user_id = 43, verified_by = 'mcp-e2e-fixture'
WHERE source_id = 42;
SQL
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$HTTP_DIR/approve-ownership.json")" "403" "transferred ownership blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "ownership transfer left no approval"
pass "ownership transfer fails closed"
BEFORE_DENIED="$(q 'SELECT COUNT(*) FROM MCPMutationProposals')"
run_agent_expect_denied "$OWNER_TOKEN" "$HTTP_DIR/run-admission-denied.json"
assert_eq "$(q 'SELECT COUNT(*) FROM MCPMutationProposals')" "$BEFORE_DENIED" "transferred ownership denied admission"
DENIED_RUN="$(python3 -c 'import json,sys; print((json.load(open(sys.argv[1], encoding="utf-8")).get("result") or {}).get("run_id",""))' "$HTTP_DIR/run-admission-denied.json")"
if [ -n "$DENIED_RUN" ]; then
  assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE event_type='mcp_mutation_admission_denied' AND run_id='${DENIED_RUN}'")" "1" "denied admission audit matches run"
else
  DENIED_AUDITS="$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE event_type='mcp_mutation_admission_denied'")"
  if [ "$DENIED_AUDITS" -lt 1 ]; then
    fail "denied admission produced no audit event"
  fi
fi
pass "denied admission recorded an audit event"
docker exec -i "$DB_CID" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
UPDATE Sources SET usr_id = 42 WHERE source_id = 42;
UPDATE SourceOwnership
SET user_id = 42, verified_at = CURRENT_TIMESTAMP,
    verified_by_user_id = 42, verified_by = 'mcp-e2e-fixture'
WHERE source_id = 42;
SQL

run_agent "$OWNER_TOKEN" "$HTTP_DIR/run-hash.json"
latest_proposal "$OWNER_TOKEN"
WRONG_SHA="$(printf '0%.0s' $(seq 1 64))"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$WRONG_SHA" "$EXP" "$HTTP_DIR/approve-hash.json")" "409" "incorrect proposal hash blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "incorrect hash left no approval"
pass "incorrect proposal fingerprint fails closed"

run_agent "$OWNER_TOKEN" "$HTTP_DIR/run-expired.json"
latest_proposal "$OWNER_TOKEN"
docker exec "$DB_CID" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c "UPDATE MCPMutationProposals SET created_at=now() - interval '10 minutes', expires_at=now() - interval '1 minute' WHERE proposal_id='${PID}'"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$HTTP_DIR/approve-expired.json")" "410" "expired proposal blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "expired proposal left no approval"
pass "expired proposal fails closed"

run_agent "$OWNER_TOKEN" "$HTTP_DIR/run-corrupt.json"
latest_proposal "$OWNER_TOKEN"
docker exec "$DB_CID" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c "UPDATE MCPMutationProposals SET policy_snapshot='{}'::jsonb WHERE proposal_id='${PID}'"
assert_eq "$(approve "$OWNER_TOKEN" "$PID" "$SHA" "$EXP" "$HTTP_DIR/approve-corrupt.json")" "503" "corrupted policy snapshot blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "corrupt proposal left no approval"
docker exec "$DB_CID" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c "DELETE FROM MCPMutationProposals WHERE proposal_id='${PID}'"
pass "corrupted policy snapshot fails closed"

run_agent "$OWNER_TOKEN" "$HTTP_DIR/run-disabled.json"
latest_proposal "$OWNER_TOKEN"
docker exec "$DB_CID" psql -U postgres -d SitesIndex -v ON_ERROR_STOP=1 >/dev/null \
  -c 'UPDATE Users SET disabled=TRUE WHERE user_id=42'
assert_eq "$(approve "$ADMIN_TOKEN" "$PID" "$SHA" "$EXP" "$HTTP_DIR/approve-disabled.json")" "403" "disabled proposal principal blocked"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "disabled principal left no approval"
pass "disabled principal fails closed"

assert_no_mutation "negative matrix"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE redacted_detail::text LIKE '%source_id%'")" "0" "audit redaction contains no source arguments"
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationAudit WHERE redacted_detail::text LIKE '%fixture-resource%'")" "0" "audit redaction contains no resource arguments"

docker stop "$DB_CID" >/dev/null
OUTAGE_CODE="$(approve "$ADMIN_TOKEN" "$PID" "$SHA" "$EXP" "$HTTP_DIR/approve-outage.json")"
if [ "$OUTAGE_CODE" = "200" ]; then
  fail "database outage produced an approval"
fi
pass "database outage fails closed (HTTP ${OUTAGE_CODE})"
docker start "$DB_CID" >/dev/null
for _ in $(seq 1 60); do
  docker exec "$DB_CID" pg_isready -U postgres -d SitesIndex >/dev/null 2>&1 && break
  sleep 2
done
assert_eq "$(q "SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id='${PID}'")" "0" "outage left no approval"
assert_no_mutation "database outage"
pass "fixture completed"

echo "=== MCP authorization E2E PASSED ==="
