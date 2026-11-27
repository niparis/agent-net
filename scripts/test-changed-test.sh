#!/bin/sh
# scripts/test-changed-test.sh: check how scripts/test-changed.sh calls go test.
#
# A fake go on a minimal PATH records its arguments and can print a timeout
# panic, so no real test runs and the real agent-net is not used. The helper
# that reads the diff is built with the real Go, named by TEST_CHANGED_GO.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
script=$root/scripts/test-changed.sh
work=$(mktemp -d "${TMPDIR:-/tmp}/test-changed-test.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM

fail() {
  echo "test-changed-test: FAIL: $*" >&2
  exit 1
}

real_go=$(command -v go) || fail "go is not on PATH"
gocache=$("$real_go" env GOCACHE) || fail "go env GOCACHE failed"

# no_go_dir holds links to the tools the script calls, and no go. A CI runner
# keeps go in /usr/bin, which a PATH that lists /usr/bin would find, so the
# no-go cases below run with this PATH and never with /usr/bin or /bin.
no_go_dir=$work/nogo
mkdir "$no_go_dir"
for tool in sh env git sed sort awk mktemp date cat rm dirname wc tr grep ls paste head cut mv mkdir; do
  tool_path=$(command -v "$tool") || fail "cannot link $tool into the no-go directory"
  ln -s "$tool_path" "$no_go_dir/$tool"
done
[ -e "$no_go_dir/go" ] && fail "the no-go directory holds go"

mkdir "$work/bin"
cat >"$work/bin/go" <<'GO'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_GO_LOG"
if [ "$1" = test ]; then
  printf '%s\n' "${AGENT_NET_NO_UNIX_SOCKETS:-unset}" >>"${FAKE_GO_ENV_LOG:-/dev/null}"
  # A test that needs a unix socket logs its name when it skips itself.
  for name in ${FAKE_GO_SKIP:-}; do
    [ -z "${AGENT_NET_NO_UNIX_SOCKETS_LOG:-}" ] || printf '%s\n' "$name" >>"$AGENT_NET_NO_UNIX_SOCKETS_LOG"
  done
fi
[ -z "${FAKE_GO_TMP_LOG:-}" ] || printf 'TMPDIR=%s GOTMPDIR=%s\n' "${TMPDIR-unset}" "${GOTMPDIR-unset}" >>"$FAKE_GO_TMP_LOG"
if [ "$1" = test ] && [ -n "${FAKE_GO_PANIC:-}" ]; then
  cat <<'OUT'
panic: test timed out after 25m0s
	running tests:
		TestSlow (25m0s)

goroutine 1 [running]:
testing.tRunner(...)
OUT
  exit 1
fi
exit 0
GO
chmod +x "$work/bin/go"
cat >"$work/bin/gofmt" <<'GOFMT'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_GOFMT_LOG"
exit 0
GOFMT
chmod +x "$work/bin/gofmt"

repo=$work/repo
mkdir -p "$repo/internal/fake"
cd "$repo"
git init -q
# Commit on a branch of its own, so a case can create a local main that is
# behind origin/main.
git checkout -q -b worker
cat >internal/fake/fake.go <<'EOF'
package fake

// Value is the value.
func Value() int { return 1 }
EOF
mkdir -p internal/gateway internal/guidance internal/toolcatalog internal/daemon/ctoskill internal/featuremap
printf 'package gateway\n\nfunc Unrelated() int { return 1 }\n' >internal/gateway/unrelated.go
printf 'package gateway\n\nfunc Migration() int { return 1 }\n' >internal/gateway/project_identity_migration.go
cat >internal/gateway/store.go <<'EOF'
package gateway

// migrations are applied in order.
var migrations = []string{`
CREATE TABLE demo (id TEXT);
`, demoMigration}

const demoMigration = `
CREATE TABLE demo_two (id TEXT);
`
EOF
cat >internal/gateway/store_test.go <<'EOF'
package gateway

import "testing"

func TestStoreValue(t *testing.T) {
	if Unrelated() != 1 {
		t.Fatal("store")
	}
}
EOF
printf 'package guidance\n\nfunc Text() int { return 1 }\n' >internal/guidance/guidance.go
printf 'package toolcatalog\n\nfunc Tool() int { return 1 }\n' >internal/toolcatalog/tool.go
printf 'package ctoskill\n\nfunc Reference() int { return 1 }\n' >internal/daemon/ctoskill/reference.go
printf 'package featuremap\n\nfunc Map() int { return 1 }\n' >internal/featuremap/featuremap.go
cat >internal/fake/fake_test.go <<'EOF'
package fake

import "testing"

func TestAlpha(t *testing.T) {
	if Value() != 1 {
		t.Fatal("alpha")
	}
}

func TestBeta(t *testing.T) {
	if Value() != 1 {
		t.Fatal("beta")
	}
}
EOF
cat >internal/fake/other.go <<'EOF'
package fake

// Other has no matching test file.
func Other() int { return 2 }
EOF
printf 'first\n' >internal/fake/notes.txt
# A stand-in rule checker. It prints its arguments, so a run shows which
# packages and docs it was given.
mkdir -p tools/rulecheck
cat >tools/rulecheck/go.mod <<'EOF'
module example.invalid/rulecheck

go 1.21
EOF
cat >tools/rulecheck/main.go <<'EOF'
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Println("rulecheck args: " + strings.Join(os.Args[1:], " "))
	fmt.Println("rulecheck: PASS")
}
EOF
git add . && git -c user.name=test -c user.email=test@example.invalid commit -q -m base

