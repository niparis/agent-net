#!/usr/bin/env bash
# Check the shard planner without building or running Go tests.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
script=${TEST_SCRIPT:-$root/scripts/test.sh}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
cat >"$work/bin/go" <<'GO'
#!/usr/bin/env bash
set -eu
if [[ $1 == list ]]; then
  printf '%s\n' github.com/niparis/agent-net/internal/{daemon,gateway,setup,tui} github.com/niparis/agent-net/cmd/agent-net github.com/niparis/agent-net/internal/protocol
  exit 0
fi
if [[ $1 == test && $2 == -race ]]; then
  printf '%s\n' "$*" >>"$SHARD_TEST_RUN_LOG"
  case " $* " in
    *' -json '*)
      printf '%s\n' '{"Action":"output","Package":"example/internal/daemon","Output":"boom\n"}' 'not json'
      ;;
  esac
  [[ -z ${FAKE_GO_FAIL:-} ]] || exit 1
  exit 0
fi
[[ $1 == test && $2 == -list && $3 == '.*' ]]
case "$4" in
  github.com/niparis/agent-net/internal/daemon|github.com/niparis/agent-net/internal/gateway|github.com/niparis/agent-net/internal/setup|github.com/niparis/agent-net/internal/tui|github.com/niparis/agent-net/cmd/agent-net) ;;
  *) echo "unexpected package: $4" >&2; exit 1 ;;
esac
printf '%s\n' "$4" >>"$SHARD_TEST_LOG"
printf '%s\n' TestAlpha TestBeta TestDelta TestGamma
GO
chmod +x "$work/bin/go"
export PATH="$work/bin:$PATH" SHARD_TEST_LOG="$work/log" SHARD_TEST_RUN_LOG="$work/runs"
bash "$script" --shards 2 --check-only >"$work/output"
for name in daemon gateway setup tui agent-net; do
  grep -Fx "Verified 4 $name tests across 2 shards exactly once." "$work/output"
done
[[ $(wc -l <"$work/log") == 5 ]]
for name in setup tui agent-net; do
  bash "$script" --package "$name" --shard-index 1 --shard-count 2 --check-only
done
if bash "$script" --package agent-net --shard-index 2 --shard-count 2 --check-only >"$work/error" 2>&1; then
  echo 'accepted an out-of-range shard' >&2
  exit 1
fi
grep -F 'shard index must be less than shard count' "$work/error"

# All-mode must execute both shards of every selected package and leave only
# the remaining package in its final job.
bash "$script" --shards 2 >"$work/run-output"
[[ $(wc -l <"$work/runs") == 11 ]]
for package in internal/daemon internal/gateway internal/setup internal/tui cmd/agent-net; do
  [[ $(grep -Fc " github.com/niparis/agent-net/$package" "$work/runs") == 2 ]]
done
grep -Fx 'test -race -count=1 github.com/niparis/agent-net/internal/protocol' "$work/runs"

# Two expensive tests in the same old round-robin bucket must be separated.
# A stale timing must not enter the plan, and untimed tests must still run.
cat >"$work/timings.json" <<'TIMINGS'
{"Action":"pass","Package":"example/internal/daemon","Test":"TestAlpha","Elapsed":100}
{"Action":"pass","Package":"example/internal/daemon","Test":"TestDelta","Elapsed":90}
{"Action":"pass","Package":"example/internal/daemon","Test":"TestRemoved","Elapsed":999}
{"Action":"pass","Package":"example/internal/daemon","Test":"TestAlpha/subtest","Elapsed":1000}
{"Action":"fail","Package":"example/internal/daemon","Test":"TestFailed","Elapsed":1000}
TIMINGS
python3 "$root/scripts/test-timings.py" "$work/timings.json" >"$work/timings"
[[ $(awk '!/^#/ { n++ } END { print n }' "$work/timings") == 4 ]]
export TEST_TIMINGS_FILE="$work/timings"
: >"$work/runs"
for shard in 0 1; do
  bash "$script" --package daemon --shard-index "$shard" --shard-count 2 >"$work/weighted-$shard"
done
if grep -E 'Alpha.*Delta|Delta.*Alpha' "$work/runs"; then
  echo 'expensive tests landed in the same shard' >&2
  exit 1
fi
[[ $(grep -c 'TestAlpha' "$work/runs") == 1 ]]
[[ $(grep -c 'TestGamma' "$work/runs") == 1 ]]
[[ $(grep -c 'TestBeta' "$work/runs") == 1 ]]
[[ $(grep -c 'TestDelta' "$work/runs") == 1 ]]
! grep -q TestRemoved "$work/runs"

# --json-output must keep the go test arguments and add only -json, copy the
# JSON to the file, and still show a readable failure.
: >"$work/runs"
bash "$script" --package daemon --shard-index 0 --shard-count 2 --json-output "$work/shard.json" >"$work/json-output"
[[ $(wc -l <"$work/runs") == 1 ]]
grep -Fq ' -count=1 -json -run ' "$work/runs"
[[ $(grep -c '"Action":"output"' "$work/shard.json") == 1 ]]
python3 "$root/scripts/test-timings.py" "$work/shard.json" "$work/timings.json" >/dev/null
if FAKE_GO_FAIL=1 bash "$script" --package daemon --shard-index 0 --shard-count 2 --json-output "$work/failed.json" >"$work/failed-output" 2>&1; then
  echo 'a failing json run passed' >&2
  exit 1
fi
grep -Fx 'boom' "$work/failed-output"
grep -Fx 'not json' "$work/failed-output"
! grep -Fq '"Action"' "$work/failed-output"
[[ -s $work/failed.json ]]
if bash "$script" --shards 2 --json-output "$work/all.json" --check-only >/dev/null 2>&1; then
  echo 'accepted --json-output without a shard index' >&2
  exit 1
fi
