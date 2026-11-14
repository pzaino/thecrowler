#!/usr/bin/env bash
# Regression test for select-postgres-client-major.sh using fake dpkg/binaries.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SELECTOR="$ROOT/scripts/select-postgres-client-major.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/pg17/bin" "$TMP/pg18/bin"
cat >"$TMP/bin/dpkg" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [ "$1" = "-L" ] && [ "$2" = "postgresql-client-17" ]; then
  echo "$FAKE_PG17/bin/pg_dump"
  echo "$FAKE_PG17/bin/pg_restore"
  exit 0
fi
exit 1
EOF
chmod +x "$TMP/bin/dpkg"
cat >"$TMP/pg17/bin/pg_dump" <<'EOF'
#!/usr/bin/env bash
echo "pg_dump (PostgreSQL) 17.5"
EOF
cat >"$TMP/pg17/bin/pg_restore" <<'EOF'
#!/usr/bin/env bash
echo "pg_restore (PostgreSQL) 17.5"
EOF
cat >"$TMP/pg18/bin/pg_dump" <<'EOF'
#!/usr/bin/env bash
echo "pg_dump (PostgreSQL) 18.6"
EOF
cat >"$TMP/pg18/bin/pg_restore" <<'EOF'
#!/usr/bin/env bash
echo "pg_restore (PostgreSQL) 18.6"
EOF
chmod +x "$TMP/pg17/bin/"* "$TMP/pg18/bin/"*

selected="$(PATH="$TMP/bin:$PATH" FAKE_PG17="$TMP/pg17" bash "$SELECTOR" 17)"
test "$selected" = "$TMP/pg17/bin"
echo "PASS: selects versioned PostgreSQL 17 client"

if PATH="$TMP/bin:$PATH" FAKE_PG17="$TMP/pg18" bash "$SELECTOR" 17 >/dev/null 2>&1; then
  echo "FAIL: mismatched client major was accepted" >&2
  exit 1
fi
echo "PASS: rejects mismatched client major"

if PATH="$TMP/bin:$PATH" FAKE_PG17="$TMP/pg17" bash "$SELECTOR" 18 >/dev/null 2>&1; then
  echo "FAIL: missing package major was accepted" >&2
  exit 1
fi
echo "PASS: rejects missing versioned package"