# reset_tree drops the previous case, so each case starts from the base commit.
reset_tree() {
  git checkout -q -- .
  git clean -qfd
}

# change_production changes a file that has a matching fake_test.go.
change_production() {
  cat >internal/fake/fake.go <<'EOF'
package fake

// Value is the value.
func Value() int { return 3 }
EOF
}

# run_check runs the script against HEAD, with the given extra arguments.
# Its output goes to $work/out and its go test calls to $work/go.log.
run_check() {
  : >"$work/go.log"
  : >"$work/gofmt.log"
  status=0
  PATH="$work/bin:/usr/bin:/bin" FAKE_GO_LOG="$work/go.log" \
    FAKE_GOFMT_LOG="$work/gofmt.log" TEST_CHANGED_GO="$real_go" \
    sh "$script" HEAD "$@" >"$work/out" 2>&1 || status=$?
}

# no_fake_package_test fails when a go test call ran the fake package.
no_fake_package_test() {
  if grep -q '^test .*\./internal/fake' "$work/go.log"; then
    fail "$1: ran a fake package test: $(cat "$work/go.log")"
  fi
}

# A production file with a matching test file runs the tests that file
# declares, and a run gets a timeout within the hand-back check budget, so go
# test names a slow test before the daemon kills the whole check.
reset_tree
change_production
run_check
line=$(grep -F 'test -count=1 -run ^(TestAlpha|TestBeta)$ -timeout ' "$work/go.log" | head -n 1)
[ -n "$line" ] || fail "a production file did not run its matching test file: $(cat "$work/out") $(cat "$work/go.log")"
secs=$(printf '%s\n' "$line" | sed -n 's/.*-timeout \([0-9]*\)s \.\/internal\/fake$/\1/p')
[ -n "$secs" ] || fail "go test has no -timeout: $line"
[ "$secs" -ge 1400 ] && [ "$secs" -le 1500 ] || fail "go test -timeout ${secs}s is not within the check budget"
[ "$status" = 0 ] || fail "a passing run exited $status"

# The contract set runs for every diff, even one that matches no contract
# file. These are the cheap tests the merge queue's gate keeps failing on when
# a branch is behind: the migrations lock, the CTO schemas and the tool text.
grep -Fq 'test -count=1 -run ^(TestMigrationsAreAppendOnly|TestMigrationsLockAcceptsAppendedMigration|TestDeliveryEvidenceIsAppendOnly)$' "$work/go.log" ||
  fail "the contract set did not run the migrations lock tests: $(cat "$work/go.log")"
grep -Fq 'test -count=1 -run ^(TestCTOSchemaBudgetAndCharterGuide)$' "$work/go.log" ||
  fail "the contract set did not run the CTO schema budget test: $(cat "$work/go.log")"
grep -Fq 'test -count=1 -run ^(TestDescriptions|TestOutputSchemasCoverProtocolTypes)$' "$work/go.log" ||
  fail "the contract set did not run the tool catalog tests: $(cat "$work/go.log")"

# The rule checker runs on the changed package, with the package as its
# argument, and passes the changed docs with -docs.
reset_tree
change_production
run_check
grep -Fq 'rulecheck args: ./internal/fake' "$work/out" ||
  fail "the rule checker did not run on the changed package: $(cat "$work/out")"
[ "$status" = 0 ] || fail "a passing rule check exited $status"

