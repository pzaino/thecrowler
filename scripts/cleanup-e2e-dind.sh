#!/usr/bin/env bash
# Remove only the run-labelled outer Docker-in-Docker container from the HOST
# daemon. This script intentionally ignores any nested DOCKER_HOST inherited
# from GITHUB_ENV and never runs broad prune/down commands.
set -euo pipefail

run_id="${1:?usage: cleanup-e2e-dind.sh <run_id> <dind_name>}"
dind_name="${2:?usage: cleanup-e2e-dind.sh <run_id> <dind_name>}"
case "$run_id" in
  ''|*[!0-9]*) echo "FAIL: invalid run ID" >&2; exit 2 ;;
esac
case "$dind_name" in
  ''|*[!A-Za-z0-9_.-]*) echo "FAIL: invalid dind container name" >&2; exit 2 ;;
esac

host_docker() {
  env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH \
    docker "$@"
}

remove_if_owned() {
  local id="$1" label
  if label="$(host_docker inspect -f '{{ index .Config.Labels "org.crowler.e2e.dind" }}' "$id" 2>/dev/null)"; then
    :
  else
    echo "FAIL: could not inspect outer dind container ${id}" >&2
    return 1
  fi
  if [ "$label" != "$run_id" ]; then
    echo "FAIL: refusing to remove ${id}; dind label is '${label}', expected '${run_id}'" >&2
    return 1
  fi
  if ! host_docker rm -f "$id" >/dev/null 2>&1; then
    echo "FAIL: could not remove outer dind container ${id}" >&2
    return 1
  fi
}

label_ids="$(host_docker ps -aq --filter "label=org.crowler.e2e.dind=${run_id}")" || {
  echo "FAIL: could not query host daemon for run-labelled dind containers" >&2
  exit 1
}
for id in $label_ids; do
  remove_if_owned "$id" || exit 1
done

# A same-name container without the run label is foreign and must be preserved.
name_ids="$(host_docker ps -aq --filter "name=^${dind_name}$")" || {
  echo "FAIL: could not query host daemon for dind container name" >&2
  exit 1
}
for id in $name_ids; do
  remove_if_owned "$id" || exit 1
done

remaining="$(host_docker ps -aq --filter "label=org.crowler.e2e.dind=${run_id}")" || {
  echo "FAIL: could not verify dind removal" >&2
  exit 1
}
if [ -n "$remaining" ]; then
  echo "FAIL: run-labelled outer dind container remains: ${remaining}" >&2
  exit 1
fi
remaining_name="$(host_docker ps -aq --filter "name=^${dind_name}$")" || {
  echo "FAIL: could not verify dind name removal" >&2
  exit 1
}
if [ -n "$remaining_name" ]; then
  echo "FAIL: outer dind name remains: ${remaining_name}" >&2
  exit 1
fi
if host_docker inspect "$dind_name" >/dev/null 2>&1; then
  echo "FAIL: outer dind container is still inspectable after cleanup" >&2
  exit 1
fi
echo "PASS: outer dind container absent from host daemon"
