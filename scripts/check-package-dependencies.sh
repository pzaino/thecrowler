#!/usr/bin/env bash
set -euo pipefail

readonly module="github.com/pzaino/thecrowler"
readonly forbidden_pattern="^${module}/pkg/(crawler|infoseed)(/|$)"
readonly packages=(pkg/browser pkg/scraper)

status=0
for package_dir in "${packages[@]}"; do
  [[ -d "$package_dir" ]] || continue

  imports="$({
    go list -f '{{range .Imports}}{{println .}}{{end}}{{range .TestImports}}{{println .}}{{end}}{{range .XTestImports}}{{println .}}{{end}}' "./${package_dir}/..."
  } | sort -u)"

  if forbidden_imports="$(printf '%s\n' "$imports" | rg "$forbidden_pattern" || true)" && [[ -n "$forbidden_imports" ]]; then
    printf 'forbidden package dependency in %s:\n%s\n' "$package_dir" "$forbidden_imports" >&2
    status=1
  fi
done

exit "$status"
