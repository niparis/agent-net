#!/bin/sh
# scripts/test-changed.sh: run the tests a diff needs, and nothing more.
#
# Usage: scripts/test-changed.sh [BASE] [--plan] [-- go-test-args...]
#
# BASE is the ref to compare against; the default is the merge-base of HEAD and
# main, which is what a worker branch needs. The script lists the changed files,
# matches each one against the rule table below, and runs the tests the first
# matching rule names. A file that matches no rule runs its own package.
# --plan prints the runs and checks without running them.
#
# Rule table. Keep it short: one line per rule. Add a line when a new
# cross-package dependency shows up in a failed hand-back.
#
#   migration  internal/gateway/store.go, migrations*.go, testdata/migrations.lock
#              -> gateway migration, replay, upgrade and cutover tests, with -race
#   test file  any *_test.go -> the tests that file declares
#   tui        internal/tui/* -> tui and graphview golden, title, help and schema tests
#   settings   internal/settings/*, internal/settingsview/*, any settings*.go
#              -> settings, gateway settings and cached-read, daemon settings tests, with -race
#   text       internal/guidance, agentfiles, setup, mcpserver, toolcatalog, cmd/agent-net
#              -> the changed package plus the tui goldens that quote its text
#   wire       internal/protocol, internal/client
#              -> decode, strict, frame and wire tests of the wire packages
#   (no rule)  -> go test for the changed package
#
# It also runs gofmt -l on the changed files and go vet on the changed packages,
# and exits non-zero if a run or a check fails.

set -u

module=github.com/niparis/agent-net

fail() {
  echo "scripts/test-changed.sh: $*" >&2
  exit 2
}

usage() {
  cat <<'EOF'
Usage: scripts/test-changed.sh [BASE] [--plan] [-- go-test-args...]

Run the tests a diff needs. BASE defaults to the merge-base of HEAD and main.
The rule table at the top of the script says which runs a changed path needs;
a path that matches no rule runs its own package. --plan prints the runs and
the checks without running them. Arguments after -- go to go test.
EOF
}

base=
plan=0
extra=
while [ $# -gt 0 ]; do
  case $1 in
    --plan|-n)
      plan=1
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    --)
      shift
      extra=$*
      break
      ;;
    -*)
      fail "unknown option: $1"
      ;;
    *)
      [ -z "$base" ] || fail "at most one base ref"
      base=$1
      shift
      ;;
  esac
done

root=$(git rev-parse --show-toplevel 2>/dev/null) || fail "not in a git repository"
cd "$root" || fail "cannot enter $root"

if [ -z "$base" ]; then
  main=$(git rev-parse --verify --quiet 'main^{commit}') || fail "no main branch; pass a base ref"
  base=$(git merge-base HEAD "$main") || fail "could not find the merge-base with main"
fi
git rev-parse --verify --quiet "$base^{commit}" >/dev/null || fail "unknown base ref: $base"

# The optional `agent-net test-run` wrapper runs every command under the
# delivery test step when this machine has it.
have_runner=0
if command -v agent-net >/dev/null 2>&1 && agent-net test-run --help >/dev/null 2>&1; then
  have_runner=1
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/agent-net-test-changed.XXXXXX") || fail "could not create a temporary directory"
trap 'rm -rf "$work"' EXIT HUP INT TERM