reset_tree
mkdir -p docs
printf 'A plain line.\n' >docs/guide.md
run_check
grep -Fq 'rulecheck args: -docs docs' "$work/out" ||
  fail "the rule checker did not check the changed docs: $(cat "$work/out")"

# --plan lists the contract set, so a worker can say what will run.
run_plan() {
  status=0
  PATH="$work/bin:/usr/bin:/bin" FAKE_GO_LOG="$work/go.log" \
    FAKE_GOFMT_LOG="$work/gofmt.log" TEST_CHANGED_GO="$real_go" \
    sh "$script" HEAD --plan >"$work/plan" 2>&1 || status=$?
}
run_plan
[ "$status" = 0 ] || fail "--plan exited $status: $(cat "$work/plan")"
grep -q '\[contract\].*TestMigrationsAreAppendOnly' "$work/plan" ||
  fail "--plan does not list the contract set: $(cat "$work/plan")"

# Arguments after -- go to go test after the script's own -timeout, so they win.
reset_tree
change_production
run_check -- -timeout 5m
grep -Fq 'test -count=1 -run ^(TestAlpha|TestBeta)$ -timeout ' "$work/go.log" ||
  fail "the arguments after -- lost the test filter: $(cat "$work/go.log")"
grep -Fq -- '-timeout 5m ./internal/fake' "$work/go.log" ||
  fail "arguments after -- do not override the timeout: $(cat "$work/go.log")"

# A timed-out run fails the check, and the last line of the output names the
# test that was running, because the daemon keeps only the tail of the output.
FAKE_GO_PANIC=1 run_check
[ "$status" = 1 ] || fail "a timed-out run exited $status, want 1"
last=$(tail -n 1 "$work/out")
case $last in
  *TestSlow*) ;;
  *) fail "last output line does not name the running test: $last" ;;
esac

# A comment-only change runs no tests, but gofmt and go vet still run, and the
# plan says the full package runs in CI.
reset_tree
cat >internal/fake/fake.go <<'EOF'
package fake

// Value is the value, now with a longer comment.

func Value() int { return 1 }
EOF
run_check
[ "$status" = 0 ] || fail "a comment-only run exited $status: $(cat "$work/out")"
no_fake_package_test "comment-only"
grep -q '^vet .*\./internal/fake' "$work/go.log" ||
  fail "a comment-only change did not run go vet: $(cat "$work/go.log")"
grep -q 'internal/fake/fake.go' "$work/gofmt.log" ||
  fail "a comment-only change did not run gofmt: $(cat "$work/gofmt.log")"
run_plan
grep -q 'internal/fake/fake.go: comment-only.*full package in CI' "$work/plan" ||
  fail "--plan does not say a comment-only file leaves the full package to CI: $(cat "$work/plan")"

# A changed or added test function runs only that function, not the rest of
# the file or the package.
reset_tree
cat >internal/fake/fake_test.go <<'EOF'
package fake

import "testing"

func TestAlpha(t *testing.T) {
	if Value() != 2 {
		t.Fatal("alpha changed")
	}
}

func TestBeta(t *testing.T) {
	if Value() != 1 {
		t.Fatal("beta")
	}
}

func TestGamma(t *testing.T) {
	if Value() != 1 {
		t.Fatal("gamma added")
	}
}
EOF
run_check
[ "$status" = 0 ] || fail "a test-only run exited $status: $(cat "$work/out")"
grep -Fq 'test -count=1 -run ^(TestAlpha|TestGamma)$ -timeout ' "$work/go.log" ||
  fail "a test-only diff did not run just the changed and added tests: $(cat "$work/go.log")"
if grep -q 'TestBeta' "$work/go.log"; then
  fail "a test-only diff ran the unchanged test: $(cat "$work/go.log")"
fi

# A production file without a same-name test file gets no package tests, and
# the plan points at CI.
reset_tree
cat >internal/fake/other.go <<'EOF'
package fake

// Other has no matching test file.
func Other() int { return 4 }
EOF
run_check
[ "$status" = 0 ] || fail "a production-only run exited $status: $(cat "$work/out")"
no_fake_package_test "production without a matching test file"
run_plan
grep -q 'internal/fake/other.go: no matching test file.*full package in CI' "$work/plan" ||
  fail "--plan does not point a production file without tests at CI: $(cat "$work/plan")"

