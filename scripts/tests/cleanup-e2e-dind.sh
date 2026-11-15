#!/usr/bin/env bash
# Regression tests for the host-scoped dind cleanup script.
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
args = sys.argv[1:]
if not args:
    raise SystemExit(2)
cmd = args[0]
if cmd == "ps":
    joined = " ".join(args)
    if "label=org.crowler.e2e.dind=" in joined:
        print("\n".join(state.get("ids", [])))
    elif "name=^" in joined:
        print("\n".join(state.get("name_ids", [])))
    else:
        raise SystemExit(2)
elif cmd == "inspect":
    if state.get("inspect_fail"):
        raise SystemExit(1)
    if args[1] == "-f":
        ident = args[-1]
        if ident not in state.get("ids", []) and ident not in state.get("name_ids", []):
            raise SystemExit(1)
        print(state.get("label", ""))
    else:
        if state.get("exists"):
            print("{}")
        else:
            raise SystemExit(1)
elif cmd == "rm":
    if state.get("rm_fail"):
        raise SystemExit(1)
    state["rm_calls"] = state.get("rm_calls", 0) + 1
    ident = args[-1]
    state["ids"] = [value for value in state.get("ids", []) if value != ident]
    state["name_ids"] = [value for value in state.get("name_ids", []) if value != ident]
    state["exists"] = bool(state.get("ids") or state.get("name_ids"))
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(state, handle)
else:
    raise SystemExit(2)
PY
chmod +x "$TMP/bin/docker"

run_case() {
  local name="$1" expected="$2" state="$3" nested="${4:-0}" rm_expected="${5:-}"
  local state_file="$TMP/$name.json"
  printf '%s' "$state" >"$state_file"
  local output rc
  set +e
  if [ "$nested" = "1" ]; then
    output="$(PATH="$TMP/bin:$PATH" FAKE_DOCKER_STATE="$state_file" DOCKER_HOST="tcp://nested:2375" \
      bash "$CLEANUP" 123 "wp04c2n-dind-123-1" 2>&1)"
  else
    output="$(PATH="$TMP/bin:$PATH" FAKE_DOCKER_STATE="$state_file" \
      bash "$CLEANUP" 123 "wp04c2n-dind-123-1" 2>&1)"
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

run_case "owned removal" 0 '{"ids":["abc"],"name_ids":["abc"],"label":"123","exists":true}' 0 1
run_case "repeated cleanup" 0 '{"ids":[],"name_ids":[],"label":"123","exists":false}' 0 0
run_case "foreign label preserved" 1 '{"ids":["abc"],"name_ids":[],"label":"foreign","exists":true}' 0 0
run_case "same name foreign preserved" 1 '{"ids":[],"name_ids":["abc"],"label":"foreign","exists":true}' 0 0
run_case "failed inspect" 1 '{"ids":["abc"],"name_ids":["abc"],"label":"123","exists":true,"inspect_fail":true}' 0 0
run_case "failed remove" 1 '{"ids":["abc"],"name_ids":["abc"],"label":"123","exists":true,"rm_fail":true}' 0 0
run_case "nested DOCKER_HOST not used" 0 '{"ids":["abc"],"name_ids":["abc"],"label":"123","exists":true}' 1 1
