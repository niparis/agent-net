#!/bin/sh
# scripts/test-changed.sh: run the tests a diff needs, and nothing more.
#
# Usage: scripts/test-changed.sh [BASE] [--plan] [--run NAMES] [-- go-test-args...]
#
# BASE is the ref to compare against; the default is the merge-base of HEAD and
# main, which is what a worker branch needs. The script lists the changed files,
# matches each one against the rule table below, and runs the tests the first
# matching rule names. --run adds the tests a brief names to a changed
# production file's package. --plan prints the runs and checks without running
# them, and names each package left to CI.
#
# Rule table. Keep it short: one line per rule. Add a line when a new
# cross-package dependency shows up in a failed hand-back.
#
#   migration  internal/gateway/store.go, migrations*.go, *_migration.go,
#              testdata/migrations.lock
#              -> gateway migration, replay, upgrade and cutover tests, with -race
#   test file  any *_test.go -> only the tests whose bodies changed or were added
#   tui        internal/tui/* -> tui and graphview golden, title, help and schema tests
#   settings   internal/settings/*, internal/settingsview/*, any settings*.go
#              -> settings, gateway settings and cached-read, daemon settings tests, with -race
#   text       internal/guidance, agentfiles, setup, mcpserver, toolcatalog, cmd/agent-net
#              -> the changed package plus the tui goldens that quote its text
#   wire       internal/protocol, internal/client
#              -> decode, strict, frame and wire tests of the wire packages
#   comment    a Go file that changes only comments or blank lines -> no test run
#   docs       any other non-Go file -> no test run
#   generated  an input of reference.md (ctoskill) or docs/feature-map.md, on top of
#              the rule above -> TestReferenceMatchesCode, TestFeatureMapMatchesCode
#   (no rule)  -> the tests in the _test.go file of the same base name, plus
#                 the names from --run; no test run when neither exists
#
# A test file gets only the functions whose declarations changed or are new. A
# production file gets its same-name test file only. The full package runs in
# the merge queue's CI, which decision 0164 made the single test gate.
#
# Every diff also runs the contract set: the migrations lock and append-only
# tests, the CTO schema and read-size checks, and the tool catalog text. These
# are small and cross-cutting, and they were the merger's most common pre-merge
# failures when a branch fell behind the default branch.
#
# It also runs gofmt -l on the changed files and go vet on the changed packages,
# and exits non-zero if a run or a check fails.
#
# Unix sockets: a sandbox such as Claude Code's can block them, and then every
# test that starts a daemon on a socket fails with "operation not permitted".
# The script probes once, and when sockets are blocked it sets
# AGENT_NET_NO_UNIX_SOCKETS=1 so those tests skip themselves (internal/testsock),
# and its summary counts them. The merge queue's CI runs them. Outside such a
# sandbox nothing changes. TEST_CHANGED_UNIX_SOCKETS=ok|blocked replaces the
# probe, for scripts/test-changed-test.sh.
#
# The rule checker (tools/rulecheck, its own Go module) checks three rules on the
# changed packages: no test sleeps over 2 seconds (B4), no em dash in production
# strings (B9), and no lock held across a gateway call beyond lockcall.baseline
# (B12). It also checks the Markdown under docs/ when a file there changed. A
# repository without tools/rulecheck has no rule checks. Its own tests run in CI,
# with go -C tools/rulecheck test, so a change there is not run here.
#
# The picker replay (tools/pickerreplay) is also its own Go module. Its tests run
# in CI with go -C tools/pickerreplay test, so a change there is not run here
# either.

set -u

module=github.com/niparis/agent-net

fail() {
  echo "scripts/test-changed.sh: $*" >&2
  exit 2
}

usage() {
  cat <<'EOF'
Usage: scripts/test-changed.sh [BASE] [--plan] [--run NAMES] [-- go-test-args...]

Run the tests a diff needs. BASE defaults to the merge-base of HEAD and main.
The rule table at the top of the script says which runs a changed path needs.
A comment-only or docs change runs no tests; a changed test file runs only the
tests it changed. --run adds the tests a brief names to a production file's
package. --plan prints the runs and the checks without running them, and names
the packages left to CI. Arguments after -- go to go test.
EOF
}

# The helper lives next to this script. Remember its directory before the script
# changes to the repository root, so a test can run the script from another
# repository.
script_dir=$(cd "$(dirname "$0")" && pwd) || fail "cannot find the script directory"