# A docs change runs no package tests, and the plan points at CI.
reset_tree
printf 'second\n' >internal/fake/notes.txt
run_check
[ "$status" = 0 ] || fail "a docs-only run exited $status: $(cat "$work/out")"
no_fake_package_test "docs-only"
run_plan
grep -q 'internal/fake/notes.txt: docs.*full package in CI' "$work/plan" ||
  fail "--plan does not point a docs change at CI: $(cat "$work/plan")"

# --run adds the tests a brief names to the production file's package, next to
# the tests in its matching test file.
reset_tree
change_production
run_check --run 'TestExtra,TestMore'
grep -Fq 'test -count=1 -run ^(TestAlpha|TestBeta|TestExtra|TestMore)$ -timeout ' "$work/go.log" ||
  fail "--run did not add the named tests: $(cat "$work/go.log")"

# A nested replay module must not be passed to root-module checks.
reset_tree
mkdir -p tools/pickerreplay
printf 'package main\n' >tools/pickerreplay/main.go
run_check
if grep -q 'vet .*tools/pickerreplay' "$work/go.log" || grep -q 'rulecheck args:.*tools/pickerreplay' "$work/out"; then
  fail "nested replay module was passed to a root-module check"
fi

# run_line prints the go test line that ran the named -run pattern, if any.
run_line() {
  grep -F -- "-run $1 " "$work/go.log" | head -n 1
}

# Each run prints a line when it starts, with its index, package and pattern,
# and an ok or FAIL line with its seconds when it ends, so a long run shows
# what it is doing.
reset_tree
change_production
run_check
start_line=$(grep -n '^\[[0-9]*/[0-9]*\] \./internal/fake ' "$work/out" | head -n 1 | cut -d: -f1)
done_line=$(grep -n '^ok \./internal/fake ([0-9]*s)' "$work/out" | head -n 1 | cut -d: -f1)
[ -n "$start_line" ] && [ -n "$done_line" ] && [ "$start_line" -lt "$done_line" ] ||
  fail "no progress line before the run's ok line: $(cat "$work/out")"

# A file that is not a migration file runs no race migration set, even when it
# sits in the gateway package. The migration set is slow, so a wrong match costs
# minutes.
reset_tree
printf 'package gateway\n\nfunc Unrelated() int { return 2 }\n' >internal/gateway/unrelated.go
run_check
[ "$status" = 0 ] || fail "an unrelated gateway change exited $status: $(cat "$work/out")"
if grep -q -- '-race' "$work/go.log"; then
  fail "an unrelated gateway file ran the race migration set: $(cat "$work/go.log")"
fi

# A gateway file that holds migrations runs the migration set, without -race.
reset_tree
printf 'package gateway\n\nfunc Migration() int { return 2 }\n' >internal/gateway/project_identity_migration.go
run_check
[ "$status" = 0 ] || fail "a migration change exited $status: $(cat "$work/out")"
grep -Fq -- '-run Migrat|Replay|Upgrade|Cutover' "$work/go.log" ||
  fail "a migration file did not run the migration set: $(cat "$work/go.log")"
if grep -q -- '-race' "$work/go.log"; then
  fail "the migration set ran with -race: $(cat "$work/go.log")"
fi

# Most gateway changes touch store.go. A change outside its migrations list and
# migration consts runs its same-name test file, not the slow migration set.
reset_tree
cat >internal/gateway/store.go <<'EOF'
package gateway

// migrations are applied in order.
var migrations = []string{`
CREATE TABLE demo (id TEXT);
`, demoMigration}

const demoMigration = `
CREATE TABLE demo_two (id TEXT);
`

func StoreValue() int { return 2 }
EOF
run_check
[ "$status" = 0 ] || fail "a plain store.go change exited $status: $(cat "$work/out")"
grep -Fq -- '-run ^(TestStoreValue)$' "$work/go.log" ||
  fail "a plain store.go change did not run its same-name test file: $(cat "$work/go.log")"
if grep -q 'Migrat|Replay|Upgrade|Cutover' "$work/go.log"; then
  fail "a plain store.go change ran the migration set: $(cat "$work/go.log")"
fi
if grep -q -- '-race' "$work/go.log"; then
  fail "a plain store.go change ran with -race: $(cat "$work/go.log")"
fi

# Changing a const whose name ends in Migration is a migration change.
reset_tree
cat >internal/gateway/store.go <<'EOF'
package gateway

// migrations are applied in order.
var migrations = []string{`
CREATE TABLE demo (id TEXT);
`, demoMigration}

