#!/bin/sh
# scripts/test-changed-test.sh: check how scripts/test-changed.sh calls go test.
#
# A fake go on a minimal PATH records its arguments and can print a timeout
# panic, so no real test runs and the real agent-net is not used.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
script=$root/scripts/test-changed.sh
work=$(mktemp -d "${TMPDIR:-/tmp}/test-changed-test.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM

fail() {
  echo "test-changed-test: FAIL: $*" >&2
  exit 1
}

mkdir "$work/bin"
cat >"$work/bin/go" <<'GO'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_GO_LOG"
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

repo=$work/repo
mkdir -p "$repo/internal/fake"
cd "$repo"
git init -q
printf 'package fake\n' >internal/fake/fake.go
printf 'first\n' >internal/fake/notes.txt
git add . && git -c user.name=test -c user.email=test@example.invalid commit -q -m base
printf 'second\n' >internal/fake/notes.txt

# run_check runs the script against HEAD, with the given extra arguments.
# Its output goes to $work/out and its go test calls to $work/go.log.
run_check() {
  : >"$work/go.log"
  status=0
  PATH="$work/bin:/usr/bin:/bin" FAKE_GO_LOG="$work/go.log" \
    sh "$script" HEAD "$@" >"$work/out" 2>&1 || status=$?
}

# A run gets a timeout that is less than the 30 minute hand-back check budget,
# so go test names a slow test before the daemon kills the whole check.
run_check
secs=$(sed -n 's/^test -count=1 -timeout \([0-9]*\)s \.\/internal\/fake$/\1/p' "$work/go.log")
[ -n "$secs" ] || fail "go test has no -timeout: $(cat "$work/go.log")"
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

# --plan lists the contract set, so a worker can say what will run.
run_plan() {
  status=0
  PATH="$work/bin:/usr/bin:/bin" FAKE_GO_LOG="$work/go.log" \
    sh "$script" HEAD --plan >"$work/plan" 2>&1 || status=$?
}
run_plan
[ "$status" = 0 ] || fail "--plan exited $status: $(cat "$work/plan")"
grep -q '\[contract\].*TestMigrationsAreAppendOnly' "$work/plan" ||
  fail "--plan does not list the contract set: $(cat "$work/plan")"

# Arguments after -- go to go test after the script's own -timeout, so they win.
run_check -- -timeout 5m
grep -q '^test -count=1 -timeout [0-9]*s -timeout 5m \./internal/fake$' "$work/go.log" ||
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

echo "test-changed-test: ok"
