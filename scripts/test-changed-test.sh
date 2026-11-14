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
cat >internal/fake/fake.go <<'EOF'
package fake

// Value is the value.
func Value() int { return 1 }
EOF
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

echo "test-changed-test: ok"