const demoMigration = `
CREATE TABLE demo_two (id TEXT, name TEXT);
`
EOF
run_check
[ "$status" = 0 ] || fail "a migration const change exited $status: $(cat "$work/out")"
grep -Fq -- '-run Migrat|Replay|Upgrade|Cutover' "$work/go.log" ||
  fail "a migration const change did not run the migration set: $(cat "$work/go.log")"
if grep -q -- '-race' "$work/go.log"; then
  fail "a migration const change ran with -race: $(cat "$work/go.log")"
fi

# A change inside the migrations list is a migration change too.
reset_tree
cat >internal/gateway/store.go <<'EOF'
package gateway

// migrations are applied in order.
var migrations = []string{`
CREATE TABLE demo (id TEXT, name TEXT);
`, demoMigration}

const demoMigration = `
CREATE TABLE demo_two (id TEXT);
`
EOF
run_check
[ "$status" = 0 ] || fail "a migrations list change exited $status: $(cat "$work/out")"
grep -Fq -- '-run Migrat|Replay|Upgrade|Cutover' "$work/go.log" ||
  fail "a migrations list change did not run the migration set: $(cat "$work/go.log")"

# The settings group is a plain run too.
reset_tree
mkdir -p internal/settings
printf 'package settings\n\nfunc Setting() int { return 2 }\n' >internal/settings/settings.go
run_check
[ "$status" = 0 ] || fail "a settings change exited $status: $(cat "$work/out")"
grep -Fq ' -timeout ' "$work/go.log" && grep -Fq './internal/settings' "$work/go.log" ||
  fail "a settings change did not run the settings package: $(cat "$work/go.log")"
if grep -q -- '-race' "$work/go.log"; then
  fail "the settings group ran with -race: $(cat "$work/go.log")"
fi

# An untracked new test file is part of the diff, so its tests run.
reset_tree
cat >internal/fake/extra_test.go <<'EOF'
package fake

import "testing"

func TestNew(t *testing.T) {
	if Value() != 1 {
		t.Fatal("new")
	}
}
EOF
run_check
grep -Fq -- '-run ^(TestNew)$' "$work/go.log" ||
  fail "an untracked new test file did not run its tests: $(cat "$work/go.log")"

# A change to tool, guidance or catalog text also runs the two generated-doc
# checks, so a stale reference.md or feature map fails here and not in CI.
reset_tree
printf 'package toolcatalog\n\nfunc Tool() int { return 2 }\n' >internal/toolcatalog/tool.go
run_check
line=$(run_line '^(TestReferenceMatchesCode)$')
case $line in
  *' ./internal/daemon/ctoskill') ;;
  *) fail "a tool text change did not run TestReferenceMatchesCode: $(cat "$work/go.log")" ;;
esac
line=$(run_line '^(TestFeatureMapMatchesCode)$')
case $line in
  *' ./internal/featuremap') ;;
  *) fail "a tool text change did not run TestFeatureMapMatchesCode: $(cat "$work/go.log")" ;;
esac

# A change that is not an input of the generated docs runs neither check.
reset_tree
change_production
run_check
if grep -q 'TestReferenceMatchesCode\|TestFeatureMapMatchesCode' "$work/go.log"; then
  fail "a change outside the generated-doc inputs ran a generated-doc check: $(cat "$work/go.log")"
fi

# When a generated-doc check fails, the output names the command that fixes it.
reset_tree
printf 'package toolcatalog\n\nfunc Tool() int { return 3 }\n' >internal/toolcatalog/tool.go
FAKE_GO_PANIC=1 run_check
[ "$status" = 1 ] || fail "a failing generated-doc check exited $status, want 1"
grep -Fq 'go generate ./internal/daemon/ctoskill ./internal/featuremap' "$work/out" ||
  fail "a failing generated-doc check does not name the fix: $(cat "$work/out")"

# When go is not on PATH, the script uses the usual install directory, and says
# so. The fake go and gofmt sit there, so the run finds them.
reset_tree
change_production
home=$work/home-fallback
: >"$work/go.log"
mkdir -p "$home/.local/go/bin" "$work/env-tmp"
cp "$work/bin/go" "$work/bin/gofmt" "$home/.local/go/bin/"
status=0
env -i TMPDIR="$work/env-tmp" HOME="$home" GOCACHE="$gocache" PATH="$no_go_dir" FAKE_GO_LOG="$work/go.log" \
  FAKE_GOFMT_LOG="$work/gofmt.log" TEST_CHANGED_GO="$real_go" \
  sh "$script" HEAD >"$work/out" 2>&1 || status=$?
