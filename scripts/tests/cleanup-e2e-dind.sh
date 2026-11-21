#!/usr/bin/env bash
# Regression tests for exact-ownership host dind cleanup.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CLEANUP="$ROOT/scripts/cleanup-e2e-dind.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

cat >"$TMP/bin/docker" <<'PY'
#!/usr/bin/env python3
import json
import os
import sys

if os.environ.get("DOCKER_HOST"):
    print("nested DOCKER_HOST leaked into host cleanup", file=sys.stderr)
    raise SystemExit(9)
path = os.environ["FAKE_DOCKER_STATE"]
with open(path, encoding="utf-8") as handle:
    state = json.load(handle)
containers = state.setdefault("containers", [])
args = sys.argv[1:]
if not args:
    raise SystemExit(2)
cmd = args[0]
if cmd == "info":
    if state.get("info_fail"):
        raise SystemExit(1)
    print("29.0.0")
elif cmd == "context" and args[1:] == ["show"]:
    if state.get("context_fail"):
        raise SystemExit(1)
    print(state.get("context", "default"))
elif cmd == "ps":
    selected = list(containers)
    for arg in args:
        if arg.startswith("name=^") and arg.endswith("$"):
            expected = arg[len("name=^"):-1]
            selected = [c for c in selected if c.get("name") == "/" + expected]
        elif arg.startswith("label="):
            key_value = arg[len("label="):]
            key, _, value = key_value.partition("=")
            field = {"org.crowler.e2e.dind": "run", "org.crowler.e2e.attempt": "attempt"}.get(key, key)
            selected = [c for c in selected if str(c.get(field, "")) == value]
    print("\n".join(c["id"] for c in selected))
elif cmd == "inspect":
    if state.get("inspect_fail"):
        raise SystemExit(1)
    ident = args[-1]
    match = next((c for c in containers if c["id"] == ident or c["name"] == "/" + ident), None)
    if match is None:
        raise SystemExit(1)
    if len(args) > 1 and args[1] == "-f":
        print(f'{match["id"]}|{match["name"]}|{match.get("run","")}|{match.get("attempt","")}')
    else:
        print("{}")
elif cmd == "rm":
    if state.get("rm_fail"):
        raise SystemExit(1)
    state["rm_calls"] = state.get("rm_calls", 0) + 1
    ident = args[-1]
    state["containers"] = [c for c in containers if c["id"] != ident]
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(state, handle)
else:
    raise SystemExit(2)
PY
chmod +x "$TMP/bin/docker"

run_case() {
  local name="$1" expected="$2" state="$3" metadata="${4:-NONE}" nested="${5:-0}" rm_expected="${6:-}"
  local state_file="$TMP/$name.json" meta_file="$TMP/$name.meta.json"
  printf '%s' "$state" >"$state_file"
  local meta_arg=""
  if [ "$metadata" != "NONE" ]; then
    printf '%s' "$metadata" >"$meta_file"
    meta_arg="$meta_file"
  fi
  local output rc
  set +e
  if [ "$nested" = "1" ]; then
    output="$(PATH="$TMP/bin:$PATH" FAKE_DOCKER_STATE="$state_file" DOCKER_HOST="tcp://nested:2375" \
      bash "$CLEANUP" 123 1 "crowler-e2e-dind-123-1" "$meta_arg" 2>&1)"
  else
    output="$(PATH="$TMP/bin:$PATH" FAKE_DOCKER_STATE="$state_file" \
      bash "$CLEANUP" 123 1 "crowler-e2e-dind-123-1" "$meta_arg" 2>&1)"
  fi
  rc=$?
  set -e
  if [ "$rc" != "$expected" ]; then
    echo "FAIL: $name: rc=$rc expected=$expected output=$output" >&2
    exit 1
  fi
  if [ -n "$rm_expected" ]; then
    local calls
    calls="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("rm_calls",0))' "$state_file")"
    if [ "$calls" != "$rm_expected" ]; then
      echo "FAIL: $name: rm calls=$calls expected=$rm_expected" >&2
      exit 1
    fi
  fi
  echo "PASS: $name"
}

OWNED='{"containers":[{"id":"abc","name":"/crowler-e2e-dind-123-1","run":"123","attempt":"1"}],"exists":true}'
META='{"container_id":"abc","name":"crowler-e2e-dind-123-1","run_id":"123","run_attempt":"1"}'

run_case "exact owned removal" 0 "$OWNED" "$META" 0 1
run_case "repeated cleanup" 0 '{"containers":[]}' "$META" 0 0
run_case "same run label wrong name" 1 '{"containers":[{"id":"abc","name":"/foreign","run":"123","attempt":"1"}]}' "$META" 0 0
run_case "correct name wrong run label" 1 '{"containers":[{"id":"abc","name":"/crowler-e2e-dind-123-1","run":"999","attempt":"1"}]}' "$META" 0 0
run_case "same run different attempt" 1 '{"containers":[{"id":"abc","name":"/crowler-e2e-dind-123-1","run":"123","attempt":"2"}]}' "$META" 0 0
run_case "recorded ID mismatch" 1 "$OWNED" '{"container_id":"other","name":"crowler-e2e-dind-123-1","run_id":"123","run_attempt":"1"}' 0 0
run_case "name regex lookalike untouched" 0 '{"containers":[{"id":"abc","name":"/crowler-e2e-dind-123-1-extra","run":"","attempt":""}]}' NONE 0 0
run_case "inspect failure" 1 '{"inspect_fail":true,"containers":[{"id":"abc","name":"/crowler-e2e-dind-123-1","run":"123","attempt":"1"}]}' "$META" 0 0
run_case "remove failure" 1 '{"rm_fail":true,"containers":[{"id":"abc","name":"/crowler-e2e-dind-123-1","run":"123","attempt":"1"}]}' "$META" 0 0
run_case "host daemon unavailable" 1 '{"info_fail":true,"containers":[]}' "$META" 0 0
run_case "unexpected host context" 1 '{"context":"remote","containers":[{"id":"abc","name":"/crowler-e2e-dind-123-1","run":"123","attempt":"1"}]}' "$META" 0 0
run_case "nested DOCKER_HOST not used" 0 "$OWNED" "$META" 1 1
run_case "partial setup no metadata" 0 "$OWNED" NONE 0 1
run_case "recorded ID already absent" 0 '{"containers":[]}' '{"container_id":"gone","name":"crowler-e2e-dind-123-1","run_id":"123","run_attempt":"1"}' 0 0
run_case "metadata identity mismatch" 1 '{"containers":[]}' '{"container_id":"abc","name":"other","run_id":"123","run_attempt":"1"}' 0 0