base=
plan=0
run_names=
extra=
while [ $# -gt 0 ]; do
  case $1 in
    --plan|-n)
      plan=1
      shift
      ;;
    --run)
      [ $# -ge 2 ] || fail "--run needs test names"
      shift
      run_names=$1
      [ -n "$run_names" ] || fail "--run needs test names"
      shift
      ;;
    --run=*)
      run_names=${1#--run=}
      [ -n "$run_names" ] || fail "--run needs test names"
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

# A non-login shell may not have go on PATH. Use the usual install directories,
# appended so a go already on PATH (and a test's fake go) still wins.
# TEST_CHANGED_GO_DIRS replaces that list, so the test can run the no-go case on
# a machine that has Go in /usr/local/go.
if ! command -v go >/dev/null 2>&1; then
  home=${HOME:-}
  go_dirs="$home/.local/go/bin /usr/local/go/bin $home/sdk/go*/bin"
  [ -z "${TEST_CHANGED_GO_DIRS:-}" ] || go_dirs=$TEST_CHANGED_GO_DIRS
  found=
  for dir in $go_dirs; do
    if [ -x "$dir/go" ]; then
      found=$dir
      break
    fi
  done
  [ -n "$found" ] || fail "go is not on PATH, and there is no go in \$HOME/.local/go/bin, /usr/local/go/bin or \$HOME/sdk/go*/bin"
  PATH="$PATH:$found"
  export PATH
  printf 'test-changed: go is not on PATH; using %s/go\n' "$found"
fi

# The optional `agent-net test-run` wrapper runs every command under the
# delivery test step when this machine has it.
have_runner=0
if command -v agent-net >/dev/null 2>&1 && agent-net test-run --help >/dev/null 2>&1; then
  have_runner=1
fi

# Go writes build and test binaries to GOTMPDIR, and the tests write their temp
# files to TMPDIR. Both take the RAM folder when the machine has one with room,
# unless the caller set one or bubblewrap is installed (see scripts/gotmpdir.sh).
# They are set before the first go call, and the work folder below goes in TMPDIR.
. "$script_dir/gotmpdir.sh"
if [ -z "${GOTMPDIR:-}" ]; then
  GOTMPDIR=$(gotmp_default) || fail "could not choose the Go temp folder"
  export GOTMPDIR
fi
mkdir -p "$GOTMPDIR" || fail "could not make the Go temp folder $GOTMPDIR"
if [ -z "${TMPDIR:-}" ]; then
  if tmp_dir=$(tmp_default); then
    TMPDIR=$tmp_dir
    export TMPDIR
  fi
fi
work=$(mktemp -d "${TMPDIR:-/tmp}/agent-net-test-changed.XXXXXX") || fail "could not create a temporary directory"
trap 'rm -rf "$work"' EXIT HUP INT TERM

# The diff helper is Go, so it reads Go syntax with the standard library. Build
# it once with the real toolchain. The fake go in scripts/test-changed-test.sh
# is only for the go test calls, so that test names the real Go in
# TEST_CHANGED_GO.
helper_go=${TEST_CHANGED_GO:-go}
printf 'build: scripts/testchanged\n'
( cd "$script_dir/.." && "$helper_go" build -o "$work/testchanged" ./scripts/testchanged ) ||
  fail "could not build scripts/testchanged with $helper_go"
helper=$work/testchanged

# One probe tells whether this sandbox blocks unix sockets. The Go helper makes
# a socket the way the daemon tests do.
unix_sockets=${TEST_CHANGED_UNIX_SOCKETS:-}
case $unix_sockets in
  ok|blocked) ;;
  *)
    if "$helper" unix-socket >/dev/null 2>&1; then
      unix_sockets=ok
    else
      unix_sockets=blocked
    fi
    ;;
esac
if [ "$unix_sockets" = blocked ]; then
  printf 'unix sockets are blocked in this sandbox: tests that need them skip themselves; the merge queue CI runs them (decision 0164)\n'
fi

# base_go_file writes the file's base version to $work/base.go. It returns 1
# when the base does not hold the path, which means the file is new.
base_go_file() {
  rm -f "$work/base.go"
  git cat-file -e "$base:$1" 2>/dev/null || return 1
  git show "$base:$1" >"$work/base.go" 2>/dev/null
}