[ "$status" = 0 ] || fail "a run without go on PATH exited $status: $(cat "$work/out")"
grep -Fq 'go is not on PATH' "$work/out" ||
  fail "the script did not say it found go in the install directory: $(cat "$work/out")"
grep -Fq 'test -count=1 -run ^(TestAlpha|TestBeta)$ -timeout ' "$work/go.log" ||
  fail "the fallback go did not run the package tests: $(cat "$work/go.log")"

# With no go in PATH or in the install directories, the script fails at once
# with a clear line. The install directories are replaced by a path that does
# not exist, so a host with Go in /usr/local/go still gets this result.
reset_tree
change_production
mkdir -p "$work/empty-home" "$work/env-tmp"
status=0
env -i TMPDIR="$work/env-tmp" HOME="$work/empty-home" PATH="$no_go_dir" TEST_CHANGED_GO="$real_go" \
  TEST_CHANGED_GO_DIRS="$work/no-install-dir" \
  sh "$script" HEAD >"$work/out" 2>&1 || status=$?
[ "$status" = 2 ] || fail "a run with no go exited $status, want 2: $(cat "$work/out")"
grep -Fq 'go is not on PATH' "$work/out" ||
  fail "a run with no go does not say so: $(cat "$work/out")"

# Two runs at once each use their own temporary directory and remove it, so
# they share no files and leave nothing behind.
reset_tree
change_production
mkdir -p "$work/tmp"
for n in 1 2; do
  (TMPDIR="$work/tmp" PATH="$work/bin:/usr/bin:/bin" FAKE_GO_LOG="$work/go.log" \
    FAKE_GOFMT_LOG="$work/gofmt.log" TEST_CHANGED_GO="$real_go" \
    sh "$script" HEAD >"$work/par-$n" 2>&1; echo $? >"$work/par-$n.status") &
done
wait
for n in 1 2; do
  [ "$(cat "$work/par-$n.status")" = 0 ] || fail "parallel run $n failed: $(cat "$work/par-$n")"
done
leftover=$(ls -A "$work/tmp")
[ -z "$leftover" ] || fail "runs left temporary directories behind: $leftover"

# Unix sockets. When the probe says they are blocked, the script sets the skip
# variable for go test, counts the tests that skipped, and still exits 0.
# TEST_CHANGED_UNIX_SOCKETS stands in for the probe, so this runs the same
# inside and outside a sandbox.
reset_tree
change_production
: >"$work/env.log"
FAKE_GO_ENV_LOG="$work/env.log" TEST_CHANGED_UNIX_SOCKETS=ok run_check
[ "$status" = 0 ] || fail "sockets ok: exited $status: $(cat "$work/out")"
grep -q 'unix sockets are blocked' "$work/out" && fail "sockets ok: the script reported blocked sockets"
grep -qx unset "$work/env.log" || fail "sockets ok: go test saw the skip variable: $(cat "$work/env.log")"
grep -qx 1 "$work/env.log" && fail "sockets ok: go test got the skip variable"

: >"$work/env.log"
FAKE_GO_ENV_LOG="$work/env.log" FAKE_GO_SKIP="TestSockA TestSockB" TEST_CHANGED_UNIX_SOCKETS=blocked run_check
[ "$status" = 0 ] || fail "sockets blocked: skipped tests changed the exit code to $status: $(cat "$work/out")"
grep -qx 1 "$work/env.log" || fail "sockets blocked: go test did not get the skip variable: $(cat "$work/env.log")"
grep -qx unset "$work/env.log" && fail "sockets blocked: a go test run had no skip variable"
# Every run lists the same two tests, so they are counted once per package.
grep -Eq 'unix sockets are blocked in this sandbox: [0-9]+ socket tests skipped; the merge queue CI runs them \(decision 0164\)' "$work/out" ||
  fail "sockets blocked: no summary line: $(cat "$work/out")"
grep -q 'skipped: ./internal/fake TestSockA' "$work/out" || fail "sockets blocked: the skipped tests are not listed: $(cat "$work/out")"

# A real failure is still a failure when sockets are blocked.
FAKE_GO_PANIC=1 FAKE_GO_SKIP=TestSockA TEST_CHANGED_UNIX_SOCKETS=blocked run_check
[ "$status" != 0 ] || fail "sockets blocked: a failing run exited 0"
[ "$(tail -n 1 "$work/out")" != "${work}" ] && grep -q 'FAIL \[' "$work/out" || fail "sockets blocked: the failure is missing: $(cat "$work/out")"