# rule_of maps a changed path to the rule that knows what it needs.
rule_of() {
  case $1 in
    internal/gateway/store.go|internal/gateway/migrations*.go|internal/gateway/testdata/migrations.lock)
      echo migration ;;
    *_test.go)
      echo test ;;
    internal/tui/*)
      echo tui ;;
    internal/settings/*|*/settings*.go)
      echo settings ;;
    internal/guidance/*|internal/agentfiles/*|internal/setup/*|internal/mcpserver/*|internal/toolcatalog/*|cmd/agent-net/*)
      echo text ;;
    internal/protocol/*|internal/client/*)
      echo wire ;;
    *)
      echo package ;;
  esac
}

# runs_for_rule prints one run per line: mode, package, test pattern, rule.
# The pattern "all" means no -run filter. Only the migration and settings
# rules use -race, because their tests share a database and a cached read.
runs_for_rule() {
  case $1 in
    migration)
      printf 'race %s %s migration\n' "$module/internal/gateway" 'Migrat|Replay|Upgrade|Cutover'
      ;;
    tui)
      printf 'plain %s %s tui\n' "$module/internal/tui" 'Golden|Title|Help|Schema'
      printf 'plain %s %s tui\n' "$module/internal/tui/graphview" 'Golden|Rasterize'
      ;;
    settings)
      printf 'race %s all settings\n' "$module/internal/settings"
      printf 'race %s all settings\n' "$module/internal/settingsview"
      printf 'race %s %s settings\n' "$module/internal/gateway" 'Settings|CachedReads'
      printf 'race %s %s settings\n' "$module/internal/daemon" 'Settings'
      ;;
    wire)
      for name in protocol client gateway mcpserver; do
        printf 'plain %s/internal/%s %s wire\n' "$module" "$name" 'Decode|Strict|Frame|Wire'
      done
      ;;
  esac
}

# package_of prints dir for the nearest directory that holds Go files.
package_of() {
  dir=$(dirname "$1")
  while [ "$dir" != "." ] && [ "$dir" != "/" ]; do
    if ls "$dir"/*.go >/dev/null 2>&1; then
      printf '%s\n' "$dir"
      return 0
    fi
    dir=$(dirname "$dir")
  done
  return 0
}

changed=$(git diff --name-only "$base" --) || fail "could not list the changed files"
untracked=$(git ls-files --others --exclude-standard 2>/dev/null || true)
changed=$(printf '%s\n%s\n' "$changed" "$untracked" | sed '/^$/d' | sort -u)

: >"$work/runs"
: >"$work/files"

printf '%s\n' "$changed" | while IFS= read -r path; do
  [ -n "$path" ] || continue
  rule=$(rule_of "$path")
  pkg=$(package_of "$path")
  case $rule in
    package)
      [ -n "$pkg" ] || continue
      printf 'plain %s all package\n' "$pkg" >>"$work/runs"
      ;;
    test)
      [ -f "$path" ] || continue
      [ -n "$pkg" ] || continue
      names=$(grep -hoE '^func (Test|Example|Fuzz)[A-Za-z0-9_]*' "$path" 2>/dev/null | sed 's/^func //')
      if [ -n "$names" ]; then
        printf 'plain %s ^(%s)$ test\n' "$pkg" "$(printf '%s\n' "$names" | paste -sd '|' -)" >>"$work/runs"
      else
        printf 'plain %s all test\n' "$pkg" >>"$work/runs"
      fi
      ;;
    text)
      # Text that goldens quote: run its own package, then the tui goldens.
      [ -n "$pkg" ] || continue
      printf 'plain %s all text\n' "$pkg" >>"$work/runs"
      printf 'plain %s %s text\n' "$module/internal/tui" 'Golden|Title|Help|Schema' >>"$work/runs"
      ;;
    *)
      runs_for_rule "$rule" >>"$work/runs"
      ;;
  esac
  printf '%s\t%s\t%s\n' "$path" "$rule" "$pkg" >>"$work/files"
done

if [ -s "$work/files" ]; then
  printf 'test-changed: base %s, %s changed file(s)\n' "$(git rev-parse --short "$base")" "$(wc -l <"$work/files" | tr -d ' ')"
  while IFS="$(printf '\t')" read -r path rule pkg; do
    printf '  %s: %s\n' "$path" "$rule"
  done <"$work/files"
fi

# Sort and drop duplicate commands that two rules both asked for.
sort -u "$work/runs" | awk '!seen[$1 "\t" $2 "\t" $3]++' >"$work/runs.unique"
mv "$work/runs.unique" "$work/runs"

# describe_run prints the command a run line stands for, with ./ packages.
describe_run() {
  mode=$1
  pkg=$2
  pattern=$3
  pkg="./${pkg#"$module/"}"
  line="go test -count=1"
  [ "$mode" = race ] && line="$line -race"
  [ "$pattern" = all ] || line="$line -run '$pattern'"
  printf '%s%s %s\n' "$line" "${extra:+ $extra}" "$pkg"
}

if [ -s "$work/runs" ]; then
  printf 'runs:\n'
  while read -r mode pkg pattern rule; do
    [ -n "${pkg:-}" ] || continue
    printf '  [%s] %s\n' "$rule" "$(describe_run "$mode" "$pkg" "$pattern")"
  done <"$work/runs"
else
  printf 'no tests need to run\n'
fi

# gofmt and go vet on the changed files and packages. A deleted file is not
# checked with gofmt, but its package is still vetted.
: >"$work/gofmt-files"
awk -F "$(printf '\t')" '$1 ~ /\.go$/ { print $1 }' "$work/files" |
  while IFS= read -r file; do
    [ -f "$file" ] && printf '%s\n' "$file"
  done >"$work/gofmt-files"
changed_packages=$(awk -F "$(printf '\t')" '$1 ~ /\.go$/ { print $3 }' "$work/files" | sed '/^$/d' | sort -u)
if [ -s "$work/gofmt-files" ]; then
  printf 'gofmt -l on %s changed Go file(s)\n' "$(wc -l <"$work/gofmt-files" | tr -d ' ')"
  if [ "$plan" = 1 ]; then
    printf '  [gofmt] gofmt -l <changed Go files>\n'
  else
    if bad=$(gofmt -l $(cat "$work/gofmt-files")); then
      if [ -n "$bad" ]; then
        printf 'gofmt: FAIL, these files need formatting:\n%s\n' "$bad"
        checks_failed=1
      else
        printf 'gofmt: PASS\n'
      fi
    else
      printf 'gofmt: FAIL\n'
      checks_failed=1
    fi
  fi
fi
if [ -n "$changed_packages" ]; then
  vet_packages=$(printf '%s\n' "$changed_packages" | sed 's|^|./|')
  printf 'go vet on the changed packages%s\n' "$(printf ' %s' $changed_packages)"
  if [ "$plan" = 1 ]; then
    printf '  [vet] go vet %s\n' "$(printf '%s ' $vet_packages)"
  else
    if go vet $vet_packages >"$work/vet.log" 2>&1; then
      printf 'vet: PASS\n'
    else
      printf 'vet: FAIL\n'
      cat "$work/vet.log"
      checks_failed=1
    fi
  fi
fi
checks_failed=${checks_failed:-0}

if [ ! -s "$work/runs" ]; then
  exit "$checks_failed"
fi

if [ "$plan" = 1 ]; then
  exit 0
fi

# Each run gets its own AGENT_NET_* directories, so no test can touch the real
# setup, and one run cannot see another run's state.
go_test() {
  export AGENT_NET_CONFIG_DIR="$run_root/config"
  export AGENT_NET_STATE_DIR="$run_root/state"
  export AGENT_NET_SOCKET="$run_root/agent-net.sock"
  export AGENT_NET_GATEWAY_DATA="$run_root/gateway"
  export AGENT_NET_CLAUDE_SESSIONS_DIR="$run_root/claude-sessions"
  mkdir -p "$run_root"
  if [ "$have_runner" = 1 ]; then
    agent-net test-run -- go test "$@"
  else
    go test "$@"
  fi
}

failed=0
index=0
while read -r mode pkg pattern rule; do
  [ -n "${pkg:-}" ] || continue
  index=$((index + 1))
  run_root="$work/run-$index"
  set -- -count=1
  [ "$mode" = race ] && set -- "$@" -race
  [ "$pattern" = all ] || set -- "$@" -run "$pattern"
  [ -z "$extra" ] || set -- "$@" $extra
  pkg_arg="./${pkg#"$module/"}"
  set -- "$@" "$pkg_arg"
  start=$(date +%s)
  if go_test "$@" >"$work/output-$index" 2>&1; then
    status=0
  else
    status=1
  fi
  end=$(date +%s)
  pkg_display="./${pkg#"$module/"}"
  if [ "$status" = 0 ]; then
    printf 'PASS [%s] %s (%ss)\n' "$rule" "$pkg_display" "$((end - start))"
    grep -E '^(ok|no test files)|^--- ' "$work/output-$index" | head -5
  else
    failed=1
    printf 'FAIL [%s] %s (%ss)\n' "$rule" "$pkg_display" "$((end - start))"
    cat "$work/output-$index"
  fi
done <"$work/runs"

exit "$((failed + checks_failed))"