# generated_input succeeds for a path that the generated docs are built from:
# the CTO setup reference and the feature map read these packages.
generated_input() {
  case $1 in
    internal/toolcatalog/*|internal/guidance/*|internal/mcpserver/*|internal/tui/*|internal/featuremap/*|internal/daemon/ctoskill/*|internal/settings/*|internal/authority/*|internal/protocol/*)
      return 0 ;;
  esac
  return 1
}

# contract_runs prints the small set every diff runs, whatever it touches.
contract_runs() {
  printf 'plain %s %s contract\n' "$module/internal/gateway" '^(TestMigrationsAreAppendOnly|TestMigrationsLockAcceptsAppendedMigration|TestDeliveryEvidenceIsAppendOnly)$'
  printf 'plain %s %s contract\n' "$module/internal/mcpserver" '^(TestCTOSchemaBudgetAndCharterGuide)$'
  printf 'plain %s %s contract\n' "$module/internal/daemon" '^(TestCTORegisteredReadSizes)$'
  printf 'plain %s %s contract\n' "$module/internal/toolcatalog" '^(TestDescriptions|TestOutputSchemasCoverProtocolTypes)$'
}

# rule_of maps a changed path to the rule that knows what it needs.
rule_of() {
  case $1 in
    tools/rulecheck/*)
      echo tool ;;
    tools/pickerreplay/*)
      echo replaytool ;;
    internal/gateway/store.go|internal/gateway/migrations*.go|internal/gateway/*_migration.go|internal/gateway/testdata/migrations.lock)
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
    *.go)
      echo package ;;
    *)
      echo docs ;;
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
: >"$work/skips"

printf '%s\n' "$changed" | while IFS= read -r path; do
  [ -n "$path" ] || continue
  rule=$(rule_of "$path")
  pkg=$(package_of "$path")

  # A comment or blank-line change needs no test run. gofmt and go vet still
  # see the file below, because it stays in $work/files.
  case $path in
    *.go)
      if [ -f "$path" ] && base_go_file "$path" &&
        "$helper" comments-only "$work/base.go" "$path"; then
        printf '%s\tcomment\t%s\n' "$path" "$pkg" >>"$work/files"
        printf '%s\tcomment-only, no test run (full package in CI)\n' "$path" >>"$work/skips"
        continue
      fi
      ;;
  esac

  if generated_input "$path"; then
    printf 'plain %s %s generated\n' "$module/internal/daemon/ctoskill" '^(TestReferenceMatchesCode)$' >>"$work/runs"
    printf 'plain %s %s generated\n' "$module/internal/featuremap" '^(TestFeatureMapMatchesCode)$' >>"$work/runs"
  fi

  case $rule in
    package)
      [ -n "$pkg" ] || continue
      names=
      sibling="${path%.go}_test.go"
      if [ -f "$sibling" ]; then
        if ! names=$("$helper" names "$sibling" 2>>"$work/helper.log"); then
          names=all
        fi
      fi
      if [ "$names" != all ] && [ -n "$run_names" ]; then
        named=$(printf '%s' "$run_names" | tr ',' '\n')
        names=$(printf '%s\n%s\n' "$names" "$named" | sed '/^$/d' | sort -u)
      fi
      if [ "$names" = all ]; then
        printf 'plain %s all package\n' "$pkg" >>"$work/runs"
      elif [ -n "$names" ]; then
        printf 'plain %s ^(%s)$ package\n' "$pkg" "$(printf '%s\n' "$names" | paste -sd '|' -)" >>"$work/runs"
      else
        printf '%s\tno matching test file, no test run (full package in CI)\n' "$path" >>"$work/skips"
      fi
      ;;
    test)
      [ -f "$path" ] || continue
      [ -n "$pkg" ] || continue
      base_go_file "$path" || rm -f "$work/base.go"
      names=
      if ! names=$("$helper" changed-tests "$work/base.go" "$path" 2>>"$work/helper.log"); then
        # A file the helper cannot parse runs all its tests, never none.
        names=all
      fi
      if [ "$names" = all ]; then
        printf 'plain %s all test\n' "$pkg" >>"$work/runs"
      elif [ -n "$names" ]; then
        printf 'plain %s ^(%s)$ test\n' "$pkg" "$(printf '%s\n' "$names" | paste -sd '|' -)" >>"$work/runs"
      else
        printf '%s\ttest file, no changed tests (full package in CI)\n' "$path" >>"$work/skips"
      fi
      ;;
    docs)
      printf '%s\tdocs, no test run (full package in CI)\n' "$path" >>"$work/skips"
      ;;
    tool)
      printf '%s\trule checker module, its tests run in CI (go -C tools/rulecheck test)\n' "$path" >>"$work/skips"
      ;;
    replaytool)
      printf '%s\tpicker replay module, its tests run in CI (go -C tools/pickerreplay test)\n' "$path" >>"$work/skips"
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

contract_runs >>"$work/runs"

if [ -s "$work/files" ]; then
  printf 'test-changed: base %s, %s changed file(s)\n' "$(git rev-parse --short "$base")" "$(wc -l <"$work/files" | tr -d ' ')"
  while IFS="$(printf '\t')" read -r path rule pkg; do
    printf '  %s: %s\n' "$path" "$rule"
  done <"$work/files"
fi

# Sort and drop duplicate commands that two rules both asked for.
sort -u "$work/runs" | awk '!seen[$1 "\t" $2 "\t" $3]++' >"$work/runs.unique"
mv "$work/runs.unique" "$work/runs"

# go test gives each package binary 10 minutes by default. A slow package on a
# busy machine then fails with a timeout panic and no failed assertion. The
# daemon kills the whole check at delivery.handback_check_timeout (default 30
# minutes), so every run gets what is left of test_budget. The runs together
# stay inside that limit, and go test names the test that was running.
test_budget=1500

# test_timeout prints the -timeout for the next run: the time left of the budget,
# and at least one second.
test_timeout() {
  left=$((deadline - $(date +%s)))
  [ "$left" -ge 1 ] || left=1
  printf '%ss\n' "$left"
}
deadline=$(($(date +%s) + test_budget))

# describe_run prints the command a run line stands for, with ./ packages.
describe_run() {
  mode=$1
  pkg=$2
  pattern=$3
  pkg="./${pkg#"$module/"}"
  line="go test -count=1"
  [ "$mode" = race ] && line="$line -race"
  [ "$pattern" = all ] || line="$line -run '$pattern'"
  line="$line -timeout $(test_timeout)"
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

# The plan names what did not run here, so a worker sees the packages the merge
# queue's CI will cover in full.
if [ "$plan" = 1 ] && [ -s "$work/skips" ]; then
  printf 'not run here (full package in CI):\n'
  while IFS="$(printf '\t')" read -r path reason; do
    printf '  %s: %s\n' "$path" "$reason"
  done <"$work/skips"
fi

# gofmt and go vet on the changed files and packages. A deleted file is not
# checked with gofmt, but its package is still vetted.
: >"$work/gofmt-files"
awk -F "$(printf '\t')" '$1 ~ /\.go$/ { print $1 }' "$work/files" |
  while IFS= read -r file; do
    [ -f "$file" ] && printf '%s\n' "$file"
  done >"$work/gofmt-files"
changed_packages=$(awk -F "$(printf '\t')" '$1 ~ /\.go$/ && $3 !~ /^tools\/(rulecheck|pickerreplay)(\/|$)/ { print $3 }' "$work/files" | sed '/^$/d' | sort -u)
if [ -s "$work/gofmt-files" ]; then
  printf 'gofmt -l on %s changed Go file(s)\n' "$(wc -l <"$work/gofmt-files" | tr -d ' ')"
  if [ "$plan" = 1 ]; then
    printf '  [gofmt] gofmt -l <changed Go files>\n'
  else
    t0=$(date +%s)
    if bad=$(gofmt -l $(cat "$work/gofmt-files")); then
      if [ -n "$bad" ]; then
        printf 'gofmt: FAIL, these files need formatting:\n%s\n' "$bad"
        checks_failed=1
      else
        printf 'gofmt: PASS (%ss)\n' "$(($(date +%s) - t0))"
      fi
    else
      printf 'gofmt: FAIL (%ss)\n' "$(($(date +%s) - t0))"
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
    t0=$(date +%s)
    if go vet $vet_packages >"$work/vet.log" 2>&1; then
      printf 'vet: PASS (%ss)\n' "$(($(date +%s) - t0))"
    else
      printf 'vet: FAIL (%ss)\n' "$(($(date +%s) - t0))"
      cat "$work/vet.log"
      checks_failed=1
    fi
  fi
fi
# The rule checks run on the changed packages, and on the docs when a Markdown
# file under docs/ changed. Comment-only Go changes cannot break those rules, so
# they are not in the list. The checker is built with the real Go, then run from
# the repository root, where its patterns and the lock baseline are relative.
rule_list=$(awk -F "$(printf '\t')" '$1 ~ /\.go$/ && $2 != "comment" && $3 != "" && $3 !~ /^tools\/(rulecheck|pickerreplay)(\/|$)/ { print "./" $3 }' "$work/files" | sort -u | paste -sd ' ' -)
rule_docs=
if awk -F "$(printf '\t')" '$1 ~ /^docs\/.*\.md$/ { found = 1 } END { exit !found }' "$work/files"; then
  rule_docs=docs
fi
if [ -f "$root/tools/rulecheck/go.mod" ] && { [ -n "$rule_list" ] || [ -n "$rule_docs" ]; }; then
  if [ "$plan" = 1 ]; then
    printf 'rule checks: rulecheck%s%s\n' "${rule_docs:+ -docs $rule_docs}" "${rule_list:+ $rule_list}"
  else
    rule_bin=$work/rulecheck
    if ! (cd "$root/tools/rulecheck" && "$helper_go" build -o "$rule_bin" .) >"$work/rulecheck-build.log" 2>&1; then
      cat "$work/rulecheck-build.log"
      fail "could not build tools/rulecheck with $helper_go"
    fi
    printf 'rule checks: rulecheck%s%s\n' "${rule_docs:+ -docs $rule_docs}" "${rule_list:+ $rule_list}"
    t0=$(date +%s)
    if (cd "$root" && if [ -n "$rule_docs" ]; then "$rule_bin" -docs "$rule_docs" $rule_list; else "$rule_bin" $rule_list; fi) >"$work/rules.log" 2>&1; then
      rules_status=PASS
    else
      rules_status=FAIL
      checks_failed=1
    fi
    cat "$work/rules.log"
    printf 'rule checks: %s (%ss)\n' "$rules_status" "$(($(date +%s) - t0))"
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
  if [ "$unix_sockets" = blocked ]; then
    export AGENT_NET_NO_UNIX_SOCKETS=1
    export AGENT_NET_NO_UNIX_SOCKETS_LOG="$run_root/skipped-sockets"
  fi
  if [ "$have_runner" = 1 ]; then
    agent-net test-run -- go test "$@"
  else
    go test "$@"
  fi
}

failed=0
index=0
total=$(grep -c . "$work/runs")
: >"$work/failed"
while read -r mode pkg pattern rule; do
  [ -n "${pkg:-}" ] || continue
  index=$((index + 1))
  run_root="$work/run-$index"
  set -- -count=1
  [ "$mode" = race ] && set -- "$@" -race
  [ "$pattern" = all ] || set -- "$@" -run "$pattern"
  set -- "$@" -timeout "$(test_timeout)"
  [ -z "$extra" ] || set -- "$@" $extra
  pkg_arg="./${pkg#"$module/"}"
  set -- "$@" "$pkg_arg"
  printf 'running %s/%s [%s] %s\n' "$index" "$total" "$rule" "$(describe_run "$mode" "$pkg" "$pattern")"
  mkdir -p "$run_root"
  printf '%s\n' "$pkg_arg" >"$run_root/pkg"
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
    # go test lists the tests that were running when it timed out. Keep that
    # line and the failed test names for the summary.
    reason=failed
    if grep -q '^panic: test timed out' "$work/output-$index"; then
      reason=$(awk '
        /^panic: test timed out/ { sub(/^panic: /, ""); msg = $0 }
        /^[[:space:]]*running tests:/ { running = 1; next }
        running && /^[[:space:]]*$/ { running = 0 }
        running { sub(/^[[:space:]]+/, ""); names = names (names ? ", " : "") $0 }
        END { printf "%s; running %s", msg, names }' "$work/output-$index")
    fi
    printf '%s\t%s\t%s\n' "$pkg_display" "$rule" "$reason" >>"$work/failed"
  fi
done <"$work/runs"

# The skipped socket tests, counted from the log each run's tests wrote. This
# line comes before the failed runs, which stay the last thing printed.
if [ "$unix_sockets" = blocked ]; then
  : >"$work/skipped-sockets"
  for log in "$work"/run-*/skipped-sockets; do
    [ -f "$log" ] || continue
    sed "s|^|$(cat "$(dirname "$log")/pkg") |" "$log" >>"$work/skipped-sockets"
  done
  sort -u "$work/skipped-sockets" -o "$work/skipped-sockets"
  printf 'unix sockets are blocked in this sandbox: %s socket tests skipped; the merge queue CI runs them (decision 0164)\n' "$(grep -c . "$work/skipped-sockets" | tr -d ' ')"
  sed 's/^/  skipped: /' "$work/skipped-sockets"
fi

# The summary is the last thing printed, so the tail of the output that the
# daemon keeps still names the failed runs.
if [ -s "$work/failed" ]; then
  printf 'test-changed: failed runs:\n'
  while IFS="$(printf '\t')" read -r pkg_display rule reason; do
    printf '  FAIL [%s] %s: %s\n' "$rule" "$pkg_display" "$reason"
  done <"$work/failed"
  # A generated doc that is out of date fails its test; the fix is to regenerate.
  if awk -F "$(printf '\t')" '$2 == "generated" { found = 1 } END { exit !found }' "$work/failed"; then
    printf 'test-changed: a generated doc is out of date; run: go generate ./internal/daemon/ctoskill ./internal/featuremap\n'
  fi
fi

exit "$((failed + checks_failed))"