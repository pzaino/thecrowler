#!/usr/bin/env bash
# Select the versioned PostgreSQL client binary directory for a required major.
#
# Ubuntu runners may carry a newer PostgreSQL client earlier in PATH than the
# version installed for the disposable server. Resolve the binary from the
# versioned package instead of assuming /usr/bin or a fixed prefix.
set -euo pipefail

major="${1:?usage: select-postgres-client-major.sh <major>}"
case "$major" in
  ''|*[!0-9]*) echo "PostgreSQL major must be numeric" >&2; exit 2 ;;
esac

package="postgresql-client-${major}"
if ! command -v dpkg >/dev/null 2>&1; then
  echo "dpkg is required to locate ${package}" >&2
  exit 2
fi
dump_path="$(dpkg -L "$package" | awk '/\/bin\/pg_dump$/{print; exit}')"
if [ -z "$dump_path" ]; then
  echo "${package} does not provide a pg_dump binary" >&2
  exit 2
fi
bin_dir="$(dirname "$dump_path")"
for tool in pg_dump pg_restore; do
  if [ ! -x "$bin_dir/$tool" ]; then
    echo "${package} is missing executable ${tool} in ${bin_dir}" >&2
    exit 2
  fi
  actual="$("$bin_dir/$tool" --version | awk '{print $3}' | cut -d. -f1)"
  if [ "$actual" != "$major" ]; then
    echo "${tool} resolved to major ${actual}, expected ${major}" >&2
    exit 1
  fi
done
printf '%s\n' "$bin_dir"
