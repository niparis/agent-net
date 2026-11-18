#!/usr/bin/env bash
# Find the smallest set of Claude Code sandbox keys that lets a Playwright
# browser check run on macOS. It runs probe.mjs under the sandbox runtime that
# Claude Code uses (the same Seatbelt profile and the same network keys), starts
# with allowLocalBinding only, and adds each Mach service the sandbox reports
# as denied until the probe passes or nothing new is denied. The last line is
# the key set to put in claudeBrowserMachServices (internal/daemon/worker_claude.go).
# Usage: scripts/claude-browser-sandbox/ladder.sh [chromium|webkit]
# It must run outside another sandbox (sandbox-exec cannot nest). It writes only
# to a temporary folder, needs network for npm, and the Playwright browsers of
# playwright-core 1.64.0 (npx playwright@1.64.0 install chromium webkit).
set -euo pipefail
engine="${1:-chromium}"
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/browser-ladder.XXXXXX")"
trap 'rm -rf "$work"' EXIT
cp "$here/probe.mjs" "$work/"
(cd "$work" && npm init -y >/dev/null && npm i --silent playwright-core@1.64.0 @anthropic-ai/sandbox-runtime@0.0.83)

mach=()
for step in 1 2 3 4 5 6 7 8 9 10 11 12; do
  list="$(printf '"%s",' "${mach[@]+"${mach[@]}"}")"
  cat >"$work/srt.json" <<JSON
{"network":{"allowedDomains":[],"deniedDomains":[],"allowLocalBinding":true,"allowMachLookup":[${list%,}]},
 "filesystem":{"denyRead":[],"allowWrite":["$work"],"denyWrite":[]}}
JSON
  echo "== step $step: allowLocalBinding + ${#mach[@]} Mach services"
  set +e
  (cd "$work" && "$work/node_modules/.bin/srt" --settings "$work/srt.json" node probe.mjs "$engine" "$work/shots") >"$work/out.txt" 2>&1
  code=$?
  set -e
  sed 's/^/   /' "$work/out.txt" | head -30
  if [ "$code" -eq 0 ]; then
    echo "PASS with allowLocalBinding and allowMachLookup=[${list%,}]"
    exit 0
  fi
  new="$(grep -o 'mach-lookup [^ )"]*' "$work/out.txt" | awk '{print $2}' | sort -u | grep -vxF -f <(printf '%s\n' "${mach[@]+"${mach[@]}"}") || true)"
  if [ -z "$new" ]; then
    echo "FAIL: the probe fails and no new Mach service is denied. Another operation is denied (see the output above)."
    exit 1
  fi
  while read -r name; do mach+=("$name"); done <<<"$new"
done
echo "FAIL: still failing after 12 steps"
exit 1
