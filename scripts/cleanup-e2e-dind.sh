#!/usr/bin/env bash
# Remove only the exact run/attempt-owned outer dind container from the HOST
# daemon. Labels are necessary but not sufficient: exact name, attempt and any
# recorded immutable container ID must agree.
#
# Usage:
#   cleanup-e2e-dind.sh <run_id> <run_attempt> <dind_name> [metadata_json]
#
# metadata_json is the run-local dind-container.json written after creation. If
# it is absent (failure before recording), the exact name plus exact run/attempt
# labels are used as a narrow fallback. No broad label-only removal exists.
set -euo pipefail

run_id="${1:?usage: cleanup-e2e-dind.sh <run_id> <run_attempt> <dind_name> [metadata_json]}"
run_attempt="${2:?usage: cleanup-e2e-dind.sh <run_id> <run_attempt> <dind_name> [metadata_json]}"
dind_name="${3:?usage: cleanup-e2e-dind.sh <run_id> <run_attempt> <dind_name> [metadata_json]}"
metadata="${4:-}"
case "$run_id" in ''|*[!0-9]*) echo "FAIL: invalid run ID" >&2; exit 2 ;; esac
case "$run_attempt" in ''|*[!0-9]*) echo "FAIL: invalid run attempt" >&2; exit 2 ;; esac
case "$dind_name" in ''|*[!A-Za-z0-9_.-]*) echo "FAIL: invalid dind container name" >&2; exit 2 ;; esac

host_docker() {
  env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH \
    docker "$@"
}

expected_id=""
if [ -n "$metadata" ] && [ -f "$metadata" ]; then
  if ! expected_id="$(
    python3 - "$metadata" "$run_id" "$run_attempt" "$dind_name" <<'PY'
import json
import sys
path, run_id, attempt, name = sys.argv[1:]
data = json.load(open(path, encoding="utf-8"))
if str(data.get("run_id")) != run_id or str(data.get("run_attempt")) != attempt or data.get("name") != name:
    raise SystemExit("metadata identity mismatch")
ident = str(data.get("container_id") or "")
if not ident:
    raise SystemExit("metadata container ID is missing")
print(ident)
PY
  )"; then
    echo "FAIL: dind metadata is missing or does not match this run/attempt/name" >&2
    exit 1
  fi
fi

if ! host_docker info >/dev/null 2>&1; then
  echo "FAIL: host Docker daemon is unavailable" >&2
  exit 1
fi
if ! host_context="$(host_docker context show 2>/dev/null)"; then
  echo "FAIL: could not determine the host Docker context" >&2
  exit 1
fi
if [ "$host_context" != "default" ]; then
  echo "FAIL: unexpected host Docker context '${host_context}'; expected 'default'" >&2
  exit 1
fi

# Docker treats the name filter as an anchored regular expression. Escape the
# only regex-significant character permitted in a container name so a lookalike
# such as `name-extra` cannot match an exact name containing a dot.
name_pattern="^${dind_name//./\\.}$"
if ! name_ids="$(host_docker ps -aq --filter "name=${name_pattern}")"; then
  echo "FAIL: could not query exact dind name on host daemon" >&2
  exit 1
fi
if ! run_ids="$(host_docker ps -aq --filter "label=org.crowler.e2e.dind=${run_id}")"; then
  echo "FAIL: could not query run-labelled dind containers on host daemon" >&2
  exit 1
fi

candidates="$(printf '%s\n%s\n' "$name_ids" "$run_ids" | sed '/^$/d' | sort -u)"
removed=0
for id in $candidates; do
  if ! info="$(host_docker inspect -f '{{.Id}}|{{.Name}}|{{index .Config.Labels "org.crowler.e2e.dind"}}|{{index .Config.Labels "org.crowler.e2e.attempt"}}' "$id" 2>/dev/null)"; then
    echo "FAIL: could not inspect candidate ${id}; no removal attempted" >&2
    exit 1
  fi
  IFS='|' read -r actual_id actual_name actual_run actual_attempt <<<"$info"
  actual_name="${actual_name#/}"
  if [ "$actual_name" != "$dind_name" ]; then
    echo "FAIL: preserving ${actual_id}; name '${actual_name}' does not match '${dind_name}'" >&2
    exit 1
  fi
  if [ "$actual_run" != "$run_id" ]; then
    echo "FAIL: preserving ${actual_id}; run label '${actual_run}' does not match '${run_id}'" >&2
    exit 1
  fi
  if [ "$actual_attempt" != "$run_attempt" ]; then
    echo "FAIL: preserving ${actual_id}; attempt label '${actual_attempt}' does not match '${run_attempt}'" >&2
    exit 1
  fi
  if [ -n "$expected_id" ] && [ "$actual_id" != "$expected_id" ]; then
    echo "FAIL: preserving ${actual_id}; recorded container ID '${expected_id}' does not match" >&2
    exit 1
  fi
  if ! host_docker rm -f "$actual_id" >/dev/null 2>&1; then
    echo "FAIL: could not remove verified outer dind container ${actual_id}" >&2
    exit 1
  fi
  removed=$((removed + 1))
done

if [ -n "$expected_id" ]; then
  if host_docker inspect "$expected_id" >/dev/null 2>&1; then
    echo "FAIL: recorded outer dind container ${expected_id} still exists" >&2
    exit 1
  fi
fi
if ! remaining_name="$(host_docker ps -aq --filter "name=${name_pattern}")"; then
  echo "FAIL: could not verify exact dind name removal" >&2
  exit 1
fi
if [ -n "$remaining_name" ]; then
  echo "FAIL: exact dind name remains: ${remaining_name}" >&2
  exit 1
fi
if ! remaining_run="$(host_docker ps -aq --filter "label=org.crowler.e2e.dind=${run_id}" --filter "label=org.crowler.e2e.attempt=${run_attempt}")"; then
  echo "FAIL: could not verify run/attempt label removal" >&2
  exit 1
fi
if [ -n "$remaining_run" ]; then
  echo "FAIL: run/attempt-labelled outer dind container remains: ${remaining_run}" >&2
  exit 1
fi
if host_docker inspect "$dind_name" >/dev/null 2>&1; then
  echo "FAIL: exact dind container is still inspectable after cleanup" >&2
  exit 1
fi
echo "PASS: exact outer dind container absent (removed ${removed})"
