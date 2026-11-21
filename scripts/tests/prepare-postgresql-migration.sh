#!/usr/bin/env bash
# Regression test for prepare-postgresql-migration.sh
#
# Verifies the exact token replacement, the identifier validation, the
# no-overwrite guard and the byte-for-byte body preservation, using temporary
# files only. No database is contacted.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HELPER="$ROOT/scripts/prepare-postgresql-migration.sh"
MIGRATION="$ROOT/pkg/database/db_migrations/postgresql-migration-v1.17.pgsql"
ADDON="$ROOT/pkg/database/db_migrations/postgresql-agent-goal-v1.17.pgsql"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

# --- 1. real migration -------------------------------------------------------
out="$WORK/migration.gui.sql"
"$HELPER" --role crowler --output "$out" >/dev/null
[ "$(grep -c ':CROWLER_DB_USER' "$out" || true)" = "0" ] || fail "token survived in the real migration"
[ "$(grep -c '"crowler"' "$out" || true)" -ge 29 ] || fail "expected at least 29 quoted replacements"
if ! cmp -s <(sed 's/:CROWLER_DB_USER/"crowler"/g' "$MIGRATION") <(tail -n +8 "$out"); then
  fail "generated body differs from the real migration outside the token"
fi
head -3 "$out" | grep -q 'GENERATED FILE' || fail "missing generated-file banner"
echo "PASS: prepares the real v1.17 migration (29 replacements, body byte-identical)"

# --- 2. optional add-on ------------------------------------------------------
addon_out="$WORK/agent-goal.gui.sql"
"$HELPER" --role crowler --input "$ADDON" --output "$addon_out" >/dev/null
[ "$(grep -c ':CROWLER_DB_USER' "$addon_out" || true)" = "0" ] || fail "token survived in the add-on"
if ! cmp -s <(sed 's/:CROWLER_DB_USER/"crowler"/g' "$ADDON") <(tail -n +8 "$addon_out"); then
  fail "generated add-on body differs outside the token"
fi
echo "PASS: prepares the optional agent-goal add-on"

# --- 3. synthetic body, quoting and multiple tokens -------------------------
cat >"$WORK/synthetic.sql" <<'SQL'
-- a comment that must survive untouched: 'quotes' and :not_a_token
CREATE TABLE t (id INT);
ALTER TABLE t OWNER TO :CROWLER_DB_USER;
GRANT SELECT ON t TO :CROWLER_DB_USER;
SQL
"$HELPER" --role "Crowler_Ops" --input "$WORK/synthetic.sql" --output "$WORK/synthetic.gui.sql" >/dev/null
grep -q 'OWNER TO "Crowler_Ops";' "$WORK/synthetic.gui.sql" || fail "case-preserving quoted identifier missing"
grep -q "GRANT SELECT ON t TO \"Crowler_Ops\";" "$WORK/synthetic.gui.sql" || fail "second replacement missing"
grep -q ":not_a_token" "$WORK/synthetic.gui.sql" || fail "an unrelated colon token was altered"
echo "PASS: replaces only :CROWLER_DB_USER and quotes the role"

# --- 4. role-name validation -------------------------------------------------
for bad in 'crowler"' 'crowler"; DROP TABLE x; --' 'crow ler' '1crowler' 'crowler.ops' 'crow$ler' ''; do
  if "$HELPER" --role "$bad" --input "$WORK/synthetic.sql" --output "$WORK/bad.sql" >/dev/null 2>&1; then
    fail "accepted unsafe role: [$bad]"
  fi
done
long_role="$(printf 'a%.0s' $(seq 1 64))"
if "$HELPER" --role "$long_role" --input "$WORK/synthetic.sql" --output "$WORK/bad.sql" >/dev/null 2>&1; then
  fail "accepted a role longer than 63 characters"
fi
echo "PASS: rejects quotes, injection, spaces, leading digits, dots, 64+ characters"

# --- 5. output guards --------------------------------------------------------
"$HELPER" --role crowler --input "$WORK/synthetic.sql" --output "$WORK/exists.sql" >/dev/null
if "$HELPER" --role crowler --input "$WORK/synthetic.sql" --output "$WORK/exists.sql" >/dev/null 2>&1; then
  fail "overwrote an existing output without --force"
fi
"$HELPER" --role crowler --input "$WORK/synthetic.sql" --output "$WORK/exists.sql" --force >/dev/null
if "$HELPER" --role crowler --input "$WORK/synthetic.sql" --output "$WORK" --force >/dev/null 2>&1; then
  fail "accepted a directory as the output path"
fi
echo "PASS: refuses to overwrite without --force and honours --force"

if "$HELPER" --role crowler --input "$WORK/synthetic.sql" --output "$WORK/synthetic.sql" >/dev/null 2>&1; then
  fail "allowed in-place rewriting of the source file"
fi
echo "PASS: refuses to rewrite the source file in place"

# --- 6. missing input / no token --------------------------------------------
if "$HELPER" --role crowler --input "$WORK/absent.sql" --output "$WORK/absent.out" >/dev/null 2>&1; then
  fail "accepted a missing input file"
fi
printf 'SELECT 1;\n' >"$WORK/no-token.sql"
if "$HELPER" --role crowler --input "$WORK/no-token.sql" --output "$WORK/no-token.out" >/dev/null 2>&1; then
  fail "accepted an input with no token"
fi
echo "PASS: rejects a missing input and an input without the token"