# Without the override the script runs the real probe, and its answer matches
# the helper run by hand.
if (cd "$root" && "$real_go" run ./scripts/testchanged unix-socket >/dev/null 2>&1); then
  want=0
else
  want=1
fi
run_check
got=0
grep -q 'unix sockets are blocked' "$work/out" && got=1
[ "$got" = "$want" ] || fail "the real probe disagrees with the helper (blocked line: $got, helper blocked: $want)"

# The Go temp folder rule (scripts/gotmpdir.sh) reads a fake stat and a fake
# /dev/shm root in the temp folder, so the real /dev/shm is never touched.
statbin=$work/statbin
mkdir "$statbin"
cat >"$statbin/stat" <<'STAT'
#!/bin/sh
[ -z "${FAKE_STAT_FAIL:-}" ] || exit 1
printf '%s\n' "$FAKE_STAT_OUT"
STAT
chmod +x "$statbin/stat"
shm_root=$work/shm
mkdir "$shm_root"
# A file where the RAM folder should be makes its mkdir fail.
busy_root=$work/busy
mkdir "$busy_root"
: >"$busy_root/agent-net-gotmp"
gotmp_for() {
  env -i PATH="$statbin:/usr/bin:/bin" GOTMP_SHM_ROOT="$2" FAKE_STAT_OUT="$1" FAKE_STAT_FAIL="${3:-}" \
    sh -c '. "$1"; gotmp_default' sh "$root/scripts/gotmpdir.sh"
}
for tc in "tmpfs 4096 2097152|$shm_root/agent-net-gotmp|8 GiB tmpfs" "tmpfs 4096 1048576|$shm_root/agent-net-gotmp|exactly 4 GiB tmpfs" \
  "tmpfs 4096 1048575|/tmp/agent-net-gotmp|just under 4 GiB tmpfs" "ext4 4096 2097152|/tmp/agent-net-gotmp|8 GiB disk"; do
  out=${tc%%|*}
  rest=${tc#*|}
  want=${rest%%|*}
  name=${rest#*|}
  got=$(gotmp_for "$out" "$shm_root") || fail "gotmp_default failed for $name"
  [ "$got" = "$want" ] || fail "Go temp folder for $name is $got, want $want"
done
[ -d "$shm_root/agent-net-gotmp" ] || fail "the RAM folder was not made"
got=$(gotmp_for "tmpfs 4096 2097152" "$busy_root") || fail "gotmp_default failed when the RAM folder cannot be made"
[ "$got" = /tmp/agent-net-gotmp ] || fail "Go temp folder when the RAM folder cannot be made is $got, want /tmp/agent-net-gotmp"
got=$(gotmp_for "" "$shm_root" 1) || fail "gotmp_default failed when stat fails"
[ "$got" = /tmp/agent-net-gotmp ] || fail "Go temp folder when stat fails is $got, want /tmp/agent-net-gotmp"

# The TMPDIR rule (tmp_default) uses the same room test as the Go temp folder,
# and prints nothing when the RAM folder is not usable.
tmp_for() {
  env -i PATH="$statbin:/usr/bin:/bin" GOTMP_SHM_ROOT="$2" FAKE_STAT_OUT="$1" FAKE_STAT_FAIL="${3:-}" \
    sh -c '. "$1"; tmp_default' sh "$root/scripts/gotmpdir.sh"
}
: >"$busy_root/agent-net-tmp"
for tc in "tmpfs 4096 2097152|$shm_root/agent-net-tmp|8 GiB tmpfs" "tmpfs 4096 1048576|$shm_root/agent-net-tmp|exactly 4 GiB tmpfs"; do
  out=${tc%%|*}
  rest=${tc#*|}
  want=${rest%%|*}
  name=${rest#*|}
  got=$(tmp_for "$out" "$shm_root") || fail "tmp_default failed for $name"
  [ "$got" = "$want" ] || fail "TMPDIR folder for $name is $got, want $want"
done
for tc in "tmpfs 4096 1048575|just under 4 GiB tmpfs" "ext4 4096 2097152|8 GiB disk"; do
  out=${tc%%|*}
  name=${tc#*|}
  if got=$(tmp_for "$out" "$shm_root"); then fail "TMPDIR folder for $name is $got, want none"; fi
done
if got=$(tmp_for "tmpfs 4096 2097152" "$busy_root"); then fail "TMPDIR folder when the RAM folder cannot be made is $got, want none"; fi
if got=$(tmp_for "" "$shm_root" 1); then fail "TMPDIR folder when stat fails is $got, want none"; fi

# test-changed.sh sets TMPDIR to the RAM folder when the caller set none, and keeps
# a TMPDIR the caller set. The fake go logs the TMPDIR and GOTMPDIR of each call.
reset_tree
change_production
: >"$work/go-env.log"
env -u TMPDIR -u GOTMPDIR PATH="$statbin:$work/bin:/usr/bin:/bin" GOTMP_SHM_ROOT="$shm_root" \
  FAKE_STAT_OUT="tmpfs 4096 2097152" FAKE_GO_LOG="$work/go.log" FAKE_GO_TMP_LOG="$work/go-env.log" \
  FAKE_GOFMT_LOG="$work/gofmt.log" TEST_CHANGED_GO="$real_go" \
  sh "$script" HEAD >"$work/out" 2>&1 || fail "a run with the RAM folders failed: $(cat "$work/out")"
want="TMPDIR=$shm_root/agent-net-tmp GOTMPDIR=$shm_root/agent-net-gotmp"
[ "$(sort -u "$work/go-env.log")" = "$want" ] ||
  fail "go test env with no TMPDIR set is $(sort -u "$work/go-env.log"), want $want"
mkdir -p "$work/caller-tmp"
reset_tree
change_production
: >"$work/go-env.log"
env -u GOTMPDIR TMPDIR="$work/caller-tmp" PATH="$statbin:$work/bin:/usr/bin:/bin" GOTMP_SHM_ROOT="$shm_root" \
  FAKE_STAT_OUT="tmpfs 4096 2097152" FAKE_GO_LOG="$work/go.log" FAKE_GO_TMP_LOG="$work/go-env.log" \
  FAKE_GOFMT_LOG="$work/gofmt.log" TEST_CHANGED_GO="$real_go" \
  sh "$script" HEAD >"$work/out" 2>&1 || fail "a run with a caller TMPDIR failed: $(cat "$work/out")"
want="TMPDIR=$work/caller-tmp GOTMPDIR=$shm_root/agent-net-gotmp"
[ "$(sort -u "$work/go-env.log")" = "$want" ] ||
  fail "go test env with a caller TMPDIR is $(sort -u "$work/go-env.log"), want $want"

# With no BASE, the base is the merge-base with origin/main when it exists, so a
# stale local main cannot widen the diff. A local main that is behind
# origin/main, plus the plan's base line, proves which ref was used.
reset_tree
change_production
git add -A
git -c user.name=test -c user.email=test@example.invalid commit -q -m tip
tip=$(git rev-parse HEAD)
git branch -f main HEAD~1
git update-ref refs/remotes/origin/main "$tip"
printf 'third\n' >internal/fake/notes.txt
status=0
PATH="$work/bin:/usr/bin:/bin" FAKE_GO_LOG="$work/go.log" FAKE_GOFMT_LOG="$work/gofmt.log" TEST_CHANGED_GO="$real_go" \
  sh "$script" --plan >"$work/base-plan" 2>&1 || status=$?
[ "$status" = 0 ] || fail "a plan with origin/main exited $status: $(cat "$work/base-plan")"
grep -Fq "test-changed: base $(git rev-parse --short "$tip") (merge-base with origin/main)" "$work/base-plan" ||
  fail "the plan did not name origin/main as the base: $(cat "$work/base-plan")"
grep -Fq 'internal/fake/notes.txt' "$work/base-plan" ||
  fail "the plan did not list the working change: $(cat "$work/base-plan")"

# Without origin/main, the base is the merge-base with local main.
git update-ref -d refs/remotes/origin/main
status=0
PATH="$work/bin:/usr/bin:/bin" FAKE_GO_LOG="$work/go.log" FAKE_GOFMT_LOG="$work/gofmt.log" TEST_CHANGED_GO="$real_go" \
  sh "$script" --plan >"$work/base-plan" 2>&1 || status=$?
[ "$status" = 0 ] || fail "a plan with main exited $status: $(cat "$work/base-plan")"
grep -Fq "test-changed: base $(git rev-parse --short HEAD~1) (merge-base with main)" "$work/base-plan" ||
  fail "the plan did not name main as the base: $(cat "$work/base-plan")"

echo "test-changed-test: ok"
